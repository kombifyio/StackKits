package foundation

_architectureV2AdguardHomeImage: {ref: "docker.io/adguard/adguardhome:v0.107.79", digest: "sha256:aba9e3bf0613be3ba3755e1fc311b126e2c24bec25e18b6483894a88283074f0"}

_architectureV2AdguardHomeStartup: "set +x; set -eu; umask 077; [ \"$STACKKITS_OWNER_HASH_FILE\" = /run/secrets/adguard-owner-bcrypt ]; [ -s \"$STACKKITS_OWNER_HASH_FILE\" ]; [ -n \"$STACKKITS_DNS_CONFIG_JSON\" ]; hash=$(base64 -d < \"$STACKKITS_OWNER_HASH_FILE\"); case \"$hash\" in '$2a$'*|'$2b$'*|'$2y$'*) ;; *) exit 1 ;; esac; [ \"${#hash}\" = 60 ]; case \"$hash\" in *[!A-Za-z0-9./$]*) exit 1 ;; esac; conf=/opt/adguardhome/conf; [ ! -L \"$conf\" ]; mkdir -p \"$conf\"; chmod 700 \"$conf\"; file=$(mktemp \"$conf/.AdGuardHome.XXXXXX\"); trap 'rm -f \"$file\"' EXIT HUP INT TERM; printf '%s\\n' \"$STACKKITS_DNS_CONFIG_JSON\" | sed \"s|@@OWNER_BCRYPT@@|$hash|g\" > \"$file\"; chmod 600 \"$file\"; mv -f \"$file\" \"$conf/AdGuardHome.yaml\"; trap - EXIT HUP INT TERM; unset hash file STACKKITS_DNS_CONFIG_JSON STACKKITS_OWNER_HASH_FILE; exec /opt/adguardhome/AdGuardHome -c \"$conf/AdGuardHome.yaml\" -w /opt/adguardhome/work --no-check-update"
_architectureV2AdguardHomeTerramateStack: _architectureV2WorkloadTerramateStack & {_slug: "adguard-home", _placement: {scope: "node-local", cardinality: "one-per-node"}}

_architectureV2AdguardHomeComputeProfile: {
	maturity:  "experimental", executable: true, realization: "apply-ready"
	hostFloor: _architectureV2CoreComputeProfile.hostFloor
	components: ["adguard-home"]
	provenance: {hostFloor: _architectureV2PolicyFloorProvenance, reservation: _architectureV2PolicyReservationProvenance}
}
// Allocations only: all profiles use exactly the same pinned DNS recipe.
// These CPU/RAM envelopes are policy, not physical-device or high-benefit proof.
_architectureV2AdguardHomeComputeProfiles: {
	low: #ModuleComputeProfileV2 & _architectureV2AdguardHomeComputeProfile & {description: "Same private selected-client DNS/setup functions with a smaller declared allocation. Physical small-device and whole-case qualification remain pending.", reservation: {ramGB: 0.0625, cpuCores: 0.05}, headroom: {ramGB: 0.0625, cpuCores: 0.45}, recommended: {ramGB: 0.125, cpuCores: 0.25}, provenance: {headroom: _architectureV2PolicyReservationProvenance, recommended: {source: "measured", ref: "docs/data/resource-evidence/stackkits-adguard-home-runtime/low.json"}}}
	standard: #ModuleComputeProfileV2 & _architectureV2AdguardHomeComputeProfile & {description: "Same private selected-client DNS/setup functions with the existing standard RAM envelope; CPU allocation is explicit. Physical/current-public qualification remains pending.", reservation: {ramGB: 0.125, cpuCores: 0.05}, headroom: {ramGB: 0.125, cpuCores: 0.95}, recommended: {ramGB: 0.125, cpuCores: 0.25}, provenance: {headroom: _architectureV2PolicyReservationProvenance, recommended: {source: "measured", ref: "docs/data/resource-evidence/stackkits-adguard-home-runtime/standard.json"}}}
	high: #ModuleComputeProfileV2 & _architectureV2AdguardHomeComputeProfile & {description: "Same private selected-client DNS/setup functions with a larger declared allocation. No new product function, QPS setting or measured high benefit is inferred.", reservation: {ramGB: 0.125, cpuCores: 0.05}, headroom: {ramGB: 0.375, cpuCores: 1.95}, recommended: {ramGB: 0.125, cpuCores: 0.25}, provenance: {headroom: _architectureV2PolicyReservationProvenance, recommended: {source: "measured", ref: "docs/data/resource-evidence/stackkits-adguard-home-runtime/high.json"}}}
}

_architectureV2AdguardHomeProviders: [{
	metadata: {id: "stackkits-adguard-home", version: "1.0.0"}
	provides: []
	workloadRefs: ["network"]
	requires: [{id: "runtime-paas"}, {id: "service-catalog"}, {id: "storage-data-policy"}, {id: "backup-core"}]
	supportedSiteKinds: ["home"]
	realization: {kind: "modules", moduleRefs: {required: [], optional: ["stackkits-adguard-home-runtime"]}}
	evidence: ["adguard-home-selected-paas-runtime-contract"]
}]

