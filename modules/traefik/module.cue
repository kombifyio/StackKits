// Package traefik — Traefik configuration module.
//
// In production, Traefik is shipped by the selected router: Coolify's
// integrated Traefik by default, or StackKit-owned Traefik for Komodo.
// This module defines the middleware configuration, security headers,
// and ForwardAuth setup that gets applied to the PaaS-shipped Traefik
// via container labels and dynamic config.
//
// Traefik uses the socket-proxy for Docker service discovery (read-only).
// It never mounts docker.sock directly. The PaaS manages its own Traefik
// instance but we recommend routing it through socket-proxy as well.
//
// In module tests (reference-compose), a standalone Traefik is used as
// a stand-in for the PaaS Traefik, connected via socket-proxy.
package traefik

import "github.com/kombifyio/stackkits/base"

// Contract declares what this module requires and provides.
Contract: base.#ModuleContract & {
	metadata: {
		name:        "traefik"
		displayName: "Traefik"
		version:     "2.1.0"
		layer:       "L2-platform-ingress"
		description: "Reverse proxy configuration — middlewares, security headers, ForwardAuth (shipped by PaaS)"
		testScenarios: ["SK-S1", "SK-S2", "SK-S3"]
	}

	// Traefik uses socket-proxy for Docker service discovery (read-only).
	requires: {
		services: {
			"socket-proxy": {
				provides: ["docker-api-proxy"]
			}
		}
		infrastructure: {
			docker:  true
			network: "shared"
		}
	}

	provides: {
		capabilities: {
			"reverse-proxy":    true
			"entrypoints":      true
			"forwardauth-host": true
			"tls-termination":  true
			"dashboard":        true
			"security-headers": true
			"rate-limiting":    true
		}
		middleware: {
			"security-headers": {
				type:        "headers"
				description: "HSTS, Content-Type-Nosniff, Frame-Deny, COOP, CORP, Permissions-Policy"
			}
			"rate-limit": {
				type:        "rateLimit"
				description: "100 req/s average, 200 burst"
			}
		}
		endpoints: {
			dashboard: {
				url:         "https://traefik.{{.domain}}"
				description: "Traefik dashboard"
			}
			api: {
				url:         "http://traefik:8080"
				internal:    true
				description: "Traefik API (internal)"
			}
		}
	}

	settings: {
		perma: {
			httpPort:  *80 | int
			httpsPort: *443 | int
		}
		flexible: {
			dashboardEnabled: *true | bool
			logLevel:         *"ERROR" | "DEBUG" | "INFO" | "WARN"
			accessLog:        *true | bool
		}
	}

	contexts: {
		local: {
			_tlsMode: "self-signed"
		}
		cloud: {
			_tlsMode: "letsencrypt"
		}
		pi: {
			_tlsMode:  "none"
			_logLevel: "WARN"
		}
	}

	// Service definition describes the PaaS-shipped Traefik's expected config.
	// In production this container is managed by the selected router, not by StackKit.
	// The service definition is used for: label generation, middleware config, tests.
	services: traefik: base.#ServiceDefinition & {
		name:     "traefik"
		type:     "reverse-proxy"
		image:    "traefik"
		tag:      "v3.6.13"
		required: true
		status:   "implemented"
		needs: ["socket-proxy"]
		command: [
			"--providers.docker=true",
			"--providers.docker.endpoint=tcp://socket-proxy:2375",
			"--providers.docker.exposedbydefault=false",
			"--providers.docker.network=frontend",
			"--entrypoints.web.address=:80",
			"--entrypoints.websecure.address=:443",
			"--api.dashboard=true",
			"--ping=true",
		]

		placement: {
			nodeType: "all"
			strategy: "entry-point"
		}

		network: {
			ports: [
				{container: 80, description: "HTTP entrypoint"},
				{container: 443, description: "HTTPS entrypoint"},
				{container: 8080, description: "Dashboard/API"},
			]
			traefik: enabled: false
			networks: ["frontend", "socket-proxy-net"]
		}

		// No docker.sock — uses socket-proxy for service discovery
		volumes: [{
			source:      "traefik-certs"
			target:      "/certs"
			type:        "volume"
			backup:      true
			description: "TLS certificate storage"
		}]

		resources: {
			memory:    "128m"
			memoryMax: "256m"
			cpus:      0.5
		}

		healthCheck: {
			enabled: true
			test: ["CMD", "traefik", "healthcheck", "--ping"]
			interval: "30s"
			timeout:  "5s"
			retries:  3
		}

		security: {
			noNewPrivileges: true
			capDrop: ["ALL"]
			capAdd: ["NET_BIND_SERVICE"]
			readOnly: true
			tmpfs: ["/tmp"]
		}

		subdomain: {key: "traefik", nested: "traefik", flat: "traefik"}
		dashboard: {icon: "&#9889;", order: 30, section: "Platform", badge: "L2 \u00b7 Reverse Proxy", guideUrl: "https://docs.kombify.io/guides/stackkits/services/traefik"}

		output: {
			url:         "https://traefik.{{.domain}}"
			description: "Traefik dashboard (protected by TinyAuth)"
		}
	}
}
