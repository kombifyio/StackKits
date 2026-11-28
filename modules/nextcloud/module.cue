// Package nextcloud -- Nextcloud files module alternative.
package nextcloud

import "github.com/kombifyio/stackkits/foundation"

Contract: foundation.#ModuleContract & {
	placementSupport: {
		managed_app_fit:  false
		rejection_reason: "Nextcloud requires a dedicated server for its stateful service stack."
	}
	metadata: {
		name:        "nextcloud"
		displayName: "Nextcloud"
		version:     "1.0.0"
		layer:       "L3-application"
		description: "Document management and collaboration suite for the files use case"
		// Opt-in: curated Files alternative for standard/high tiers; Cloudreve
		// is the release default for the files route.
		maturity: "opt-in"
		testScenarios: ["SK-S1", "SK-S2", "SK-S3"]
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
		}
	}

	provides: {
		capabilities: {
			"files":            true
			"document-storage": true
			"file-sharing":     true
			"collaboration":    true
		}
		endpoints: {
			ui: {
				url:         "https://files.{{.domain}}"
				description: "Nextcloud web UI"
			}
		}
	}

	contexts: {
		local: {}
		cloud: {}
	}

	presentation: {
		guideUrl: "https://docs.kombify.io/guides/stackkits/use-cases/files"
		clients: [{
			platform: "android"
			kind:     "store"
			url:      "https://play.google.com/store/apps/details?id=com.nextcloud.client"
		}, {
			platform: "android"
			kind:     "store"
			url:      "https://f-droid.org/packages/com.nextcloud.client/"
		}, {
			platform: "ios"
			kind:     "store"
			url:      "https://apps.apple.com/app/id1125420102"
		}, {
			platform: "windows"
			kind:     "download"
			url:      "https://nextcloud.com/install/#install-clients"
		}, {
			platform: "macos"
			kind:     "download"
			url:      "https://nextcloud.com/install/#install-clients"
		}, {
			platform: "linux"
			kind:     "download"
			url:      "https://nextcloud.com/install/#install-clients"
		}]
		vendor: {
			name:    "Nextcloud"
			website: "https://nextcloud.com"
			docs:    "https://docs.nextcloud.com"
			license: "AGPL-3.0"
		}
	}

	services: nextcloud: foundation.#ServiceDefinition & {
		name:        "nextcloud"
		displayName: "Files"
		description: "Document management and file sharing backed by Nextcloud"
		type:        "storage"
		image:       "nextcloud"
		tag:         "30-apache"
		upstream: {
			github: {repo: "nextcloud/server"}
			pinLine: "30-apache"
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
				rule:    "Host(`files.{{.domain}}`)"
				port:    80
			}
			networks: ["base_net"]
		}

		accessPolicy: {
			// Owner direction 2026-09-25: sign-in maps to Pocket ID. Nextcloud
			// supports OIDC natively (user_oidc), so its desktop and mobile
			// clients log in through Pocket ID instead of a forward-auth wall.
			outerAuth:      "self"
			appAuth:        "oidc-sso"
			ownerBootstrap: "Nextcloud image auto-configures the first admin from StackKit admin credentials; the user_oidc app registers Pocket ID (https://id.{{.domain}}) as the OIDC provider."
		}

		volumes: [
			{
				source:      "nextcloud-html"
				target:      "/var/www/html"
				type:        "volume"
				backup:      true
				description: "Nextcloud app, config, and user data"
			},
			{
				source:      "nextcloud-db"
				target:      "/var/lib/mysql"
				type:        "volume"
				backup:      true
				description: "Nextcloud MariaDB data"
			},
		]

		healthCheck: {
			enabled: true
			http: {
				path:   "/status.php"
				port:   80
				scheme: "http"
			}
			interval: "30s"
			timeout:  "10s"
			retries:  5
		}

		config: {
			serviceGroup: "files"
			routeRole:    "files"
			replacementContract: {
				stableRouteKey:  "files"
				stableLocalSlug: "files"
				requiredCapabilities: ["files", "document-storage"]
				bootstrapRequirements: ["create-first-admin", "trusted-domain-config"]
				note: "Nextcloud is an alternative implementation for the same files route."
			}
		}

		resources: {
			memory:    "1g"
			memoryMax: "3g"
			cpus:      1.5
		}

		security: {
			noNewPrivileges: true
			capDrop: ["ALL"]
		}

		labels: {
			"traefik.enable":                                           "true"
			"traefik.http.routers.nextcloud.rule":                      "Host(`files.{{.domain}}`)"
			"traefik.http.routers.nextcloud.entrypoints":               "web"
			"traefik.http.services.nextcloud.loadbalancer.server.port": "80"
			"stackkit.layer":                                           "3-application"
			"stackkit.managed-by":                                      "selected-paas"
		}

		subdomain: {key: "files", nested: "files", flat: "files"}
		dashboard: {icon: "&#128193;", order: 60, section: "Applications", badge: "L3 \u00b7 Files", enableVar: "enable_files"}

		output: {
			url:         "https://files.{{.domain}}"
			description: "Nextcloud file management"
		}
	}
}

