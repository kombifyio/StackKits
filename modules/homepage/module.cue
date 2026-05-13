// Package homepage -- IaC-managed homelab start dashboard.
//
// Homepage/gethomepage is the default user-facing start page for a homelab.
// It is configured from generated YAML and reads Docker metadata through a
// Docker Socket Proxy, not by mounting the Docker socket into Homepage.
package homepage

import "github.com/kombifyio/stackkits/base"

Contract: base.#ModuleContract & {
	metadata: {
		name:        "homepage"
		displayName: "Homepage"
		version:     "1.0.0"
		layer:       "L3-application"
		description: "IaC-managed homelab start dashboard generated from the StackKits service catalog"
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
			docker:            true
			network:           "shared"
			persistentStorage: false
		}
	}

	provides: {
		capabilities: {
			"dashboard":         true
			"homelab-startpage": true
			"service-overview":  true
			"docker-discovery":  true
		}
		endpoints: {
			ui: {
				url:         "https://home.{{.domain}}"
				description: "Homelab start dashboard"
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

	services: {
		homepage: base.#ServiceDefinition & {
			name:     "homepage"
			type:     "dashboard"
			image:    "ghcr.io/gethomepage/homepage"
			tag:      "latest"
			required: false
			status:   "implemented"
			needs: ["traefik", "homepage-socket-proxy"]

			placement: {
				nodeType: "main"
				strategy: "single"
			}

			network: {
				traefik: {
					enabled: true
					rule:    "Host(`home.{{.domain}}`)"
					port:    3000
				}
				networks: ["base_net"]
			}

			healthCheck: {
				enabled: true
				http: {
					path:   "/"
					port:   3000
					scheme: "http"
				}
				interval: "30s"
				timeout:  "5s"
				retries:  3
			}

			resources: {
				memory:    "128m"
				memoryMax: "256m"
				cpus:      0.25
			}

			securityContext: {
				noNewPrivileges: true
				capabilitiesDrop: ["ALL"]
			}

			config: {
				dockerDiscovery: {
					mode:                *"socket-proxy" | "tls-remote"
					writableSocketMount: false
				}
				healthStatus: {
					source:              "docker-socket-proxy"
					serverName:          "stackkit"
					containerNameSource: "service-catalog-module-map"
					note:                "Homepage health badges must use Docker container status. Browser-facing .localhost hostnames are not resolvable from inside the Homepage container."
				}
				generatedFiles: ["settings.yaml", "services.yaml", "widgets.yaml", "docker.yaml"]
			}

			labels: {
				"traefik.enable":                                          "true"
				"traefik.http.routers.homepage.rule":                      "Host(`home.{{.domain}}`)"
				"traefik.http.routers.homepage.entrypoints":               "web"
				"traefik.http.services.homepage.loadbalancer.server.port": "3000"
			}

			subdomain: {key: "home", nested: "home", flat: "home"}

			dashboard: {
				icon:      "&#8962;"
				order:     0
				section:   "Platform"
				badge:     "L3 \u00b7 Start"
				enableVar: "enable_homepage"
				guideUrl:  "https://docs.kombify.io/guides/stackkits/services/homepage"
			}

			output: {
				url:         "https://home.{{.domain}}"
				description: "Homepage homelab start dashboard"
			}
		}

		"homepage-socket-proxy": base.#ServiceDefinition & {
			name:     "homepage-socket-proxy"
			type:     "infrastructure"
			image:    "tecnativa/docker-socket-proxy"
			tag:      "latest"
			required: false
			status:   "implemented"
			needs: []

			placement: {
				nodeType: "main"
				strategy: "single"
			}

			network: {
				mode: "bridge"
				networks: ["base_net"]
			}

			environment: {
				LOG_LEVEL:  "warning"
				CONTAINERS: "1"
				INFO:       "1"
				NETWORKS:   "1"
				SERVICES:   "1"
				TASKS:      "1"
				VERSION:    "1"
				POST:       "0"
			}

			volumes: [{
				source:   "/var/run/docker.sock"
				target:   "/var/run/docker.sock"
				type:     "bind"
				readOnly: true
				backup:   false
			}]

			resources: {
				memory:    "64m"
				memoryMax: "128m"
				cpus:      0.1
			}

			securityContext: {
				noNewPrivileges: true
			}
		}
	}
}
