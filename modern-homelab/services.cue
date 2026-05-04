// Package modern_homelab - Service Definitions
//
// Modern Homelab = Multi-node Docker Compose hybrid with:
// - Identity-aware proxy stack (LLDAP + Step-CA + TinyAuth/PocketID)
// - Coolify OR Dokploy as PaaS (context-driven)
// - Traefik on cloud entry node
// - Tunnel (Cloudflare/Pangolin) for CGNAT bypass
// - VPN is optional (not required, identity stack provides zero-trust)
//
// KEY DIFFERENCES FROM base-kit:
// - Multi-environment Docker Compose (bridges home + cloud trust domains, no Swarm)
// - PaaS context-driven: domain+wildcard → Coolify, else → Dokploy
// - Identity-aware proxies make VPN optional
// - Public access via cloud node is default
// - Monitoring, backup, tunnels are composable add-ons
//
// PLACEMENT:
// - Cloud node: Traefik, PaaS, TinyAuth, PocketID, Uptime Kuma, OTel gateway, Grafana
// - Local node: Workloads deployed via PaaS (Immich, Jellyfin, etc.)
// - Daemonset: Grafana Alloy OTLP agents, cAdvisor, node-exporter (all nodes)

package modern_homelab

import "github.com/kombifyio/stackkits/base"

// =============================================================================
// CORE: TRAEFIK (Cloud Entry Point)
// =============================================================================

#TraefikService: base.#ServiceDefinition & {
	name:        "traefik"
	displayName: "Traefik"
	category:    "core"
	type:        "reverse-proxy"
	required:    true
	image:       "traefik"
	tag:         "v3.3"
	status:      "planned"
	description: "Cloud entry-point reverse proxy with automatic HTTPS"

	placement: {
		nodeType: "cloud"
		strategy: "entry-point"
	}

	network: {
		ports: [
			{host: 80, container: 80, protocol: "tcp", description: "HTTP"},
			{host: 443, container: 443, protocol: "tcp", description: "HTTPS"},
			{host: 8080, container: 8080, protocol: "tcp", description: "Dashboard"},
		]
		mode: "bridge"
	}

	volumes: [
		{
			source:      "/var/run/docker.sock"
			target:      "/var/run/docker.sock"
			type:        "bind"
			readOnly:    true
			backup:      false
			description: "Docker socket for container discovery"
		},
		{
			source:      "traefik-certs"
			target:      "/certs"
			type:        "volume"
			backup:      true
			description: "SSL certificates storage"
		},
		{
			source:      "traefik-config"
			target:      "/etc/traefik"
			type:        "volume"
			backup:      true
			description: "Traefik configuration"
		},
	]

	healthCheck: {
		enabled: true
		http: {
			path:   "/ping"
			port:   8080
			scheme: "http"
		}
		interval:    "10s"
		timeout:     "5s"
		retries:     3
		startPeriod: "10s"
	}

	config: {
		dashboard:    bool | *true
		acme:         bool | *true
		acmeEmail:    string
		acmeProvider: "letsencrypt" | "letsencrypt-staging" | *"letsencrypt"
		logLevel:     "DEBUG" | "INFO" | "WARN" | "ERROR" | *"INFO"
	}

	labels: {
		"traefik.enable":                            "true"
		"traefik.http.routers.api.entrypoints":      "websecure"
		"traefik.http.routers.api.rule":             "Host(`traefik.{{.domain}}`)"
		"traefik.http.routers.api.service":          "api@internal"
		"traefik.http.routers.api.tls.certresolver": "letsencrypt"
		"stackkit.layer":                            "2-platform"
		"stackkit.managed-by":                       "terraform"
	}

	output: {
		url:         "https://traefik.{{.domain}}"
		description: "Traefik Dashboard"
		credentials: note: "Protected by TinyAuth ForwardAuth"
	}

	restartPolicy: "unless-stopped"
}

// =============================================================================
// LAYER 2: PLATFORM IDENTITY
// =============================================================================

