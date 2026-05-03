// Package uptimekuma -- Uptime Kuma monitoring module.
//
// Provides self-hosted uptime monitoring with status pages and notifications.
// Requires Traefik for reverse proxy routing.
//
// PROVEN CONFIG: Validated via reference-compose.yml.
package uptimekuma

import "github.com/kombifyio/stackkits/base"

// Contract declares what this module requires and provides.
Contract: base.#ModuleContract & {
	metadata: {
		name:        "uptime-kuma"
		displayName: "Uptime Kuma"
		version:     "1.0.0"
		layer:       "L3-application"
		description: "Self-hosted uptime monitoring with status pages and notifications"
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
		}
	}

	provides: {
		capabilities: {
			"monitoring":    true
			"uptime-checks": true
			"status-pages":  true
			"notifications": true
		}
		endpoints: {
			ui: {
				url:         "https://kuma.{{.domain}}"
				description: "Uptime Kuma dashboard"
			}
		}
	}

	settings: {
		flexible: {
			logLevel: *"info" | "debug" | "warn" | "error"
		}
	}

	contexts: {
		local: {}
		cloud: {}
		pi:    {}
	}

	services: "uptime-kuma": base.#ServiceDefinition & {
		name:     "uptime-kuma"
		type:     "monitoring"
		image:    "louislam/uptime-kuma"
		tag:      "1"
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
				rule:    "Host(`kuma.{{.domain}}`)"
				port:    3001
			}
			networks: ["base_net"]
		}

		volumes: [{
			source:      "uptime-kuma-data"
			target:      "/app/data"
			type:        "volume"
			backup:      true
			description: "Uptime Kuma database and config"
		}]

		healthCheck: {
			enabled: true
			http: {
				path:   "/"
				port:   3001
				scheme: "http"
			}
			interval: "30s"
			timeout:  "10s"
			retries:  3
		}

		resources: {
			memory:    "256m"
			memoryMax: "512m"
			cpus:      0.5
		}

		security: {
			noNewPrivileges: true
			capDrop: ["ALL"]
		}

		labels: {
			"traefik.enable":                                                           "true"
			"traefik.http.routers.uptime-kuma.rule":                                    "Host(`kuma.{{.domain}}`)"
			"traefik.http.routers.uptime-kuma.entrypoints":                             "web"
			"traefik.http.services.uptime-kuma.loadbalancer.server.port":               "3001"
		}

		subdomain: {key: "kuma", nested: "kuma", flat: "kuma"}
		dashboard: {icon: "&#128202;", order: 10, section: "Applications", badge: "L3 \u00b7 Monitoring"}

		output: {
			url:         "https://kuma.{{.domain}}"
			description: "Uptime Kuma monitoring dashboard"
		}
	}
}
