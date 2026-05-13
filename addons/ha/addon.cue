// Package addons - High Availability Add-On
//
// Provides platform-level HA for multi-node deployments.
// Adds load balancing, VIP failover, database HA, cache HA,
// service discovery, and shared storage.
//
// Requires: minimum 3 nodes (quorum requirement).
// Extends: modern-homelab or any StackKit with 3+ nodes.
//
// License:
//   - HAProxy: GPL-2.0
//   - Keepalived: GPL-2.0
//   - Patroni: MIT
//   - etcd: Apache-2.0
//   - CoreDNS: Apache-2.0
//   - Valkey: BSD-3-Clause
//   - GlusterFS: GPL-3.0
//   - DRBD: GPL-2.0
//   - OpenZFS: CDDL-1.0
//   - Litestream: Apache-2.0
//   - LiteFS: Apache-2.0
//   All components are SaaS-safe under config-generation model.
//   See docs/license-compliance-saas.md for full analysis.
//
// Usage in stackfile.cue:
//   import "github.com/kombifyio/stackkits/addons/ha"
//
//   addons: {
//       "ha": ha.#Config & {
//           vip: address: "192.168.1.100"
//           vip: interface: "eth0"
//           database: enabled: true
//       }
//   }
//
package ha

// #Config defines the HA add-on configuration
#Config: {
	_addon: {
		name:        "ha"
		displayName: "High Availability"
		version:     "1.0.0"
		layer:       "PLATFORM"
		minNodes:    3
		description: "Platform-level HA: load balancing, VIP failover, database HA, cache HA, service discovery, shared storage"
	}

	enabled: bool | *true

	// =========================================================================
	// QUORUM
	// =========================================================================

	quorum: #QuorumConfig

	// =========================================================================
	// VIP FAILOVER (Keepalived)
	// =========================================================================

	vip: #VIPConfig

	// =========================================================================
	// LOAD BALANCING (HAProxy)
	// =========================================================================

	loadBalancer: #LoadBalancerConfig

	// =========================================================================
	// SERVICE DISCOVERY (CoreDNS + etcd)
	// =========================================================================

	discovery: #DiscoveryConfig

	// =========================================================================
	// DATABASE HA (Patroni + etcd + HAProxy)
	// =========================================================================

	database: #DatabaseHAConfig

	// =========================================================================
	// CACHE HA (Valkey Sentinel)
	// =========================================================================

	cache: #CacheHAConfig

	// =========================================================================
	// SHARED STORAGE
	// =========================================================================

	storage: #StorageConfig

	// =========================================================================
	// SQLITE HA (Litestream / LiteFS)
	// =========================================================================

	sqlite: #SQLiteHAConfig

	// =========================================================================
	// ORCHESTRATION
	// =========================================================================

	orchestration: #OrchestrationConfig

	// =========================================================================
	// SECURITY HARDENING
	// =========================================================================

	security: #HASecurityConfig
}

// =============================================================================
// QUORUM
// =============================================================================

#QuorumConfig: {
	// Minimum nodes for HA quorum (must be odd, >= 3)
	minNodes: int & >=3 | *3

	// Consensus store (etcd is the only option -- Consul is BSL-1.1)
	consensusStore: "etcd"
}

// =============================================================================
// VIP FAILOVER
// =============================================================================

#VIPConfig: {
	enabled: bool | *true

	// Virtual IP address (required if enabled)
	address: string

	// Network interface for VIP binding
	interface: string | *"eth0"

	// VIP mode (auto-detected from node context if not set)
	//   vrrp:        Standard VRRP for local/private networks
	//   floating-ip: Cloud provider API (Hetzner, DigitalOcean, Vultr)
	//   dns-failover: Low-TTL DNS health checks (universal, slower)
	mode: *"vrrp" | "floating-ip" | "dns-failover"

	// VRRP-specific settings
	if mode == "vrrp" || mode == "floating-ip" {
		vrrp: #VRRPConfig
	}

	// Cloud floating IP settings
	if mode == "floating-ip" {
		floatingIP: #FloatingIPConfig
	}

	// DNS failover settings
	if mode == "dns-failover" {
		dnsFailover: #DNSFailoverConfig
	}
}

