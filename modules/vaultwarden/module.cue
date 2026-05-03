// Package vaultwarden -- Vaultwarden password manager module.
//
// Transitional module contract mirroring the currently deployed Base Kit app.
package vaultwarden

import "github.com/kombifyio/stackkits/base"

Contract: base.#ModuleContract & {
	metadata: {
		name:        "vaultwarden"
		displayName: "Vaultwarden"
		version:     "1.0.0"
		layer:       "L3-application"
		description: "Bitwarden-compatible password vault for passwords, TOTP, and secure notes"
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
			"password-manager": true
			"vault":            true
		}
		endpoints: {
			ui: {
				url:         "https://vault.{{.domain}}"
				description: "Vaultwarden web UI"
			}
			admin: {
				url:         "https://vault.{{.domain}}/admin"
				description: "Vaultwarden admin UI"
			}
		}
	}

	settings: {
		flexible: {
			signupsAllowed: *false | bool
		}
	}

	contexts: {
		local: {}
		cloud: {}
		pi:    {}
	}

	services: vaultwarden: base.#ServiceDefinition & {
		name:     "vaultwarden"
		type:     "application"
		image:    "vaultwarden/server"
		tag:      "latest"
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
				rule:    "Host(`vault.{{.domain}}`)"
				port:    80
			}
			networks: ["base_net"]
		}

		volumes: [{
			source:      "vaultwarden-data"
			target:      "/data"
			type:        "volume"
			backup:      true
			description: "Vaultwarden database and attachments"
		}]

		environment: {
			DOMAIN:          "https://vault.{{.domain}}"
			SIGNUPS_ALLOWED: "false"
			ADMIN_TOKEN:     "{{.vaultwarden_admin_token}}"
		}

		healthCheck: {
			enabled: true
			http: {
				path:   "/alive"
				port:   80
				scheme: "http"
			}
			interval: "30s"
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
		}

		labels: {
			"traefik.enable":                                             "true"
			"traefik.http.routers.vaultwarden.rule":                      "Host(`vault.{{.domain}}`)"
			"traefik.http.routers.vaultwarden.entrypoints":               "web"
			"traefik.http.services.vaultwarden.loadbalancer.server.port": "80"
			"stackkit.layer":                                             "3-application"
			"stackkit.managed-by":                                        "compose"
		}

		subdomain: {key: "vault", nested: "vault", flat: "vault"}
		dashboard: {icon: "&#128272;", order: 30, section: "Applications", badge: "L3 \u00b7 Vault", enableVar: "enable_vaultwarden"}

		output: {
			url:         "https://vault.{{.domain}}"
			description: "Vaultwarden password manager"
		}
	}
}
