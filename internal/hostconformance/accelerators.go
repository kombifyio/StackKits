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
	// WSL marks a GPU that WSL2 paravirtualizes from Windows (/dev/dxg): its
	// driver is the Windows NVIDIA driver, and a Linux NVIDIA driver must
	// never be installed inside the distribution.
	WSL bool `json:"wsl,omitempty"`
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
	nvidiaPCIVendor     = "10de"
	firstDRMRenderMinor = 128
)

// WSL2 exposes a Windows GPU by paravirtualization: no NVIDIA device is on the
// PCI bus, the DirectX device node stands in for it, and the Windows driver
// mounts nvidia-smi and libcuda under /usr/lib/wsl/lib. sudo's secure_path
// drops that directory, so the tool is resolved by its fixed path too.
const (
	WSLGPUDevice     = "/dev/dxg"
	WSLNVIDIASMIPath = "/usr/lib/wsl/lib/nvidia-smi"
)

var driverVersionPattern = regexp.MustCompile(`^[0-9][0-9A-Za-z.+-]*$`)

// OnWSL reports whether this Linux host is a WSL2 distribution, or a Hyper-V
// guest whose GPU Windows paravirtualizes (/dev/dxg). Either way the GPU
// driver belongs to Windows. WSL without GPU paravirtualization
// (gpuSupport=false) still counts: a Linux NVIDIA driver must not be
// installed there either. A nil source observes the local host.
func OnWSL(source LocalSource) bool {
	if source == nil {
		source = osLocalSource{}
	}
	if exists(source, WSLGPUDevice) || exists(source, "/proc/sys/fs/binfmt_misc/WSLInterop") {
		return true
	}
	release, err := source.ReadFile("/proc/sys/kernel/osrelease")
	return err == nil && strings.Contains(strings.ToLower(string(release)), "microsoft")
}

// NVIDIASMICommand returns how to invoke nvidia-smi on this host: from PATH,
// else from the WSL driver mount. ok is false when neither exists.
func NVIDIASMICommand(source LocalSource) (string, bool) {
	if source == nil {
		source = osLocalSource{}
	}
	if _, err := source.LookPath("nvidia-smi"); err == nil {
		return "nvidia-smi", true
	}
	if OnWSL(source) && exists(source, WSLNVIDIASMIPath) {
		return WSLNVIDIASMIPath, true
	}
	return "", false
}

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
	facts := AcceleratorFacts{Vendor: "nvidia", ContainerAccess: AcceleratorAccessNone, WSL: OnWSL(source)}
	answered, smiFailed := false, false
	if smi, found := NVIDIASMICommand(source); found {
		output, runErr := source.Run(ctx, smi, "--query-gpu=memory.total,driver_version", "--format=csv,noheader,nounits")
		smiFailed = runErr != nil
		if runErr == nil {
			if devices, vram, driver, parseErr := ParseNVIDIASMIQuery(output); parseErr == nil {
				facts.Devices, facts.MinVRAMGiB, facts.DriverVersion = devices, vram, driver
				answered = true
			}
		}
	}
	if !answered && facts.WSL {
		// WSL has no NVIDIA PCI device or /dev/nvidia* node to count. With
		// GPU paravirtualization on, the Windows NVIDIA driver's own
		// nvidia-smi failing is one GPU whose driver must be repaired on
		// Windows. Without /dev/dxg, without that nvidia-smi, or with output
		// that is no GPU list, this is not (yet) an NVIDIA GPU.
		if !exists(source, WSLGPUDevice) || !exists(source, WSLNVIDIASMIPath) || !smiFailed {
			return AcceleratorFacts{}, false
		}
		facts.Devices = 1
	} else if !answered {
		// A card without an answering driver: device nodes of a broken
		// driver, or, with no NVIDIA driver loaded at all, the PCI bus itself.
		// Admission refuses with the driver resolution instead of reporting
		// the GPU as unobserved.
		facts.Devices = countExisting(source, "/dev/nvidia%d", 0)
		if facts.Devices == 0 {
			facts.Devices = countNVIDIAPCIDisplayDevices(ctx, source)
		}
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

// countNVIDIAPCIDisplayDevices counts NVIDIA display controllers on the PCI
// bus, which needs no NVIDIA driver: lspci first, then the DRM cards a
// fallback driver such as nouveau exposes.
func countNVIDIAPCIDisplayDevices(ctx context.Context, source LocalSource) int {
	if _, err := source.LookPath("lspci"); err == nil {
		if output, runErr := source.Run(ctx, "lspci", "-mm", "-n", "-d", nvidiaPCIVendor+":"); runErr == nil {
			return ParseLSPCIDisplayDevices(output)
		}
	}
	count := 0
	for card := 0; card < maxProbedGPUs; card++ {
		vendor, err := source.ReadFile(fmt.Sprintf("/sys/class/drm/card%d/device/vendor", card))
		if err == nil && strings.TrimSpace(string(vendor)) == "0x"+nvidiaPCIVendor {
			count++
		}
	}
	return count
}

// ParseLSPCIDisplayDevices counts display controllers (PCI class 03xx: VGA or
// 3D controller) in `lspci -mm -n` output, whose lines read
// `01:00.0 "0300" "10de" "2504" -ra1 "1458" "4096"`. The audio function of
// the same card (class 0403) is not a GPU.
func ParseLSPCIDisplayDevices(output []byte) int {
	count := 0
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && strings.HasPrefix(strings.Trim(fields[1], `"`), "03") {
			count++
		}
	}
	return count
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
