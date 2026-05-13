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
		testScenarios: ["SK-S1", "SK-S2", "SK-S4"]
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
			accessPolicy: {
				appAuth:        "self-auth"
				ownerBootstrap: "init-immich creates ci/admin owner from StackKit admin credentials and completes onboarding."
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
			"stackkit.managed-by":                                   "compose"
		}

		subdomain: {key: "photos", nested: "photos", flat: "photos"}
		dashboard: {icon: "&#128247;", order: 50, section: "Applications", badge: "L3 \u00b7 Photos", enableVar: "enable_immich", guideUrl: "https://docs.kombify.io/guides/stackkits/services/immich"}

		output: {
			url:         "https://photos.{{.domain}}"
			description: "Immich photo management"
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