#VRRPConfig: {
	// VRRP router ID (unique per network, 1-255)
	routerID: int & >=1 & <=255 | *51

	// Priority of this node (highest = master, 1-254)
	priority: int & >=1 & <=254 | *100

	// Advertisement interval
	advertInterval: int | *1

	// Unicast mode (required for cloud, optional for local)
	unicast: bool | *false

	// Unicast peers (IP addresses of other Keepalived nodes)
	unicastPeers: [...string] | *[]

	// Don't reclaim VIP when recovered node comes back
	nopreempt: bool | *true

	// Authentication
	auth?: {
		type:     "PASS" | "AH" | *"PASS"
		password: =~"^secret://"
	}
}

#FloatingIPConfig: {
	// Cloud provider for API calls
	provider: "hetzner" | "digitalocean" | "vultr" | "linode"

	// Floating IP ID (provider-specific)
	floatingIPID: string

	// API token for reassignment (secret reference)
	apiToken: =~"^secret://"
}

#DNSFailoverConfig: {
	// DNS provider for automated record updates
	provider: "cloudflare" | "hetzner-dns" | "route53" | "custom"

	// TTL for DNS records (lower = faster failover, more DNS queries)
	ttl: int | *30

	// Health check endpoint
	healthEndpoint: string | *"/health"

	// Check interval
	checkInterval: string | *"10s"
}

// =============================================================================
// LOAD BALANCING
// =============================================================================

#LoadBalancerConfig: {
	enabled: bool | *true

	// Provider (HAProxy is the only SaaS-safe option)
	provider: "haproxy"

	// Health check settings for backends
	healthCheck: {
		interval: string | *"3s"
		fall:     int | *2
		rise:     int | *3
		method:   "GET" | "TCP" | *"GET"
		path:     string | *"/health"
	}

	// Stats dashboard
	stats: {
		enabled: bool | *true
		port:    uint16 | *8404
		auth?:   =~"^secret://"
	}

	// Connection draining timeout during maintenance
	drainTimeout: string | *"30s"
}

// =============================================================================
// SERVICE DISCOVERY
// =============================================================================

#DiscoveryConfig: {
	enabled: bool | *true

	// Provider: CoreDNS + etcd (default, Apache-2.0)
	// Consul available as separate add-on with BSL-1.1 license warning
	provider: *"coredns-etcd" | "dns-static"

	// DNS zone for service discovery
	zone: string | *"ha.local"

	// etcd settings (shared with database HA)
	etcd: {
		// Number of etcd nodes (must match quorum minNodes)
		nodes: int | *3

		// Client port
		clientPort: uint16 | *2379

		// Peer port
		peerPort: uint16 | *2380

		// Data directory
		dataDir: string | *"/var/lib/etcd"

		// Snapshot interval
		snapshotCount: int | *10000

		// Compaction (auto or periodic)
		autoCompaction: {
			mode:      "periodic" | "revision" | *"periodic"
			retention: string | *"1h"
		}
	}

	// CoreDNS settings
	if provider == "coredns-etcd" {
		coredns: {
			// DNS listen port
			port: uint16 | *53

			// Cache TTL
			cacheTTL: int | *30

			// Upstream DNS for non-service queries
			upstream: [...string] | *["8.8.8.8", "1.1.1.1"]
		}
	}

	// Health check sidecar (updates etcd with service health)
	healthSidecar: {
		enabled:  bool | *true
		interval: string | *"5s"
		timeout:  string | *"3s"
	}
}

// =============================================================================
// DATABASE HA
// =============================================================================

#DatabaseHAConfig: {
	enabled: bool | *true

	// Provider: Patroni manages PostgreSQL HA via etcd
	provider: "patroni"

	// Number of PostgreSQL replicas (excluding primary)
	replicas: int | *2

	// PostgreSQL version
	pgVersion: string | *"16"

	// Patroni settings
	patroni: {
		// REST API port
		apiPort: uint16 | *8008

		// Retry timeout for leader election
		retryTimeout: int | *10

		// TTL for leader lock in etcd
		ttl: int | *30

		// Loop wait between health checks
		loopWait: int | *10

		// Maximum lag for synchronous replication (bytes)
		maxLagOnFailover: int | *1048576
	}

	// HAProxy for PostgreSQL connection routing
	proxy: {
		enabled: bool | *true
		port:    uint16 | *5432

		// Read-only port for replica connections
		readOnlyPort: uint16 | *5433
	}

	// Backup settings
	backup: {
		// WAL archiving for point-in-time recovery
		walArchiving: bool | *true

		// Base backup schedule (cron)
		schedule: string | *"0 2 * * *"

		// Backup target
		target: "local" | "s3" | *"local"
	}
}

