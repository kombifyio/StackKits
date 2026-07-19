// Package base - Virtualization & VPS Compatibility schemas for Layer 1 Foundation
package base

// =============================================================================
// VIRTUALIZATION STANDARD
// =============================================================================
//
// Every StackKit runs on a target host. That host may be bare metal, a KVM
// virtual machine, an LXC container with nesting, or an OpenVZ container.
// Each of these environments provides a different set of kernel features to
// the userspace — and Docker relies on specific kernel features to work.
//
// This standard defines:
//   1. How to classify the virtualization environment
//   2. What kernel features are required
//   3. Compatibility tiers (full, degraded, incompatible)
//   4. Workarounds that can be applied automatically
//
// This is a Layer 1 Foundation standard because it represents the most
// fundamental constraint: can the host run containers at all?
// =============================================================================

// #VirtualizationType classifies the host's virtualization environment.
// Detected at runtime by `stackkit prepare` and `stackkit compat`.
#VirtualizationType: "kvm" | "openvz" | "lxc" | "vmware" | "hyperv" | "xen" | "oracle" | "microsoft" | "none"

// #CompatibilityTier classifies how well a host supports Docker/StackKits.
//
//   full:         Docker works perfectly, all features available.
//   degraded:     Docker works with automatic workarounds
//                 (vfs storage, host networking, DNS fix).
//   incompatible: Kernel blocks unshare(2) — Docker cannot run at all.
#CompatibilityTier: "full" | "degraded" | "incompatible"

// #KernelFeatures defines the kernel-level features Docker requires.
// Each feature is probed independently by `stackkit prepare`.
#KernelFeatures: {
	// unshare(2) syscall — CRITICAL. If this is false, nothing works.
	// Docker uses unshare to create PID, mount, and network namespaces.
	// Blocked on OpenVZ and restricted LXC containers.
	unshare: bool

	// OverlayFS kernel module — enables efficient copy-on-write storage.
	// When unavailable, Docker falls back to fuse-overlayfs or vfs.
	overlayfs: bool | *true

	// Bridge networking — allows Docker to create isolated bridge networks.
	// When unavailable, Docker uses host networking mode.
	bridge: bool | *true

	// iptables NAT — enables Docker port mapping.
	// When unavailable, Docker runs without iptables management.
	iptablesNAT: bool | *true

	// cgroup version — v1 (legacy) or v2 (unified).
	// Both are supported; v2 is preferred on modern systems.
	cgroupVersion: "v1" | "v2" | *"v2"
}

// #VirtualizationConfig defines the virtualization environment requirements.
// This is part of Layer 1 Foundation — every StackKit must declare what
// virtualization environments it supports and what the minimum requirements are.
#VirtualizationConfig: {
	// Minimum required kernel features
	// Default: unshare MUST be true (non-negotiable for any StackKit)
	requirements: #KernelFeatures & {
		unshare: true // Every StackKit requires unshare — this is non-negotiable
	}

	// Supported virtualization types
	// Default: KVM and bare metal (none) are always supported
	supportedTypes: [...#VirtualizationType] | *["kvm", "none"]

	// Minimum compatibility tier required
	// Default: degraded (allows workarounds, rejects incompatible VPS)
	minimumTier: #CompatibilityTier | *"degraded"

	// Automatic workarounds that can be applied when degraded
	workarounds: #AutoWorkarounds
}

// #AutoWorkarounds defines the automatic workarounds stackkit can apply
// when the host has degraded Docker support.
#AutoWorkarounds: {
	// Fall back to vfs storage driver when overlay2/fuse-overlayfs unavailable.
	// vfs is slower and uses more disk but works everywhere.
	vfsStorageFallback: bool | *true

	// Switch to host networking when bridge creation is blocked.
	// Disables network isolation between containers.
	hostNetworkFallback: bool | *true

	// Inject explicit DNS servers when container DNS resolution fails.
	// Uses 1.1.1.1 and 8.8.8.8 as fallback resolvers.
	dnsFallback: bool | *true

	// Pre-pull images from host network when container DNS is broken.
	// Pulls images before Docker Compose starts, bypassing broken DNS.
	hostPrePull: bool | *true

	// Switch to iptables-legacy when nf_tables backend fails.
	iptablesLegacyFallback: bool | *true
}

// #DetectedEnvironment captures the runtime-detected virtualization state.
// Written to ~/.stackkits/capabilities.json by `stackkit prepare`.
// Read by `stackkit generate` to adapt deployment artifacts.
#DetectedEnvironment: {
	// Detected virtualization type
	virtualizationType: #VirtualizationType

	// Detected compatibility tier
	compatibilityTier: #CompatibilityTier

	// Detected kernel features
	kernelFeatures: #KernelFeatures

	// Workarounds that were applied (empty if tier is "full")
	appliedWorkarounds: [...string] | *[]

	// Docker storage driver in use
	storageDriver: "overlay2" | "fuse-overlayfs" | "vfs" | *"overlay2"

	// Docker network mode in use
	networkMode: "bridge" | "host" | *"bridge"
}
