// Package dozzle -- Dozzle container log viewer module.
//
// Provides real-time Docker container log viewing via web UI.
// Uses socket-proxy for Docker API access (never mounts docker.sock directly).
//
// PROVEN CONFIG: Validated via reference-compose.yml.
package dozzle

import "github.com/kombifyio/stackkits/base"

// Contract declares what this module requires and provides.
Contract: base.#ModuleContract & {
	metadata: {
		name:        "dozzle"
		displayName: "Dozzle"
		version:     "1.1.0"
		layer:       "L3-application"
		description: "Real-time Docker container log viewer"
		testScenarios: ["SK-S2", "SK-S3"]
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
			"socket-proxy": {
				provides: ["docker-api-proxy"]
			}
		}
		infrastructure: {
			docker:  true
			network: "shared"
		}
	}

	provides: {
		capabilities: {
			"log-viewer":     true
			"container-logs": true
		}
		endpoints: {
			ui: {
				url:         "https://logs.{{.domain}}"
				description: "Dozzle log viewer"
			}
		}
	}

	settings: {
		flexible: {
			logLevel: *"info" | "debug"
		}
	}

	contexts: {
		local: {}
		cloud: {}
		pi: {}
	}

	services: dozzle: base.#ServiceDefinition & {
		name:     "dozzle"
		type:     "logging"
		image:    "amir20/dozzle"
		tag:      "latest"
		required: false
		status:   "implemented"
		needs: ["traefik", "socket-proxy"]

		placement: {
			nodeType: "all"
			strategy: "single"
		}

		network: {
			traefik: {
				enabled: true
				rule:    "Host(`logs.{{.domain}}`)"
				port:    8080
			}
			networks: ["frontend", "socket-proxy-net"]
		}

		// No docker.sock — uses DOCKER_HOST=tcp://socket-proxy:2375
		environment: {
			DOCKER_HOST: "tcp://socket-proxy:2375"
		}

		healthCheck: {
			enabled: true
			http: {
				path:   "/"
				port:   8080
				scheme: "http"
			}
			interval: "30s"
			timeout:  "5s"
			retries:  3
		}

		resources: {
			memory:    "64m"
			memoryMax: "128m"
			cpus:      0.25
		}

		security: {
			noNewPrivileges: true
			capDrop: ["ALL"]
			readOnly: true
			tmpfs: ["/tmp"]
		}

		labels: {
			"traefik.enable":                                        "true"
			"traefik.http.routers.dozzle.rule":                      "Host(`logs.{{.domain}}`)"
			"traefik.http.routers.dozzle.entrypoints":               "web"
			"traefik.http.services.dozzle.loadbalancer.server.port": "8080"
			"stackkit.layer":                                        "3-application"
			"stackkit.managed-by":                                   "selected-paas"
		}

		subdomain: {key: "logs", nested: "logs", flat: "logs"}

		output: {
			url:         "https://logs.{{.domain}}"
			description: "Dozzle container log viewer"
		}
	}
}
