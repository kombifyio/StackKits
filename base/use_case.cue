// Package base -- Use Case Package schema.
//
// A Use Case Package describes the product-facing application intent and the
// concrete runtime/API/MCP surfaces that can satisfy it. Tool modules remain
// atomic; packages compose those modules plus managed/control-plane handoffs.
package base

#UseCaseSlug: "smart-home" | "photos" | "media" | "vault" | "files" | "ai" | "dev" | "mail" | "game" | "remote"

#UseCaseLayer: "application" | "platform" | "foundation"

#UseCaseLifecycle: "stable" | "beta" | "pilot" | "experimental" | "draft"

#UseCaseRole: "default" | "alternative" | "optional"

#UseCaseRuntimeRealization: "oss" | "control-plane" | "hybrid" | "external"

#UseCaseToolRole: "primary" | "supporting" | "connector" | "worker" | "database" | "bridge"

#UseCaseConnectorKind: "stackkit" | "home-assistant-native" | "product-api" | "external"

#UseCaseConnectorOwner: "stackkit" | "product" | "control-plane" | "external"

#UseCaseCapabilityMode: "read" | "plan" | "write" | "ops"

#UseCaseCapabilityAuthority: "read-only" | "gated-write" | "destructive"

#UseCaseLifecycleStageName: "install" | "manage" | "backup" | "upgrade" | "restore" | "drift" | "remove"

#UseCaseLifecycleOperationID: "stackkit.init" | "stackkit.validate" | "stackkit.resolve" | "stackkit.generate" | "stackkit.plan" | "stackkit.apply" | "stackkit.verify" | "stackkit.status" | "stackkit.logs" | "stackkit.backup" | "stackkit.restore" | "stackkit.upgrade" | "stackkit.drift" | "stackkit.remove"

#UseCaseLifecycleSurface: "installer" | "cli" | "mcp" | "state-console"

#UseCaseLifecycleEvidence: "resolved-plan" | "generation-receipt" | "apply-result" | "owner-observation" | "snapshot-anchor" | "restore-result" | "upgrade-result" | "drift-report" | "removal-result"

#UseCaseLifecyclePhase: "resolve" | "generate" | "apply" | "verify" | "observe" | "inspect" | "snapshot" | "preflight" | "update" | "migrate" | "rollback" | "stage" | "safety-snapshot" | "activate" | "recover" | "compare" | "authorize" | "remove"

#UseCasePackage: {
	metadata: {
		name:        =~"^[a-z][a-z0-9-]+$"
		displayName: string
		version:     =~"^[0-9]+\\.[0-9]+\\.[0-9]+(-[a-z0-9.]+)?$"
		layer:       #UseCaseLayer
		category:    #UseCaseSlug
		lifecycle:   #UseCaseLifecycle
		description: string
	}

	selection: {
		role:        #UseCaseRole
		defaultTool: #UseCaseToolRef
		alternatives?: [...#UseCaseToolRef] | *[]
	}

	defaultRuntimeProfile: =~"^[a-z][a-z0-9-]+$"
	runtimeProfiles: [=~"^[a-z][a-z0-9-]+$"]: #UseCaseRuntimeProfile

	tools: [=~"^[a-z][a-z0-9-]+$"]: #UseCaseToolRef

	connectors?: [=~"^[a-z][a-z0-9-]+$"]: #UseCaseConnector

	productApis?: [=~"^[a-z][a-z0-9-]+$"]: #UseCaseProductAPI

	ril?: {
		capabilities: [=~"^[a-z][a-z0-9-]+$"]: #UseCaseCapability
	}

	setup?: {
		defaultPolicy: "manual" | "on_demand" | "automatic"
		drops?: [...#UseCaseSetupDrop] | *[]
	}

	evidence?: {
		healthChecks?: [...string] | *[]
		required?: [...string] | *[]
	}

	// lifecycle is product intent only. The referenced operation IDs remain
	// implemented by the standalone operation registry; use-case packages do
	// not implement commands, MCP handlers, or State Console behavior.
	lifecycle?: {
		referenceVertical: bool | *false
		stages: {
			install: #UseCaseLifecycleStage & {name: "install"}
			manage: #UseCaseLifecycleStage & {name: "manage"}
			backup: #UseCaseLifecycleStage & {name: "backup"}
			upgrade: #UseCaseLifecycleStage & {name: "upgrade"}
			restore: #UseCaseLifecycleStage & {name: "restore"}
			drift: #UseCaseLifecycleStage & {name: "drift"}
			remove: #UseCaseLifecycleStage & {name: "remove"}
		}
	}
}

#UseCaseLifecycleStage: {
	name: #UseCaseLifecycleStageName

	// operations are stable registry identities, never shell fragments.
	operations: [#UseCaseLifecycleOperationID, ...#UseCaseLifecycleOperationID]
	surfaces: [#UseCaseLifecycleSurface, ...#UseCaseLifecycleSurface]
	evidence: [#UseCaseLifecycleEvidence, ...#UseCaseLifecycleEvidence]
	phases: [#UseCaseLifecyclePhase, ...#UseCaseLifecyclePhase]

	mutation:      bool
	destructive:   bool
	ownerApproval: bool

	if mutation {
		ownerApproval: true
	}
	if destructive {
		mutation:      true
		ownerApproval: true
	}
}

#UseCaseRuntimeProfile: {
	displayName: string
	description: string
	realization: #UseCaseRuntimeRealization

	placementModes: [...#PlacementMode]
	contexts: [...#NodeContext]

	managedServerlessEligible: bool | *false
	requiresControlPlane:      bool | *false
	requiresLocalBridge:       bool | *false

	notes?: [...string] | *[]
}

#UseCaseToolRef: {
	moduleSlug: =~"^[a-z][a-z0-9-]+$"
	role:       #UseCaseToolRole
	required:   bool | *false
	rationale:  string
	capabilities?: [...string] | *[]
}

#UseCaseConnector: {
	kind:          #UseCaseConnectorKind
	name:          =~"^[a-z][a-z0-9-]+$"
	owner:         #UseCaseConnectorOwner
	endpoint?:     string
	transport:     string
	auth:          string
	nativeProduct: bool | *false
	capabilities?: [...string] | *[]
}

#UseCaseProductAPI: {
	protocol:  "rest" | "websocket" | "webdav" | "s3-compatible"
	basePath?: string
	auth:      string
	purpose:   string
}

#UseCaseCapability: {
	mode:             #UseCaseCapabilityMode
	authority:        #UseCaseCapabilityAuthority
	source:           "stackkit-mcp" | "product-mcp" | "product-api" | "bridge"
	requiresApproval: bool | *false
	evidence:         string
}

#UseCaseSetupDrop: {
	name:        =~"^[a-z][a-z0-9-]+$"
	policy:      "manual" | "on_demand" | "automatic"
	description: string
}
