// Package step_ca — Smallstep Step-CA internal PKI module.
//
// Internal certificate authority for the homelab. Issues short-lived TLS
// certificates to services via ACME protocol. Traefik uses Step-CA as its
// certificate resolver instead of Let's Encrypt (for local/private domains).
//
// Integration:
//   Step-CA (ACME) → Traefik (TLS termination with auto-renewed certs)
//
// Not exposed via Traefik — internal service only. Services reach Step-CA
// directly on the shared network.
//
// Provisioners: ACME (Traefik), JWK (admin), optional OIDC (PocketID)
//
// Reference: IDENTITY-STACKKITS.md §3
package step_ca

import "github.com/kombifyio/stackkits/base"

// Contract declares what this module requires and provides.
Contract: base.#ModuleContract & {
	metadata: {
		name:        "step-ca"
		displayName: "Step-CA"
		version:     "1.0.0"
		layer:       "L1-foundation"
		description: "Internal certificate authority — ACME provider for Traefik TLS"
		// Draft: the step-ca service is status "planned".
		maturity: "draft"
	}

	requires: {
		infrastructure: {
			docker:            true
			persistentStorage: true
			network:           "shared"
		}
	}

	provides: {
		capabilities: {
			"pki":                   true
			"certificate-authority": true
			"acme":                  true
			"mtls":                  true
		}
		endpoints: {
			api: {
				url:         "https://step-ca:9000"
				internal:    true
				description: "Step-CA API and ACME directory"
			}
			acme: {
				url:         "https://step-ca:9000/acme/acme/directory"
				internal:    true
				description: "ACME directory for Traefik cert resolver"
			}
			health: {
				url:         "https://step-ca:9000/health"
				internal:    true
				description: "Health check endpoint"
			}
		}
	}

	settings: {
		perma: {
			caName:   *"Homelab CA" | string
			password: string
			dnsNames: [...string] | *["step-ca", "ca.stack.local"]
		}
		flexible: {
			acmeEnabled:     *true | bool
			defaultCertLife: *"24h" | string
			maxCertLife:     *"720h" | string
		}
	}

	contexts: {
		local: {
			_acmeEnabled: true
		}
		cloud: {
			// Cloud contexts may use Let's Encrypt instead of Step-CA
			_acmeEnabled: false
		}
		pi: {
			_acmeEnabled: true
			_resources: {
				memory:    "128m"
				memoryMax: "256m"
			}
		}
	}

	services: "step-ca": base.#ServiceDefinition & {
		name:     "step-ca"
		type:     "pki"
		image:    "smallstep/step-ca"
		tag:      "latest"
		required: false
		status:   "planned"

		placement: {
			nodeType: "all"
			strategy: "single"
		}

		network: {
			traefik: enabled: false // Internal only — not exposed via Traefik
			networks: ["frontend"]
		}

		volumes: [{
			source:      "step-ca-data"
			target:      "/home/step"
			type:        "volume"
			backup:      true
			description: "Step-CA PKI data (root CA, intermediate CA, database, config)"
		}]

		environment: {
			DOCKER_STEPCA_INIT_NAME:             "{{.step_ca_name}}"
			DOCKER_STEPCA_INIT_DNS_NAMES:        "step-ca,ca.{{.domain}}"
			DOCKER_STEPCA_INIT_PASSWORD:         "{{.step_ca_password}}"
			DOCKER_STEPCA_INIT_PROVISIONER_NAME: "Admin JWK"
			DOCKER_STEPCA_INIT_ACME:             "true"
		}

		healthCheck: {
			enabled: true
			test: ["CMD-SHELL", "curl -f -k https://localhost:9000/health || exit 1"]
			interval:    "30s"
			timeout:     "5s"
			retries:     5
			startPeriod: "30s"
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

		output: {
			description: "Step-CA internal PKI (not externally accessible)"
		}
	}
}
