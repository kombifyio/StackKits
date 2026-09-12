// Package smart_home defines the Smart Home use case package.
//
// Home Assistant owns the product MCP surface via its native /api/mcp server.
// StackKits owns configuration and admitted lifecycle/evidence, not device or
// automation features. Managed profiles describe Cloud-only commercial intent;
// optional local connectivity reuses existing upstream services.
package smart_home

import "github.com/kombifyio/stackkits/foundation"

Package: foundation.#UseCasePackage & {
	metadata: {
		name:        "smart-home"
		useCaseRef:  "smart-home"
		displayName: "Smart Home"
		version:     "0.1.1"
		layer:       "application"
		category:    "smart-home"
		lifecycle:   "pilot"
		description: "Home Assistant deployment and lifecycle integration using its native MCP/API and optional upstream MQTT/device services; Managed profiles are Cloud-only intent."
	}

	selection: {
		role: "optional"
		defaultTool: {
			moduleSlug: "home-assistant"
			role:       "primary"
			required:   true
			rationale:  "Home Assistant is the canonical open smart-home control plane and provides the native product MCP server for agents."
			capabilities: ["smart-home-hub", "native-product-mcp", "assist-api", "automation"]
		}
		alternatives: []
	}

	defaultRuntimeProfile: "self-hosted-container"
	runtimeProfiles: {
		"provisioned-ha-os": {
			displayName: "New Home Assistant OS appliance"
			description: "An authorized runtime owner provisions HAOS and applies the fresh-instance baseline. Select the home-assistant-haos workload alternative explicitly."
			realization: "external"
			placementModes: ["local-only", "standard"]
			managedServerlessEligible: false
			requiresControlPlane: false
			requiresLocalBridge: false
			notes: ["Executable through an admitted external API owner. VM resources, images, endpoints and credentials remain in provider custody. Imported data uses home-assistant-imported instead and never receives the fresh baseline."]
		}
		"kombify-managed": {
			displayName: "Kombify Managed Home Assistant"
			description: "Kombify operates the Home Assistant runtime, state, routing, auth handoff, backup, and MCP/API wiring without requiring user-owned HA OS hardware."
			realization: "control-plane"
			placementModes: ["managed-serverless"]
			managedServerlessEligible: true
			requiresControlPlane:      true
			requiresLocalBridge:       false
			notes: ["Cloud-only Managed product intent, outside StackKits and Techstack Core/Standard. Existing Techstack commercial orchestration operates upstream Home Assistant; this is not an admitted native workload alternative."]
		}
		"kombify-managed-hybrid": {
			displayName: "Kombify Managed Home Assistant with local integrations"
			description: "Cloud-managed Home Assistant connected to explicitly selected existing local services for LAN and device access. Device and protocol behavior remains upstream-owned."
			realization: "hybrid"
			placementModes: ["managed-serverless", "standard"]
			managedServerlessEligible: true
			requiresControlPlane:      true
			requiresLocalBridge:       true
			notes: ["Cloud-only Managed intent, outside Techstack Core/Standard. Reuse Home Assistant integrations, an existing MQTT broker and optional Zigbee2MQTT; no Kombify device bridge is implied. These optional services are not yet provisioned by native StackKits."]
		}
		"self-hosted-container": {
			displayName: "Self-hosted Home Assistant Container"
			description: "StackKits deploys Home Assistant Container through the explicitly selected standalone Compose or PaaS adapter."
			realization: "oss"
			placementModes: ["local-only", "standard"]
			managedServerlessEligible: false
			requiresControlPlane:      false
			requiresLocalBridge:       false
			notes: ["Container mode does not imply Home Assistant OS, Supervisor, add-on store, MQTT, or radio/device integration parity. Cloud placement does not grant access to Home LAN devices."]
		}
		"self-hosted-ha-os": {
			displayName: "Self-hosted Home Assistant OS"
			description: "The user brings a Home Assistant OS device or VM; StackKits records and connects to it."
			realization: "external"
			placementModes: ["local-only", "standard"]
			managedServerlessEligible: false
			requiresControlPlane:      false
			requiresLocalBridge:       false
			notes: ["External integration intent. Native StackKits does not install Home Assistant OS or admit this profile as a workload alternative."]
		}
		"bring-your-own-ha": {
			displayName: "Bring Your Own Home Assistant"
			description: "An existing Home Assistant instance is connected through its native MCP/API surfaces."
			realization: "external"
			placementModes: ["local-only", "standard", "managed-serverless"]
			managedServerlessEligible: true
			requiresControlPlane:      false
			requiresLocalBridge:       false
			notes: ["External integration intent. Connecting an existing instance is not provided by the native Home Assistant Container rollout."]
		}
	}

	computeTiers: {
		low: {
			included: true
			moduleSlug: "home-assistant"
			functions: ["smart-home-hub", "automation"]
			load: {residency: "always-on", baseline: "active-resident", burst: "interactive"}
			notes: ["24/7 radio/state loop is the base load. Classic constrained-device resident. Native MCP and extra bridges stay optional."]
		}
		standard: {
			included: true
			moduleSlug: "home-assistant"
			functions: ["smart-home-hub", "native-product-mcp", "assist-api", "automation"]
			load: {residency: "always-on", baseline: "active-resident", burst: "interactive"}
		}
		high: {
			included: true
			moduleSlug: "home-assistant"
			functions: ["smart-home-hub", "native-product-mcp", "assist-api", "automation"]
			load: {residency: "always-on", baseline: "active-resident", burst: "interactive"}
			notes: ["Same OSS functions as standard. Managed/hybrid profiles are realization, not a kit graph."]
		}
	}

	tools: {
		"home-assistant": {
			moduleSlug: "home-assistant"
			role:       "primary"
			required:   true
			rationale:  "Primary smart-home product and native MCP/API owner."
			capabilities: ["native-product-mcp", "rest-api", "websocket-api", "assist-api"]
		}
		mosquitto: {
			moduleSlug: "mosquitto"
			role:       "supporting"
			required:   false
			rationale:  "Planned optional MQTT broker; not included in the native Home Assistant Container rollout."
			capabilities: ["mqtt"]
		}
		zigbee2mqtt: {
			moduleSlug: "zigbee2mqtt"
			role:       "supporting"
			required:   false
			rationale:  "Existing upstream Zigbee2MQTT connected to Home Assistant through MQTT. Its optional deployment integration is planned; declaration alone does not install it."
			capabilities: ["zigbee", "mqtt-bridge"]
		}
	}

	connectors: {
		stackkit: {
			kind:      "stackkit"
			name:      "stackkit"
			owner:     "stackkit"
			endpoint:  "/mcp"
			transport: "streamable-http"
			auth:      "stackkit-mcp-token"
			capabilities: ["lifecycle", "setup", "evidence", "handoff"]
		}
		"home-assistant": {
			kind:      "home-assistant-native"
			name:      "home-assistant"
			owner:     "product"
			endpoint:  "/api/mcp"
			transport: "streamable-http"
			auth:      "home-assistant-auth"
			nativeProduct: true
			capabilities: ["assist", "exposed-entities", "tools", "resources"]
		}
	}

	productApis: {
		rest: {
			protocol: "rest"
			basePath: "/api"
			auth:     "home-assistant-auth"
			purpose:  "Health checks, state snapshots, service calls, and setup verification."
		}
		websocket: {
			protocol: "websocket"
			basePath: "/api/websocket"
			auth:     "home-assistant-auth"
			purpose:  "Live events, state changes, device/entity/area context, and RIL observation."
		}
	}

	ril: capabilities: {
		inventory: {
			mode:      "read"
			authority: "read-only"
			source:    "product-mcp"
			evidence:  "Entity/device/area inventory from Home Assistant exposed entities."
		}
		status: {
			mode:      "read"
			authority: "read-only"
			source:    "product-api"
			evidence:  "REST/WebSocket health, integration, and state summary."
		}
		"automation-plan": {
			mode:      "plan"
			authority: "read-only"
			source:    "product-mcp"
			evidence:  "Draft automation or scene plan without applying changes."
		}
		"service-call": {
			mode:      "write"
			authority: "gated-write"
			source:    "product-mcp"
			requiresApproval: true
			evidence: "Audited Home Assistant service call through exposed MCP tools."
		}
		"bridge-diagnostics": {
			mode:      "ops"
			authority: "read-only"
			source:    "bridge"
			evidence:  "Bridge, MQTT, Zigbee, Z-Wave, Matter/Thread, and local-network diagnostics."
		}
	}

	setup: {
		defaultPolicy: "on_demand"
		drops: [
			{
				name:        "home-assistant-owner-bootstrap"
				policy:      "on_demand"
				description: "Create the Homelab owner through Home Assistant onboarding (/api/onboarding/users) using StackKit admin credentials. Password never appears in generate artifacts."
			},
		]
	}

	evidence: {
		healthChecks: ["home-assistant-route", "home-assistant-api", "home-assistant-native-mcp"]
		required: ["route", "auth", "backup", "owner-bootstrap", "native-product-mcp"]
	}

	agentSurface: {
		equipPolicy:  "on-generate"
		lifecycleMcp: {}
		productMcps: [{
			id:                   "home-assistant"
			owner:                "product"
			endpoint:             "/api/mcp"
			transport:            "streamable-http"
			auth:                 "home-assistant-auth"
			generateClientConfig: true
		}]
		apis: [{
			id:       "rest"
			protocol: "rest"
			purpose:  "Health, state snapshots, service calls, and setup verification."
			auth:     "home-assistant-auth"
		}, {
			id:       "websocket"
			protocol: "websocket"
			purpose:  "Live events, state changes, and device/entity/area context."
			auth:     "home-assistant-auth"
		}]
		skills: [{
			id:       "homelab-mcp"
			audience: "product-user"
			source:   "stackkits"
			path:     "use-cases/smart-home/agent/homelab-mcp/SKILL.md"
		}]
		cliHelpers: [{
			command: "stackkit agent mcp-config"
			purpose: "Print the stackkit lifecycle MCP client connection. Product MCP remains Home Assistant /api/mcp."
		}]
		configBaseline: {
			status:         "declared"
			moduleInputRef: "stackkits-home-assistant-runtime"
		}
	}

	lifecycle: foundation.#StandardUseCaseLifecycle
}
