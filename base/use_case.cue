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
	kind:      #UseCaseConnectorKind
	name:      =~"^[a-z][a-z0-9-]+$"
	owner:     #UseCaseConnectorOwner
	endpoint?: string
	transport: string
	auth:      string
	nativeProduct: bool | *false
	capabilities?: [...string] | *[]
}

#UseCaseProductAPI: {
	protocol: "rest" | "websocket" | "webdav" | "s3-compatible"
	basePath?: string
	auth:     string
	purpose:  string
}

#UseCaseCapability: {
	mode:      #UseCaseCapabilityMode
	authority: #UseCaseCapabilityAuthority
	source:    "stackkit-mcp" | "product-mcp" | "product-api" | "bridge"
	requiresApproval: bool | *false
	evidence: string
}

#UseCaseSetupDrop: {
	name:        =~"^[a-z][a-z0-9-]+$"
	policy:      "manual" | "on_demand" | "automatic"
	description: string
}
