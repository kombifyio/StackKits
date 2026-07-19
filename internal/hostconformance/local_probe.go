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
	Source       LocalSource
	Architecture string
}

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

	runtimeStatus := "pass"
	runtimeSummary := "A supported container runtime binary is available"
	if runtimeFacts.Engine == "none" {
		runtimeStatus = "warning"
		runtimeSummary = "No supported container runtime binary was detected"
	}
	return Observation{
		Facts: Facts{
			OS:             osFacts,
			Architecture:   architecture,
			KernelRelease:  kernel,
			Runtime:        runtimeFacts,
			Virtualization: virtualization,
		},
		Checks: []Check{
			{ID: "host-facts-complete", Category: "host-diagnostic", Status: "pass", Summary: "Required read-only host facts were observed"},
			{ID: "container-runtime", Category: "host-diagnostic", Status: runtimeStatus, Summary: runtimeSummary},
		},
	}, nil
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
