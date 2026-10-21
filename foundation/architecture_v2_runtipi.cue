package foundation

// Prepared source only: none of these definitions is appended to
// ArchitectureV2Catalog. Marketplace remains recorded and Runtipi cannot be
// selected until the current-pin application lifecycle has real evidence.
#PreparedRuntipiMarketplaceAlternative: #WorkloadAlternativeV2 & {
	id:                        "runtipi", providerRef:       "stackkits-marketplace", moduleRef: "stackkits-marketplace-runtipi-runtime"
	providesRuntimeAdapterRef: "runtipi", minimumTLSVersion: "TLS1.3"
	route: {serviceRef: "marketplace", healthRef: "runtipi-bootstrap-ready"}
	runtime: {allowedKinds: ["host"], allowedDeliveries: ["stackkit"], allowedAdapterRefs: []}
	setup: {mode: "automatic", owner: "module", actionRefs: ["runtipi-owner-bootstrap"]}
	inputs: {settings: {allowedRefs: [], requiredRefs: []}, secretInputs: {allowedRefs: [], requiredRefs: []}}
}

#PreparedRuntipiProvider: #CapabilityProvider & {
	metadata: {id: "stackkits-runtipi", version: "1.0.0"}
	runtimeAdapterRefs: ["runtipi"]
	provides: [], requires: []
	supportedSiteKinds: ["home", "cloud"]
	realization: {kind: "modules", moduleRefs: {required: ["stackkits-runtipi-runtime"], optional: []}}
	evidence: ["runtipi-current-pin-lifecycle"]
}

#PreparedRuntipiComputeProfile: #ModuleComputeProfileV2 & {
	description:        "Unqualified pinned Runtipi backend, database and queue policy envelope; no measured capacity claim."
	maturity:           "beta", executable: true, realization: "apply-ready"
	platformManagement: "standalone"
	components: ["runtipi", "postgres", "queue"]
	hostFloor: {minCpuCores: 2, minRamGB: 4, minStorageGB: 20}
	reservation: {ramGB: 2.25, cpuCores: 0.5}
	recommended: {ramGB: 4, cpuCores: 2}
	provenance: {hostFloor: _architectureV2PolicyFloorProvenance, reservation: _architectureV2PolicyReservationProvenance, recommended: _architectureV2PolicyRecommendedProvenance}
}

#PreparedRuntipiMarketplaceModule: #ModuleContractV2 & {
	metadata: {id: "stackkits-marketplace-runtipi-runtime", version: "1.0.0", description: "Prepared, unqualified node-owned Runtipi installation with encrypted operator custody and the existing Kit router."}
	role: "workload", providerRef: "stackkits-marketplace"
	provides: [], requires: ["stackkits-core-host-bootstrap"]
	supportedSiteKinds: ["home", "cloud"]
	nodeSelection: {authority: "control-authority-site", controlPlaneMembers: "only", requiredRoles: ["worker"]}
	computeProfiles: {standard: #PreparedRuntipiComputeProfile, high: #PreparedRuntipiComputeProfile}
	defaultComputeProfile: "standard"
	runtime: {
		execution: "executable", kind: "host", delivery: "stackkit", entryComponentRef: "runtipi"
		components: [{
			id: "runtipi", role: "application", lifecycle: "daemon", networkRefs: ["marketplace", "kit-core"]
			image: {ref: "ghcr.io/runtipi/runtipi:v4.10.2", digest: "sha256:9cdb7297d8f3d3f3d74425b56caecb3d452a4916146136503d9163b477ace6f0"}
			dependsOn: ["postgres", "queue"], health: {kind: "http", path: "/api/health", port: 3000}
			resources: memoryLimit: "1536m"
		}, {
			id: "postgres", role: "database", lifecycle: "daemon", networkRefs: ["marketplace"]
			image: {ref: "postgres:14", digest: "sha256:14bfab572eec6abf65892e1db7c3ba8d41b2a2855c1b143664907d6e146eb6e1"}
			health: kind: "image", resources: memoryLimit: "512m"
		}, {
			id: "queue", role: "application", lifecycle: "daemon", networkRefs: ["marketplace"]
			image: {ref: "rabbitmq:4-alpine", digest: "sha256:8668a6ed4a878a6619213d9d5721dd4983ecb662312fe233816e1d1ff8a96753"}
			health: kind: "image", resources: memoryLimit: "256m"
		}]
	}
	renderUnits: [{
		id: "marketplace", kind: "native-config", rendererRef: "stackkit", compatibleTargets: ["compose", "opentofu"]
		templateRef: "builtin://workloads/marketplace/runtipi-installation/v1.json", version: "1.0.0", contractHash: "sha256:f6ff4df74dede73dd1a571154680c5636ad7c5be5c3fca3cb8db3b97cc5652c9"
		publicInputRefs: ["delivery-route"], secretInputRefs: [], planInputRefs: ["kit"]
		inputBindings: [{targetRef: "delivery-route", sourceRef: "network.moduleRoute", valueType: "authority-bound-module-route-v1", cardinality: "single", required: false, defaultValue: null}]
		outputs: ["workloads/marketplace/runtipi-installation.json"]
		placement: {scope: "node-local", cardinality: "one-per-node"}
		serviceEndpoints: [{serviceRef: "marketplace", upstreamProtocol: "http", targetPort: 3000, requiredPrivilege: "user", ingressAuth: "forward-auth", allowedIngressProtocols: ["https"], allowedExposures: ["local", "remote-private", "public"], originSelector: "control-authority-site", healthRef: "runtipi-bootstrap-ready", data: {bindingRef: "marketplace", requiredClasses: ["personal", "secret"], locality: "primary-site"}}]
	}]
	realizationSupport: {
		contractVersion: "1.0.0", scope: "concrete", level: "apply-ready", compatibleRendererRefs: ["stackkit"]
		inputs: {contractComplete: true, requiredRefs: []}, planInputs: {contractComplete: true, requiredRefs: ["kit"]}
		artifacts: {
			requiredRefs: ["marketplace-runtipi-installation"]
			outputBindings: [{artifactRef: "marketplace-runtipi-installation", unitRef: "marketplace", outputRef: "workloads/marketplace/runtipi-installation.json"}]
			contracts: [{id: "marketplace-runtipi-installation", kind: "native-config", format: "json", mode: "0600", required: true, compatibleTargets: ["compose", "opentofu"], unitRef: "marketplace", outputRef: "workloads/marketplace/runtipi-installation.json"}]
		}
		evidence: requiredRefs: ["runtipi-current-pin-lifecycle"]
	}
	health: [{id: "runtipi-bootstrap-ready", kind: "http", phase: "continuous", path: "/api/health", port: 3000, timeoutSeconds: 10, expectedStatuses: [200]}]
	evidence: ["runtipi-current-pin-lifecycle"]
}

