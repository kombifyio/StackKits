// Package addons - VPN Overlay Add-On
//
// Provides Headscale/Tailscale mesh VPN for multi-node communication.
// Required for modern-homelab and recommended for ha-kit.
//
// Usage in stackfile.cue:
//   import "github.com/kombifyio/stackkits/addons/vpn-overlay"
//
//   addons: {
//       "vpn-overlay": vpnoverlay.#Config & {
//           provider: "headscale"
//           serverUrl: "https://hs.example.com"
//       }
//   }
//
package vpnoverlay

// #Config defines VPN overlay add-on configuration
#Config: {
	// Add-on metadata
	_addon: {
		name:        "vpn-overlay"
		displayName: "VPN Mesh Overlay"
		version:     "1.0.0"
		layer:       "NETWORK"
	}

	_compatibility: {
		stackkits: ["modern-homelab", "ha-kit"]
		contexts:  ["local", "cloud"]
		requires:  []
		conflicts: []
	}

	// Provider selection
	provider: *"headscale" | "tailscale" | "netbird" | "zerotier"

	// Enabled state (can be disabled to remove from stack)
	enabled: bool | *true

	// Provider-specific configuration
	if provider == "headscale" {
		headscale: #HeadscaleConfig
	}

	if provider == "tailscale" {
		tailscale: #TailscaleConfig
	}

	if provider == "netbird" {
		netbird: #NetbirdConfig
	}

	// Network settings
	network: {
		// VPN subnet (for route advertisement)
		subnet: string | *"100.64.0.0/10"

		// Advertise local routes to VPN
		advertiseRoutes: [...string] | *[]

		// Accept routes from other nodes
		acceptRoutes: bool | *true

		// Exit node mode
		exitNode: bool | *false

		// DNS settings
		dns: {
			enabled:    bool | *true
			magicDNS:   bool | *true
			nameserver: [...string] | *[]
		}
	}
}

// #HeadscaleConfig for self-hosted coordination server
#HeadscaleConfig: {
	// Server URL (required for clients)
	serverUrl: string

	// Auth key (secret reference)
	authKey?: =~"^secret://"

	// Namespace/user
	namespace: string | *"default"

	// Server-side configuration (for running Headscale itself)
	server?: {
		enabled: bool | *false

		// Server port
		port: uint16 | *443

		// GRPC port
		grpcPort: uint16 | *50443

		// Metrics port
		metricsPort: uint16 | *9090

		// Database
		database: {
			type: "sqlite" | "postgres" | *"sqlite"
			url:  string | *"/var/lib/headscale/db.sqlite"
		}

		// DERP (relay) configuration
		derp: {
			urls: [...string] | *["https://controlplane.tailscale.com/derpmap/default"]
			autoUpdate:             bool | *true
			updateFrequency:        string | *"24h"
			stun?: {
				listenAddr: string | *"0.0.0.0:3478"
			}
		}

		// OIDC authentication
		oidc?: {
			issuer:       string
			clientId:     string
			clientSecret: =~"^secret://"
			scope:        [...string] | *["openid", "profile", "email"]
		}
	}
}

// #TailscaleConfig for Tailscale.com coordination
#TailscaleConfig: {
	// Auth key from Tailscale admin console
	authKey: =~"^secret://"

	// Tailnet name
	tailnet?: string

	// Tags for ACL
	tags: [...string] | *[]

	// Hostname prefix
	hostname?: string

	// Serve settings (expose services via Tailscale Funnel)
	serve?: [...#TailscaleServe]
}

#TailscaleServe: {
	port:   uint16
	path:   string | *"/"
	funnel: bool | *false
}

// #NetbirdConfig for NetBird self-hosted or cloud
#NetbirdConfig: {
	// Management server URL
	managementUrl: string | *"https://api.netbird.io:443"

	// Setup key
	setupKey: =~"^secret://"

	// Admin URL for web UI
	adminUrl?: string
}

// Service definition for Headscale server
#HeadscaleService: {
	name:        "headscale"
	displayName: "Headscale"
	image:       string | *"headscale/headscale:latest"
	layer:       "NETWORK"
	category:    "vpn"

	ports: [
		{container: 443, host: 8443, protocol: "tcp", name: "https"},
		{container: 50443, host: 50443, protocol: "tcp", name: "grpc"},
		{container: 9090, host: 9091, protocol: "tcp", name: "metrics"},
		{container: 3478, host: 3478, protocol: "udp", name: "stun"},
	]

	volumes: [
		{name: "headscale_data", path: "/var/lib/headscale", type: "volume"},
		{name: "headscale_config", path: "/etc/headscale", type: "volume"},
	]

	environment: {
		TZ: string | *"UTC"
	}

	healthCheck: {
		test:     ["CMD", "headscale", "health"]
		interval: "30s"
		timeout:  "5s"
		retries:  3
	}

	traefik: {
		enabled: true
		rule:    string
		entryPoints: ["https"]
	}
}

// Service definition for Tailscale client sidecar
#TailscaleService: {
	name:        "tailscale"
	displayName: "Tailscale"
	image:       string | *"tailscale/tailscale:stable"
	layer:       "NETWORK"
	category:    "vpn"

	capAdd: ["NET_ADMIN", "NET_RAW", "SYS_MODULE"]

	volumes: [
		{name: "tailscale_state", path: "/var/lib/tailscale", type: "volume"},
		{host: "/dev/net/tun", path: "/dev/net/tun", type: "bind"},
	]

	environment: {
		TS_AUTHKEY:    string | *""
		TS_STATE_DIR:  "/var/lib/tailscale"
		TS_ACCEPT_DNS: string | *"true"
		TS_EXTRA_ARGS: string | *""
		TS_USERSPACE:  string | *"false"
	}
}

// #Outputs defines what this add-on exports to the stack
#Outputs: {
	// VPN interface name
	interface: string | *"tailscale0"

	// VPN IP (populated at runtime)
	vpnIP?: string

	// Coordination server URL
	coordinatorUrl?: string

	// Tags applied to this node
	tags: [...string]
}
