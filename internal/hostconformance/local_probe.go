package hostconformance

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"time"
)

const localProbeCommandTimeout = 5 * time.Second

var runtimeVersionPattern = regexp.MustCompile(`[0-9]+(?:\.[0-9A-Za-z-]+)+`)

type LocalSource interface {
	ReadFile(string) ([]byte, error)
	LookPath(string) (string, error)
	Run(context.Context, string, ...string) ([]byte, error)
}

type LocalProbe struct {
	Source           LocalSource
	Architecture     string
	StorageSourceRef string
	StoragePath      string
}

// amd64MicroarchitectureFlags are the x86-64-v2 feature flags as exposed by
// Linux /proc/cpuinfo. The observation is an intersection across every CPU
// record, so a flag exposed by only one CPU cannot satisfy the requirement.
var amd64MicroarchitectureFlags = []string{"ssse3", "sse4_1", "sse4_2", "popcnt", "cx16", "lahf_lm"}

type osLocalSource struct{}

func (osLocalSource) ReadFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

func (osLocalSource) LookPath(name string) (string, error) {
	return exec.LookPath(name)
}

func (osLocalSource) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	bounded, cancel := context.WithTimeout(ctx, localProbeCommandTimeout)
	defer cancel()
	return exec.CommandContext(bounded, name, args...).CombinedOutput()
}

func (p LocalProbe) Observe(ctx context.Context) (Observation, error) {
	source := p.Source
	if source == nil {
		source = osLocalSource{}
	}
	osRelease, err := source.ReadFile("/etc/os-release")
	if err != nil {
		return Observation{}, fmt.Errorf("read /etc/os-release: %w", err)
	}
	osFacts, err := parseOSRelease(osRelease)
	if err != nil {
		return Observation{}, err
	}
	architecture := normalizeArchitecture(p.Architecture)
	if architecture == "" {
		architecture = normalizeArchitecture(runtime.GOARCH)
	}
	if architecture != "amd64" && architecture != "arm64" {
		return Observation{}, fmt.Errorf("unsupported host architecture %q", architecture)
	}
	microarchitectureLevel := observeAMD64MicroarchitectureLevel(source, architecture)
	kernelOutput, err := source.Run(ctx, "uname", "-r")
	if err != nil {
		return Observation{}, fmt.Errorf("read kernel release: %w", err)
	}
	kernel := strings.TrimSpace(string(kernelOutput))
	if kernel == "" || strings.ContainsAny(kernel, " \t\r\n") {
		return Observation{}, errors.New("kernel release probe returned no canonical token")
	}
	runtimeFacts := detectRuntime(ctx, source)
	virtualization := detectVirtualization(ctx, source)
	initSystem := ObserveInitSystem(ctx, source)

	runtimeStatus := "pass"
	runtimeSummary := "A supported container runtime binary is available"
	if runtimeFacts.Engine == "none" {
		runtimeStatus = "warning"
		runtimeSummary = "No supported container runtime binary was detected"
	}
	return Observation{
		Facts: Facts{
			OS:                          osFacts,
			Architecture:                architecture,
			AMD64MicroarchitectureLevel: microarchitectureLevel,
			KernelRelease:               kernel,
			Runtime:                     runtimeFacts,
			Virtualization:              virtualization,
			InitSystem:                  initSystem,
		},
		Checks: []Check{
			{ID: "host-facts-complete", Category: "host-diagnostic", Status: "pass", Summary: "Required read-only host facts were observed"},
			{ID: "container-runtime", Category: "host-diagnostic", Status: runtimeStatus, Summary: runtimeSummary},
		},
	}, nil
}

// observeAMD64MicroarchitectureLevel returns 0 when the feature source is
// unavailable or incomplete, 1 for a complete amd64 CPU inventory below
// x86-64-v2, and 2 when every observed CPU exposes the x86-64-v2 flags.
// Higher levels are reserved until a safe source measures them.
func observeAMD64MicroarchitectureLevel(source LocalSource, architecture string) int {
	if architecture != "amd64" {
		return 0
	}
	data, err := source.ReadFile("/proc/cpuinfo")
	if err != nil {
		return 0
	}
	records := cpuInfoFlagRecords(data)
	if len(records) == 0 {
		return 0
	}
	for _, flags := range records {
		if len(flags) == 0 {
			return 0
		}
	}
	for _, flags := range records {
		if !hasAMD64MicroarchitectureFlags(flags) {
			return 1
		}
	}
	return 2
}