#PreparedRuntipiAdapterModule: #ModuleContractV2 & {
	metadata: {id: "stackkits-runtipi-runtime", version: "1.0.0", description: "Prepared, unqualified node-custodied Runtipi application adapter; no server-provider lifecycle."}
	role: "platform", providerRef: "stackkits-runtipi"
	provides: [], requires: ["stackkits-marketplace-runtipi-runtime"]
	supportedSiteKinds: ["home", "cloud"]
	nodeSelection: {authority: "control-authority-site", controlPlaneMembers: "only", requiredRoles: ["worker"]}
	runtimeAdapter: {id: "runtipi", supportedKinds: ["container"], supportedDeliveries: ["application-adapter"], operations: ["apply", "observe"], capabilities: ["application-setup-local-api"], credentialCustody: "local-owner", providerLifecycle: "not-owned", evidenceRequired: true}
	runtime: {execution: "contract-handoff", kind: "control-plane", delivery: "external-control-plane"}
	renderUnits: [{
		id: "runtipi-adapter", kind: "native-config", rendererRef: "stackkit", compatibleTargets: ["compose", "opentofu"]
		templateRef: "builtin://platform/runtipi/runtime-adapter/v1.json", version: "1.0.0", contractHash: "sha256:2765e73e1718e1186b54f61bf0ef7104980e4f2e743243416830a198d50559ef"
		publicInputRefs: [], secretInputRefs: [], planInputRefs: []
		outputs: ["platform/runtipi/runtime-adapter.json"]
		placement: {scope: "node-local", cardinality: "one-per-node"}
	}]

	realizationSupport: {
		contractVersion: "1.0.0", scope: "concrete", level: "generation-ready", compatibleRendererRefs: ["stackkit"]
		inputs: {contractComplete: true, requiredRefs: []}, planInputs: {contractComplete: true, requiredRefs: []}
		artifacts: {
			requiredRefs: ["runtipi-runtime-adapter"]
			outputBindings: [{artifactRef: "runtipi-runtime-adapter", unitRef: "runtipi-adapter", outputRef: "platform/runtipi/runtime-adapter.json"}]
			contracts: [{id: "runtipi-runtime-adapter", kind: "native-config", format: "json", mode: "0600", required: true, compatibleTargets: ["compose", "opentofu"], unitRef: "runtipi-adapter", outputRef: "platform/runtipi/runtime-adapter.json"}]
		}
		evidence: requiredRefs: ["runtipi-current-pin-lifecycle"]
	}
	health: [{id: "runtipi-runtime-contract", kind: "contract"}]
	evidence: ["runtipi-current-pin-lifecycle"]
}
