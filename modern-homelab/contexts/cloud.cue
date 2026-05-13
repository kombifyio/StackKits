// Package modern_homelab - Cloud Node Context
//
// Default configuration for cloud VPS nodes in the hybrid topology.
// Cloud nodes serve as public entry points, management planes,
// and always-on service hosts.

package modern_homelab

// #CloudContext defines defaults for cloud-type nodes
#CloudContext: {
	// Node type identifier
	nodeType: "cloud"

	// Cloud provider defaults
	provider: {
		name:   *"hetzner" | "digitalocean" | "vultr" | "linode"
		region: string | *"fsn1"
		size:   string | *"cx22"
		image:  string | *"debian-12"
	}

	// Compute defaults for cloud nodes
	compute: {
		cpuCores:  int & >=2 | *2
		ramGB:     int & >=2 | *4
		storageGB: int & >=20 | *40
	}

	// OS defaults
	os: {
		family:  *"debian" | "rhel"
		distro:  *"debian" | "ubuntu"
		version: string | *"12"
	}

	// Docker configuration
	docker: {
		version:  string | *"27.0"
		dataRoot: string | *"/var/lib/docker"
	}

	// System tuning for cloud nodes
	system: {
		swapEnabled: false
		swapSize:    0
		// Cloud nodes are always-on, optimize for uptime
		maxOpenFiles: int | *65535
	}

	// Services that run on cloud nodes
	services: [
		"traefik",
		"tinyauth",
		"coolify",
		"dokploy",
		"pocketid",
		"uptime-kuma",
		"dozzle",
		// Monitoring add-on services (if enabled)
		"grafana",
		"victoriametrics",
		"loki",
		// Application add-on services
		"vaultwarden",
		"stalwart",
	]

	// Network configuration
	network: {
		// Cloud nodes have public IPs
		publicAccess: true
		// Firewall rules
		inboundPorts: [22, 80, 443]
	}

	// Security defaults for cloud nodes
	security: {
		ssh: {
			permitRootLogin:        "prohibit-password"
			passwordAuthentication: false
		}
		firewall: {
			defaultInbound: "deny"
			defaultOutbound: "allow"
		}
	}
}
