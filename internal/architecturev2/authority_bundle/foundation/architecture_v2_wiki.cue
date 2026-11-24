// Wiki admission is pinned to the exact-main Delivery image the AI Platform
// published and the authenticated mirror copied. A missing artifact never
// becomes a placeholder pin.
package foundation

import (
	"crypto/sha256"
	"encoding/hex"
)

#WikiPublishedArtifact: {
	sourceSHA: =~"^[0-9a-f]{40}$"
	digest:    =~"^sha256:[0-9a-f]{64}$" & !="sha256:0000000000000000000000000000000000000000000000000000000000000000"
	receipt:   =~"^https://github.com/kombify/kombify-AI-Platform/actions/runs/[0-9]+$"
}

// The admitted artifact is the exact-main ai-knowledge-node Delivery image:
// parent publication run 45242842246 built it, and the authenticated mirror
// child run 45243200601 (delivery-knowledge-node-image.yml, artifact
// 14853710294) read the same immutable digest back. GHCR stays private
// upstream; the customer pull reference is the api.kombify.io OCI endpoint.
_architectureV2WikiPublication: #WikiPublishedArtifact & {
	sourceSHA: "092c18ccacfaa36acfa7cefa424c1136c0092481"
	digest:    "sha256:cd0cbd69cf48dd03530d9054d1c2831f7b03c7b724f4fce593c8283a9a78699b"
	receipt:   "https://github.com/kombify/kombify-AI-Platform/actions/runs/45242842246"
}

// Bind names outside the conditional scopes so other catalog files can use them.
ArchitectureV2WikiWorkloadRefs:    _
_architectureV2WikiProviders:      _
_architectureV2WikiModules:        _
_architectureV2WikiWorkloads:      _
_architectureV2WikiLifecycles:     _
_architectureV2WikiImage:          _
_architectureV2WikiStack:          _
_architectureV2WikiSupport:        _
_architectureV2WikiInfrastructure: _