func cpuInfoFlagRecords(data []byte) []map[string]struct{} {
	var records []map[string]struct{}
	var current map[string]struct{}
	seenProcessor := false
	seenFlags := false
	for _, line := range strings.Split(string(data), "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "processor":
			if current != nil {
				if !seenProcessor || !seenFlags {
					return nil
				}
				records = append(records, current)
			}
			current = map[string]struct{}{}
			seenProcessor, seenFlags = true, false
		case "flags":
			if current == nil || !seenProcessor {
				return nil
			}
			seenFlags = true
			for _, flag := range strings.Fields(value) {
				current[strings.ToLower(flag)] = struct{}{}
			}
		}
	}
	if current == nil || !seenProcessor || !seenFlags {
		return nil
	}
	records = append(records, current)
	return records
}

func hasAMD64MicroarchitectureFlags(flags map[string]struct{}) bool {
	if _, ok := flags["pni"]; !ok {
		if _, ok := flags["sse3"]; !ok {
			return false
		}
	}
	for _, required := range amd64MicroarchitectureFlags {
		if _, ok := flags[required]; !ok {
			return false
		}
	}
	return true
}

func parseOSRelease(data []byte) (OSFacts, error) {
	values := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		values[key] = value
	}
	distribution := strings.ToLower(strings.TrimSpace(values["ID"]))
	version := strings.TrimSpace(values["VERSION_ID"])
	if !contractIDPattern.MatchString(distribution) || version == "" || strings.ContainsAny(version, " \t\r\n") {
		return OSFacts{}, errors.New("/etc/os-release must provide canonical ID and VERSION_ID values")
	}
	return OSFacts{Family: "linux", Distribution: distribution, Version: version}, nil
}

func detectRuntime(ctx context.Context, source LocalSource) RuntimeFacts {
	probes := []struct {
		engine string
		name   string
		args   []string
	}{
		{engine: "docker", name: "docker", args: []string{"--version"}},
		{engine: "podman", name: "podman", args: []string{"--version"}},
		{engine: "containerd", name: "containerd", args: []string{"--version"}},
	}
	for _, probe := range probes {
		if _, err := source.LookPath(probe.name); err != nil {
			continue
		}
		output, err := source.Run(ctx, probe.name, probe.args...)
		version := runtimeVersionPattern.FindString(string(output))
		if err != nil || version == "" {
			version = "unavailable"
		}
		return RuntimeFacts{Engine: probe.engine, Version: version}
	}
	return RuntimeFacts{Engine: "none", Version: "unavailable"}
}

func detectVirtualization(ctx context.Context, source LocalSource) VirtualizationFacts {
	class := "none"
	if _, err := source.LookPath("systemd-detect-virt"); err == nil {
		if output, runErr := source.Run(ctx, "systemd-detect-virt"); len(strings.TrimSpace(string(output))) > 0 {
			class = normalizeVirtualization(strings.TrimSpace(string(output)))
			if runErr != nil && class == "none" {
				class = "bare-metal"
			}
		}
	} else {
		class = fallbackVirtualization(source)
	}
	nested := false
	for _, path := range []string{"/sys/module/kvm_intel/parameters/nested", "/sys/module/kvm_amd/parameters/nested"} {
		if value, err := source.ReadFile(path); err == nil && allowedValue(strings.ToLower(strings.TrimSpace(string(value))), "1", "y", "yes") {
			nested = true
		}
	}
	return VirtualizationFacts{Class: class, Nested: nested}
}

func fallbackVirtualization(source LocalSource) string {
	if _, err := source.ReadFile("/proc/vz/veinfo"); err == nil {
		return "openvz"
	}
	if environ, err := source.ReadFile("/proc/1/environ"); err == nil && strings.Contains(string(environ), "container=lxc") {
		return "lxc"
	}
	return "none"
}

func normalizeArchitecture(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "x86_64", "amd64":
		return "amd64"
	case "aarch64", "arm64":
		return "arm64"
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func normalizeVirtualization(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "qemu":
		return "kvm"
	case "hyper-v":
		return "hyperv"
	case "virtualbox":
		return "oracle"
	case "none":
		return "bare-metal"
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}
