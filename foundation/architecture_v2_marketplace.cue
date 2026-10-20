package foundation

_architectureV2MarketplaceStorage: [
	{componentRef: "postgres", volumeRef: "postgres", target: "/var/lib/postgresql/data", backupDataClass: "database"},
	{componentRef: "redis", volumeRef: "redis", target: "/data", backupDataClass: "platform-state"},
	{componentRef: "coolify", volumeRef: "ssh", target: "/var/www/html/storage/app/ssh", backupDataClass: "secret-material"},
	for name in ["applications", "databases", "services", "backups", "images"] {componentRef: "coolify", volumeRef: name, target: "/var/www/html/storage/app/\(name)", backupDataClass: "platform-state"},
]

_architectureV2MarketplaceInfrastructure: #WorkloadInfrastructureV1 & {
	dataBinding: {moduleRef: "stackkits-workload-data-binding", bindingRef: "marketplace", classes: ["personal", "secret"], locality: "primary-site"}
	storageAllocation: {moduleRef: "stackkits-storage-allocation", allocations: [for v in _architectureV2MarketplaceStorage {
		{componentRef: v.componentRef, volumeRef: v.volumeRef, target: v.target, class: "persistent", backup: true, dataClasses: ["personal", "secret"], dataBindingRef: "marketplace"}
	}]}
	backupSource: {moduleRef: "stackkits-backup-source", allocations: [for v in _architectureV2MarketplaceStorage {
		{componentRef: v.componentRef, volumeRef: v.volumeRef, dataClasses: ["personal", "secret"], backupDataClass: v.backupDataClass}
	}]}
	snapshot: moduleRef: "stackkits-snapshot"
	restore: moduleRef: "stackkits-restore"
	recovery: moduleRef: "stackkits-recovery"
}

// Coolify is an optional application. Its own control database is part of
// the node installation, never a managed development-data fixture.
_architectureV2MarketplaceComputeProfile: #ModuleComputeProfileV2 & {
	description: "Pinned Marketplace control application and its PostgreSQL/Redis dependencies. This declared policy envelope is not a measured capacity claim."
	maturity: "beta", executable: true, realization: "apply-ready"
	platformManagement: "standalone"
	components: ["coolify", "postgres", "redis"]
	hostFloor: {minCpuCores: 2, minRamGB: 4, minStorageGB: 20}
	reservation: {ramGB: 2.25, cpuCores: 0.5}
	recommended: {ramGB: 4, cpuCores: 2}
	provenance: {hostFloor: _architectureV2PolicyFloorProvenance, reservation: _architectureV2PolicyReservationProvenance, recommended: _architectureV2PolicyRecommendedProvenance}
}

_architectureV2MarketplaceProviders: [{
	metadata: {id: "stackkits-marketplace", version: "1.0.0"}
	provides: [], workloadRefs: ["marketplace"]
	requires: [{id: "service-catalog"}, {id: "storage-data-policy"}, {id: "backup-core"}]
	supportedSiteKinds: ["home", "cloud"]
	realization: {kind: "modules", moduleRefs: {required: [], optional: ["stackkits-marketplace-coolify-runtime"]}}
	evidence: ["marketplace-native-installation"]
}]

_architectureV2MarketplaceWorkloads: [#WorkloadContractV2 & {
	metadata: {id: "marketplace", version: "1.0.0", description: "Optional Coolify app store operated on the owner's node with the existing Kit router and PocketID. Full data, lifecycle and Cloud admission remain separate evidence."}
	kind: "application", useCaseRef: "marketplace"
	functionalCapabilities: ["self-hosted-app-store"]
	supportedSiteKinds: ["home", "cloud"], dataClasses: ["personal", "secret"]
	defaultAlternative: "coolify"
	computeTiers: {
		low: {included: false, reason: "The initial Marketplace installation requires the standard node envelope."}
		standard: {included: true, alternativeID: "coolify"}
		high: {included: true, alternativeID: "coolify"}
	}
	alternatives: [{
		id: "coolify", providerRef: "stackkits-marketplace", moduleRef: "stackkits-marketplace-coolify-runtime"
		route: {serviceRef: "marketplace", healthRef: "marketplace-bootstrap-ready"}
		runtime: {allowedKinds: ["host"], allowedDeliveries: ["stackkit"], allowedAdapterRefs: []}
		setup: {mode: "automatic", owner: "module", actionRefs: ["marketplace-owner-bootstrap"]}
		inputs: {settings: {allowedRefs: [], requiredRefs: []}, secretInputs: {allowedRefs: [], requiredRefs: []}}
		infrastructure: _architectureV2MarketplaceInfrastructure
	}]
}]

_architectureV2MarketplaceLifecycles: [#ApplicationLifecycleContractV1 & {
	metadata: {id: "marketplace", version: "1.0.0", description: "Node-owned Marketplace installation and operations. Application features remain upstream-owned."}
	workloadRef: "marketplace", useCaseRef: "marketplace", packageRef: "marketplace"
	lifecycle: #StandardUseCaseLifecycle & {stages: setup: {}}
}]

