// =============================================================================
// ha-kit StackKit Tests
// =============================================================================
// Validates Docker Swarm HA schema constraints and decision points.
//
// Decision Points Tested:
//   1. Manager count must be ODD (3, 5, or 7)
//   2. Minimum 3 nodes for HA quorum
//   3. Manager nodes: >=4 CPU, >=8 GB RAM
//   4. Worker nodes: >=2 CPU, >=4 GB RAM
//   5. Enterprise variant requires >=5 nodes
//   6. Storage backend conditional fields
//   7. Quorum size computation
//   8. Failover VIP configuration
//   9. Swarm overlay network encryption
//   10. Service deploy mode constraints
// =============================================================================

package ha_kit

import "list"

// =============================================================================
// POSITIVE TESTS: Valid 3-Node HA Configuration (default variant)
// =============================================================================

_testValid3NodeHA: #HAKitStack & {
	meta: name: "ha-3node"

	deploymentMode: "advanced"
	variant:        "default"

	swarm: {
		managerCount: 3
		workerCount:  0
		network: encrypted: true
	}

	nodes: [
		{
			name: "manager-1"
			host: "10.0.0.1"
			role: "manager"
			compute: {
				cpuCores:  4
				ramGB:     8
				storageGB: 100
			}
		},
		{
			name: "manager-2"
			host: "10.0.0.2"
			role: "manager"
			compute: {
				cpuCores:  4
				ramGB:     8
				storageGB: 100
			}
		},
		{
			name: "manager-3"
			host: "10.0.0.3"
			role: "manager"
			compute: {
				cpuCores:  4
				ramGB:     8
				storageGB: 100
			}
		},
	]

	network: {
		domain:    "ha.example.com"
		acmeEmail: "ops@example.com"
		overlay: encrypted: true
	}

	storage: {
		backend: "glusterfs"
		glusterfs: {
			replicaCount: 3
			volumeType:   "replicate"
		}
	}

	services: {
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

	failover: {
		mode: "automatic"
		vip: {
			enabled:   true
			address:   "10.0.0.100"
			interface: "eth0"
			priority:  100
		}
		healthCheck: {
			interval: "5s"
			timeout:  "3s"
			retries:  3
		}
	}

	backup: {
		enabled:    true
		provider:   "restic"
		schedule:   "0 2 * * *"
		repository: "s3:s3.amazonaws.com/my-backups"
		retention: {
			daily:   14
			weekly:  8
			monthly: 12
		}
	}
}

// Verify quorum computation for 3 managers
_testQuorum3: {
	_testValid3NodeHA.swarm._quorumSize
	2
}
_testMaxFailures3: {
	_testValid3NodeHA.swarm._maxFailures
	1
}

// =============================================================================
// POSITIVE TEST: Valid 5-Node HA (3 managers + 2 workers)
// =============================================================================

_testValid5NodeHA: #HAKitStack & {
	meta: name: "ha-5node"

	deploymentMode: "advanced"
	variant:        "default"

	swarm: {
		managerCount: 3
		workerCount:  2
	}

	nodes: [
		{name: "mgr-1", host: "10.0.0.1", role: "manager", compute: {cpuCores: 4, ramGB: 8, storageGB: 100}},
		{name: "mgr-2", host: "10.0.0.2", role: "manager", compute: {cpuCores: 4, ramGB: 8, storageGB: 100}},
		{name: "mgr-3", host: "10.0.0.3", role: "manager", compute: {cpuCores: 4, ramGB: 8, storageGB: 100}},
		{name: "wrk-1", host: "10.0.0.4", role: "worker", compute: {cpuCores: 2, ramGB: 4, storageGB: 50}},
		{name: "wrk-2", host: "10.0.0.5", role: "worker", compute: {cpuCores: 2, ramGB: 4, storageGB: 50}},
	]

	network: {
		domain:    "prod.example.com"
		acmeEmail: "infra@example.com"
	}

	storage: {
		backend: "glusterfs"
		glusterfs: replicaCount: 3
	}

	services: {
		traefik:    {enabled: true, mode: "global"}
		keepalived: {enabled: true}
		dokploy:    {enabled: true}
		storage:    {enabled: true}
		prometheus: {enabled: true, replicas: 2}
		grafana:    {enabled: true}
		dozzle:     {enabled: true, mode: "global"}
	}

	failover: {
		mode: "automatic"
		vip: {
			address:   "10.0.0.100"
			interface: "eth0"
		}
	}

	backup: {
		enabled:    true
		provider:   "restic"
		schedule:   "0 3 * * *"
		repository: "/mnt/backup/ha-cluster"
	}
}

