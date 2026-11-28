// Package coolify — Optional Layer-3 Marketplace & Custom-Apps extensibility module.
//
// Per ADR-0054 §3 and ADR-0049 (amended), Coolify 4.7.5+ is solely an optional
// Layer-3 extensibility module for the 359-template Marketplace and custom
// Git/Compose applications. It NEVER manages curated StackKits core apps.
//
// Routing & isolation:
//   - Kit-Traefik + Access Gate owns host ports 80/443, TLS, and auth policy.
//   - Coolify's internal Traefik runs HTTP-only (Option A, ACME disabled, zero
//     host ports 80/443) behind the dedicated zero-custody cookie-scrubber
//     sidecar (`stackkit-server --mode=cookie-scrubber`).
//   - PocketID OIDC admits only `owners` and `admins` (`AUTOUPDATE=false`).
package coolify

import (
	"strings"
	"github.com/kombifyio/stackkits/foundation"
)

Contract: foundation.#ModuleContract & {
	placementSupport: {
		managed_app_fit:  false
		rejection_reason: "Coolify requires dedicated Docker daemon orchestration and operates as an optional Layer-3 extensibility module (isolated Extension VM or single-node trusted-owner-only)."
	}

	metadata: {
		name:        "coolify"
		displayName: "Coolify"
		version:     "4.7.5"
		layer:       "L3-application"
		description: "Optional Layer-3 Marketplace (359 templates) and Custom-Apps/Git-build extensibility behind Kit-Traefik and zero-custody cookie-scrubber (never manages curated core apps)"
		core:        false
		maturity:    "opt-in"
	}

	delivery: {
		type:      "stackkit-iac"
		managedBy: "stackkit"
	}

	requires: {
		services: {
			traefik: {
				minVersion: "3.0"
				provides: ["reverse-proxy"]
			}
			pocketid: {
				provides: ["oidc"]
			}
		}
		infrastructure: {
			docker:            true
			dockerSocket:      true
			persistentStorage: true
			network:           "coolify-edge"
			minMemory:         "1536m"
		}
	}

	provides: {
		capabilities: {
			"marketplace-extensibility":    true
			"custom-apps":                  true
			"git-deploy":                   true
			"docker-compose-extensibility": true
		}
		endpoints: {
			dashboard: {
				url:         "https://marketplace.{{.domain}}"
				description: "Coolify 4.7.5+ Marketplace & Custom-Apps UI (PocketID OIDC owners/admins, behind Kit-Traefik ForwardAuth)"
			}
			api: {
				url:         "http://coolify:8080"
				internal:    true
				description: "Coolify internal API (private network only)"
			}
			cookieScrubber: {
				url:         "http://coolify-cookie-scrubber:8089"
				internal:    true
				description: "Isolated zero-custody cookie-scrubber sidecar bridging basement-core to coolify-edge"
			}
		}
	}

	settings: {
		perma: {
			coolifyVersion:         "4.7.5"
			coolifyImageDigest:     "sha256:ac2259a438f0221e73eca2838c0c929ff2fd304f8345daabc85c910b6c83d6ee"
			autoUpdate:             false
			managesCuratedCoreApps: false
			proxyMode:              "option-a-http-only"
			trustMode:              *"isolated-extension-vm" | "single-node-trusted-owner"
			oidcAllowedGroups: ["owners", "admins"]
			postgresVersion: *"15-alpine" | string
			redisVersion:    *"7-alpine" | string
		}
		flexible: {
			agentProjectName: *"kombify-agent-apps" | string
			tinyAuthCookieNames: *["tinyauth", "tinyauth_session"] | [...string]
		}
	}

	contexts: {
		local: {}
		cloud: {}
		pi: {}
	}

	presentation: {
		guideUrl: "https://docs.kombify.io/stackkits/apps/marketplace"
		vendor: {
			name:    "Coolify"
			website: "https://coolify.io"
			docs:    "https://coolify.io/docs"
			license: "Apache-2.0"
		}
	}

	services: {
		coolify: foundation.#ServiceDefinition & {
			name:     "coolify"
			type:     "application"
			image:    "coollabsio/coolify"
			tag:      "4.7.5"
			upstream: {
				github: {repo: "coollabsio/coolify"}
			}
			required: false
			status:   "implemented"
			needs: ["traefik", "pocketid"]

			placement: {
				nodeType: "all"
				strategy: "single"
			}

			network: {
				traefik: {
					enabled: true
					rule:    "Host(`marketplace.{{.domain}}`)"
					port:    8080
				}
				networks: ["coolify-edge", "coolify-internal"]
			}

			accessPolicy: {
				outerAuth:      "tinyauth-pocketid"
				appAuth:        "oidc-sso"
				ownerBootstrap: "PocketID OIDC admits only owners and admins; OIDC registration and root-team auto-join stay off. Coolify never manages curated StackKits core apps."
			}

			volumes: [
				{
					source:      "coolify-ssh"
					target:      "/var/www/html/storage/app/ssh"
					type:        "volume"
					backup:      true
					description: "Coolify SSH key storage"
				},
				{
					source:      "coolify-applications"
					target:      "/var/www/html/storage/app/applications"
					type:        "volume"
					backup:      true
					description: "Coolify custom applications storage"
				},
				{
					source:      "coolify-databases"
					target:      "/var/www/html/storage/app/databases"
					type:        "volume"
					backup:      true
					description: "Coolify user database definitions"
				},
				{
					source:      "coolify-services"
					target:      "/var/www/html/storage/app/services"
					type:        "volume"
					backup:      true
					description: "Coolify Marketplace service definitions"
				},
				{
					source:      "coolify-backups"
					target:      "/var/www/html/storage/app/backups"
					type:        "volume"
					backup:      true
					description: "Coolify local backup staging"
				},
				{
					source:      "coolify-images"
					target:      "/var/www/html/storage/app/images"
					type:        "volume"
					backup:      true
					description: "Coolify custom image assets"
				},
			]

			environment: {
				APP_NAME:     "Marketplace"
				APP_ENV:      "production"
				APP_URL:      "https://marketplace.{{.domain}}"
				AUTOUPDATE:   "false"
				DB_HOST:      "coolify-postgres"
				DB_PORT:      "5432"
				DB_DATABASE:  "coolify"
				DB_USERNAME:  "coolify"
				REDIS_HOST:   "coolify-redis"
			}

			healthCheck: {
				enabled: true
				http: {
					path:   "/api/health"
					port:   8080
					scheme: "http"
				}
				interval:    "30s"
				timeout:     "10s"
				retries:     3
				startPeriod: "60s"
			}

			resources: {
				memory:    "1536m"
				memoryMax: "2g"
				cpus:      1.0
			}

			security: {
				noNewPrivileges: true
				capDrop: ["ALL"]
			}

			labels: {
				"traefik.enable":                                         "true"
				"traefik.http.routers.coolify.rule":                      "Host(`marketplace.{{.domain}}`)"
				"traefik.http.routers.coolify.entrypoints":               "websecure"
				"traefik.http.services.coolify.loadbalancer.server.port": "8080"
				"stackkit.layer":                                         "3-application"
				"stackkit.managed-by":                                    "stackkit"
				"stackkit.extensibility.curated-core-apps":               "false"
			}

			subdomain: {key: "marketplace", nested: "marketplace", flat: "marketplace"}
			dashboard: {icon: "&#128722;", order: 60, section: "Applications", badge: "L3 \u00b7 Marketplace", enableVar: "enable_coolify"}

			output: {
				url:         "https://marketplace.{{.domain}}"
				description: "Coolify 4.7.5+ Optional Layer-3 Marketplace & Custom-Apps extensibility"
			}
		}

		"coolify-cookie-scrubber": foundation.#ServiceDefinition & {
			name:     "coolify-cookie-scrubber"
			type:     "reverse-proxy"
			image:    "docker.io/library/alpine"
			tag:      "3.24"
			required: false
			status:   "implemented"
			needs: ["traefik"]

			placement: {
				nodeType: "all"
				strategy: "single"
			}

			network: {
				networks: ["basement-core", "coolify-edge"]
			}

			command: [
				"/usr/local/bin/stackkit-server",
				"--mode=cookie-scrubber",
				"--scrubber-listen=0.0.0.0:8089",
				"--scrubber-upstream=http://coolify-proxy:80",
				"--scrubber-tinyauth-cookies=" + strings.Join(settings.flexible.tinyAuthCookieNames, ","),
			]

			// Zero-custody sidecar: mounts only the read-only stackkit-server
			// binary; zero custody dir, zero signer socket, zero Docker socket,
			// zero MCP token.
			volumes: [
				{
					source:      "./stackkit-server"
					target:      "/usr/local/bin/stackkit-server"
					type:        "bind"
					readOnly:    true
					backup:      false
					description: "Read-only stackkit-server binary run in --mode=cookie-scrubber"
				},
			]

			healthCheck: {
				enabled: true
				http: {
					path:   "/.well-known/stackkit/cookie-scrubber/healthz"
					port:   8089
					scheme: "http"
				}
				interval: "15s"
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
				readOnly:        true
				capDrop: ["ALL"]
			}

			output: {
				description: "Zero-custody cookie-scrubber sidecar between Kit-Traefik and Coolify internal HTTP proxy"
			}
		}

		"coolify-postgres": foundation.#ServiceDefinition & {
			name:     "coolify-postgres"
			type:     "database"
			image:    "postgres"
			tag:      "15-alpine"
			required: false
			status:   "implemented"

			placement: {
				nodeType: "all"
				strategy: "single"
			}

			network: {
				networks: ["coolify-internal"]
			}

			volumes: [
				{
					source:      "coolify-postgres-data"
					target:      "/var/lib/postgresql/data"
					type:        "volume"
					backup:      true
					description: "Coolify PostgreSQL 15 database (backed up atomically with APP_KEY and API token custody)"
				},
			]

			environment: {
				POSTGRES_DB:   "coolify"
				POSTGRES_USER: "coolify"
			}

			healthCheck: {
				enabled:  true
				command:  "pg_isready -U coolify"
				interval: "10s"
				timeout:  "5s"
				retries:  5
			}

			resources: {
				memory:    "512m"
				memoryMax: "512m"
				cpus:      0.5
			}

			security: {
				noNewPrivileges: true
				capDrop: ["ALL"]
			}

			output: {
				description: "Coolify PostgreSQL 15 (internal only)"
			}
		}

		"coolify-redis": foundation.#ServiceDefinition & {
			name:     "coolify-redis"
			type:     "cache"
			image:    "redis"
			tag:      "7-alpine"
			required: false
			status:   "implemented"

			placement: {
				nodeType: "all"
				strategy: "single"
			}

			network: {
				networks: ["coolify-internal"]
			}

			volumes: [
				{
					source:      "coolify-redis-data"
					target:      "/data"
					type:        "volume"
					backup:      true
					description: "Coolify Redis 7 state"
				},
			]

			healthCheck: {
				enabled:  true
				command:  "redis-cli ping"
				interval: "10s"
				timeout:  "5s"
				retries:  3
			}

			resources: {
				memory:    "256m"
				memoryMax: "256m"
				cpus:      0.25
			}

			security: {
				noNewPrivileges: true
				capDrop: ["ALL"]
				readOnly: true
				tmpfs: ["/tmp"]
			}

			output: {
				description: "Coolify Redis 7 cache (internal only)"
			}
		}
	}

	enabled: false
}
