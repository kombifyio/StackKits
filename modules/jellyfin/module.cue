// Package jellyfin -- Jellyfin media server module.
//
// Transitional module contract mirroring the currently deployed Base Kit app.
package jellyfin

import "github.com/kombifyio/stackkits/base"

Contract: base.#ModuleContract & {
	metadata: {
		name:        "jellyfin"
		displayName: "Jellyfin"
		version:     "1.0.0"
		layer:       "L3-application"
		description: "Free media server for movies, TV, music, and photos"
		testScenarios: ["SK-S2", "SK-S4"]
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
			minMemory:         "512m"
		}
	}

	provides: {
		capabilities: {
			"media-server": true
			"video-stream": true
		}
		endpoints: {
			ui: {
				url:         "https://media.{{.domain}}"
				description: "Jellyfin web UI"
			}
		}
	}

	settings: {
		flexible: {
			mediaPath: *"/opt/media" | string
		}
	}

	contexts: {
		local: {}
		cloud: {}
		pi: {
			_resources: {
				memory:    "256m"
				memoryMax: "768m"
				cpus:      1.0
			}
		}
	}

	services: jellyfin: base.#ServiceDefinition & {
		name:     "jellyfin"
		type:     "media"
		image:    "jellyfin/jellyfin"
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
				rule:    "Host(`media.{{.domain}}`)"
				port:    8096
			}
			networks: ["base_net"]
		}

		volumes: [
			{
				source:      "jellyfin-config"
				target:      "/config"
				type:        "volume"
				backup:      true
				description: "Jellyfin configuration and metadata"
			},
			{
				source:      "jellyfin-cache"
				target:      "/cache"
				type:        "volume"
				backup:      false
				description: "Jellyfin transcoding cache"
			},
			{
				source:      "/opt/media"
				target:      "/media"
				type:        "bind"
				readOnly:    true
				backup:      false
				description: "Host media directory"
			},
		]

		healthCheck: {
			enabled: true
			http: {
				path:   "/health"
				port:   8096
				scheme: "http"
			}
			interval: "30s"
			timeout:  "5s"
			retries:  3
		}

		resources: {
			memory:    "512m"
			memoryMax: "2g"
			cpus:      2.0
		}

		security: {
			noNewPrivileges: true
			capDrop: ["ALL"]
		}

		labels: {
			"traefik.enable":                                          "true"
			"traefik.http.routers.jellyfin.rule":                      "Host(`media.{{.domain}}`)"
			"traefik.http.routers.jellyfin.entrypoints":               "web"
			"traefik.http.services.jellyfin.loadbalancer.server.port": "8096"
			"stackkit.layer":                                          "3-application"
			"stackkit.managed-by":                                     "selected-paas"
		}

		subdomain: {key: "media", nested: "media", flat: "media"}
		dashboard: {icon: "&#127916;", order: 40, section: "Applications", badge: "L3 \u00b7 Media", enableVar: "enable_jellyfin", guideUrl: "https://docs.kombify.io/guides/stackkits/services/jellyfin"}

		output: {
			url:         "https://media.{{.domain}}"
			description: "Jellyfin media server"
		}
	}
}
