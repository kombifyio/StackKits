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
		provider:                "wireguard"
		initiation:              "local-outbound"
		outboundEstablished:     true
		trafficMode:             "policy-scoped"
		advertiseDefaultRoute:   false
		advertisePrivateSubnets: false
		allowBroadRoutes:        false
		peerSiteRefs: ["home", "edge"]
	}
	policy: {
		defaultDeny:                    true
		cloudMayEnrollDevices:          false
		cloudMayIssueDeviceCredentials: false
	}
	controlAgent: {
		enabled:   true
		transport: "mtls-agent"
		audience:  "stackkit-runtime"
		actionAllowlist: ["plan", "apply", "verify"]
		maxTTLSeconds:                  60
		capabilityScopedActions:        true
		requiresSignedActions:          true
		requiresNonce:                  true
		requiresIdempotencyKey:         true
		requiresResolvedPlanHash:       true
		replayProtection:               true
		requiresApprovalForDestructive: true
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

_assertPhotosCapabilityContract: [
	for capability in ArchitectureV2Catalog.capabilities
	if capability.metadata.id == "photos" {
		id:          capability.metadata.id
		layer:       capability.metadata.layer
		siteKinds:   capability.supportedSiteKinds
		dataClasses: capability.dataClasses
		requirementIDs: [for requirement in capability.requires {requirement.id}]
	},
] & [{
	id:    "photos"
	layer: "application"
	siteKinds: ["home", "cloud"]
	dataClasses: ["personal"]
	requirementIDs: ["runtime-paas", "service-catalog", "storage-data-policy"]
}]

_assertImmichProviderContract: [
	for provider in ArchitectureV2Catalog.providers
	if provider.metadata.id == "stackkits-immich" {
		id:              provider.metadata.id
		provides:        provider.provides
		requiredModules: provider.realization.moduleRefs.required
	},
] & [{
	id: "stackkits-immich"
	provides: ["photos"]
	requiredModules: ["stackkits-immich-runtime"]
}]

_assertImmichServiceContract: [
	for module in ArchitectureV2Catalog.modules
	if module.metadata.id == "stackkits-immich-runtime"
	for unit in module.renderUnits
	for endpoint in unit.serviceEndpoints {
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
	providerRef: "stackkits-immich"
	authority:   "control-authority-site"
	requiredRoles: ["worker"]
	realizationLevel: "contract-only"
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
