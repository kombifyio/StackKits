// Package base - Main StackKit composition schema
package base

// #BaseStackKit is the foundation that all StackKits extend.
// It composes all the base configurations into a single structure.
#BaseStackKit: {
	// StackKit metadata (required)
	metadata: #StackKitMetadata

	// Node context (auto-detected or user-specified via --context flag)
	context?: #NodeContext

	// Install/lifecycle mode (bare/bootstrapped/advanced; legacy simple accepted by Go normalization)
	deploymentMode?: string

	// Placement axis (Stack-Default intent). Per-unit override at #Module.
	// Optional, so existing specs stay valid (non-breaking). See base/placement.cue.
	placementMode?: #PlacementIntent

	// System configuration (host-level)
	system: #SystemConfig

	// Base package set (system tooling)
	packages: #BasePackages

	// User accounts
	users: #SystemUsers

	// Container runtime
	container: #ContainerRuntime

	// Security settings
	security: {
		ssh:       #SSHHardening
		firewall:  #FirewallPolicy
		container: #ContainerSecurityContext
		secrets:   #SecretsPolicy
		tls:       #TLSPolicy
		audit:     #AuditConfig
	}

	// Virtualization environment requirements (Layer 1 Foundation)
	virtualization: #VirtualizationConfig

	// Identity services (Layer 1 Foundation)
	identity: {
		// Lightweight LDAP server
		lldap: #LLDAPConfig

		// Certificate Authority
		stepCA: #StepCAConfig

		// Identity provider configuration (for zero-trust)
		provider?: #IdentityProvider

		// PKI configuration
		pki?: #PKIConfig

		// RBAC policy
		rbac?: #RBACPolicy
	}

	// Network configuration
	network: {
		defaults: #NetworkDefaults
		dns:      #DNSConfig
		ntp:      #NTPConfig
		vpn:      #VPNConfig
		proxy:    #ProxyConfig
	}

	// Observability
	observability: {
		monitoring: #MonitoringConfig
		logging:    #LoggingConfig
		health:     #HealthCheck
		metrics:    #MetricsConfig
		alerting:   #AlertingConfig
		backup:     #BackupConfig
	}

	// Service definitions — named map keyed by service name
	// Enables `services.traefik.enabled` access pattern.
	// Aligns with #Layer3Applications and Go StackSpec.Services (map[string]any)
	services: [string]: #ServiceDefinition

	// Node definitions (to be provided by user spec)
	nodes: [...#NodeDefinition]

	// Add-ons (composable capability extensions)
	addons?: [string]: _

	// Output URLs and documentation (optional)
	outputs?: _

	// Allow extensions by platforms and StackKits
	...
}

// #StackKitMetadata provides information about the StackKit
#StackKitMetadata: {
	// StackKit identifier (lowercase, hyphenated)
	name: =~"^[a-z][a-z0-9-]+$"

	// Display name for UI
	displayName: string

	// Semantic version
	version: =~"^[0-9]+\\.[0-9]+\\.[0-9]+(-[a-z0-9.]+)?$"

	// Short description (one line)
	description: string

	// Category for grouping (e.g., "development", "production")
	category?: string

	// Long description (Markdown, optional)
	longDescription?: string

	// Author information
	author?: string

	// License identifier
	license: string | *"Apache-2.0"

	// Homepage URL
	homepage?: string

	// Minimum KombiStack version required
	minKombiStackVersion: string | *"1.0.0"

	// Tags for categorization
	tags: [...string] | *[]

	// Deprecated flag
	deprecated: bool | *false

	// Deprecation message (if deprecated)
	deprecationMessage?: string
}