// TinyAuth - Lightweight ForwardAuth proxy (default identity layer)
#TinyAuthService: base.#ServiceDefinition & {
	name:        "tinyauth"
	displayName: "TinyAuth"
	category:    "platform-identity"
	type:        "auth"
	required:    true
	image:       "ghcr.io/steveiliop56/tinyauth"
	tag:         "v3"
	status:      "planned"
	description: "Identity-aware ForwardAuth proxy for all Traefik routes"
	needs:       ["traefik"]

	placement: {
		nodeType: "cloud"
		strategy: "single"
	}

	network: {
		ports: [
			{host: 3002, container: 3000, protocol: "tcp", description: "Web UI"},
		]
		traefik: {
			enabled: true
			rule:    "Host(`auth.{{.domain}}`)"
			tls:     true
			port:    3000
		}
	}

	volumes: [
		{
			source:      "tinyauth-data"
			target:      "/data"
			type:        "volume"
			backup:      true
			description: "TinyAuth data and user database"
		},
	]

	healthCheck: {
		enabled: true
		http: {
			path:   "/api/health"
			port:   3000
			scheme: "http"
		}
		interval:    "30s"
		timeout:     "5s"
		retries:     3
		startPeriod: "10s"
	}

	resources: {
		memory:    "128m"
		memoryMax: "256m"
		cpus:      0.25
	}

	labels: {
		"traefik.enable":                                                        "true"
		"traefik.http.routers.tinyauth.entrypoints":                             "websecure"
		"traefik.http.routers.tinyauth.rule":                                    "Host(`auth.{{.domain}}`)"
		"traefik.http.routers.tinyauth.tls.certresolver":                        "letsencrypt"
		"traefik.http.services.tinyauth.loadbalancer.server.port":               "3000"
		"traefik.http.middlewares.tinyauth.forwardauth.address":                 "http://tinyauth:3000/api/auth/verify"
		"traefik.http.middlewares.tinyauth.forwardauth.authResponseHeaders":     "X-User,X-Email"
		"stackkit.layer":      "2-platform"
		"stackkit.managed-by": "terraform"
	}

	output: {
		url:         "https://auth.{{.domain}}"
		description: "TinyAuth Identity Proxy"
		credentials: note: "Configured via LLDAP user directory"
	}

	restartPolicy: "unless-stopped"
}

// PocketID - OIDC provider with passkeys (optional upgrade from TinyAuth)
#PocketIDService: base.#ServiceDefinition & {
	name:        "pocketid"
	displayName: "PocketID"
	category:    "platform-identity"
	type:        "auth"
	required:    false
	enabled:     false
	image:       "ghcr.io/pocket-id/pocket-id"
	tag:         "latest"
	status:      "planned"
	description: "OIDC provider with passkeys for SSO across all services"
	needs:       ["traefik"]

	placement: {
		nodeType: "cloud"
		strategy: "single"
	}

	network: {
		ports: [
			{host: 3003, container: 80, protocol: "tcp", description: "Web UI"},
		]
		traefik: {
			enabled: true
			rule:    "Host(`id.{{.domain}}`)"
			tls:     true
			port:    80
		}
	}

	volumes: [
		{
			source:      "pocketid-data"
			target:      "/app/backend/data"
			type:        "volume"
			backup:      true
			description: "PocketID database and keys"
		},
	]

	resources: {
		memory:    "256m"
		memoryMax: "512m"
		cpus:      0.5
	}

	labels: {
		"stackkit.layer":      "2-platform"
		"stackkit.managed-by": "terraform"
	}

	output: {
		url:         "https://id.{{.domain}}"
		description: "PocketID OIDC Provider"
		credentials: note: "Admin credentials generated on first setup"
	}

	restartPolicy: "unless-stopped"
}

// =============================================================================
// LAYER 2: PAAS (Context-Driven Selection)
// =============================================================================