// =============================================================================
// POSITIVE TEST: 5-Manager Cluster (quorum of 3, tolerates 2 failures)
// =============================================================================

_testValid5ManagerCluster: #HAKitStack & {
	meta: name: "ha-5mgr"

	variant: "default"

	swarm: managerCount: 5

	nodes: [
		{name: "m-1", host: "10.0.0.1", role: "manager", compute: {cpuCores: 4, ramGB: 8, storageGB: 100}},
		{name: "m-2", host: "10.0.0.2", role: "manager", compute: {cpuCores: 4, ramGB: 8, storageGB: 100}},
		{name: "m-3", host: "10.0.0.3", role: "manager", compute: {cpuCores: 4, ramGB: 8, storageGB: 100}},
		{name: "m-4", host: "10.0.0.4", role: "manager", compute: {cpuCores: 4, ramGB: 8, storageGB: 100}},
		{name: "m-5", host: "10.0.0.5", role: "manager", compute: {cpuCores: 4, ramGB: 8, storageGB: 100}},
	]

	network: {
		domain:    "five.example.com"
		acmeEmail: "admin@example.com"
	}

	storage: backend: "glusterfs"

	services: {
		traefik:    {enabled: true, mode: "global"}
		keepalived: {enabled: true}
		dokploy:    {enabled: true}
		storage:    {enabled: true}
		dozzle:     {enabled: true, mode: "global"}
	}

	failover: {
		mode: "automatic"
		vip: {address: "10.0.0.200", interface: "eth0"}
	}

	backup: {
		enabled:    true
		provider:   "restic"
		repository: "/backup/five"
	}
}

// Verify quorum computation for 5 managers
_testQuorum5: {
	_testValid5ManagerCluster.swarm._quorumSize
	3
}
_testMaxFailures5: {
	_testValid5ManagerCluster.swarm._maxFailures
	2
}

// =============================================================================
// POSITIVE TEST: Minimal Variant (Uptime Kuma only)
// =============================================================================

_testMinimalVariant: #HAKitStack & {
	meta: name: "ha-minimal"

	variant: "minimal"

	swarm: managerCount: 3

	nodes: [
		{name: "n-1", host: "10.0.0.1", role: "manager", compute: {cpuCores: 4, ramGB: 8, storageGB: 100}},
		{name: "n-2", host: "10.0.0.2", role: "manager", compute: {cpuCores: 4, ramGB: 8, storageGB: 100}},
		{name: "n-3", host: "10.0.0.3", role: "manager", compute: {cpuCores: 4, ramGB: 8, storageGB: 100}},
	]

	network: {
		domain:    "minimal.example.com"
		acmeEmail: "admin@example.com"
	}

	storage: backend: "glusterfs"

	services: {
		traefik:    {enabled: true, mode: "global"}
		keepalived: {enabled: true}
		dokploy:    {enabled: true}
		storage:    {enabled: true}
		uptimeKuma: {enabled: true}
		dozzle:     {enabled: true, mode: "global"}
	}

	failover: {
		mode: "automatic"
		vip: {address: "10.0.0.50", interface: "eth0"}
	}

	backup: {
		enabled:    true
		provider:   "restic"
		repository: "/backup/minimal"
	}
}

