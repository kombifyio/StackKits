// Package modern_homelab - Default Values
//
// Context-driven defaults for the Modern Homelab hybrid architecture.
// Secrets via SOPS + age, PaaS via domain detection.

package modern_homelab

// =============================================================================
// CLOUD NODE DEFAULTS (VPS)
// =============================================================================

#CloudNodeDefaults: {
	provider: {
		name:   "hetzner"
		region: "fsn1"
		size:   "cx22"      // 2 vCPU, 4 GB RAM
		image:  "debian-12"
	}

	docker: {
		version:  "27.0"
		dataRoot: "/var/lib/docker"
	}

	system: {
		swapEnabled: false
		swapSize:    0
	}
}

// =============================================================================
// LOCAL NODE DEFAULTS (On-Premises)
// =============================================================================

#LocalNodeDefaults: {
	docker: {
		version:  "27.0"
		dataRoot: "/var/lib/docker"
	}

	system: {
		swapEnabled: true
		swapSize:    4096
	}
}

// =============================================================================
// TRAEFIK DEFAULTS
// =============================================================================

#TraefikDefaults: {
	dashboard:    true
	acme:         true
	acmeProvider: "letsencrypt"
	logLevel:     "INFO"

	accessLog: {
		enabled:  true
		filePath: "/var/log/traefik/access.log"
	}

	metrics: {
		prometheus:  true
		entrypoint: "metrics"
	}
}

// =============================================================================
// PAAS DEFAULTS
// =============================================================================

#CoolifyDefaults: {
	autoUpdate:  false
	pushEnabled: true
	instanceSettings: {
		isRegistrationEnabled: false
		isAutoUpdateEnabled:   false
	}
	resources: {
		cpuLimit:    "2.0"
		memoryLimit: "2048m"
	}
}

#DokployDefaults: {
	traefikMe: true     // Use traefik-me for local access without domain
	magicDns:  true     // MagicDNS for service discovery
	resources: {
		cpuLimit:    "1.0"
		memoryLimit: "1024m"
	}
}

// =============================================================================
// MONITORING DEFAULTS
// =============================================================================

#MonitoringDefaults: {
	victoriametrics: {
		retention:      "30d"
		scrapeInterval: "15s"
		// Deduplication for HA setups
		dedup: enabled: false
	}

	grafana: {
		anonymousAccess: false
		plugins: [
			"grafana-piechart-panel",
			"grafana-clock-panel",
		]
		dashboards: {
			nodeExporter: true
			docker:       true
			traefik:      true
		}
	}

	loki: {
		retention:          "168h"
		ingestionRateLimit: "4MB"
		ingestionBurstSize: "6MB"
		maxQueryLookback:   "168h"
	}

	alloy: {
		// Unified telemetry agent replacing Promtail
		collectLogs:    true
		collectMetrics: true
		collectTraces:  false
	}
}

// =============================================================================
// IDENTITY DEFAULTS (from base stack)
// =============================================================================

#IdentityDefaults: {
	// Layer 1: always on
	lldap: {
		enabled: true
		baseDn:  "dc=homelab,dc=local"
	}

	stepCA: {
		enabled:     true
		provisioner: "acme"
		// Auto-renew internal certs
		autoRenew: true
	}

	// Layer 2: TinyAuth as default proxy
	tinyauth: {
		enabled: true
		// ForwardAuth for all Traefik routes
		forwardAuth: true
	}

	// Layer 2: PocketID optional OIDC
	pocketid: {
		enabled: false
	}
}

// =============================================================================
// TUNNEL DEFAULTS
// =============================================================================

#TunnelDefaults: {
	// Default: Cloudflare Tunnel (free, simple)
	provider: "cloudflare"

	cloudflare: {
		// Zero-trust access via Cloudflare
		zeroTrust: true
	}

	pangolin: {
		// Self-hosted alternative, AGPL-3
		// WireGuard-based, includes SSO + Let's Encrypt
		serverPort: 443
	}
}

// =============================================================================
// BACKUP DEFAULTS
// =============================================================================

#BackupDefaults: {
	// Restic for encrypted, deduplicated backups
	provider: "restic"

	schedule: "0 2 * * *"  // Daily at 2 AM
	retention: {
		keepDaily:   7
		keepWeekly:  4
		keepMonthly: 6
	}

	// 3-2-1 rule: local + offsite
	targets: {
		local: {
			enabled: true
			path:    "/backup/restic"
		}
		offsite: {
			enabled:  false
			provider: "b2"    // Backblaze B2 (cheapest)
		}
	}
}

// =============================================================================
// SECRETS DEFAULTS
// =============================================================================

#SecretsDefaults: {
	provider:    "sops-age"
	ageKeyFile:  "/etc/sops/age-key.txt"
	encryptedSecretsFile: "secrets.enc.yaml"
}

// =============================================================================
// NETWORK DEFAULTS
// =============================================================================

#NetworkDefaults: {
	// Docker bridge for co-located services
	serviceBridge: {
		name:    "kombistack"
		driver:  "bridge"
		subnet:  "172.20.0.0/16"
		gateway: "172.20.0.1"
	}

	// DNS configuration
	dns: {
		servers: ["1.1.1.1", "8.8.8.8"]
	}

	// Reserved ports
	ports: {
		reserved:   [22, 80, 443]
		traefik:    [80, 443, 8080]
		coolify:    [8000, 6001, 6002]
		dokploy:    [3000]
	}
}

// =============================================================================
// SECURITY DEFAULTS
// =============================================================================

#SecurityDefaults: {
	firewall: {
		inbound: [
			{port: 22, protocol: "tcp", source: "0.0.0.0/0", description: "SSH"},
			{port: 80, protocol: "tcp", source: "0.0.0.0/0", description: "HTTP"},
			{port: 443, protocol: "tcp", source: "0.0.0.0/0", description: "HTTPS"},
		]
		outbound: [
			{port: 0, protocol: "all", destination: "0.0.0.0/0", description: "Allow all outbound"},
		]
	}

	ssh: {
		permitRootLogin:        "prohibit-password"
		passwordAuthentication: false
		pubkeyAuthentication:   true
	}

	docker: {
		liverestore:   true
		userlandProxy: false
		iptables:      true
	}
}
