// Package immich -- Immich photo management module.
//
// Transitional module contract mirroring the currently deployed Base Kit app.
package immich

import "github.com/kombifyio/stackkits/base"

Contract: base.#ModuleContract & {
	metadata: {
		name:        "immich"
		displayName: "Immich"
		version:     "1.0.1"
		layer:       "L3-application"
		description: "Self-hosted photo and video management with AI-powered search and mobile backup"
		maturity:    "default"
		testScenarios: ["SK-S1", "SK-S2", "SK-S4"]
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
			"photos":           true
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

	services: {
		immich: base.#ServiceDefinition & {
			name:  "immich"
			type:  "application"
			image: "ghcr.io/immich-app/immich-server"
			tag:   "v2.7.0@sha256:ee60b98e7fcc836d61d7f5e7689514f3de7a9480f31ec6ca62d6221056b46ae1"
			upstream: {
				github: {repo: "immich-app/immich"}
				track:   "patch"
				pinLine: "v2.7"
			}
			required: false
			status:   "implemented"
			needs: ["traefik", "immich-machine-learning", "immich-postgres", "immich-redis"]

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
				networks: ["base_net", "immich-internal"]
			}

			accessPolicy: {
				outerAuth:      "tinyauth-pocketid"
				appAuth:        "self-auth"
				ownerBootstrap: "init-immich creates ci/admin owner from StackKit admin credentials and completes onboarding."
			}

			volumes: [{
				source:      "immich-upload"
				target:      "/usr/src/app/upload"
				type:        "volume"
				backup:      true
				description: "Immich uploads"
			}]

			environment: {
				DB_HOSTNAME:                 "immich-postgres"
				DB_PORT:                     "5432"
				DB_USERNAME:                 "immich"
				DB_PASSWORD:                 "{{.immich_db_password}}"
				DB_DATABASE_NAME:            "immich"
				REDIS_HOSTNAME:              "immich-redis"
				REDIS_PORT:                  "6379"
				IMMICH_MACHINE_LEARNING_URL: "http://immich-machine-learning:3003"
			}

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

			config: {
				serviceGroup: "photos"
				routeRole:    "photos"
				replacementContract: {
					stableRouteKey:  "photos"
					stableLocalSlug: "photos"
					requiredCapabilities: ["photos", "photo-management"]
					bootstrapRequirements: ["create-first-admin", "complete-server-onboarding", "complete-user-onboarding"]
					note: "Any replacement photo module must keep the photos route role and document equivalent owner bootstrap steps."
				}
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
				"stackkit.managed-by":                                   "selected-paas"
			}

			subdomain: {key: "photos", nested: "photos", flat: "photos"}
			dashboard: {icon: "&#128247;", order: 50, section: "Applications", badge: "L3 \u00b7 Photos", enableVar: "enable_immich", guideUrl: "https://docs.kombify.io/guides/stackkits/services/immich"}

			output: {
				url:         "https://photos.{{.domain}}"
				description: "Immich photo management"
			}
		}

		"immich-machine-learning": base.#ServiceDefinition & {
			name:  "immich-machine-learning"
			type:  "application"
			image: "ghcr.io/immich-app/immich-machine-learning"
			tag:   "v2.7.0@sha256:aff861526d690bb720130a46bd48ee2827c44d2f601a194e61f31e979a591952"
			upstream: {
				github: {repo: "immich-app/immich"}
				track:   "patch"
				pinLine: "v2.7"
			}
			required: true
			status:   "implemented"

			placement: {
				nodeType: "all"
				strategy: "single"
			}

			network: {networks: ["immich-internal"]}

			volumes: [{
				source:      "immich-model-cache"
				target:      "/cache"
				type:        "volume"
				backup:      false
				description: "Immich ML model cache"
			}]

			// The ML image carries its own /health probe. The rendered Compose
			// keeps that image healthcheck enabled rather than replacing it.
			healthCheck: {enabled: true}

			security: {
				noNewPrivileges: true
				capDrop: ["ALL"]
			}

			labels: {
				"stackkit.layer":      "3-application"
				"stackkit.managed-by": "selected-paas"
			}

			output: {description: "Immich machine-learning worker (internal)"}
		}

		"immich-postgres": base.#ServiceDefinition & {
			name:  "immich-postgres"
			type:  "database"
			image: "ghcr.io/immich-app/postgres"
			tag:   "16-vectorchord0.3.0-pgvectors0.3.0"
			upstream: {
				github: {repo: "immich-app/immich"}
				pinLine: "16-vectorchord0.3.0-pgvectors0.3.0"
			}
			required: true
			status:   "implemented"

			placement: {
				nodeType: "all"
				strategy: "single"
			}

			network: {networks: ["immich-internal"]}

			volumes: [{
				source:      "immich-postgres-data"
				target:      "/var/lib/postgresql/data"
				type:        "volume"
				backup:      true
				description: "Immich PostgreSQL data"
			}]

			environment: {
				POSTGRES_USER:        "immich"
				POSTGRES_PASSWORD:    "{{.immich_db_password}}"
				POSTGRES_DB:          "immich"
				DB_DATABASE_NAME:     "immich"
				DB_USERNAME:          "immich"
				DB_PASSWORD:          "{{.immich_db_password}}"
				POSTGRES_INITDB_ARGS: "--data-checksums"
			}

			healthCheck: {
				enabled:     true
				command:     "pg_isready -U immich -d postgres"
				interval:    "10s"
				timeout:     "5s"
				retries:     5
				startPeriod: "10s"
			}

			security: {
				noNewPrivileges: true
				capDrop: ["ALL"]
			}

			labels: {
				"stackkit.layer":      "3-application"
				"stackkit.managed-by": "selected-paas"
			}

			output: {description: "Immich PostgreSQL database (internal)"}
		}

		// One-shot database reconciliation retained from the canonical Compose
		// rollout. The Immich PostgreSQL image normally creates POSTGRES_DB on a
		// fresh volume; this idempotent unit also repairs an already-initialized
		// cluster where the application database is absent.
		"immich-postgres-init": base.#ServiceDefinition & {
			name:  "immich-postgres-init"
			type:  "automation"
			image: "ghcr.io/immich-app/postgres"
			tag:   "16-vectorchord0.3.0-pgvectors0.3.0"
			upstream: {
				github: {repo: "immich-app/immich"}
				pinLine: "16-vectorchord0.3.0-pgvectors0.3.0"
			}
			required: true
			status:   "implemented"
			needs: ["immich-postgres"]

			placement: {
				nodeType: "all"
				strategy: "single"
			}

			network: {networks: ["immich-internal"]}
			restartPolicy: "no"

			environment: {
				PGPASSWORD: "{{.immich_db_password}}"
			}

			command: [
				"sh",
				"-c",
				"until pg_isready -h immich-postgres -U immich -d postgres; do sleep 1; done; psql -h immich-postgres -U immich -d postgres -tAc \"SELECT 1 FROM pg_database WHERE datname = 'immich'\" | grep -q 1 || createdb -h immich-postgres -U immich immich",
			]

			security: {
				noNewPrivileges: true
				capDrop: ["ALL"]
			}

			labels: {
				"stackkit.layer":      "3-application"
				"stackkit.managed-by": "selected-paas"
			}

			output: {description: "Idempotent Immich database initialization (one-shot)"}
		}

		"immich-redis": base.#ServiceDefinition & {
			name:  "immich-redis"
			type:  "cache"
			image: "ghcr.io/valkey-io/valkey"
			tag:   "9"
			upstream: {github: {repo: "valkey-io/valkey"}}
			required: true
			status:   "implemented"

			placement: {
				nodeType: "all"
				strategy: "single"
			}

			network: {networks: ["immich-internal"]}
			command: ["valkey-server"]

			healthCheck: {
				enabled: true
				test: ["CMD", "redis-cli", "ping"]
				interval: "10s"
				timeout:  "5s"
				retries:  5
			}

			security: {
				noNewPrivileges: true
				capDrop: ["ALL"]
			}

			labels: {
				"stackkit.layer":      "3-application"
				"stackkit.managed-by": "selected-paas"
			}

			output: {description: "Immich Valkey cache (internal)"}
		}
	}

	provisioners: "init-immich": base.#ProvisionerService & {
		image:     "python:3.11-alpine"
		dependsOn: "immich"
		networks: ["base_net"]
		environment: {
			IMMICH_URL:  "http://immich:2283"
			IMMICH_USER: "{{.adminEmail}}"
			IMMICH_PASS: "{{.adminPassword}}"
			IMMICH_NAME: "StackKit Admin"
		}
		command: """
			python3 - <<'PYEOF'
			import json, os, urllib.request

			base = os.environ["IMMICH_URL"].rstrip("/")
			user = os.environ["IMMICH_USER"]
			password = os.environ["IMMICH_PASS"]
			name = os.environ.get("IMMICH_NAME", "StackKit Admin")

			def request(path, method="GET", payload=None, token=""):
			    data = json.dumps(payload).encode("utf-8") if payload is not None else None
			    headers = {"Content-Type": "application/json"}
			    if token:
			        headers["Authorization"] = f"Bearer {token}"
			    req = urllib.request.Request(f"{base}{path}", data=data, headers=headers, method=method)
			    with urllib.request.urlopen(req, timeout=10) as resp:
			        text = resp.read().decode("utf-8")
			        return resp.status, json.loads(text) if text else {}

			_, config = request("/api/server/config")
			if not config.get("isInitialized"):
			    request("/api/auth/admin-sign-up", "POST", {"email": user, "password": password, "name": name})
			_, login = request("/api/auth/login", "POST", {"email": user, "password": password})
			token = login["accessToken"]
			request("/api/users/me", "PUT", {"name": name, "password": password}, token)
			request("/api/users/me/onboarding", "PUT", {"isOnboarded": True}, token)
			request("/api/system-metadata/admin-onboarding", "POST", {"isOnboarded": True}, token)
			PYEOF
			"""
	}
}
