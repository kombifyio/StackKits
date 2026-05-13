package commands

import (
	"os"
	"os/exec"
	"strings"

	"github.com/kombifyio/stackkits/pkg/models"
)

// detectVirtualization detects the virtualization type of the current system.
// Returns "kvm", "openvz", "lxc", "vmware", "hyperv", "xen", or "none" (bare metal).
func detectVirtualization() string {
	// Method 1: systemd-detect-virt (most reliable on systemd systems)
	if out, err := exec.Command("systemd-detect-virt").CombinedOutput(); err == nil {
		virt := strings.TrimSpace(string(out))
		if virt != "" && virt != models.VirtNone {
			return virt
		}
		return models.VirtNone
	}

	// Method 2: Check /proc/vz (OpenVZ indicator)
	if _, err := os.Stat("/proc/vz"); err == nil {
		if _, err := os.Stat("/proc/bc"); err != nil {
			// /proc/vz exists but /proc/bc doesn't → guest (not host)
			return models.VirtOpenVZ
		}
	}

	// Method 3: Check for LXC
	if data, err := os.ReadFile("/proc/1/environ"); err == nil {
		if strings.Contains(string(data), "container=lxc") {
			return models.VirtLXC
		}
	}
	if data, err := os.ReadFile("/proc/1/cgroup"); err == nil {
		if strings.Contains(string(data), "/lxc/") {
			return models.VirtLXC
		}
	}

	// Method 4: Check DMI for KVM/QEMU/VMware
	if data, err := os.ReadFile("/sys/class/dmi/id/product_name"); err == nil {
		product := strings.TrimSpace(strings.ToLower(string(data)))
		switch {
		case strings.Contains(product, "kvm"), strings.Contains(product, "qemu"):
			return models.VirtKVM
		case strings.Contains(product, "vmware"):
			return "vmware"
		case strings.Contains(product, "virtualbox"):
			return "oracle"
		case strings.Contains(product, "hyper-v"):
			return "microsoft"
		}
	}

	// Method 5: Check hypervisor CPUID flag
	if data, err := os.ReadFile("/proc/cpuinfo"); err == nil {
		if strings.Contains(string(data), "hypervisor") {
			return models.VirtKVM
		}
	}

	return models.VirtNone
}

// testUnshare tests whether the unshare(2) syscall is available.
// This is the single most important check — if unshare is blocked, Docker cannot
// create any containers regardless of what else works.
func testUnshare() bool {
	cmd := exec.Command("unshare", "--mount", "--pid", "--fork", "true")
	return cmd.Run() == nil
}

// detectCgroupVersion returns "v2" if the system uses cgroup v2 (unified), else "v1".
func detectCgroupVersion() string {
	if data, err := os.ReadFile("/proc/filesystems"); err == nil {
		if strings.Contains(string(data), "cgroup2") {
			// Check if cgroup2 is actually mounted as the unified hierarchy
			if _, err := os.Stat("/sys/fs/cgroup/cgroup.controllers"); err == nil {
				return "v2"
			}
		}
	}
	return "v1"
}