// =============================================================================
// CACHE HA
// =============================================================================

#CacheHAConfig: {
	enabled: bool | *true

	// Provider: Valkey Sentinel (BSD-3, drop-in Redis replacement)
	provider: "valkey-sentinel"

	// Number of Sentinel instances
	sentinelCount: int & >=3 | *3

	// Sentinel port
	sentinelPort: uint16 | *26379

	// Valkey port
	valkeyPort: uint16 | *6379

	// Quorum for failover (majority of sentinels)
	quorum: int | *2

	// Down-after-milliseconds (detect failure)
	downAfterMs: int | *5000

	// Failover timeout
	failoverTimeout: int | *60000

	// Memory limit per instance
	maxMemory: string | *"256mb"

	// Eviction policy
	maxMemoryPolicy: "allkeys-lru" | "volatile-lru" | "noeviction" | *"allkeys-lru"
}

// =============================================================================
// SHARED STORAGE
// =============================================================================

#StorageConfig: {
	enabled: bool | *true

	// Storage mode:
	//   file:  GlusterFS (default, simpler, file-level replication)
	//   block: DRBD + ZFS (advanced, block-level, stronger integrity)
	storageMode: *"file" | "block"

	// GlusterFS settings (when storageMode == "file")
	if storageMode == "file" {
		glusterfs: #GlusterFSConfig
	}

	// DRBD + ZFS settings (when storageMode == "block")
	if storageMode == "block" {
		drbd: #DRBDConfig
		zfs:  #ZFSConfig
	}

	// External/managed storage (always available as backup tier)
	external: {
		// S3-compatible storage for backups and cold data
		s3?: {
			endpoint:  string
			bucket:    string
			accessKey: =~"^secret://"
			secretKey: =~"^secret://"
			region:    string | *"auto"
		}
	}
}

#GlusterFSConfig: {
	// Replica count (matches quorum minNodes)
	replicaCount: int | *3

	// Volume type
	volumeType: *"replicate" | "distributed" | "dispersed"

	// Brick path on each node
	brickPath: string | *"/data/glusterfs"

	// Volumes to create
	volumes: [...#GlusterVolume] | *[{
		name:  "shared-data"
		mount: "/mnt/shared"
	}]

	// Self-healing settings
	selfHeal: {
		enabled:  bool | *true
		daemon:   bool | *true
		interval: string | *"600"
	}
}

#GlusterVolume: {
	name:  string
	mount: string
}

#DRBDConfig: {
	// Protocol (C = synchronous, strongest consistency)
	protocol: "A" | "B" | *"C"

	// Disk device
	disk: string | *"/dev/sda2"

	// Meta-disk
	metaDisk: string | *"internal"

	// Network settings
	net: {
		// Max buffers
		maxBuffers: int | *8192

		// Max epoch size
		maxEpochSize: int | *8192
	}

	// Fencing for split-brain prevention
	fencing: {
		handler:  "fence-peer" | "stonith" | *"fence-peer"
		resource: string | *"drbd0"
	}
}

#ZFSConfig: {
	// Pool name
	pool: string | *"hapool"

	// VDEV layout
	vdev: "mirror" | "raidz" | "raidz2" | *"mirror"

	// Compression
	compression: "lz4" | "zstd" | "off" | *"lz4"

	// ARC cache size limit (ZFS can be RAM-hungry)
	arcMaxBytes: string | *"2G"

	// Auto-snapshot
	autoSnapshot: {
		enabled:  bool | *true
		frequent: int | *4
		hourly:   int | *24
		daily:    int | *7
		weekly:   int | *4
		monthly:  int | *12
	}
}

// =============================================================================
// SQLITE HA
// =============================================================================

