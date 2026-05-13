// Package ha_kit - Default Values
//
// Smart defaults for High Availability Docker Swarm deployments.
// These provide production-ready settings for 3-node and 5-node clusters.

package ha_kit

// =============================================================================
// CLUSTER SIZE DEFAULTS
// =============================================================================

// #ThreeNodeDefaults - Minimum HA configuration (3 managers, 0 workers)
#ThreeNodeDefaults: {
	swarm: {
		managerCount: 3
		workerCount:  0
	}
	storage: {
		backend: "glusterfs"
		glusterfs: {
			replicaCount: 3
			volumeType:   "replicate"
		}
	}
	failover: {
		mode: "automatic"
		vip: enabled: true
	}
}

// #FiveNodeDefaults - Recommended HA configuration (3 managers, 2 workers)
#FiveNodeDefaults: {
	swarm: {
		managerCount: 3
		workerCount:  2
	}
	storage: {
		backend: "glusterfs"
		glusterfs: {
			replicaCount: 3
			volumeType:   "replicate"
		}
	}
	failover: {
		mode: "automatic"
		vip: enabled: true
	}
}

// #EnterpriseDefaults - Full enterprise stack (5 managers, 2+ workers)
#EnterpriseDefaults: {
	swarm: {
		managerCount: 5
		workerCount:  2
	}
	storage: {
		backend: "ceph"
		ceph: {
			osdCount:          5
			monCount:          3
			enableCephFS:      true
			enableRGW:         true
			replicationFactor: 3
		}
	}
	failover: {
		mode: "automatic"
		vip: enabled: true
		recovery: preempt: false
	}
}

// =============================================================================
// SERVICE DEFAULTS PER VARIANT
// =============================================================================

// #DefaultVariantServices - Full monitoring + Dokploy
#DefaultVariantServices: #HAServiceSet & {
	traefik:    {enabled: true, mode: "global"}
	keepalived: {enabled: true}
	dokploy:    {enabled: true}
	storage:    {enabled: true}
	prometheus: {enabled: true, replicas: 2}
	grafana:    {enabled: true}
	loki:       {enabled: true}
	alertmanager: {enabled: true, replicas: 2}
	dozzle:     {enabled: true, mode: "global"}
	restic:     {enabled: true}
}

// #MinimalVariantServices - Basic HA + Dokploy
#MinimalVariantServices: #HAServiceSet & {
	traefik:    {enabled: true, mode: "global"}
	keepalived: {enabled: true}
	dokploy:    {enabled: true}
	storage:    {enabled: true}
	uptimeKuma: {enabled: true}
	dozzle:     {enabled: true, mode: "global"}
}

// #EnterpriseVariantServices - Full enterprise stack
#EnterpriseVariantServices: #HAServiceSet & {
	traefik:      {enabled: true, mode: "global"}
	keepalived:   {enabled: true}
	dokploy:      {enabled: true}
	storage:      {enabled: true}
	prometheus:   {enabled: true, replicas: 2}
	grafana:      {enabled: true}
	loki:         {enabled: true}
	alertmanager: {enabled: true, replicas: 2}
	thanos:       {enabled: true}
	ceph:         {enabled: true}
	haproxy:      {enabled: true}
	dozzle:       {enabled: true, mode: "global"}
	restic:       {enabled: true}
}

// =============================================================================
// MONITORING DEFAULTS
// =============================================================================

#MonitoringDefaults: {
	prometheus: {
		retention:      "30d"
		scrapeInterval: "15s"
		replicas:       2 // HA: 2 Prometheus instances
		// Remote write for long-term (if Thanos enabled)
		remoteWrite: enabled: false
	}

	alertmanager: {
		replicas:    2 // HA: deduplication across replicas
		groupWait:   "30s"
		groupInterval: "5m"
		repeatInterval: "4h"
	}

	grafana: {
		replicas: 1 // Single instance is fine (stateless with DB)
		plugins: [
			"grafana-piechart-panel",
			"grafana-clock-panel",
		]
		dashboards: {
			nodeExporter:  true
			docker:        true
			dockerSwarm:   true
			traefik:       true
			glusterfs:     true
		}
	}

	loki: {
		retention:         "168h" // 7 days
		ingestionRateLimit: "4MB"
		replicas:           1
	}
}

// =============================================================================
// BACKUP DEFAULTS (HA-aware)
// =============================================================================

#BackupDefaults: {
	schedule: "0 2 * * *" // 2 AM daily

	// Pre-backup hooks (capture cluster state)
	preHooks: [
		"docker node ls --format '{{.Hostname}} {{.Status}}'",
		"docker service ls --format '{{.Name}} {{.Replicas}}'",
	]

	retention: {
		daily:   14
		weekly:  8
		monthly: 12
	}

	// Volumes to back up
	includeVolumes: [
		"dokploy-data",
		"traefik-certs",
		"prometheus-data",
		"grafana-data",
		"loki-data",
	]

	// Exclude patterns
	excludePatterns: [
		"*.tmp",
		"*.log",
		"cache/*",
	]
}

// =============================================================================
// NETWORK DEFAULTS
// =============================================================================

#NetworkDefaults: {
	overlay: {
		subnet:    "10.10.0.0/16"
		encrypted: true
	}

	bridge: {
		subnet: "172.20.0.0/16"
	}

	// Ports required for Docker Swarm
	requiredPorts: [
		{port: 2377, protocol: "tcp", description: "Swarm management"},
		{port: 7946, protocol: "tcp", description: "Node communication"},
		{port: 7946, protocol: "udp", description: "Node communication"},
		{port: 4789, protocol: "udp", description: "Overlay network (VXLAN)"},
		{port: 22, protocol: "tcp", description: "SSH"},
		{port: 80, protocol: "tcp", description: "HTTP"},
		{port: 443, protocol: "tcp", description: "HTTPS"},
	]
}

// =============================================================================
// SWARM UPDATE DEFAULTS
// =============================================================================

#SwarmUpdateDefaults: {
	// Rolling update configuration
	updateConfig: {
		parallelism:   1
		delay:         "10s"
		failureAction: "rollback"
		order:         "start-first"
	}

	// Rollback configuration
	rollbackConfig: {
		parallelism:   1
		delay:         "5s"
		failureAction: "pause"
		order:         "stop-first"
	}
}
