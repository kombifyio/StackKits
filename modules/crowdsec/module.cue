// Package crowdsec — CrowdSec IDS + WAF module.
//
// Behavioral intrusion detection (log analysis + crowd-sourced threat intel)
// and application-level WAF via the AppSec component.
//
// Integrates with Traefik via the bouncer plugin. CrowdSec analyzes Traefik
// access logs and blocks malicious IPs in real-time.
//
// Middleware chain position: crowdsec → rate-limit → security-headers → tinyauth
//
// Reference: NETWORK-SECURITY-STACKKITS_1.md §7.3
package crowdsec

import "github.com/kombifyio/stackkits/base"

// Contract declares what this module requires and provides.
Contract: base.#ModuleContract & {
	metadata: {
		name:        "crowdsec"
		displayName: "CrowdSec"
		version:     "1.0.0"
		layer:       "L2-platform-ingress"
		description: "Behavioral IDS + WAF with crowd-sourced threat intel and Traefik bouncer"
		// Draft: the crowdsec service is status "planned".
		maturity: "draft"
	}

	requires: {
		services: {
			traefik: {
				minVersion: "3.0"
				provides: ["reverse-proxy"]
			}
		}
		infrastructure: {
			docker:  true
			network: "shared"
		}
	}

	provides: {
		capabilities: {
			"ids":          true
			"waf":          true
			"ip-blocking":  true
			"threat-intel": true
		}
		middleware: {
			crowdsec: {
				type:        "plugin"
				plugin:      "crowdsec-bouncer-traefik-plugin"
				description: "IP reputation check + AppSec WAF"
			}
		}
		endpoints: {
			api: {
				url:         "http://crowdsec:8080"
				internal:    true
				description: "CrowdSec LAPI (local API)"
			}
			appsec: {
				url:         "http://crowdsec:7422"
				internal:    true
				description: "CrowdSec AppSec WAF endpoint"
			}
		}
	}

	settings: {
		perma: {
			bouncerKey: string
		}
		flexible: {
			mode: *"stream" | "live"
			collections: [...string] | *[
				"crowdsecurity/traefik",
				"crowdsecurity/linux",
				"crowdsecurity/sshd",
				"crowdsecurity/http-cve",
				"crowdsecurity/appsec-virtual-patching",
				"crowdsecurity/appsec-generic-rules",
			]
			appsecEnabled: *true | bool
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

	services: crowdsec: base.#ServiceDefinition & {
		name:     "crowdsec"
		type:     "security"
		image:    "crowdsecurity/crowdsec"
		tag:      "latest"
		required: false
		status:   "planned"
		needs: ["traefik"]

		placement: {
			nodeType: "all"
			strategy: "single"
		}

		network: {
			traefik: enabled: false
			networks: ["frontend"]
		}

		volumes: [
			{
				source:      "crowdsec-config"
				target:      "/etc/crowdsec"
				type:        "volume"
				backup:      true
				description: "CrowdSec configuration and scenarios"
			},
			{
				source:      "crowdsec-data"
				target:      "/var/lib/crowdsec/data"
				type:        "volume"
				backup:      false
				description: "CrowdSec decisions database"
			},
		]

		environment: {
			COLLECTIONS:         "crowdsecurity/traefik crowdsecurity/linux crowdsecurity/sshd crowdsecurity/http-cve crowdsecurity/appsec-virtual-patching crowdsecurity/appsec-generic-rules"
			BOUNCER_KEY_traefik: "{{.crowdsec_bouncer_key}}"
		}

		healthCheck: {
			enabled:  true
			command:  "cscli version"
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
			capAdd: ["NET_BIND_SERVICE"]
		}

		output: {
			description: "CrowdSec IDS + WAF (internal, no external access)"
		}
	}
}
