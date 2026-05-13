// Package tunnel - Tunnel Add-On
//
// Provides CGNAT/DS-Lite bypass for local nodes behind NAT.
// Two providers:
//   - Cloudflare Tunnel (default): Free, managed, zero-trust access
//   - Pangolin: Self-hosted, WireGuard-based, includes SSO + Let's Encrypt
//
// License:
//   - Cloudflare Tunnel: Free tier (proprietary service)
//   - Pangolin: AGPL-3 (server) + Fossorial Commercial License (optional)
//
// Usage:
//   addons: tunnel: tunnel.#Config & {
//       provider: "cloudflare"
//       cloudflare: token: "secret://tunnel/cf-token"
//   }

package tunnel

// #Config defines tunnel add-on configuration
#Config: {
	_addon: {
		name:        "tunnel"
		displayName: "CGNAT Tunnel"
		version:     "1.0.0"
		layer:       "NETWORK"
		description: "Bypass CGNAT/DS-Lite to expose local services"
	}

	enabled: bool | *true

	// Provider selection
	provider: *"cloudflare" | "pangolin"

	// Cloudflare Tunnel configuration
	if provider == "cloudflare" {
		cloudflare: #CloudflareConfig
	}

	// Pangolin configuration
	if provider == "pangolin" {
		pangolin: #PangolinConfig
	}
}

// #CloudflareConfig for Cloudflare Tunnel (cloudflared)
#CloudflareConfig: {
	// Tunnel token (from Cloudflare Zero Trust dashboard)
	token: =~"^secret://"

	// Tunnel name
	tunnelName: string | *"homelab"

	// Zero Trust access policies
	zeroTrust: bool | *true

	// Ingress rules (map local services to public hostnames)
	ingress: [...#CloudflareIngress] | *[]

	// Metrics port for cloudflared
	metricsPort: uint16 | *2000

	// Protocol between cloudflared and origin
	originProtocol: "http" | "https" | *"http"
}

#CloudflareIngress: {
	hostname: string
	service:  string
	path?:    string
}

// #PangolinConfig for self-hosted Pangolin
#PangolinConfig: {
	// Server configuration (runs on cloud node)
	server: {
		// Domain for the Pangolin server
		domain: string

		// Server port (WireGuard)
		port: uint16 | *443

		// Dashboard port
		dashboardPort: uint16 | *8443

		// SSO configuration (built-in)
		sso: {
			enabled: bool | *true
		}

		// Let's Encrypt integration (built-in)
		letsEncrypt: {
			enabled: bool | *true
			email:   string
		}
	}

	// Client configuration (runs on local nodes)
	client: {
		// Server endpoint
		serverUrl: string

		// Client auth token
		token: =~"^secret://"

		// WireGuard interface
		interface: string | *"wg0"
	}
}

// Service definitions

#CloudflaredService: {
	name:        "cloudflared"
	displayName: "Cloudflare Tunnel"
	image:       "cloudflare/cloudflared:latest"
	category:    "tunnel"

	placement: {
		nodeType: "local"
		strategy: "daemonset"
	}

	volumes: [
		{name: "cloudflared-config", path: "/etc/cloudflared", type: "volume"},
	]

	environment: {
		TUNNEL_TOKEN: string
	}

	healthCheck: {
		test:     ["CMD", "cloudflared", "tunnel", "info"]
		interval: "30s"
		timeout:  "10s"
		retries:  3
	}
}

#PangolinServerService: {
	name:        "pangolin-server"
	displayName: "Pangolin Server"
	image:       "fossor/pangolin:latest"
	category:    "tunnel"

	placement: {
		nodeType: "cloud"
		strategy: "single"
	}

	ports: [
		{container: 443, host: 443, protocol: "tcp", name: "https"},
		{container: 8443, host: 8443, protocol: "tcp", name: "dashboard"},
		{container: 51820, host: 51820, protocol: "udp", name: "wireguard"},
	]

	volumes: [
		{name: "pangolin-data", path: "/data", type: "volume"},
		{name: "pangolin-config", path: "/etc/pangolin", type: "volume"},
	]
}

#PangolinClientService: {
	name:        "pangolin-client"
	displayName: "Pangolin Client"
	image:       "fossor/pangolin-client:latest"
	category:    "tunnel"

	placement: {
		nodeType: "local"
		strategy: "daemonset"
	}

	capAdd: ["NET_ADMIN"]

	volumes: [
		{name: "pangolin-client-config", path: "/etc/pangolin", type: "volume"},
	]
}

// #Outputs defines what this add-on exports
#Outputs: {
	// Tunnel provider in use
	provider: string

	// Tunnel status endpoint
	statusUrl?: string

	// Public hostname for tunnel access
	publicHostname?: string
}
