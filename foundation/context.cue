// Legacy NodeContext vocabulary.
//
// Architecture v2 does not select a kit, product graph, or architecture from
// this enum. Native v2 init rejects --context. Migration maps:
//   local -> site.kind home
//   cloud -> site.kind cloud
//   pi    -> site.kind home + nodes[].hardware.profile pi
//
// hardware.profile pi is a constrained homelab device class (SBC, mini-PC,
// low-RAM NUC, and similar), not Raspberry-only. Architecture is
// nodes[].hardware.arch or attested inventory. The product graph is
// install.computeTier.
//
// The former #ContextConfig/#ContextDefaults resolver (per-context TLS, PaaS,
// memory factor, arch defaults) had no consumer and contradicted v2, where
// those decisions are KitDefinition, computeTier graph, and inventory owned.
package foundation

// #NodeContext is legacy migration input: "local" | "cloud" | "pi".
#NodeContext: "local" | "cloud" | "pi"

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
		contexts: []
		requires: []
		conflicts: []
	}

	// Whether this add-on is enabled
	enabled: bool | *true
}
