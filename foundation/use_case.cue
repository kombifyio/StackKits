// Package foundation -- Use Case Package schema.
//
// A Use Case Package describes the product-facing application intent and the
// concrete runtime/API/MCP surfaces that can satisfy it. Tool modules remain
// atomic; packages compose those modules plus managed/control-plane handoffs.
package foundation

import "list"

#UseCaseLayer: "application" | "platform" | "foundation"

#UseCaseLifecycle: "stable" | "beta" | "pilot" | "experimental" | "draft"

#UseCaseRole: "default" | "alternative" | "optional"

#UseCaseRuntimeRealization: "oss" | "control-plane" | "hybrid" | "external"

#UseCaseToolRole: "primary" | "supporting" | "connector" | "worker" | "database" | "bridge"

#UseCaseModuleSlug: =~"^[a-z][a-z0-9-]+$"

#UseCaseConnectorKind: "stackkit" | "home-assistant-native" | "product-api" | "external"

#UseCaseConnectorOwner: "stackkit" | "product" | "control-plane" | "external"

#UseCaseCapabilityMode: "read" | "plan" | "write" | "ops"

#UseCaseCapabilityAuthority: "read-only" | "gated-write" | "destructive"

#UseCaseLifecycleStageName: "install" | "manage" | "backup" | "upgrade" | "restore" | "drift" | "remove"

#UseCaseLifecycleOperationID: "stackkit.init" | "stackkit.validate" | "stackkit.resolve" | "stackkit.generate" | "stackkit.plan" | "stackkit.apply" | "stackkit.verify" | "stackkit.status" | "stackkit.logs" | "stackkit.backup" | "stackkit.restore" | "stackkit.upgrade" | "stackkit.drift" | "stackkit.remove" | "stackkit.advanced.change-set.apply" | "stackkit.drift.reconcile.advanced"

#UseCaseLifecycleSurface: "installer" | "cli" | "mcp" | "state-console"

#UseCaseLifecycleEvidence: "resolved-plan" | "generation-receipt" | "apply-result" | "owner-observation" | "snapshot-anchor" | "restore-result" | "upgrade-result" | "drift-report" | "removal-result"

#UseCaseLifecyclePhase: "resolve" | "generate" | "apply" | "verify" | "observe" | "inspect" | "snapshot" | "preflight" | "update" | "migrate" | "rollback" | "stage" | "safety-snapshot" | "activate" | "recover" | "compare" | "authorize" | "remove"

#UseCasePackage: {
	metadata: {
		name: =~"^[a-z][a-z0-9-]+$"
		// useCaseRef joins this package to the CUE-owned product catalog. A
		// package remains product/docs input; it does not make a workload
		// executable or replace Architecture v2 runtime authority.
		useCaseRef:  category
		displayName: string
		version:     =~"^[0-9]+\\.[0-9]+\\.[0-9]+(-[a-z0-9.]+)?$"
		layer:       #UseCaseLayer
		category:    #UseCaseSlug
		lifecycle:   #UseCaseLifecycle
		description: string
	}

	selection: {
		role: #UseCaseRole
		// Draft packages may leave the default unresolved when the committed
		// catalog intentionally has no selected implementation yet.
		defaultTool?: #UseCaseToolRef
		alternatives?: [...#UseCaseToolRef] | *[]
	}
	if metadata.lifecycle != "draft" {
		selection: defaultTool: #UseCaseToolRef
	}

	defaultRuntimeProfile: =~"^[a-z][a-z0-9-]+$"
	runtimeProfiles: {
		[=~"^[a-z][a-z0-9-]+$"]: #UseCaseRuntimeProfile
		(defaultRuntimeProfile): #UseCaseRuntimeProfile
	}

	tools: [ModuleSlug=#UseCaseModuleSlug]: #UseCaseToolRef & {
		moduleSlug: ModuleSlug
	}

	_selectionIntegrity: {
		if selection.defaultTool != _|_ {
			defaultTool: [for moduleSlug, _ in tools if moduleSlug == selection.defaultTool.moduleSlug {moduleSlug}] & list.MinItems(1) & list.MaxItems(1)
		}
		alternatives: [for alternative in selection.alternatives {
			moduleSlug: alternative.moduleSlug
			matches: [for moduleSlug, _ in tools if moduleSlug == alternative.moduleSlug {moduleSlug}] & list.MinItems(1) & list.MaxItems(1)
		}]
	}

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

	// computeTiers declares what this use case is on each kit graph.
	// It does not select the stack graph: install.computeTier does.
	// Kit computeTierGraphs remain the execution substitutions.
	// Hardware floors stay on the graph and module runtimeRequirements.
	computeTiers: {
		low:      #UseCaseComputeTierFitV2
		standard: #UseCaseComputeTierFitV2
		high:     #UseCaseComputeTierFitV2
	}

	_computeTierTools: [
		for name, fit in computeTiers
		if fit.included && fit.moduleSlug != _|_ {
			tier: name
			matches: [for slug, _ in tools if slug == fit.moduleSlug {slug}] & list.MinItems(1) & list.MaxItems(1)
		},
	]

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

	managedServerlessEligible: bool | *false
	requiresControlPlane:      bool | *false
	requiresLocalBridge:       bool | *false

	notes?: [...string] | *[]
}

// #UseCaseLoadResidency is when the workload occupies the node.
// always-on contributes to base load; on-demand is ad-hoc query/session
// performance; scheduled is a bounded batch window.
#UseCaseLoadResidency: "always-on" | "on-demand" | "scheduled"

// #UseCaseLoadBaseline is the idle cost while the use case is enabled.
// none: not resident. idle-resident: process up, mostly waiting.
// active-resident: continuously working (indexers, ML, radio loops).
#UseCaseLoadBaseline: "none" | "idle-resident" | "active-resident"

// #UseCaseLoadBurst is the spike shape when someone actually uses it.
#UseCaseLoadBurst: "none" | "interactive" | "ingest" | "batch"

// #UseCaseComputeTierFitV2 is the declared product surface of this use case
// on one install.computeTier graph. Unifier reads it. Apply does not.
#UseCaseComputeTierFitV2: {
	included: bool
	if included {
		functions: [...string] & list.MinItems(1)
		load: {
			residency: #UseCaseLoadResidency
			baseline:  #UseCaseLoadBaseline
			burst:     #UseCaseLoadBurst
		}
		moduleSlug?: #UseCaseModuleSlug
		notes?: [...string] | *[]
	}
	if !included {
		reason: string
	}
}

#UseCaseToolRef: {
	moduleSlug: #UseCaseModuleSlug
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