// #ServiceDefinition defines a deployable service
#ServiceDefinition: {
	// Service identifier (DNS-compatible)
	name: =~"^[a-z][a-z0-9-]+$"

	// Display name
	displayName?: string

	// Service category (for grouping/filtering)
	category?: string

	// Whether this service is required
	required?: bool

	// Service implementation status
	status?: "implemented" | "planned" | "beta" | "deprecated"

	// Placement constraints
	placement?: {
		nodeType?: string
		strategy?: string
		...
	}

	// Service type
	type: #ServiceType

	// Container image
	image: string

	// Image tag. Deliberately has no default: every service must pin its
	// tag explicitly. A silent *"latest" default contradicts deterministic
	// Day-1 deployments (Golden Rules §7.1) and caused real pre-pull
	// failures in release gates.
	tag: string

	// Upstream watch coordinates (ADR-0028). Authoring SSOT for the Admin
	// tool_release_watch job; synced to sk_tool_watch by `stackkit module
	// release`. Watch metadata only — deliberately NOT part of the module
	// contract hash, so watch-config changes never force a version bump.
	upstream?: {
		// GitHub source for releases and GHSA advisories, e.g. "traefik/traefik".
		github?: {
			repo: =~"^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$"
		}

		// Container registry to watch for tags/digests. Defaults to the
		// service image when omitted.
		registry?: {
			image: string
		}

		// Auto-bump aggressiveness for update candidates.
		track: "patch" | *"minor" | "major"

		// Variant line to stay within for non-plain-semver tags
		// (e.g. "16-alpine", "apache"). Required whenever the pinned tag
		// carries a variant suffix.
		pinLine?: string

		// OSV.dev coordinates (secondary advisory source).
		osv?: {
			ecosystem: string
			name:      string
		}
	}

	// Service description
	description?: string

	// Service dependencies (other service names)
	needs: [...string] | *[]

	// Target node (optional, defaults to "main")
	node?: string

	// Network configuration
	network: #ServiceNetworkConfig

	// Health check (overrides default)
	healthCheck?: #HealthCheck

	// Resource limits
	resources?: #ResourceLimits

	// Security context (overrides default)
	securityContext?: #ContainerSecurityContext

	// Access policy. Required for every Traefik-routed service: no
	// exposure without an explicit policy (Golden Rules §4.1/§4.8).
	accessPolicy?: #AccessPolicy
	if network.traefik != _|_ {
		if network.traefik.enabled {
			accessPolicy: #AccessPolicy
		}
	}

	// Environment variables
	environment?: [string]: string

	// Environment from secrets
	environmentSecrets?: [string]: =~"^secret://"

	// Volume mounts
	volumes?: [...#VolumeMount]

	// Service-specific configuration (varies by service)
	config?: [string]: _

	// Restart policy
	restartPolicy: "always" | "unless-stopped" | "on-failure" | "no" | *"unless-stopped"

	// Logging override
	logging?: #LoggingConfig

	// Labels for service discovery
	labels?: [string]: string

	// Whether service is enabled
	enabled: bool | *true

	// Output URLs and access information
	// url is optional — internal services (databases, caches) may omit it
	output?: {
		url?:        string
		description: string
		credentials?: {
			defaultUser?: string
			note:         string
		}
	}

	// Subdomain routing configuration for domain computation.
	// key = Terraform local.domains map key.
	// nested = subdomain for own-domain mode ({nested}.{domain}).
	// flat = subdomain suffix for kombify.me mode ({prefix}-{flat}.{domain}).
	subdomain?: {
		key:    =~"^[a-z][a-z0-9_]*$"
		nested: =~"^[a-z][a-z0-9-]*$"
		flat:   =~"^[a-z][a-z0-9-]*$"
	}

	// Dashboard card configuration for the generated homelab dashboard.
	dashboard?: {
		// HTML entity for the card icon (e.g., "&#128100;")
		icon: string
		// Display order within section (lower = first)
		order: int & >=0
		// Dashboard section grouping
		section: "Platform" | "Applications"
		// Layer badge label (e.g., "L1 · IdP")
		badge: string
		// Terraform enable variable name. Omit for always-shown services.
		enableVar?: =~"^enable_[a-z_]+$"
		// Public Mintlify guide for the service row in the generated Node Hub.
		guideUrl?: =~"^https://docs[.]kombify[.]io/.+"
	}

	// Allow additional custom fields for service-specific extensions
	...
}