// Coolify - Full PaaS for users WITH domain + wildcard
#CoolifyService: base.#ServiceDefinition & {
	name:        "coolify"
	displayName: "Coolify"
	category:    "platform"
	type:        "paas"
	required:    false
	image:       "ghcr.io/coollabsio/coolify"
	tag:         "latest"
	status:      "planned"
	description: "Self-hosted PaaS with multi-server support and git deployments"
	needs:       ["traefik"]

	placement: {
		nodeType: "cloud"
		strategy: "single"
	}

	network: {
		ports: [
			{host: 8000, container: 8000, protocol: "tcp", description: "Web UI"},
			{host: 6001, container: 6001, protocol: "tcp", description: "Websockets"},
			{host: 6002, container: 6002, protocol: "tcp", description: "Terminal"},
		]
		traefik: {
			enabled: true
			rule:    "Host(`coolify.{{.domain}}`)"
			tls:     true
			port:    8000
		}
	}

	volumes: [
		{
			source:      "/var/run/docker.sock"
			target:      "/var/run/docker.sock"
			type:        "bind"
			readOnly:    false
			backup:      false
			description: "Docker socket for container management"
		},
		{
			source:      "coolify-data"
			target:      "/data/coolify"
			type:        "volume"
			backup:      true
			description: "Coolify application data"
		},
		{
			source:      "/data/coolify/ssh"
			target:      "/data/coolify/ssh"
			type:        "bind"
			backup:      true
			description: "SSH keys for remote node access"
		},
	]

	config: {
		appUrl:      string
		pushEnabled: bool | *true
		autoUpdate:  bool | *false
		instanceSettings: {
			isRegistrationEnabled: bool | *false
			isAutoUpdateEnabled:   bool | *false
		}
	}

	// Multi-node: Coolify manages remote Docker hosts via SSH
	multiNode: {
		enabled: true
		remoteHosts: [...{
			name:    string
			address: string
			user:    string | *"root"
			port:    int | *22
		}]
	}

	resources: {
		memory:    "2048m"
		memoryMax: "4096m"
		cpus:      2.0
	}

	labels: {
		"stackkit.layer":      "2-platform"
		"stackkit.managed-by": "terraform"
	}

	output: {
		url:         "https://coolify.{{.domain}}"
		description: "Coolify PaaS Dashboard"
		credentials: note: "Admin credentials set during first setup"
	}

	restartPolicy: "unless-stopped"
}

// Dokploy - Simpler PaaS for users WITHOUT domain (traefik-me + MagicDNS)
#DokployService: base.#ServiceDefinition & {
	name:        "dokploy"
	displayName: "Dokploy"
	category:    "platform"
	type:        "paas"
	required:    false
	image:       "dokploy/dokploy"
	tag:         "latest"
	status:      "planned"
	description: "Simple PaaS with traefik-me integration for domainless setups"
	needs:       ["traefik"]

	placement: {
		nodeType: "cloud"
		strategy: "single"
	}

	network: {
		ports: [
			{host: 3000, container: 3000, protocol: "tcp", description: "Web UI"},
		]
		traefik: {
			enabled: true
			rule:    "Host(`dokploy.{{.domain}}`)"
			tls:     true
			port:    3000
		}
	}

	volumes: [
		{
			source:      "/var/run/docker.sock"
			target:      "/var/run/docker.sock"
			type:        "bind"
			readOnly:    false
			backup:      false
			description: "Docker socket for container management"
		},
		{
			source:      "dokploy-data"
			target:      "/etc/dokploy"
			type:        "volume"
			backup:      true
			description: "Dokploy configuration and data"
		},
	]

	config: {
		traefikMe: bool | *true
		magicDns:  bool | *true
	}

	resources: {
		memory:    "1024m"
		memoryMax: "2048m"
		cpus:      1.0
	}

	labels: {
		"stackkit.layer":      "2-platform"
		"stackkit.managed-by": "terraform"
	}

	output: {
		url:         "https://dokploy.{{.domain}}"
		description: "Dokploy PaaS Dashboard"
		credentials: note: "Admin credentials set during first setup"
	}

	restartPolicy: "unless-stopped"
}

// =============================================================================
// LAYER 3: UTILITY SERVICES (Core Platform)
// =============================================================================

// Dozzle - Real-time Docker log viewer
#DozzleService: base.#ServiceDefinition & {
	name:        "dozzle"
	displayName: "Dozzle"
	category:    "monitoring"
	type:        "logging"
	required:    false
	enabled:     true
	image:       "amir20/dozzle"
	tag:         "latest"
	status:      "planned"
	description: "Real-time Docker container log viewer with multi-host support"
	needs:       ["traefik"]

	placement: {
		nodeType: "cloud"
		strategy: "single"
	}

	network: {
		ports: [
			{host: 8888, container: 8080, protocol: "tcp", description: "Web UI"},
		]
		traefik: {
			enabled: true
			rule:    "Host(`logs.{{.domain}}`)"
			tls:     true
			port:    8080
		}
	}

	volumes: [
		{
			source:      "/var/run/docker.sock"
			target:      "/var/run/docker.sock"
			type:        "bind"
			readOnly:    true
			backup:      false
			description: "Docker socket for log access"
		},
	]

	resources: {
		memory:    "128m"
		memoryMax: "256m"
		cpus:      0.25
	}

	labels: {
		"stackkit.layer":      "3-application"
		"stackkit.managed-by": "terraform"
	}

	output: {
		url:         "https://logs.{{.domain}}"
		description: "Dozzle Log Viewer"
		credentials: note: "Protected by TinyAuth ForwardAuth"
	}

	restartPolicy: "unless-stopped"
}

