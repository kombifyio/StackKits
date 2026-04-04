// Package immich -- Immich photo management module.
//
// Transitional module contract mirroring the currently deployed Base Kit app.
package immich

import "github.com/kombifyio/stackkits/base"

Contract: base.#ModuleContract & {
	metadata: {
		name:        "immich"
		displayName: "Immich"
		version:     "1.0.0"
		layer:       "L3-application"
		description: "Self-hosted photo and video management with AI-powered search and mobile backup"
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
		}
	}

	provides: {
		capabilities: {
			"photo-management": true
			"media-backup":     true
			"ai-search":        true
		}
		endpoints: {
			ui: {
				url:         "https://photos.{{.domain}}"
				description: "Immich web UI"
			}
		}
	}

	settings: {
		flexible: {
			uploadPath: *"/usr/src/app/upload" | string
		}
	}

	contexts: {
		local: {}
		cloud: {}
		pi: {
			_resources: {
				memory:    "768m"
				memoryMax: "2g"
				cpus:      1.0
			}
		}
	}

	services: immich: base.#ServiceDefinition & {
		name:     "immich"
		type:     "application"
		image:    "ghcr.io/immich-app/immich-server"
		tag:      "release"
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
				rule:    "Host(`photos.{{.domain}}`)"
				port:    2283
			}
			networks: ["base_net"]
		}

		volumes: [
			{
				source:      "immich-upload"
				target:      "/usr/src/app/upload"
				type:        "volume"
				backup:      true
				description: "Immich uploads"
			},
			{
				source:      "immich-postgres-data"
				target:      "/var/lib/postgresql/data"
				type:        "volume"
				backup:      true
				description: "Immich PostgreSQL data"
			},
			{
				source:      "immich-model-cache"
				target:      "/cache"
				type:        "volume"
				backup:      false
				description: "Immich ML model cache"
			},
		]

		healthCheck: {
			enabled: true
			http: {
				path:   "/api/server/ping"
				port:   2283
				scheme: "http"
			}
			interval: "30s"
			timeout:  "10s"
			retries:  5
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
			"traefik.enable":                                        "true"
			"traefik.http.routers.immich.rule":                      "Host(`photos.{{.domain}}`)"
			"traefik.http.routers.immich.entrypoints":               "web"
			"traefik.http.services.immich.loadbalancer.server.port": "2283"
			"stackkit.layer":                                        "3-application"
			"stackkit.managed-by":                                   "compose"
		}

		output: {
			url:         "https://photos.{{.domain}}"
			description: "Immich photo management"
		}
	}
}
