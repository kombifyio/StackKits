// Package pocketid — PocketID OIDC identity provider module.
//
// Self-hosted OpenID Connect provider for SSO.
// Requires Traefik for ingress routing.
package pocketid

import "github.com/kombifyio/stackkits/base"

Contract: base.#ModuleContract & {
	metadata: {
		name:        "pocketid"
		displayName: "PocketID"
		version:     "1.0.0"
		layer:       "L2-platform-identity"
		description: "Self-hosted OIDC provider for single sign-on with passkeys"
		maturity:    "default"
		testScenarios: ["SK-S1", "SK-S2", "SK-S3", "SK-S5"]
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
			"oidc":              true
			"oidc-provider":     true
			"identity-provider": true
			"sso":               true
			"passkeys":          true
		}
		endpoints: {
			ui: {
				url:         "https://id.{{.domain}}"
				description: "PocketID admin and login UI"
			}
			wellknown: {
				url:         "http://pocketid:1411/.well-known/openid-configuration"
				internal:    true
				description: "OIDC discovery endpoint"
			}
		}
	}

	settings: {
		perma: {
			encryptionKey: string
		}
		flexible: {
			trustProxy: *true | bool
			logLevel:   *"info" | "debug" | "warn" | "error"
		}
	}

	contexts: {
		local: {
			_trustProxy: true
		}
		cloud: {
			_trustProxy: true
		}
		pi: {
			_trustProxy: true
		}
	}

	services: pocketid: base.#ServiceDefinition & {
		name:  "pocketid"
		type:  "auth"
		image: "ghcr.io/pocket-id/pocket-id"
		// PocketID v2 (PR #1229, v2.0.0+) is required for Phase 1's apply-time
		// owner-bootstrap. v1 has no STATIC_API_KEY support and cannot be
		// API-bootstrapped, so the fragment-rendered deploy/pocketid.tf would
		// be unbootable against a real install. v1 is end-of-life; the
		// integration test (tests/integration/pocketid_e2e_test.go) docker-runs
		// v2 directly, so this pin keeps the rendered TF and the integration
		// harness on the same image.
		tag:      "v2.7.0"
		upstream: {
			github: {repo: "pocket-id/pocket-id"}
		}
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
				rule:    "Host(`id.{{.domain}}`)"
				port:    1411
			}
			networks: ["base_net"]
		}

		accessPolicy: {
			outerAuth:      "self"
			appAuth:        "self-auth"
			ownerBootstrap: "StackKits creates the Owner user record plus a one-time passkey setup URL; PocketID is passkey-only."
		}

		volumes: [
			{
				source:      "pocketid-data"
				target:      "/app/backend/data"
				type:        "volume"
				backup:      true
				description: "PocketID database and config"
			},
		]

		environment: {
			TZ: "{{.timezone}}"
			// PocketID v2 reads APP_URL (the legacy v1 PUBLIC_APP_URL was
			// removed); see base/identity/_pocketid.tf.tmpl for the matching
			// shape used by the legacy fallback path.
			APP_URL:     "https://id.{{.domain}}"
			TRUST_PROXY: "true"
			// PocketID v2 (PR #1229, v2.0.0+) accepts a fixed admin token at
			// boot via STATIC_API_KEY, bypassing the in-browser setup wizard.
			// The CLI persists this value in <homelab>/.stackkit/pocketid-static-api-key
			// (see internal/identity/static_api_key.go) and renders the same
			// value into the apply-time owner-bootstrap call so the
			// container env and the orchestrator agree on the credential.
			STATIC_API_KEY: "{{.pocketid_static_api_key}}"
			// PocketID v2 refuses to start without ENCRYPTION_KEY (>=16 raw
			// bytes) — without it the container exits immediately with
			// "config error: ENCRYPTION_KEY must be at least 16 bytes long".
			// Persisted at <homelab>/.stackkit/pocketid-encryption-key
			// (24 raw bytes -> ~32 chars base64). Rotating this key makes
			// existing data on the volume undecryptable, so we re-use the
			// same value across destroy → re-apply round-trips.
			ENCRYPTION_KEY: "{{.pocketid_encryption_key}}"
		}

		healthCheck: {
			enabled: true
			http: {
				path:   "/healthz"
				port:   1411
				scheme: "http"
			}
			interval: "30s"
			timeout:  "5s"
			retries:  3
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
			"traefik.enable":                                          "true"
			"traefik.http.routers.pocketid.rule":                      "Host(`id.{{.domain}}`)"
			"traefik.http.routers.pocketid.entrypoints":               "web"
			"traefik.http.services.pocketid.loadbalancer.server.port": "1411"
		}

		subdomain: {key: "id", nested: "id", flat: "id"}
		dashboard: {icon: "&#128100;", order: 10, section: "Platform", badge: "L1 \u00b7 IdP", guideUrl: "https://docs.kombify.io/guides/stackkits/services/pocketid"}

		output: {
			url:         "https://id.{{.domain}}"
			description: "PocketID OIDC provider"
		}
	}

	// The stable Base Kit template registers TinyAuth as a public PKCE OIDC
	// client during apply using PocketID's STATIC_API_KEY bootstrap path.
}
