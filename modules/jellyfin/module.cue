// Package jellyfin -- Jellyfin media server module.
//
// Transitional module contract mirroring the currently deployed Base Kit app.
package jellyfin

import "github.com/kombifyio/stackkits/foundation"

_runtimeImages: foundation.ArchitectureV2ModuleImages["stackkits-jellyfin-runtime"]

Contract: foundation.#ModuleContract & {
	placementSupport: {
		managed_app_fit:  false
		rejection_reason: "Jellyfin transcoding and media storage require a dedicated server."
	}
	metadata: {
		name:        "jellyfin"
		displayName: "Jellyfin"
		version:     "1.0.0"
		layer:       "L3-application"
		description: "Free media server for movies, TV, music, and photos"
		maturity:    "opt-in"
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

	presentation: {
		guideUrl: "https://docs.kombify.io/stackkits/apps/jellyfin"
		clients: [{
			platform: "android"
			kind:     "store"
			url:      "https://play.google.com/store/apps/details?id=org.jellyfin.mobile"
		}, {
			platform: "ios"
			kind:     "store"
			url:      "https://apps.apple.com/us/app/jellyfin-mobile/id1480192618"
		}, {
			platform: "windows"
			kind:     "download"
			url:      "https://github.com/jellyfin/jellyfin-media-player/releases"
		}]
		vendor: {
			name:    "Jellyfin"
			website: "https://jellyfin.org"
			docs:    "https://jellyfin.org/docs/"
			license: "GPL-2.0"
			support: [{
				kind: "donate"
				url:  "https://opencollective.com/jellyfin"
			}]
		}
	}

	services: jellyfin: foundation.#ServiceDefinition & {
		name:  "jellyfin"
		type:  "media"
		image: _runtimeImages["jellyfin"].image
		tag:   _runtimeImages["jellyfin"].tag
		upstream: {
			github: {repo: "jellyfin/jellyfin"}
		}
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

		accessPolicy: {
			outerAuth: "tinyauth-pocketid"
			appAuth:   "self-auth"
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
		dashboard: {icon: "&#127916;", order: 40, section: "Applications", badge: "L3 \u00b7 Media", enableVar: "enable_jellyfin"}

		output: {
			url:         "https://media.{{.domain}}"
			description: "Jellyfin media server"
		}
	}
}

// AppModuleV3 is the #AppModuleV3 declaration of jellyfin (ADR-0055 Phase 2A).
AppModuleV3: foundation.#AppModuleV3 & {
	let _images = foundation.AppModuleV3CatalogImages["stackkits-jellyfin-runtime"]
	apiVersion:     "stackkit.app-module/v3"
	moduleRef:      "stackkits-jellyfin-runtime"
	workloadRef:    "media"
	alternativeRef: "jellyfin"
	release:        "12.1"
	entryComponent: "jellyfin"
	delivery: {contract: "opentofu-stage1", resourcePrefix: "workload"}
	lifecycle: {}
	route: {
		serviceRef:        "media"
		targetPort:        8096
		upstreamProtocol:  "http"
		ingressAuth:       "forward-auth"
		healthRef:         "jellyfin-http"
		requiredPrivilege: "user"
		allowedExposures: ["local", "remote-private", "public"]
	}
	secrets: {}
	backup: {
		volumes: [
			{componentRef: "jellyfin", volumeRef: "config"},
		]
		hookRef:  "hook/quiesce-stop"
		probeRef: "probe/http-status"
	}
	networks: {
		"jellyfin-internal": {}
	}
	components: [
		{
			id:        "jellyfin"
			role:      "application"
			lifecycle: "daemon"
			image:     _images["jellyfin"]
			environment: {}
			secretEnvironment: {}
			volumes: [
				{id: "config", target: "/config", class: "persistent", backup: true},
				{id: "cache", target: "/cache", class: "cache"},
				{id: "library", target: "/media", class: "persistent", readOnly: true},
			]
			networks: ["jellyfin-internal"]
			health: {kind: "http", path: "/health", port: 8096}
			resources: {memoryLimit: "2g", memoryReservation: "512m"}
			labels: {}
			traits: {
				homeIdentityAccess: {caBundleTarget: "/etc/stackkit/ca-bundle.pem", caBundleEnvironment: ["SSL_CERT_FILE"]}
				pocketIDClient: {
					callbackURLs: ["{{origin}}/sso/OID/redirect/pocketid", "{{origin}}/sso/OID/r/pocketid"]
					environment: {
						JELLYFIN_PublishedServerUrl: "{{origin}}"
					}
				}
				bundledPlugin: {kind: "jellyfin-sso", version: "5.1.1", sha256: "sha256:db126b4763bda3a3754fa2103232fc03ef1bf929cfeda35f2e9af39c875233b1"}
			}
		},
	]
}
