// Package base — Module Contract schema for service modules.
//
// A Module is the atomic unit of a StackKit. Each module defines
// a single service (or tightly coupled service group like Dokploy+Postgres+Redis)
// with explicit dependency declarations and capability contracts.
//
// Modules live in modules/<name>/ and are imported by StackKits.
package base

// #ModuleMetadata identifies a module.
#ModuleMetadata: {
	// Module identifier (lowercase, hyphenated, DNS-safe)
	name: =~"^[a-z][a-z0-9-]+$"

	// Display name for UI/docs
	displayName: string

	// Semantic version of the module definition
	version: =~"^[0-9]+\\.[0-9]+\\.[0-9]+(-[a-z0-9.]+)?$"

	// Layer classification
	layer: #ModuleLayer

	// Short description (one line)
	description: string

	// Whether this is a core module (ships with StackKits)
	core: bool | *true

	// Release maturity of the module (Golden Rules §5.2/§10):
	// "default": part of a kit's release default set; first-run path is
	//   automated and covered by the canonical E2E gates.
	// "opt-in": curated and selectable, but not enabled by default.
	// "draft": exists for development/evaluation; MUST NOT be part of the
	//   default set or the canonical E2E dispatch.
	// No silent default: every module must classify itself explicitly.
	maturity: "default" | "opt-in" | "draft"

	// Canonical StackKit scenarios that cover this module in fast, VM, or live gates.
	// Draft modules MUST NOT claim canonical scenarios (enforced below).
	testScenarios?: [...#CanonicalScenarioID]

	// Draft modules stay out of canonical E2E dispatch (Golden Rules §5.2).
	if maturity == "draft" {
		testScenarios?: []
	}
}

// #CanonicalScenarioID enumerates the canonical rollout scenarios in the private StackKit scenario catalog.
#CanonicalScenarioID: "SK-S1" | "SK-S2" | "SK-S3" | "SK-S4" | "SK-S5"

// #ModuleLayer classifies where a module sits in the stack.
#ModuleLayer:
	"L1-foundation" |
	"L2-platform-ingress" |
	"L2-platform-identity" |
	"L2-platform-paas" |
	"L2-platform-dns" |
	"L2-platform-observability" |
	"L2-platform-diagnostics" |
	"L3-application"

// #DeliveryType defines how a module reaches the target runtime.
#DeliveryType: "stackkit-iac" | "paas"

// #DeliveryManager identifies the runtime owner for a delivered module.
#DeliveryManager: "stackkit" | "selected-paas" | "external-paas"

// #ModuleDelivery declares the delivery boundary for a module. Layer-3
// applications may emit compose manifests, but those manifests are PaaS input;
// OpenTofu/local-exec must not start L3 applications directly.
#ModuleDelivery: {
	type:      #DeliveryType
	managedBy: #DeliveryManager
}

// #RequiresSpec declares what a module needs from other modules or infrastructure.
#RequiresSpec: {
	// Required services (other modules that must be enabled)
	services?: [string]: #ServiceRequirement

	// Required infrastructure capabilities
	infrastructure?: #InfraRequirement
}

// #ServiceRequirement specifies what is needed from a dependency.
#ServiceRequirement: {
	// Minimum version of the dependency
	minVersion?: string

	// Capabilities the dependency must provide
	provides?: [...string]

	// Whether this dependency is optional (soft dependency)
	optional: bool | *false
}

// #InfraRequirement declares infrastructure needs.
#InfraRequirement: {
	// Requires Docker runtime
	docker: bool | *true

	// Requires a shared network
	network?: string

	// Requires Docker socket access
	dockerSocket: bool | *false

	// Requires persistent storage
	persistentStorage: bool | *false

	// Minimum memory (e.g., "128m")
	minMemory?: string

	// Required architecture
	arch?: "amd64" | "arm64" | "any"
}

// #ProvidesSpec declares what a module offers to other modules and users.
#ProvidesSpec: {
	// Named capabilities this module provides
	capabilities?: [string]: bool

	// Traefik middleware definitions this module creates
	middleware?: [string]: #MiddlewareSpec

	// Endpoints this module exposes
	endpoints?: [string]: #EndpointSpec
}

// #MiddlewareSpec describes a Traefik middleware provided by this module.
#MiddlewareSpec: {
	type: "forwardauth" | "basicauth" | "ratelimit" | "headers" | "redirect" | string
	// Middleware-specific config (varies by type)
	[string]: _
}

// #EndpointSpec describes an endpoint this module exposes.
#EndpointSpec: {
	// URL template (supports {{.domain}} placeholders)
	url: string

	// Whether this endpoint is internal (container-to-container only)
	internal: bool | *false

	// Description
	description?: string
}

// #SettingsSpec classifies module settings into perma (immutable after deploy)
// and flexible (changeable via day-2 operations).
#SettingsSpec: {
	// Perma settings: set once at deploy time, changing requires teardown+redeploy
	perma?: [string]: _

	// Flexible settings: can be changed via stackkit apply without teardown
	flexible?: [string]: _
}

// #ContextOverrides defines per-context configuration adjustments.
// Keys are NodeContext values ("local", "cloud", "pi").
#ContextOverrides: [#NodeContext]: _

// #ProvisionerService defines a one-shot container that configures a service
// after it becomes healthy. Provisioners run setup logic (create admin users,
// seed groups, configure defaults) and exit. They never restart.
//
// Pattern:
//   - restart: "no"
//   - depends_on: {<service>: {condition: "service_healthy"}}
//   - Idempotent: safe to run multiple times (handle "already exists" responses)
//
// Examples: kuma-provisioner, dokploy-provisioner, pocketid-provisioner, lldap-provisioner
#ProvisionerService: {
	// Container image (e.g., "alpine/curl:latest", "node:22-alpine")
	image: string

	// Shell command(s) to run — use $var (double-dollar) in Docker Compose YAML
	command: string

	// Service this provisioner configures (must be healthy before running)
	dependsOn: string

	// Additional networks to join (to reach the service being provisioned)
	networks?: [...string]

	// Environment variables
	environment?: [string]: string
}

// #ModuleContract is the complete contract for a service module.
// Every module/<name>/module.cue MUST define a value satisfying this schema.
#ModuleContract: {
	// Module identity
	metadata: #ModuleMetadata

	// Delivery contract. StackKit-owned/default L3 modules should declare
	// PaaS delivery. L3 as a category is not globally forced through PaaS:
	// user-installed apps outside StackKit manifests are state-unmanaged.
	delivery?: #ModuleDelivery

	// What this module requires
	requires?: #RequiresSpec

	// What this module provides
	provides?: #ProvidesSpec

	// Settings classification
	settings?: #SettingsSpec

	// Per-context overrides
	contexts?: #ContextOverrides

	// Placement eligibility (publishable metadata) + optional per-unit override.
	// MS/coupled realization is Control-Plane, not OSS. See base/placement.cue.
	placementSupport?: #PlacementSupport
	// Mode-only shorthand (NOT #PlacementIntent like #BaseStackKit.placementMode):
	// at module level the sub-dimensions (exposure/coupling) are StackKit-managed,
	// so the per-unit override is mode-only.
	placementMode?: #PlacementMode

	// The service definition(s) this module deploys
	services: [string]: #ServiceDefinition

	// Optional one-shot provisioners (run after services are healthy)
	provisioners?: [string]: #ProvisionerService

	// Whether this module is enabled in the current composition
	enabled: bool | *true
}
