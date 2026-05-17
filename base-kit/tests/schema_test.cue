// =============================================================================
// Base Kit CUE Tests v3.0
// =============================================================================
// Validates the schema definitions and constraints for base-kit StackKit.
// 
// Tests:
//   - Default Variant (Coolify + Uptime Kuma)
//   - Beszel Variant (Dokploy + Beszel)
//   - Minimal Variant (Dockge + Portainer + Netdata)
//   - Deployment Modes (simple/advanced)
//   - Compute Tiers (high/standard/low)
// =============================================================================

package tests

import (
	homelab "github.com/kombifyio/stackkits/base-kit"
)

// =============================================================================
// DEFAULT VARIANT TESTS (Coolify + Uptime Kuma)
// =============================================================================

// Test: Default Variant - minimal valid configuration
_validDefaultVariant: homelab.#BaseKitStack & {
	meta: {
		name:    "test-homelab"
		version: "3.0.0"
	}
	deploymentMode: "simple"
	variant:        "default"
	nodes: [{
		id:   "node-1"
		name: "test-node"
		host: "192.168.1.100"
		compute: {
			cpuCores:  4
			ramGB:     8
			storageGB: 100
		}
	}]
	network: {
		domain:    "test.local"
		acmeEmail: "admin@test.local"
	}
	// Default Variant Services
	services: {
		traefik:    enabled: true
		dokploy:    enabled: true
		uptimeKuma: enabled: true
		dozzle:     enabled: true
		whoami:     enabled: true
	}
}

// =============================================================================
// BESZEL VARIANT TESTS (Dokploy + Beszel)
// =============================================================================

// Test: Beszel Variant
_validBeszelVariant: homelab.#BaseKitStack & {
	meta: {
		name:    "beszel-homelab"
		version: "3.0.0"
	}
	deploymentMode: "simple"
	variant:        "beszel"
	nodes: [{
		id:   "node-1"
		name: "beszel-node"
		host: "192.168.1.101"
		compute: {
			cpuCores:  4
			ramGB:     8
			storageGB: 100
		}
	}]
	network: {
		domain:    "beszel.local"
		acmeEmail: "admin@beszel.local"
	}
	// Beszel Variant Services
	services: {
		traefik: enabled: true
		dokploy: enabled: true
		beszel:  enabled: true
		dozzle:  enabled: true
		whoami:  enabled: true
	}
}

// =============================================================================
// MINIMAL VARIANT TESTS (Dockge + Portainer + Netdata)
// =============================================================================

// Test: Minimal Variant - classic stack
_validMinimalVariant: homelab.#BaseKitStack & {
	meta: {
		name:    "minimal-homelab"
		version: "3.0.0"
	}
	deploymentMode: "simple"
	variant:        "minimal"
	nodes: [{
		id:   "node-1"
		name: "minimal-node"
		host: "192.168.1.102"
		compute: {
			cpuCores:  2
			ramGB:     4
			storageGB: 64
		}
	}]
	network: {
		domain:    "minimal.local"
		acmeEmail: "admin@minimal.local"
	}
	// Minimal Variant Services (classic stack)
	services: {
		traefik:   enabled: true
		dockge:    enabled: true
		portainer: enabled: true
		netdata:   enabled: true
		dozzle:    enabled: true
	}
}

// =============================================================================
// COMPUTE TIER TESTS
// =============================================================================

// Test: High Compute Configuration
_validHighComputeConfig: homelab.#BaseKitStack & {
	meta: {
		name:    "high-compute-lab"
		version: "3.0.0"
	}
	deploymentMode: "simple"
	variant:        "default"
	computeTier:    "high"
	nodes: [{
		id:   "powerful-server"
		name: "bigbox"
		host: "10.0.0.10"
		compute: {
			cpuCores:  16
			ramGB:     64
			storageGB: 2000
		}
	}]
	network: {
		domain:    "homelab.example.com"
		acmeEmail: "admin@example.com"
	}
	services: {
		traefik:    enabled: true
		dokploy:    enabled: true
		uptimeKuma: enabled: true
		dozzle:     enabled: true
		whoami:     enabled: true
	}
}

// Test: Low Compute with automatic variant switch
_validLowComputeConfig: homelab.#BaseKitStack & {
	meta: {
		name:    "low-compute-lab"
		version: "3.0.0"
	}
	deploymentMode: "simple"
	variant:        "minimal" // Low compute recommends minimal
	computeTier:    "low"
	nodes: [{
		id:   "mini-pc"
		name: "minibox"
		host: "192.168.1.50"
		compute: {
			cpuCores:  2
			ramGB:     4
			storageGB: 64
		}
	}]
	network: {
		domain:    "mini.local"
		acmeEmail: "admin@mini.local"
	}
	services: {
		traefik:   enabled: true
		dockge:    enabled: true
		portainer: enabled: true
		netdata:   enabled: true
		dozzle:    enabled: true
	}
}

// =============================================================================
// DEPLOYMENT MODE TESTS
// =============================================================================