// Uptime Kuma - External uptime monitoring
#UptimeKumaService: base.#ServiceDefinition & {
	name:        "uptime-kuma"
	displayName: "Uptime Kuma"
	category:    "monitoring"
	type:        "uptime"
	required:    false
	enabled:     true
	image:       "louislam/uptime-kuma"
	tag:         "1"
	status:      "planned"
	description: "Self-hosted uptime monitoring and status page"
	needs:       ["traefik"]

	placement: {
		nodeType: "cloud"
		strategy: "single"
	}

	network: {
		ports: [
			{host: 3001, container: 3001, protocol: "tcp", description: "Web UI"},
		]
		traefik: {
			enabled: true
			rule:    "Host(`status.{{.domain}}`)"
			tls:     true
			port:    3001
		}
	}

	volumes: [
		{
			source:      "uptime-kuma-data"
			target:      "/app/data"
			type:        "volume"
			backup:      true
			description: "Uptime Kuma database"
		},
	]

	resources: {
		memory:    "256m"
		memoryMax: "512m"
		cpus:      0.5
	}

	labels: {
		"stackkit.layer":      "3-application"
		"stackkit.managed-by": "terraform"
	}

	output: {
		url:         "https://status.{{.domain}}"
		description: "Uptime Kuma Status Page"
		credentials: note: "Admin credentials set on first login"
	}

	restartPolicy: "unless-stopped"
}

// Whoami - Test/debug service for verifying Traefik routing
#WhoamiService: base.#ServiceDefinition & {
	name:        "whoami"
	displayName: "Whoami"
	category:    "debug"
	type:        "application"
	required:    false
	enabled:     false
	image:       "traefik/whoami"
	tag:         "latest"
	status:      "planned"
	description: "Debug service for testing Traefik routing and TLS"

	placement: {
		nodeType: "cloud"
		strategy: "single"
	}

	network: {
		ports: [
			{host: 8081, container: 80, protocol: "tcp", description: "HTTP"},
		]
		traefik: {
			enabled: true
			rule:    "Host(`whoami.{{.domain}}`)"
			tls:     true
			port:    80
		}
	}

	resources: {
		memory: "32m"
		cpus:   0.1
	}

	labels: {
		"stackkit.layer":      "3-application"
		"stackkit.managed-by": "terraform"
	}

	restartPolicy: "unless-stopped"
}

// =============================================================================
// SERVICE COLLECTIONS
// =============================================================================

// Services for Coolify-based deployment (user has domain + wildcard)
#CoolifyServiceSet: {
	traefik:    #TraefikService
	tinyauth:   #TinyAuthService
	pocketid:   #PocketIDService
	coolify:    #CoolifyService & {enabled: true}
	dozzle:     #DozzleService
	uptimeKuma: #UptimeKumaService
	whoami:     #WhoamiService
}

// Services for Dokploy-based deployment (no domain)
#DokployServiceSet: {
	traefik:    #TraefikService
	tinyauth:   #TinyAuthService
	pocketid:   #PocketIDService
	dokploy:    #DokployService & {enabled: true}
	dozzle:     #DozzleService
	uptimeKuma: #UptimeKumaService
	whoami:     #WhoamiService
}

// =============================================================================
// PLACEMENT DEFINITIONS
// =============================================================================

#DefaultPlacement: {
	// Cloud node: public-facing, management, and central OTLP fan-in.
	cloud: [
		"traefik",
		"tinyauth",
		"pocketid",
		"coolify",
		"dokploy",
		"uptime-kuma",
		"dozzle",
		"otel-gateway",
		"grafana",
		"victoriametrics",
		"loki",
		"vaultwarden",
		"stalwart",
	]

	// Local node: compute, storage, data sovereignty
	local: [
		"immich",
		"jellyfin",
		"ollama",
		"open-webui",
		"home-assistant",
		"cloudreve",
		"nextcloud",
		"radicale",
		"guacamole",
		"minio",
		"gitea",
	]

	// All nodes: node-local telemetry agents and supporting exporters.
	daemonset: [
		"grafana-alloy",
		"cadvisor",
		"node-exporter",
		"restic-agent",
	]
}