// #ServiceType categorizes services (comprehensive homelab taxonomy)
#ServiceType:
	// Infrastructure
	"reverse-proxy" | "load-balancer" | "ingress" | "vpn" | "vpn-client" | "dns" | "infrastructure" |
	// Platform
	"paas" | "container-manager" | "compose-manager" | "cluster" |
	// Identity & Security
	"auth" | "directory" | "pki" | "security" |
	// Data
	"database" | "cache" | "storage" | "block-storage" | "distributed-storage" |
	// Application Tiers
	"backend" | "frontend" | "application" | "api" |
	// Observability
	"monitoring" | "metrics" | "metrics-aggregation" | "dashboards" | "dashboard" |
	"logging" | "logs" | "log-shipper" | "uptime" | "observability" | "alerting" |
	// DevOps
	"ci-cd" | "gitops" | "registry" | "backup" | "disaster-recovery" | "automation" |
	// Management
	"management" |
	// Specialized
	"media" | "photos" | "vault" | "object-storage" | "test" | "custom"

// #ServiceNetworkConfig defines service networking
#ServiceNetworkConfig: {
	// Port mappings
	ports?: [...#PortMapping]

	// Traefik integration
	traefik?: {
		enabled: bool | *false
		rule?:   string
		tls?:    bool | *true
		port?:   int // Target port for Traefik
		middlewares?: [...string]
	}

	// Network mode
	mode: "bridge" | "host" | "none" | *"bridge"

	// Networks to join
	networks?: [...string]
}

// #PortMapping defines a port mapping
#PortMapping: {
	host?:        uint16 & >0 & <=65535
	container:    uint16 & >0 & <=65535
	protocol:     "tcp" | "udp" | *"tcp"
	description?: string // Optional description for documentation
}

// #ResourceLimits defines container resource constraints
#ResourceLimits: {
	// Memory limit (e.g., "512m", "2g")
	memory?: string

	// Memory maximum (alias)
	memoryMax?: string

	// Memory reservation
	memoryReservation?: string

	// CPU limit (e.g., 0.5, 2.0)
	cpus?: number

	// CPU shares
	cpuShares?: int

	// Storage limit
	storage?: string
}

// #VolumeMount defines a volume mount
#VolumeMount: {
	// Source (host path or volume name)
	source: string

	// Container path
	target: string

	// Volume type
	type: "bind" | "volume" | "tmpfs" | *"volume"

	// Description for documentation
	description?: string

	// Read-only mount
	readOnly: bool | *false

	// Backup this volume
	backup: bool | *true

	// Volume driver options
	driverOpts?: [string]: string
}

// #NodeDefinition defines a managed node
#NodeDefinition: {
	// Node identifier
	name: =~"^[a-z][a-z0-9-]+$"

	// Display name
	displayName?: string

	// Node role (canonical multi-node vocabulary, see #ClusterNodeRole)
	role: #ClusterNodeRole | *"main"

	// Node type
	type: "local" | "cloud" | "hybrid" | *"local"

	// Operating system
	os: #SupportedOS

	// Cloud provider (if cloud type)
	provider?: #Provider

	// SSH configuration
	ssh?: #SSHConfig

	// Node resources
	resources: #NodeResources

	// Node labels
	labels?: [string]: string

	// Node tags
	tags?: [...string]

	// Whether node is enabled
	enabled: bool | *true
}

// #SupportedOS lists supported operating systems
#SupportedOS: "ubuntu-24" | "ubuntu-22" | "debian-12" | "debian-11" |
	"rocky-9" | "alma-9" | "raspbian-12"

// #Provider lists supported cloud providers
#Provider: "local" | "hetzner" | "docker" | "proxmox" | "aws" | "gcp" | "azure" | "digitalocean"

// #SSHConfig defines SSH connection parameters
#SSHConfig: {
	// Target host (IP or hostname)
	host: string

	// SSH port
	port: uint16 & >0 & <=65535 | *22

	// SSH user
	user: =~"^[a-z_][a-z0-9_-]*$" | *"ubuntu"

	// Path to private key
	privateKeyPath?: string

	// Private key content (secret reference)
	privateKey?: =~"^secret://"
}

// #NodeResources defines node hardware specifications
#NodeResources: {
	// CPU cores
	cpu: int & >=1

	// Memory in GB
	memory: int & >=1

	// Disk in GB
	disk: int & >=10

	// Architecture
	arch: "amd64" | "arm64" | *"amd64"

	// GPU available
	gpu?: #GPUSpec
}

// #GPUSpec defines GPU specifications
#GPUSpec: {
	// GPU vendor
	vendor: "nvidia" | "amd" | "intel"

	// GPU model
	model?: string

	// VRAM in GB
	vram?: int
}