// =============================================================================
// POSITIVE TEST: Enterprise Variant (5+ nodes, Ceph, Thanos)
// =============================================================================

_testEnterpriseVariant: #HAKitStack & {
	meta: name: "ha-enterprise"

	variant: "enterprise"

	swarm: {
		managerCount: 5
		workerCount:  2
	}

	nodes: [
		{name: "e-1", host: "10.0.0.1", role: "manager", compute: {cpuCores: 8, ramGB: 16, storageGB: 200}},
		{name: "e-2", host: "10.0.0.2", role: "manager", compute: {cpuCores: 8, ramGB: 16, storageGB: 200}},
		{name: "e-3", host: "10.0.0.3", role: "manager", compute: {cpuCores: 8, ramGB: 16, storageGB: 200}},
		{name: "e-4", host: "10.0.0.4", role: "manager", compute: {cpuCores: 8, ramGB: 16, storageGB: 200}},
		{name: "e-5", host: "10.0.0.5", role: "manager", compute: {cpuCores: 8, ramGB: 16, storageGB: 200}},
		{name: "w-1", host: "10.0.0.6", role: "worker", compute: {cpuCores: 4, ramGB: 8, storageGB: 200}},
		{name: "w-2", host: "10.0.0.7", role: "worker", compute: {cpuCores: 4, ramGB: 8, storageGB: 200}},
	]

	network: {
		domain:    "enterprise.example.com"
		acmeEmail: "enterprise@example.com"
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

	services: {
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

	failover: {
		mode: "automatic"
		vip: {
			address:   "10.0.0.200"
			interface: "eth0"
			priority:  200
		}
		recovery: preempt: false
	}

	backup: {
		enabled:    true
		provider:   "restic"
		schedule:   "0 1 * * *"
		repository: "s3:s3.eu-central-1.amazonaws.com/enterprise-backups"
		retention: {
			daily:   30
			weekly:  12
			monthly: 24
		}
	}
}

// =============================================================================
// POSITIVE TEST: NFS Storage Backend
// =============================================================================

_testNFSBackend: #HAKitStack & {
	meta: name: "ha-nfs"

	variant: "default"
	swarm: managerCount: 3

	nodes: [
		{name: "nfs-1", host: "10.0.0.1", role: "manager", compute: {cpuCores: 4, ramGB: 8, storageGB: 100}},
		{name: "nfs-2", host: "10.0.0.2", role: "manager", compute: {cpuCores: 4, ramGB: 8, storageGB: 100}},
		{name: "nfs-3", host: "10.0.0.3", role: "manager", compute: {cpuCores: 4, ramGB: 8, storageGB: 100}},
	]

	network: {
		domain:    "nfs.example.com"
		acmeEmail: "admin@example.com"
	}

	storage: {
		backend: "nfs"
		nfs: {
			server:     "10.0.0.50"
			exportPath: "/exports/swarm"
			version:    "4.1"
		}
	}

	services: {
		traefik:    {enabled: true, mode: "global"}
		keepalived: {enabled: true}
		dokploy:    {enabled: true}
		storage:    {enabled: true}
		dozzle:     {enabled: true, mode: "global"}
	}

	failover: {
		mode: "manual"
		vip: {address: "10.0.0.100", interface: "eth0"}
	}

	backup: {
		enabled:    true
		provider:   "restic"
		repository: "/mnt/nfs-backup"
	}
}

// =============================================================================
// POSITIVE TEST: Simple Deployment Mode (with HA warning)
// =============================================================================