#SQLiteHAConfig: {
	// Strategy for SQLite-based apps (Vaultwarden, Radicale, TinyAuth, etc.)
	strategy: *"litestream" | "litefs" | "none"

	// Litestream: continuous backup to S3/local (simple, reliable)
	if strategy == "litestream" {
		litestream: {
			// Backup target
			target: "local" | "s3" | *"local"

			// Local backup path
			localPath: string | *"/backups/sqlite"

			// Snapshot interval
			snapshotInterval: string | *"1h"

			// Retention
			retention: string | *"24h"

			// Checkpoint interval
			checkpointInterval: string | *"1m"
		}
	}

	// LiteFS: FUSE-based replication (true replication, more complex)
	if strategy == "litefs" {
		litefs: {
			// Primary node selection via lease
			leaseStore: "etcd"

			// FUSE mount path
			mountPath: string | *"/litefs"

			// Candidate nodes for primary
			candidates: bool | *true
		}
	}
}

// =============================================================================
// ORCHESTRATION
// =============================================================================

#OrchestrationConfig: {
	// Orchestration approach:
	//   compose-manual: Docker Compose per node + health scripts (default, zero license risk)
	//   nomad:          HashiCorp Nomad (opt-in, BSL-1.1, needs commercial license)
	mode: *"compose-manual" | "nomad"

	// compose-manual settings
	if mode == "compose-manual" {
		composeManual: {
			// Rolling update strategy
			rollingUpdate: {
				// Max nodes updated simultaneously
				maxParallel: int | *1

				// Health check wait between nodes
				healthWait: string | *"30s"

				// Rollback on failure
				rollbackOnFailure: bool | *true
			}

			// Placement via CUE-generated configs
			placement: {
				// Anti-affinity: don't run same service on multiple nodes
				antiAffinity: bool | *true

				// Prefer local nodes for latency-sensitive services
				preferLocal: bool | *false
			}
		}
	}

	// Nomad settings (opt-in, requires license acknowledgment)
	if mode == "nomad" {
		nomad: {
			// LICENSE WARNING: Nomad is BSL-1.1.
			// "offering the Licensed Work to third parties on an embedded basis
			// which is competitive with HashiCorp's products" is restricted.
			// Contact licensing@hashicorp.com for commercial terms.
			_licenseWarning: "BSL-1.1: Not safe for kombify SaaS without commercial license"

			licenseAcknowledged: bool & true

			// Server count
			serverCount: 3 | 5 | *3

			// Client settings
			client: {
				gcInterval: string | *"1m"
				gcThreshold: string | *"80%"
			}
		}
	}
}

// =============================================================================
// SECURITY HARDENING
// =============================================================================

#HASecurityConfig: {
	// Mandatory mTLS between all cluster nodes
	mtls: {
		enabled: bool | *true

		// Certificate authority (Step-CA in RA mode)
		ca: "step-ca"

		// Certificate lifetime (short-lived, auto-renewed)
		certLifetime: string | *"24h"

		// Auto-renewal threshold
		renewBefore: string | *"6h"
	}

	// etcd security
	etcdSecurity: {
		// Encryption at rest
		encryptionAtRest: bool | *true

		// RBAC
		rbac: bool | *true

		// Client certificate auth
		clientCertAuth: bool | *true
	}

	// Docker daemon hardening
	docker: {
		// Containers survive daemon restarts
		liveRestore: bool | *true

		// Storage driver
		storageDriver: string | *"overlay2"

		// Rootless mode (not all HA components support it)
		rootless: bool | *false

		// Image verification
		contentTrust: bool | *false

		// Log limits
		logMaxSize: string | *"50m"
		logMaxFile: int | *5

		// Ulimits
		nofileLimit: int | *65536
	}

	// Network segmentation rules
	networkSegmentation: {
		enabled: bool | *true

		// Separate management, service, and storage traffic
		zones: {
			management: {
				description: "SSH, etcd, CoreDNS, Patroni API"
				restricted:  bool | *true
			}
			service: {
				description: "HTTP/S through HAProxy only"
				restricted:  bool | *true
			}
			storage: {
				description: "GlusterFS/DRBD replication"
				vlan?:       int
			}
			vrrp: {
				description: "VRRP between LB nodes only"
				restricted:  bool | *true
			}
		}
	}
}

