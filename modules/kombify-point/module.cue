// Package kombify_point describes the local DNS resolver integration.
//
// The service implementation lives in kombify-Techstack. StackKits owns the
// rollout adapter: image, environment, health routing, and user guidance.
package kombify_point

import "github.com/kombifyio/stackkits/base"

Contract: base.#ModuleContract & {
	metadata: {
		name:        "kombify-point"
		displayName: "Kombify Point DNS"
		version:     "1.0.0"
		layer:       "L1-foundation"
		description: "Local LAN DNS resolver for *.home and *.<name>.home service names"
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
				url:         "https://point.{{.domain}}/healthz"
				description: "Kombify Point health endpoint"
			}
		}
	}

	settings: {
		perma: {
			zones:    [...string] | *["home"]
			targetIP: string
		}
		flexible: {
			upstreams: [...string] | *["1.1.1.1:53", "8.8.8.8:53"]
		}
	}

	contexts: {
		local: {_enabled: false}
		pi:    {_enabled: false}
		cloud: {_enabled: false}
	}

	services: "kombify-point": base.#ServiceDefinition & {
		name:     "kombify-point"
		type:     "dns"
		image:    "ghcr.io/kombifyio/kombify-point"
		tag:      "latest"
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

		environment: {
			KOMBIFY_POINT_ZONES:      "{{.domain}}"
			KOMBIFY_POINT_TARGET_IP:  "{{.server_lan_ip}}"
			KOMBIFY_POINT_UPSTREAMS:  "1.1.1.1:53,8.8.8.8:53"
			KOMBIFY_POINT_HTTP_ADDR:  ":8088"
			KOMBIFY_POINT_DNS_ADDR:   ":53"
		}

		healthCheck: {
			enabled: true
			test: ["CMD-SHELL", "wget -q -O- http://127.0.0.1:8088/healthz >/dev/null || exit 1"]
			interval: "30s"
			timeout:  "5s"
			retries:  3
		}

		subdomain: {key: "point", nested: "point", flat: "point"}
		dashboard: {icon: "&#127760;", order: 35, section: "Platform", badge: "L1 \\u00b7 DNS", enableVar: "enable_kombify_point"}

		output: {
			url:         "https://point.{{.domain}}/healthz"
			description: "Kombify Point local DNS resolver"
		}
	}
}