// Test: Simple Mode (OpenTofu-only)
_validSimpleModeConfig: homelab.#BaseKitStack & {
	meta: {
		name:    "simple-mode-lab"
		version: "3.0.0"
	}
	deploymentMode: "simple"
	variant:        "default"
	nodes: [{
		id:   "simple-node"
		name: "simpleserver"
		host: "192.168.1.110"
		compute: {
			cpuCores:  4
			ramGB:     8
			storageGB: 100
		}
	}]
	network: {
		domain:    "simple.local"
		acmeEmail: "admin@simple.local"
	}
	services: {
		traefik:    enabled: true
		dokploy:    enabled: true
		uptimeKuma: enabled: true
		dozzle:     enabled: true
		whoami:     enabled: true
	}
}

// Test: Advanced Mode (Terramate with drift detection)
_validAdvancedModeConfig: homelab.#BaseKitStack & {
	meta: {
		name:    "advanced-mode-lab"
		version: "3.0.0"
	}
	deploymentMode: "advanced"
	driftDetection: {
		enabled:  true
		schedule: "0 */6 * * *"
	}
	variant: "default"
	nodes: [{
		id:   "advanced-node"
		name: "advancedserver"
		host: "192.168.1.111"
		compute: {
			cpuCores:  8
			ramGB:     16
			storageGB: 200
		}
	}]
	network: {
		domain:    "advanced.local"
		acmeEmail: "admin@advanced.local"
	}
	services: {
		traefik:    enabled: true
		dokploy:    enabled: true
		uptimeKuma: enabled: true
		dozzle:     enabled: true
		whoami:     enabled: true
	}
}

// =============================================================================
// NODE VALIDATION TESTS
// =============================================================================

// Test: Node with OS configuration
_validNodeWithOS: homelab.#HomelabNode & {
	id:   "test-node-001"
	name: "testserver"
	host: "192.168.1.100"
	compute: {
		cpuCores:  8
		ramGB:     32
		storageGB: 500
	}
	os: {
		family:  "debian"
		distro:  "ubuntu"
		version: "24.04"
	}
	role: "main"
}

// Test: Minimal valid node
_minimalNode: homelab.#HomelabNode & {
	id:   "minimal"
	name: "mini"
	host: "10.0.0.1"
	compute: {
		cpuCores:  1
		ramGB:     2
		storageGB: 20
	}
}

// Test: BaseKit supports one homelab with main + worker + storage nodes.
_validMultiNodeBaseKit: homelab.#BaseKitStack & {
	meta: name: "multi-node"
	nodes: [
		{
			id:   "main-1"
			name: "main"
			host: "192.168.1.10"
			role: "main"
			compute: {cpuCores: 4, ramGB: 8, storageGB: 100}
		},
		{
			id:   "worker-1"
			name: "worker"
			host: "192.168.1.11"
			role: "worker"
			compute: {cpuCores: 4, ramGB: 8, storageGB: 100}
		},
		{
			id:   "storage-1"
			name: "storage"
			host: "192.168.1.12"
			role: "storage"
			compute: {cpuCores: 4, ramGB: 8, storageGB: 500}
		},
	]
	network: {domain: "stack.home", acmeEmail: "admin@stack.home"}
	services: {
		traefik: enabled: true
		dokploy: enabled: true
		dozzle: enabled: true
	}
}

// =============================================================================
// NETWORK VALIDATION TESTS
// =============================================================================

// Test: Network with custom DNS
_validNetworkWithDNS: homelab.#NetworkConfig & {
	domain:    "custom.example.com"
	acmeEmail: "ssl@example.com"
	subnet:    "10.10.0.0/16"
	dns: {
		servers: ["9.9.9.9", "1.0.0.1"]
	}
}

// Test: Minimal network config
_minimalNetwork: homelab.#NetworkConfig & {
	domain:    "test.local"
	acmeEmail: "admin@test.local"
}

// =============================================================================
// DEPLOYMENT CONFIG TESTS
// =============================================================================

// Test: Simple deployment config
_simpleDeployment: homelab.#DeploymentConfig & {
	mode: "simple"
	day1: {
		engine: "opentofu"
		actions: ["init", "plan", "apply"]
	}
	day2: {
		enabled: false
	}
}

// Test: Advanced deployment config  
_advancedDeployment: homelab.#DeploymentConfig & {
	mode: "advanced"
	day1: {
		engine: "opentofu"
		actions: ["init", "plan", "apply"]
	}
	day2: {
		enabled: true
		engine:  "terramate"
		actions: ["drift", "update", "destroy"]
		features: {
			drift_detection:  true
			change_sets:      true
			rolling_updates:  true
			stack_ordering:   true
		}
	}
}

// =============================================================================
// SERVICE SET TESTS
// =============================================================================

// Test: Default services
_defaultServices: homelab.#ServiceSet & {
	traefik:    enabled: true
	dokploy:    enabled: true
	uptimeKuma: enabled: true
	dozzle:     enabled: true
	whoami:     enabled: true
}

// Test: Minimal services
_minimalServices: homelab.#ServiceSet & {
	traefik:   enabled: true
	dockge:    enabled: true
	portainer: enabled: true
	netdata:   enabled: true
	dozzle:    enabled: true
	whoami:    enabled: false
}
