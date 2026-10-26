package foundation

// Prepared prerequisite contracts, deliberately outside ArchitectureV2Catalog.
// A hidden Vault consumer connects the standard provider executor. The
// coordinator must obtain real-host receipts and regenerate authority bundles
// before allowing public workload selection of this adapter.
// Neither definition promotes the draft cells or changes the Compose default.
#PreparedDokployProvider: #CapabilityProvider & {
	metadata: {id: "stackkits-dokploy", version: "1.0.0"}
	provides: [], runtimeAdapterRefs: ["dokploy"]
	requires: [{id: "runtime-paas"}, {id: "service-catalog"}]
	supportedSiteKinds: ["home", "cloud"]
	realization: {kind: "modules", moduleRefs: {required: ["stackkits-dokploy-runtime"], optional: []}}
	health: [{id: "dokploy-runtime-contract", kind: "contract"}]
	evidence: ["dokploy-current-pin-lifecycle"]
}

// PocketID authenticates at the existing TinyAuth protection boundary.
// It is not native Dokploy OIDC. Dokploy still asks for its local owner login;
// the existing owner/bootstrap identity is retained, never recreated here.
#PreparedDokployLoginPolicy: {
	outerAuth: "tinyauth-pocketid"
	appAuth: "local-owner"
	nativeOIDC: "licensed-not-configured"
	routing: "existing-router-owner-no-second-ingress"
	bootstrap: "existing-node-owner-bootstrap-authenticated-and-encrypted"
	provider: {source: "vanillauys/dokploy", version: "1.10.0", requiredServerVersion: "0.30.8"}
}

#PreparedDokployAdapterModule: #ModuleContractV2 & {
	metadata: {id: "stackkits-dokploy-runtime", version: "1.0.0", description: "Prepared Dokploy Layer-2 provider handoff using authenticated existing bootstrap and node-held identities; current-pin lifecycle is unqualified."}
	role: "platform", providerRef: "stackkits-dokploy"
	provides: [], requires: ["stackkits-core-host-bootstrap"]
	supportedSiteKinds: ["home", "cloud"]
	nodeSelection: {authority: "control-authority-site", controlPlaneMembers: "only", requiredRoles: ["worker"]}
	runtimeAdapter: {
		id: "dokploy", supportedKinds: ["container"], supportedDeliveries: ["application-adapter"]
		operations: ["apply", "observe"]
		credentialCustody: "local-owner", providerLifecycle: "not-owned", evidenceRequired: true
	}
	runtime: {execution: "contract-handoff", kind: "control-plane", delivery: "external-control-plane"}
	renderUnits: [{
		id: "dokploy-adapter", kind: "native-config", rendererRef: "stackkit", compatibleTargets: ["opentofu"]
		templateRef: "builtin://platform/dokploy/runtime-adapter/v1.json", version: "1.0.0"
		contractHash: "sha256:d68e5c60af35ed780ee057eea9c7a570919c2ab92b83224176df92636da9de1e"
		publicInputRefs: [], secretInputRefs: [], planInputRefs: []
		outputs: ["platform/dokploy/runtime-adapter.json"]
		placement: {scope: "node-local", cardinality: "one-per-node"}
	}]
	realizationSupport: {
		contractVersion: "1.0.0", scope: "concrete", level: "generation-ready", compatibleRendererRefs: ["stackkit"]
		inputs: {contractComplete: true, requiredRefs: []}, planInputs: {contractComplete: true, requiredRefs: []}
		artifacts: {
			requiredRefs: ["dokploy-runtime-adapter"]
			outputBindings: [{artifactRef: "dokploy-runtime-adapter", unitRef: "dokploy-adapter", outputRef: "platform/dokploy/runtime-adapter.json"}]
			contracts: [{id: "dokploy-runtime-adapter", kind: "native-config", format: "json", mode: "0600", required: true, compatibleTargets: ["opentofu"], unitRef: "dokploy-adapter", outputRef: "platform/dokploy/runtime-adapter.json"}]
		}
		evidence: requiredRefs: ["dokploy-current-pin-lifecycle"]
	}
	health: [{id: "dokploy-runtime-contract", kind: "contract"}]
	evidence: ["dokploy-current-pin-lifecycle"]
}