// Only the rendered configuration is backed up; the query log and statistics
// in work are rebuildable and stay out of the backup.
_architectureV2AdguardHomeInfrastructure: #WorkloadInfrastructureV1 & {
	storageAllocation: {moduleRef: "stackkits-storage-allocation", allocations: [
		{componentRef: "adguard-home", volumeRef: "config", target: "/opt/adguardhome/conf", class: "persistent", backup: true, dataClasses: ["personal"], dataBindingRef: "network"},
		{componentRef: "adguard-home", volumeRef: "work", target: "/opt/adguardhome/work", class: "persistent", backup: false, dataClasses: ["personal"], dataBindingRef: "network"},
	]}
	dataBinding: {moduleRef: "stackkits-workload-data-binding", bindingRef: "network", classes: ["personal"], locality: "primary-site"}
	backupSource: {moduleRef: "stackkits-backup-source", allocations: [for a in storageAllocation.allocations if a.backup {componentRef: a.componentRef, volumeRef: a.volumeRef, dataClasses: a.dataClasses}]}
	snapshot: moduleRef: "stackkits-snapshot"
	restore: moduleRef:  "stackkits-restore"
	recovery: moduleRef: "stackkits-recovery"
}

_architectureV2AdguardHomeWorkloads: [#WorkloadContractV2 & {
	metadata: {id: "network", version: "1.0.0", description: "AdGuard Home filters only explicitly selected private clients. The plan owns DNS policy; existing LAN DNS and DHCP remain untouched. Policy reservation is unmeasured."}
	kind: "application", useCaseRef: "network"
	functionalCapabilities: ["dns-filtering", "selected-client-dns"]
	supportedSiteKinds: ["home"]
	dataClasses: ["personal"]
	defaultAlternative: "adguard-home"
	computeTiers: {
		low: {included: true, alternativeID: "adguard-home"}
		standard: {included: true, alternativeID: "adguard-home"}
		high: {included: true, alternativeID: "adguard-home"}
	}
	alternatives: [{
		id: "adguard-home", providerRef: "stackkits-adguard-home", moduleRef: "stackkits-adguard-home-runtime"
		route: {serviceRef: "network", healthRef: "adguard-home-http"}
		infrastructure: _architectureV2AdguardHomeInfrastructure
		runtime: {
			allowedKinds: ["container"], allowedDeliveries: ["application-adapter"]
			allowedAdapterRefs: ["standalone-compose"], defaultAdapterRef: "standalone-compose", defaultFallbackAdapterRefs: []
			compatibility: [{adapterRef: "standalone-compose", maturity: "beta", capabilities: {deployment: true, routeTLS: true, statusEvidence: true, backupRestore: false}}]
		}
		setup: {mode: "on-demand", owner: "module", actionRefs: ["adguard-home-policy-check"]}
		inputs: {
			settings: {allowedRefs: ["dns-policy", "lan-listener"], requiredRefs: ["dns-policy"]}
			secretInputs: {allowedRefs: ["owner-password"], requiredRefs: ["owner-password"]}
		}
	}]
}]

_architectureV2AdguardHomeLifecycles: [#ApplicationLifecycleContractV1 & {
	metadata: {id: "network", version: "1.0.0", description: "AdGuard Home filters only explicitly selected private clients. The plan owns DNS policy; existing LAN DNS and DHCP remain untouched. Policy reservation is unmeasured."}
	workloadRef: "network", useCaseRef: "network", packageRef: "network"
	lifecycle: #StandardUseCaseLifecycle & {stages: setup: {}}
}]

_architectureV2AdguardHomeSupport: #ModuleRealizationSupportV2 & {
	contractVersion: "1.0.0", scope: "concrete", level: "apply-ready"
	compatibleRendererRefs: ["stackkit"]
	inputs: {contractComplete: true, requiredRefs: ["dns-policy", "owner-password", "compute-profile"]}
	artifacts: {
		requiredRefs: ["adguard-home-workload-bundle", _architectureV2AdguardHomeTerramateStack.contract.id]
		outputBindings: [{artifactRef: "adguard-home-workload-bundle", unitRef: "adguard-home", outputRef: "workloads/adguard-home/bundle.json"}, _architectureV2AdguardHomeTerramateStack.binding]
		contracts: [{id: "adguard-home-workload-bundle", kind: "native-config", format: "json", mode: "0640", required: true, compatibleTargets: ["compose", "opentofu"], unitRef: "adguard-home", outputRef: "workloads/adguard-home/bundle.json"}, _architectureV2AdguardHomeTerramateStack.contract]
	}
	evidence: requiredRefs: ["adguard-home-selected-paas-runtime-contract"]
}

