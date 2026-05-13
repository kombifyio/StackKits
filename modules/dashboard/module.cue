// Package dashboard -- StackKits Node Hub module.
//
// Provides a node-local hub with onboarding, recovery hints, and links to the
// most important services on this StackKits node.
// Uses nginx:alpine as a lightweight static file server.
// Requires Traefik for reverse proxy routing.
//
// PROVEN CONFIG: Validated via reference-compose.yml.
package dashboard

import "github.com/kombifyio/stackkits/base"

// Contract declares what this module requires and provides.
Contract: base.#ModuleContract & {
	metadata: {
		name:        "dashboard"
		displayName: "StackKits Node Hub"
		version:     "1.0.0"
		layer:       "L3-application"
		description: "Node-local StackKits hub with onboarding, recovery, and service links"
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
			"dashboard":        true
			"service-overview": true
		}
		endpoints: {
			ui: {
				url:         "https://base.{{.domain}}"
				description: "StackKits Node Hub"
			}
		}
	}

	settings: {
		flexible: {
			title: *"My Homelab" | string
		}
	}

	contexts: {
		local: {}
		cloud: {}
		pi: {}
	}

	services: dashboard: base.#ServiceDefinition & {
		name:     "dashboard"
		type:     "dashboard"
		image:    "nginx"
		tag:      "alpine"
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
				rule:    "Host(`base.{{.domain}}`)"
				port:    80
			}
			networks: ["base_net"]
		}

		healthCheck: {
			enabled: true
			http: {
				path:   "/"
				port:   80
				scheme: "http"
			}
			interval: "30s"
			timeout:  "5s"
			retries:  3
		}

		resources: {
			memory:    "16m"
			memoryMax: "32m"
			cpus:      0.1
		}

		security: {
			noNewPrivileges: true
			capDrop: ["ALL"]
			readOnly: true
			tmpfs: ["/tmp", "/var/cache/nginx", "/run"]
		}

		labels: {
			"traefik.enable":                                           "true"
			"traefik.http.routers.dashboard.rule":                      "Host(`base.{{.domain}}`)"
			"traefik.http.routers.dashboard.entrypoints":               "web"
			"traefik.http.services.dashboard.loadbalancer.server.port": "80"
		}

		subdomain: {key: "base", nested: "base", flat: "base"}
		dashboard: {icon: "&#128421;", order: 0, section: "Platform", badge: "L3 \u00b7 Hub", enableVar: "enable_dashboard", guideUrl: "https://docs.kombify.io/guides/stackkits/node-hub"}

		output: {
			url:         "https://base.{{.domain}}"
			description: "StackKits Node Hub"
		}
	}
}
