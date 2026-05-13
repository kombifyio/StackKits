// =============================================================================
// STACKKIT: MODERN-HOMELAB - Hybrid Infrastructure Pattern
// =============================================================================
//
// Architecture v4: StackKit + Context + Add-Ons
//
// Architecture Pattern: Hybrid Infrastructure
//   Local nodes (compute, storage, data sovereignty) bridged with
//   cloud nodes (public ingress, management, 24/7 availability)
//   via identity-aware proxies and tunnels -- VPN is optional.
//
// Network Model: Identity-Aware Proxy (not VPN-first)
//   - LLDAP + Step-CA provide auto-certs and mTLS
//   - TinyAuth provides ForwardAuth for all services
//   - Cloudflare Tunnel or Pangolin bypasses CGNAT/DS-Lite
//   - VPN (Headscale/Tailscale) available as optional add-on
//
// PaaS Decision (context-driven):
//   - User has domain + wildcard → Coolify (multi-node, git deploys)
//   - User has no domain          → Dokploy (traefik-me + MagicDNS)
//
// Container Runtime: Docker Compose per node (no Swarm)
//   Coolify/Dokploy coordinates multi-node deployments via SSH.
// =============================================================================

package modern_homelab

import (
	"list"
)

// =============================================================================
// MAIN SCHEMA: #ModernHomelabStack
// =============================================================================

#ModernHomelabStack: {
	// Metadata
	meta: #StackMeta

	// Deployment Mode
	deploymentMode: *"simple" | "advanced"

	// PaaS selection (context-driven)
	paas: *"coolify" | "dokploy"

	// Domain configuration
	domain: #DomainConfig

	// Drift detection (triggers advanced mode)
	driftDetection?: {
		enabled:  bool | *false
		schedule: string | *"0 */6 * * *"
	}

	// Node configuration (minimum 2: 1 cloud + 1 local)
	nodes: [...#HybridNode] & list.MinItems(2)

	// At least one cloud node and one local node
	_cloudNodes: [for n in nodes if n.type == "cloud" {n}]
	_localNodes: [for n in nodes if n.type == "local" {n}]
	_hasCloud: len(_cloudNodes) >= 1
	_hasLocal: len(_localNodes) >= 1

	// Services (core platform services)
	services: #CoreServiceSet

	// Add-ons (composable extensions)
	addons?: #AddonSelection

	// Secrets configuration
	secrets: #SecretsConfig

	// Deployment config (auto-generated)
	_deployment: #DeploymentConfig & {
		if deploymentMode == "simple" {
			mode: "simple"
			day1: {
				engine: "opentofu"
				actions: ["init", "plan", "apply"]
			}
			day2: enabled: false
		}
		if deploymentMode == "advanced" {
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
					drift_detection: true
					change_sets:     true
					rolling_updates: true
					stack_ordering:  true
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
	version: string | *"4.0.0"
}

// =============================================================================
// DOMAIN CONFIGURATION
// =============================================================================

#DomainConfig: {
	// Primary domain (required for Coolify, optional for Dokploy)
	name?: string

	// Can use wildcard SSL (*.domain.com)
	wildcard: bool | *false

	// DNS provider for automatic management
	dnsProvider?: "cloudflare" | "hetzner" | "route53" | "manual"

	// ACME email for Let's Encrypt
	acmeEmail?: string
}

// =============================================================================
// DEPLOYMENT CONFIGURATION
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
			drift_detection: bool
			change_sets:     bool
			rolling_updates: bool
			stack_ordering:  bool
		}
	}
}

// =============================================================================
// NODE DEFINITIONS
// =============================================================================

#HybridNode: {
	id:   string & =~"^[a-z][a-z0-9-]*$"
	name: string & =~"^[a-z][a-z0-9-]*$"

	// Node type determines placement and capabilities
	type: "cloud" | "local"

	// Role in the hybrid architecture
	role: *"worker" | "main"

	// Host address
	host: string

	// Compute resources
	compute: #ComputeResources

	// OS configuration
	os?: #OSConfig

	// Cloud provider (only for cloud nodes)
	if type == "cloud" {
		provider?: #CloudProvider
	}

	// GPU (only for local nodes typically)
	gpu?: #GPUSpec

	// Node labels for placement decisions
	labels?: [string]: string

	enabled: bool | *true
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