if _architectureV2WikiPublication == null {
	ArchitectureV2WikiWorkloadRefs: []
	_architectureV2WikiProviders: []
	_architectureV2WikiModules: []
	_architectureV2WikiWorkloads: []
	_architectureV2WikiLifecycles: []
}
if _architectureV2WikiPublication != null {
	_architectureV2WikiPublication: #WikiPublishedArtifact
	ArchitectureV2WikiWorkloadRefs: ["wiki"]
	_architectureV2WikiImage: {ref: "api.kombify.io/kombiverselabs/kombify-knowledge-node:git-\(_architectureV2WikiPublication.sourceSHA)", digest: _architectureV2WikiPublication.digest}
	_architectureV2WikiStack: _architectureV2WorkloadTerramateStack & {_slug: "wiki", _placement: {scope: "node-local", cardinality: "one-per-node"}}
	_architectureV2WikiProviders: [{
		metadata: {id: "stackkits-wiki", version: "1.0.0"}
		provides: [], workloadRefs: ["wiki"]
		requires: [{id: "runtime-paas"}, {id: "service-catalog"}, {id: "storage-data-policy"}, {id: "backup-core"}]
		supportedSiteKinds: ["home", "cloud"]
		realization: {kind: "modules", moduleRefs: {required: [], optional: ["stackkits-wiki-runtime"]}}
		evidence: ["wiki-selected-paas-runtime-contract"]
	}]
	_architectureV2WikiWorkloads: [#WorkloadContractV2 & {
		metadata: {id: "wiki", version: "1.0.0", description: "Personal IT Wiki: one SQLite file, local reader and native MCP. Replica connection stays off until the account bundle route is admitted."}
		kind: "application", useCaseRef: "wiki", functionalCapabilities: ["wiki-read", "wiki-search", "wiki-graph"], supportedSiteKinds: ["home", "cloud"], dataClasses: ["personal"]
		defaultAlternative: "wiki"
		computeTiers: {low: {included: false, reason: "No independently measured low variant."}, standard: {included: true, alternativeID: "wiki"}, high: {included: false, reason: "No named measured high capability; standard is not copied as high."}}
		alternatives: [{
			id: "wiki", providerRef: "stackkits-wiki", moduleRef: "stackkits-wiki-runtime"
			route: {serviceRef: "wiki", healthRef: "wiki-http"}
			runtime: {allowedKinds: ["container"], allowedDeliveries: ["application-adapter"], allowedAdapterRefs: ["standalone-compose"], defaultAdapterRef: "standalone-compose", defaultFallbackAdapterRefs: [], compatibility: [{adapterRef: "standalone-compose", maturity: "beta", capabilities: {deployment: true, routeTLS: true, statusEvidence: true, backupRestore: true}}]}
			setup: {mode: "manual", owner: "operator", actionRefs: []}
			infrastructure: _architectureV2WikiInfrastructure
			inputs: {settings: {allowedRefs: [], requiredRefs: []}, secretInputs: {allowedRefs: ["read-bearer", "write-bearer", "connection", "registry-auth"], requiredRefs: ["read-bearer", "write-bearer", "connection", "registry-auth"]}}
		}]
	}]
	_architectureV2WikiLifecycles: [#ApplicationLifecycleContractV1 & {
		metadata: {id: "wiki", version: "1.0.0", description: "Personal IT Wiki: one SQLite file, local reader and native MCP. Replica connection stays off until the account bundle route is admitted."}
		workloadRef: "wiki", useCaseRef: "wiki", packageRef: "wiki", lifecycle: #StandardUseCaseLifecycle & {stages: setup: {}}
	}]
	_architectureV2WikiSupport: #ModuleRealizationSupportV2 & {
		contractVersion: "1.0.0", scope: "concrete", level: "apply-ready", compatibleRendererRefs: ["stackkit"]
		inputs: {contractComplete: true, requiredRefs: ["read-bearer", "write-bearer", "connection", "registry-auth"]}
		artifacts: {requiredRefs: ["wiki-workload-bundle", _architectureV2WikiStack.contract.id], outputBindings: [{artifactRef: "wiki-workload-bundle", unitRef: "wiki", outputRef: "workloads/wiki/bundle.json"}, _architectureV2WikiStack.binding], contracts: [{id: "wiki-workload-bundle", kind: "native-config", format: "json", mode: "0640", required: true, compatibleTargets: ["compose", "opentofu"], unitRef: "wiki", outputRef: "workloads/wiki/bundle.json"}, _architectureV2WikiStack.contract]}
		evidence: requiredRefs: ["wiki-selected-paas-runtime-contract"]
	}
	_architectureV2WikiModules: [{
		metadata: {id: "stackkits-wiki-runtime", version: "1.0.0", description: "Personal IT Wiki: one SQLite file, local reader and native MCP. Replica connection stays off until the account bundle route is admitted."}
		role: "workload", providerRef: "stackkits-wiki", provides: [], supportedSiteKinds: ["home", "cloud"], nodeSelection: {authority: "control-authority-site", requiredRoles: ["worker"]}
		computeProfiles: standard: #ModuleComputeProfileV2 & {description: "Personal IT Wiki: one SQLite file, local reader and native MCP. Replica connection stays off until the account bundle route is admitted.", maturity: "experimental", executable: true, realization: "apply-ready", hostFloor: _architectureV2CoreComputeProfile.hostFloor & {allowedArchitectures: ["amd64"]}, reservation: ramGB: 0.125, components: ["wiki"], provenance: {hostFloor: _architectureV2PolicyFloorProvenance, reservation: _architectureV2PolicyReservationProvenance}}
		defaultComputeProfile: "standard"
		runtimeRequirements: allowedArchitectures: ["amd64"]
		runtime: {kind: "container", delivery: "application-adapter", engine: "docker", image: _architectureV2WikiImage, entryComponentRef: "wiki", components: [{
			id: "wiki", role: "application", lifecycle: "daemon", image: _architectureV2WikiImage, dependsOn: [], networkRefs: ["wiki-internal"]
			egress: true
			entrypoint: ["/app/wiki"], command: ["serve", "-initialize"]
			environment: {WIKI_FILE: "/var/lib/wiki/wiki.db", WIKI_LISTEN: "0.0.0.0:8484"}
			secretEnvironment: {WIKI_READ_BEARER: "read-bearer", WIKI_WRITE_BEARER: "write-bearer"}
			secretFiles: [{slot: "connection", target: "/run/secrets/wiki-connection", pathEnvironment: "WIKI_CONNECTION_FILE", uid: 65532, gid: 65532}]
			volumes: [{id: "state", target: "/var/lib/wiki", class: "persistent", backup: true}]
			health: {kind: "http", path: "/health", port: 8484}, resources: {memoryLimit: "512m", memoryReservation: "128m"}
		}]}
		renderUnits: [{
			id: "wiki", kind: "native-config", rendererRef: "stackkit", compatibleTargets: ["compose", "opentofu"], templateRef: "builtin://workloads/wiki/bundle/v2.json", version: "2.0.0", contractHash: "sha256:\(hex.Encode(sha256.Sum256("stackkit.workload-bundle/v2|WikiWorkloadBundle|application-adapter|route:authority-bound-module-route-v1|provider-lifecycle:not-owned|components:wiki|data:wiki|release:git-\(_architectureV2WikiPublication.sourceSHA)|secret-material:references-only|format:kombify.wiki.v1|initialize:missing-local-writer-only|connection:node-custody|registry:node-custody-oci:secret://stackkits/wiki/registry-auth|peer:none")))"
			publicInputRefs: ["delivery-route"], inputBindings: [{targetRef: "delivery-route", sourceRef: "network.moduleRoute", valueType: "authority-bound-module-route-v1", cardinality: "single", required: false, defaultValue: null}], secretInputRefs: ["read-bearer", "write-bearer", "connection", "registry-auth"], outputs: ["workloads/wiki/bundle.json"], placement: {scope: "node-local", cardinality: "one-per-node"}
			serviceEndpoints: [{serviceRef: "wiki", upstreamProtocol: "http", targetPort: 8484, requiredPrivilege: "user", ingressAuth: "native", allowedIngressProtocols: ["https"], allowedExposures: ["local", "remote-private"], originSelector: "control-authority-site", healthRef: "wiki-http", data: {bindingRef: "wiki", requiredClasses: ["personal"], locality: "primary-site"}}]
		}, _architectureV2WikiStack.unit]
		renderVariants: [
			{id: "compose", target: "compose", rendererRef: "stackkit", contractHash: "sha256:66aa1152e7d87b3292a2eefbcca398746546e6499c60b70000a0a7cb34704f02", unitRefs: ["wiki"], artifactRefs: ["wiki-workload-bundle"], publicInputRefs: ["delivery-route"], secretInputRefs: ["read-bearer", "write-bearer", "connection", "registry-auth"], planInputRefs: []},
			{id: "opentofu", target: "opentofu", rendererRef: "stackkit", contractHash: "sha256:52e50871398d3f1fc5bca11be237abce135bdb39157fa33bc23b6b3d18bd0e41", unitRefs: ["wiki"], artifactRefs: ["wiki-workload-bundle"], publicInputRefs: ["delivery-route"], secretInputRefs: ["read-bearer", "write-bearer", "connection", "registry-auth"], planInputRefs: []},
			{id: "terramate", target: "terramate", rendererRef: "stackkit", contractHash: _architectureV2TerramateStackHashes.workload, unitRefs: ["wiki", "terramate-stack"], artifactRefs: ["wiki-workload-bundle", _architectureV2WikiStack.contract.id], publicInputRefs: ["delivery-route"], secretInputRefs: ["read-bearer", "write-bearer", "connection", "registry-auth"], planInputRefs: []},
		]
		realizationSupport: _architectureV2WikiSupport
		health: [{id: "wiki-http", phase: "continuous", kind: "http", path: "/health", port: 8484, timeoutSeconds: 10, expectedStatuses: [200]}]
		evidence: ["wiki-selected-paas-runtime-contract"]
	}]

	_architectureV2WikiInfrastructure: #WorkloadInfrastructureV1 & {
		dataBinding: {moduleRef: "stackkits-workload-data-binding", bindingRef: "wiki", classes: ["personal"], locality: "primary-site"}
		storageAllocation: {moduleRef: "stackkits-storage-allocation", allocations: [{componentRef: "wiki", volumeRef: "state", target: "/var/lib/wiki", class: "persistent", backup: true, dataClasses: ["personal"], dataBindingRef: "wiki"}]}
		backupSource: {moduleRef: "stackkits-backup-source", allocations: [for a in storageAllocation.allocations if a.backup {componentRef: a.componentRef, volumeRef: a.volumeRef, dataClasses: a.dataClasses, backupDataClass: "secret-material"}]}
		snapshot: moduleRef: "stackkits-snapshot"
		restore: moduleRef:  "stackkits-restore"
		recovery: moduleRef: "stackkits-recovery"
	}

}
