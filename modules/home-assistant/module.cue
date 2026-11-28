// Package home_assistant -- Home Assistant application module.
//
// This module represents the self-hosted container runtime profile. Managed
// profiles are realized through the Kombify Control Plane and use the Smart
// Home package handoff instead of this local module.
package home_assistant

import "github.com/kombifyio/stackkits/foundation"

_runtimeImages: foundation.ArchitectureV2ModuleImages["stackkits-home-assistant-runtime"]

Contract: foundation.#ModuleContract & {
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
			"smart-home":            true
			"home-automation":       true
			"native-product-mcp":    true
			"home-assistant-rest":   true
			"home-assistant-ws":     true
			"home-assistant-assist": true
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
		managed_app_fit:    false
		missing_adapters: ["control-plane-managed-home-assistant"]
		rejection_reason: "The self-hosted Home Assistant container requires a dedicated server for persistent data and home integrations."
	}

	presentation: {
		guideUrl: "https://docs.kombify.io/guides/stackkits/use-cases/smart-home"
		clients: [{
			platform: "android"
			kind:     "store"
			url:      "https://play.google.com/store/apps/details?id=io.homeassistant.companion.android"
		}, {
			platform: "ios"
			kind:     "store"
			url:      "https://apps.apple.com/app/id1099568401"
		}]
		vendor: {
			name:    "Home Assistant"
			website: "https://www.home-assistant.io"
			docs:    "https://www.home-assistant.io/docs/"
			license: "Apache-2.0"
		}
	}

	services: "home-assistant": foundation.#ServiceDefinition & {
		name:        "home-assistant"
		displayName: "Home Assistant"
		type:        "application"
		image:       _runtimeImages["home-assistant"].image
		tag:         _runtimeImages["home-assistant"].tag
		upstream: {
			github: {repo: "home-assistant/core"}
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
			serviceGroup:   "smart-home"
			routeRole:      "smart-home"
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
				stableRouteKey:  "smart-home"
				stableLocalSlug: "smart-home"
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
			"traefik.enable":                                                "true"
			"traefik.http.routers.home-assistant.rule":                      "Host(`smart-home.{{.domain}}`)"
			"traefik.http.routers.home-assistant.entrypoints":               "web"
			"traefik.http.services.home-assistant.loadbalancer.server.port": "8123"
			"stackkit.layer":                                                "3-application"
			"stackkit.managed-by":                                           "selected-paas"
			"stackkit.product-mcp":                                          "home-assistant-native"
		}

		subdomain: {key: "smart_home", nested: "smart-home", flat: "smart-home"}
		dashboard: {icon: "&#127968;", order: 55, section: "Applications", badge: "L3 · Smart Home", enableVar: "enable_home_assistant"}

		output: {
			url:         "https://smart-home.{{.domain}}"
			description: "Home Assistant smart-home hub with native MCP endpoint at /api/mcp"
		}
	}
}

// AppModuleV3 is the complete #AppModuleV3 declaration of the Smart Home
// workload (ADR-0055 Stage 1 pilot). internal/engine renders it into the same
// Compose project the standalone Compose owner emits today for
// stackkits-home-assistant-runtime; modules/home-assistant/golden/compose.yaml
// pins that output. The legacy Contract above stays until Phase 2.
AppModuleV3: foundation.#AppModuleV3 & {
	apiVersion:     "stackkit.app-module/v3"
	moduleRef:      "stackkits-home-assistant-runtime"
	workloadRef:    "smart-home"
	alternativeRef: "home-assistant"
	release:        "2026.9.4"
	entryComponent: "home-assistant"
	delivery: {contract: "opentofu-stage1", resourcePrefix: "workload"}
	lifecycle: optionalAppsetupRef: "setup/home-assistant-owner-bootstrap"
	route: {
		serviceRef:        "smart-home"
		targetPort:        8123
		upstreamProtocol:  "http"
		ingressAuth:       "forward-auth"
		healthRef:         "home-assistant-http"
		requiredPrivilege: "user"
		allowedExposures: ["local", "remote-private", "public"]
	}
	secrets: {}
	// The StackKits-owned baseline and the seeding entrypoint are generated
	// by pure config renderers and bind-mounted read-only into /config.
	configFiles: [
		{componentRef: "home-assistant", target: "/config/configuration.yaml", configRendererRef: "config/home-assistant-configuration-yaml"},
		{componentRef: "home-assistant", target: "/config/stackkits-init.sh", configRendererRef:  "config/home-assistant-init-script"},
	]
	backup: {
		volumes: [{componentRef: "home-assistant", volumeRef: "config"}]
		hookRef:  "hook/quiesce-stop"
		probeRef: "probe/http-status"
	}
	networks: "home-assistant-internal": {}
	components: [{
		id:        "home-assistant"
		role:      "application"
		lifecycle: "daemon"
		image:     foundation.AppModuleV3CatalogImages["stackkits-home-assistant-runtime"]["home-assistant"]
		entrypoint: ["/bin/sh", "/config/stackkits-init.sh"]
		environment: {}
		secretEnvironment: {}
		volumes: [{id: "config", target: "/config", class: "persistent", backup: true}]
		networks: ["home-assistant-internal"]
		health: {kind: "http", path: "/", port: 8123}
		resources: {memoryLimit: "2g", memoryReservation: "512m"}
		labels: {}
		traits: {
			bundledPlugin: {
				kind:    "hass-oidc"
				version: "1.2.1"
				sha256:  "sha256:e5badaaacaa63cfd6fe733924a05e76d75058836190398598fb24de57cd47ccd"
				artifacts: {
					patch:          "stackkit-owner-binding-v1"
					patchSHA256:    "sha256:052c4e0b85b962a5fad74e395f250f4d5cf9eff63fa5be68fc1c4494535d1a8a"
					aiofilesSHA256: "sha256:abe311e527c862958650f9438e859c1fa7568a141b22abcd015e120e86a85695"
					joserfcSHA256:  "sha256:17e5d7a5a35e65442b05efc435a3d5d46696ffa2c8a2ed0eea6f63fc268e3224"
				}
			}
			homeIdentityAccess: {caBundleTarget: "/etc/stackkit/ca-bundle.pem", caBundleEnvironment: ["SSL_CERT_FILE"]}
			pocketIDClient: {
				publicClient: true
				callbackPath: "/auth/oidc/callback"
				environment: {}
				configuration: {
					target: "/config/stackkit-oidc.yaml"
					body:   "client_id: \"{{clientId}}\"\ndiscovery_url: \"{{issuer}}/.well-known/openid-configuration\"\nroles:\n  admin: admins\n  user: household\nclaims:\n  username: sub\n  groups: groups\nfeatures:\n  automatic_user_linking: false\n  force_https: true\n  default_redirect: false\nnetwork:\n  tls_verify: true\n  tls_ca_path: /etc/stackkit/ca-bundle.pem"
				}
			}
			homeAssistantNotify: {settingRef: "kombify-notify", target: "/config/stackkit-notify"}
		}
	}]
}