#CloudProvider: {
	name:    "hetzner" | "digitalocean" | "vultr" | "linode"
	region?: string
	size?:   string
	image?:  string
}

#GPUSpec: {
	vendor: "nvidia" | "amd" | "intel"
	model?: string
	vramGB?: int
}

// =============================================================================
// CORE SERVICE SET (Platform Layer)
// =============================================================================

#CoreServiceSet: {
	// Always present on cloud node
	traefik: #ServiceToggle & {enabled: true}

	// PaaS (one of)
	coolify?: #ServiceToggle
	dokploy?: #ServiceToggle

	// Identity-aware proxy (default: TinyAuth)
	tinyauth: #ServiceToggle & {enabled: true}

	// OIDC provider (optional)
	pocketid?: #ServiceToggle

	// Log viewer
	dozzle: #ServiceToggle

	// Test service
	whoami?: #ServiceToggle

	// External uptime monitor (cloud node)
	uptimeKuma?: #ServiceToggle
}

#ServiceToggle: {
	enabled: bool | *false
}

// =============================================================================
// ADDON SELECTION
// =============================================================================

#AddonSelection: {
	// Infrastructure add-ons
	tunnel?:      #AddonToggle
	vpnOverlay?:  #AddonToggle
	monitoring?:  #AddonToggle
	backup?:      #AddonToggle
	authelia?:    #AddonToggle

	// Use case add-ons (the 10 homelab scenarios)
	vault?:        #AddonToggle  // Password Manager
	photos?:       #AddonToggle  // Photo Gallery
	media?:        #AddonToggle  // Media Streaming
	fileSharing?:  #AddonToggle  // File Sharing
	smartHome?:    #AddonToggle  // Smart Home
	aiWorkloads?:  #AddonToggle  // AI/LLM
	calendar?:     #AddonToggle  // CalDAV/CardDAV
	mail?:         #AddonToggle  // Mail Server
	devPlatform?:  #AddonToggle  // Dev Environment
	gameserver?:   #AddonToggle  // Game Servers
	remoteDesktop?: #AddonToggle // Virtual PC

	// Constraint: mail add-on includes CalDAV, so calendar is redundant
	if mail != _|_ if mail.enabled {
		calendar?: enabled: false
	}
}

#AddonToggle: {
	enabled: bool | *false
	variant?: string
}

// =============================================================================
// SECRETS CONFIGURATION
// =============================================================================

#SecretsConfig: {
	// Provider for encrypted secrets
	provider: *"sops-age" | "sops-gpg"

	// Age key file location
	ageKeyFile: string | *"/etc/sops/age-key.txt"

	// Encrypted secrets file
	encryptedSecretsFile: string | *"secrets.enc.yaml"
}

// =============================================================================
// PLACEMENT RULES
// =============================================================================
// These rules define WHERE services run in the hybrid topology.

#PlacementRules: {
	// Cloud node: public-facing, management, always-on
	cloud: [
		"traefik",
		"coolify",
		"dokploy",
		"tinyauth",
		"pocketid",
		"uptime-kuma",
		"vaultwarden",
		"grafana",
		"victoriametrics",
		"loki",
		"stalwart",
	]

	// Local node: compute, storage, data sovereignty
	local: [
		"immich",
		"jellyfin",
		"ollama",
		"open-webui",
		"home-assistant",
		"cloudreve",
		"nextcloud",
		"radicale",
		"guacamole",
		"minio",
		"gitea",
	]

	// All nodes: agents and telemetry
	daemonset: [
		"grafana-alloy",
		"cadvisor",
		"node-exporter",
		"restic-agent",
	]
}