_architectureV2MarketplaceModules: [{
	metadata: {id: "stackkits-marketplace-coolify-runtime", version: "1.0.0", description: "Node-custodied Marketplace installation with a pinned Coolify control application, private API, closed root bootstrap and PocketID. It does not acquire a workload execution adapter."}
	role: "workload", providerRef: "stackkits-marketplace"
	provides: [], requires: ["stackkits-core-host-bootstrap"]
	supportedSiteKinds: ["home", "cloud"]
	nodeSelection: {authority: "control-authority-site", controlPlaneMembers: "only", requiredRoles: ["worker"]}
	computeProfiles: {standard: _architectureV2MarketplaceComputeProfile, high: _architectureV2MarketplaceComputeProfile}
	defaultComputeProfile: "standard"
	serviceControls: [{key: "marketplace", serviceRef: "marketplace", adapter: "compose", runtimeRef: "marketplace", componentRefs: ["coolify", "postgres", "redis"], allowedActions: ["logs", "restart", "start", "stop"], critical: false}]
	runtime: {
		execution: "executable", kind: "host", delivery: "stackkit", entryComponentRef: "coolify"
		components: [{
			id: "coolify", role: "application", lifecycle: "daemon", networkRefs: ["marketplace", "kit-core"]
			image: {ref: "coollabsio/coolify:4.4.6", digest: "sha256:7c54f42d64c0d20fed4a8f4561df9018a561fecba9931ed3f64a4f3e0d34a3be"}
			dependsOn: ["postgres", "redis"], health: {kind: "http", path: "/api/health", port: 8080}
			resources: {memoryLimit: "1536m"}
			volumes: [for v in _architectureV2MarketplaceStorage if v.componentRef == "coolify" {id: v.volumeRef, target: v.target, class: "persistent", backup: true}]
		}, {
			id: "postgres", role: "database", lifecycle: "daemon", networkRefs: ["marketplace"]
			image: {ref: "postgres:15-alpine", digest: "sha256:f7d23353e1b15400d22ebe31189f4d314b87a4c129cc400c8c2d8d4ca127bf81"}
			health: {kind: "image"}, resources: {memoryLimit: "512m"}
			volumes: [for v in _architectureV2MarketplaceStorage if v.componentRef == "postgres" {id: v.volumeRef, target: v.target, class: "persistent", backup: true}]
		}, {
			id: "redis", role: "cache", lifecycle: "daemon", networkRefs: ["marketplace"]
			image: {ref: "redis:7-alpine", digest: "sha256:858f009f9709ce576febc734aa78b8f6d624b82571f9ddb6bda4377c833b3499"}
			health: {kind: "image"}, resources: {memoryLimit: "256m"}
			volumes: [for v in _architectureV2MarketplaceStorage if v.componentRef == "redis" {id: v.volumeRef, target: v.target, class: "persistent", backup: true}]
		}]
	}
	// Installation is a node-owned host operation on either target. The
	// generated recipe retains the same native bootstrap owner; it does not
	// turn curated Coolify workloads into Docker-provider resources.
	renderUnits: [{
		id: "marketplace", kind: "native-config", rendererRef: "stackkit", compatibleTargets: ["compose", "opentofu"]
		templateRef: "builtin://workloads/marketplace/installation/v1.json", version: "1.0.0", contractHash: "sha256:da9344f190d12d566365c28618922dd94147c82cfff141f0f68c41da7aa6d396"
		publicInputRefs: ["delivery-route"], secretInputRefs: [], planInputRefs: ["kit"]
		inputBindings: [{targetRef: "delivery-route", sourceRef: "network.moduleRoute", valueType: "authority-bound-module-route-v1", cardinality: "single", required: false, defaultValue: null}]
		outputs: ["workloads/marketplace/installation.json"]
		placement: {scope: "node-local", cardinality: "one-per-node"}
		serviceEndpoints: [{serviceRef: "marketplace", upstreamProtocol: "http", targetPort: 8080, requiredPrivilege: "user", ingressAuth: "forward-auth", allowedIngressProtocols: ["https"], allowedExposures: ["local", "remote-private", "public"], originSelector: "control-authority-site", healthRef: "marketplace-bootstrap-ready", data: {bindingRef: "marketplace", requiredClasses: ["personal", "secret"], locality: "primary-site"}}]
	}]
	realizationSupport: {
		contractVersion: "1.0.0", scope: "concrete", level: "apply-ready", compatibleRendererRefs: ["stackkit"]
		inputs: {contractComplete: true, requiredRefs: []}, planInputs: {contractComplete: true, requiredRefs: ["kit"]}
		artifacts: {
			requiredRefs: ["marketplace-installation"]
			outputBindings: [{artifactRef: "marketplace-installation", unitRef: "marketplace", outputRef: "workloads/marketplace/installation.json"}]
			contracts: [{id: "marketplace-installation", kind: "native-config", format: "json", mode: "0600", required: true, compatibleTargets: ["compose", "opentofu"], unitRef: "marketplace", outputRef: "workloads/marketplace/installation.json"}]
		}
		evidence: requiredRefs: ["marketplace-native-installation"]
	}
	health: [{id: "marketplace-bootstrap-ready", kind: "http", phase: "continuous", path: "/api/health", port: 8080, timeoutSeconds: 10, expectedStatuses: [200]}]
	evidence: ["marketplace-native-installation"]
}]
