// Package nextcloud -- Nextcloud files module alternative.
package nextcloud

import "github.com/kombifyio/stackkits/base"

Contract: base.#ModuleContract & {
	metadata: {
		name:        "nextcloud"
		displayName: "Nextcloud"
		version:     "1.0.0"
		layer:       "L3-application"
		description: "Document management and collaboration suite for the files use case"
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

	services: nextcloud: base.#ServiceDefinition & {
		name:        "nextcloud"
		displayName: "Files"
		description: "Document management and file sharing backed by Nextcloud"
		type:        "storage"
		image:       "nextcloud"
		tag:         "30-apache"
		required:    false
		status:      "implemented"
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
			accessPolicy: {
				appAuth:        "self-auth"
				ownerBootstrap: "Nextcloud image auto-configures the first admin from StackKit admin credentials."
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
		dashboard: {icon: "&#128193;", order: 60, section: "Applications", badge: "L3 \u00b7 Files", enableVar: "enable_files", guideUrl: "https://docs.kombify.io/guides/stackkits/services/files"}

		output: {
			url:         "https://files.{{.domain}}"
			description: "Nextcloud file management"
		}
	}
}
