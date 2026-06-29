// Package kombify_point describes the local DNS resolver integration.
//
// The rollout uses CoreDNS as the stable resolver runtime. StackKits owns the
// generated Corefile, health routing, and user guidance.
package kombify_point

import "github.com/kombifyio/stackkits/base"

Contract: base.#ModuleContract & {
	metadata: {
		name:        "kombify-point"
		displayName: "kombify Point DNS"
		version:     "1.0.0"
		layer:       "L1-foundation"
		description: "Local LAN DNS resolver for StackKit home service names"
		maturity:    "opt-in"
		testScenarios: ["SK-S1", "SK-S4"]
	}

	requires: {
		infrastructure: {
			docker:  true
			network: "host-port-53"
		}
	}

	provides: {
		capabilities: {
			"local-dns": true
			"wildcard":  true
			"forwarder": true
		}
		endpoints: {
			dns: {
				url:         "udp://{{.server_lan_ip}}:53"
				internal:    true
				description: "LAN DNS resolver endpoint"
			}
			health: {
				url:         "http://point.{{.domain}}/health"
				description: "Kombify Point health endpoint"
			}
		}
	}

	settings: {
		perma: {
			zones: [...string] | *["home"]
			targetIP: string
		}
		flexible: {
			upstreams: [...string] | *["1.1.1.1:53", "8.8.8.8:53"]
		}
	}

	contexts: {
		local: {_enabled: false}
		pi: {_enabled: false}
		cloud: {_enabled: false}
	}

	services: "kombify-point": base.#ServiceDefinition & {
		name:     "kombify-point"
		type:     "dns"
		image:    "coredns/coredns"
		tag:      "1.11.3"
		required: false
		status:   "implemented"
		needs: ["traefik"]

		placement: {
			nodeType: "all"
			strategy: "single"
		}

		network: {
			ports: [
				{container: 53, host: 53, protocol: "tcp", description: "DNS TCP"},
				{container: 53, host: 53, protocol: "udp", description: "DNS UDP"},
				{container: 8088, description: "Health API"},
			]
			traefik: {
				enabled: true
				rule:    "Host(`point.{{.domain}}`)"
				port:    8088
			}
			networks: ["frontend"]
		}

		// Matches the generated rollout: the kombify-point router gets the
		// tinyauth middleware when TinyAuth is enabled (basement-kit/templates/simple/main.tf).
		accessPolicy: {
			outerAuth: "tinyauth-pocketid"
			appAuth:   "none"
			reason:    "CoreDNS health/status endpoint with no own authentication; the gateway protects the HTTP route. DNS itself is served on host port 53, not via Traefik."
		}

		healthCheck: {
			enabled: true
			test: ["CMD", "/coredns", "-version"]
			interval: "30s"
			timeout:  "5s"
			retries:  3
		}

		subdomain: {key: "point", nested: "point", flat: "point"}
		dashboard: {icon: "&#127760;", order: 35, section: "Platform", badge: "L1 \\u00b7 DNS", enableVar: "enable_kombify_point", guideUrl: "https://docs.kombify.io/guides/stackkits/services/kombify-point"}

		output: {
			url:         "http://point.{{.domain}}/health"
			description: "kombify Point local DNS resolver"
		}
	}
}
