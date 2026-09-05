package hostpreflight

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/hostconformance"
)

// probeTimeout bounds every external command. A host that cannot answer a
// read-only probe quickly is itself a finding; preflight must never hang in
// front of an Apply.
const probeTimeout = 10 * time.Second

// osReleaseQuotes are the quote characters os-release values may be wrapped in.
const osReleaseQuotes = "\"" + "\x27"

// ObserveRequest names the paths and ports this Apply will actually use, so the
// probe measures the host the rollout touches rather than a generic machine.
type ObserveRequest struct {
	WorkspacePath string
	RequiredPorts []int
}

// Observe measures the host. It never returns an error for an unobservable
// fact: the fact is recorded as unobserved and the evaluation decides what
// that means under the active policy.
func Observe(ctx context.Context, request ObserveRequest) Facts {
	facts := Facts{
		ObservedAt:   time.Now().UTC(),
		OS:           runtime.GOOS,
		Architecture: runtime.GOARCH,
		CPUCores:     runtime.NumCPU(),
	}
	facts.InitSystem = hostconformance.ObserveInitSystem(ctx, nil)
	if facts.OS != "linux" {
		// Every StackKits Apply target is a Linux host. Measuring this machine
		// instead would describe the wrong device.
		facts.Docker = observeDocker(ctx)
		return facts
	}

	facts.Distribution, facts.OSVersion = observeOSRelease()
	facts.KernelRelease = strings.TrimSpace(runCommand(ctx, "uname", "-r"))
	facts.Virtualization = observeVirtualization(ctx)
	facts.Memory = observeMemory()
	facts.CgroupVersion, facts.MemoryCgroup = observeCgroups()
	facts.NamespacesOK = observeNamespaces(ctx)
	facts.ClockSynced = observeClock(ctx)
	facts.CPUBaseline = observeCPUBaseline(facts.Architecture)
	facts.Docker = observeDocker(ctx)
	facts.Disks = observeDisks(request.WorkspacePath, facts.Docker.RootDir)
	facts.Ports = observePorts(ctx, request.WorkspacePath, request.RequiredPorts)
	return facts
}

func runCommand(ctx context.Context, name string, args ...string) string {
	bounded, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	output, err := exec.CommandContext(bounded, name, args...).Output()
	if err != nil {
		return ""
	}
	return string(output)
}

func observeOSRelease() (distribution, version string) {
	data, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return "", ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		key, value, found := strings.Cut(strings.TrimSpace(line), "=")
		if !found {
			continue
		}
		value = strings.Trim(value, osReleaseQuotes)
		switch key {
		case "ID":
			distribution = value
		case "VERSION_ID":
			version = value
		}
	}
	return distribution, version
}

func observeVirtualization(ctx context.Context) string {
	if detected := strings.TrimSpace(runCommand(ctx, "systemd-detect-virt")); detected != "" {
		return detected
	}
	if _, err := os.Stat("/proc/vz"); err == nil {
		if _, hostErr := os.Stat("/proc/bc"); hostErr != nil {
			return "openvz"
		}
	}
	if data, err := os.ReadFile("/proc/1/environ"); err == nil && strings.Contains(string(data), "container=lxc") {
		return "lxc"
	}
	if data, err := os.ReadFile("/sys/class/dmi/id/product_name"); err == nil {
		product := strings.ToLower(strings.TrimSpace(string(data)))
		for _, candidate := range []struct{ marker, name string }{
			{"kvm", "kvm"}, {"qemu", "kvm"}, {"vmware", "vmware"}, {"virtualbox", "oracle"},
		} {
			if strings.Contains(product, candidate.marker) {
				return candidate.name
			}
		}
	}
	return "none"
}

func observeMemory() MemoryFacts {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return MemoryFacts{}
	}
	facts := MemoryFacts{Observed: true}
	for _, line := range strings.Split(string(data), "\n") {
		key, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		fields := strings.Fields(value)
		if len(fields) == 0 {
			continue
		}
		kb, convErr := strconv.ParseFloat(fields[0], 64)
		if convErr != nil {
			continue
		}
		gb := kb / 1024 / 1024
		switch key {
		case "MemTotal":
			facts.TotalGB = gb
		case "MemAvailable":
			facts.AvailableGB = gb
		case "SwapTotal":
			facts.SwapGB = gb
		}
	}
	return facts
}

func observeCgroups() (version string, memoryController *bool) {
	if data, err := os.ReadFile("/sys/fs/cgroup/cgroup.controllers"); err == nil {
		enabled := false
		for _, controller := range strings.Fields(string(data)) {
			if controller == "memory" {
				enabled = true
				break
			}
		}
		return "v2", &enabled
	}
	if _, err := os.Stat("/sys/fs/cgroup/memory"); err == nil {
		enabled := true
		return "v1", &enabled
	}
	return "", nil
}