_testSimpleModeHA: #HAKitStack & {
	meta: name: "ha-simple"

	deploymentMode: "simple" // Triggers warning
	variant:        "default"

	swarm: managerCount: 3

	nodes: [
		{name: "s-1", host: "10.0.0.1", role: "manager", compute: {cpuCores: 4, ramGB: 8, storageGB: 100}},
		{name: "s-2", host: "10.0.0.2", role: "manager", compute: {cpuCores: 4, ramGB: 8, storageGB: 100}},
		{name: "s-3", host: "10.0.0.3", role: "manager", compute: {cpuCores: 4, ramGB: 8, storageGB: 100}},
	]

	network: {
		domain:    "simple-ha.example.com"
		acmeEmail: "admin@example.com"
	}

	storage: backend: "glusterfs"

	services: {
		traefik:    {enabled: true, mode: "global"}
		keepalived: {enabled: true}
		dokploy:    {enabled: true}
		storage:    {enabled: true}
		dozzle:     {enabled: true, mode: "global"}
	}

	failover: {
		mode: "automatic"
		vip: {address: "10.0.0.100", interface: "eth0"}
	}

	backup: {
		enabled:    true
		provider:   "restic"
		repository: "/backup/simple"
	}
}
// Verify the warning is generated
_testSimpleModeWarning: {
	_testSimpleModeHA._haWarning
	"WARNING: simple mode lacks drift detection and rolling updates - not recommended for HA deployments"
}

// =============================================================================
// POSITIVE TEST: 7-Manager Cluster (max quorum)
// =============================================================================

_testValid7ManagerCluster: #HAKitStack & {
	meta: name: "ha-7mgr"

	variant: "default"
	swarm: managerCount: 7

	nodes: [
		{name: "m-1", host: "10.0.0.1", role: "manager", compute: {cpuCores: 4, ramGB: 8, storageGB: 100}},
		{name: "m-2", host: "10.0.0.2", role: "manager", compute: {cpuCores: 4, ramGB: 8, storageGB: 100}},
		{name: "m-3", host: "10.0.0.3", role: "manager", compute: {cpuCores: 4, ramGB: 8, storageGB: 100}},
		{name: "m-4", host: "10.0.0.4", role: "manager", compute: {cpuCores: 4, ramGB: 8, storageGB: 100}},
		{name: "m-5", host: "10.0.0.5", role: "manager", compute: {cpuCores: 4, ramGB: 8, storageGB: 100}},
		{name: "m-6", host: "10.0.0.6", role: "manager", compute: {cpuCores: 4, ramGB: 8, storageGB: 100}},
		{name: "m-7", host: "10.0.0.7", role: "manager", compute: {cpuCores: 4, ramGB: 8, storageGB: 100}},
	]

	network: {
		domain:    "seven.example.com"
		acmeEmail: "admin@example.com"
	}

	storage: backend: "glusterfs"

	services: {
		traefik:    {enabled: true, mode: "global"}
		keepalived: {enabled: true}
		dokploy:    {enabled: true}
		storage:    {enabled: true}
		dozzle:     {enabled: true, mode: "global"}
	}

	failover: {
		mode: "automatic"
		vip: {address: "10.0.0.200", interface: "eth0"}
	}

	backup: {
		enabled:    true
		provider:   "restic"
		repository: "/backup/seven"
	}
}
// Verify quorum computation for 7 managers
_testQuorum7: {
	_testValid7ManagerCluster.swarm._quorumSize
	4
}
_testMaxFailures7: {
	_testValid7ManagerCluster.swarm._maxFailures
	3
}

// =============================================================================
// POSITIVE TEST: VPN-connected distributed nodes
// =============================================================================

