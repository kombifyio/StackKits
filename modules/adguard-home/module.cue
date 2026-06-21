// Package adguard-home -- AdGuard Home DNS filter and ad-blocker module.
//
// Provides DNS filtering, ad/malware blocking, local DNS rewrites,
// and DNS rebinding protection. Acts as the upstream DNS for the network.
// Upstream: Unbound (recursive resolver) for local/cloud contexts,
// Cloudflare DoH (1.1.1.1) as fallback.
//
// PROVEN CONFIG: Validated via reference-compose.yml.
package adguard_home

import "github.com/kombifyio/stackkits/base"

// Contract declares what this module requires and provides.
Contract: base.#ModuleContract & {
	metadata: {
		name:        "adguard-home"
		displayName: "AdGuard Home"
		version:     "1.0.1"
		layer:       "L2-platform-dns"
		description: "DNS filter, ad/malware blocking, and local DNS rewrites"
		// Draft: the adguard-provisioner command is a prose stub, not an
		// executable script — no automated first-run path yet.
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
			"dns-filtering":      true
			"ad-blocking":        true
			"dns-rebind-protect": true
			"local-dns-rewrites": true
			"dns-server":         true
		}
		endpoints: {
			ui: {
				url:         "https://adguard.{{.domain}}"
				description: "AdGuard Home web UI"
			}
			dns: {
				url:         "dns://{{.nodeIP}}:53"
				internal:    true
				description: "DNS server endpoint"
			}
		}
	}

	settings: {
		perma: {
			// Admin credentials (set at deploy time). The password MUST be a
			// secret reference, never a literal value (Golden Rules §4.4/§4.5).
			adminUser:     string | *"admin"
			adminPassword: =~"^secret://"
		}
		flexible: {
			// Upstream DNS: unbound (local/cloud) or cloudflare-doh (pi/simple)
			upstreamDNS: *"unbound" | "cloudflare-doh" | "quad9" | "custom"
			// Enable safe browsing (malware + adult content)
			safeBrowsing: bool | *false
			// DNS rebinding protection (critical security feature)
			dnRebindProtect: bool | *true
			// Query log retention (days)
			queryLogRetentionDays: int | *7
		}
	}

	contexts: {
		local: {
			// Use Unbound as recursive resolver (self-hosted)
			settings: flexible: upstreamDNS: "unbound"
		}
		cloud: {
			// Use Unbound as recursive resolver (self-hosted)
			settings: flexible: upstreamDNS: "unbound"
		}
		pi: {
			// Pi has limited resources — use DoH for simplicity
			settings: flexible: upstreamDNS: "cloudflare-doh"
		}
	}

	services: "adguard-home": base.#ServiceDefinition & {
		name:     "adguard-home"
		type:     "dns"
		image:    "adguard/adguardhome"
		tag:      "latest"
		required: false
		status:   "implemented"
		needs: ["traefik"]

		placement: {
			nodeType: "local"
			strategy: "single"
		}

		network: {
			traefik: {
				enabled: true
				rule:    "Host(`adguard.{{.domain}}`)"
				port:    3000
			}
			networks: ["frontend"]
		}

		accessPolicy: {
			outerAuth: "tinyauth-pocketid"
			appAuth:   "self-auth"
		}

		healthCheck: {
			enabled:  true
			interval: "30s"
			timeout:  "5s"
			retries:  5
		}

		resources: {
			memory:    "128m"
			memoryMax: "256m"
			cpus:      0.25
		}

		security: {
			noNewPrivileges: true
			capDrop: ["ALL"]
			// DAC_OVERRIDE needed: image dirs owned by nobody:nobody (uid 65534),
			// container runs as root — without DAC_OVERRIDE, root cannot write to them
			capAdd: ["NET_BIND_SERVICE", "CHOWN", "SETUID", "SETGID", "DAC_OVERRIDE"]
		}

		labels: {
			"traefik.enable":                                              "true"
			"traefik.http.routers.adguard-home.rule":                      "Host(`adguard.{{.domain}}`)"
			"traefik.http.routers.adguard-home.entrypoints":               "web"
			"traefik.http.services.adguard-home.loadbalancer.server.port": "3000"
		}

		output: {
			url:         "https://adguard.{{.domain}}"
			description: "AdGuard Home DNS filter and admin UI"
		}
	}

	provisioners: "adguard-provisioner": base.#ProvisionerService & {
		image:     "alpine/curl:latest"
		dependsOn: "adguard-home"
		networks: ["frontend"]
		// See tests/reference-compose.yml for the full provisioner script.
		// Uses POST /control/install/configure to configure AdGuard Home headlessly.
		// Credentials: admin / Admin1234!
		command: "see reference-compose.yml adguard-provisioner service"
	}
}
