// Package tinyauth — TinyAuth v4 ForwardAuth module.
//
// Provides authentication via Traefik ForwardAuth middleware.
// Supports passkeys, passwords, OAuth (GitHub, Google, OIDC).
// Uses socket-proxy for Docker label-based access control (never mounts docker.sock directly).
//
// PROVEN CONFIG: Validated via reference-compose.yml (8/8 tests pass).
package tinyauth

import "github.com/kombifyio/stackkits/base"

// Contract declares what this module requires and provides.
Contract: base.#ModuleContract & {
	metadata: {
		name:        "tinyauth"
		displayName: "TinyAuth"
		version:     "4.1.0"
		layer:       "L2-platform-identity"
		description: "Lightweight authentication proxy with ForwardAuth, passkeys, and OAuth support"
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
			sessionExpiry:  *86400 | int
			secureCookie:   *true | bool
			logLevel:       *"info" | "debug" | "warn" | "error"
			trustedProxies: [...string] | *[]
		}
	}

	contexts: {
		local: {
			_secureCookie: false
		}
		cloud: {
			_secureCookie: true
		}
		pi: {
			_secureCookie: false
		}
	}

	services: tinyauth: base.#ServiceDefinition & {
		name:     "tinyauth"
		type:     "auth"
		image:    "ghcr.io/steveiliop56/tinyauth"
		tag:      "v4"
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

		// No docker.sock — uses DOCKER_HOST=tcp://socket-proxy:2375 for label-based access control
		volumes: [{
			source:      "tinyauth-data"
			target:      "/data"
			type:        "volume"
			backup:      true
			description: "TinyAuth SQLite database and session data"
		}]

		environment: {
			TZ:             "{{.timezone}}"
			APP_URL:        "https://auth.{{.domain}}"
			USERS:          "{{.tinyauth_users}}"
			SECURE_COOKIE:  "{{.tinyauth_secure_cookie}}"
			SESSION_EXPIRY: "{{.tinyauth_session_expiry}}"
			DOCKER_HOST:    "tcp://socket-proxy:2375"
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
			tmpfs: ["/tmp", "/data"]
		}

		labels: {
			"traefik.enable":                                                        "true"
			"traefik.http.routers.tinyauth.rule":                                    "Host(`auth.{{.domain}}`)"
			"traefik.http.routers.tinyauth.entrypoints":                             "web"
			"traefik.http.services.tinyauth.loadbalancer.server.port":               "3000"
			"traefik.http.middlewares.tinyauth.forwardauth.address":                 "http://tinyauth:3000/api/auth/traefik"
			"traefik.http.middlewares.tinyauth.forwardauth.trustForwardHeader":      "true"
			"traefik.http.middlewares.tinyauth.forwardauth.authResponseHeaders":     "remote-user,remote-sub,remote-name,remote-email,remote-groups"
		}

		subdomain: {key: "auth", nested: "auth", flat: "auth"}
		dashboard: {icon: "&#128274;", order: 20, section: "Platform", badge: "L1 \u00b7 ForwardAuth"}

		output: {
			url:         "https://auth.{{.domain}}"
			description: "TinyAuth authentication portal"
		}
	}
}
