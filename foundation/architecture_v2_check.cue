// Package foundation - concrete compile checks for the additive architecture v2 spine.
package foundation

_accessAdminCheck: #AccessPolicyV2 & {
	privilege:      "admin"
	authentication: "human+device"
}
_assertAccessAdminDevice:   true & _accessAdminCheck.enrolledDeviceRequired
_assertAccessAdminStepUp:   true & _accessAdminCheck.ownerStepUpRequired
_assertAccessAdminLANBound: "local-only" & _accessAdminCheck.exposure
_assertAccessDefaultLANOnly: "local-only" & (#AccessPolicyV2 & {}).exposure

// An anonymous public route serves content only.
_accessAnonymousPublicCheck: #AccessPolicyV2 & {
	exposure:       "public"
	authentication: "none"
}
_assertAnonymousPublicUser: "user" & _accessAnonymousPublicCheck.privilege
_assertAnonymousNoDevice:   false & _accessAnonymousPublicCheck.enrolledDeviceRequired

// The Owner allows a service's third-party apps away from home: a person's
// own user route reachable through the remote listener with an app pass.
// Refused, as the negative fixtures app-access-away-* in
// internal/cue/testdata/architecture_v2_invalid prove: on a local-only route,
// with device proof, agent or no login, and on a sign-in or privileged route.
_accessAppAccessAwayCheck: #AccessPolicyV2 & {
	exposure:      "remote-private"
	appAccessAway: true
}
_assertAppAccessAwayUser:     "user" & _accessAppAccessAwayCheck.privilege
_assertAppAccessAwayHuman:    "human" & _accessAppAccessAwayCheck.authentication
_assertAppAccessAwayNoDevice: false & _accessAppAccessAwayCheck.enrolledDeviceRequired
_assertAppAccessAwayDefault:  false & (#AccessPolicyV2 & {}).appAccessAway

// The Owner's opt-in is the only way a LAN-bound privileged route leaves the
// LAN listener.
_accessVaultRemoteOptInCheck: #AccessPolicyV2 & {
	exposure:         "remote-private"
	privilege:        "vault"
	authentication:   "human+device"
	ownerRemoteOptIn: true
}
_assertVaultRemoteStepUp: true & _accessVaultRemoteOptInCheck.ownerStepUpRequired

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
	authoritySiteRef: "home"
}
_assertDeviceGeneratedKey:    true & _deviceEnrollmentCheck.requireDeviceGeneratedKey
_assertDevicePossession:      true & _deviceEnrollmentCheck.requirePossessionProof
_assertDeviceHomeAttestation: "authority-lan-listener" & _deviceEnrollmentCheck.homeAttestation
_assertDeviceAtHomeUndo:      86400 & _deviceEnrollmentCheck.atHome.undoWindowSeconds
_assertDeviceAwayConfirmers: ["own-enrolled-device", "owner"] & _deviceEnrollmentCheck.away.confirmers
_assertDeviceLifetime: 3600 & _deviceEnrollmentCheck.lifetimes.deviceLifetimeSeconds

_identityOriginKombifyMeCheck: #IdentityOriginV1 & {name: "smith.kombify.me"}
_assertIdentityOriginKombifyMe: {host: "id.smith.kombify.me", source: "kombify-me", certificates: "publicly-trusted"} & {
	host:         _identityOriginKombifyMeCheck.host
	source:       _identityOriginKombifyMeCheck.source
	certificates: _identityOriginKombifyMeCheck.certificates
}
_identityOriginLocalCheck: #IdentityOriginV1 & {name: "lab.home"}
_assertIdentityOriginLocal: "local-only" & _identityOriginLocalCheck.source

_assertVPNAddOnOffByDefault: false & (#VPNAddOnV1 & {tool: "wireguard"}).enabled

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
	runtimeDeliveries: ["application-adapter"]
	setup: {mode: "on-demand", owner: "module", actionRefs: ["immich-owner-bootstrap"]}
	settings: {allowedRefs: [], requiredRefs: []}
	secretInputs: {allowedRefs: ["database-password"], requiredRefs: ["database-password"]}
}, {
	id:   "photos"
	kind: "application"
	functionalCapabilities: ["photo-library", "mobile-photo-backup"]
	siteKinds: ["home", "cloud"]
	dataClasses: ["personal"]
	defaultAlternative: "immich"
	alternativeID:      "immich-lite"
	providerRef:        "stackkits-immich-lite"
	moduleRef:          "stackkits-immich-lite-runtime"
	serviceRef:         "photos"
	healthRef:          "immich-http"
	runtimeKinds: ["container"]
	runtimeDeliveries: ["application-adapter"]
	setup: {mode: "on-demand", owner: "module", actionRefs: ["immich-owner-bootstrap"]}
	settings: {allowedRefs: [], requiredRefs: []}
	secretInputs: {allowedRefs: ["database-password"], requiredRefs: ["database-password"]}
}]

_assertPhotosComputeTierAlternatives: [
	for workload in ArchitectureV2Catalog.workloads
	if workload.metadata.id == "photos" {
		low:      workload.computeTiers.low.alternativeID
		standard: workload.computeTiers.standard.alternativeID
		high:     workload.computeTiers.high.alternativeID
	},
] & [{
	low:      "immich-lite"
	standard: "immich"
	high:     "immich"
}]

_assertFilesComputeTierAlternatives: [
	for workload in ArchitectureV2Catalog.workloads
	if workload.metadata.id == "files" {
		low:      workload.computeTiers.low.alternativeID
		standard: workload.computeTiers.standard.alternativeID
		high:     workload.computeTiers.high.alternativeID
	},
] & [{
	low:      "cloudreve"
	standard: "cloudreve"
	high:     "cloudreve"
}]

_assertVaultComputeTierAlternatives: [
	for workload in ArchitectureV2Catalog.workloads
	if workload.metadata.id == "vault" {
		low:      workload.computeTiers.low.alternativeID
		standard: workload.computeTiers.standard.alternativeID
		high:     workload.computeTiers.high.alternativeID
	},
] & [{
	low:      "vaultwarden"
	standard: "vaultwarden"
	high:     "vaultwarden"
}]

_assertMediaComputeTierAlternatives: [
	for workload in ArchitectureV2Catalog.workloads
	if workload.metadata.id == "media" {
		lowIncluded: workload.computeTiers.low.included
		standard:    workload.computeTiers.standard.alternativeID
		high:        workload.computeTiers.high.alternativeID
	},
] & [{
	lowIncluded: false
	standard:    "jellyfin"
	high:        "jellyfin"
}]

_assertSmartHomeComputeTierAlternatives: [
	for workload in ArchitectureV2Catalog.workloads
	if workload.metadata.id == "smart-home" {
		low:      workload.computeTiers.low.alternativeID
		standard: workload.computeTiers.standard.alternativeID
		high:     workload.computeTiers.high.alternativeID
	},
] & [{
	low:      "home-assistant"
	standard: "home-assistant"
	high:     "home-assistant"
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
	realizationLevel: "apply-ready"
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