_testVPNCluster: #HAKitStack & {
	meta: name: "ha-vpn"

	variant: "default"
	swarm: managerCount: 3

	nodes: [
		{name: "dc-1", host: "10.0.0.1", role: "manager", compute: {cpuCores: 4, ramGB: 8, storageGB: 100}, zone: "eu-west"},
		{name: "dc-2", host: "10.0.0.2", role: "manager", compute: {cpuCores: 4, ramGB: 8, storageGB: 100}, zone: "eu-east"},
		{name: "dc-3", host: "10.0.0.3", role: "manager", compute: {cpuCores: 4, ramGB: 8, storageGB: 100}, zone: "us-east"},
	]

	network: {
		domain:    "geo.example.com"
		acmeEmail: "admin@example.com"
		vpn: {
			enabled:  true
			provider: "headscale"
		}
	}

	storage: backend: "glusterfs"

	services: {
		traefik:    {enabled: true, mode: "global"}
		keepalived: {enabled: true}
		dokploy:    {enabled: true}
		storage:    {enabled: true}
		dozzle:     {enabled: true, mode: "global"}
	}

	failover: {
		mode: "automatic"
		vip: {address: "10.0.0.100", interface: "tailscale0"}
	}

	backup: {
		enabled:    true
		provider:   "restic"
		repository: "s3:s3.eu-west-1.amazonaws.com/geo-backup"
	}
}

// =============================================================================
// POSITIVE TEST: Shared Volumes Configuration
// =============================================================================

_testSharedVolumes: #HAStorageConfig & {
	backend: "glusterfs"
	glusterfs: replicaCount: 3
	sharedVolumes: [
		{name: "app-data", path: "/data/apps", size: "50Gi", accessMode: "ReadWriteMany", backup: true},
		{name: "config", path: "/data/config", size: "5Gi", accessMode: "ReadWriteMany", backup: true},
		{name: "cache", path: "/data/cache", size: "20Gi", accessMode: "ReadWriteMany", backup: false},
	]
}

// =============================================================================
// SERVICE DEFINITION TESTS
// =============================================================================

// Test: Traefik HA deploys in global mode
_testTraefikDeploy: {
	t: #TraefikHAService
	t.deploy.mode
	"global"
}

// Test: Prometheus HA has 2 replicas
_testPrometheusDeploy: {
	p: #PrometheusHAService
	p.deploy.replicas
	2
}

// Test: AlertManager HA has 2 replicas
_testAlertManagerDeploy: {
	a: #AlertManagerHAService
	a.deploy.replicas
	2
}

// Test: Dozzle deploys globally
_testDozzleDeploy: {
	d: #DozzleHAService
	d.deploy.mode
	"global"
}

// Test: Node Exporter deploys globally
_testNodeExporterDeploy: {
	n: #NodeExporterService
	n.deploy.mode
	"global"
}

// Test: Dokploy is single-instance on managers
_testDokployDeploy: {
	d: #DokployHAService
	d.deploy.replicas
	1
}

// Test: Ceph monitors have 3 replicas
_testCephMonDeploy: {
	c: #CephHAService
	c.deploy.replicas
	3
}

// =============================================================================
// SWARM CONFIG TESTS
// =============================================================================

// Test: Default overlay encryption is true
_testOverlayEncryption: {
	s: #SwarmHAConfig & {managerCount: 3}
	s.network.encrypted
	true
}

// Test: Default restart policy
_testRestartPolicy: {
	s: #SwarmHAConfig & {managerCount: 3}
	s.restartPolicy.condition
	"on-failure"
}

// Test: Default update order is start-first (zero-downtime)
_testUpdateOrder: {
	s: #SwarmHAConfig & {managerCount: 3}
	s.updateConfig.order
	"start-first"
}

// Test: Default failure action is rollback
_testFailureAction: {
	s: #SwarmHAConfig & {managerCount: 3}
	s.updateConfig.failureAction
	"rollback"
}

