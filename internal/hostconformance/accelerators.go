package hostconformance

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// Accelerator container access classes recorded on an inventory node.
const (
	AcceleratorAccessCDI             = "cdi"
	AcceleratorAccessROCmDeviceNodes = "rocm-device-nodes"
	AcceleratorAccessNone            = "none"
)

// AcceleratorFacts is one observed GPU vendor on the local node. It carries no
// serial numbers, UUIDs or bus addresses: only what admission compares.
// MinVRAMGiB is the smallest device (0 when unobserved) and DriverVersion is
// empty when no driver answered.
type AcceleratorFacts struct {
	Vendor          string `json:"vendor"`
	Devices         int    `json:"devices"`
	MinVRAMGiB      int    `json:"minVramGiB,omitempty"`
	DriverVersion   string `json:"driverVersion,omitempty"`
	ContainerAccess string `json:"containerAccess"`
}

// DriverMajor returns the leading numeric component of the driver version, or
// 0 when no driver answered.
func (f AcceleratorFacts) DriverMajor() int {
	major, _, _ := strings.Cut(f.DriverVersion, ".")
	value, err := strconv.Atoi(major)
	if err != nil {
		return 0
	}
	return value
}

// pathSource reports whether a device or file node exists without opening it.
// Opening /dev/kfd or a DRM node to probe for it is not a read-only act.
type pathSource interface {
	Exists(string) bool
}

func (osLocalSource) Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// nvidiaCDISpecPaths are the locations nvidia-ctk writes: the static spec of
// `nvidia-ctk cdi generate --output=/etc/cdi/nvidia.yaml` and the spec the
// nvidia-cdi-refresh service keeps under /var/run/cdi.
var nvidiaCDISpecPaths = []string{
	"/etc/cdi/nvidia.yaml", "/etc/cdi/nvidia.json",
	"/var/run/cdi/nvidia.yaml", "/var/run/cdi/nvidia.json",
	"/run/cdi/nvidia.yaml", "/run/cdi/nvidia.json",
}

// Bounded device probes keep observation deterministic without directory
// listings: DRM cards and render nodes are numbered from fixed bases.
const (
	maxProbedGPUs       = 16
	amdPCIVendorID      = "0x1002"
	firstDRMRenderMinor = 128
)

var driverVersionPattern = regexp.MustCompile(`^[0-9][0-9A-Za-z.+-]*$`)

// ObserveAccelerators reports the GPUs containers could use on this node, in
// fixed vendor order (nvidia, amd). A host without GPUs yields an empty,
// non-nil list: "observed none" differs from "not observed".
func ObserveAccelerators(ctx context.Context, source LocalSource) []AcceleratorFacts {
	if source == nil {
		source = osLocalSource{}
	}
	result := []AcceleratorFacts{}
	if nvidia, ok := observeNVIDIA(ctx, source); ok {
		result = append(result, nvidia)
	}
	if amd, ok := observeAMD(source); ok {
		result = append(result, amd)
	}
	return result
}

func observeNVIDIA(ctx context.Context, source LocalSource) (AcceleratorFacts, bool) {
	facts := AcceleratorFacts{Vendor: "nvidia", ContainerAccess: AcceleratorAccessNone}
	answered := false
	if _, err := source.LookPath("nvidia-smi"); err == nil {
		output, runErr := source.Run(ctx, "nvidia-smi", "--query-gpu=memory.total,driver_version", "--format=csv,noheader,nounits")
		if runErr == nil {
			if devices, vram, driver, parseErr := ParseNVIDIASMIQuery(output); parseErr == nil {
				facts.Devices, facts.MinVRAMGiB, facts.DriverVersion = devices, vram, driver
				answered = true
			}
		}
	}
	if !answered {
		// Device nodes without an answering driver: a card is present, but
		// its driver is missing or broken. Admission refuses with guidance.
		facts.Devices = countExisting(source, "/dev/nvidia%d", 0)
		if facts.Devices == 0 {
			return AcceleratorFacts{}, false
		}
	}
	if answered && hasNVIDIACDISpec(source) {
		facts.ContainerAccess = AcceleratorAccessCDI
	}
	return facts, true
}

// ParseNVIDIASMIQuery parses `nvidia-smi --query-gpu=memory.total,driver_version
// --format=csv,noheader,nounits`: one "<MiB>, <driver>" line per GPU.
func ParseNVIDIASMIQuery(output []byte) (devices, minVRAMGiB int, driverVersion string, err error) {
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		memory, driver, found := strings.Cut(line, ",")
		memory, driver = strings.TrimSpace(memory), strings.TrimSpace(driver)
		mib, parseErr := strconv.ParseFloat(memory, 64)
		if !found || parseErr != nil || mib <= 0 || math.IsInf(mib, 0) || !driverVersionPattern.MatchString(driver) {
			return 0, 0, "", fmt.Errorf("unrecognized nvidia-smi line %q", line)
		}
		if driverVersion != "" && driver != driverVersion {
			return 0, 0, "", errors.New("nvidia-smi reports more than one driver version")
		}
		driverVersion = driver
		gib := nominalGiB(mib * 1024 * 1024)
		if devices == 0 || gib < minVRAMGiB {
			minVRAMGiB = gib
		}
		devices++
	}
	if devices == 0 {
		return 0, 0, "", errors.New("nvidia-smi reported no GPU")
	}
	return devices, minVRAMGiB, driverVersion, nil
}

