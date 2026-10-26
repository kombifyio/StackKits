package foundation

// The selected Komodo adapter owns operations on this node. The existing
// adapter/agent handoffs retain workload identity; this unit realizes their
// private API dependency without acquiring a server-provider lifecycle.
_architectureV2KomodoBootstrapModules: [{
	metadata: {
		id:          "stackkits-komodo-bootstrap-runtime"
		version:     "1.0.0"
		description: "Node-local Komodo Core and outbound Periphery bootstrap for an explicitly selected Basement Standard installation."
	}
	role:        "platform"
	providerRef: "stackkits-komodo"
	provides: []
	requires: ["stackkits-core-host-bootstrap", "stackkits-basement-core-lite-runtime"]
	supportedSiteKinds: ["home"]
	nodeSelection: {
		authority:           "control-authority-site"
		controlPlaneMembers: "only"
		requiredRoles: ["worker"]
	}
	runtimeAdapterBootstrap: {
		adapterRef: "komodo"
		credentialCustody: "local-owner"
		providerLifecycle: "not-owned"
		evidenceRequired: true
	}
	runtime: {
		execution:         "executable"
		kind:              "host"
		delivery:          "stackkit"
		entryComponentRef: "core"
		components: [
			{
				id: "mongo", role: "database", lifecycle: "daemon"
				networkRefs: ["komodo-control"]
				image: {ref: "mongo:7", digest: "sha256:1f995ad6fdb93244a1addab1b58f934a0bc2f5643c38e02f5e9d7f0c7d227a7b"}
				health: {kind: "image"}
				resources: {memoryLimit: "512m"}
			},
			{
				id: "core", role: "application", lifecycle: "daemon"
				networkRefs: ["komodo-control", "basement-core"]
				image: {ref: "ghcr.io/moghtech/komodo-core:2.3.3", digest: "sha256:bca73d0eee143066228fb9b80c38a5a2726e573ad2758f7aa92b5c91a8aef1c7"}
				dependsOn: ["mongo"]
				health: {kind: "http", path: "/version", port: 9120}
				resources: {memoryLimit: "512m"}
			},
			{
				id: "periphery", role: "application", lifecycle: "daemon"
				networkRefs: ["basement-core"]
				image: {ref: "ghcr.io/moghtech/komodo-periphery:2.3.3", digest: "sha256:fa3f1a641a265216066676950d5eecdf506955a7bafe2f01996135ae93115cd5"}
				dependsOn: ["core"]
				health: {kind: "image"}
				resources: {memoryLimit: "256m"}
			},
		]
	}
	renderUnits: [{
		id: "bootstrap", kind: "native-config", rendererRef: "stackkit"
		// OpenTofu workloads still need the retained native bootstrap owner to
		// install Core/Periphery and establish private API/node custody first.
		// This recipe grants no provider resource or host lifecycle authority.
		compatibleTargets: ["compose", "opentofu"]
		templateRef:  "builtin://platform/komodo/bootstrap/v1.json"
		version:      "1.0.0"
		contractHash: "sha256:fb498163b0aae017eb15961d0096cab5b7b2ef20edc61dfeb995d44f53e849c6"
		publicInputRefs: [], secretInputRefs: [], planInputRefs: ["kit"]
		outputs: ["platform/komodo/bootstrap.json"]
		placement: {scope: "node-local", cardinality: "one-per-node"}
	}]
	realizationSupport: {
		contractVersion: "1.0.0"
		scope:           "concrete"
		level:           "apply-ready"
		compatibleRendererRefs: ["stackkit"]
		inputs: {contractComplete: true, requiredRefs: []}
		planInputs: {contractComplete: true, requiredRefs: ["kit"]}
		artifacts: {
			requiredRefs: ["komodo-bootstrap"]
			outputBindings: [{artifactRef: "komodo-bootstrap", unitRef: "bootstrap", outputRef: "platform/komodo/bootstrap.json"}]
			contracts: [{
				id: "komodo-bootstrap", kind: "native-config", format: "json", mode: "0600", required: true
				compatibleTargets: ["compose", "opentofu"]
				unitRef: "bootstrap", outputRef: "platform/komodo/bootstrap.json"
			}]
		}
		evidence: requiredRefs: ["komodo-native-bootstrap"]
	}
	health: [{id: "komodo-bootstrap-ready", kind: "contract", scope: "each-node"}]
	evidence: ["komodo-native-bootstrap"]
}]
