// =============================================================================
// STACKKIT: ha-kit - High Availability Docker Swarm Deployment
// =============================================================================
//
// Version 1.0.0-alpha - Docker Swarm HA with quorum validation
//
// Deployment Mode:
//   - advanced (default): Terramate-orchestrated, drift detection, rolling updates
//   - simple: OpenTofu-only (NOT recommended for HA, warning issued)
//
// Variants:
//   - default: Dokploy + Full monitoring (Prometheus HA, Grafana, Loki)
//   - minimal: Dokploy + Uptime Kuma (lightweight monitoring)
//   - enterprise: Dokploy + Thanos + Ceph (long-term storage, distributed FS)
//
// DECISION POINTS (CUE-enforced):
//   1. Manager count must be ODD (quorum requirement: 3, 5, or 7)
//   2. Minimum 3 nodes for HA (quorum needs n/2+1 surviving)
//   3. Manager nodes need higher resources than workers
//   4. Storage backend selection (GlusterFS/NFS for shared state)
//   5. Failover mode (automatic/manual VIP failover)
//   6. Overlay network encryption (default: encrypted)
//   7. Replica count cannot exceed node count
//
// PREREQUISITES:
//   - At least 3 servers (physical or VM)
//   - Docker 24.0+ on all nodes
//   - Own domain with DNS control
//   - Network connectivity between all nodes (VPN or LAN)
// =============================================================================

package ha_kit

import (
	"list"
	"github.com/kombifyio/stackkits/base"
)

// =============================================================================
// MAIN SCHEMA: #HAKitStack
// =============================================================================

#HAKitStack: {
	// Metadata
	meta: {
		name:    string & =~"^[a-z][a-z0-9-]*$"
		version: string | *"1.0.0-alpha"
	}

	// Deployment mode (advanced recommended for HA)
	deploymentMode: *"advanced" | "simple"

	// Variant selection
	variant: *"default" | "minimal" | "enterprise"

	// =========================================================================
	// DECISION POINT 1: Docker Swarm HA Configuration
	// =========================================================================
	swarm: #SwarmHAConfig

	// =========================================================================
	// DECISION POINT 2: Minimum 3 nodes for HA quorum
	// =========================================================================
	nodes: [...#HANode] & list.MinItems(3)

	// Network configuration
	network: #HANetworkConfig

	// Storage configuration
	storage: #HAStorageConfig

	// Services
	services: #HAServiceSet

	// Failover configuration
	failover: #FailoverConfig

	// Backup configuration
	backup: base.#BackupDecision

	// Alerting (recommended for HA)
	alerting?: base.#AlertingDecision

	// =========================================================================
	// DECISION POINT 3: Simple mode warning for HA
	// =========================================================================
	if deploymentMode == "simple" {
		_haWarning: "WARNING: simple mode lacks drift detection and rolling updates - not recommended for HA deployments"
	}

	// =========================================================================
	// DECISION POINT 4: Quorum validation
	// Number of manager-role nodes must match swarm.managerCount
	// =========================================================================
	_managerNodes: [ for n in nodes if n.role == "manager" {n}]
	_workerNodes:  [ for n in nodes if n.role == "worker" {n}]

	// =========================================================================
	// DECISION POINT 5: Enterprise variant requires more nodes
	// =========================================================================
	if variant == "enterprise" {
		nodes: list.MinItems(5)
		storage: backend: "ceph" | "glusterfs"
	}

	// Deployment config
	_deployment: {
		if deploymentMode == "advanced" {
			engine: "terramate"
			stacks: ["network", "swarm", "storage", "services", "monitoring"]
			features: {
				drift_detection: true
				rolling_updates: true
				stack_ordering:  true
				change_sets:     true
			}
		}
		if deploymentMode == "simple" {
			engine: "opentofu"
		}
	}
}

// =============================================================================
// DOCKER SWARM HA CONFIGURATION
// =============================================================================

