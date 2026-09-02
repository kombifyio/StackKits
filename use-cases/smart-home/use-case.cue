// Package smart_home defines the Smart Home use case package.
//
// Home Assistant owns the product MCP surface via its native /api/mcp server.
// StackKits owns the admitted container lifecycle/evidence. Managed, external,
// and local-bridge profiles below describe product intent, not native rollout.
package smart_home

import "github.com/kombifyio/stackkits/foundation"

Package: foundation.#UseCasePackage & {
	metadata: {
		name:        "smart-home"
		useCaseRef:  "smart-home"
		displayName: "Smart Home"
		version:     "0.1.0"
		layer:       "application"
		category:    "smart-home"
		lifecycle:   "pilot"
		description: "Home automation centered on Home Assistant with native MCP, API, managed runtime, and optional local bridge profiles."
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
		"kombify-managed": {
			displayName: "Kombify Managed Home Assistant"
			description: "Kombify operates the Home Assistant runtime, state, routing, auth handoff, backup, and MCP/API wiring without requiring user-owned HA OS hardware."
			realization: "control-plane"
			placementModes: ["managed-serverless"]
			managedServerlessEligible: true
			requiresControlPlane:      true
			requiresLocalBridge:       false
			notes: ["Managed product intent, not an admitted native workload alternative. Stateful application profile; not a stateless function runtime."]
		}
		"kombify-managed-hybrid": {
			displayName: "Kombify Managed Home Assistant + Home Bridge"
			description: "Kombify manages Home Assistant while an optional local bridge supplies LAN discovery and radio/device adjacency."
			realization: "hybrid"
			placementModes: ["managed-serverless", "standard"]
			managedServerlessEligible: true
			requiresControlPlane:      true
			requiresLocalBridge:       true
			notes: ["Planned bridge profile for devices that must stay near the home network. Native StackKits does not provision the Home Bridge, MQTT broker, or radio integrations."]
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
		"kombify-home-bridge": {
			moduleSlug: "kombify-home-bridge"
			role:       "bridge"
			required:   false
			rationale:  "Planned local adjacency bridge; native StackKits does not currently provision this component."
			capabilities: ["lan-discovery", "matter-thread", "zigbee", "z-wave", "mqtt-bridge"]
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
			rationale:  "Planned Zigbee bridge; declaring a device or Home Bridge intent does not install it through native StackKits."
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