_architectureV2AdguardHomeModules: [{
	metadata: {id: "stackkits-adguard-home-runtime", version: "1.0.0", description: "AdGuard Home filters only explicitly selected private clients. The plan owns DNS policy; existing LAN DNS and DHCP remain untouched. Policy reservation is unmeasured."}
	role: "workload", providerRef: "stackkits-adguard-home"
	provides: [], supportedSiteKinds: ["home"]
	nodeSelection: {authority: "control-authority-site", requiredRoles: ["worker"]}
	computeProfiles: _architectureV2AdguardHomeComputeProfiles, defaultComputeProfile: "standard"
	runtime: {
		kind:  "container", delivery:                              "application-adapter", engine: "docker"
		image: _architectureV2AdguardHomeImage, entryComponentRef: "adguard-home"
		components: [{
			id: "adguard-home", role: "application", lifecycle: "daemon", image: _architectureV2AdguardHomeImage
			dependsOn: [], networkRefs: ["adguard-home-internal"], egress: true
			entrypoint: ["/bin/sh"], command: ["-ec", _architectureV2AdguardHomeStartup]
			secretEnvironment: STACKKITS_OWNER_HASH_FILE: "owner-password"
			lanListeners: [{port: 53535, protocol: "udp", settingRef: "lan-listener"}, {port: 53535, protocol: "tcp", settingRef: "lan-listener"}]
			volumes: [{id: "config", target: "/opt/adguardhome/conf", class: "persistent", backup: true}, {id: "work", target: "/opt/adguardhome/work", class: "persistent", backup: false}]
			health: {kind: "http", path: "/login.html", port: 8080}
			resources: {memoryLimit: "256m", memoryReservation: "128m"}
		}]
	}
	renderUnits: [{
		id: "adguard-home", kind: "native-config", rendererRef: "stackkit", compatibleTargets: ["compose", "opentofu"]
		templateRef: "builtin://workloads/adguard-home/bundle/v2.json", version: "2.0.0", contractHash: "sha256:90f259424b178586a6ee96663d2305f15370a5db8be0ee8ce0abcbf62d6cdd24"
		publicInputRefs: ["delivery-route", "dns-policy", "lan-listener", "compute-profile"]
		inputBindings: [{targetRef: "delivery-route", sourceRef: "network.moduleRoute", valueType: "authority-bound-module-route-v1", cardinality: "single", required: false, defaultValue: null}, {targetRef: "compute-profile", sourceRef: "modules.computeProfileBinding", valueType: "module-compute-profile-v1", cardinality: "single", required: true}]
		secretInputRefs: ["owner-password"], outputs: ["workloads/adguard-home/bundle.json"]
		placement: {scope: "node-local", cardinality: "one-per-node"}
		serviceEndpoints: [{serviceRef: "network", upstreamProtocol: "http", targetPort: 8080, requiredPrivilege: "user", ingressAuth: "forward-auth", allowedIngressProtocols: ["https"], allowedExposures: ["local", "remote-private", "public"], originSelector: "control-authority-site", healthRef: "adguard-home-http", data: {bindingRef: "network", requiredClasses: ["personal"], locality: "primary-site"}}]
	}, _architectureV2AdguardHomeTerramateStack.unit]
	renderVariants: [{
		id: "compose", target: "compose", rendererRef: "stackkit", contractHash: "sha256:c29cb073dde154d4aa38789b506874de7f2d2364fe8deb2dbb181a507fcc346a"
		unitRefs: ["adguard-home"], artifactRefs: ["adguard-home-workload-bundle"], publicInputRefs: ["delivery-route", "dns-policy", "lan-listener", "compute-profile"], secretInputRefs: ["owner-password"], planInputRefs: []
	}, {
		id: "opentofu", target: "opentofu", rendererRef: "stackkit", contractHash: "sha256:c13901023f2cb0271580070c4e115940afa77510e42b9aad549bfd8981ed5690"
		unitRefs: ["adguard-home"], artifactRefs: ["adguard-home-workload-bundle"], publicInputRefs: ["delivery-route", "dns-policy", "lan-listener", "compute-profile"], secretInputRefs: ["owner-password"], planInputRefs: []
	}, {
		id: "terramate", target: "terramate", rendererRef: "stackkit", contractHash: _architectureV2TerramateStackHashes.workload
		unitRefs: ["adguard-home", "terramate-stack"], artifactRefs: ["adguard-home-workload-bundle", _architectureV2AdguardHomeTerramateStack.contract.id], publicInputRefs: ["delivery-route", "dns-policy", "lan-listener", "compute-profile"], secretInputRefs: ["owner-password"], planInputRefs: []
	}]
	realizationSupport: _architectureV2AdguardHomeSupport
	health: [{id: "adguard-home-http", phase: "continuous", kind: "http", path: "/login.html", port: 8080, timeoutSeconds: 10, expectedStatuses: [200]}]
	evidence: ["adguard-home-selected-paas-runtime-contract"]
}]
