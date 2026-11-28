// Package sites_framework -- Optional kombify Sites Framework self-hosting add-on (Blueprint §5.3).
//
// Boundary (D-2026-11-24-04): kombify Sites (hosted builder) stays SaaS;
// self-hosting a site built with the open kombify Sites Framework (kombifyio/Sites)
// is an optional user workload under dev (`dev-sites`), matching
// kombify-Sites/docs/HOSTING.md profiles:
//   1. `deck` / `topic-artifact`: pinned Node 24 + sqlite-vec + Companion endpoint
//   2. `web`: pinned SvelteKit Node 24 + PocketBase (pb_data, pb_migrations, pb_hooks)
package sites_framework

import "github.com/kombifyio/stackkits/foundation"

Contract: foundation.#ModuleContract & {
	metadata: {
		name:        "sites-framework"
		displayName: "kombify Sites Framework"
		version:     "0.1.0"
		layer:       "L3-application"
		description: "Optional self-hosted runtime for sites built with the open kombify Sites Framework (deck/topic-artifact Node 24 + sqlite-vec and web SvelteKit + PocketBase profiles)"
		core:        false
		maturity:    "opt-in"
		testScenarios: []
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
			minMemory:         "256m"
			arch:              "any"
		}
	}

	provides: {
		capabilities: {
			"dev-sites":       true
			"sites-framework": true
		}
		endpoints: {
			ui: {
				url:         "https://sites.{{.domain}}"
				description: "Self-hosted kombify Sites Framework workload"
			}
		}
	}

	settings: {
		flexible: {
			siteProfile: *"web" | "deck" | "topic-artifact"
		}
	}

	placementSupport: {
		local_only:         true
		standard:           true
		managed_serverless: true
		managed_app_fit:    false
		rejection_reason:   "Self-hosted kombify Sites Framework runs user-supplied site builds on the operator's server."
	}

	services: {
		"sites-web": foundation.#ServiceDefinition & {
			name:        "sites-web"
			displayName: "Sites Web (SvelteKit Node 24)"
			description: "Pinned Node 24 SvelteKit runtime for kombify Sites Framework web/deck/topic-artifact sites"
			type:        "application"
			image:       "node"
			tag:         "24-alpine"
			required:    false
			status:      "beta"
			needs: ["traefik", "sites-pocketbase"]

			placement: {
				nodeType: "main"
				strategy: "single"
			}

			network: {
				traefik: {
					enabled: true
					rule:    "Host(`sites.{{.domain}}`)"
					port:    3000
				}
				networks: ["base_net", "sites-internal"]
			}

			accessPolicy: {
				outerAuth:      "tinyauth-pocketid"
				appAuth:        "self-auth"
				ownerBootstrap: "Site runtime connects to local PocketBase (web) or sqlite-vec volume (deck/topic-artifact)."
			}

			environment: {
				POCKETBASE_URL: "http://sites-pocketbase:8090"
			}

			volumes: [{
				source:      "sites-sqlite-data"
				target:      "/data/sqlite"
				type:        "volume"
				backup:      true
				description: "SQLite / sqlite-vec storage for deck and topic-artifact profiles"
			}]

			healthCheck: {
				enabled: true
				http: {
					path:   "/"
					port:   3000
					scheme: "http"
				}
				interval: "30s"
				timeout:  "5s"
				retries:  5
			}

			resources: {
				memory:    "128m"
				memoryMax: "512m"
				cpus:      0.5
			}

			security: {
				noNewPrivileges: true
				capDrop: ["ALL"]
			}

			labels: {
				"stackkit.layer":      "3-application"
				"stackkit.managed-by": "selected-paas"
			}
		}

		"sites-pocketbase": foundation.#ServiceDefinition & {
			name:        "sites-pocketbase"
			displayName: "Sites PocketBase"
			description: "PocketBase backend for kombify Sites Framework web profile (pb_data, pb_migrations, pb_hooks)"
			type:        "database"
			image:       "ghcr.io/muchobien/pocketbase"
			tag:         "latest"
			required:    false
			status:      "beta"
			needs: []

			placement: {
				nodeType: "main"
				strategy: "single"
			}

			network: {
				networks: ["sites-internal"]
			}

			volumes: [
				{
					source:      "sites-pb-data"
					target:      "/pb/pb_data"
					type:        "volume"
					backup:      true
					description: "PocketBase SQLite database and uploaded assets (backup with SQL dump snapshot)"
				},
				{
					source:      "sites-pb-migrations"
					target:      "/pb/pb_migrations"
					type:        "volume"
					backup:      true
					description: "PocketBase schema migrations"
				},
				{
					source:      "sites-pb-hooks"
					target:      "/pb/pb_hooks"
					type:        "volume"
					backup:      true
					description: "PocketBase JS hooks"
				},
			]

			healthCheck: {
				enabled: true
				http: {
					path:   "/api/health"
					port:   8090
					scheme: "http"
				}
				interval: "30s"
				timeout:  "5s"
				retries:  5
			}

			resources: {
				memory:    "64m"
				memoryMax: "256m"
				cpus:      0.25
			}

			security: {
				noNewPrivileges: true
				capDrop: ["ALL"]
			}

			labels: {
				"stackkit.layer":      "3-application"
				"stackkit.managed-by": "selected-paas"
			}
		}
	}
}
