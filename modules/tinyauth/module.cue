// Package tinyauth — the legacy TinyAuth v5 service contract.
//
// Provides authentication via Traefik ForwardAuth middleware.
// Supports passkeys, passwords, OAuth (GitHub, Google, OIDC).
// Uses socket-proxy for Docker label-based access control (never mounts docker.sock directly).
//
// Variable names come from the central pinned v5 service definition.
// Parser evidence does not substitute for native identity/lifecycle acceptance.
package tinyauth

import "github.com/kombifyio/stackkits/foundation"

// Contract declares what this module requires and provides.
Contract: foundation.#ModuleContract & {
	metadata: {
		name:        "tinyauth"
		displayName: "TinyAuth"
		version:     "5.0.7"
		layer:       "L2-platform-identity"
		description: "Lightweight authentication proxy with ForwardAuth, passkeys, and OAuth support"
		maturity:    "default"
		testScenarios: ["SK-S1", "SK-S2", "SK-S3", "SK-S5"]
	}

	requires: {
		services: {
			traefik: {
				minVersion: "3.0"
				provides: ["reverse-proxy", "forwardauth-host"]
			}
			"socket-proxy": {
				provides: ["docker-api-proxy"]
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
			"forwardauth":     true
			"authentication":  true
			"user-management": true
			"passkeys":        true
			"oauth":           true
		}
		middleware: {
			tinyauth: {
				type:               "forwardauth"
				address:            "http://tinyauth:3000/api/auth/traefik"
				trustForwardHeader: true
				authResponseHeaders: [
					"remote-user", "remote-sub", "remote-name",
					"remote-email", "remote-groups",
				]
			}
		}
		endpoints: {
			auth: {
				url:         "https://auth.{{.domain}}"
				description: "TinyAuth login page"
			}
			api: {
				url:         "http://tinyauth:3000"
				internal:    true
				description: "TinyAuth API (internal)"
			}
			forwardauth: {
				url:         "http://tinyauth:3000/api/auth/traefik"
				internal:    true
				description: "ForwardAuth endpoint for Traefik middleware"
			}
		}
	}

	settings: {
		perma: {
			authMode: *"passkeys_plus_legacy" | "passkeys_only" | "password_only"
		}
		flexible: {
			sessionExpiry: *86400 | int
			secureCookie:  *true | bool
			logLevel:      *"info" | "debug" | "warn" | "error"
			trustedProxies: [...string] | *[]
		}
	}

	contexts: {
		local: {
			_secureCookie: true
		}
		cloud: {
			_secureCookie: true
		}
		pi: {
			_secureCookie: true
		}
	}

	services: tinyauth: foundation.#ServiceDefinition & {
		name: "tinyauth"
		type: "auth"
		// Pin matches base/services.cue and the release pre-pull list.
		image: "ghcr.io/steveiliop56/tinyauth"
		tag:   "v5.0.7"
		upstream: {
			github: {repo: "steveiliop56/tinyauth"}
		}
		required: true
		status:   "implemented"
		needs: ["traefik", "socket-proxy"]

		placement: {
			nodeType: "all"
			strategy: "single"
		}

		network: {
			traefik: {
				enabled: true
				rule:    "Host(`auth.{{.domain}}`)"
				port:    3000
			}
			networks: ["frontend", "socket-proxy-net"]
		}

		// TinyAuth IS the login gateway — it must stay anonymously reachable
		// for login flows and keeps its own auth surface.
		accessPolicy: {
			outerAuth: "self"
			appAuth:   "self-auth"
		}

		// No docker.sock — uses DOCKER_HOST=tcp://socket-proxy:2375 for label-based access control
		volumes: [{
			source:      "tinyauth-data"
			target:      "/data"
			type:        "volume"
			backup:      true
			description: "TinyAuth SQLite database and session data"
		}]

		// Share the canonical v5 variable map rather than maintaining a v4 copy.
		// The legacy module keeps its socket proxy and uses the selected trusted
		// issuer for all OAuth legs; TLS verification remains enabled.
		environment: {
			for name, value in foundation.#TinyAuthService.environment
			if name != "TZ" && name != "TINYAUTH_OAUTH_PROVIDERS_POCKETID_TOKENURL" && name != "TINYAUTH_OAUTH_PROVIDERS_POCKETID_USERINFOURL" {
				(name): value
			}
			TZ:                                            "{{.timezone}}"
			TINYAUTH_OIDC_PRIVATEKEYPATH:                  "/data/tinyauth-oidc.key"
			TINYAUTH_OIDC_PUBLICKEYPATH:                   "/data/tinyauth-oidc.pub"
			DOCKER_HOST:                                   "tcp://socket-proxy:2375"
			TINYAUTH_OAUTH_PROVIDERS_POCKETID_TOKENURL:    "{{.tinyauth_oidc_issuer}}/api/oidc/token"
			TINYAUTH_OAUTH_PROVIDERS_POCKETID_USERINFOURL: "{{.tinyauth_oidc_issuer}}/api/oidc/userinfo"
		}

		healthCheck: {
			enabled:  true
			command:  "tinyauth healthcheck"
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
			readOnly: true
			// /data is the persistent tinyauth-data volume (SQLite user store,
			// backup: true) and must NOT be tmpfs-overlaid.
			tmpfs: ["/tmp"]
		}

		labels: {
			"traefik.enable":                                                    "true"
			"traefik.http.routers.tinyauth.rule":                                "Host(`auth.{{.domain}}`)"
			"traefik.http.routers.tinyauth.entrypoints":                         "web"
			"traefik.http.services.tinyauth.loadbalancer.server.port":           "3000"
			"traefik.http.middlewares.tinyauth.forwardauth.address":             "http://tinyauth:3000/api/auth/traefik"
			"traefik.http.middlewares.tinyauth.forwardauth.trustForwardHeader":  "true"
			"traefik.http.middlewares.tinyauth.forwardauth.authResponseHeaders": "remote-user,remote-sub,remote-name,remote-email,remote-groups"
		}

		subdomain: {key: "auth", nested: "auth", flat: "auth"}
		dashboard: {icon: "&#128274;", order: 20, section: "Platform", badge: "L1 \u00b7 ForwardAuth", guideUrl: "https://docs.kombify.io/stackkits/apps/tinyauth"}

		output: {
			url:         "https://auth.{{.domain}}"
			description: "TinyAuth authentication portal"
		}
	}
}
