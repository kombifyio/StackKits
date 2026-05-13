// Package modern_homelab - Local Node Context
//
// Default configuration for on-premises/homelab nodes in the hybrid topology.
// Local nodes provide compute power, storage, and data sovereignty.
// They are reached via tunnel (Cloudflare/Pangolin), not direct public access.

package modern_homelab

// #LocalContext defines defaults for local-type nodes
#LocalContext: {
	// Node type identifier
	nodeType: "local"

	// Compute defaults for local nodes (bare-metal or VM)
	compute: {
		cpuCores:  int & >=2 | *4
		ramGB:     int & >=4 | *16
		storageGB: int & >=50 | *200
	}

	// OS defaults
	os: {
		family:  *"debian" | "rhel"
		distro:  *"ubuntu" | "debian"
		version: string | *"24.04"
	}

	// Docker configuration
	docker: {
		version:  string | *"27.0"
		dataRoot: string | *"/var/lib/docker"
	}

	// System tuning for local nodes
	system: {
		swapEnabled: true
		swapSize:    int | *4096
		// Local nodes may have more resources
		maxOpenFiles: int | *65535
	}

	// GPU support (local nodes often have GPUs)
	gpu?: {
		vendor: "nvidia" | "amd" | "intel"
		model?: string
		vramGB?: int
		// NVIDIA container toolkit
		nvidiaRuntime: bool | *true
	}

	// Services that run on local nodes
	services: [
		// Application workloads deployed via PaaS
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

	// Daemonset services (run on every node including local)
	daemonset: [
		"grafana-alloy",
		"cadvisor",
		"node-exporter",
		"restic-agent",
	]

	// Network configuration
	network: {
		// Local nodes are behind NAT/CGNAT
		publicAccess: false
		// Reached via tunnel from cloud node
		accessMethod: *"tunnel" | "vpn" | "direct"
	}

	// Storage paths for local data
	storage: {
		// Base path for application data
		dataPath: string | *"/data"
		// Media storage
		mediaPath: string | *"/data/media"
		// Backup staging area
		backupPath: string | *"/backup"
		// Photo storage (Immich)
		photosPath: string | *"/data/photos"
	}

	// Security defaults for local nodes
	security: {
		ssh: {
			permitRootLogin:        "prohibit-password"
			passwordAuthentication: false
		}
		// Local nodes don't need public firewall rules
		firewall: {
			defaultInbound:  "deny"
			defaultOutbound: "allow"
			// Only allow SSH and tunnel traffic
			inboundPorts: [22]
		}
	}
}
