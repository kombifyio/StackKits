// Package dokploy — Dokploy PaaS platform module.
//
// Self-hosted Platform-as-a-Service for deploying applications.
// Includes PostgreSQL and Redis as internal dependencies.
// Requires Traefik for ingress routing.
package dokploy

import "github.com/kombifyio/stackkits/base"

Contract: base.#ModuleContract & {
	metadata: {
		name:        "dokploy"
		displayName: "Dokploy"
		version:     "1.0.0"
		layer:       "L2-platform-paas"
		description: "Self-hosted PaaS with PostgreSQL and Redis for deploying applications"
		testScenarios: ["SK-S2"]
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
			dockerSocket:      true
			persistentStorage: true
			network:           "shared"
			minMemory:         "512m"
		}
	}

	provides: {
		capabilities: {
			"paas":           true
			"app-deployment": true
			"docker-compose": true
			"git-deploy":     true
		}
		endpoints: {
			dashboard: {
				url:         "https://dokploy.{{.domain}}"
				description: "Dokploy dashboard for app deployment"
			}
			api: {
				url:         "http://dokploy:3000"
				internal:    true
				description: "Dokploy API (internal)"
			}
		}
	}

	settings: {
		perma: {
			postgresVersion: *"16-alpine" | string
			redisVersion:    *"7-alpine" | string
		}
		flexible: {
			nodeEnv:  *"production" | string
			logLevel: *"info" | "debug" | "warn" | "error"
		}
	}

	contexts: {
		local: {}
		cloud: {}
		pi: {
			_resources: {
				memory:    "384m"
				memoryMax: "768m"
			}
		}
	}

	services: {
		dokploy: base.#ServiceDefinition & {
			name:     "dokploy"
			type:     "paas"
			image:    "dokploy/dokploy"
			tag:      "latest"
			required: true
			status:   "implemented"
			needs: ["traefik"]

			placement: {
				nodeType: "all"
				strategy: "single"
			}

			network: {
				traefik: {
					enabled: true
					rule:    "Host(`dokploy.{{.domain}}`)"
					port:    3000
				}
				networks: ["base_net", "base_net_db"]
			}

			volumes: [
				{
					source:      "/var/run/docker.sock"
					target:      "/var/run/docker.sock"
					type:        "bind"
					readOnly:    false
					backup:      false
					description: "Docker socket for container management"
				},
				{
					source:      "dokploy-data"
					target:      "/app/data"
					type:        "volume"
					backup:      true
					description: "Dokploy application data"
				},
			]

			environment: {
				NODE_ENV:     "production"
				DATABASE_URL: "postgresql://dokploy:{{.dokploy_db_password}}@dokploy-postgres:5432/dokploy"
				REDIS_URL:    "redis://dokploy-redis:6379"
			}

			healthCheck: {
				enabled: true
				http: {
					path:   "/api/health"
					port:   3000
					scheme: "http"
				}
				interval: "30s"
				timeout:  "10s"
				retries:  3
			}

			resources: {
				memory:    "512m"
				memoryMax: "1g"
				cpus:      1.0
			}

			security: {
				noNewPrivileges: true
				capDrop: ["ALL"]
			}

			labels: {
				"traefik.enable":                                         "true"
				"traefik.http.routers.dokploy.rule":                      "Host(`dokploy.{{.domain}}`)"
				"traefik.http.routers.dokploy.entrypoints":               "web"
				"traefik.http.services.dokploy.loadbalancer.server.port": "3000"
			}

			subdomain: {key: "dokploy", nested: "dokploy", flat: "dokploy"}
			dashboard: {icon: "&#128640;", order: 40, section: "Platform", badge: "L2 \u00b7 PaaS", enableVar: "enable_dokploy", guideUrl: "https://docs.kombify.io/guides/stackkits/services/dokploy"}

			output: {
				url:         "https://dokploy.{{.domain}}"
				description: "Dokploy PaaS dashboard"
			}
		}

		"dokploy-postgres": base.#ServiceDefinition & {
			name:     "dokploy-postgres"
			type:     "database"
			image:    "postgres"
			tag:      "16-alpine"
			required: true
			status:   "implemented"

			placement: {
				nodeType: "all"
				strategy: "single"
			}

			network: {
				networks: ["base_net_db"]
			}

			volumes: [
				{
					source:      "dokploy-postgres-data"
					target:      "/var/lib/postgresql/data"
					type:        "volume"
					backup:      true
					description: "PostgreSQL data directory"
				},
			]

			environment: {
				POSTGRES_DB:       "dokploy"
				POSTGRES_USER:     "dokploy"
				POSTGRES_PASSWORD: "{{.dokploy_db_password}}"
			}

			healthCheck: {
				enabled:  true
				command:  "pg_isready -U dokploy"
				interval: "10s"
				timeout:  "5s"
				retries:  5
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

			output: {
				description: "Dokploy PostgreSQL (internal, no external access)"
			}
		}

		"dokploy-redis": base.#ServiceDefinition & {
			name:     "dokploy-redis"
			type:     "cache"
			image:    "redis"
			tag:      "7-alpine"
			required: true
			status:   "implemented"

			placement: {
				nodeType: "all"
				strategy: "single"
			}

			network: {
				networks: ["base_net_db"]
			}

			volumes: [
				{
					source:      "dokploy-redis-data"
					target:      "/data"
					type:        "volume"
					backup:      false
					description: "Redis data (cache, non-critical)"
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
				memory:    "128m"
				memoryMax: "256m"
				cpus:      0.25
			}

			security: {
				noNewPrivileges: true
				capDrop: ["ALL"]
				readOnly: true
				tmpfs: ["/data"]
			}

			output: {
				description: "Dokploy Redis cache (internal, no external access)"
			}
		}
	}
}
