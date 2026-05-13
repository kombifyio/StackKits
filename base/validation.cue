// Package base - Reusable Validation Patterns
//
// Provides regex-based validators and constraint patterns
// used across all StackKit schemas for consistent input validation.

package base

// =============================================================================
// INPUT VALIDATION PATTERNS
// =============================================================================

// #Validators provides reusable regex patterns for common field types.
// Use these in schema fields: field: #Validators.ipv4
#Validators: {
	// IPv4 address (e.g., "192.168.1.1")
	ipv4: =~"^((25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)\\.){3}(25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)$"

	// CIDR notation (e.g., "10.0.0.0/16")
	cidr: =~"^((25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)\\.){3}(25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)/([0-9]|[12][0-9]|3[0-2])$"

	// Fully qualified domain name (e.g., "app.example.com")
	fqdn: =~"^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$"

	// Local domain suffixes (e.g., "mylab.local", "home.lan")
	localDomain: =~"\\.(local|lan|home|internal|test)$"

	// Email address
	email: =~"^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\\.[a-zA-Z]{2,}$"

	// Cron expression (5-field)
	cron: =~"^[0-9*,/\\-]+ [0-9*,/\\-]+ [0-9*,/\\-]+ [0-9*,/\\-]+ [0-9*,/\\-]+$"

	// Memory size (e.g., "512m", "2g", "1Gi")
	memorySize: =~"^[0-9]+(m|g|k|M|G|K|Mi|Gi|Ki)$"

	// Duration with unit (e.g., "30s", "5m", "2h", "7d")
	duration: =~"^[0-9]+(s|m|h|d)$"

	// Semantic version (e.g., "1.2.3", "v1.0.0-beta.1")
	semver: =~"^v?[0-9]+\\.[0-9]+\\.[0-9]+(-[a-zA-Z0-9.]+)?$"

	// Docker image reference (e.g., "nginx", "ghcr.io/org/img:tag")
	dockerImage: =~"^[a-z0-9]([a-z0-9._/-]*[a-z0-9])?$"

	// Hostname (DNS-compatible label)
	hostname: =~"^[a-z][a-z0-9-]*[a-z0-9]$"

	// Port number (as string for regex, use uint16 for typed)
	portString: =~"^[0-9]{1,5}$"

	// Subnet in private range (RFC1918)
	privateSubnet: =~"^(10\\.|172\\.(1[6-9]|2[0-9]|3[01])\\.|192\\.168\\.)"
}

// =============================================================================
// CONSTRAINT TYPES
// =============================================================================

// #PortRange constrains a port to valid TCP/UDP range
#PortRange: uint16 & >0 & <=65535

// #MemoryMB constrains memory in megabytes (min 64MB)
#MemoryMB: int & >=64

// #DiskGB constrains disk in gigabytes (min 1GB)
#DiskGB: int & >=1

// #CPUCores constrains CPU core count
#CPUCores: int & >=1 & <=256

// #ReplicaCount constrains replica counts (1-99)
#ReplicaCount: int & >=1 & <=99

// =============================================================================
// DOMAIN TYPE DETECTION
// =============================================================================

// #DomainType classifies a domain as local or public based on suffix
#DomainType: {
	domain: string

	// Computed: is this a local-only domain?
	_isLocal: bool
	if domain =~ "\\.(local|lan|home|internal|test)$" {
		_isLocal: true
	}
	if domain !~ "\\.(local|lan|home|internal|test)$" {
		_isLocal: false
	}
}

// =============================================================================
// TLS DECISION LOGIC
// =============================================================================

// #TLSDecision determines TLS strategy based on domain type and user choice
#TLSDecision: {
	// TLS mode
	mode: "acme" | "self-signed" | "custom" | "none"

	// ACME-specific config (required when mode == "acme")
	if mode == "acme" {
		provider:  "letsencrypt" | "letsencrypt-staging" | "zerossl" | *"letsencrypt"
		challenge: "http" | "dns" | *"http"

		// DNS challenge requires provider config
		if challenge == "dns" {
			dnsProvider: "cloudflare" | "route53" | "hetzner" | "digitalocean" | "manual"
		}
	}

	// Custom certs (required when mode == "custom")
	if mode == "custom" {
		certFile: string
		keyFile:  string
	}
}

// =============================================================================
// BACKUP DECISION TREE
// =============================================================================

// #BackupDecision provides a decision tree for backup configuration
#BackupDecision: {
	enabled: bool | *true

	// When enabled, all backup fields are required
	if enabled == true {
		backend:  "restic" | "borgbackup" | "rclone" | *"restic"
		schedule: string | *"0 3 * * *"

		retention: {
			daily:   int & >=1 | *7
			weekly:  int & >=0 | *4
			monthly: int & >=0 | *6
		}

		destination: #BackupDestination
	}
}

// #BackupDestination defines where backups are stored
#BackupDestination: {
	type: "local" | "s3" | "sftp" | "b2"

	if type == "local" {
		path: string | *"/opt/backups"
	}
	if type == "s3" {
		bucket:    string
		endpoint?: string // Custom S3 endpoint (MinIO, Wasabi, etc.)
		region:    string | *"us-east-1"
	}
	if type == "sftp" {
		host: string
		port: uint16 | *22
		user: string
		path: string
	}
	if type == "b2" {
		bucket: string
	}
}

// =============================================================================
// ALERTING DECISION TREE
// =============================================================================

// #AlertingDecision provides alerting configuration with channel-specific fields
#AlertingDecision: {
	enabled: bool | *false

	// When enabled, at least one channel must be configured
	if enabled == true {
		channels: [...#NotificationChannel] & [_, ...]
	}
}

// #NotificationChannel defines a notification target with type-specific fields
#NotificationChannel: {
	type: "email" | "slack" | "discord" | "telegram" | "webhook" | "gotify"
	name: string | *type

	if type == "email" {
		smtp: {
			host: string
			port: uint16 | *587
			from: string
			to:   [...string] & [_, ...]
		}
	}
	if type == "slack" {
		webhookUrl: string
		channel?:   string
	}
	if type == "discord" {
		webhookUrl: string
	}
	if type == "telegram" {
		botToken: string
		chatId:   string
	}
	if type == "gotify" {
		url:      string
		token:    string
		priority: int & >=1 & <=10 | *5
	}
	if type == "webhook" {
		url:     string
		method:  "POST" | "PUT" | *"POST"
		headers?: [string]: string
	}
}

// =============================================================================
// STORAGE CONFIGURATION
// =============================================================================

// #StorageConfig defines storage tier and paths
#StorageConfig: {
	// Data directory for application data
	dataDir: string | *"/opt/data"

	// Docker data root
	dockerDataRoot: string | *"/var/lib/docker"

	// Stacks directory (for compose files)
	stacksDir: string | *"/opt/stacks"

	// Backup directory (local backups)
	backupDir: string | *"/opt/backups"

	// Volume driver
	volumeDriver: "local" | "nfs" | *"local"

	// NFS config (only when volumeDriver == "nfs")
	if volumeDriver == "nfs" {
		nfs: {
			server: string
			path:   string
		}
	}
}
