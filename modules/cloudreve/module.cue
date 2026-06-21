// Package cloudreve -- Cloudreve files module.
//
// Transitional module contract for the BaseKit files use case.
package cloudreve

import "github.com/kombifyio/stackkits/base"

Contract: base.#ModuleContract & {
	metadata: {
		name:        "cloudreve"
		displayName: "Cloudreve"
		version:     "1.0.0"
		layer:       "L3-application"
		description: "Lightweight document management and file sharing"
		maturity:    "default"
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
			minMemory:         "256m"
		}
	}

	provides: {
		capabilities: {
			"files":            true
			"document-storage": true
			"file-sharing":     true
		}
		endpoints: {
			ui: {
				url:         "https://files.{{.domain}}"
				description: "Cloudreve web UI"
			}
		}
	}

	settings: {
		flexible: {
			registrationOpen: *false | bool
		}
	}

	contexts: {
		local: {}
		cloud: {}
		pi: {}
	}

	services: cloudreve: base.#ServiceDefinition & {
		name:        "cloudreve"
		displayName: "Files"
		description: "Document management and file sharing backed by Cloudreve"
		type:        "storage"
		image:       "cloudreve/cloudreve"
		tag:         "latest"
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
				port:    5212
			}
			networks: ["base_net"]
		}

		accessPolicy: {
			outerAuth:      "tinyauth-pocketid"
			appAuth:        "self-auth"
			ownerBootstrap: "init-cloudreve creates the first admin from StackKit admin credentials."
		}

		volumes: [{
			source:      "cloudreve-data"
			target:      "/cloudreve/data"
			type:        "volume"
			backup:      true
			description: "Cloudreve configuration, SQLite database, and local file data"
		}]

		healthCheck: {
			enabled: true
			http: {
				path:   "/"
				port:   5212
				scheme: "http"
			}
			interval: "30s"
			timeout:  "5s"
			retries:  5
		}

		config: {
			serviceGroup: "files"
			routeRole:    "files"
			replacementContract: {
				stableRouteKey:  "files"
				stableLocalSlug: "files"
				requiredCapabilities: ["files", "document-storage"]
				bootstrapRequirements: ["create-first-admin", "close-public-registration"]
				note: "Any replacement files module must keep the files route role and document equivalent owner bootstrap steps."
			}
		}

		resources: {
			memory:    "256m"
			memoryMax: "768m"
			cpus:      0.5
		}

		security: {
			noNewPrivileges: true
			capDrop: ["ALL"]
		}

		labels: {
			"traefik.enable":                                           "true"
			"traefik.http.routers.cloudreve.rule":                      "Host(`files.{{.domain}}`)"
			"traefik.http.routers.cloudreve.entrypoints":               "web"
			"traefik.http.services.cloudreve.loadbalancer.server.port": "5212"
			"stackkit.layer":                                           "3-application"
			"stackkit.managed-by":                                      "selected-paas"
		}

		subdomain: {key: "files", nested: "files", flat: "files"}
		dashboard: {icon: "&#128193;", order: 60, section: "Applications", badge: "L3 \u00b7 Files", enableVar: "enable_files", guideUrl: "https://docs.kombify.io/guides/stackkits/services/files"}

		output: {
			url:         "https://files.{{.domain}}"
			description: "Cloudreve file sharing"
		}
	}

	provisioners: "init-cloudreve": base.#ProvisionerService & {
		image:     "python:3.11-alpine"
		dependsOn: "cloudreve"
		networks: ["base_net"]
		environment: {
			CLOUDREVE_URL:  "http://cloudreve:5212"
			CLOUDREVE_USER: "{{.adminEmail}}"
			CLOUDREVE_PASS: "{{.adminPassword}}"
		}
		command: """
			python3 - <<'PYEOF'
			import json, os, time, urllib.error, urllib.request

			base = os.environ["CLOUDREVE_URL"].rstrip("/")
			user = os.environ["CLOUDREVE_USER"]
			password = os.environ["CLOUDREVE_PASS"]

			def request(path, method="GET", payload=None):
			    data = json.dumps(payload).encode("utf-8") if payload is not None else None
			    req = urllib.request.Request(f"{base}/api/v4{path}", data=data, headers={"Content-Type": "application/json"}, method=method)
			    with urllib.request.urlopen(req, timeout=10) as resp:
			        text = resp.read().decode("utf-8")
			        return json.loads(text) if text else {}

			for _ in range(30):
			    try:
			        request("/site/config")
			        break
			    except Exception:
			        time.sleep(2)

			try:
			    request("/user", "POST", {"email": user, "password": password, "language": "en-US"})
			    print("Cloudreve first admin created")
			except urllib.error.HTTPError as exc:
			    body = exc.read().decode("utf-8", "ignore")
			    if "409" in body or "exist" in body.lower():
			        print("Cloudreve admin already exists")
			    else:
			        raise
			except Exception as exc:
			    print(f"Cloudreve admin bootstrap skipped: {exc}")

			PYEOF
			"""
	}
}
