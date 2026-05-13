// =============================================================================
// PLATFORM: DOCKER - CUE SCHEMA DEFINITION
// =============================================================================
// Layer 2 (PLATFORM): CUE schema for Docker platform configuration
//
// This schema defines:
// - Docker configuration options
// - Traefik reverse proxy options
// - Network configuration
// - Platform-specific constraints
// =============================================================================

package docker

import (
	"github.com/kombifyio/stackkits/base"
)

// #DockerPlatform extends base configuration for Docker deployments
#DockerPlatform: base.#BaseStackKit & {
	// Platform identifier
	platform: "docker"
	
	// Docker-specific configuration
	docker: #DockerConfig
	
	// Traefik reverse proxy configuration
	traefik: #TraefikConfig
	
	// Docker network configuration
	networks: [...#DockerNetwork]
}

// #DockerConfig defines Docker daemon and runtime settings
#DockerConfig: {
	// Docker version constraint (minimum)
	version: string | *"24.0"
	
	// Docker Compose version
	compose_version: string | *"2.24"
	
	// Logging driver configuration
	logging: {
		driver: "json-file" | "journald" | "local" | *"json-file"
		max_size: string | *"10m"
		max_file: string | *"3"
	}
	
	// Storage driver
	storage_driver: "overlay2" | "btrfs" | "zfs" | *"overlay2"
	
	// Enable BuildKit by default
	buildkit: bool | *true
	
	// Automatic cleanup settings
	auto_prune: {
		enabled: bool | *true
		schedule: string | *"0 4 * * 0"  // Weekly on Sunday at 4 AM
		// What to prune
		images: bool | *true
		containers: bool | *true
		volumes: bool | *false  // Dangerous: could lose data
		networks: bool | *true
	}
	
	// Resource limits
	default_ulimits: {
		nofile: {
			soft: int | *65535
			hard: int | *65535
		}
	}
}

// #TraefikConfig defines Traefik reverse proxy settings
#TraefikConfig: {
	// Enable Traefik
	enabled: bool | *true
	
	// Traefik version
	version: string | *"v3.1"
	
	// Dashboard configuration
	dashboard: {
		enabled: bool | *true
		// Insecure mode (no auth) - NOT recommended for production
		insecure: bool | *false
		// Basic auth credentials (if not insecure)
		auth?: {
			username: string
			password: string  // Should be htpasswd encoded
		}
	}
	
	// TLS configuration (depends on network mode)
	tls: {
		// auto = use ACME for public, self-signed for local
		mode: "auto" | "acme" | "self-signed" | "custom" | *"auto"
		
		// ACME configuration (for public mode)
		acme?: {
			email: string
			// Challenge type
			challenge: "http" | "dns" | *"http"
			// DNS provider (if challenge is dns)
			dns_provider?: string
		}
		
		// Custom certificates
		custom?: {
			cert_file: string
			key_file: string
		}
	}
	
	// Entrypoints
	entrypoints: {
		http_port: int | *80
		https_port: int | *443
		// Additional ports
		extra: [...{
			name: string
			port: int
			protocol: "tcp" | "udp" | *"tcp"
		}]
	}
	
	// Logging
	logging: {
		level: "DEBUG" | "INFO" | "WARN" | "ERROR" | *"INFO"
		access_log: bool | *true
	}
}

// #DockerNetwork defines a Docker network
#DockerNetwork: {
	// Network name
	name: string
	
	// Network driver
	driver: "bridge" | "overlay" | "macvlan" | "host" | *"bridge"
	
	// Enable IPv6
	ipv6: bool | *false
	
	// Internal network (no external access)
	internal: bool | *false
	
	// Custom subnet (optional)
	subnet?: string
	
	// Custom gateway (optional)
	gateway?: string
}

// #DockerService defines a service deployed via Docker/Compose
#DockerService: {
	// Service name
	name: string
	
	// Docker image
	image: string
	
	// Image tag
	tag: string | *"latest"
	
	// Restart policy
	restart: "no" | "always" | "unless-stopped" | "on-failure" | *"unless-stopped"
	
	// Networks to join
	networks: [...string] | *["kombistack"]
	
	// Port mappings
	ports: [...{
		host: int
		container: int
		protocol: "tcp" | "udp" | *"tcp"
	}]
	
	// Volume mounts
	volumes: [...{
		source: string
		target: string
		type: "bind" | "volume" | "tmpfs" | *"bind"
		read_only: bool | *false
	}]
	
	// Environment variables
	environment: [string]: string
	
	// Traefik labels for routing
	traefik?: {
		enabled: bool | *true
		rule: string  // e.g., "Host(`app.localhost`)"
		entrypoints: [...string] | *["websecure"]
		tls: bool | *true
		middlewares: [...string]
	}
	
	// Health check
	healthcheck?: {
		test: string
		interval: string | *"30s"
		timeout: string | *"10s"
		retries: int | *3
		start_period: string | *"10s"
	}
	
	// Resource limits
	resources?: {
		limits?: {
			cpus: string
			memory: string
		}
		reservations?: {
			cpus: string
			memory: string
		}
	}
	
	// Dependencies
	depends_on: [...string]
}

// Default networks for Docker platform
#DefaultDockerNetworks: [
	{
		name: "kombistack"
		driver: "bridge"
		internal: false
	},
	{
		name: "kombistack-internal"
		driver: "bridge"
		internal: true
	}
]
