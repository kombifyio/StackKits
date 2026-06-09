// Package uptimekuma -- Uptime Kuma monitoring module.
//
// Provides self-hosted uptime monitoring with status pages and notifications.
// Requires Traefik for reverse proxy routing.
//
// PROVEN CONFIG: Validated via reference-compose.yml.
package uptimekuma

import "github.com/kombifyio/stackkits/base"

// Contract declares what this module requires and provides.
Contract: base.#ModuleContract & {
	metadata: {
		name:        "uptime-kuma"
		displayName: "Uptime Kuma"
		version:     "1.0.0"
		layer:       "L2-platform-observability"
		description: "Self-hosted uptime monitoring with status pages and notifications"
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
			persistentStorage: true
			network:           "shared"
		}
	}

	provides: {
		capabilities: {
			"monitoring":         true
			"uptime-checks":      true
			"status-pages":       true
			"notifications":      true
			"outer-auth-handoff": true
		}
		endpoints: {
			ui: {
				url:         "https://kuma.{{.domain}}"
				description: "Uptime Kuma dashboard"
			}
		}
	}

	settings: {
		flexible: {
			logLevel: *"info" | "debug" | "warn" | "error"
		}
	}

	contexts: {
		local: {}
		cloud: {}
		pi: {}
	}

	services: "uptime-kuma": base.#ServiceDefinition & {
		name:     "uptime-kuma"
		type:     "monitoring"
		image:    "louislam/uptime-kuma"
		tag:      "1"
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
				rule:    "Host(`kuma.{{.domain}}`)"
				port:    3001
			}
			networks: ["base_net"]
		}

		volumes: [{
			source:      "uptime-kuma-data"
			target:      "/app/data"
			type:        "volume"
			backup:      true
			description: "Uptime Kuma database and config"
		}]

		environment: {
			"UPTIME_KUMA_DB_TYPE": "sqlite"
		}

		healthCheck: {
			enabled: true
			http: {
				path:   "/"
				port:   3001
				scheme: "http"
			}
			interval: "30s"
			timeout:  "10s"
			retries:  3
		}

		config: {
			accessPolicy: {
				outerAuth: "tinyauth-pocketid"
				appAuth:   "disabled-after-bootstrap"
				reason:    "BaseKit protects kuma.<domain> with TinyAuth/PocketID, then init-kuma disables the Kuma app login to avoid a second login prompt."
			}
			serviceGroup: "monitoring"
			routeRole:    "kuma"
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

		labels: {
			"traefik.enable":                                             "true"
			"traefik.http.routers.uptime-kuma.rule":                      "Host(`kuma.{{.domain}}`)"
			"traefik.http.routers.uptime-kuma.entrypoints":               "web"
			"traefik.http.services.uptime-kuma.loadbalancer.server.port": "3001"
		}

		subdomain: {key: "kuma", nested: "kuma", flat: "kuma"}
		dashboard: {icon: "&#128202;", order: 10, section: "Platform", badge: "L2 \u00b7 Monitoring", enableVar: "enable_uptime_kuma", guideUrl: "https://docs.kombify.io/guides/stackkits/services/uptime-kuma"}

		output: {
			url:         "https://kuma.{{.domain}}"
			description: "Uptime Kuma monitoring dashboard"
		}
	}

	provisioners: "init-kuma": base.#ProvisionerService & {
		image:     "python:3.11-alpine"
		dependsOn: "uptime-kuma"
		networks: ["base_net"]
		environment: {
			KUMA_URL:  "http://uptime-kuma:3001"
			KUMA_USER: "admin"
			KUMA_PASS: "{{.kumaAdminPassword}}"
			DOMAIN:    "{{.domain}}"
		}
		command: """
			pip install -q uptime-kuma-api
			python3 - <<'PYEOF'
			from uptime_kuma_api import UptimeKumaApi, MonitorType
			import os, sys

			api = UptimeKumaApi(os.environ["KUMA_URL"], wait_events=True, timeout=30)
			user = os.environ["KUMA_USER"]
			pw = os.environ["KUMA_PASS"]
			try:
			    api.setup(user, pw)
			except Exception:
			    pass
			api.login(user, pw)
			api.set_settings(password=pw, disableAuth=True, trustProxy=True, entryPage="dashboard")
			# Add StackKit service monitors here.
			api.disconnect()
			PYEOF
			"""
	}
}
