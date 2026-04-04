// =============================================================================
// STACKKIT: base-kit - Single Server Deployment
// =============================================================================
//
// Version 5.0 - Transitional compatibility schema for the v5 Base Kit model.
//
// Architecture Pattern: Single-Environment
//   All services run in one deployment target (local server or cloud VPS).
//
// Deployment Modes:
//   - simple:   OpenTofu Day-1 only (initial provisioning)
//   - advanced: OpenTofu + Terramate Day-1 + Day-2 (drift, updates, lifecycle)
//
// PaaS Selection (Context-driven, M2):
//   - local context → Dokploy (simpler, port-based)
//   - cloud context → Coolify (more features, git deploys)
//
// Use Cases:
//   - Personal home server or cloud VPS
//   - Development environment
//   - Small self-hosted services
//   - PaaS-style application deployments
//
// Note: This schema currently serves two callers:
//   1. legacy CUE tests that still use variant-era inputs
//   2. the v5 stack-spec surface used by the Go CLI and stackkit.yaml metadata
//
// The migration strategy is additive: expose the v5 fields without dropping
// the legacy compatibility layer until Terraform generation is fully module-driven.
// =============================================================================

package base_kit

import (
	"list"
)

// =============================================================================
// MAIN SCHEMA: #BaseKitStack
// =============================================================================
// This is the primary user-facing schema that tests and users interact with.
// It provides a simplified interface while using the base schemas internally.

#BaseKitStack: {
	// Metadata
	meta: #StackMeta

	// Canonical v5 deployment surface
	mode: *"simple" | "advanced"
	runtime?: *"docker" | "native"
	context?: *"local" | "cloud" | "pi"
	paas?: *"dokploy" | "coolify" | "dockge" | "none"
	addons?: [...string]
	useCases?: [string]: #UseCaseSelection
	domain?: string
	subdomainPrefix?: string
	email?: string
	adminEmail?: string
	tls?: #TLSConfig
	compute: #ComputeConfig
	ssh?: #SSHConfig

	// Legacy compatibility aliases retained during migration
	deploymentMode: mode

	// Variant selection is legacy compatibility only.
	variant: *"default" | "coolify" | "beszel" | "minimal"

	computeTier: compute.tier

	// Drift detection (triggers advanced mode)
	driftDetection?: {
		enabled:  bool | *false
		schedule: string | *"0 */6 * * *"
	}

	// Node configuration (exactly 1 node)
	nodes: [...#HomelabNode] & list.MinItems(1) & list.MaxItems(1)

	// Transitional network shape: accepts both legacy CUE fields and v5 stack-spec fields.
	network: #NetworkConfig

	// Legacy compatibility service set. The generator remains the source of truth
	// until module-driven Terraform selection lands.
	services: #ServiceSet

	// Deployment config (auto-generated based on mode)
	_deployment: #DeploymentConfig & {
		if mode == "simple" {
			mode: "simple"
			day1: {
				engine: "opentofu"
				actions: ["init", "plan", "apply"]
			}
			day2: enabled: false
		}
		if mode == "advanced" {
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
	}
}

// =============================================================================
// METADATA
// =============================================================================

#StackMeta: {
	name:    string & =~"^[a-z][a-z0-9-]*$"
	version: string | *"5.0.0"
}

// =============================================================================
// DEPLOYMENT MODE CONFIGURATION
// =============================================================================

#DeploymentConfig: {
	mode: "simple" | "advanced"

	day1: {
		engine: "opentofu"
		actions: [...string]
	}

	day2: {
		enabled: bool
		engine?: string
		actions?: [...string]
		features?: {
			drift_detection:  bool
			change_sets:      bool
			rolling_updates:  bool
			stack_ordering:   bool
		}
	}
}

// =============================================================================
// NODE DEFINITION
// =============================================================================

#HomelabNode: {
	id:   string & =~"^[a-z][a-z0-9-]*$"
	name: string & =~"^[a-z][a-z0-9-]*$"
	host: string
	ip?:  string

	compute: #ComputeResources

	os?: #OSConfig

	role: *"worker" | "main" | "standalone"
}

#ComputeResources: {
	cpuCores:  int & >=1
	ramGB:     int & >=2
	storageGB: int & >=20
}

#OSConfig: {
	family:  *"debian" | "rhel"
	distro:  *"ubuntu" | "debian" | "rocky" | "alma"
	version: string | *"24.04"
}

// =============================================================================
// NETWORK CONFIGURATION
// =============================================================================

#NetworkConfig: {
	mode?: "local" | "public" | "hybrid" | *"local"
	domain?: string
	acmeEmail?: string

	subnet: string | *"172.20.0.0/16"
	gateway?: string

	dns?: {
		servers: [...string] | *["1.1.1.1", "8.8.8.8"]
	}

	tls?: {
		mode?: "local" | "acme" | "off" | *"local"
	}
}

#ComputeConfig: {
	tier: *"standard" | "high" | "low"
}

#SSHConfig: {
	user?:    string
	port?:    int & >=1 & <=65535
	keyPath?: string
}

#TLSConfig: {
	provider?:  string
	challenge?: *"tls" | "dns"
}

#UseCaseSelection: {
	enabled?: bool | *true
	tool?:    string
}

// =============================================================================
// SERVICE SET (Variant-based)
// =============================================================================

#ServiceSet: {
	// Core services (always present)
	traefik: #ServiceToggle & {enabled: true}
	dozzle:  #ServiceToggle
	whoami:  #ServiceToggle

	// Default variant services
	dokploy?:    #ServiceToggle
	uptimeKuma?: #ServiceToggle

	// Beszel variant services
	beszel?: #ServiceToggle

	// Minimal variant services
	dockge?:    #ServiceToggle
	portainer?: #ServiceToggle
	netdata?:   #ServiceToggle
}

#ServiceToggle: {
	enabled: bool | *false
}

// =============================================================================
// NOTE: #BaseKitKit was removed in v4.0.0 (TD-07)
// All tests and users use #BaseKitStack as the canonical schema.
// Rich layer configs (identity, platform, security, observability) are
// handled by the CLI at generation time, not in the user-facing schema.
// =============================================================================