// AppModuleV3 is the #AppModuleV3 declaration of nextcloud (ADR-0055 Phase 2A).
AppModuleV3: foundation.#AppModuleV3 & {
	let _images = foundation.AppModuleV3CatalogImages["stackkits-nextcloud-runtime"]
	apiVersion:     "stackkit.app-module/v3"
	moduleRef:      "stackkits-nextcloud-runtime"
	workloadRef:    "files"
	alternativeRef: "nextcloud"
	release:        "35.0.1-apache"
	entryComponent: "nextcloud"
	delivery: {contract: "opentofu-stage1", resourcePrefix: "workload"}
	lifecycle: {}
	route: {
		serviceRef:        "files"
		targetPort:        80
		upstreamProtocol:  "http"
		ingressAuth:       "forward-auth"
		healthRef:         "nextcloud-http"
		requiredPrivilege: "user"
		allowedExposures: ["local", "remote-private", "public"]
	}
	secrets: {
		"database-password": purpose: "Custody secret slot database-password for nextcloud"
		"owner-password": purpose: "Custody secret slot owner-password for nextcloud"
	}
	backup: {
		volumes: [
			{componentRef: "nextcloud", volumeRef: "html"},
			{componentRef: "nextcloud-postgres", volumeRef: "database"},
		]
		hookRef:  "hook/quiesce-stop"
		probeRef: "probe/http-status"
	}
	networks: {
		"nextcloud-internal": {}
	}
	components: [
		{
			id:        "nextcloud"
			role:      "application"
			lifecycle: "daemon"
			image:     _images["nextcloud"]
			dependsOn: ["nextcloud-postgres", "nextcloud-valkey"]
			environment: {
				NEXTCLOUD_ADMIN_USER: "owner"
				OVERWRITEPROTOCOL: "https"
				POSTGRES_DB: "nextcloud"
				POSTGRES_HOST: "nextcloud-postgres"
				POSTGRES_USER: "nextcloud"
				REDIS_HOST: "nextcloud-valkey"
				TRUSTED_PROXIES: "10.0.0.0/8 172.16.0.0/12 192.168.0.0/16"
			}
			secretEnvironment: {
				NEXTCLOUD_ADMIN_PASSWORD: "owner-password"
				POSTGRES_PASSWORD: "database-password"
			}
			volumes: [
				{id: "html", target: "/var/www/html", class: "persistent", backup: true},
			]
			networks: ["nextcloud-internal"]
			health: {kind: "http", path: "/status.php", port: 80}
			resources: {memoryLimit: "3g", memoryReservation: "512m"}
			labels: {}
			traits: {}
		},
		{
			id:        "nextcloud-postgres"
			role:      "database"
			lifecycle: "daemon"
			image:     _images["nextcloud-postgres"]
			environment: {
				POSTGRES_DB: "nextcloud"
				POSTGRES_USER: "nextcloud"
			}
			secretEnvironment: {
				POSTGRES_PASSWORD: "database-password"
			}
			volumes: [
				{id: "database", target: "/var/lib/postgresql", class: "persistent", backup: true},
			]
			networks: ["nextcloud-internal"]
			health: {kind: "command", command: ["pg_isready", "-U", "nextcloud", "-d", "nextcloud"]}
			resources: {memoryLimit: "1g", memoryReservation: "256m"}
			labels: {}
			traits: {}
		},
		{
			id:        "nextcloud-valkey"
			role:      "cache"
			lifecycle: "daemon"
			image:     _images["nextcloud-valkey"]
			command: ["valkey-server"]
			environment: {}
			secretEnvironment: {}
			volumes: [
				{id: "cache", target: "/data", class: "cache"},
			]
			networks: ["nextcloud-internal"]
			health: {kind: "command", command: ["valkey-cli", "ping"]}
			resources: {memoryLimit: "256m", memoryReservation: "64m"}
			labels: {}
			traits: {}
		},
	]
}
