// Package whoami -- Whoami HTTP echo module.
//
// Provides an HTTP echo service for network and routing diagnostics.
// Requires Traefik for reverse proxy routing.
//
// NOTE: traefik/whoami is scratch-based -- NO shell, NO wget, NO curl inside.
// Health checks using shell tools will fail. Test from outside the container only.
//
// PROVEN CONFIG: Validated via reference-compose.yml.
package whoami

import "github.com/kombifyio/stackkits/base"

// Contract declares what this module requires and provides.
Contract: base.#ModuleContract & {
	metadata: {
		name:        "whoami"
		displayName: "Whoami"
		version:     "1.0.0"
		layer:       "L3-application"
		description: "HTTP echo service for network and routing diagnostics"
		testScenarios: ["SK-S1", "SK-S2", "SK-S3"]
	}

	requires: {
		services: {
			traefik: {
				minVersion: "3.0"
				provides: ["reverse-proxy"]
			}
		}
		infrastructure: {
			docker:  true
			network: "shared"
		}
	}

	provides: {
		capabilities: {
			"network-test": true
			"http-echo":    true
		}
		endpoints: {
			ui: {
				url:         "https://whoami.{{.domain}}"
				description: "Whoami echo service"
			}
		}
	}

	settings: {}

	contexts: {
		local: {}
		cloud: {}
		pi: {}
	}

	services: whoami: base.#ServiceDefinition & {
		name:     "whoami"
		type:     "test"
		image:    "traefik/whoami"
		tag:      "latest"
		required: false
		status:   "implemented"
		needs: ["traefik"]

		placement: {
			nodeType: "all"
			strategy: "single"
		}

		network: {
			traefik: {
				enabled: true
				rule:    "Host(`whoami.{{.domain}}`)"
				port:    80
			}
			networks: ["base_net"]
		}

		// NOTE: traefik/whoami is scratch-based -- NO shell, NO wget, NO curl inside.
		// Health checks using shell tools will fail. Use TCP check or skip.

		resources: {
			memory:    "16m"
			memoryMax: "32m"
			cpus:      0.1
		}

		security: {
			noNewPrivileges: true
			capDrop: ["ALL"]
			readOnly: true
		}

		labels: {
			"traefik.enable":                                        "true"
			"traefik.http.routers.whoami.rule":                      "Host(`whoami.{{.domain}}`)"
			"traefik.http.routers.whoami.entrypoints":               "web"
			"traefik.http.services.whoami.loadbalancer.server.port": "80"
		}

		subdomain: {key: "whoami", nested: "whoami", flat: "whoami"}
		dashboard: {icon: "&#129302;", order: 20, section: "Applications", badge: "L3 \u00b7 Test", enableVar: "enable_whoami", guideUrl: "https://docs.kombify.io/guides/stackkits/services/whoami"}

		output: {
			url:         "https://whoami.{{.domain}}"
			description: "Whoami network diagnostic service"
		}
	}
}