// =============================================================================
// NEGATIVE TESTS (Commented - must fail when evaluated)
// =============================================================================
// Run individually with: cue vet -d _invalidXxx ./ha-kit/...
//
// --- NEGATIVE TEST: Even manager count (quorum requires odd) ---
// Expected error: managerCount must be (3 | 5 | 7)
//
// _invalidEvenManagers: #HAKitStack & {
//     meta: name: "bad-even"
//     variant: "default"
//     swarm: managerCount: 4  // ← REJECTED: even count violates quorum
//     nodes: [
//         {name: "a", host: "1", role: "manager", compute: {cpuCores: 4, ramGB: 8, storageGB: 100}},
//         {name: "b", host: "2", role: "manager", compute: {cpuCores: 4, ramGB: 8, storageGB: 100}},
//         {name: "c", host: "3", role: "manager", compute: {cpuCores: 4, ramGB: 8, storageGB: 100}},
//         {name: "d", host: "4", role: "manager", compute: {cpuCores: 4, ramGB: 8, storageGB: 100}},
//     ]
//     network: { domain: "bad.example.com"; acmeEmail: "a@b.com" }
//     storage: backend: "glusterfs"
//     services: { traefik: {enabled: true}; keepalived: {enabled: true}; dokploy: {enabled: true}; storage: {enabled: true}; dozzle: {enabled: true} }
//     failover: { vip: { address: "1.2.3.4"; interface: "eth0" } }
//     backup: { enabled: true; provider: "restic"; repository: "/x" }
// }

// --- NEGATIVE TEST: Less than 3 nodes ---
// Expected error: list.MinItems(3) violated
//
// _invalidTooFewNodes: #HAKitStack & {
//     meta: name: "bad-2node"
//     variant: "default"
//     swarm: managerCount: 3
//     nodes: [
//         {name: "a", host: "1", role: "manager", compute: {cpuCores: 4, ramGB: 8, storageGB: 100}},
//         {name: "b", host: "2", role: "manager", compute: {cpuCores: 4, ramGB: 8, storageGB: 100}},
//     ] // ← REJECTED: only 2 nodes, need minimum 3
//     network: { domain: "bad.example.com"; acmeEmail: "a@b.com" }
//     storage: backend: "glusterfs"
//     services: { traefik: {enabled: true}; keepalived: {enabled: true}; dokploy: {enabled: true}; storage: {enabled: true}; dozzle: {enabled: true} }
//     failover: { vip: { address: "1.2.3.4"; interface: "eth0" } }
//     backup: { enabled: true; provider: "restic"; repository: "/x" }
// }

// --- NEGATIVE TEST: Manager node with insufficient CPU ---
// Expected error: cpuCores >= 4 required for manager role
//
// _invalidManagerCPU: #HANode & {
//     name: "weak-manager"
//     host: "10.0.0.1"
//     role: "manager"
//     compute: {
//         cpuCores: 2   // ← REJECTED: managers need >=4 CPU
//         ramGB: 8
//         storageGB: 100
//     }
// }

// --- NEGATIVE TEST: Manager node with insufficient RAM ---
// Expected error: ramGB >= 8 required for manager role
//
// _invalidManagerRAM: #HANode & {
//     name: "low-ram-manager"
//     host: "10.0.0.1"
//     role: "manager"
//     compute: {
//         cpuCores: 4
//         ramGB: 4       // ← REJECTED: managers need >=8 GB RAM
//         storageGB: 100
//     }
// }

// --- NEGATIVE TEST: Enterprise variant with only 3 nodes ---
// Expected error: list.MinItems(5) when variant == "enterprise"
//
// _invalidEnterprise3Nodes: #HAKitStack & {
//     meta: name: "bad-enterprise"
//     variant: "enterprise"
//     swarm: managerCount: 3
//     nodes: [
//         {name: "a", host: "1", role: "manager", compute: {cpuCores: 4, ramGB: 8, storageGB: 100}},
//         {name: "b", host: "2", role: "manager", compute: {cpuCores: 4, ramGB: 8, storageGB: 100}},
//         {name: "c", host: "3", role: "manager", compute: {cpuCores: 4, ramGB: 8, storageGB: 100}},
//     ] // ← REJECTED: enterprise needs >=5 nodes
//     network: { domain: "bad.example.com"; acmeEmail: "a@b.com" }
//     storage: { backend: "ceph"; ceph: { osdCount: 3; monCount: 3 } }
//     services: { traefik: {enabled: true}; keepalived: {enabled: true}; dokploy: {enabled: true}; storage: {enabled: true}; dozzle: {enabled: true} }
//     failover: { vip: { address: "1.2.3.4"; interface: "eth0" } }
//     backup: { enabled: true; provider: "restic"; repository: "/x" }
// }