func observeNamespaces(ctx context.Context) *bool {
	if _, err := exec.LookPath("unshare"); err != nil {
		// Without the helper the syscall cannot be proven either way.
		return nil
	}
	bounded, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	available := exec.CommandContext(bounded, "unshare", "--mount", "--pid", "--fork", "true").Run() == nil
	return &available
}

func observeClock(ctx context.Context) *bool {
	if _, err := os.Stat("/run/systemd/timesync/synchronized"); err == nil {
		synced := true
		return &synced
	}
	switch strings.TrimSpace(runCommand(ctx, "timedatectl", "show", "-p", "NTPSynchronized", "--value")) {
	case "yes":
		synced := true
		return &synced
	case "no":
		synced := false
		return &synced
	}
	return nil
}

// cpuBaselineFlags is the x86-64-v2 feature set as spelled in /proc/cpuinfo.
// Service images in these kits ship x86-64-v2 builds, so a CPU model without
// the set (a default kvm64 guest, for example) crashes them at runtime.
var cpuBaselineFlags = []string{"cx16", "lahf_lm", "popcnt", "sse4_1", "sse4_2", "ssse3"}

func observeCPUBaseline(architecture string) *bool {
	if architecture != "amd64" {
		return nil
	}
	data, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return nil
	}
	present := map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "flags") {
			continue
		}
		_, value, _ := strings.Cut(line, ":")
		for _, flag := range strings.Fields(value) {
			present[flag] = true
		}
		break
	}
	if len(present) == 0 {
		return nil
	}
	for _, required := range cpuBaselineFlags {
		if !present[required] {
			missing := false
			return &missing
		}
	}
	satisfied := true
	return &satisfied
}

// dockerInfo is the subset of docker info this admission needs.
type dockerInfo struct {
	ServerVersion   string   `json:"ServerVersion"`
	Driver          string   `json:"Driver"`
	CgroupDriver    string   `json:"CgroupDriver"`
	CgroupVersion   string   `json:"CgroupVersion"`
	MemoryLimit     bool     `json:"MemoryLimit"`
	SwapLimit       bool     `json:"SwapLimit"`
	DockerRootDir   string   `json:"DockerRootDir"`
	SecurityOptions []string `json:"SecurityOptions"`
}

func observeDocker(ctx context.Context) DockerFacts {
	facts := DockerFacts{}
	if _, err := exec.LookPath("docker"); err != nil {
		facts.Diagnostic = "docker-not-found"
		return facts
	}
	facts.BinaryPresent = true

	bounded, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	output, err := exec.CommandContext(bounded, "docker", "info", "--format", "{{json .}}").CombinedOutput()
	if err != nil {
		text := strings.TrimSpace(string(output))
		facts.Diagnostic = boundedDiagnostic(text)
		facts.PermissionDenied = strings.Contains(strings.ToLower(text), "permission denied")
		return facts
	}
	var info dockerInfo
	if jsonErr := json.Unmarshal(output, &info); jsonErr != nil {
		facts.Diagnostic = "docker info is not parseable JSON"
		return facts
	}
	facts.DaemonReachable = true
	facts.ServerVersion = info.ServerVersion
	facts.StorageDriver = info.Driver
	facts.CgroupDriver = info.CgroupDriver
	facts.CgroupVersion = info.CgroupVersion
	facts.MemoryLimitSupported = info.MemoryLimit
	facts.SwapLimitSupported = info.SwapLimit
	facts.RootDir = info.DockerRootDir
	for _, option := range info.SecurityOptions {
		if strings.Contains(option, "rootless") {
			facts.Rootless = true
		}
	}
	facts.ComposePluginVersion = strings.TrimSpace(runCommand(ctx, "docker", "compose", "version", "--short"))
	return facts
}

func boundedDiagnostic(value string) string {
	value = strings.TrimSpace(value)
	if index := strings.IndexByte(value, '\n'); index >= 0 {
		value = value[:index]
	}
	if len(value) > 256 {
		value = value[:256] + "..."
	}
	return value
}

func observeDisks(workspace, dockerRoot string) []DiskFact {
	seen := map[string]bool{}
	disks := make([]DiskFact, 0, 2)
	for _, path := range []string{dockerRoot, workspace} {
		path = strings.TrimSpace(path)
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		fact := DiskFact{Path: path}
		if free, ok := freeBytes(path); ok {
			fact.FreeGB = float64(free) / (1 << 30)
			fact.Observed = true
		}
		disks = append(disks, fact)
	}
	return disks
}

func observePorts(ctx context.Context, workspace string, ports []int) []PortFact {
	facts := make([]PortFact, 0, len(ports))
	for _, port := range ports {
		fact := PortFact{Port: port}
		listener, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", port))
		if err != nil {
			fact.InUse = true
			fact.Detail = boundedDiagnostic(err.Error())
			fact.OwnedByCurrentRuntime = currentWorkspaceOwnsPort(ctx, workspace, port)
		} else {
			_ = listener.Close()
		}
		facts = append(facts, fact)
	}
	return facts
}
