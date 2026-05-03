// Package lldap — LLDAP lightweight LDAP directory module.
//
// Central user and group directory for the homelab. Provides LDAP protocol
// for service integration and a web UI for user management.
//
// Integration chain:
//   LLDAP (directory) → PocketID (OIDC, syncs users via LDAP)
//                     → TinyAuth (ForwardAuth, validates credentials via LDAP)
//
// LLDAP is the source of truth for users and groups. PocketID and TinyAuth
// consume its data — they never write to LLDAP.
//
// Default groups: homelab_owner, homelab_operator, homelab_developer, homelab_viewer
//
// Reference: IDENTITY-STACKKITS.md §2
package lldap

import "github.com/kombifyio/stackkits/base"

// Contract declares what this module requires and provides.
Contract: base.#ModuleContract & {
	metadata: {
		name:        "lldap"
		displayName: "LLDAP"
		version:     "1.0.0"
		layer:       "L1-foundation"
		description: "Lightweight LDAP directory — source of truth for users and groups"
	}

	// No service dependencies — this is L1 foundation (like socket-proxy).
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
			"directory":      true
			"ldap-directory": true
			"user-groups":    true
			"ldap":           true
			"user-storage":   true
		}
		endpoints: {
			ui: {
				url:         "https://ldap.{{.domain}}"
				description: "LLDAP web administration UI"
			}
			ldap: {
				url:         "ldap://lldap:3890"
				internal:    true
				description: "LDAP protocol endpoint (container-to-container)"
			}
		}
	}

	settings: {
		perma: {
			adminPassword: string
			jwtSecret:     string
			baseDN:        *"dc=stack,dc=local" | string
		}
		flexible: {
			ldapPort:  *3890 | int
			httpPort:  *17170 | int
			ldapsPort: *6360 | int
		}
	}

	contexts: {
		local: {}
		cloud: {}
		pi: {
			_resources: {
				memory:    "128m"
				memoryMax: "256m"
			}
		}
	}

	services: lldap: base.#ServiceDefinition & {
		name:     "lldap"
		type:     "directory"
		image:    "lldap/lldap"
		tag:      "latest"
		required: false
		status:   "planned"
		needs: ["traefik"]

		placement: {
			nodeType: "all"
			strategy: "single"
		}

		network: {
			traefik: {
				enabled: true
				rule:    "Host(`ldap.{{.domain}}`)"
				port:    17170
			}
			networks: ["frontend"]
		}

		volumes: [{
			source:      "lldap-data"
			target:      "/data"
			type:        "volume"
			backup:      true
			description: "LLDAP SQLite database and configuration"
		}]

		environment: {
			TZ:                   "{{.timezone}}"
			LLDAP_LDAP_USER_PASS: "{{.lldap_admin_password}}"
			LLDAP_JWT_SECRET:     "{{.lldap_jwt_secret}}"
			LLDAP_LDAP_BASE_DN:   "{{.lldap_base_dn}}"
			LLDAP_HTTP_URL:       "https://ldap.{{.domain}}"
		}

		healthCheck: {
			enabled: true
			test: ["CMD", "/app/lldap", "healthcheck", "--config-file", "/data/lldap_config.toml"]
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
			"traefik.enable":                                       "true"
			"traefik.http.routers.lldap.rule":                      "Host(`ldap.{{.domain}}`)"
			"traefik.http.routers.lldap.entrypoints":               "web"
			"traefik.http.services.lldap.loadbalancer.server.port": "17170"
		}

		output: {
			url:         "https://ldap.{{.domain}}"
			description: "LLDAP directory admin UI"
		}
	}
}
