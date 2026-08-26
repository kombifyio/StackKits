// Package smart_home defines the Smart Home use case package.
//
// Home Assistant owns the product MCP surface via its native /api/mcp server.
// StackKits owns lifecycle/evidence, and Kombify Control Plane realizes managed
// stateful runtime profiles.
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

	defaultRuntimeProfile: "kombify-managed-hybrid"
	runtimeProfiles: {
		"kombify-managed": {
			displayName: "Kombify Managed Home Assistant"
			description: "Kombify operates the Home Assistant runtime, state, routing, auth handoff, backup, and MCP/API wiring without requiring user-owned HA OS hardware."
			realization: "control-plane"
			placementModes: ["managed-serverless"]
			managedServerlessEligible: true
			requiresControlPlane:      true
			requiresLocalBridge:       false
			notes: ["Stateful managed application profile; not a stateless function runtime."]
		}
		"kombify-managed-hybrid": {
			displayName: "Kombify Managed Home Assistant + Home Bridge"
			description: "Kombify manages Home Assistant while an optional local bridge supplies LAN discovery and radio/device adjacency."
			realization: "hybrid"
			placementModes: ["managed-serverless", "standard"]
			managedServerlessEligible: true
			requiresControlPlane:      true
			requiresLocalBridge:       true
			notes: ["Use when Zigbee, Z-Wave, Thread, Matter, Bluetooth, local MQTT, or mDNS discovery must stay near the home network."]
		}
		"self-hosted-container": {
			displayName: "Self-hosted Home Assistant Container"
			description: "StackKit deploys Home Assistant as a selected-PaaS application on the user's own node."
			realization: "oss"
			placementModes: ["local-only", "standard"]
			managedServerlessEligible: false
			requiresControlPlane:      false
			requiresLocalBridge:       false
			notes: ["Container mode does not imply full Home Assistant OS or Supervisor parity."]
		}
		"self-hosted-ha-os": {
			displayName: "Self-hosted Home Assistant OS"
			description: "The user brings a Home Assistant OS device or VM; StackKits records and connects to it."
			realization: "external"
			placementModes: ["local-only", "standard"]
			managedServerlessEligible: false
			requiresControlPlane:      false
			requiresLocalBridge:       false
		}
		"bring-your-own-ha": {
			displayName: "Bring Your Own Home Assistant"
			description: "An existing Home Assistant instance is connected through its native MCP/API surfaces."
			realization: "external"
			placementModes: ["local-only", "standard", "managed-serverless"]
			managedServerlessEligible: true
			requiresControlPlane:      false
			requiresLocalBridge:       false
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
			rationale:  "Optional local adjacency bridge for LAN discovery and radio/device integrations when Home Assistant itself is managed remotely."
			capabilities: ["lan-discovery", "matter-thread", "zigbee", "z-wave", "mqtt-bridge"]
		}
		mosquitto: {
			moduleSlug: "mosquitto"
			role:       "supporting"
			required:   false
			rationale:  "Optional MQTT broker for local device integrations."
			capabilities: ["mqtt"]
		}
		zigbee2mqtt: {
			moduleSlug: "zigbee2mqtt"
			role:       "supporting"
			required:   false
			rationale:  "Optional Zigbee bridge when the deployment declares a coordinator device or Home Bridge capability."
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
				name:        "home-assistant-native-mcp"
				policy:      "on_demand"
				description: "Verify Home Assistant auth, expose the native /api/mcp endpoint through the protected route, and record MCP/API evidence."
			},
			{
				name:        "home-bridge-pairing"
				policy:      "on_demand"
				description: "Pair the optional local Home Bridge for LAN/radio/device adjacency."
			},
		]
	}

	evidence: {
		healthChecks: ["home-assistant-route", "home-assistant-api", "home-assistant-native-mcp"]
		required: ["route", "auth", "backup", "native-product-mcp"]
	}
}