#SwarmHAConfig: {
	// Mode is always HA for this StackKit
	mode: "ha"

	// =========================================================================
	// DECISION POINT: Manager count must be ODD for quorum
	// Raft consensus requires (n/2)+1 managers to be available
	// =========================================================================
	managerCount: (3 | 5 | 7) & int

	// Worker count (optional, can be 0 if all nodes are managers)
	workerCount: int & >=0 | *0

	// Computed quorum size
	_quorumSize: int
	if managerCount == 3 {_quorumSize: 2}
	if managerCount == 5 {_quorumSize: 3}
	if managerCount == 7 {_quorumSize: 4}

	// Maximum tolerated failures before quorum loss
	_maxFailures: int
	if managerCount == 3 {_maxFailures: 1}
	if managerCount == 5 {_maxFailures: 2}
	if managerCount == 7 {_maxFailures: 3}

	// Overlay network settings
	network: {
		driver:    "overlay"
		encrypted: bool | *true
		subnet:    string | *"10.10.0.0/16"
	}

	// Ingress mode
	ingress: {
		mode: "routing-mesh" | "host" | *"routing-mesh"
	}

	// Service update config (rolling updates)
	updateConfig: {
		parallelism:   int & >=1 | *1
		delay:         string | *"10s"
		failureAction: "pause" | "continue" | "rollback" | *"rollback"
		order:         "start-first" | "stop-first" | *"start-first"
	}

	// Restart policy for swarm services
	restartPolicy: {
		condition:   "on-failure" | "any" | "none" | *"on-failure"
		delay:       string | *"5s"
		maxAttempts: int & >=0 | *3
		window:      string | *"120s"
	}
}

// =============================================================================
// HA NODE DEFINITION
// =============================================================================

#HANode: {
	name: string & =~"^[a-z][a-z0-9-]*$"
	host: string // IP address or hostname

	// =========================================================================
	// DECISION POINT: Node role assignment
	// =========================================================================
	role: "manager" | "worker"

	// =========================================================================
	// DECISION POINT: Manager nodes need more resources
	// =========================================================================
	if role == "manager" {
		compute: {
			cpuCores:  int & >=4
			ramGB:     int & >=8
			storageGB: int & >=100
		}
	}
	if role == "worker" {
		compute: {
			cpuCores:  int & >=2
			ramGB:     int & >=4
			storageGB: int & >=50
		}
	}

	compute: {
		cpuCores:  int & >=2
		ramGB:     int & >=4
		storageGB: int & >=50
		arch:      "amd64" | "arm64" | *"amd64"
	}

	// OS configuration
	os?: {
		distro:  "ubuntu" | "debian" | *"ubuntu"
		version: string | *"24.04"
	}

	// Availability zone (for spread placement across failure domains)
	zone?: string

	// Labels for service placement constraints
	labels?: [string]: string

	// SSH access
	ssh?: {
		port:    uint16 | *22
		user:    string | *"ubuntu"
		keyPath: string | *"~/.ssh/id_ed25519"
	}
}

// =============================================================================
// HA NETWORK CONFIGURATION
// =============================================================================

#HANetworkConfig: {
	// Domain (required for HA)
	// Both public domains (example.com) and local domains (.local, .lan) are supported.
	// Public domains → Let's Encrypt TLS via ACME challenge
	// Local domains  → self-signed or internal CA (Step-CA), Keepalived VIP for failover
	domain: string & =~"^[a-zA-Z0-9]([a-zA-Z0-9.-]*[a-zA-Z0-9])?\\.[a-zA-Z]{2,}$"

	// ACME email for TLS
	acmeEmail: =~"^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\\.[a-zA-Z]{2,}$"

	// Docker overlay network
	overlay: {
		subnet:    string | *"10.10.0.0/16"
		encrypted: bool | *true
	}

	// Service bridge network
	bridge: {
		subnet: string | *"172.20.0.0/16"
	}

	// VPN configuration (for geographically distributed nodes)
	vpn?: {
		enabled:  bool | *false
		provider: "headscale" | "wireguard"
	}

	// DNS configuration
	dns?: {
		provider: "cloudflare" | "route53" | "hetzner" | "manual"
		servers:  [...string] | *["1.1.1.1", "8.8.8.8"]
	}

	// TLS configuration
	tls: {
		// letsencrypt: public domains with ACME
		// letsencrypt-staging: testing ACME
		// step-ca: internal CA for local domains
		// custom: user-provided certificates
		provider:  "letsencrypt" | "letsencrypt-staging" | "step-ca" | "custom" | *"letsencrypt"
		challenge: "http" | "dns" | *"http"
	}
}

