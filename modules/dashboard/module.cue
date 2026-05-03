// Package dashboard -- Service overview dashboard module.
//
// Provides a service overview dashboard with links to all deployed services.
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
		displayName: "Dashboard"
		version:     "1.0.0"
		layer:       "L3-application"
		description: "Service overview dashboard with links to all deployed services"
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
				url:         "https://dash.{{.domain}}"
				description: "Homelab dashboard"
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
		pi:    {}
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
				rule:    "Host(`dash.{{.domain}}`)"
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
			"traefik.enable":                                                      "true"
			"traefik.http.routers.dashboard.rule":                                 "Host(`dash.{{.domain}}`)"
			"traefik.http.routers.dashboard.entrypoints":                          "web"
			"traefik.http.services.dashboard.loadbalancer.server.port":            "80"
		}

		subdomain: {key: "dashboard", nested: "base", flat: "dash"}

		output: {
			url:         "https://dash.{{.domain}}"
			description: "Homelab service dashboard"
		}
	}
}