// --- NEGATIVE TEST: Enterprise with local storage (must be ceph or glusterfs) ---
// Expected error: storage.backend must be "ceph" | "glusterfs" for enterprise
//
// _invalidEnterpriseLocalStorage: #HAKitStack & {
//     meta: name: "bad-storage"
//     variant: "enterprise"
//     swarm: managerCount: 5
//     nodes: [
//         {name: "a", host: "1", role: "manager", compute: {cpuCores: 8, ramGB: 16, storageGB: 200}},
//         {name: "b", host: "2", role: "manager", compute: {cpuCores: 8, ramGB: 16, storageGB: 200}},
//         {name: "c", host: "3", role: "manager", compute: {cpuCores: 8, ramGB: 16, storageGB: 200}},
//         {name: "d", host: "4", role: "manager", compute: {cpuCores: 8, ramGB: 16, storageGB: 200}},
//         {name: "e", host: "5", role: "manager", compute: {cpuCores: 8, ramGB: 16, storageGB: 200}},
//     ]
//     network: { domain: "bad.example.com"; acmeEmail: "a@b.com" }
//     storage: backend: "local"  // ← REJECTED: enterprise requires ceph or glusterfs
//     services: { traefik: {enabled: true}; keepalived: {enabled: true}; dokploy: {enabled: true}; storage: {enabled: true}; dozzle: {enabled: true} }
//     failover: { vip: { address: "1.2.3.4"; interface: "eth0" } }
//     backup: { enabled: true; provider: "restic"; repository: "/x" }
// }

// --- NEGATIVE TEST: Local domain (HA requires public domain) ---
// Expected error: domain must not match .(local|lan|home|internal|test)$
//
// _invalidLocalDomain: #HAKitStack & {
//     meta: name: "bad-domain"
//     variant: "default"
//     swarm: managerCount: 3
//     nodes: [
//         {name: "a", host: "1", role: "manager", compute: {cpuCores: 4, ramGB: 8, storageGB: 100}},
//         {name: "b", host: "2", role: "manager", compute: {cpuCores: 4, ramGB: 8, storageGB: 100}},
//         {name: "c", host: "3", role: "manager", compute: {cpuCores: 4, ramGB: 8, storageGB: 100}},
//     ]
//     network: {
//         domain: "ha.local"     // ← REJECTED: .local not allowed for HA
//         acmeEmail: "a@b.com"
//     }
//     storage: backend: "glusterfs"
//     services: { traefik: {enabled: true}; keepalived: {enabled: true}; dokploy: {enabled: true}; storage: {enabled: true}; dozzle: {enabled: true} }
//     failover: { vip: { address: "1.2.3.4"; interface: "eth0" } }
//     backup: { enabled: true; provider: "restic"; repository: "/x" }
// }

// --- NEGATIVE TEST: Invalid ACME email format ---
// Expected error: acmeEmail regex validation fails
//
// _invalidAcmeEmail: #HANetworkConfig & {
//     domain: "good.example.com"
//     acmeEmail: "not-an-email"   // ← REJECTED: must match email regex
// }

// --- NEGATIVE TEST: Manager count of 1 (not in allowed set) ---
// Expected error: managerCount must be (3 | 5 | 7)
//
// _invalidSingleManager: #SwarmHAConfig & {
//     managerCount: 1  // ← REJECTED: 1 is not 3|5|7
// }

// --- NEGATIVE TEST: Invalid node name (uppercase) ---
// Expected error: name must match ^[a-z][a-z0-9-]*$
//
// _invalidNodeName: #HANode & {
//     name: "MyNode"   // ← REJECTED: uppercase not allowed
//     host: "10.0.0.1"
//     role: "manager"
//     compute: { cpuCores: 4; ramGB: 8; storageGB: 100 }
// }
