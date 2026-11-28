// Package immich -- Immich photo management module.
//
// Transitional module contract mirroring the currently deployed Base Kit app.
package immich

import "github.com/kombifyio/stackkits/foundation"

_runtimeImages: foundation.ArchitectureV2ModuleImages["stackkits-immich-runtime"]

Contract: foundation.#ModuleContract & {
	placementSupport: {
		managed_app_fit:  false
		rejection_reason: "Immich Full requires a dedicated server for media processing and persistent data."
	}
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

	presentation: {
		guideUrl: "https://docs.kombify.io/stackkits/apps/immich"
		clients: [{
			platform: "android"
			kind:     "store"
			url:      "https://play.google.com/store/apps/details?id=app.alextran.immich"
		}, {
			platform: "android"
			kind:     "download"
			url:      "https://github.com/immich-app/immich/releases/latest/download/app-release.apk"
		}, {
			platform: "ios"
			kind:     "store"
			url:      "https://apps.apple.com/us/app/immich/id1613945652"
		}]
		vendor: {
			name:    "Immich"
			website: "https://immich.app"
			docs:    "https://docs.immich.app/"
			license: "AGPL-3.0"
			support: [{
				kind: "purchase"
				url:  "https://buy.immich.app/"
			}]
		}
	}

	services: {
		immich: foundation.#ServiceDefinition & {
			name:  "immich"
			type:  "application"
			image: _runtimeImages["immich-server"].image
			tag:   _runtimeImages["immich-server"].tag
			upstream: {
				github: {repo: "immich-app/immich"}
				track:   "patch"
				pinLine: "v3.3"
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
			dashboard: {icon: "&#128247;", order: 50, section: "Applications", badge: "L3 \u00b7 Photos", enableVar: "enable_immich"}

			output: {
				url:         "https://photos.{{.domain}}"
				description: "Immich photo management"
			}
		}

		"immich-machine-learning": foundation.#ServiceDefinition & {
			name:  "immich-machine-learning"
			type:  "application"
			image: _runtimeImages["immich-machine-learning"].image
			tag:   _runtimeImages["immich-machine-learning"].tag
			upstream: {
				github: {repo: "immich-app/immich"}
				track:   "patch"
				pinLine: "v3.3"
			}
			required: false
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

		"immich-postgres": foundation.#ServiceDefinition & {
			name:  "immich-postgres"
			type:  "database"
			image: _runtimeImages["immich-postgres"].image
			tag:   _runtimeImages["immich-postgres"].tag
			upstream: {
				github: {repo: "immich-app/immich"}
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
		"immich-postgres-init": foundation.#ServiceDefinition & {
			name:  "immich-postgres-init"
			type:  "automation"
			image: _runtimeImages["immich-postgres-init"].image
			tag:   _runtimeImages["immich-postgres-init"].tag
			upstream: {
				github: {repo: "immich-app/immich"}
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

		"immich-redis": foundation.#ServiceDefinition & {
			name:  "immich-redis"
			type:  "cache"
			image: _runtimeImages["immich-valkey"].image
			tag:   _runtimeImages["immich-valkey"].tag
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

	provisioners: "init-immich": foundation.#ProvisionerService & {
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

// AppModuleV3 is the complete #AppModuleV3 declaration of the Photos workload
// (ADR-0055 Stage 1 pilot). internal/engine renders it into the same Compose
// project the standalone Compose owner emits today for
// stackkits-immich-runtime; modules/immich/golden/compose.yaml pins that
// output. The legacy Contract above stays until Phase 2.
AppModuleV3: foundation.#AppModuleV3 & {
	let _images = foundation.AppModuleV3CatalogImages["stackkits-immich-runtime"]
	apiVersion:     "stackkit.app-module/v3"
	moduleRef:      "stackkits-immich-runtime"
	workloadRef:    "photos"
	alternativeRef: "immich"
	release:        "v3.3.4"
	entryComponent: "immich-server"
	delivery: {contract: "opentofu-stage1", resourcePrefix: "workload"}
	lifecycle: {
		requiredSafetyInit: ["immich-postgres-init"]
		optionalAppsetupRef: "setup/immich-owner-bootstrap"
	}
	route: {
		serviceRef:        "photos"
		targetPort:        2283
		upstreamProtocol:  "http"
		ingressAuth:       "forward-auth"
		healthRef:         "immich-http"
		requiredPrivilege: "user"
		allowedExposures: ["local", "remote-private", "public"]
	}
	secrets: "database-password": purpose: "Immich PostgreSQL password shared by the server, the database and its init job"
	backup: {
		volumes: [
			{componentRef: "immich-server", volumeRef:   "library"},
			{componentRef: "immich-postgres", volumeRef: "database"},
		]
		hookRef:  "hook/quiesce-stop"
		probeRef: "probe/http-status"
	}
	networks: "immich-internal": {}
	components: [{
		id:        "immich-server"
		role:      "application"
		lifecycle: "daemon"
		image:     _images["immich-server"]
		dependsOn: ["immich-machine-learning", "immich-postgres-init", "immich-valkey"]
		environment: {
			DB_DATABASE_NAME:            "immich"
			DB_HOSTNAME:                 "immich-postgres"
			DB_PORT:                     "5432"
			DB_USERNAME:                 "immich"
			IMMICH_MACHINE_LEARNING_URL: "http://immich-machine-learning:3003"
			REDIS_HOSTNAME:              "immich-valkey"
			REDIS_PORT:                  "6379"
		}
		secretEnvironment: DB_PASSWORD: "database-password"
		volumes: [{id: "library", target: "/data", class: "persistent", backup: true}]
		networks: ["immich-internal"]
		health: {kind: "http", path: "/api/server/ping", port: 2283}
		resources: {memoryLimit: "3g", memoryReservation: "512m"}
		labels: {}
		traits: {
			homeIdentityAccess: {caBundleTarget: "/etc/stackkit/ca-bundle.pem", caBundleEnvironment: ["NODE_EXTRA_CA_CERTS"]}
			pocketIDClient: {
				callbackURLs: ["{{origin}}/auth/login", "{{origin}}/user-settings", "app.immich:///oauth-callback", "https://kombify.io/app/immich-oauth"]
				environment: IMMICH_CONFIG_FILE: "/etc/stackkit/immich.json"
				configuration: {
					target: "/etc/stackkit/immich.json"
					body:   "{\"oauth\":{\"autoLaunch\":true,\"autoRegister\":true,\"buttonText\":\"Continue with Pocket ID\",\"clientId\":\"{{clientId}}\",\"clientSecret\":\"{{clientSecret}}\",\"enabled\":true,\"issuerUrl\":\"{{issuer}}\",\"mobileOverrideEnabled\":true,\"mobileRedirectUri\":\"https://kombify.io/app/immich-oauth\",\"profileSigningAlgorithm\":\"none\",\"roleClaim\":\"immich_role\",\"scope\":\"openid email profile\",\"signingAlgorithm\":\"RS256\",\"tokenEndpointAuthMethod\":\"client_secret_post\"}}"
				}
			}
		}
	}, {
		id:            "immich-machine-learning"
		role:          "machine-learning"
		lifecycle:     "daemon"
		healthFailure: "degraded"
		image:         _images["immich-machine-learning"]
		environment: {}
		secretEnvironment: {}
		egress: true
		volumes: [{id: "model-cache", target: "/cache", class: "cache"}]
		networks: ["immich-internal"]
		health: {kind: "command", command: ["python3", "healthcheck.py"]}
		resources: {memoryLimit: "3g", memoryReservation: "512m"}
		labels: {}
	}, {
		id:        "immich-postgres"
		role:      "database"
		lifecycle: "daemon"
		image:     _images["immich-postgres"]
		environment: {
			POSTGRES_DB:          "immich"
			POSTGRES_INITDB_ARGS: "--data-checksums"
			POSTGRES_USER:        "immich"
		}
		secretEnvironment: POSTGRES_PASSWORD: "database-password"
		volumes: [{id: "database", target: "/var/lib/postgresql/data", class: "persistent", backup: true}]
		networks: ["immich-internal"]
		// The image declares HEALTHCHECK --start-interval=5s; pinning it keeps
		// the configured value the observed one on both executions.
		health: {kind: "command", command: ["pg_isready", "-U", "immich", "-d", "postgres"], startInterval: "5s"}
		resources: {memoryLimit: "2g", memoryReservation: "256m"}
		labels: {}
	}, {
		id:        "immich-postgres-init"
		role:      "database-init"
		lifecycle: "one-shot"
		image:     _images["immich-postgres-init"]
		command: ["sh", "-c", "until pg_isready -h immich-postgres -U immich -d postgres; do sleep 1; done; psql -h immich-postgres -U immich -d postgres -tAc \"SELECT 1 FROM pg_database WHERE datname = 'immich'\" | grep -q 1 || createdb -h immich-postgres -U immich immich"]
		dependsOn: ["immich-postgres"]
		environment: PGUSER:       "immich"
		secretEnvironment: PGPASSWORD: "database-password"
		networks: ["immich-internal"]
		health: kind: "completion"
		resources: memoryLimit: "256m"
		labels: {}
	}, {
		id:        "immich-valkey"
		role:      "cache"
		lifecycle: "daemon"
		image:     _images["immich-valkey"]
		command: ["valkey-server"]
		environment: {}
		secretEnvironment: {}
		networks: ["immich-internal"]
		health: {kind: "command", command: ["redis-cli", "ping"]}
		resources: {memoryLimit: "512m", memoryReservation: "64m"}
		labels: {}
	}]
}
