// Package system provides host system detection for StackKits.
package system

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// VirtType represents the virtualization technology of the host.
type VirtType string

const (
	VirtNone    VirtType = "none" // bare metal
	VirtKVM     VirtType = "kvm"
	VirtVMware  VirtType = "vmware"
	VirtHyperV  VirtType = "microsoft"
	VirtXen     VirtType = "xen"
	VirtOpenVZ  VirtType = "openvz"
	VirtLXC     VirtType = "lxc"
	VirtDocker  VirtType = "docker"
	VirtWSL     VirtType = "wsl"
	VirtUnknown VirtType = "unknown"
)

// Runtime represents the selected deployment runtime.
type Runtime string

const (
	RuntimeDocker Runtime = "docker"
	RuntimeNative Runtime = "native"
)

// DetectVirt returns the virtualization type of the current host.
// Uses systemd-detect-virt when available, falls back to /proc checks.
func DetectVirt(ctx context.Context) VirtType {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// Try systemd-detect-virt first
	cmd := exec.CommandContext(ctx, "systemd-detect-virt", "--container")
	output, err := cmd.Output()
	if err == nil {
		virt := strings.TrimSpace(string(output))
		if virt != "" && virt != string(VirtNone) {
			return parseVirtType(virt)
		}
	}

	// Try full virt detection (covers VMs too)
	cmd = exec.CommandContext(ctx, "systemd-detect-virt")
	output, err = cmd.Output()
	if err == nil {
		virt := strings.TrimSpace(string(output))
		if virt != "" && virt != string(VirtNone) {
			return parseVirtType(virt)
		}
		return VirtNone
	}

	// Fallback: check /proc indicators
	return detectVirtFromProc()
}

// CanRunContainers tests whether a container runtime can actually create containers.
// This catches OpenVZ/LXC hosts where the daemon starts but containers fail.
func CanRunContainers(ctx context.Context, binary string) bool {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, binary, "run", "--rm", "busybox", "true") // #nosec G204 -- binary is "docker"
	return cmd.Run() == nil
}

// IsContainerBlocked returns true if the virt type is known to block container creation.
func IsContainerBlocked(virt VirtType) bool {
	switch virt {
	case VirtOpenVZ, VirtLXC:
		return true
	default:
		return false
	}
}

func parseVirtType(s string) VirtType {
	switch strings.ToLower(s) {
	case "kvm", "qemu":
		return VirtKVM
	case "vmware":
		return VirtVMware
	case "microsoft":
		return VirtHyperV
	case "xen":
		return VirtXen
	case "openvz":
		return VirtOpenVZ
	case "lxc", "lxc-libvirt":
		return VirtLXC
	case "docker":
		return VirtDocker
	case "wsl":
		return VirtWSL
	case string(VirtNone):
		return VirtNone
	default:
		return VirtUnknown
	}
}

func detectVirtFromProc() VirtType {
	// Check for OpenVZ
	cmd := exec.Command("test", "-f", "/proc/vz/veinfo")
	if cmd.Run() == nil {
		return VirtOpenVZ
	}

	// Check for LXC
	if output, err := exec.Command("cat", "/proc/1/environ").Output(); err == nil {
		if strings.Contains(string(output), "container=lxc") {
			return VirtLXC
		}
	}

	return VirtUnknown
}

// DetectCPUCores returns the number of CPU cores on the host.
func DetectCPUCores() int {
	out, err := exec.Command("nproc").Output()
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0
	}
	return n
}

// DetectMemoryGB returns the total system memory in GB.
func DetectMemoryGB() float64 {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "MemTotal:") {
			var totalKB uint64
			_, _ = fmt.Sscanf(line, "MemTotal: %d kB", &totalKB)
			if totalKB > 0 {
				return float64(totalKB) / 1024 / 1024
			}
		}
	}
	return 0
}
