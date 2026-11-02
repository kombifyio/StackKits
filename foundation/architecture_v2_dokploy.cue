package foundation

// Dokploy is a Layer-2 PaaS platform for Basement (ADR-0045). Selecting the
// dokploy adapter for a workload realizes both modules: the bootstrap installs
// Dokploy on this node without Swarm and without its own Traefik, creates its
// owner and API key in node custody, and publishes its UI only through the Kit
// router behind forward-auth; the adapter deploys the selected workloads
// through that node-local API. Native Dokploy SSO is licensed and not
// configured: PocketID authenticates at the Kit router and Dokploy keeps its
// local owner login.
_architectureV2DokployProviders: [{
	metadata: {id: "stackkits-dokploy", version: "1.0.0"}
	provides: []
	runtimeAdapterRefs: ["dokploy"]
	requires: [{id: "runtime-paas"}, {id: "service-catalog"}]
	supportedSiteKinds: ["home"]
	realization: {
		kind: "modules"
		moduleRefs: {
			required: ["stackkits-dokploy-bootstrap-runtime", "stackkits-dokploy-runtime"]
			optional: []
		}
	}
	health: [{id: "dokploy-runtime-contract", kind: "contract"}]
	evidence: ["dokploy-adapter-contract"]
}]

_architectureV2DokployModules: [{
	metadata: {
		id:          "stackkits-dokploy-bootstrap-runtime"
		version:     "1.0.0"
		description: "Node-local Dokploy and PostgreSQL bootstrap for an explicitly selected Basement Standard installation; no Swarm, no second ingress, owner API key in node custody."
	}
	role:        "platform"
	providerRef: "stackkits-dokploy"
	provides: []
	requires: ["stackkits-core-host-bootstrap", "stackkits-basement-core-lite-runtime"]
	supportedSiteKinds: ["home"]
	nodeSelection: {
		authority:           "control-authority-site"
		controlPlaneMembers: "only"
		requiredRoles: ["worker"]
	}
	// The UI route's service is this module's own control, which binds it to
	// the Dokploy runtime the way Core and the Marketplace bind theirs.
	serviceControls: [{key: "dokploy", serviceRef: "dokploy", adapter: "compose", runtimeRef: "dokploy", componentRefs: ["dokploy", "postgres"], allowedActions: ["logs", "restart", "start", "stop"], critical: false}]
	runtimeAdapterBootstrap: {
		adapterRef:        "dokploy"
		credentialCustody: "local-owner"
		providerLifecycle: "not-owned"
		evidenceRequired:  true
	}
	runtime: {
		execution:         "executable"
		kind:              "host"
		delivery:          "stackkit"
		entryComponentRef: "dokploy"
		components: [
			{
				id: "postgres", role: "database", lifecycle: "daemon"
				networkRefs: ["dokploy-control"]
				image: {ref: "docker.io/library/postgres:16-alpine", digest: "sha256:0cefaabd882e42bcb2a2f3dc56d3ce4770560ba62fd7f197f1a2e008fd5731ab"}
				health: {kind: "image"}
				resources: {memoryLimit: "512m"}
			},
			{
				id: "dokploy", role: "application", lifecycle: "daemon"
				networkRefs: ["dokploy-control", "basement-core"]
				image: {ref: "docker.io/dokploy/dokploy:v0.30.8", digest: "sha256:e8789a3f71a1e50d817836ad222ee0eab8ee69fc1b0451fee189a880b5602871"}
				dependsOn: ["postgres"]
				health: {kind: "http", path: "/api/health", port: 3000}
				// Dokploy 0.30 needs about 1.1 GiB at startup with its 1 GiB Node heap.
				resources: {memoryLimit: "1536m"}
			},
		]
	}
	renderUnits: [{
		id: "bootstrap", kind: "native-config", rendererRef: "stackkit"
		compatibleTargets: ["compose", "opentofu"]
		templateRef:  "builtin://platform/dokploy/bootstrap/v1.json"
		version:      "1.0.0"
		contractHash: "sha256:000c76b558747cdaf1a58248f9c7dc73363f70a269e962cba80454d24aecfa84"
		publicInputRefs: ["delivery-route"], secretInputRefs: [], planInputRefs: ["kit"]
		inputBindings: [{targetRef: "delivery-route", sourceRef: "network.moduleRoute", valueType: "authority-bound-module-route-v1", cardinality: "single", required: false, defaultValue: null}]
		outputs: ["platform/dokploy/bootstrap.json"]
		placement: {scope: "node-local", cardinality: "one-per-node"}
		// The UI route is authored with the first Dokploy workload. TinyAuth admits
		// only declared hosts; household members pass it but hold no Dokploy account.
		serviceEndpoints: [{serviceRef: "dokploy", upstreamProtocol: "http", targetPort: 3000, requiredPrivilege: "user", ingressAuth: "forward-auth", allowedIngressProtocols: ["https"], allowedExposures: ["local", "remote-private"], originSelector: "control-authority-site", healthRef: "dokploy-ui-ready", data: {bindingRef: "dokploy", requiredClasses: ["secret"], locality: "primary-site"}}]
	}]
	realizationSupport: {
		contractVersion: "1.0.0"
		scope:           "concrete"
		level:           "apply-ready"
		compatibleRendererRefs: ["stackkit"]
		inputs: {contractComplete: true, requiredRefs: []}
		planInputs: {contractComplete: true, requiredRefs: ["kit"]}
		artifacts: {
			requiredRefs: ["dokploy-bootstrap"]
			outputBindings: [{artifactRef: "dokploy-bootstrap", unitRef: "bootstrap", outputRef: "platform/dokploy/bootstrap.json"}]
			contracts: [{
				id: "dokploy-bootstrap", kind: "native-config", format: "json", mode: "0600", required: true
				compatibleTargets: ["compose", "opentofu"]
				unitRef: "bootstrap", outputRef: "platform/dokploy/bootstrap.json"
			}]
		}
		evidence: requiredRefs: ["dokploy-native-bootstrap"]
	}
	health: [{id: "dokploy-ui-ready", kind: "http", phase: "continuous", path: "/api/health", port: 3000, timeoutSeconds: 10, expectedStatuses: [200]}]
	evidence: ["dokploy-native-bootstrap"]
}, {
	metadata: {
		id:          "stackkits-dokploy-runtime"
		version:     "1.0.0"
		description: "Workload-scoped Dokploy Compose API adapter over the node's own bootstrapped Dokploy; owner API key in node custody, server-provider lifecycle external."
	}
	role:        "platform"
	providerRef: "stackkits-dokploy"
	provides: []
	requires: ["stackkits-dokploy-bootstrap-runtime"]
	supportedSiteKinds: ["home"]
	nodeSelection: {
		authority:           "control-authority-site"
		controlPlaneMembers: "only"
		requiredRoles: ["worker"]
	}
	runtimeAdapter: {
		id: "dokploy"
		supportedKinds: ["container"]
		supportedDeliveries: ["application-adapter"]
		operations: ["apply", "observe", "remove"]
		capabilities: ["application-setup-local-api"]
		credentialCustody: "local-owner"
		providerLifecycle: "not-owned"
		evidenceRequired:  true
	}
	runtime: {execution: "contract-handoff", kind: "control-plane", delivery: "external-control-plane"}
	renderUnits: [{
		id: "dokploy-adapter", kind: "native-config", rendererRef: "stackkit"
		compatibleTargets: ["compose", "opentofu"]
		templateRef:  "builtin://platform/dokploy/runtime-adapter/v1.json"
		version:      "1.0.0"
		contractHash: "sha256:c1368eba64ddd4b19ec9de15224be5035ab20a82907e6826c120283c6d73ff2d"
		publicInputRefs: [], secretInputRefs: [], planInputRefs: []
		outputs: ["platform/dokploy/runtime-adapter.json"]
		placement: {scope: "node-local", cardinality: "one-per-node"}
	}]
	renderVariants: [
		{
			id:           "compose", target: "compose", rendererRef: "stackkit"
			contractHash: "sha256:12bc280a7042488f8b15d62ba91b9a08367aca9806a22230bc96b01e1e494607"
			unitRefs: ["dokploy-adapter"], artifactRefs: ["dokploy-runtime-adapter"]
			publicInputRefs: [], secretInputRefs: [], planInputRefs: []
		},
		{
			id:           "opentofu", target: "opentofu", rendererRef: "stackkit"
			contractHash: "sha256:81700878de92b06985c45f4173531717b68cebbdc6da4fe93bee6f0b33866f9d"
			unitRefs: ["dokploy-adapter"], artifactRefs: ["dokploy-runtime-adapter"]
			publicInputRefs: [], secretInputRefs: [], planInputRefs: []
		},
	]
	realizationSupport: {
		contractVersion: "1.0.0"
		scope:           "concrete"
		level:           "generation-ready"
		compatibleRendererRefs: ["stackkit"]
		inputs: {contractComplete: true, requiredRefs: []}
		artifacts: {
			requiredRefs: ["dokploy-runtime-adapter"]
			outputBindings: [{artifactRef: "dokploy-runtime-adapter", unitRef: "dokploy-adapter", outputRef: "platform/dokploy/runtime-adapter.json"}]
			contracts: [{
				id: "dokploy-runtime-adapter", kind: "native-config", format: "json", mode: "0640", required: true
				compatibleTargets: ["compose", "opentofu"]
				unitRef: "dokploy-adapter", outputRef: "platform/dokploy/runtime-adapter.json"
			}]
		}
		evidence: requiredRefs: []
	}
	health: [{id: "dokploy-runtime-contract", kind: "contract", scope: "each-node"}]
	evidence: ["dokploy-adapter-contract"]
}]