func hasNVIDIACDISpec(source LocalSource) bool {
	for _, path := range nvidiaCDISpecPaths {
		data, err := source.ReadFile(path)
		if err == nil && strings.Contains(string(data), "nvidia.com/gpu") {
			return true
		}
	}
	return false
}

func observeAMD(source LocalSource) (AcceleratorFacts, bool) {
	facts := AcceleratorFacts{Vendor: "amd", ContainerAccess: AcceleratorAccessNone}
	for card := 0; card < maxProbedGPUs; card++ {
		device := fmt.Sprintf("/sys/class/drm/card%d/device/", card)
		vendor, err := source.ReadFile(device + "vendor")
		if err != nil || strings.TrimSpace(string(vendor)) != amdPCIVendorID {
			continue
		}
		facts.Devices++
		raw, err := source.ReadFile(device + "mem_info_vram_total")
		if err != nil {
			continue
		}
		bytes, err := strconv.ParseFloat(strings.TrimSpace(string(raw)), 64)
		if err != nil || bytes <= 0 {
			continue
		}
		if gib := nominalGiB(bytes); facts.MinVRAMGiB == 0 || gib < facts.MinVRAMGiB {
			facts.MinVRAMGiB = gib
		}
	}
	if facts.Devices == 0 {
		return AcceleratorFacts{}, false
	}
	if version, err := source.ReadFile("/sys/module/amdgpu/version"); err == nil && driverVersionPattern.MatchString(strings.TrimSpace(string(version))) {
		facts.DriverVersion = strings.TrimSpace(string(version))
	}
	if exists(source, "/dev/kfd") && countExisting(source, "/dev/dri/renderD%d", firstDRMRenderMinor) > 0 {
		facts.ContainerAccess = AcceleratorAccessROCmDeviceNodes
	}
	return facts, true
}

func exists(source LocalSource, path string) bool {
	if paths, ok := source.(pathSource); ok {
		return paths.Exists(path)
	}
	return false
}

// countExisting counts consecutive device nodes from base (nvidia0, nvidia1,
// ... or renderD128, renderD129, ...).
func countExisting(source LocalSource, pattern string, base int) int {
	count := 0
	for index := base; index < base+maxProbedGPUs; index++ {
		if !exists(source, fmt.Sprintf(pattern, index)) {
			break
		}
		count++
	}
	return count
}

// nominalGiB rounds device memory to the nearest GiB, so an 8 GB card that
// reports 8188 MiB is an 8 GiB card, never 7.
func nominalGiB(bytes float64) int {
	gib := int(math.Round(bytes / bytesPerGiB))
	if gib < 1 {
		return 1
	}
	return gib
}

func validateAcceleratorFacts(accelerators []AcceleratorFacts) error {
	seen := map[string]bool{}
	for _, accelerator := range accelerators {
		if accelerator.Vendor != "nvidia" && accelerator.Vendor != "amd" {
			return fmt.Errorf("accelerator vendor %q is unsupported", accelerator.Vendor)
		}
		if seen[accelerator.Vendor] {
			return fmt.Errorf("accelerator vendor %q is reported twice", accelerator.Vendor)
		}
		seen[accelerator.Vendor] = true
		if accelerator.Devices < 0 || accelerator.MinVRAMGiB < 0 {
			return errors.New("accelerator device count and VRAM must not be negative")
		}
		if accelerator.DriverVersion != "" && !driverVersionPattern.MatchString(accelerator.DriverVersion) {
			return errors.New("accelerator driver version is malformed")
		}
		if !allowedValue(accelerator.ContainerAccess, AcceleratorAccessCDI, AcceleratorAccessROCmDeviceNodes, AcceleratorAccessNone) {
			return fmt.Errorf("accelerator container access %q is invalid", accelerator.ContainerAccess)
		}
	}
	return nil
}

// acceleratorsDocument is the inventory projection of observed accelerators.
func acceleratorsDocument(accelerators []AcceleratorFacts) []any {
	result := make([]any, 0, len(accelerators))
	for _, accelerator := range accelerators {
		entry := map[string]any{
			"vendor": accelerator.Vendor, "devices": accelerator.Devices, "containerAccess": accelerator.ContainerAccess,
		}
		if accelerator.MinVRAMGiB > 0 {
			entry["minVramGiB"] = accelerator.MinVRAMGiB
		}
		if accelerator.DriverVersion != "" {
			entry["driverVersion"] = accelerator.DriverVersion
		}
		result = append(result, entry)
	}
	return result
}
