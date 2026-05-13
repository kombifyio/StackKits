// Package ha_kit - Service Definitions (Scaffolding)
// 
// Status: SCAFFOLDING - Services are planned but not yet implemented
//
// Focus: High Availability via Docker Swarm, Automatic Failover, Distributed Storage
// Platform: Docker Swarm (no Kubernetes — see ADR-0002)

package ha_kit

import "github.com/kombifyio/stackkits/base"

// =============================================================================
// DOCKER SWARM HA
// =============================================================================

// #DockerSwarmService - Docker Swarm Cluster (Planned)
#DockerSwarmService: base.#ServiceDefinition & {
	name:        "docker-swarm"
	displayName: "Docker Swarm"
	category:    "orchestration"
	type:        "cluster"
	required:    true
	status:      "planned"
	description: "Docker Swarm cluster for high availability"

	config: {
		managerCount:         3 | 5 | *3
		workerCount:          int | *0
		autolock:             bool | *true
		raftSnapshotInterval: int | *10000
		taskHistoryLimit:     int | *5
	}

	// TODO: Implement Docker Swarm HA
	// - Multi-manager setup (Raft consensus)
	// - Worker node auto-join
	// - Service mesh / overlay networking
	// - Rolling updates & rollback
}

// =============================================================================
// LOAD BALANCING & ROUTING
// =============================================================================

// #TraefikHAService - HA Reverse Proxy (Planned)
#TraefikHAService: base.#ServiceDefinition & {
	name:        "traefik-ha"
	displayName: "Traefik HA"
	category:    "networking"
	type:        "reverse-proxy"
	required:    true
	status:      "planned"
	description: "Traefik with Docker Swarm provider for HA routing"
	needs:       ["docker-swarm"]

	config: {
		replicas:    int | *2
		dashboard:   bool | *true
		acme:        bool | *true
		acmeEmail:   string
		entrypoints: [...string] | *["web", "websecure"]
	}

	// TODO: Implement Traefik HA
	// - Deploy as global or replicated Swarm service
	// - Docker Swarm provider auto-discovers services
	// - Let's Encrypt with distributed challenge solver
}

// #HAProxyService - External Load Balancer (Planned)
#HAProxyService: base.#ServiceDefinition & {
	name:        "haproxy"
	displayName: "HAProxy"
	category:    "networking"
	type:        "load-balancer"
	required:    false
	status:      "planned"
	description: "External load balancer for Swarm manager access"
	needs:       ["docker-swarm"]

	// TODO: Implement HAProxy
	// - Health checks for manager nodes
	// - Automatic failover
	// - TLS termination
}

// =============================================================================
// HA MONITORING
// =============================================================================

// #PrometheusHAService - HA Prometheus (Planned)
#PrometheusHAService: base.#ServiceDefinition & {
	name:        "prometheus-ha"
	displayName: "Prometheus HA"
	category:    "monitoring"
	type:        "metrics"
	required:    true
	status:      "planned"
	description: "High Availability Prometheus setup"
	needs:       ["docker-swarm"]

	config: {
		replicas:  2
		retention: "15d"
	}

	// TODO: Implement Prometheus HA
	// - Multiple replicas via Swarm service
	// - Shared storage for metrics
	// - Alert deduplication
}

// #ThanosService - Long-term Metrics (Planned)
#ThanosService: base.#ServiceDefinition & {
	name:        "thanos"
	displayName: "Thanos"
	category:    "monitoring"
	type:        "metrics-aggregation"
	required:    false
	status:      "planned"
	description: "Global view and long-term storage for Prometheus"
	needs:       ["docker-swarm", "prometheus-ha"]

	config: {
		objectStorage: "s3" | "gcs" | "azure" | "minio" | *"minio"
		retention:     "90d"
	}

	// TODO: Implement Thanos
	// - Query frontend
	// - Store gateway
	// - Compactor
}

// =============================================================================
// DISTRIBUTED STORAGE
// =============================================================================

// #GlusterFSService - Distributed File Storage (Planned)
#GlusterFSService: base.#ServiceDefinition & {
	name:        "glusterfs"
	displayName: "GlusterFS"
	category:    "storage"
	type:        "distributed-storage"
	required:    true
	status:      "planned"
	description: "Distributed file storage for Docker Swarm volumes"
	needs:       ["docker-swarm"]

	config: {
		replicaCount: 3
		volumeType:   "replicate" | "distributed" | "dispersed" | *"replicate"
		brickPath:    string | *"/data/glusterfs"
	}

	// TODO: Implement GlusterFS
	// - Replicated volumes for persistent data
	// - Docker volume plugin integration
	// - Automatic brick healing
}

// #MinIOService - Object Storage (Planned)
#MinIOService: base.#ServiceDefinition & {
	name:        "minio"
	displayName: "MinIO"
	category:    "storage"
	type:        "object-storage"
	required:    false
	status:      "planned"
	description: "S3-compatible object storage for backups"
	needs:       ["docker-swarm"]

	config: {
		distributed: bool | *true
		nodes:       int | *4
		drives:      int | *4
	}

	// TODO: Implement MinIO
	// - Distributed mode across Swarm nodes
	// - Backup target for monitoring data
}

// =============================================================================
// BACKUP & DISASTER RECOVERY
// =============================================================================

// #ResticService - Backup (Planned)
#ResticService: base.#ServiceDefinition & {
	name:        "restic"
	displayName: "Restic"
	category:    "backup"
	type:        "backup"
	required:    true
	status:      "planned"
	description: "Incremental backup for Swarm volumes and configs"
	needs:       ["docker-swarm"]

	config: {
		schedule:       string | *"0 2 * * *" // Daily at 2 AM
		retentionDays:  int | *30
		backupTarget:   "local" | "s3" | "sftp" | *"local"
		encryptBackups: bool | *true
	}

	// TODO: Implement Restic
	// - Volume snapshots via Swarm
	// - Encrypted off-site backups
	// - Automated restore testing
}

// =============================================================================
// SERVICE COLLECTIONS (Planned — Docker Swarm HA Variants)
// =============================================================================

// #DefaultHAServices - Standard HA deployment (Docker Swarm)
#DefaultHAServices: {
	swarm:      #DockerSwarmService
	traefik:    #TraefikHAService
	prometheus: #PrometheusHAService
	thanos:     #ThanosService
	glusterfs:  #GlusterFSService
	restic:     #ResticService
}

// #EnterpriseServices - Full enterprise stack (Docker Swarm)
#EnterpriseServices: {
	swarm:      #DockerSwarmService
	traefik:    #TraefikHAService
	haproxy:    #HAProxyService
	prometheus: #PrometheusHAService
	thanos:     #ThanosService
	glusterfs:  #GlusterFSService
	minio:      #MinIOService
	restic:     #ResticService
}