// =============================================================================
// SERVICE DEFINITIONS
// =============================================================================

#HAProxyService: {
	name:        "haproxy"
	displayName: "HAProxy"
	image:       string | *"haproxy:2.9-alpine"
	layer:       "PLATFORM"
	category:    "load-balancer"

	ports: [
		{container: 80, host: 80, protocol: "tcp", name: "http"},
		{container: 443, host: 443, protocol: "tcp", name: "https"},
		{container: 8404, host: 8404, protocol: "tcp", name: "stats"},
	]

	volumes: [
		{name: "haproxy_config", path: "/usr/local/etc/haproxy", type: "bind"},
	]

	healthCheck: {
		test:     ["CMD", "haproxy", "-c", "-f", "/usr/local/etc/haproxy/haproxy.cfg"]
		interval: "10s"
		timeout:  "5s"
		retries:  3
	}

	restart: "unless-stopped"
}

#KeepalivedService: {
	name:        "keepalived"
	displayName: "Keepalived"
	image:       string | *"osixia/keepalived:2.0.20"
	layer:       "PLATFORM"
	category:    "failover"

	networkMode: "host"
	capAdd: ["NET_ADMIN", "NET_BROADCAST"]

	volumes: [
		{name: "keepalived_config", path: "/container/service/keepalived/assets", type: "bind"},
		{name: "keepalived_scripts", path: "/usr/local/bin", type: "bind"},
	]

	restart: "unless-stopped"
}

#EtcdService: {
	name:        "etcd"
	displayName: "etcd"
	image:       string | *"quay.io/coreos/etcd:v3.5"
	layer:       "PLATFORM"
	category:    "consensus"

	ports: [
		{container: 2379, host: 2379, protocol: "tcp", name: "client"},
		{container: 2380, host: 2380, protocol: "tcp", name: "peer"},
	]

	volumes: [
		{name: "etcd_data", path: "/var/lib/etcd", type: "volume"},
	]

	environment: {
		ETCD_DATA_DIR:                    "/var/lib/etcd"
		ETCD_LISTEN_CLIENT_URLS:          "https://0.0.0.0:2379"
		ETCD_LISTEN_PEER_URLS:            "https://0.0.0.0:2380"
		ETCD_AUTO_COMPACTION_MODE:        "periodic"
		ETCD_AUTO_COMPACTION_RETENTION:   "1h"
		ETCD_SNAPSHOT_COUNT:              "10000"
		ETCD_CLIENT_CERT_AUTH:            "true"
	}

	healthCheck: {
		test:     ["CMD", "etcdctl", "endpoint", "health", "--cluster"]
		interval: "15s"
		timeout:  "10s"
		retries:  3
	}

	restart: "unless-stopped"
}

#CoreDNSService: {
	name:        "coredns"
	displayName: "CoreDNS"
	image:       string | *"coredns/coredns:1.11"
	layer:       "PLATFORM"
	category:    "service-discovery"

	ports: [
		{container: 53, host: 53, protocol: "tcp", name: "dns-tcp"},
		{container: 53, host: 53, protocol: "udp", name: "dns-udp"},
		{container: 9153, host: 9153, protocol: "tcp", name: "metrics"},
	]

	volumes: [
		{name: "coredns_config", path: "/etc/coredns", type: "bind"},
	]

	healthCheck: {
		test:     ["CMD", "dig", "@localhost", "-p", "53", "health.check"]
		interval: "10s"
		timeout:  "5s"
		retries:  3
	}

	restart: "unless-stopped"
}

#PatroniService: {
	name:        "patroni"
	displayName: "Patroni (PostgreSQL HA)"
	image:       string | *"zalando/patroni:3.3"
	layer:       "PLATFORM"
	category:    "database-ha"

	ports: [
		{container: 5432, host: 5432, protocol: "tcp", name: "postgresql"},
		{container: 8008, host: 8008, protocol: "tcp", name: "patroni-api"},
	]

	volumes: [
		{name: "postgresql_data", path: "/var/lib/postgresql/data", type: "volume"},
		{name: "patroni_config", path: "/etc/patroni", type: "bind"},
	]

	healthCheck: {
		test:     ["CMD", "pg_isready", "-U", "postgres"]
		interval: "10s"
		timeout:  "5s"
		retries:  3
	}

	restart: "unless-stopped"
}

