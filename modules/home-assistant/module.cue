// Package home_assistant -- Home Assistant application module.
//
// This module represents the self-hosted container runtime profile. Managed
// profiles are realized through the Kombify Control Plane and use the Smart
// Home package handoff instead of this local module.
package home_assistant

import "github.com/kombifyio/stackkits/base"

Contract: base.#ModuleContract & {
	metadata: {
		name:        "home-assistant"
		displayName: "Home Assistant"
		version:     "0.1.0"
		layer:       "L3-application"
		description: "Open smart-home control plane with native MCP, REST, and WebSocket APIs"
		core:        false
		maturity:    "opt-in"
		testScenarios: []
	}

	delivery: {
		type:      "paas"
		managedBy: "selected-paas"
	}

	requires: {
		services: {
			traefik: {
				minVersion: "3.0"
				provides: ["reverse-proxy"]
			}
		}
		infrastructure: {
			docker:            true
			persistentStorage: true
			network:           "shared"
			minMemory:         "1g"
			arch:              "any"
		}
	}

	provides: {
		capabilities: {
			"smart-home":             true
			"home-automation":        true
			"native-product-mcp":     true
			"home-assistant-rest":    true
			"home-assistant-ws":      true
			"home-assistant-assist":  true
		}
		endpoints: {
			ui: {
				url:         "https://smart-home.{{.domain}}"
				description: "Home Assistant web UI"
			}
			mcp: {
				url:         "https://smart-home.{{.domain}}/api/mcp"
				description: "Native Home Assistant MCP server"
			}
			api: {
				url:         "https://smart-home.{{.domain}}/api"
				description: "Home Assistant REST API"
			}
		}
	}

	settings: {
		flexible: {
			runtimeProfile: *"self-hosted-container" | string
			nativeMCP:      true
			restAPI:        true
			websocketAPI:   true
		}
	}

	placementSupport: {
		local_only:         true
		standard:           true
		managed_serverless: false
		missing_adapters:   ["control-plane-managed-home-assistant"]
		rejection_reason:   "Managed realization is control-plane owned; the OSS module only implements the self-hosted container profile."
	}

	services: "home-assistant": base.#ServiceDefinition & {
		name:        "home-assistant"
		displayName: "Home Assistant"
		type:        "application"
		image:       "ghcr.io/home-assistant/home-assistant"
		tag:         "stable"
		upstream: {
			github:  {repo: "home-assistant/core"}
			pinLine: "stable"
		}
		description: "Home Assistant smart-home hub"
		required:    false
		status:      "beta"
		needs: ["traefik"]

		placement: {
			nodeType: "main"
			strategy: "single"
		}

		network: {
			traefik: {
				enabled: true
				rule:    "Host(`smart-home.{{.domain}}`)"
				port:    8123
			}
			networks: ["base_net"]
		}

		accessPolicy: {
			outerAuth:      "tinyauth-pocketid"
			appAuth:        "self-auth"
			ownerBootstrap: "Home Assistant owner setup is product-owned; StackKit records onboarding and native MCP/API verification evidence."
		}

		environment: {
			TZ: "{{.timezone}}"
		}

		volumes: [
			{
				source:      "home-assistant-config"
				target:      "/config"
				type:        "volume"
				backup:      true
				description: "Home Assistant configuration, database, integrations, and automation state"
			},
		]

		healthCheck: {
			enabled: true
			http: {
				path:   "/"
				port:   8123
				scheme: "http"
			}
			interval: "30s"
			timeout:  "10s"
			retries:  8
		}

		config: {
			serviceGroup: "smart-home"
			routeRole:    "smart-home"
			runtimeProfile: "self-hosted-container"
			productMcp: {
				type:     "home-assistant-native"
				endpoint: "/api/mcp"
				auth:     "home-assistant-auth"
			}
			productApis: {
				rest:      "/api"
				websocket: "/api/websocket"
			}
			replacementContract: {
				stableRouteKey:       "smart-home"
				stableLocalSlug:      "smart-home"
				requiredCapabilities: ["smart-home", "native-product-mcp"]
				bootstrapRequirements: ["verify-home-assistant-auth", "verify-native-mcp", "record-exposed-entity-policy"]
				note: "Any replacement Smart Home module must preserve Home Assistant native MCP/API semantics or document an equivalent product-native MCP surface."
			}
		}

		resources: {
			memory:    "1g"
			memoryMax: "4g"
			cpus:      2.0
		}

		security: {
			noNewPrivileges: true
			capDrop: ["ALL"]
		}

		labels: {
			"traefik.enable":                                                  "true"
			"traefik.http.routers.home-assistant.rule":                       "Host(`smart-home.{{.domain}}`)"
			"traefik.http.routers.home-assistant.entrypoints":                "web"
			"traefik.http.services.home-assistant.loadbalancer.server.port":  "8123"
			"stackkit.layer":                                                  "3-application"
			"stackkit.managed-by":                                             "selected-paas"
			"stackkit.product-mcp":                                            "home-assistant-native"
		}

		subdomain: {key: "smart_home", nested: "smart-home", flat: "smart-home"}
		dashboard: {icon: "&#127968;", order: 55, section: "Applications", badge: "L3 · Smart Home", enableVar: "enable_home_assistant", guideUrl: "https://docs.kombify.io/guides/stackkits/services/home-assistant"}

		output: {
			url:         "https://smart-home.{{.domain}}"
			description: "Home Assistant smart-home hub with native MCP endpoint at /api/mcp"
		}
	}
}
