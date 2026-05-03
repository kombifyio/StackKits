// Package base_kit - Smart defaults and compute tier logic
package base_kit

// =============================================================================
// SERVICE VARIANT BASE SCHEMA
// =============================================================================

// #ServiceConfig defines per-service configuration within a variant
#ServiceConfig: {
	enabled:     bool
	description: string
	port?:       int
	ports?: [...int]
	config?: {...}
}

// #ServiceVariant is the base schema for all service variants
// All variant definitions (default, coolify, beszel, minimal) extend this
#ServiceVariant: {
	name:           string
	description:    string
	requiresDomain: bool | *false

	// Resource requirements (optional)
	requirements?: {
		minCpu:    int
		minMemory: int
		minDisk:   int
	}

	// Service configurations (keyed by service name)
	services: {
		[serviceName=string]: #ServiceConfig
	}

	// Features enabled by this variant
	features?: {
		[featureName=string]: bool
	}
}

// =============================================================================
// SMART DEFAULTS
// =============================================================================

// #SmartDefaults provides intelligent defaults based on compute tier
#SmartDefaults: {
	// Input: detected compute tier
	computeTier: "high" | "standard" | "low"

	// Output: service configuration based on tier
	services: {
		if computeTier == "high" {
			monitoring: "full"
			management: "advanced"
			logging:    "full"
			_enabledServices: ["traefik", "dockge", "dozzle", "netdata", "portainer", "prometheus", "grafana"]
		}
		if computeTier == "standard" {
			monitoring: "standard"
			management: "basic"
			logging:    "basic"
			_enabledServices: ["traefik", "dockge", "dozzle", "netdata"]
		}
		if computeTier == "low" {
			monitoring: "minimal"
			management: "minimal"
			logging:    "basic"
			_enabledServices: ["traefik", "dockge", "dozzle", "glances"]
		}
	}

	// Docker resource limits per tier
	docker: {
		if computeTier == "high" {
			defaultMemoryLimit:     "4g"
			defaultMemoryReservation: "1g"
			defaultCpuLimit:        4.0
			maxContainers:          50
			logMaxSize:             "100m"
			logMaxFile:             10
		}
		if computeTier == "standard" {
			defaultMemoryLimit:     "1g"
			defaultMemoryReservation: "256m"
			defaultCpuLimit:        1.0
			maxContainers:          20
			logMaxSize:             "50m"
			logMaxFile:             5
		}
		if computeTier == "low" {
			defaultMemoryLimit:     "512m"
			defaultMemoryReservation: "128m"
			defaultCpuLimit:        0.5
			maxContainers:          10
			logMaxSize:             "20m"
			logMaxFile:             3
		}
	}

	// Traefik configuration per tier
	traefik: {
		if computeTier == "high" {
			accessLog:    true
			metrics:      true
			tracing:      true
			maxIdleConns: 200
		}
		if computeTier == "standard" {
			accessLog:    false
			metrics:      true
			tracing:      false
			maxIdleConns: 100
		}
		if computeTier == "low" {
			accessLog:    false
			metrics:      false
			tracing:      false
			maxIdleConns: 50
		}
	}

	// Backup configuration per tier
	backup: {
		if computeTier == "high" {
			enabled:   true
			frequency: "0 */6 * * *" // Every 6 hours
			retention: {
				daily:   14
				weekly:  8
				monthly: 12
			}
		}
		if computeTier == "standard" {
			enabled:   true
			frequency: "0 3 * * *" // Daily at 3am
			retention: {
				daily:   7
				weekly:  4
				monthly: 6
			}
		}
		if computeTier == "low" {
			enabled:   true
			frequency: "0 4 * * 0" // Weekly on Sunday
			retention: {
				daily:   3
				weekly:  2
				monthly: 1
			}
		}
	}
}

// #ComputeTierDetector determines the compute tier from resources
#ComputeTierDetector: {
	// Input: node resources
	cpu:    int
	memory: int

	// Output: computed tier
	tier: *"standard" | "high" | "low"

	// High tier: 8+ CPU AND 16+ GB RAM
	if cpu >= 8 && memory >= 16 {
		tier: "high"
	}

	// Low tier: <4 CPU OR <8 GB RAM
	if cpu < 4 || memory < 8 {
		tier: "low"
	}

	// Standard is the default (4-7 CPU, 8-15 GB)
}

// #DomainConfig provides domain configuration defaults.
// This is the canonical list of all service subdomains.
// When subdomainPrefix is set (kombify.me), domains use flat naming:
//   {prefix}-{flat}.{domain}  e.g. sh-mylab-abc-dash.kombify.me
// When empty (own domain or LAN), domains use nested naming:
//   {nested}.{domain}         e.g. dash.kmbchr.de
#DomainConfig: {
	// Primary domain (required)
	domain: string

	// Optional subdomain prefix (kombify.me tunnel mode)
	subdomainPrefix: string | *""

	// Subdomain definitions: key → {nested, flat}
	subdomains: {
		dashboard:  { nested: "base", flat: "dash" }
		traefik:    { nested: "traefik", flat: "traefik" }
		auth:       { nested: "auth", flat: "tinyauth" }
		pocketid:   { nested: "id", flat: "id" }
		dokploy:    { nested: "dokploy", flat: "dokploy" }
		coolify:    { nested: "coolify", flat: "coolify" }
		dockge:     { nested: "dockge", flat: "dockge" }
		kuma:       { nested: "kuma", flat: "kuma" }
		whoami:     { nested: "whoami", flat: "whoami" }
		vault:      { nested: "vault", flat: "vault" }
		media:      { nested: "media", flat: "media" }
		photos:     { nested: "photos", flat: "photos" }
		logs:       { nested: "logs", flat: "logs" }
		monitor:    { nested: "monitor", flat: "monitor" }
		portainer:  { nested: "portainer", flat: "portainer" }
		netdata:    { nested: "netdata", flat: "netdata" }
	}

	// Full URLs (computed from prefix + subdomain + domain)
	urls: {
		for k, v in subdomains {
			if subdomainPrefix != "" {
				"\(k)": "\(subdomainPrefix)-\(v.flat).\(domain)"
			}
			if subdomainPrefix == "" {
				"\(k)": "\(v.nested).\(domain)"
			}
		}
	}
}

// #DefaultPorts defines default port mappings
#DefaultPorts: {
	ssh:       22
	http:      80
	https:     443
	traefik:   8080
	dockge:    5001
	dozzle:    8080
	netdata:   19999
	glances:   61208
	portainer: 9000
	prometheus: 9090
	grafana:   3000
}

// #DefaultVolumes defines default volume paths
#DefaultVolumes: {
	stacksDir:     "/opt/stacks"
	dataDir:       "/opt/data"
	backupDir:     "/opt/backups"
	certsDir:      "/opt/certs"
	configDir:     "/opt/config"
	dockerSocket:  "/var/run/docker.sock"
}

// #HostsEntry for /etc/hosts configuration
#HostsEntry: {
	ip:      string
	domains: [...string]
}

// #LocalHostsConfig generates /etc/hosts entries for local access
#LocalHostsConfig: {
	serverIP: string
	domain:   string

	entries: [...#HostsEntry] & [
		{
			ip: serverIP
			domains: [
				"base.\(domain)",
				"traefik.\(domain)",
				"auth.\(domain)",
				"id.\(domain)",
				"dokploy.\(domain)",
				"kuma.\(domain)",
				"whoami.\(domain)",
				"vault.\(domain)",
				"media.\(domain)",
				"photos.\(domain)",
				"logs.\(domain)",
			]
		},
	]
}