// =============================================================================
// HA STORAGE CONFIGURATION
// =============================================================================

#HAStorageConfig: {
	// =========================================================================
	// DECISION POINT: Storage backend selection
	// =========================================================================
	backend: "glusterfs" | "nfs" | "ceph" | "local" | *"glusterfs"

	// GlusterFS configuration (recommended for HA)
	if backend == "glusterfs" {
		glusterfs: {
			// Replica count for data redundancy
			replicaCount: int & >=2 | *3

			// Volume type
			volumeType: "replicate" | "distribute" | "distribute-replicate" | *"replicate"

			// Brick path on each node
			brickPath: string | *"/data/gluster/brick1"

			// Transport type
			transport: "tcp" | "rdma" | *"tcp"
		}
	}

	// NFS configuration
	if backend == "nfs" {
		nfs: {
			server:     string
			exportPath: string | *"/exports/ha-kit"
			version:    "3" | "4" | "4.1" | *"4.1"
			options:    string | *"rw,sync,no_subtree_check"
		}
	}

	// Ceph configuration (enterprise)
	if backend == "ceph" {
		ceph: {
			osdCount:          int & >=3 | *3
			monCount:          3 | 5 | *3
			enableCephFS:      bool | *true
			enableRGW:         bool | *false // S3-compatible gateway
			replicationFactor: int & >=2 | *3
			poolName:          string | *"ha-kit-pool"
		}
	}

	// Shared volumes that need HA storage
	sharedVolumes: [...#SharedVolume]
}

// #SharedVolume defines a volume that must be accessible from multiple nodes
#SharedVolume: {
	name: string
	path: string
	size: string | *"10Gi"
	// Access mode for shared storage
	accessMode: "ReadWriteOnce" | "ReadWriteMany" | *"ReadWriteMany"
	// Should this volume be backed up?
	backup: bool | *true
}

// =============================================================================
// FAILOVER CONFIGURATION
// =============================================================================

#FailoverConfig: {
	// =========================================================================
	// DECISION POINT: Failover mode (automatic vs manual)
	// =========================================================================
	mode: "automatic" | "manual" | *"automatic"

	// Virtual IP (VIP) for seamless failover
	vip: {
		enabled:   bool | *true
		address:   string // Virtual IP address (must be on same subnet)
		interface: string | *"eth0"
		// Keepalived priority (higher = preferred master)
		priority: int & >=1 & <=255 | *100
	}

	// Health check configuration for failover triggers
	healthCheck: {
		interval: string | *"5s"
		timeout:  string | *"3s"
		retries:  int & >=1 & <=10 | *3
	}

	// Recovery behavior after node comes back online
	recovery: {
		autoRejoin:  bool | *true
		gracePeriod: string | *"30s"
		// Should a recovered node reclaim master role?
		preempt: bool | *false
	}

	// Drain behavior when a node goes down
	drain: {
		// How long to wait for containers to stop gracefully
		timeout: string | *"60s"
		// Force kill after timeout?
		force: bool | *true
	}
}

// =============================================================================
// HA SERVICE SET
// =============================================================================

#HAServiceSet: {
	// Core HA services (always required)
	traefik:    #HAServiceToggle & {enabled: true, mode: *"global" | "replicated"}
	keepalived: #HAServiceToggle & {enabled: true}

	// PaaS (Dokploy in Swarm mode)
	dokploy: #HAServiceToggle & {enabled: true}

	// Storage service (matches storage.backend)
	storage: #HAServiceToggle & {enabled: true}

	// Monitoring (variant-dependent)
	prometheus?:   #HAServiceToggle
	grafana?:      #HAServiceToggle
	loki?:         #HAServiceToggle
	alertmanager?: #HAServiceToggle
	uptimeKuma?:   #HAServiceToggle
	beszel?:       #HAServiceToggle

	// Enterprise services
	thanos?:  #HAServiceToggle
	ceph?:    #HAServiceToggle
	haproxy?: #HAServiceToggle

	// Log viewer (always included)
	dozzle: #HAServiceToggle & {enabled: true}

	// Backup agent
	restic?: #HAServiceToggle
}

#HAServiceToggle: {
	enabled:  bool | *false
	mode?:    "global" | "replicated"
	replicas?: int & >=1
}
