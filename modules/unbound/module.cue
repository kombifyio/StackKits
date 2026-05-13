// Package unbound -- Unbound recursive DNS resolver module.
//
// Provides recursive DNS resolution with DNSSEC validation.
// No third-party DNS dependency — resolves directly from root servers.
// Intended to be used as upstream for AdGuard Home.
//
// Chain: AdGuard Home → Unbound → Root servers
//
// PROVEN CONFIG: Validated via reference-compose.yml.
package unbound

import "github.com/kombifyio/stackkits/base"

// Contract declares what this module requires and provides.
Contract: base.#ModuleContract & {
	metadata: {
		name:        "unbound"
		displayName: "Unbound"
		version:     "1.0.0"
		layer:       "L1-foundation"
		description: "Recursive DNS resolver with DNSSEC validation — no third-party dependency"
		testScenarios: ["SK-S1", "SK-S4"]
	}

	requires: {
		infrastructure: {
			docker:  true
			network: "shared"
		}
	}

	provides: {
		capabilities: {
			"dns-recursive":       true
			"dnssec-validation":   true
			"dns-server":          true
			"no-external-dns-dep": true
		}
		endpoints: {
			dns: {
				url:         "dns://unbound:53"
				internal:    true
				description: "Unbound DNS resolver (internal only, port 53)"
			}
		}
	}

	settings: {
		perma: {
			dnsPort: int | *53
		}
		flexible: {
			// DNSSEC validation (recommended)
			dnssec: bool | *true
			// Prefetch frequently-queried records
			prefetch: bool | *true
			// Cache size (number of messages)
			msgCacheSize: int | *4194304
			// Number of threads (1 is fine for homelab)
			numThreads: int | *1
		}
	}

	contexts: {
		local: {}
		cloud: {}
		pi: {
			// Reduced cache for Pi
			settings: flexible: msgCacheSize: 2097152
		}
	}

	services: unbound: base.#ServiceDefinition & {
		name:     "unbound"
		type:     "dns"
		image:    "klutchell/unbound"
		tag:      "latest"
		required: false
		status:   "implemented"
		needs: []

		placement: {
			nodeType: "local"
			strategy: "single"
		}

		network: {
			traefik: {
				enabled: false
			}
			networks: ["backend"]
		}

		healthCheck: {
			enabled:  true
			interval: "30s"
			timeout:  "5s"
			retries:  5
		}

		resources: {
			memory:    "64m"
			memoryMax: "128m"
			cpus:      0.25
		}

		security: {
			noNewPrivileges: true
			capDrop: ["ALL"]
			capAdd: ["NET_BIND_SERVICE", "SETUID", "SETGID"]
		}

		output: {
			url:         "dns://unbound:53"
			description: "Unbound recursive DNS resolver — used as upstream for AdGuard Home"
		}
	}
}