#ValkeySentinelService: {
	name:        "valkey"
	displayName: "Valkey + Sentinel"
	image:       string | *"valkey/valkey:8-alpine"
	layer:       "PLATFORM"
	category:    "cache-ha"

	ports: [
		{container: 6379, host: 6379, protocol: "tcp", name: "valkey"},
		{container: 26379, host: 26379, protocol: "tcp", name: "sentinel"},
	]

	volumes: [
		{name: "valkey_data", path: "/data", type: "volume"},
	]

	healthCheck: {
		test:     ["CMD", "valkey-cli", "ping"]
		interval: "10s"
		timeout:  "5s"
		retries:  3
	}

	restart: "unless-stopped"
}

#GlusterFSService: {
	name:        "glusterfs"
	displayName: "GlusterFS"
	layer:       "PLATFORM"
	category:    "shared-storage"

	// GlusterFS runs on the host, not in a container
	// CUE generates gluster CLI commands for volume creation
	hostService: true

	healthCheck: {
		test:     ["CMD", "gluster", "volume", "status"]
		interval: "30s"
		timeout:  "10s"
		retries:  3
	}
}

#LitestreamService: {
	name:        "litestream"
	displayName: "Litestream"
	image:       string | *"litestream/litestream:0.3"
	layer:       "PLATFORM"
	category:    "sqlite-backup"

	volumes: [
		{name: "litestream_config", path: "/etc/litestream.yml", type: "bind"},
	]

	restart: "unless-stopped"
}

// =============================================================================
// OUTPUTS
// =============================================================================

// #Outputs defines what this add-on exports to the stack and other add-ons
#Outputs: {
	// VIP address for DNS/client configuration
	vipAddress: string

	// HAProxy stats URL
	haproxyStatsUrl: string | *"http://haproxy.stack.local/stats"

	// PostgreSQL connection (via HAProxy)
	postgresqlUrl: string

	// PostgreSQL read-only connection
	postgresqlReadOnlyUrl: string

	// Valkey connection (via Sentinel)
	valkeyUrl: string

	// Valkey Sentinel connection
	valkeySentinelUrl: string

	// etcd connection
	etcdUrl: string

	// CoreDNS zone
	dnsZone: string

	// GlusterFS mount point
	sharedStoragePath: string

	// Service discovery endpoint (for other add-ons to register)
	discoveryEndpoint: string
}

// =============================================================================
// HA SERVICE DEFINITION EXTENSION
// =============================================================================

// #HAServiceMeta can be embedded into any add-on's service definition
// to declare HA behavior. The HA add-on provides the infrastructure;
// each service decides how to use it.
#HAServiceMeta: {
	ha: {
		// Number of replicas (1 = single instance with fast restart)
		replicas: int | *1

		// Failover mode
		//   active-passive: one primary, standbys take over on failure
		//   active-active:  multiple instances serve traffic simultaneously
		//   none:           single instance, restart on failure
		failoverMode: *"none" | "active-passive" | "active-active"

		// Health check endpoint (for HAProxy/Keepalived to monitor)
		healthCheck?: {
			endpoint: string
			interval: string | *"10s"
			timeout:  string | *"5s"
			retries:  int | *3
		}

		// Data strategy (how this service handles persistent data in HA)
		//   stateless:      no persistent data, replicate freely
		//   shared-fs:      uses GlusterFS for shared file access
		//   replicated-db:  uses Patroni PostgreSQL (or Valkey Sentinel)
		//   external:       data lives outside the cluster (S3, external DB)
		//   sqlite-backup:  uses Litestream for SQLite backup
		dataStrategy: *"stateless" | "shared-fs" | "replicated-db" | "external" | "sqlite-backup"

		// Placement constraints
		placement: {
			// Don't co-locate with another instance of the same service
			antiAffinity: bool | *true

			// Prefer local nodes (latency-sensitive)
			preferLocal: bool | *false

			// Required node type
			nodeType?: "local" | "cloud" | "all"
		}
	}
}
