// Explicit live-only notification relay. It never falls back from a durable
// request and initializes no SQL, native-user, Web Push or attachment store.
package foundation

_architectureV2NtfyLiveImage: {ref: "docker.io/binwiederhier/ntfy:v2.30.0", digest: "sha256:8563a39da2f5a47360618b2ab9200239c76b37f4145f6d627d9ab4368abd23f0"}
_architectureV2NtfyLiveStack: _architectureV2WorkloadTerramateStack & {_slug: "ntfy-live", _placement: {scope: "node-local", cardinality: "one-per-node"}}
_architectureV2NtfyLiveProviders: [{
	metadata: {id: "stackkits-ntfy-live", version: "1.0.0"}
	provides: [], workloadRefs: ["automation-notifications"]
	requires: [{id: "runtime-paas"}, {id: "service-catalog"}]
	supportedSiteKinds: ["home", "cloud"]
	realization: {kind: "modules", moduleRefs: {required: [], optional: ["stackkits-ntfy-live-runtime"]}}
	evidence: ["ntfy-live-selected-paas-runtime-contract"]
}]
// Data contract (backup and restore): nothing to carry. The live-only relay
// keeps no cache, SQL store, native account, Web Push key or attachment, so the
// workload declares no storage allocation and its delivery row keeps
// `backupRestore: false`; a node restore skips it because it holds nothing to
// restore. Durable push is a separate prerequisite and must claim the capability
// together with its first backup-enabled volume: the catalog refuses the
// combination otherwise.
_architectureV2NtfyLiveWorkloads: [#WorkloadContractV2 & {
	metadata: {id: "automation-notifications", version: "1.0.0", description: "Explicit ntfy live-only notification add-on: forward to connected subscribers; no queued replay, native accounts, Web Push or attachments. Durable push and backup digest delivery remain separate prerequisites."}
	kind: "application", useCaseRef: "automation", functionalCapabilities: ["live-notifications"], supportedSiteKinds: ["home", "cloud"], dataClasses: []
	defaultAlternative: "ntfy-live"
	computeTiers: {low: {included: false, reason: "No independently measured low variant."}, standard: {included: true, alternativeID: "ntfy-live"}, high: {included: false, reason: "No named measured high capability; standard is not copied as high."}}
	alternatives: [{
		id: "ntfy-live", providerRef: "stackkits-ntfy-live", moduleRef: "stackkits-ntfy-live-runtime"
		route: {serviceRef: "automation-notifications", healthRef: "ntfy-live-http"}
		runtime: {allowedKinds: ["container"], allowedDeliveries: ["application-adapter"], allowedAdapterRefs: ["standalone-compose"], defaultAdapterRef: "standalone-compose", defaultFallbackAdapterRefs: [], compatibility: [{adapterRef: "standalone-compose", maturity: "beta", capabilities: {deployment: true, routeTLS: true, statusEvidence: true, backupRestore: false}}]}
		setup: {mode: "on-demand", owner: "module", actionRefs: ["ntfy-live-delivery-check"]}
		inputs: {settings: {allowedRefs: [], requiredRefs: []}, secretInputs: {allowedRefs: [], requiredRefs: []}}
	}]
}]
_architectureV2NtfyLiveLifecycles: [#ApplicationLifecycleContractV1 & {
	metadata: {id: "automation-notifications", version: "1.0.0", description: "Node-bound live relay lifecycle and non-account delivery check; no durable message or identity acceptance."}
	workloadRef: "automation-notifications", useCaseRef: "automation", packageRef: "automation", lifecycle: #StandardUseCaseLifecycle & {stages: setup: {}}
}]
_architectureV2NtfyLiveSupport: #ModuleRealizationSupportV2 & {
	contractVersion: "1.0.0", scope: "concrete", level: "apply-ready", compatibleRendererRefs: ["stackkit"]
	inputs: {contractComplete: true, requiredRefs: []}
	artifacts: {requiredRefs: ["ntfy-live-workload-bundle", _architectureV2NtfyLiveStack.contract.id], outputBindings: [{artifactRef: "ntfy-live-workload-bundle", unitRef: "ntfy-live", outputRef: "workloads/ntfy-live/bundle.json"}, _architectureV2NtfyLiveStack.binding], contracts: [{id: "ntfy-live-workload-bundle", kind: "native-config", format: "json", mode: "0640", required: true, compatibleTargets: ["compose", "opentofu"], unitRef: "ntfy-live", outputRef: "workloads/ntfy-live/bundle.json"}, _architectureV2NtfyLiveStack.contract]}
	evidence: requiredRefs: ["ntfy-live-selected-paas-runtime-contract"]
}
_architectureV2NtfyLiveModules: [{
	metadata: {id: "stackkits-ntfy-live-runtime", version: "1.0.0", description: "Digest-pinned ntfy2.30.0 explicit live-only relay. No persistence, accounts, Web Push, attachment custody or provider execution. Optional administrator-only private route."}
	role: "workload", providerRef: "stackkits-ntfy-live", provides: [], supportedSiteKinds: ["home", "cloud"], nodeSelection: {authority: "control-authority-site", requiredRoles: ["worker"]}
	computeProfiles: standard: #ModuleComputeProfileV2 & {description: "Connected-client live relay only. No backlog or mobile/offline guarantee. Resource reservations are configured policy; no measured low/high benefit is declared.", maturity: "experimental", executable: true, realization: "apply-ready", hostFloor: _architectureV2CoreComputeProfile.hostFloor, reservation: ramGB: 0.0625, components: ["ntfy-live"], provenance: {hostFloor: _architectureV2PolicyFloorProvenance, reservation: _architectureV2PolicyReservationProvenance}}
	defaultComputeProfile: "standard"
	runtime: {kind: "container", delivery: "application-adapter", engine: "docker", image: _architectureV2NtfyLiveImage, entryComponentRef: "ntfy-live", components: [{
		id: "ntfy-live", role: "application", lifecycle: "daemon", image: _architectureV2NtfyLiveImage, dependsOn: [], networkRefs: ["ntfy-live-internal"]
		command: ["serve", "--cache-duration", "0", "--enable-login=false", "--enable-signup=false", "--enable-reservations=false"]
		environment: {NTFY_LOG_LEVEL: "WARN", NTFY_AUTH_FILE: "", NTFY_DATABASE_URL: "", NTFY_WEB_PUSH_FILE: "", NTFY_WEB_PUSH_PUBLIC_KEY: "", NTFY_WEB_PUSH_PRIVATE_KEY: "", NTFY_ATTACHMENT_CACHE_DIR: "", NTFY_UPSTREAM_BASE_URL: ""}
		health: {kind: "http", path: "/v1/health", port: 80}, resources: {memoryLimit: "256m", memoryReservation: "64m"}
	}]}
	renderUnits: [{
		id: "ntfy-live", kind: "native-config", rendererRef: "stackkit", compatibleTargets: ["compose", "opentofu"], templateRef: "builtin://workloads/ntfy-live/bundle/v2.json", version: "2.0.0", contractHash: "sha256:6fc300a89932443df39baf7a1c8a39e7120013516aec75aede10430475211fbc"
		publicInputRefs: ["delivery-route"], inputBindings: [{targetRef: "delivery-route", sourceRef: "network.moduleRoute", valueType: "authority-bound-module-route-v1", cardinality: "single", required: false, defaultValue: null}], secretInputRefs: [], outputs: ["workloads/ntfy-live/bundle.json"], placement: {scope: "node-local", cardinality: "one-per-node"}
		serviceEndpoints: [{serviceRef: "automation-notifications", upstreamProtocol: "http", targetPort: 80, requiredPrivilege: "admin", ingressAuth: "forward-auth", allowedIngressProtocols: ["https"], allowedExposures: ["local", "remote-private"], originSelector: "control-authority-site", healthRef: "ntfy-live-http"}]
	}, _architectureV2NtfyLiveStack.unit]
	renderVariants: [
		{id: "compose", target: "compose", rendererRef: "stackkit", contractHash: "sha256:66aa1152e7d87b3292a2eefbcca398746546e6499c60b70000a0a7cb34704f02", unitRefs: ["ntfy-live"], artifactRefs: ["ntfy-live-workload-bundle"], publicInputRefs: ["delivery-route"], secretInputRefs: [], planInputRefs: []},
		{id: "opentofu", target: "opentofu", rendererRef: "stackkit", contractHash: "sha256:52e50871398d3f1fc5bca11be237abce135bdb39157fa33bc23b6b3d18bd0e41", unitRefs: ["ntfy-live"], artifactRefs: ["ntfy-live-workload-bundle"], publicInputRefs: ["delivery-route"], secretInputRefs: [], planInputRefs: []},
		{id: "terramate", target: "terramate", rendererRef: "stackkit", contractHash: _architectureV2TerramateStackHashes.workload, unitRefs: ["ntfy-live", "terramate-stack"], artifactRefs: ["ntfy-live-workload-bundle", _architectureV2NtfyLiveStack.contract.id], publicInputRefs: ["delivery-route"], secretInputRefs: [], planInputRefs: []},
	]
	realizationSupport: _architectureV2NtfyLiveSupport
	health: [{id: "ntfy-live-http", phase: "continuous", kind: "http", path: "/v1/health", port: 80, timeoutSeconds: 10, expectedStatuses: [200]}]
	evidence: ["ntfy-live-selected-paas-runtime-contract"]
}]
