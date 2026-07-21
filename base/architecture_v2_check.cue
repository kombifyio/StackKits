// Package base - concrete compile checks for the additive architecture v2 spine.
package base

_accessAdminCheck: #AccessPolicyV2 & {
	exposure:       "lan"
	privilege:      "admin"
	authentication: "human+device"
}
_assertAccessAdminDevice: true & _accessAdminCheck.enrolledDeviceRequired
_assertAccessAdminStepUp: true & _accessAdminCheck.ownerStepUpRequired

_quorumCheck: #ControlPlaneIntent & {
	mode:             "quorum"
	authoritySiteRef: "home"
	members: ["controller-a", "controller-b", "controller-c"]
}
_assertQuorumCount: 3 & _quorumCheck._memberCount

_bridgeCheck: #BridgeContract & {
	overlay: {
		contractRef: "outbound-private-mesh"
		trafficMode: "policy-scoped"
		peerSiteRefs: ["home", "edge"]
	}
	policy: {
		defaultDeny:                    true
		cloudMayEnrollDevices:          false
		cloudMayIssueDeviceCredentials: false
	}
	controlAgent: {
		enabled: true
		actionAllowlist: ["plan", "apply", "verify"]
	}
}
_assertBridgeDefaultDeny:  true & _bridgeCheck.policy.defaultDeny
_assertBridgeNoLANTransit: false & _bridgeCheck.policy.allowRFC1918Transit

_deviceEnrollmentCheck: #DeviceEnrollmentPolicy & {
	mode:                      "local-only"
	authoritySiteRef:          "home"
	endpointExposure:          "lan"
	remoteEnrollment:          false
	requireOwnerStepUp:        true
	requireLocalPairingProof:  true
	requireDeviceGeneratedKey: true
	requirePossessionProof:    true
	hardwareBackedKey:         "preferred"
	revocationSupported:       true
	credentialTTLSeconds:      3600
}
_assertDeviceGeneratedKey: true & _deviceEnrollmentCheck.requireDeviceGeneratedKey
_assertDevicePossession:   true & _deviceEnrollmentCheck.requirePossessionProof

_assertPhotosCapabilityRetired: [for capability in ArchitectureV2Catalog.capabilities if capability.metadata.id == "photos" {capability.metadata.id}] & []

_assertPhotosWorkloadContract: [
	for workload in ArchitectureV2Catalog.workloads
	if workload.metadata.id == "photos"
	for alternative in workload.alternatives {
		id:                     workload.metadata.id
		kind:                   workload.kind
		functionalCapabilities: workload.functionalCapabilities
		siteKinds:              workload.supportedSiteKinds
		dataClasses:            workload.dataClasses
		defaultAlternative:     workload.defaultAlternative
		alternativeID:          alternative.id
		providerRef:            alternative.providerRef
		moduleRef:              alternative.moduleRef
		serviceRef:             alternative.route.serviceRef
		healthRef:              alternative.route.healthRef
		runtimeKinds:           alternative.runtime.allowedKinds
		runtimeDeliveries:      alternative.runtime.allowedDeliveries
		setup:                  alternative.setup
		settings:               alternative.inputs.settings
		secretInputs:           alternative.inputs.secretInputs
	},
] & [{
	id:   "photos"
	kind: "application"
	functionalCapabilities: ["photo-library", "mobile-photo-backup"]
	siteKinds: ["home", "cloud"]
	dataClasses: ["personal"]
	defaultAlternative: "immich"
	alternativeID:      "immich"
	providerRef:        "stackkits-immich"
	moduleRef:          "stackkits-immich-runtime"
	serviceRef:         "photos"
	healthRef:          "immich-http"
	runtimeKinds: ["container"]
	runtimeDeliveries: ["selected-paas"]
	setup: {mode: "manual", owner: "operator", actionRefs: []}
	settings: {allowedRefs: [], requiredRefs: []}
	secretInputs: {allowedRefs: ["database-password"], requiredRefs: ["database-password"]}
}]

_assertImmichProviderContract: [
	for provider in ArchitectureV2Catalog.providers
	if provider.metadata.id == "stackkits-immich" {
		id:              provider.metadata.id
		provides:        provider.provides
		workloadRefs:    provider.workloadRefs
		requiredModules: provider.realization.moduleRefs.required
		optionalModules: provider.realization.moduleRefs.optional
	},
] & [{
	id: "stackkits-immich"
	provides: []
	workloadRefs: ["photos"]
	requiredModules: []
	optionalModules: ["stackkits-immich-runtime"]
}]

_assertImmichServiceContract: [
	for module in ArchitectureV2Catalog.modules
	if module.metadata.id == "stackkits-immich-runtime"
	for unit in module.renderUnits
	for endpoint in unit.serviceEndpoints {
		role:                module.role
		provides:            module.provides
		providerRef:         module.providerRef
		authority:           module.nodeSelection.authority
		requiredRoles:       module.nodeSelection.requiredRoles
		realizationLevel:    module.realizationSupport.level
		unitID:              unit.id
		placement:           unit.placement
		serviceRef:          endpoint.serviceRef
		upstreamProtocol:    endpoint.upstreamProtocol
		targetPort:          endpoint.targetPort
		originSelector:      endpoint.originSelector
		healthRef:           endpoint.healthRef
		dataBindingRef:      endpoint.data.bindingRef
		requiredDataClasses: endpoint.data.requiredClasses
		dataLocality:        endpoint.data.locality
	},
] & [{
	role: "workload"
	provides: []
	providerRef: "stackkits-immich"
	authority:   "control-authority-site"
	requiredRoles: ["worker"]
	realizationLevel: "generation-ready"
	unitID:           "immich-server"
	placement: {
		scope:       "node-local"
		cardinality: "one-per-node"
	}
	serviceRef:       "photos"
	upstreamProtocol: "http"
	targetPort:       2283
	originSelector:   "control-authority-site"
	healthRef:        "immich-http"
	dataBindingRef:   "photos"
	requiredDataClasses: ["personal"]
	dataLocality: "primary-site"
}]
