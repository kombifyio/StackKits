package foundation

// External application authority names the product instance contract, never
// the service endpoint, provider guest, token or transport. The authenticated
// runtime owner binds those through the exact sealed RuntimeTarget.
#ExternalApplicationInstanceV1: {
	platform:            "home-assistant"
	installationMethod:  "haos" | "unknown"
	instanceOrigin:      "new" | "existing" | "imported"
	managementScope:     "observed" | "managed"
	configurationPolicy: "preserve-user-changes"
	dataCustody:         "external-instance-owner"
	baselinePolicy:      "fresh-only" | "preserve"
	if instanceOrigin == "new" {
		installationMethod: "haos"
		managementScope:    "managed"
		baselinePolicy:     "fresh-only"
		baselineVersion:    "1"
	}
	if instanceOrigin != "new" {
		baselinePolicy:   "preserve"
		baselineVersion?: _|_
	}
	if instanceOrigin == "existing" {managementScope: "observed"}
	if instanceOrigin == "imported" {
		installationMethod: "haos"
		managementScope:    "managed"
	}
}

_architectureV2HomeAssistantInstances: [
	{alternative: "home-assistant-haos", module: "stackkits-home-assistant-haos-runtime", origin: "new", method: "haos", scope: "managed", baseline: "fresh-only"},
	{alternative: "home-assistant-existing", module: "stackkits-home-assistant-existing-runtime", origin: "existing", method: "unknown", scope: "observed", baseline: "preserve"},
	{alternative: "home-assistant-imported", module: "stackkits-home-assistant-imported-runtime", origin: "imported", method: "haos", scope: "managed", baseline: "preserve"},
]

_architectureV2HomeAssistantInstanceAlternatives: [for instance in _architectureV2HomeAssistantInstances {
	id:          instance.alternative
	providerRef: "stackkits-home-assistant-appliance"
	moduleRef:   instance.module
	// This is a logical service identity. The external module publishes no
	// StackKits serviceEndpoint and therefore no local reverse-proxy origin.
	route: {serviceRef: "smart-home", healthRef: "home-assistant-instance"}
	runtime: {
		allowedKinds: ["external"]
		allowedDeliveries: ["external-control-plane"]
		allowedAdapterRefs: []
		defaultFallbackAdapterRefs: []
	}
	setup: {mode: "manual", owner: "operator", actionRefs: []}
	inputs: {
		settings: {allowedRefs: [], requiredRefs: []}
		secretInputs: {allowedRefs: [], requiredRefs: []}
	}
}]

_architectureV2HomeAssistantInstanceProviders: [{
	metadata: {id: "stackkits-home-assistant-appliance", version: "1.0.0"}
	provides: []
	workloadRefs: ["smart-home"]
	requires: []
	supportedSiteKinds: ["home", "cloud"]
	realization: {kind: "modules", moduleRefs: {
		required: []
		optional: [for instance in _architectureV2HomeAssistantInstances {instance.module}]
	}}
	evidence: ["home-assistant-instance-evidence"]
}]

_architectureV2HomeAssistantInstanceModules: [for instance in _architectureV2HomeAssistantInstances {
	metadata: {id: instance.module, version: "1.0.0", description: "Externally bound Home Assistant instance; API authority, endpoint, VM and credentials remain with the admitted runtime owner."}
	role:        "workload"
	providerRef: "stackkits-home-assistant-appliance"
	provides: []
	supportedSiteKinds: ["home", "cloud"]
	nodeSelection: {authority: "control-authority-site", requiredRoles: ["worker"]}
	computeProfiles: {
		standard: {description: "External Home Assistant API owner; guest capacity is admitted by its provisioning owner, not reserved on the StackKits core host.", maturity: "beta", executable: true, realization: "apply-ready"}
	}
	defaultComputeProfile: "standard"
	runtime: {
		execution: "executable"
		kind:      "external"
		delivery:  "external-control-plane"
		engine:    "api"
		settings: #ExternalApplicationInstanceV1 & {
			platform:            "home-assistant"
			installationMethod:  instance.method
			instanceOrigin:      instance.origin
			managementScope:     instance.scope
			configurationPolicy: "preserve-user-changes"
			dataCustody:         "external-instance-owner"
			baselinePolicy:      instance.baseline
		}
	}
	renderUnits: [{
		id:           "instance"
		kind:         "native-config"
		rendererRef:  "stackkit"
		templateRef:  "builtin://workloads/home-assistant/instance/v1.json"
		version:      "1.0.0"
		contractHash: "sha256:c09bb12b007fb88863f5f3bc4001de7f4a30f5125c657aceffe811c698a4d6b5"
		publicInputRefs: []
		secretInputRefs: []
		outputs: ["workloads/\(instance.alternative)/instance.json"]
		placement: {scope: "node-local", cardinality: "one-per-node"}
	}]
	realizationSupport: {
		contractVersion: "1.0.0"
		scope:           "concrete"
		level:           "apply-ready"
		compatibleRendererRefs: ["stackkit"]
		inputs: {contractComplete: true, requiredRefs: []}
		artifacts: {
			requiredRefs: ["\(instance.module)-instance"]
			outputBindings: [{artifactRef: "\(instance.module)-instance", unitRef: "instance", outputRef: "workloads/\(instance.alternative)/instance.json"}]
			contracts: [{id: "\(instance.module)-instance", kind: "native-config", format: "json", mode: "0640", required: true, compatibleTargets: ["compose", "opentofu", "terramate"], unitRef: "instance", outputRef: "workloads/\(instance.alternative)/instance.json"}]
		}
		evidence: requiredRefs: ["home-assistant-instance-evidence"]
	}
	health: [{id: "home-assistant-instance", phase: "post-apply", kind: "contract", scope: "each-node"}]
	evidence: ["home-assistant-instance-evidence"]
}]
