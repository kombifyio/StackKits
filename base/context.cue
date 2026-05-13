// Context definitions for StackKit deployments.
//
// A NodeContext describes the runtime environment of a deployment target.
// It is auto-detected (not user-chosen) based on hardware capabilities
// and provider metadata, though it can be overridden via CLI --context flag.
//
// The Context × StackKit matrix produces curated default configurations:
//   - PAAS selection (Dokploy vs Coolify)
//   - TLS strategy (self-signed via Step-CA vs Let's Encrypt)
//   - Resource limits (full vs constrained)
//   - Image architecture (amd64 vs arm64)
//   - Storage driver selection
package base

// #NodeContext enumerates the supported deployment contexts.
#NodeContext: "local" | "cloud" | "pi"

// #ContextConfig defines the resolved configuration for a given context.
// StackKits use this to derive sensible defaults without user intervention.
#ContextConfig: {
	// Which context this config resolves for
	context: #NodeContext

	// Display name for UI
	displayName: string

	// Description
	description: string

	// TLS strategy
	tls: #ContextTLS

	// PAAS preference
	paas: #ContextPAAS

	// Resource profile
	resources: #ContextResources

	// Image architecture
	arch: "amd64" | "arm64" | *"amd64"

	// Default access mode
	accessMode: "ports" | "proxy" | *"ports"

	// Whether this context typically has a public IP
	publicIP: bool | *false

	// Storage driver recommendation
	storageDriver: "overlay2" | "devicemapper" | "vfs" | *"overlay2"
}

// #ContextTLS defines TLS strategy for a context
#ContextTLS: {
	mode: "letsencrypt" | "self-signed" | "none" | *"self-signed"

	// Whether ACME is available (requires public IP + domain)
	acmeAvailable: bool | *false
}

// #ContextPAAS defines PAAS preference for a context
#ContextPAAS: {
	// Preferred PAAS for this context
	preferred: "dokploy" | "coolify" | "dockge" | *"dokploy"
}

// #ContextResources defines resource limits for a context
#ContextResources: {
	// Default compute tier
	defaultTier: "high" | "standard" | "low" | *"standard"

	// Per-service memory limit factor (1.0 = standard, 0.5 = constrained)
	memoryFactor: number & >=0.25 & <=2.0 | *1.0

	// CPU shares (1024 = full share)
	cpuShares: int & >=256 & <=2048 | *1024
}

// #ContextDefaults resolves configuration defaults based on context.
// Usage in StackKit CUE files:
//   _resolved: #ContextDefaults & { _context: "local" }
#ContextDefaults: {
	_context: #NodeContext

	// --- local context ---
	if _context == "local" {
		tls: {
			mode:          "self-signed"
			acmeAvailable: false
		}
		paas: preferred:     "dokploy"
		resources: {
			defaultTier:  "standard"
			memoryFactor: 1.0
			cpuShares:    1024
		}
		arch:          "amd64"
		accessMode:    "ports"
		publicIP:      false
		storageDriver: "overlay2"
	}

	// --- cloud context ---
	if _context == "cloud" {
		tls: {
			mode:          "letsencrypt"
			acmeAvailable: true
		}
		paas: preferred:     "coolify"
		resources: {
			defaultTier:  "standard"
			memoryFactor: 1.0
			cpuShares:    1024
		}
		arch:          "amd64"
		accessMode:    "proxy"
		publicIP:      true
		storageDriver: "overlay2"
	}

	// --- pi context ---
	if _context == "pi" {
		tls: {
			mode:          "self-signed"
			acmeAvailable: false
		}
		paas: preferred:     "dockge"
		resources: {
			defaultTier:  "low"
			memoryFactor: 0.5
			cpuShares:    512
		}
		arch:          "arm64"
		accessMode:    "ports"
		publicIP:      false
		storageDriver: "overlay2"
	}
}

// #AddOnMetadata defines the common metadata all Add-Ons must provide.
// This is the base schema that all addon _addon blocks conform to.
#AddOnMetadata: {
	// Addon identifier (lowercase, hyphenated)
	name: =~"^[a-z][a-z0-9-]+$"

	// Display name for UI
	displayName: string

	// Semantic version
	version: =~"^[0-9]+\\.[0-9]+\\.[0-9]+(-[a-z0-9.]+)?$"

	// Layer classification
	layer: "INFRASTRUCTURE" | "NETWORK" | "OBSERVABILITY" | "APPLICATION" | "SECURITY"

	// Description
	description: string
}

// #AddOnCompatibility defines what an Add-On is compatible with.
#AddOnCompatibility: {
	// Compatible StackKits (empty = all)
	stackkits: [...string] | *[]

	// Compatible contexts (empty = all)
	contexts: [...#NodeContext] | *[]

	// Required other add-ons
	requires: [...string] | *[]

	// Mutually exclusive add-ons
	conflicts: [...string] | *[]
}

// #AddOnBase is the base schema that all Add-On #Config definitions should embed.
// Usage in addon CUE files:
//   #Config: {
//       #AddOnBase
//       // addon-specific fields...
//   }
#AddOnBase: {
	// Metadata (conventionally set as hidden field _addon)
	_addon: #AddOnMetadata

	// Compatibility constraints
	_compatibility: #AddOnCompatibility | *{
		stackkits: []
		contexts:  []
		requires:  []
		conflicts: []
	}

	// Whether this add-on is enabled
	enabled: bool | *true
}
