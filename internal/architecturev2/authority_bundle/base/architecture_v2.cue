// Package base - additive v2 architecture contracts for typed StackKit planning.
//
// These contracts deliberately do not replace #StackBase yet. They define the
// next canonical input and resolved-plan boundary while the existing rollout
// renderer continues to consume the v1 compatibility projection.
package base

import (
	"list"
	"path"
	"strings"
	"struct"
)

#ArchitectureAPIVersion: "stackkit/v2alpha1"

#ContractID:   string & =~"^[a-z][a-z0-9-]*$"
#CapabilityID: #ContractID
#SiteID:       #ContractID
#NodeID:       #ContractID

#SemanticVersion: string & =~"^v?[0-9]+\\.[0-9]+\\.[0-9]+(-[0-9A-Za-z.-]+)?$"
#ContentHash:     string & =~"^sha256:[a-f0-9]{64}$"
#SecretReference: string & =~"^(secret|vault|doppler|techstack)://[^[:space:]]+$"
#AbsolutePath:    string & =~"^/[^[:cntrl:]]*$"

// #UnixSocketPath is a canonical absolute Unix-domain socket path. The
// portable ASCII segment set keeps the path deterministic across authority,
// resolver, renderer, and OpenAPI consumers. Empty, dot, dot-dot, repeated,
// backslash, whitespace, control-character, and trailing-slash segments are
// rejected; the concrete target must end in .sock. Linux sockaddr_un.sun_path
// has 108 bytes including its terminating NUL, so the persisted UTF-8 path is
// capped at 107 bytes. Because this contract is ASCII-only, MaxRunes(107) is
// exactly the same byte bound rather than a weaker Unicode-codepoint bound.
#UnixSocketPath: string &
	=~"^/[A-Za-z0-9._-]+(/[A-Za-z0-9._-]+)*\\.sock$" &
	!~"(^|/)\\.\\.?(/|$)" &
	strings.MaxRunes(107)

// #ArtifactPath is a canonical, cross-platform relative materialization path.
// It deliberately uses the portable ASCII subset accepted by every supported
// host and rejects Windows device names even when they carry an extension.
#ArtifactPath: string &
	=~"^[A-Za-z0-9.][A-Za-z0-9._-]*(/[A-Za-z0-9._-]+)*$" &
	!~"(^|/)[^/]*\\.(/|$)" &
	!~"(?i)(^|/)(con|prn|aux|nul|com[1-9]|lpt[1-9])(\\.[^/]*)?(/|$)"

// A dot output root is a supported execution mode, but dot is not a valid
// artifact path or module-owned output.
#OutputRootPath: "." | (#ArtifactPath & !~"(?i)^\\.stackkit(/|$)")

// The generator owns the top-level .stackkit control namespace. Modules may
// neither declare nor bind any path below it as an ordinary output.
#ModuleArtifactOutputPathV2: #ArtifactPath & !~"(?i)^\\.stackkit(/|$)"

// #ArtifactPathSetClosureV2 proves both case-insensitive identity and the
// file-tree invariant that no artifact path can also be another artifact's
// ancestor. Without the latter, one host would have to materialize the same
// path as both a file and a directory.
#ArtifactPathSetClosureV2: {
	paths: [...#ArtifactPath]
	_portableIdentityUnique: list.UniqueItems([for artifactPath in paths {
		strings.ToLower(artifactPath)
	}]) & true
	_fileAncestorConflicts: [
		for filePath in paths
		for descendantPath in paths
		if strings.HasPrefix(strings.ToLower(descendantPath), strings.ToLower(filePath)+"/") {
			file:       filePath
			descendant: descendantPath
		},
	] & list.MaxItems(0)
}

// #PublicSettings is the only free-form value surface allowed in a persisted
// ResolvedPlan. Secret-like keys are rejected recursively; deployment-specific
// secret material must use an explicit #SecretReference map instead.
#PlanScalar: null | bool | number | string
#PlanValue: #PlanScalar | [...#PlanValue] | #PublicSettings
#PublicSettings: {
	[string]:                                                                #PlanValue
	[=~"(?i).*(password|passwd|secret|token|private[-_]?key|credential).*"]: _|_
}

#SiteKind: "home" | "cloud"

#ControlPlaneMode: "single" | "warm-standby" | "quorum"

#NodeRoleV2:              "controller" | "worker" | "storage" | "edge"
#RuntimeVirtualizationV2: "bare-metal" | "kvm" | "openvz" | "lxc" | "vmware" | "hyperv" | "xen" | "oracle" | "microsoft" | "none"

#CommonCapabilityIDs: [
	"topology-core",
	"host-bootstrap",
	"security-baseline",
	"human-identity-core",
	"device-trust-core",
	"access-policy",
	"runtime-paas",
	"service-catalog",
	"secrets-recovery",
	"storage-data-policy",
	"backup-core",
	"observability-evidence",
	"lifecycle-update",
]

// #KitDefinition declares the immutable architecture profile selected by a
// StackSpec. Runtime detection may validate this choice but must never change
// the selected kit or its allowed site kinds.
#KitDefinition: {
	apiVersion: #ArchitectureAPIVersion
	kind:       "KitDefinition"

	metadata: {
		slug:        #KitSlug
		version:     string & =~"^[0-9]+\\.[0-9]+\\.[0-9]+(-[a-z0-9.]+)?$"
		displayName: string
		description: string
	}

	topology: {
		allowedSiteKinds: [...#SiteKind] & list.MinItems(1)
		requiredSiteKinds: [...#SiteKind] & list.MinItems(1)
		minSites:  int & >=1
		maxSites:  int & >=minSites
		multiNode: true

		controlPlane: {
			defaultMode: #ControlPlaneMode | *"single"
			allowedModes: [...#ControlPlaneMode] & list.MinItems(1)
			allowedAuthorityKinds: [...#SiteKind] & list.MinItems(1)
			// authority-site prevents a federated edge from being mistaken for a
			// control-plane HA member. It constrains membership, not worker or edge
			// placement.
			memberSiteScope: *"any" | "authority-site"
		}

		_allowedUnique:   list.UniqueItems(allowedSiteKinds) & true
		_requiredUnique:  list.UniqueItems(requiredSiteKinds) & true
		_modeUnique:      list.UniqueItems(controlPlane.allowedModes) & true
		_authorityUnique: list.UniqueItems(controlPlane.allowedAuthorityKinds) & true

		_requiredAllowed: [for required in requiredSiteKinds {
			kind: required
			matches: [for allowed in allowedSiteKinds if allowed == required {allowed}] & list.MinItems(1)
		}]
		_authorityAllowed: [for authority in controlPlane.allowedAuthorityKinds {
			kind: authority
			matches: [for allowed in allowedSiteKinds if allowed == authority {allowed}] & list.MinItems(1)
		}]
	}

	capabilities: {
		required: [...#CapabilityID]
		defaults: [...#CapabilityID] | *[]
		optional: [...#CapabilityID] | *[]
		forbidden: [...#CapabilityID] | *[]

		_requiredUnique:  list.UniqueItems(required) & true
		_defaultsUnique:  list.UniqueItems(defaults) & true
		_optionalUnique:  list.UniqueItems(optional) & true
		_forbiddenUnique: list.UniqueItems(forbidden) & true

		_ambiguous: [
			for left in list.Concat([required, defaults, optional])
			for denied in forbidden
			if left == denied {left},
		] & list.MaxItems(0)
	}

	accessDefaults:   #KitAccessDefaults
	reachability:     #KitReachabilityContractV2
	dataDefaults:     #KitDataDefaults
	failureDefaults:  #KitFailureDefaults
	deviceEnrollment: #KitDeviceEnrollment
	partitionPolicy:  #PartitionPolicy
	generation:       #KitGenerationContract
	network:          #KitNetworkContract
	availability:     #KitAvailabilityContractV2

	bridge: {
		required: bool | *false
		sourceKinds: [...#SiteKind] | *[]
		edgeKinds: [...#SiteKind] | *[]

		_sourceKindsUnique: list.UniqueItems(sourceKinds) & true
		_edgeKindsUnique:   list.UniqueItems(edgeKinds) & true
	}

	_reachabilityRules: [
		reachability.routes.local,
		reachability.routes["remote-private"],
		reachability.routes.public,
	]
	_reachabilityCapabilityChecks: [
		for rule in _reachabilityRules
		for requiredCapability in rule.requiredCapabilities {
			capability: requiredCapability
			declared: [
				for declared in list.Concat([capabilities.required, capabilities.defaults, capabilities.optional])
				if requiredCapability == declared {declared},
			] & list.MinItems(1)
			forbidden: [
				for denied in capabilities.forbidden
				if requiredCapability == denied {denied},
			] & list.MaxItems(0)
		},
	]
	_reachabilityOriginKindChecks: [
		for rule in _reachabilityRules
		for originKind in rule.allowedOriginKinds {
			kind: originKind
			matches: [
				for allowed in topology.allowedSiteKinds
				if originKind == allowed {allowed},
			] & list.MinItems(1)
		},
	]

	evidenceScenarios: [...string] & list.MinItems(1)
}

#GenerationStrategy: "kit-template" | "module-fragments"
#GenerationTarget:   "opentofu" | "compose" | "native"
#NetworkModeV2:      "private" | "public-capable" | "hybrid"

// #KitGenerationContract is definition-owned. Its values are covered by the
// KitDefinition hash, so a renderer cannot silently select another template or
// output family after resolution.
#KitGenerationContract: {
	defaultStrategy: #GenerationStrategy | *"kit-template"
	allowedStrategies: [...#GenerationStrategy] & list.MinItems(1) | *["kit-template", "module-fragments"]
	defaultTarget: #GenerationTarget | *"opentofu"
	allowedTargets: [...#GenerationTarget] & list.MinItems(1) | *["opentofu"]
	contractVersion: #SemanticVersion | *"1.0.0"

	_strategyUnique: list.UniqueItems(allowedStrategies) & true
	_targetUnique:   list.UniqueItems(allowedTargets) & true
	_defaultStrategyAllowed: [for strategy in allowedStrategies if strategy == defaultStrategy {strategy}] & list.MinItems(1)
	_defaultTargetAllowed: [for target in allowedTargets if target == defaultTarget {target}] & list.MinItems(1)
}

#KitNetworkContract: {
	mode:           #NetworkModeV2
	domainRequired: bool
	defaultDomain?: string & =~"^[A-Za-z0-9][A-Za-z0-9.-]*[A-Za-z0-9]$"
	defaultTLSMode: "off" | "internal" | "public"
}

#KitAccessDefaults: {
	publicRoutesDefaultClosed: true
	lanLocationIsIdentity:     false
	privilegedStepUpRequired:  true
	deviceEnrollment:          "local-only" | "remote-bootstrap" | "none"
	deviceBoundSessions:       true
}

#AccessPolicyExposureV2: "private" | "lan" | "public"

// #KitRouteExposureRuleV2 makes route reachability a definition-owned
// capability contract. A route is never made reachable merely because its
// intent asks for an exposure mode.
#KitRouteExposureRuleV2: {
	allowed: bool
	requiredCapabilities: [...#CapabilityID] | *[]
	allowedOriginKinds: [...#SiteKind] | *[]

	_requiredCapabilitiesUnique: list.UniqueItems(requiredCapabilities) & true
	_allowedOriginKindsUnique:   list.UniqueItems(allowedOriginKinds) & true

	if allowed == true {
		allowedOriginKinds: list.MinItems(1)
	}
	if allowed == false {
		requiredCapabilities: []
		allowedOriginKinds: []
	}
}

// #KitReachabilityContractV2 separates kit identity from runtime context. It
// declares which access policies and route exposure modes a selected kit may
// resolve, and which selected capabilities must authorize each route.
#KitReachabilityContractV2: {
	accessPolicies: {
		allowedExposures: [...#AccessPolicyExposureV2] & list.MinItems(1)
		lanStepDownAllowed: bool

		_allowedExposuresUnique: list.UniqueItems(allowedExposures) & true
		if lanStepDownAllowed == true {
			_lanExposureDeclared: [
				for exposure in allowedExposures
				if exposure == "lan" {exposure},
			] & list.MinItems(1)
		}
	}
	routes: {
		local:            #KitRouteExposureRuleV2
		"remote-private": #KitRouteExposureRuleV2
		public:           #KitRouteExposureRuleV2
	}
}

#KitDeviceEnrollment: {
	required: bool
	mode:     "local-only" | "disabled"
	authorityKinds: [...#SiteKind] | *[]

	if required == true {
		mode:           "local-only"
		authorityKinds: list.MinItems(1)
	}
	if required == false {
		mode: "disabled"
		authorityKinds: []
	}
}

#KitDataDefaults: {
	authority:               "home" | "cloud" | "policy"
	cloudCopyRequiresPolicy: bool | *true
}

#KitFailureDefaults: {
	offlineOperation:              bool
	localServicesSurviveCloudLoss: bool
	cloudEdgeFailsClosed:          bool | *true
}

// #DeviceEnrollmentPolicy is separate from human authentication. A trusted
// network can make enrollment reachable, but cannot itself establish identity.
#DeviceEnrollmentPolicy: {
	mode:                      "local-only"
	authoritySiteRef:          #SiteID
	endpointExposure:          "lan"
	remoteEnrollment:          false
	requireOwnerStepUp:        true
	requireLocalPairingProof:  true
	requireDeviceGeneratedKey: true
	requirePossessionProof:    true
	hardwareBackedKey:         "preferred" | "required"
	revocationSupported:       true
	credentialTTLSeconds:      int & >=300 & <=86400
}

#PartitionPolicy: {
	onCloudLoss:                     "local-continues" | "fail-closed" | "not-applicable"
	onLinkLoss:                      "local-continues" | "cloud-continues" | "fail-closed" | "not-applicable"
	cloudEdge:                       "fail-closed" | "not-applicable"
	localIdentityAuthorityAvailable: bool
	maxStaleVerificationSeconds:     int & >=0
	denyNewCrossSiteSessions:        true
}

#SiteSpec: {
	id:   #SiteID
	kind: #SiteKind

	name?:         string
	failureDomain: string & =~"^.+$"

	provider?: {
		name:        #ContractID
		region?:     string
		accountRef?: string & =~"^(secret|vault|doppler|techstack)://.+$"
	}

	network?: {
		managementCIDRs?: [...string]
		serviceCIDRs?: [...string]
		storageCIDRs?: [...string]
	}

	labels?: [string]: string
}

#NodeSpecV2: {
	id:      #NodeID
	siteRef: #SiteID
	roles: [...#NodeRoleV2] & list.MinItems(1)

	_roleUnique: list.UniqueItems(roles) & true

	management: {
		transport:      *"ssh" | "local-agent" | "managed-agent"
		host:           string & =~"^.+$"
		port?:          int & >=1 & <=65535
		user?:          string
		credentialRef?: #SecretReference
	}

	addresses?: {
		private?: string
		public?:  string
	}

	hardware: {
		arch:            *"amd64" | "arm64"
		virtualization?: #RuntimeVirtualizationV2
		profile:         *"standard" | "pi" | "gpu" | "storage"
		cpuCores?:       int & >=1
		ramGB?:          int & >=1
		storageGB?:      int & >=1
	}

	failureDomain: string & =~"^.+$"
	labels?: [string]: string
	enabled: bool | *true
}

#ControlPlaneIntent: {
	mode:             #ControlPlaneMode | *"single"
	authoritySiteRef: #SiteID
	members: [...#NodeID] & list.MinItems(1)

	_memberUnique: list.UniqueItems(members) & true

	if mode == "single" {
		members: list.MaxItems(1)
	}
	if mode == "warm-standby" {
		members: list.MinItems(2)
	}
	if mode == "quorum" {
		members: list.MinItems(3)
		// Keep the first supported quorum realization deliberately bounded.
		_memberCount: len(members) & (3 | 5 | 7)
	}
}

#CapabilitySelectionConfigV2: {
	providerRef?: #ContractID
	settings?:    #PublicSettings
	secretRefs?: [string]: #SecretReference
}

#CapabilitySelection: {
	enable: [...#CapabilityID] | *[]
	disable: [...#CapabilityID] | *[]
	config?: [#CapabilityID]: #CapabilitySelectionConfigV2

	_enableUnique:  list.UniqueItems(enable) & true
	_disableUnique: list.UniqueItems(disable) & true
	_conflicts: [for enabled in enable for disabled in disable if enabled == disabled {enabled}] & list.MaxItems(0)
}

#AddOnSelection: {
	enabled: bool | *true
	config?: #PublicSettings
	secretRefs?: [string]: #SecretReference
}

#SpecSourceLineage: {
	kind:       *"native-v2" | "migrated-v1" | "registry"
	ref?:       string & =~"^[^[:space:]]+$"
	migration?: #MigrationSourceLineage

	if kind == "native-v2" || kind == "registry" {
		migration?: _|_
	}
	if kind == "migrated-v1" {
		migration: #MigrationSourceLineage
	}
}

#MigrationSourceLineage: {
	fromAPIVersion: string & =~"^stackkit/.+$"
	adapterVersion: #SemanticVersion
	reportHash:     #ContentHash
	accepted:       true
	ambiguous:      false
	warnings: [...string] | *[]
	manualActions: []
}

#GenerationIntentV2: {
	strategy:   #GenerationStrategy
	target:     #GenerationTarget
	outputRoot: #OutputRootPath | *"deploy"
}

#SystemIntentV2: {
	timezone:           string & =~"^(UTC|[A-Za-z_+-]+(/[A-Za-z0-9_+.-]+)+)$" | *"UTC"
	locale:             string & =~"^[A-Za-z]{2,3}_[A-Za-z]{2,3}(\\.[A-Za-z0-9-]+)?$" | *"en_US.UTF-8"
	swap:               *"auto" | "disabled" | "manual"
	swapSizeMB?:        int & >=0
	unattendedUpgrades: *"security" | "disabled" | "all"

	if swap == "manual" {
		swapSizeMB: int & >0
	}
}

#StorageIntentV2: {
	dataRoot:     #AbsolutePath | *"/opt/data"
	backupRoot:   #AbsolutePath | *"/opt/backups"
	stacksRoot:   #AbsolutePath | *"/opt/stacks"
	mediaRoot?:   #AbsolutePath
	volumeDriver: *"local" | "nfs"
	external?: {
		deviceRef?: string & =~"^(/dev/|uuid:|label:).+$"
		mountPoint: #AbsolutePath
	}
	if volumeDriver == "nfs" {
		nfs: {
			server: string & =~"^.+$"
			path:   #AbsolutePath
		}
	}
}

#ContainerRuntimeIntentV2: {
	engine:         *"docker" | "podman"
	rootless:       bool | *false
	liveRestore:    bool | *true
	storageDriver?: "overlay2" | "btrfs" | "zfs" | "vfs"
	dataRoot:       #AbsolutePath | *"/var/lib/docker"
	logDriver:      *"json-file" | "journald" | "syslog" | "none"
	registryMirrors: [...string] | *[]
}

#NetworkIntentV2: {
	mode: #NetworkModeV2
	domain: {
		base:             string & =~"^[A-Za-z0-9][A-Za-z0-9.-]*[A-Za-z0-9]$"
		subdomainPrefix?: #ContractID
	}
	transport: {
		subnet:   string | *"172.20.0.0/16"
		gateway?: string
		mtu:      int & >=576 & <=9216 | *1500
		ipv6:     bool | *false
	}
	dns: {
		servers: [...string] & list.MinItems(1) | *["1.1.1.1", "8.8.8.8"]
		searchDomains: [...string] | *[]
	}
	tls: {
		defaultMode:  "internal" | "off" | "public"
		providerRef?: #ContractID
		challenge?:   "tls-alpn-01" | "http-01" | "dns-01"
		minVersion:   *"TLS1.2" | "TLS1.3"
		credentialRefs?: [string]: #SecretReference
	}
}

#ModuleIntentV2: {
	enabled:         bool | *true
	runtimeProfile?: #ContractID
	settings?:       #PublicSettings
	secretRefs?: [string]: #SecretReference
}

#InstallIntentV2: {
	mode:    *"bootstrapped" | "bare" | "advanced"
	runtime: *"docker" | "native"
	platform: {
		management:      *"selected-provider" | "standalone" | "native"
		providerRef?:    #ContractID
		fallbackAllowed: bool | *false
		setupPolicy: {
			platform:           *"automatic" | "on-demand" | "manual"
			applicationDefault: *"on-demand" | "automatic" | "manual"
		}
	}

	if runtime == "native" {
		platform: management: "native"
	}
	if platform.management != "standalone" {
		platform: fallbackAllowed: false
	}
}

// #StackSpecV2 is desired intent. It contains no discovered host facts and no
// plaintext secret material.
#StackSpecV2: {
	apiVersion: #ArchitectureAPIVersion
	kind:       "StackSpec"

	metadata: {
		name:      #ContractID
		stackId?:  #ContractID
		fleetRef?: #ContractID
	}
	source: #SpecSourceLineage | *{kind: "native-v2"}

	kit: {
		slug:     #KitSlug
		version?: string
	}

	install:    #InstallIntentV2
	generation: #GenerationIntentV2
	system: #SystemIntentV2 | *{}
	storage: #StorageIntentV2 | *{}
	network:    #NetworkIntentV2
	container?: #ContainerRuntimeIntentV2

	sites: [...#SiteSpec] & list.MinItems(1)
	nodes: [...#NodeSpecV2] & list.MinItems(1)
	controlPlane: #ControlPlaneIntent

	capabilities: #CapabilitySelection
	addons?: [string]: #AddOnSelection

	access?: [string]: #AccessPolicyV2
	bridge?: #BridgeContract
	availability: #AvailabilityIntent & {mode: controlPlane.mode}
	deviceEnrollment?: #DeviceEnrollmentPolicy
	partitionPolicy:   #PartitionPolicy
	workloads?: [string]: #WorkloadPlacementIntent
	modules?: [string]:   #ModuleIntentV2
	routes?: [string]:    #ServiceRouteIntentV2
	data?: #DataPlacementIntent

	if install.runtime == "docker" {
		container: #ContainerRuntimeIntentV2
	}
	if install.runtime == "native" {
		container?: _|_
	}

	_siteIDsUnique: list.UniqueItems([for site in sites {site.id}]) & true
	_nodeIDsUnique: list.UniqueItems([for node in nodes {node.id}]) & true
	_haCapabilityMustUseAddon: [for capability in capabilities.enable if capability == "availability-ha" {capability}] & list.MaxItems(0)

	_nodeSiteRefs: [for node in nodes {
		node: node.id
		matches: [for site in sites if site.id == node.siteRef {site.id}] & list.MinItems(1) & list.MaxItems(1)
	}]

	_controllerIDs: [for node in nodes for role in node.roles if role == "controller" {node.id}]
	_memberControllers: [for member in controlPlane.members {
		node: member
		matches: [for controller in _controllerIDs if controller == member {controller}] & list.MinItems(1) & list.MaxItems(1)
	}]
	_authoritySite: [for site in sites if site.id == controlPlane.authoritySiteRef {site.id}] & list.MinItems(1) & list.MaxItems(1)
	_memberFailureDomains: [
		for member in controlPlane.members
		for node in nodes
		if node.id == member {node.failureDomain},
	]

	if controlPlane.mode != "single" {
		addons: ha: enabled: true
		availability: enabled: true
		_memberFailureDomainsUnique: list.UniqueItems(_memberFailureDomains) & true
		_memberFailureDomainSpread:  _memberFailureDomains & list.MinItems(availability.failureDomainSpread)
	}
	if availability.enabled == true {
		controlPlane: mode: "warm-standby" | "quorum"
		addons: ha: enabled: true
	}
	if controlPlane.mode == "single" {
		availability: enabled: false
		if addons.ha != _|_ {
			addons: ha: enabled: false
		}
	}
	if addons.ha != _|_ if addons.ha.enabled == true {
		controlPlane: mode:    "warm-standby" | "quorum"
		availability: enabled: true
	}

	if deviceEnrollment != _|_ {
		_deviceEnrollmentAuthority: [for site in sites if site.id == deviceEnrollment.authoritySiteRef {site.id}] & list.MinItems(1) & list.MaxItems(1)
	}
	if data != _|_ {
		if data.defaultAuthority != "per-workload" {
			_dataDefaultAuthorityKind: [for site in sites if site.kind == data.defaultAuthority {site.id}] & list.MinItems(1)
		}
		if data.bindings != _|_ {
			_dataPrimarySites: [for bindingID, dataBinding in data.bindings {
				binding: bindingID
				matches: [for site in sites if site.id == dataBinding.primarySiteRef {site.id}] & list.MinItems(1) & list.MaxItems(1)
			}]
			_dataReplicaSites: [for bindingID, dataBinding in data.bindings if dataBinding.replicaSiteRefs != _|_ for replicaRef in dataBinding.replicaSiteRefs {
				binding: bindingID
				replica: replicaRef
				matches: [for site in sites if site.id == replicaRef {site.id}] & list.MinItems(1) & list.MaxItems(1)
			}]
			_dataReplicaSets: [for bindingID, dataBinding in data.bindings if dataBinding.replicaSiteRefs != _|_ {
				binding: bindingID
				unique:  list.UniqueItems(dataBinding.replicaSiteRefs) & true
				primaryExcluded: [for replicaRef in dataBinding.replicaSiteRefs if replicaRef == dataBinding.primarySiteRef {replicaRef}] & list.MaxItems(0)
			}]
		}
	}
	if access != _|_ {
		_accessAllowedSiteSets: [for policyID, accessPolicy in access if accessPolicy.allowedSiteRefs != _|_ {
			policy: policyID
			unique: list.UniqueItems(accessPolicy.allowedSiteRefs) & true
		}]
		_accessAllowedSites: [for policyID, accessPolicy in access if accessPolicy.allowedSiteRefs != _|_ for allowedSiteRef in accessPolicy.allowedSiteRefs {
			policy: policyID
			site:   allowedSiteRef
			matches: [for site in sites if site.id == allowedSiteRef {site.id}] & list.MinItems(1) & list.MaxItems(1)
		}]
	}

	if bridge != _|_ {
		_bridgePublicationsUnique: list.UniqueItems(bridge.publications) & true
		_bridgeFlowsUnique:        list.UniqueItems(bridge.policy.allowedFlows) & true
		_overlayPeerRefs: [for peer in bridge.overlay.peerSiteRefs {
			site: peer
			matches: [for site in sites if site.id == peer {site.id}] & list.MinItems(1) & list.MaxItems(1)
		}]
		_publicationPeerMembership: [for publication in bridge.publications {
			service: publication.serviceRef
			source: [for peer in bridge.overlay.peerSiteRefs if peer == publication.sourceSiteRef {peer}] & list.MinItems(1) & list.MaxItems(1)
			edge: [for peer in bridge.overlay.peerSiteRefs if peer == publication.edgeSiteRef {peer}] & list.MinItems(1) & list.MaxItems(1)
		}]
		_flowPeerMembership: [for flow in bridge.policy.allowedFlows {
			service: flow.serviceRef
			from: [for peer in bridge.overlay.peerSiteRefs if peer == flow.fromSiteRef {peer}] & list.MinItems(1) & list.MaxItems(1)
			to: [for peer in bridge.overlay.peerSiteRefs if peer == flow.toSiteRef {peer}] & list.MinItems(1) & list.MaxItems(1)
		}]
		_publicationRefs: [for publication in bridge.publications {
			service: publication.serviceRef
			source: [for site in sites if site.id == publication.sourceSiteRef {site.id}] & list.MinItems(1) & list.MaxItems(1)
			edge: [for site in sites if site.id == publication.edgeSiteRef {site.id}] & list.MinItems(1) & list.MaxItems(1)
		}]
		_bridgeFlowRefs: [for flow in bridge.policy.allowedFlows {
			service: flow.serviceRef
			from: [for site in sites if site.id == flow.fromSiteRef {site.id}] & list.MinItems(1) & list.MaxItems(1)
			to: [for site in sites if site.id == flow.toSiteRef {site.id}] & list.MinItems(1) & list.MaxItems(1)
		}]
		// Data-plane bridge flows are deliberately one-to-one with published,
		// catalog-backed services. A separate typed contract is required before a
		// control-plane channel can be introduced.
		_bridgeFlowCountExact: len(bridge.policy.allowedFlows) & len(bridge.publications)
		_bridgeFlowsBoundToPublications: [for flow in bridge.policy.allowedFlows {
			service: flow.serviceRef
			matches: [
				for publication in bridge.publications
				if publication.serviceRef == flow.serviceRef
				if publication.edgeSiteRef == flow.fromSiteRef
				if publication.sourceSiteRef == flow.toSiteRef {publication.serviceRef},
			] & list.MinItems(1) & list.MaxItems(1)
		}]
		if len(bridge.publications) > 0 {
			// A publication is executable only when all three policy seams agree:
			// an existing access policy, an exact edge-to-source allow-flow, and
			// an explicit data-authority binding. The cloud edge may proxy data,
			// but it never becomes its implicit authority or a broad LAN route.
			access: [string]: #AccessPolicyV2
			data: bindings: [string]: {
				classes: [...#DataClass] & list.MinItems(1)
				primarySiteRef: #SiteID
				replicaSiteRefs?: [...#SiteID]
				cloudCopyAllowed: bool | *false
			}
			_publicationPolicyRefs: [for publication in bridge.publications {
				service: publication.serviceRef
				policy:  publication.auth.policyRef
				matches: [
					for policyID, policy in access
					if policyID == publication.auth.policyRef
					if policy.exposure == "public"
					if policy.authentication != "none"
					if policy.allowedMethods != _|_ {policyID},
				] & list.MinItems(1) & list.MaxItems(1)
			}]
			_publicationFlowContracts: [for publication in bridge.publications {
				service: publication.serviceRef
				matches: [
					for flow in bridge.policy.allowedFlows
					if flow.fromSiteRef == publication.edgeSiteRef
					if flow.toSiteRef == publication.sourceSiteRef
					if flow.serviceRef == publication.serviceRef {flow.serviceRef},
				] & list.MinItems(1)
			}]
			_publicationMethodContracts: [
				for publication in bridge.publications
				for flow in bridge.policy.allowedFlows
				if flow.fromSiteRef == publication.edgeSiteRef
				if flow.toSiteRef == publication.sourceSiteRef
				if flow.serviceRef == publication.serviceRef
				if flow.protocol == "http" || flow.protocol == "https"
				for method in flow.methods {
					service:    publication.serviceRef
					httpMethod: method
					matches: [
						for policyID, policy in access
						if policyID == publication.auth.policyRef
						for allowed in policy.allowedMethods
						if allowed == method {allowed},
					] & list.MinItems(1) & list.MaxItems(1)
				},
			]
			_publicationDataContracts: [for publication in bridge.publications {
				service: publication.serviceRef
				matches: [
					for bindingID, binding in data.bindings
					if bindingID == publication.serviceRef
					if binding.primarySiteRef == publication.sourceSiteRef
					if binding.cloudCopyAllowed == false {bindingID},
				] & list.MinItems(1) & list.MaxItems(1)
			}]
		}
		if len(bridge.policy.allowedFlows) > 0 {
			data: bindings: [string]: {
				classes: [...#DataClass] & list.MinItems(1)
				primarySiteRef: #SiteID
				replicaSiteRefs?: [...#SiteID]
				cloudCopyAllowed: bool | *false
			}
			_bridgeFlowDataContracts: [for flow in bridge.policy.allowedFlows {
				service: flow.serviceRef
				binding: [for bindingID, _ in data.bindings if bindingID == flow.serviceRef {bindingID}] & list.MinItems(1) & list.MaxItems(1)
				classes: [for flowClass in flow.dataClasses {
					class: flowClass
					matches: [
						for bindingID, binding in data.bindings
						if bindingID == flow.serviceRef
						for bindingClass in binding.classes
						if bindingClass == flowClass {bindingClass},
					] & list.MinItems(1) & list.MaxItems(1)
				}]
			}]
		}
	}
}

// #StackInstanceRecordV2 is the Fleet-facing identity of one independently
// planned and operated StackInstance. A Fleet groups instances for inventory
// and lifecycle visibility; it never merges their control authorities or
// creates implicit network, identity, or administrator trust.
#StackInstanceRecordV2: {
	stackId: #ContractID
	kit: {
		slug:    #KitSlug
		version: string
	}
	resolvedPlan: {
		apiVersion:      "stackkit.resolved-plan/v1"
		planHash:        #ContentHash
		specHash:        #ContentHash
		compilerVersion: string
	}
	sites: [...{
		id:   #SiteID
		kind: #SiteKind
	}] & list.MinItems(1)
	nodes: [...{
		id: #NodeID
		// inventoryRef is the stable physical/virtual device identity. It is
		// Fleet-unique even when local node IDs such as "main" repeat.
		inventoryRef: #ContractID
		siteRef:      #SiteID
		roles: [...#NodeRoleV2] & list.MinItems(1)
	}] & list.MinItems(1)
	controlAuthority: {
		id:      #ContractID
		siteRef: #SiteID
		mode:    #ControlPlaneMode
		members: [...#NodeID] & list.MinItems(1)
		haAddOnEnabled: bool

		if mode == "single" {
			members:        list.MaxItems(1)
			haAddOnEnabled: false
		}
		if mode != "single" {
			haAddOnEnabled: true
		}
	}

	_siteIDsUnique: list.UniqueItems([for site in sites {site.id}]) & true
	_nodeIDsUnique: list.UniqueItems([for node in nodes {node.id}]) & true
	_inventoryRefsLocal: list.UniqueItems([for node in nodes {node.inventoryRef}]) & true
	_memberIDsUnique: list.UniqueItems(controlAuthority.members) & true
	_nodeSiteRefs: [for node in nodes {
		node: node.id
		matches: [for site in sites if site.id == node.siteRef {site.id}] & list.MinItems(1) & list.MaxItems(1)
	}]
	_authoritySiteRef: [for site in sites if site.id == controlAuthority.siteRef {site.id}] & list.MinItems(1) & list.MaxItems(1)
	_controllerIDs: [for node in nodes for role in node.roles if role == "controller" {node.id}]
	_memberControllers: [for member in controlAuthority.members {
		node: member
		matches: [for controller in _controllerIDs if controller == member {controller}] & list.MinItems(1) & list.MaxItems(1)
	}]
}

#FleetV2: {
	apiVersion: "stackkit.fleet/v1"
	kind:       "Fleet"
	metadata: {
		id:   #ContractID
		name: string & =~"^.+$"
	}
	instances: [...#StackInstanceRecordV2] & list.MinItems(1)
	isolation: {
		implicitNetworkTrust:  false
		implicitAdminTrust:    false
		implicitIdentityTrust: false
		implicitSecretSharing: false
	}

	_stackIDsUnique: list.UniqueItems([for instance in instances {instance.stackId}]) & true
	// The same physical or virtual device cannot silently become a member of
	// two independent StackInstances. Explicit federation is a bridge contract,
	// never Fleet membership inheritance.
	_inventoryRefsUnique: list.UniqueItems([
		for instance in instances
		for node in instance.nodes {node.inventoryRef},
	]) & true
}

#WorkloadPlacementIntent: {
	siteRefs?: [...#SiteID]
	nodeRefs?: [...#NodeID]
	requiresRoles?: [...#NodeRoleV2]
	dataClasses?: [...#DataClass]
}

#DataClass: "public" | "internal" | "personal" | "sensitive" | "secret"

#CloudCopyPolicyV2: {
	policyRef: #ContractID
	allowedClasses: [...#DataClass] & list.MinItems(1)
	allowPrimary:  bool | *false
	allowReplicas: bool | *false

	_allowedClassesUnique: list.UniqueItems(allowedClasses) & true
}

#DataBindingV2: {
	classes: [...#DataClass] & list.MinItems(1)
	primarySiteRef: #SiteID
	replicaSiteRefs?: [...#SiteID]
	cloudCopyAllowed: bool | *false
	cloudCopyPolicy?: #CloudCopyPolicyV2

	_classesUnique: list.UniqueItems(classes) & true
}

#DataPlacementIntent: {
	defaultAuthority: #SiteKind | "per-workload"
	bindings?: [string]: #DataBindingV2
}

// #HomeAuthorityCloudCopyBindingV2 turns every cloud placement or cloud-copy
// opt-in into an explicit, typed policy decision. It is applied by the selected
// KitDefinition when Home is the default authority; Cloud Kit's native cloud
// authority remains governed by its own definition instead.
#HomeAuthorityCloudCopyBindingV2: {
	sites: [...#SiteSpec] & list.MinItems(1)
	data: #DataPlacementIntent & {defaultAuthority: "home"}

	if data.bindings != _|_ {
		_cloudCopyOptInPolicies: [for bindingID, dataBinding in data.bindings if dataBinding.cloudCopyAllowed == true {
			bindingRef: bindingID
			policy:     dataBinding.cloudCopyPolicy
			classes: [for dataClass in dataBinding.classes {
				class: dataClass
				matches: [for allowed in dataBinding.cloudCopyPolicy.allowedClasses if allowed == dataClass {allowed}] & list.MinItems(1) & list.MaxItems(1)
			}]
		}]
		_cloudPrimaryPolicies: [
			for bindingID, dataBinding in data.bindings
			for site in sites
			if site.id == dataBinding.primarySiteRef && site.kind == "cloud" {
				bindingRef:    bindingID
				copyAllowed:   dataBinding.cloudCopyAllowed & true
				primaryPolicy: dataBinding.cloudCopyPolicy.allowPrimary & true
				policyClassScope: [for dataClass in dataBinding.classes {
					class: dataClass
					matches: [for allowed in dataBinding.cloudCopyPolicy.allowedClasses if allowed == dataClass {allowed}] & list.MinItems(1) & list.MaxItems(1)
				}]
			},
		]
		_cloudReplicaPolicies: [
			for bindingID, dataBinding in data.bindings
			if dataBinding.replicaSiteRefs != _|_
			for replicaRef in dataBinding.replicaSiteRefs
			for site in sites
			if site.id == replicaRef && site.kind == "cloud" {
				bindingRef:    bindingID
				replica:       replicaRef
				copyAllowed:   dataBinding.cloudCopyAllowed & true
				replicaPolicy: dataBinding.cloudCopyPolicy.allowReplicas & true
				policyClassScope: [for dataClass in dataBinding.classes {
					class: dataClass
					matches: [for allowed in dataBinding.cloudCopyPolicy.allowedClasses if allowed == dataClass {allowed}] & list.MinItems(1) & list.MaxItems(1)
				}]
			},
		]
	}
}

// #AccessPolicyV2 keeps network location separate from identity. LAN-aware
// convenience is allowed only in combination with an enrolled device.
#AccessPolicyV2: {
	exposure:       *"private" | "lan" | "public"
	privilege:      *"user" | "admin" | "identity" | "secrets"
	authentication: *"human" | "device" | "human+device" | "none"

	enrolledDeviceRequired: bool | *false
	ownerStepUpRequired:    bool | *false
	lanStepDown?:           bool | *false
	allowedSiteRefs?: [...#SiteID]
	allowedMethods?: [...#HTTPMethod] & list.MinItems(1)
	if allowedMethods != _|_ {
		_allowedMethodsUnique: list.UniqueItems(allowedMethods) & true
	}

	if exposure == "public" {
		authentication: "human" | "device" | "human+device"
	}
	if lanStepDown == true {
		exposure:               "lan"
		enrolledDeviceRequired: true
		authentication:         "device" | "human+device"
	}
	if privilege == "admin" || privilege == "identity" || privilege == "secrets" {
		authentication:         "human+device"
		ownerStepUpRequired:    true
		enrolledDeviceRequired: true
	}
}

#ServiceExposureV2: "local" | "remote-private" | "public"

// #ModuleServiceEndpointV2 is the catalog-owned backend contract for one
// routable service. A StackSpec may choose the listener port and exposure, but
// it cannot invent a backend service, protocol, or target port. Placement is
// inherited from the owning resolved render unit and therefore remains bound
// to the unit's exact site, node, daemon, and instance projection.
#ModuleServiceEndpointV2: {
	serviceRef:       #ContractID
	upstreamProtocol: #NetworkProtocol
	targetPort:       int & >=1 & <=65535
	allowedIngressProtocols: [...#NetworkProtocol] & list.MinItems(1)
	allowedExposures: [...#ServiceExposureV2] & list.MinItems(1)
	originSelector: *"single-site" | "control-authority-site"
	healthRef:      #ContractID
	data?: {
		// Service and data authority intentionally share one stable key. This
		// prevents a publication from silently switching to an unrelated data
		// binding while keeping the same routable service identity.
		bindingRef: serviceRef
		requiredClasses: [...#DataClass] & list.MinItems(1)
		locality: "primary-site" | "primary-or-replica"

		_requiredClassesUnique: list.UniqueItems(requiredClasses) & true
	}

	_allowedIngressProtocolsUnique: list.UniqueItems(allowedIngressProtocols) & true
	_allowedExposuresUnique:        list.UniqueItems(allowedExposures) & true
}

#ServiceRouteIntentV2: {
	serviceRef:      #ContractID
	moduleRef?:      #ContractID
	exposure:        #ServiceExposureV2 | *"local"
	protocol:        #NetworkProtocol | *"https"
	port:            int & >=1 & <=65535
	targetPort?:     int & >=1 & <=65535
	host?:           string & =~"^.+$"
	path?:           string & =~"^/"
	accessPolicyRef: #ContractID

	if protocol == "http" || protocol == "https" {
		host: string & =~"^.+$"
		path: string & =~"^/"
	}
}

#ResolvedAccessDecisionV2: {
	exposure:               #ServiceExposureV2
	authentication:         "human" | "device" | "human+device" | "workload" | "none"
	privilege:              *"user" | "admin" | "identity" | "secrets"
	enrolledDeviceRequired: bool | *false
	ownerStepUpRequired:    bool | *false
	allowedMethods?: [...#HTTPMethod] & list.MinItems(1)
	defaultClosed: true
	policyRef:     #ContractID
	if allowedMethods != _|_ {
		_allowedMethodsUnique: list.UniqueItems(allowedMethods) & true
	}

	if exposure == "remote-private" || exposure == "public" {
		authentication: "human" | "device" | "human+device" | "workload"
	}
	if privilege == "admin" || privilege == "identity" || privilege == "secrets" {
		authentication:         "human+device"
		enrolledDeviceRequired: true
		ownerStepUpRequired:    true
	}
}

#ResolvedRouteTLSV2: {
	required:     bool
	mode:         "off" | "internal" | "terminate-at-edge" | "passthrough"
	minVersion?:  "TLS1.2" | "TLS1.3"
	providerRef?: #ContractID
	credentialRefs?: [string]: #SecretReference

	if required == false {mode: "off"}
	if required == true {
		mode:       "internal" | "terminate-at-edge" | "passthrough"
		minVersion: "TLS1.2" | "TLS1.3"
	}
}

#ResolvedServiceRouteV2: {
	id:            #ContractID
	serviceRef:    #ContractID
	moduleRef:     #ContractID
	originSiteRef: #SiteID
	originNodeRefs: [...#NodeID] & list.MinItems(1)
	exposure:         #ServiceExposureV2
	protocol:         #NetworkProtocol
	upstreamProtocol: #NetworkProtocol
	port:             int & >=1 & <=65535
	targetPort:       int & >=1 & <=65535
	host?:            string & =~"^.+$"
	path?:            string & =~"^/"
	access: #ResolvedAccessDecisionV2 & {exposure: exposure}
	tls:           #ResolvedRouteTLSV2
	healthGateRef: #ContractID

	if protocol == "http" || protocol == "https" {
		host: string & =~"^.+$"
		path: string & =~"^/"
	}
	if protocol == "https" {
		tls: required: true
	}
	if exposure == "public" {
		protocol: "https"
		access: defaultClosed: true
		tls: required:         true
	}
}

#NetworkProtocol: "tcp" | "udp" | "http" | "https"

#HTTPMethod: "GET" | "HEAD" | "POST" | "PUT" | "PATCH" | "DELETE" | "OPTIONS"

#BridgeFlow: {
	fromSiteRef: #SiteID
	toSiteRef:   #SiteID
	serviceRef:  #ContractID
	protocol:    #NetworkProtocol
	// Cross-site flows are never port-wildcards. Even HTTP/HTTPS flows name
	// the exact transport ports that the protected Site may receive.
	ports: [...int & >=1 & <=65535] & list.MinItems(1)
	methods?: [...#HTTPMethod]
	dataClasses: [...#DataClass] & list.MinItems(1) | *["internal"]
	serviceIdentityRequired: true

	_distinctSites:     (fromSiteRef != toSiteRef) & true
	_portsUnique:       list.UniqueItems(ports) & true
	_dataClassesUnique: list.UniqueItems(dataClasses) & true
	if protocol == "http" || protocol == "https" {
		methods: [...#HTTPMethod] & list.MinItems(1)
		_methodsUnique: list.UniqueItems(methods) & true
	}
	if protocol == "tcp" || protocol == "udp" {
		methods?: _|_
	}
}

#ConnectivityOverlay: {
	provider:                "wireguard" | "headscale" | "tailscale" | "netbird" | "pangolin"
	initiation:              "local-outbound"
	outboundEstablished:     true
	trafficMode:             "management-only" | "policy-scoped"
	advertiseDefaultRoute:   false
	advertisePrivateSubnets: false
	allowBroadRoutes:        false
	peerSiteRefs: [...#SiteID] & list.MinItems(2)

	_peerUnique: list.UniqueItems(peerSiteRefs) & true
}

_servicePublicationShape: {
	serviceRef:    #ContractID
	sourceSiteRef: #SiteID
	edgeSiteRef:   #SiteID
	host:          string & =~"^.+$"
	protocol:      "https"
	port:          int & >=1 & <=65535
	path:          string & =~"^/"
	defaultClosed: true

	tls: {
		required: true
		// Edge authorization and rate limiting are only executable when the
		// edge terminates TLS. Passthrough requires a separate end-to-end
		// verifier contract and is deliberately unavailable until that contract
		// is part of the catalog authority.
		mode:       "terminate-at-edge"
		minVersion: "TLS1.2" | "TLS1.3"
	}
	auth: {
		required:  true
		policyRef: #ContractID
	}
	origin: {
		identityRef:  #ContractID
		mtlsRequired: true
	}
	rateLimit: {
		enabled:       true
		requests:      int & >=1
		windowSeconds: int & >=1
	}

	_distinctSites: (sourceSiteRef != edgeSiteRef) & true
}

#ServicePublication: _servicePublicationShape

#BridgePolicy: {
	defaultDeny: true
	allowedFlows: [...#BridgeFlow] | *[]
	allowRFC1918Transit:            false
	cloudMayEnrollDevices:          false
	cloudMayIssueDeviceCredentials: false
}

#OutboundControlContract: {
	enabled:   true
	transport: "managed-agent" | "mtls-agent"
	audience:  #ContractID
	actionAllowlist: [...#ContractID] & list.MinItems(1)
	maxTTLSeconds:                  int & >=1 & <=300
	capabilityScopedActions:        true
	requiresSignedActions:          true
	requiresNonce:                  true
	requiresIdempotencyKey:         true
	requiresResolvedPlanHash:       true
	replayProtection:               true
	requiresApprovalForDestructive: true
}

#BridgeContract: {
	overlay: #ConnectivityOverlay
	publications: [...#ServicePublication] | *[]
	policy:       #BridgePolicy
	controlAgent: #OutboundControlContract

	if overlay.trafficMode == "management-only" {
		publications: []
		policy: allowedFlows: []
	}
	if len(publications) > 0 {
		overlay: trafficMode: "policy-scoped"
	}
	if len(policy.allowedFlows) > 0 {
		overlay: trafficMode: "policy-scoped"
	}
}

#ResolvedServicePublicationV2: _servicePublicationShape & {
	auth: {
		required:  true
		policyRef: #ContractID
	}
	let publicationPolicyRef = auth.policyRef
	moduleRef: #ContractID
	unitRef:   #ContractID
	originNodeRefs: [...#NodeID] & list.MinItems(1)
	originInstanceRefs: [...#ContractID] & list.MinItems(1)
	upstreamProtocol: #NetworkProtocol
	targetPort:       int & >=1 & <=65535
	healthGateRef:    #ContractID
	dataBindingRef?:  #ContractID
	access: #ResolvedAccessDecisionV2 & {
		exposure:  "public"
		policyRef: publicationPolicyRef
	}

	_originNodeRefsUnique:     list.UniqueItems(originNodeRefs) & true
	_originInstanceRefsUnique: list.UniqueItems(originInstanceRefs) & true
}

#ResolvedBridgeContractV2: {
	overlay: #ConnectivityOverlay
	publications: [...#ResolvedServicePublicationV2] | *[]
	policy:       #BridgePolicy
	controlAgent: #OutboundControlContract

	_publicationsUnique: list.UniqueItems(publications) & true
	_flowsUnique:        list.UniqueItems(policy.allowedFlows) & true
	_publicationPeerMembership: [for publication in publications {
		service: publication.serviceRef
		source: [for peer in overlay.peerSiteRefs if peer == publication.sourceSiteRef {peer}] & list.MinItems(1) & list.MaxItems(1)
		edge: [for peer in overlay.peerSiteRefs if peer == publication.edgeSiteRef {peer}] & list.MinItems(1) & list.MaxItems(1)
	}]
	_flowPeerMembership: [for flow in policy.allowedFlows {
		service: flow.serviceRef
		from: [for peer in overlay.peerSiteRefs if peer == flow.fromSiteRef {peer}] & list.MinItems(1) & list.MaxItems(1)
		to: [for peer in overlay.peerSiteRefs if peer == flow.toSiteRef {peer}] & list.MinItems(1) & list.MaxItems(1)
	}]

	if overlay.trafficMode == "management-only" {
		publications: []
		policy: allowedFlows: []
	}
	if len(publications) > 0 {
		overlay: trafficMode: "policy-scoped"
	}
	if len(policy.allowedFlows) > 0 {
		overlay: trafficMode: "policy-scoped"
	}
}

#AvailabilityIntent: {
	enabled:              bool | *false
	mode:                 #ControlPlaneMode | *"single"
	rpoSeconds?:          int & >=0
	rtoSeconds?:          int & >0
	failureDomainSpread?: int & >=1
	fencing:              *"none" | "manual" | "automatic"

	if enabled == false {mode: "single"}
	if mode == "single" {enabled: false}
	if mode == "warm-standby" {
		enabled:             true
		rpoSeconds:          int & >=0
		rtoSeconds:          int & >0
		failureDomainSpread: int & >=2
		fencing:             "manual" | "automatic"
	}
	if mode == "quorum" {
		enabled:             true
		rpoSeconds:          int & >=0
		rtoSeconds:          int & >0
		failureDomainSpread: 3 | 5 | 7
		fencing:             "manual" | "automatic"
	}
}

// #KitAvailabilityPolicyV2 is immutable KitDefinition authority. The same HA
// add-on therefore resolves differently for Basement, Cloud and Modern without
// creating an HA kit or allowing runtime context to choose the failure model.
#AvailabilityFailureModelV2: {
	basis:             "local-device" | "provider-zone" | "site-and-link"
	memberSiteScope:   "control-member-sites" | "authority-site-control-members"
	partitionBehavior: "local-control-continues" | "provider-network-failover" | "home-authority-continues-cloud-edge-fails-closed"
}

#AvailabilityHealthAcceptanceV2: {
	requiredGateRefs: [...#ContractID] & list.MinItems(1)
	memberReadiness:         "all" | "majority"
	_requiredGateRefsUnique: list.UniqueItems(requiredGateRefs) & true
}

#AvailabilityEvidenceAcceptanceV2: {
	requiredRefs: [...string & =~"^.+$"] & list.MinItems(1)
	_requiredRefsUnique: list.UniqueItems(requiredRefs) & true
}

#KitAvailabilityPolicyV2: {
	mode:           "warm-standby" | "quorum"
	policyRef:      #ContractID
	realizationRef: #ContractID
	providerRef:    #ContractID
	moduleRef:      #ContractID
	selector:       "control-plane-members"

	defaults: {
		rpoSeconds:          int & >=0
		rtoSeconds:          int & >0
		failureDomainSpread: int & >=2
		fencing:             "manual" | "automatic"
	}
	limits: {
		maxRpoSeconds:          int & >=0
		maxRtoSeconds:          int & >0
		minFailureDomainSpread: int & >=2
		maxFailureDomainSpread: int & >=minFailureDomainSpread
		allowedFencing: [...("manual" | "automatic")] & list.MinItems(1)
		_allowedFencingUnique: list.UniqueItems(allowedFencing) & true
	}
	failureModel:       #AvailabilityFailureModelV2
	healthAcceptance:   #AvailabilityHealthAcceptanceV2
	evidenceAcceptance: #AvailabilityEvidenceAcceptanceV2

	_defaultRpoWithinLimit:    defaults.rpoSeconds & <=limits.maxRpoSeconds
	_defaultRtoWithinLimit:    defaults.rtoSeconds & <=limits.maxRtoSeconds
	_defaultSpreadWithinLimit: defaults.failureDomainSpread & >=limits.minFailureDomainSpread & <=limits.maxFailureDomainSpread
	_defaultFencingAllowed: [for allowed in limits.allowedFencing if allowed == defaults.fencing {allowed}] & list.MinItems(1) & list.MaxItems(1)
	if mode == "warm-standby" {
		defaults: failureDomainSpread:     2
		limits: minFailureDomainSpread:    2
		healthAcceptance: memberReadiness: "all"
	}
	if mode == "quorum" {
		defaults: failureDomainSpread: 3 | 5 | 7
		limits: {
			minFailureDomainSpread: 3
			maxFailureDomainSpread: 3 | 5 | 7
		}
		healthAcceptance: memberReadiness: "majority"
	}
}

#KitAvailabilityContractV2: {
	policies: {
		"warm-standby": #KitAvailabilityPolicyV2 & {mode: "warm-standby"}
		quorum: #KitAvailabilityPolicyV2 & {mode: "quorum"}
	}
}

#ResolvedAvailabilityMemberV2: {
	nodeRef:       #NodeID
	siteRef:       #SiteID
	failureDomain: string & =~"^.+$"
}

#ResolvedAvailabilityV2: {
	#AvailabilityIntent
	policyRef?:          #ContractID
	realizationRef?:     #ContractID
	providerRef?:        #ContractID
	moduleRef?:          #ContractID
	selector?:           "control-plane-members"
	failureModel?:       #AvailabilityFailureModelV2
	healthAcceptance?:   #AvailabilityHealthAcceptanceV2
	evidenceAcceptance?: #AvailabilityEvidenceAcceptanceV2
	selectedMembers?: [...#ResolvedAvailabilityMemberV2]
}

#CapabilityRequirement: {
	id:          #CapabilityID
	minVersion?: string
	optional:    bool | *false
}

#CapabilityContract: {
	metadata: {
		id:          #CapabilityID
		version:     string
		description: string
		layer:       "foundation" | "platform" | "application" | "operations"
	}
	requires?: [...#CapabilityRequirement]
	conflicts?: [...#CapabilityID]
	supportedSiteKinds: [...#SiteKind] & list.MinItems(1)
	privileges?: [...string]
	networkFlows?: [...#BridgeFlow]
	secretInputs?: [...#ContractID]
	dataClasses?: [...#DataClass]
	health?: [...#HealthCheckContractV2]
	evidence?: [...string]
}

#ProviderModuleRefsV2: {
	required: [...#ContractID] | *[]
	optional: [...#ContractID] | *[]

	_requiredUnique: list.UniqueItems(required) & true
	_optionalUnique: list.UniqueItems(optional) & true
	_overlap: [
		for requiredRef in required
		for optionalRef in optional
		if requiredRef == optionalRef {requiredRef},
	] & list.MaxItems(0)
}

#ProviderOwnerInputBindingV2: {
	source:        "capability-setting" | "capability-secret"
	capabilityRef: #CapabilityID
	key:           string & =~"^[^[:space:]]+$"
}

// #ProviderRealizationContractV2 makes provider ownership explicit. Required
// modules are selected by the compiler; optional modules are selected only by
// governed StackSpec module intent. Host/external owners remain providers and
// must never be projected into synthetic modules.
#ProviderRealizationContractV2: {
	kind:                *"none" | "host" | "external" | "modules"
	ownerRef?:           #ContractID
	realizationSupport?: #ModuleRealizationSupportV2
	inputBindings?: [#ContractID]: #ProviderOwnerInputBindingV2
	moduleRefs: #ProviderModuleRefsV2 | *{
		required: []
		optional: []
	}

	_allModuleRefs: list.Concat([moduleRefs.required, moduleRefs.optional])
	if kind == "modules" {
		_allModuleRefs:      list.MinItems(1)
		ownerRef?:           _|_
		realizationSupport?: _|_
		inputBindings?:      _|_
	}
	if kind == "host" || kind == "external" {
		ownerRef:           #ContractID
		realizationSupport: #ModuleRealizationSupportV2
		inputBindings: [#ContractID]: #ProviderOwnerInputBindingV2 | *{}
		moduleRefs: {
			required: []
			optional: []
		}
	}
	if kind == "none" {
		ownerRef?:           _|_
		realizationSupport?: _|_
		inputBindings?:      _|_
		moduleRefs: {
			required: []
			optional: []
		}
	}
}

#CapabilityProvider: {
	metadata: {
		id:      #ContractID
		version: string
	}
	provides: [...#CapabilityID] & list.MinItems(1)
	requires?: [...#CapabilityRequirement]
	conflicts?: [...#ContractID]
	supportedSiteKinds: [...#SiteKind] & list.MinItems(1)
	privileges?: [...string]
	networkFlows?: [...#BridgeFlow]
	secretInputs?: [...#ContractID]
	dataClasses?: [...#DataClass]
	realization: #ProviderRealizationContractV2 | *{
		kind: "none"
		moduleRefs: {
			required: []
			optional: []
		}
	}
	selection?: {
		// At most one provider may be the governed default for the same
		// capability/site-kind pair. Otherwise StackSpec must select providerRef.
		defaultForSiteKinds: [...#SiteKind] | *[]
	}
	health?: [...#HealthCheckContractV2]
	evidence?: [...string]

	if realization.kind == "host" || realization.kind == "external" {
		realization: ownerRef: metadata.id
		health: [...#HealthCheckContractV2] & list.MinItems(1)
		evidence: [...string & =~"^.+$"] & list.MinItems(1)
		_ownerInputCapabilityRefs: [
			for inputRef, binding in realization.inputBindings {
				input:      inputRef
				capability: binding.capabilityRef
				matches: [for capabilityRef in provides if capabilityRef == binding.capabilityRef {capabilityRef}] & list.MinItems(1) & list.MaxItems(1)
			},
		]
		_requiredOwnerInputBindings: [
			for requiredRef in realization.realizationSupport.inputs.requiredRefs {
				input: requiredRef
				matches: [for inputRef, _ in realization.inputBindings if inputRef == requiredRef {inputRef}] & list.MinItems(1) & list.MaxItems(1)
			},
		]
	}
}

#ModuleRuntimeContractV2: {
	kind:     "container" | "native" | "host" | "external" | "control-plane"
	delivery: "stackkit" | "selected-paas" | "external-control-plane"
	engine?:  "docker" | "podman" | "systemd" | "binary" | "api"
	image?: {
		ref:     string & =~"^[^[:space:]]+$"
		digest?: #ContentHash
	}
	settings?: #PublicSettings

	if kind == "container" {
		engine: "docker" | "podman"
		image: {ref: string & =~"^[^[:space:]]+$"}
	}
	if kind == "external" || kind == "control-plane" {
		delivery: "external-control-plane"
	}
}

// #ModuleNodeSelectionV2 is the catalog-owned semantic selector for module
// placement. It is evaluated only against normalized StackSpec topology; host
// discovery may attest those declarations but may never widen the selector.
#ModuleNodeSelectionV2: {
	authority:           *"any" | "control-authority-site" | "non-control-authority-sites"
	controlPlaneMembers: *"any" | "only" | "exclude"
	requiredRoles?: [...#NodeRoleV2] & list.MinItems(1)
	matchLabels?: {[string]: string} & struct.MinFields(1)

	if requiredRoles != _|_ {
		_requiredRolesUnique: list.UniqueItems(requiredRoles) & true
	}
}

#ModuleRuntimeInventoryFactV1: "arch" | "cpuCores" | "ramGB" | "storageGB" | "virtualization"

// #ModuleRuntimeRequirementsV2 describes the minimum attested runtime facts a
// selected node must satisfy. Every constrained fact is required in both the
// desired node hardware declaration and InventoryFacts, and both values must
// agree before the compiler may place the module.
#ModuleRuntimeRequirementsV2: {
	allowedArchitectures?: [...("amd64" | "arm64")] & list.MinItems(1)
	minCpuCores?:  int & >=1
	minRamGB?:     int & >=1
	minStorageGB?: int & >=1
	allowedVirtualization?: [...#RuntimeVirtualizationV2] & list.MinItems(1)
	requireInventoryFacts?: [...#ModuleRuntimeInventoryFactV1] & list.MinItems(1)

	if allowedArchitectures != _|_ {
		_allowedArchitecturesUnique: list.UniqueItems(allowedArchitectures) & true
	}
	if allowedVirtualization != _|_ {
		_allowedVirtualizationUnique: list.UniqueItems(allowedVirtualization) & true
	}
	if requireInventoryFacts != _|_ {
		_requiredInventoryFactsUnique: list.UniqueItems(requireInventoryFacts) & true
	}
} & struct.MinFields(1)

#ModuleRenderUnitKindV2: "compose" | "cue-fragment" | "host" | "job" | "opentofu" | "native-config"

#GenerationArtifactKindV2:   "opentofu" | "compose" | "metadata" | "script" | "native-config"
#GenerationArtifactFormatV2: "json" | "yaml" | "hcl" | "shell" | "text"

// Implementation interfaces are runtime seams between selected modules. They
// are deliberately separate from product and kit capabilities: resolving one
// never selects a capability or changes a kit profile.
#RenderUnitPlacementV2: {
	scope:       "module" | "node-local"
	cardinality: "single" | "one-per-node" | "one-per-daemon"
	daemonRef?:  #ContractID

	if scope == "module" {
		cardinality: "single"
		daemonRef?:  _|_
	}
	if cardinality == "one-per-daemon" {
		scope:     "node-local"
		daemonRef: #ContractID
	}
	if cardinality != "one-per-daemon" {
		daemonRef?: _|_
	}
}

// Runtime daemon identity is always node-scoped. daemonRef is the stable
// contract name selected by a render unit; instanceRef proves which observed
// daemon instance satisfied it on that node. A socket path may not alias a
// second daemon identity on the same node.
#RuntimeDaemonFactV1: {
	instanceRef: #ContractID
	engine:      "docker"
	socketPath:  #UnixSocketPath
}

#ResolvedRuntimeDaemonBindingV1: {
	siteRef:     #SiteID
	nodeRef:     #NodeID
	daemonRef:   #ContractID
	instanceRef: #ContractID
	engine:      "docker"
	socketPath:  #UnixSocketPath
}

#DockerHTTPReadonlyScopeV1: "CONTAINERS" | "EVENTS" | "NETWORKS" | "PING" | "VERSION" | "INFO" | "SERVICES" | "TASKS"

#DockerHTTPReadonlyEndpointV1: {
	ref:        #ContractID
	visibility: "node-local"
	transport:  "tcp"
	networkRef: #ContractID
	address:    string & =~"^[^[:space:]]+$"
	port:       int & >=1 & <=65535
}

#DockerSocketDirectEndpointV1: {
	visibility: "node-local"
	transport:  "unix-socket"

	// A fixed path is an assertion that every selected daemon binding exposes
	// exactly that socket. pathSource delegates the concrete path to each
	// already-resolved node-scoped daemon binding, which is required for valid
	// rootless fleets whose user IDs (and therefore socket paths) differ.
	({
		path:        #UnixSocketPath
		pathSource?: _|_
	} | {
		path?:      _|_
		pathSource: "daemon-binding"
	})
}

#ImplementationInterfaceProviderV2: {
	id:       #ContractID
	kind:     "docker-http-readonly-v1"
	protocol: "docker-http"
	version:  "v1"
	endpoint: #DockerHTTPReadonlyEndpointV1
	scopes: [...#DockerHTTPReadonlyScopeV1] & list.MinItems(1)
	coLocation:    "same-node-and-network"
	daemonRef:     #ContractID
	policyProfile: #ContractID

	_scopesUnique: list.UniqueItems(scopes) & true
	_baselineScopesEnabled: [for baselineScope in ["CONTAINERS", "EVENTS", "NETWORKS", "PING", "VERSION"] {
		scope: baselineScope
		matches: [for scope in scopes if scope == baselineScope {scope}] & list.MinItems(1) & list.MaxItems(1)
	}]
}

#ImplementationInterfaceRequirementShapeV2: {
	id:       #ContractID
	kind:     "docker-http-readonly-v1" | "docker-socket-direct-v1"
	protocol: string
	version:  string
	endpoint: _
	scopes: [...string] & list.MinItems(1)
	coLocation:    string
	daemonRef:     #ContractID
	policyProfile: #ContractID
	providerBindings?: [...#ResolvedImplementationInterfaceBindingV2]

	_scopesUnique: list.UniqueItems(scopes) & true
	if kind == "docker-http-readonly-v1" {
		protocol: "docker-http"
		version:  "v1"
		endpoint: #DockerHTTPReadonlyEndpointV1
		scopes: [...#DockerHTTPReadonlyScopeV1]
		coLocation: "same-node-and-network"
	}
	if kind == "docker-socket-direct-v1" {
		protocol: "docker-engine"
		version:  "v1"
		endpoint: #DockerSocketDirectEndpointV1
		scopes: ["docker-api:full"]
		coLocation: "same-node"
	}
}

#ResolvedImplementationInterfaceBindingV2: {
	interfaceRef:        #ContractID
	moduleRef:           #ContractID
	unitRef:             #ContractID
	providerInstanceRef: #ContractID
	consumerInstanceRef: #ContractID
	siteRef:             #SiteID
	nodeRef:             #NodeID
	daemonRef:           #ContractID
	daemonInstanceRef:   #ContractID
	networkRef:          #ContractID
	networkInstanceRef:  #ContractID
	policyProfile:       #ContractID
	endpointRef:         #ContractID
}

// A runtime network is an instance-owned connectivity boundary. networkRef is
// only the logical contract label; networkInstanceRef and the complete owner
// tuple prove which concrete provider instance owns the boundary.
#ResolvedRuntimeNetworkOwnerV1: {
	moduleRef:    #ContractID
	unitRef:      #ContractID
	instanceRef:  #ContractID
	interfaceRef: #ContractID
}

#ResolvedRuntimeNetworkMemberV1: {
	role:         "provider" | "consumer"
	moduleRef:    #ContractID
	unitRef:      #ContractID
	instanceRef:  #ContractID
	interfaceRef: #ContractID
}

#ResolvedRuntimeNetworkInstanceV1: {
	id:                #ContractID
	networkRef:        #ContractID
	siteRef:           #SiteID
	nodeRef:           #NodeID
	daemonRef:         #ContractID
	daemonInstanceRef: #ContractID
	owner:             #ResolvedRuntimeNetworkOwnerV1
	members: [...#ResolvedRuntimeNetworkMemberV1] & list.MinItems(1)

	let ownerInstanceRef = owner.instanceRef
	let ownerInterfaceRef = owner.interfaceRef
	id: "\(ownerInstanceRef)-network-\(networkRef)-interface-\(ownerInterfaceRef)"

	_memberSubjectsUnique: list.UniqueItems([
		for member in members {"\(member.role)/\(member.moduleRef)/\(member.unitRef)/\(member.instanceRef)/\(member.interfaceRef)"},
	]) & true
	_providerMembersExact: [for member in members if member.role == "provider" {
		role: member.role
		matches: [
			if member.moduleRef == owner.moduleRef && member.unitRef == owner.unitRef && member.instanceRef == owner.instanceRef && member.interfaceRef == owner.interfaceRef {member.instanceRef},
		] & list.MinItems(1) & list.MaxItems(1)
	}] & list.MinItems(1) & list.MaxItems(1)
}

#ResolvedRuntimeNetworkBindingV1: {
	networkInstanceRef: #ContractID
	networkRef:         #ContractID
	role:               "provider" | "consumer"
	interfaceRef:       #ContractID
	siteRef:            #SiteID
	nodeRef:            #NodeID
	daemonRef:          #ContractID
	daemonInstanceRef:  #ContractID
	owner:              #ResolvedRuntimeNetworkOwnerV1
}

#ImplementationInterfaceRequirementV2: #ImplementationInterfaceRequirementShapeV2 & {
	providerBindings?: _|_
}

#ResolvedImplementationInterfaceRequirementV2: #ImplementationInterfaceRequirementShapeV2 & {
	kind: "docker-http-readonly-v1" | "docker-socket-direct-v1"
	if kind == "docker-http-readonly-v1" {
		providerBindings: [...#ResolvedImplementationInterfaceBindingV2] & list.MinItems(1)
		_providerProfileDrift: [
			for left in providerBindings
			for right in providerBindings
			if left.moduleRef != right.moduleRef || left.unitRef != right.unitRef || left.interfaceRef != right.interfaceRef || left.daemonRef != right.daemonRef || left.networkRef != right.networkRef || left.policyProfile != right.policyProfile || left.endpointRef != right.endpointRef {
				left:  "\(left.moduleRef)/\(left.unitRef)/\(left.interfaceRef)/\(left.daemonRef)/\(left.networkRef)/\(left.policyProfile)/\(left.endpointRef)"
				right: "\(right.moduleRef)/\(right.unitRef)/\(right.interfaceRef)/\(right.daemonRef)/\(right.networkRef)/\(right.policyProfile)/\(right.endpointRef)"
			},
		] & list.MaxItems(0)
		_bindingNodesUnique: list.UniqueItems([for binding in providerBindings {binding.nodeRef}]) & true
	}
	if kind == "docker-socket-direct-v1" {
		providerBindings?: _|_
	}
}

// A privileged interface approval governs only the exact Docker socket seam.
// It never grants host filesystem, root mount, process, or metrics observation;
// those are a separate authority category and cannot be inferred from this one.
#PrivilegedInterfaceApprovalV2: {
	id:            #ContractID
	kind:          "docker-socket-direct-v1"
	moduleRef:     #ContractID
	unitRef:       #ContractID
	providerRef:   #ContractID
	daemonRef:     #ContractID
	policyProfile: #ContractID
	reasonCode:    "provider-backing" | "lifecycle-owner"
	evidenceRef:   string & =~"^[^[:space:]]+$"
}

#ResolvedPrivilegedInterfaceApprovalV2: {
	id:            #ContractID
	kind:          "docker-socket-direct-v1"
	moduleRef:     #ContractID
	unitRef:       #ContractID
	providerRef:   #ContractID
	daemonRef:     #ContractID
	policyProfile: #ContractID
	reasonCode:    "provider-backing" | "lifecycle-owner"
	evidenceRef:   string & =~"^[^[:space:]]+$"
	siteRefs: [...#SiteID] & list.MinItems(1)
	nodeRefs: [...#NodeID] & list.MinItems(1)
	evidenceGateRef: #ContractID

	_siteRefsUnique: list.UniqueItems(siteRefs) & true
	_nodeRefsUnique: list.UniqueItems(nodeRefs) & true
}

// #CatalogPlanArtifactV2 is the CUE-owned metadata contract for the minimal
// set of artifacts that exists independently of module selection. Module
// output artifacts are deliberately forbidden here and must be declared by
// their owning #ModuleContractV2 instead.
#CatalogPlanArtifactV2: {
	id:       #ContractID
	kind:     #GenerationArtifactKindV2
	path:     #ArtifactPath
	format:   #GenerationArtifactFormatV2
	mode:     string & =~"^0[0-7]{3}$"
	required: true
	compatibleTargets: [...#GenerationTarget] & list.MinItems(1)

	_compatibleTargetsUnique: list.UniqueItems(compatibleTargets) & true
}

// #ModuleGenerationArtifactV2 owns the complete generation metadata and the
// exact render-unit output that materializes it. outputRef is also the path
// relative to generation.outputRoot; no Go-side artifact table may override
// these fields.
#ModuleGenerationArtifactV2: {
	id:       #ContractID
	kind:     #GenerationArtifactKindV2
	format:   #GenerationArtifactFormatV2
	mode:     string & =~"^0[0-7]{3}$"
	required: true
	compatibleTargets: [...#GenerationTarget] & list.MinItems(1)
	unitRef:   #ContractID
	outputRef: #ModuleArtifactOutputPathV2

	_compatibleTargetsUnique: list.UniqueItems(compatibleTargets) & true
	if kind == "opentofu" {
		compatibleTargets: [..."opentofu"]
	}
	if kind == "compose" {
		compatibleTargets: [..."compose"]
	}
}

// #ModuleRenderUnitContractV2 is one independently renderable, hash-bound
// implementation unit. rendererRef is exact: selection never falls back to a
// compatible engine or infers a renderer from kind/templateRef.
#ModuleRenderUnitContractV2: {
	id:           #ContractID
	kind:         #ModuleRenderUnitKindV2
	rendererRef:  #ContractID
	templateRef:  string & =~"^[^[:space:]]+$"
	version:      #SemanticVersion
	contractHash: #ContentHash

	publicInputRefs: [...#ContractID] | *[]
	secretInputRefs: [...#ContractID] | *[]
	outputs: [...#ModuleArtifactOutputPathV2] & list.MinItems(1)
	placement: #RenderUnitPlacementV2 | *{
		scope:       "module"
		cardinality: "single"
	}
	serviceEndpoints: [...#ModuleServiceEndpointV2] | *[]
	providesInterfaces: [...#ImplementationInterfaceProviderV2] | *[]
	requiresInterfaces: [...#ImplementationInterfaceRequirementV2] | *[]

	_publicInputRefsUnique: list.UniqueItems(publicInputRefs) & true
	_secretInputRefsUnique: list.UniqueItems(secretInputRefs) & true
	_providedInterfaceIDsUnique: list.UniqueItems([for contract in providesInterfaces {contract.id}]) & true
	_requiredInterfaceIDsUnique: list.UniqueItems([for contract in requiresInterfaces {contract.id}]) & true
	_inputKindsDisjoint: [for inputRef in publicInputRefs {
		input: inputRef
		matches: [for secretRef in secretInputRefs if secretRef == inputRef {secretRef}] & list.MaxItems(0)
	}]
	_outputsUnique: list.UniqueItems(outputs) & true
	_serviceRefsUnique: list.UniqueItems([for endpoint in serviceEndpoints {endpoint.serviceRef}]) & true
	if len(serviceEndpoints) > 0 {
		// Routable backends require concrete site/node/instance locality. A
		// module-scoped singleton has no executable network origin to publish.
		placement: scope: "node-local"
	}
	_directAndProxyDisjoint: [
		for directContract in requiresInterfaces
		if directContract.kind == "docker-socket-direct-v1"
		for proxyContract in requiresInterfaces
		if proxyContract.kind == "docker-http-readonly-v1" {
			direct: directContract.id
			proxy:  proxyContract.id
		},
	] & list.MaxItems(0)
	if len(providesInterfaces) > 0 {
		placement: {
			scope:       "node-local"
			cardinality: "one-per-daemon"
		}
		_providedDaemonMatchesPlacement: [for contract in providesInterfaces {
			interface: contract.id
			matches: [if placement.daemonRef == contract.daemonRef {contract.daemonRef}] & list.MinItems(1) & list.MaxItems(1)
		}]
	}
	if len(requiresInterfaces) > 0 {
		placement: scope: "node-local"
	}
	_directDaemonMatchesPlacement: [
		for contract in requiresInterfaces
		if contract.kind == "docker-socket-direct-v1" {
			interface: contract.id
			matches: [if placement.cardinality == "one-per-daemon" && placement.daemonRef == contract.daemonRef {contract.daemonRef}] & list.MinItems(1) & list.MaxItems(1)
		},
	]
}

// #ModuleRealizationSupportV2 separates a resolvable contract from an
// executable implementation. The level is a governed attestation, not a user
// preference. An umbrella contract can describe capability ownership for
// shadow planning, but can never make generation or apply ready.
#ModuleRealizationSupportV2: {
	contractVersion: "1.0.0"
	scope:           "umbrella" | "concrete"
	level:           "contract-only" | "generation-ready" | "apply-ready"
	compatibleRendererRefs: [...#ContractID] | *[]
	inputs: {
		// true means every input required by the implementation is represented
		// by requiredRefs; an empty requiredRefs list can then mean no inputs.
		contractComplete: bool
		requiredRefs: [...#ContractID] | *[]
	}
	artifacts: {
		requiredRefs: [...#ContractID] | *[]
		outputBindings: [...#ModuleArtifactOutputBindingV2] | *[]
		contracts: [...#ModuleGenerationArtifactV2] | *[]

		_outputBindingArtifactRefsUnique: list.UniqueItems([for binding in outputBindings {binding.artifactRef}]) & true
		_outputBindingUnitOutputsUnique: list.UniqueItems([for binding in outputBindings {"\(binding.unitRef)/\(binding.outputRef)"}]) & true
		_contractIDsUnique: list.UniqueItems([for contract in contracts {contract.id}]) & true
		_contractOutputsUnique: list.UniqueItems([for contract in contracts {contract.outputRef}]) & true
	}
	evidence: requiredRefs: [...string & =~"^[^[:space:]]+$"] | *[]

	_compatibleRendererRefsUnique: list.UniqueItems(compatibleRendererRefs) & true
	_inputRefsUnique:              list.UniqueItems(inputs.requiredRefs) & true
	_artifactRefsUnique:           list.UniqueItems(artifacts.requiredRefs) & true
	_evidenceRefsUnique:           list.UniqueItems(evidence.requiredRefs) & true

	if scope == "umbrella" {
		level: "contract-only"
	}
	if level != "contract-only" {
		scope:                  "concrete"
		compatibleRendererRefs: list.MinItems(1)
		inputs: contractComplete: true
		artifacts: requiredRefs:  list.MinItems(1)
	}
	if level == "apply-ready" {
		evidence: requiredRefs: list.MinItems(1)
	}
}

#ModuleArtifactOutputBindingV2: {
	artifactRef: #ContractID
	unitRef:     #ContractID
	outputRef:   #ModuleArtifactOutputPathV2
}

// #ModuleContractV2 is the governed provider-to-runtime seam. Capability
// providers reference these contracts by moduleRefs; a provider ID is never
// synthesized into a module ID.
#ModuleContractV2: {
	metadata: {
		id:          #ContractID
		version:     #SemanticVersion
		description: string & =~"^.+$"
	}
	providerRef: #ContractID
	provides: [...#CapabilityID] & list.MinItems(1)
	requires?: [...#ContractID] & list.MinItems(1)
	supportedSiteKinds: [...#SiteKind] & list.MinItems(1)
	nodeSelection?:       #ModuleNodeSelectionV2
	runtimeRequirements?: #ModuleRuntimeRequirementsV2
	runtime:              #ModuleRuntimeContractV2
	// inputDefaults is resolved once per module and then fanned out unchanged to
	// every render unit declaring the public input. Per-unit defaults are
	// intentionally forbidden so one logical input cannot resolve differently
	// across implementation units.
	inputDefaults?: #PublicSettings
	renderUnits: [...#ModuleRenderUnitContractV2] | *[]
	realizationSupport: #ModuleRealizationSupportV2
	health?: [...#HealthCheckContractV2]
	evidence?: [...string]

	_renderUnitIDsUnique: list.UniqueItems([for unit in renderUnits {unit.id}]) & true
	if requires != _|_ {
		_requiresUnique: list.UniqueItems(requires) & true
	}
	_renderUnitOutputsUnique: list.UniqueItems([for unit in renderUnits for outputRef in unit.outputs {outputRef}]) & true
	_moduleServiceRefsUnique: list.UniqueItems([for unit in renderUnits for endpoint in unit.serviceEndpoints {endpoint.serviceRef}]) & true
	_serviceHealthRefs: [for unit in renderUnits for endpoint in unit.serviceEndpoints {
		service: endpoint.serviceRef
		matches: [for healthContract in health if healthContract.id == endpoint.healthRef {healthContract.id}] & list.MinItems(1) & list.MaxItems(1)
	}]
	_moduleInterfaceAccessModesDisjoint: [
		for directUnit in renderUnits
		for directContract in directUnit.requiresInterfaces
		if directContract.kind == "docker-socket-direct-v1"
		for proxyUnit in renderUnits
		for proxyContract in proxyUnit.requiresInterfaces
		if proxyContract.kind == "docker-http-readonly-v1" {
			direct: "\(directUnit.id)/\(directContract.id)"
			proxy:  "\(proxyUnit.id)/\(proxyContract.id)"
		},
	] & list.MaxItems(0)
	_renderUnitInputKindsDisjoint: [for unit in renderUnits for publicRef in unit.publicInputRefs {
		unit:  unit.id
		input: publicRef
		matches: [for candidate in renderUnits for secretRef in candidate.secretInputRefs if secretRef == publicRef {candidate.id}] & list.MaxItems(0)
	}]
	_inputDefaultsPublic: [if inputDefaults != _|_ for key, _ in inputDefaults {
		input: key
		matches: [
			for unit in renderUnits
			for inputRef in unit.publicInputRefs
			if inputRef == key {unit.id},
		] & list.MinItems(1)
		secretMatches: [
			for unit in renderUnits
			for inputRef in unit.secretInputRefs
			if inputRef == key {unit.id},
		] & list.MaxItems(0)
	}]
	_renderUnitRequiredInputsDeclared: [for requiredRef in realizationSupport.inputs.requiredRefs {
		input: requiredRef
		matches: [
			for unit in renderUnits
			for inputRefs in [unit.publicInputRefs, unit.secretInputRefs]
			for inputRef in inputRefs
			if inputRef == requiredRef {unit.id},
		] & list.MinItems(1)
	}]
	_renderUnitSecretInputsRequired: [for unit in renderUnits for secretRef in unit.secretInputRefs {
		unit:  unit.id
		input: secretRef
		matches: [for requiredRef in realizationSupport.inputs.requiredRefs if requiredRef == secretRef {requiredRef}] & list.MinItems(1) & list.MaxItems(1)
	}]
	_renderUnitRenderersGoverned: [for unit in renderUnits {
		unit: unit.id
		matches: [for rendererRef in realizationSupport.compatibleRendererRefs if rendererRef == unit.rendererRef {rendererRef}] & list.MinItems(1) & list.MaxItems(1)
	}]
	_requiredArtifactOutputBindings: [for artifactRef in realizationSupport.artifacts.requiredRefs {
		artifact: artifactRef
		matches: [for binding in realizationSupport.artifacts.outputBindings if binding.artifactRef == artifactRef {"\(binding.unitRef)/\(binding.outputRef)"}] & list.MinItems(1) & list.MaxItems(1)
	}]
	_requiredArtifactContracts: [for artifactRef in realizationSupport.artifacts.requiredRefs {
		artifact: artifactRef
		matches: [for contract in realizationSupport.artifacts.contracts if contract.id == artifactRef {contract.id}] & list.MinItems(1) & list.MaxItems(1)
	}]
	_artifactContractsRequired: [for contract in realizationSupport.artifacts.contracts {
		artifact: contract.id
		matches: [for artifactRef in realizationSupport.artifacts.requiredRefs if artifactRef == contract.id {artifactRef}] & list.MinItems(1) & list.MaxItems(1)
	}]
	_artifactContractsOwned: [for contract in realizationSupport.artifacts.contracts {
		artifact: contract.id
		matches: [
			for binding in realizationSupport.artifacts.outputBindings
			if binding.artifactRef == contract.id && binding.unitRef == contract.unitRef && binding.outputRef == contract.outputRef {binding.artifactRef},
		] & list.MinItems(1) & list.MaxItems(1)
	}]
	_outputBindingsGoverned: [for binding in realizationSupport.artifacts.outputBindings {
		artifact: binding.artifactRef
		matches: [
			for contract in realizationSupport.artifacts.contracts
			if contract.id == binding.artifactRef && contract.unitRef == binding.unitRef && contract.outputRef == binding.outputRef {contract.id},
		] & list.MinItems(1) & list.MaxItems(1)
	}]
	_artifactKindsMatchUnits: [
		for contract in realizationSupport.artifacts.contracts
		if contract.kind == "opentofu" || contract.kind == "compose" || contract.kind == "native-config" {
			artifact: contract.id
			matches: [
				for unit in renderUnits
				if unit.id == contract.unitRef && unit.kind == contract.kind {unit.id},
			] & list.MinItems(1) & list.MaxItems(1)
		},
	]
	_renderUnitOutputArtifactBindings: [for unit in renderUnits for outputRef in unit.outputs {
		unit:   unit.id
		output: outputRef
		matches: [for binding in realizationSupport.artifacts.outputBindings if binding.unitRef == unit.id && binding.outputRef == outputRef {binding.artifactRef}] & list.MinItems(1) & list.MaxItems(1)
	}]
	_outputBindingArtifactRefs: [for binding in realizationSupport.artifacts.outputBindings {
		artifact: binding.artifactRef
		matches: [for artifactRef in realizationSupport.artifacts.requiredRefs if artifactRef == binding.artifactRef {artifactRef}] & list.MinItems(1) & list.MaxItems(1)
	}]
	_outputBindingUnitOutputs: [for binding in realizationSupport.artifacts.outputBindings {
		unit:   binding.unitRef
		output: binding.outputRef
		matches: [
			for unit in renderUnits
			if unit.id == binding.unitRef
			for outputRef in unit.outputs
			if outputRef == binding.outputRef {outputRef},
		] & list.MinItems(1) & list.MaxItems(1)
	}]
	if realizationSupport.scope == "umbrella" {
		renderUnits: list.MaxItems(0)
	}
	if realizationSupport.level != "contract-only" {
		renderUnits: list.MinItems(1)
		realizationSupport: artifacts: contracts: list.MinItems(1)
	}
}

#HealthCheckContractV2: {
	id:             #ContractID
	phase:          *"post-apply" | "pre-apply" | "continuous"
	kind:           "contract" | "http" | "tcp" | "container" | "process"
	path?:          string & =~"^/"
	port?:          int & >=1 & <=65535
	timeoutSeconds: int & >=1 & <=300 | *30
	expectedStatuses?: [...int & >=100 & <=599]

	if kind == "http" {
		path: string & =~"^/"
		port: int & >=1 & <=65535
		expectedStatuses: [...int & >=100 & <=599] & list.MinItems(1)
	}
	if kind == "tcp" {
		port: int & >=1 & <=65535
	}
}

// #ArchitectureV2CatalogContract closes the governed compiler catalog and
// verifies every cross-reference before any StackSpec can be resolved.
#ArchitectureV2CatalogContract: {
	capabilities: [...#CapabilityContract]
	providers: [...#CapabilityProvider]
	addons: [...#AddOnContract]
	modules: [...#ModuleContractV2] | *[]
	privilegedInterfaceApprovals: [...#PrivilegedInterfaceApprovalV2] | *[]
	planArtifacts: [...#CatalogPlanArtifactV2] & list.MinItems(1) & list.MaxItems(1) | *[{
		id:       "resolved-plan"
		kind:     "metadata"
		path:     ".stackkit/resolved-plan.json"
		format:   "json"
		mode:     "0600"
		required: true
		compatibleTargets: ["compose", "opentofu"]
	}]
	_planArtifactExact: [
		for artifact in planArtifacts
		if artifact.id == "resolved-plan" && artifact.kind == "metadata" && artifact.path == ".stackkit/resolved-plan.json" && artifact.format == "json" && artifact.mode == "0600" && artifact.required == true {artifact.id},
	] & list.MinItems(1) & list.MaxItems(1)

	integrity: uniqueness: {
		capabilityIDsUnique: list.UniqueItems([for contract in capabilities {contract.metadata.id}]) & true
		providerIDsUnique: list.UniqueItems([for contract in providers {contract.metadata.id}]) & true
		addonIDsUnique: list.UniqueItems([for contract in addons {contract.metadata.id}]) & true
		moduleIDsUnique: list.UniqueItems([for contract in modules {contract.metadata.id}]) & true
		providedInterfaceIDsUnique: list.UniqueItems([
			for module in modules
			for unit in module.renderUnits
			for contract in unit.providesInterfaces {contract.id},
		]) & true
		dockerProxyDaemonPoliciesUnique: list.UniqueItems([
			for module in modules
			for unit in module.renderUnits
			for contract in unit.providesInterfaces {"\(contract.daemonRef)/\(contract.policyProfile)"},
		]) & true
		privilegedApprovalIDsUnique: list.UniqueItems([for approval in privilegedInterfaceApprovals {approval.id}]) & true
		privilegedApprovalSubjectsUnique: list.UniqueItems([
			for approval in privilegedInterfaceApprovals {"\(approval.moduleRef)/\(approval.unitRef)/\(approval.daemonRef)/\(approval.policyProfile)"},
		]) & true
		moduleArtifactRefsUnique: list.UniqueItems([
			for contract in modules
			for binding in contract.realizationSupport.artifacts.outputBindings {binding.artifactRef},
		]) & true
		moduleArtifactOutputRefsUnique: list.UniqueItems([
			for contract in modules
			for binding in contract.realizationSupport.artifacts.outputBindings {binding.outputRef},
		]) & true
		generationArtifactIDsUnique: list.UniqueItems([
			for contract in planArtifacts {contract.id},
			for module in modules
			for contract in module.realizationSupport.artifacts.contracts {contract.id},
		]) & true
		generationArtifactPathsUnique: list.UniqueItems([
			for contract in planArtifacts {contract.path},
			for module in modules
			for contract in module.realizationSupport.artifacts.contracts {contract.outputRef},
		]) & true
		generationArtifactPortablePathsUnique: list.UniqueItems([
			for contract in planArtifacts {strings.ToLower(contract.path)},
			for module in modules
			for contract in module.realizationSupport.artifacts.contracts {strings.ToLower(contract.outputRef)},
		]) & true
	}
	_directInterfaceRequirementsApproved: [
		for module in modules
		for unit in module.renderUnits
		for requirement in unit.requiresInterfaces
		if requirement.kind == "docker-socket-direct-v1" {
			module:    module.metadata.id
			unit:      unit.id
			interface: requirement.id
			matches: [
				for approval in privilegedInterfaceApprovals
				if approval.moduleRef == module.metadata.id && approval.unitRef == unit.id && approval.providerRef == module.providerRef && approval.daemonRef == requirement.daemonRef && approval.policyProfile == requirement.policyProfile {approval.id},
			] & list.MinItems(1) & list.MaxItems(1)
		},
	]
	_privilegedApprovalsGovernDirectRequirements: [for approval in privilegedInterfaceApprovals {
		approval: approval.id
		matches: [
			for module in modules
			if module.metadata.id == approval.moduleRef && module.providerRef == approval.providerRef
			for unit in module.renderUnits
			if unit.id == approval.unitRef && unit.placement.scope == "node-local" && unit.placement.cardinality == "one-per-daemon" && unit.placement.daemonRef == approval.daemonRef
			for requirement in unit.requiresInterfaces
			if requirement.kind == approval.kind && requirement.daemonRef == approval.daemonRef && requirement.policyProfile == approval.policyProfile {requirement.id},
		] & list.MinItems(1) & list.MaxItems(1)
		evidenceMatches: [
			for module in modules
			if module.metadata.id == approval.moduleRef && module.evidence != _|_
			for evidenceRef in module.evidence
			if evidenceRef == approval.evidenceRef {evidenceRef},
		] & list.MinItems(1) & list.MaxItems(1)
		providerMatches: [for provider in providers if provider.metadata.id == approval.providerRef {provider.metadata.id}] & list.MinItems(1) & list.MaxItems(1)
		if approval.reasonCode == "provider-backing" {
			providerBackingMatches: [
				for module in modules
				if module.metadata.id == approval.moduleRef
				for unit in module.renderUnits
				if unit.id == approval.unitRef
				for contract in unit.providesInterfaces
				if contract.daemonRef == approval.daemonRef {contract.id},
			] & list.MinItems(1) & list.MaxItems(1)
		}
		if approval.reasonCode == "lifecycle-owner" {
			providerBackingMatches: [
				for module in modules
				if module.metadata.id == approval.moduleRef
				for unit in module.renderUnits
				if unit.id == approval.unitRef
				for contract in unit.providesInterfaces
				if contract.daemonRef == approval.daemonRef {contract.id},
			] & list.MaxItems(0)
		}
	}]
	_generationArtifactPathClosure: #ArtifactPathSetClosureV2 & {
		paths: [
			for contract in planArtifacts {contract.path},
			for module in modules
			for contract in module.realizationSupport.artifacts.contracts {contract.outputRef},
		]
	}

	integrity: providerCapabilities: [for providerContract in providers for capabilityRef in providerContract.provides {
		provider:   providerContract.metadata.id
		capability: capabilityRef
		matches: [for capability in capabilities if capability.metadata.id == capabilityRef {capability.metadata.id}] & list.MinItems(1) & list.MaxItems(1)
	}]
	integrity: providerRequirements: [for providerContract in providers if providerContract.requires != _|_ for requirement in providerContract.requires {
		provider:   providerContract.metadata.id
		capability: requirement.id
		matches: [for capability in capabilities if capability.metadata.id == requirement.id {capability.metadata.id}] & list.MinItems(1) & list.MaxItems(1)
	}]
	integrity: capabilityRequirements: [for capabilityContract in capabilities if capabilityContract.requires != _|_ for requirement in capabilityContract.requires {
		capability: capabilityContract.metadata.id
		requires:   requirement.id
		matches: [for candidate in capabilities if candidate.metadata.id == requirement.id {candidate.metadata.id}] & list.MinItems(1) & list.MaxItems(1)
	}]
	// integrity is deliberately a regular derived field: unlike package-scoped
	// hidden fields it remains an enforceable proof when this definition is
	// imported by the Go validator. Runtime decoders may discard the proof after
	// CUE has validated it, while generated catalog JSON retains the evidence.
	integrity: providerModuleOwnership: [
		for providerContract in providers
		for moduleRefs in [providerContract.realization.moduleRefs.required, providerContract.realization.moduleRefs.optional]
		for moduleRef in moduleRefs {
			provider: providerContract.metadata.id
			module:   moduleRef
			matches: [
				for module in modules
				if module.metadata.id == moduleRef && module.providerRef == providerContract.metadata.id {module.metadata.id},
			] & list.MinItems(1) & list.MaxItems(1)
		}]
	integrity: moduleCapabilities: [for moduleContract in modules for capabilityRef in moduleContract.provides {
		module:     moduleContract.metadata.id
		capability: capabilityRef
		matches: [for capability in capabilities if capability.metadata.id == capabilityRef {capability.metadata.id}] & list.MinItems(1) & list.MaxItems(1)
	}]
	integrity: moduleGovernance: [for moduleContract in modules {
		module:   moduleContract.metadata.id
		provider: moduleContract.providerRef
		matches: [for provider in providers if provider.metadata.id == moduleContract.providerRef {provider.metadata.id}] & list.MinItems(1) & list.MaxItems(1)
		governanceMatches: [
			for provider in providers
			if provider.metadata.id == moduleContract.providerRef
			for moduleRefs in [provider.realization.moduleRefs.required, provider.realization.moduleRefs.optional]
			for moduleRef in moduleRefs
			if moduleRef == moduleContract.metadata.id {moduleRef},
		] & list.MinItems(1) & list.MaxItems(1)
	}]
	integrity: moduleRequirements: [for moduleContract in modules if moduleContract.requires != _|_ for requirement in moduleContract.requires {
		module:   moduleContract.metadata.id
		requires: requirement
		matches: [for candidate in modules if candidate.metadata.id == requirement {candidate.metadata.id}] & list.MinItems(1) & list.MaxItems(1)
	}]
	integrity: addonCapabilities: [for addonContract in addons for capabilityRef in addonContract.provides {
		addon:      addonContract.metadata.id
		capability: capabilityRef
		matches: [for capability in capabilities if capability.metadata.id == capabilityRef {capability.metadata.id}] & list.MinItems(1) & list.MaxItems(1)
	}]
	integrity: addonRequirements: [for addonContract in addons if addonContract.requires != _|_ for requirement in addonContract.requires {
		addon:      addonContract.metadata.id
		capability: requirement.id
		matches: [for capability in capabilities if capability.metadata.id == requirement.id {capability.metadata.id}] & list.MinItems(1) & list.MaxItems(1)
	}]
	integrity: defaultProviderUniqueness: [
		for capabilityContract in capabilities
		for candidateSiteKind in ["home", "cloud"] {
			capability: capabilityContract.metadata.id
			siteKind:   candidateSiteKind
			matches: [
				for provider in providers
				if provider.selection != _|_
				for defaultKind in provider.selection.defaultForSiteKinds
				if defaultKind == candidateSiteKind
				for provided in provider.provides
				if provided == capabilityContract.metadata.id {provider.metadata.id},
			] & list.MaxItems(1)
		},
	]
}

#HAAddOnAvailabilityContractV2: {
	policyAuthority: "kit-definition"
	supportedModes: [...("warm-standby" | "quorum")] & list.MinItems(2) & list.MaxItems(2)
	selector:              "control-plane-members"
	_supportedModesUnique: list.UniqueItems(supportedModes) & true
}

#AddOnContract: {
	metadata: {
		id:          #ContractID
		version:     string
		description: string
	}
	supportedKits: [...#KitSlug] & list.MinItems(1)
	provides: [...#CapabilityID] & list.MinItems(1)
	requires?: [...#CapabilityRequirement]
	conflicts?: [...#ContractID]
	availability?: #HAAddOnAvailabilityContractV2
}

// Inventory is a separate compiler input. Facts validate an intent; they never
// select or mutate the kit.
#InventoryFacts: {
	schemaVersion: "stackkit.inventory/v1"
	nodes: [#NodeID]: {
		observedSiteKind?: #SiteKind
		arch?:             "amd64" | "arm64"
		cpuCores?:         int & >=1
		ramGB?:            int & >=1
		storageGB?:        int & >=1
		publicAddress?:    string
		privateAddress?:   string
		virtualization?:   #RuntimeVirtualizationV2
		provider?:         string
		runtimeDaemons: {
			[#ContractID]: #RuntimeDaemonFactV1
		} | *{}

		_runtimeDaemonInstancesUnique: list.UniqueItems([for _, daemon in runtimeDaemons {daemon.instanceRef}]) & true
		_runtimeDaemonSocketsUnique: list.UniqueItems([for _, daemon in runtimeDaemons {daemon.socketPath}]) & true
	}
}

#ResolvedCapability: {
	id:           #CapabilityID
	providerRef?: #ContractID
	contractHash: #ContentHash
	settings?:    #PublicSettings & struct.MinFields(1)
	secretRefs?: {
		[string]: #SecretReference
	} & struct.MinFields(1)
}

#ResolvedProvider: {
	id:           #ContractID
	version:      string
	contractHash: #ContentHash
	provides: [...#CapabilityID] & list.MinItems(1)
	siteRefs: [...#SiteID] & list.MinItems(1)
	realization: #ProviderRealizationContractV2
	owner?:      #ResolvedProviderOwnerV2

	if realization.kind == "host" || realization.kind == "external" {
		owner: {
			ref:                realization.ownerRef
			kind:               realization.kind
			version:            version
			contractHash:       contractHash
			realizationSupport: realization.realizationSupport
		}
	}
	if realization.kind == "none" || realization.kind == "modules" {
		owner?: _|_
	}
}

#ResolvedProviderOwnerV2: {
	ref:                #ContractID
	kind:               "host" | "external"
	version:            string & =~"^.+$"
	contractHash:       #ContentHash
	realizationSupport: #ModuleRealizationSupportV2
	inputs: {
		values: #PublicSettings
		secretRefs: [string]: #SecretReference
	}
	healthGateRefs: [...#ContractID] & list.MinItems(1)
	evidenceGateRefs: [...#ContractID] & list.MinItems(1)
}

#ResolvedModuleRenderInstanceOutputV2: {
	// ref remains the immutable logical output declared by the catalog render
	// unit. artifactRef and path are the exact materialization owned by this
	// execution instance.
	ref:         #ModuleArtifactOutputPathV2
	artifactRef: #ContractID
	path:        #ModuleArtifactOutputPathV2
}

#ResolvedModuleRenderUnitInstanceV2: {
	id:    #ContractID
	scope: "module" | "node-local"

	siteRef?:           #SiteID
	nodeRef?:           #NodeID
	daemonRef?:         #ContractID
	daemonInstanceRef?: #ContractID
	daemonEngine?:      "docker"
	daemonSocketPath?:  #UnixSocketPath
	networkBindings!: [...#ResolvedRuntimeNetworkBindingV1]
	outputs: [...#ResolvedModuleRenderInstanceOutputV2] & list.MinItems(1)

	_networkBindingSubjectsUnique: list.UniqueItems([
		for binding in networkBindings {"\(binding.networkInstanceRef)/\(binding.role)/\(binding.interfaceRef)"},
	]) & true
	_outputRefsUnique: list.UniqueItems([for output in outputs {output.ref}]) & true
	_outputArtifactRefsUnique: list.UniqueItems([for output in outputs {output.artifactRef}]) & true
	_outputPathsUnique: list.UniqueItems([for output in outputs {output.path}]) & true

	// Daemon identity is an atomic projection. A persisted plan may not carry
	// only part of the observed daemon binding.
	if daemonRef != _|_ {
		daemonInstanceRef: #ContractID
		daemonEngine:      "docker"
		daemonSocketPath:  #UnixSocketPath
	}
	if daemonInstanceRef != _|_ {
		daemonRef:        #ContractID
		daemonEngine:     "docker"
		daemonSocketPath: #UnixSocketPath
	}
	if daemonEngine != _|_ {
		daemonRef:         #ContractID
		daemonInstanceRef: #ContractID
		daemonSocketPath:  #UnixSocketPath
	}
	if daemonSocketPath != _|_ {
		daemonRef:         #ContractID
		daemonInstanceRef: #ContractID
		daemonEngine:      "docker"
	}

	if scope == "module" {
		siteRef?:           _|_
		nodeRef?:           _|_
		daemonRef?:         _|_
		daemonInstanceRef?: _|_
		daemonEngine?:      _|_
		daemonSocketPath?:  _|_
		networkBindings: []
	}
	if scope == "node-local" {
		siteRef: #SiteID
		nodeRef: #NodeID
	}
}

#ResolvedModuleRenderUnitV2: {
	id:           #ContractID
	kind:         #ModuleRenderUnitKindV2
	rendererRef:  #ContractID
	templateRef:  string & =~"^[^[:space:]]+$"
	version:      #SemanticVersion
	contractHash: #ContentHash

	publicInputRefs: [...#ContractID] | *[]
	secretInputRefs: [...#ContractID] | *[]
	values: #PublicSettings
	secretRefs: [string]: #SecretReference
	outputs: [...#ModuleArtifactOutputPathV2] & list.MinItems(1)
	siteRefs: [...#SiteID] & list.MinItems(1)
	nodeRefs: [...#NodeID] & list.MinItems(1)
	placement: #RenderUnitPlacementV2
	daemonBindings: [...#ResolvedRuntimeDaemonBindingV1] | *[]
	instances: [...#ResolvedModuleRenderUnitInstanceV2] & list.MinItems(1)
	serviceEndpoints: [...#ModuleServiceEndpointV2] | *[]
	providesInterfaces: [...#ImplementationInterfaceProviderV2] | *[]
	requiresInterfaces: [...#ResolvedImplementationInterfaceRequirementV2] | *[]
	let unitID = id
	let eligibleSiteRefs = siteRefs
	instances: [...{
		scope:    "module" | "node-local"
		siteRef?: #SiteID
		if scope == "node-local" {
			siteRef: #SiteID
			let instanceSiteRef = siteRef
			_siteOwnedExact: [
				for eligibleSiteRef in eligibleSiteRefs
				if eligibleSiteRef == instanceSiteRef {eligibleSiteRef},
			] & list.MinItems(1) & list.MaxItems(1)
		}
	}]

	_publicInputRefsUnique: list.UniqueItems(publicInputRefs) & true
	_secretInputRefsUnique: list.UniqueItems(secretInputRefs) & true
	_siteRefsUnique:        list.UniqueItems(siteRefs) & true
	_nodeRefsUnique:        list.UniqueItems(nodeRefs) & true
	_providedInterfaceIDsUnique: list.UniqueItems([for contract in providesInterfaces {contract.id}]) & true
	_requiredInterfaceIDsUnique: list.UniqueItems([for contract in requiresInterfaces {contract.id}]) & true
	_inputKindsDisjoint: [for inputRef in publicInputRefs {
		input: inputRef
		matches: [for secretRef in secretInputRefs if secretRef == inputRef {secretRef}] & list.MaxItems(0)
	}]
	_valuesDeclared: [for key, _ in values {
		input: key
		matches: [for inputRef in publicInputRefs if inputRef == key {inputRef}] & list.MinItems(1) & list.MaxItems(1)
	}]
	_secretRefsDeclared: [for key, _ in secretRefs {
		input: key
		matches: [for inputRef in secretInputRefs if inputRef == key {inputRef}] & list.MinItems(1) & list.MaxItems(1)
	}]
	_outputsUnique: list.UniqueItems(outputs) & true
	_instanceIDsUnique: list.UniqueItems([for instance in instances {instance.id}]) & true
	_instanceArtifactRefsUnique: list.UniqueItems([
		for instance in instances
		for output in instance.outputs {output.artifactRef},
	]) & true
	_instancePathsUnique: list.UniqueItems([
		for instance in instances
		for output in instance.outputs {output.path},
	]) & true
	_serviceRefsUnique: list.UniqueItems([for endpoint in serviceEndpoints {endpoint.serviceRef}]) & true
	_directAndProxyDisjoint: [
		for directContract in requiresInterfaces
		if directContract.kind == "docker-socket-direct-v1"
		for proxyContract in requiresInterfaces
		if proxyContract.kind == "docker-http-readonly-v1" {
			direct: directContract.id
			proxy:  proxyContract.id
		},
	] & list.MaxItems(0)
	if placement.scope == "node-local" && placement.cardinality == "single" {
		nodeRefs: list.MaxItems(1)
	}
	if placement.scope == "module" {
		instances: [#ResolvedModuleRenderUnitInstanceV2 & {
			id:    "\(unitID)-logical"
			scope: "module"
		}]
		_moduleInstanceExact: [for instance in instances {
			matches: [if instance.scope == "module" && instance.id == "\(unitID)-logical" {instance.id}] & list.MinItems(1) & list.MaxItems(1)
		}]
	}
	if placement.scope == "node-local" && placement.cardinality != "one-per-daemon" {
		instances: [for nodeRef in nodeRefs {#ResolvedModuleRenderUnitInstanceV2 & {
			id:                 "\(unitID)-node-\(nodeRef)"
			scope:              "node-local"
			nodeRef:            nodeRef
			daemonRef?:         _|_
			daemonInstanceRef?: _|_
			daemonEngine?:      _|_
			daemonSocketPath?:  _|_
		}}]
		_nodeInstanceCountExact: len(instances) & len(nodeRefs)
		_nodeInstancesExact: [for nodeRef in nodeRefs {
			node: nodeRef
			matches: [
				for instance in instances
				if instance.nodeRef == nodeRef && instance.id == "\(unitID)-node-\(nodeRef)" {instance.id},
			] & list.MinItems(1) & list.MaxItems(1)
		}]
		_nodeInstancesOwned: [for instance in instances {
			instance: instance.id
			idMatches: [if instance.id == "\(unitID)-node-\(instance.nodeRef)" {instance.id}] & list.MinItems(1) & list.MaxItems(1)
			nodeMatches: [for nodeRef in nodeRefs if nodeRef == instance.nodeRef {nodeRef}] & list.MinItems(1) & list.MaxItems(1)
			siteMatches: [for siteRef in siteRefs if siteRef == instance.siteRef {siteRef}] & list.MinItems(1) & list.MaxItems(1)
		}]
	}
	if placement.cardinality == "one-per-daemon" {
		daemonBindings: [...#ResolvedRuntimeDaemonBindingV1] & list.MinItems(1)
		instances: [for binding in daemonBindings {#ResolvedModuleRenderUnitInstanceV2 & {
			id:                "\(unitID)-node-\(binding.nodeRef)-daemon-\(binding.instanceRef)"
			scope:             "node-local"
			siteRef:           binding.siteRef
			nodeRef:           binding.nodeRef
			daemonRef:         binding.daemonRef
			daemonInstanceRef: binding.instanceRef
			daemonEngine:      binding.engine
			daemonSocketPath:  binding.socketPath
		}}]
		_daemonInstanceCountExact: len(instances) & len(daemonBindings)
		_daemonBindingNodesUnique: list.UniqueItems([for binding in daemonBindings {binding.nodeRef}]) & true
		_daemonBindingInstancesUnique: list.UniqueItems([for binding in daemonBindings {binding.instanceRef}]) & true
		_daemonBindingRefMatchesPlacement: [for binding in daemonBindings {
			node: binding.nodeRef
			matches: [if binding.daemonRef == placement.daemonRef {binding.daemonRef}] & list.MinItems(1) & list.MaxItems(1)
		}]
		_daemonInstancesExact: [for binding in daemonBindings {
			binding: binding.instanceRef
			matches: [
				for instance in instances
				if instance.siteRef == binding.siteRef && instance.nodeRef == binding.nodeRef && instance.daemonRef == binding.daemonRef && instance.daemonInstanceRef == binding.instanceRef && instance.daemonEngine == binding.engine && instance.daemonSocketPath == binding.socketPath && instance.id == "\(unitID)-node-\(binding.nodeRef)-daemon-\(binding.instanceRef)" {instance.id},
			] & list.MinItems(1) & list.MaxItems(1)
		}]
		_daemonInstancesOwned: [for instance in instances {
			instance: instance.id
			idMatches: [if instance.id == "\(unitID)-node-\(instance.nodeRef)-daemon-\(instance.daemonInstanceRef)" {instance.id}] & list.MinItems(1) & list.MaxItems(1)
			matches: [
				for binding in daemonBindings
				if instance.siteRef == binding.siteRef && instance.nodeRef == binding.nodeRef && instance.daemonRef == binding.daemonRef && instance.daemonInstanceRef == binding.instanceRef && instance.daemonEngine == binding.engine && instance.daemonSocketPath == binding.socketPath {binding.instanceRef},
			] & list.MinItems(1) & list.MaxItems(1)
		}]
	}
	if placement.cardinality != "one-per-daemon" {
		daemonBindings: []
	}
	if len(providesInterfaces) > 0 {
		placement: {
			scope:       "node-local"
			cardinality: "one-per-daemon"
		}
		_providedDaemonMatchesPlacement: [for contract in providesInterfaces {
			interface: contract.id
			matches: [if placement.daemonRef == contract.daemonRef {contract.daemonRef}] & list.MinItems(1) & list.MaxItems(1)
		}]
	}
	if len(requiresInterfaces) > 0 {
		placement: scope: "node-local"
	}
	_directDaemonMatchesPlacement: [
		for contract in requiresInterfaces
		if contract.kind == "docker-socket-direct-v1" {
			interface: contract.id
			matches: [if placement.cardinality == "one-per-daemon" && placement.daemonRef == contract.daemonRef {contract.daemonRef}] & list.MinItems(1) & list.MaxItems(1)
		},
	]
	_directSocketPathsMatchBindings: [
		for contract in requiresInterfaces
		if contract.kind == "docker-socket-direct-v1" && contract.endpoint.path != _|_
		for binding in daemonBindings {
			interface: contract.id
			node:      binding.nodeRef
			matches: [if contract.endpoint.path == binding.socketPath {binding.instanceRef}] & list.MinItems(1) & list.MaxItems(1)
		},
	]
}

#ResolvedModuleV2: {
	id:           #ContractID
	version:      #SemanticVersion
	contractHash: #ContentHash
	providerRef:  #ContractID
	provides: [...#CapabilityID] & list.MinItems(1)
	siteRefs: [...#SiteID] & list.MinItems(1)
	nodeRefs: [...#NodeID] & list.MinItems(1)
	requires?: [...#ContractID] & list.MinItems(1)
	nodeSelection?:       #ModuleNodeSelectionV2
	runtimeRequirements?: #ModuleRuntimeRequirementsV2
	runtime:              #ModuleRuntimeContractV2
	renderUnits: [...#ResolvedModuleRenderUnitV2] | *[]
	realizationSupport: #ModuleRealizationSupportV2
	// Gate references are currently projected only onto explicit host/external
	// provider owners. The compiler emits no module-level refs, so accepting
	// caller-authored values here would create unsigned semantic metadata.
	healthGateRefs?:   _|_
	evidenceGateRefs?: _|_
	let moduleID = id
	if requires != _|_ {
		_requiresUnique: list.UniqueItems(requires) & true
	}

	// Instance outputs are a materialized projection of the logical unit
	// output-binding contract. Keeping these constraints on each concrete
	// output prevents an unevaluated aggregate comprehension from accepting a
	// rehashed but semantically widened plan.
	renderUnits: [...{
		id:        #ContractID
		placement: #RenderUnitPlacementV2
		outputs: [...#ModuleArtifactOutputPathV2] & list.MinItems(1)
		let unitID = id
		let unitScope = placement.scope
		let logicalOutputs = outputs
		instances: [...{
			id: #ContractID
			outputs: [...#ResolvedModuleRenderInstanceOutputV2] & list.MinItems(1)
			let instanceID = id
			_instanceOutputCountExact: len(outputs) & len(logicalOutputs)
			outputs: [...{
				ref:         #ModuleArtifactOutputPathV2
				artifactRef: #ContractID
				path:        #ModuleArtifactOutputPathV2
				let logicalOutputRef = ref
				let logicalArtifactRefs = [
					for binding in realizationSupport.artifacts.outputBindings
					if binding.unitRef == unitID && binding.outputRef == logicalOutputRef {binding.artifactRef},
				]
				_logicalArtifactRefExact: logicalArtifactRefs & list.MinItems(1) & list.MaxItems(1)
				if len(logicalArtifactRefs) == 1 && unitScope == "module" {
					artifactRef: logicalArtifactRefs[0]
					path:        logicalOutputRef
				}
				if len(logicalArtifactRefs) == 1 && unitScope == "node-local" {
					artifactRef: "\(logicalArtifactRefs[0])-instance-\(instanceID)"
					path:        "instances/\(moduleID)/\(instanceID)/\(logicalOutputRef)"
				}
			}]
		}]
	}]

	_nodeLocalSingleRequiresExactModuleNode: [
		for unit in renderUnits
		if unit.placement.scope == "node-local" && unit.placement.cardinality == "single" {
			unit:            unit.id
			moduleNodeCount: len(nodeRefs) & 1
			unitNodeCount:   len(unit.nodeRefs) & 1
			matches: [for moduleNodeRef in nodeRefs for unitNodeRef in unit.nodeRefs if moduleNodeRef == unitNodeRef {moduleNodeRef}] & list.MinItems(1) & list.MaxItems(1)
		},
	]

	_requiredArtifactContracts: [for artifactRef in realizationSupport.artifacts.requiredRefs {
		artifact: artifactRef
		matches: [for contract in realizationSupport.artifacts.contracts if contract.id == artifactRef {contract.id}] & list.MinItems(1) & list.MaxItems(1)
	}]
	_artifactContractsRequired: [for contract in realizationSupport.artifacts.contracts {
		artifact: contract.id
		matches: [for artifactRef in realizationSupport.artifacts.requiredRefs if artifactRef == contract.id {artifactRef}] & list.MinItems(1) & list.MaxItems(1)
	}]
	_artifactContractsOwned: [for contract in realizationSupport.artifacts.contracts {
		artifact: contract.id
		matches: [
			for binding in realizationSupport.artifacts.outputBindings
			if binding.artifactRef == contract.id && binding.unitRef == contract.unitRef && binding.outputRef == contract.outputRef {binding.artifactRef},
		] & list.MinItems(1) & list.MaxItems(1)
	}]
	_outputBindingsGoverned: [for binding in realizationSupport.artifacts.outputBindings {
		artifact: binding.artifactRef
		matches: [
			for contract in realizationSupport.artifacts.contracts
			if contract.id == binding.artifactRef && contract.unitRef == binding.unitRef && contract.outputRef == binding.outputRef {contract.id},
		] & list.MinItems(1) & list.MaxItems(1)
	}]
	_artifactKindsMatchUnits: [
		for contract in realizationSupport.artifacts.contracts
		if contract.kind == "opentofu" || contract.kind == "compose" || contract.kind == "native-config" {
			artifact: contract.id
			matches: [
				for unit in renderUnits
				if unit.id == contract.unitRef && unit.kind == contract.kind {unit.id},
			] & list.MinItems(1) & list.MaxItems(1)
		},
	]

	_renderUnitIDsUnique: list.UniqueItems([for unit in renderUnits {unit.id}]) & true
	_renderUnitOutputsUnique: list.UniqueItems([for unit in renderUnits for outputRef in unit.outputs {outputRef}]) & true
	_moduleServiceRefsUnique: list.UniqueItems([for unit in renderUnits for endpoint in unit.serviceEndpoints {endpoint.serviceRef}]) & true
	_moduleInterfaceAccessModesDisjoint: [
		for directUnit in renderUnits
		for directContract in directUnit.requiresInterfaces
		if directContract.kind == "docker-socket-direct-v1"
		for proxyUnit in renderUnits
		for proxyContract in proxyUnit.requiresInterfaces
		if proxyContract.kind == "docker-http-readonly-v1" {
			direct: "\(directUnit.id)/\(directContract.id)"
			proxy:  "\(proxyUnit.id)/\(proxyContract.id)"
		},
	] & list.MaxItems(0)
	_renderUnitInputKindsDisjoint: [for unit in renderUnits for publicRef in unit.publicInputRefs {
		unit:  unit.id
		input: publicRef
		matches: [for candidate in renderUnits for secretRef in candidate.secretInputRefs if secretRef == publicRef {candidate.id}] & list.MaxItems(0)
	}]
	_renderUnitRequiredInputsDeclared: [for requiredRef in realizationSupport.inputs.requiredRefs {
		input: requiredRef
		matches: [
			for unit in renderUnits
			for inputRefs in [unit.publicInputRefs, unit.secretInputRefs]
			for inputRef in inputRefs
			if inputRef == requiredRef {unit.id},
		] & list.MinItems(1)
	}]
	_renderUnitSecretInputsRequired: [for unit in renderUnits for secretRef in unit.secretInputRefs {
		unit:  unit.id
		input: secretRef
		matches: [for requiredRef in realizationSupport.inputs.requiredRefs if requiredRef == secretRef {requiredRef}] & list.MinItems(1) & list.MaxItems(1)
	}]
	_renderUnitRenderersGoverned: [for unit in renderUnits {
		unit: unit.id
		matches: [for rendererRef in realizationSupport.compatibleRendererRefs if rendererRef == unit.rendererRef {rendererRef}] & list.MinItems(1) & list.MaxItems(1)
	}]
	_requiredArtifactOutputBindings: [for artifactRef in realizationSupport.artifacts.requiredRefs {
		artifact: artifactRef
		matches: [for binding in realizationSupport.artifacts.outputBindings if binding.artifactRef == artifactRef {"\(binding.unitRef)/\(binding.outputRef)"}] & list.MinItems(1) & list.MaxItems(1)
	}]
	_renderUnitOutputArtifactBindings: [for unit in renderUnits for outputRef in unit.outputs {
		unit:   unit.id
		output: outputRef
		matches: [for binding in realizationSupport.artifacts.outputBindings if binding.unitRef == unit.id && binding.outputRef == outputRef {binding.artifactRef}] & list.MinItems(1) & list.MaxItems(1)
	}]
	_outputBindingArtifactRefs: [for binding in realizationSupport.artifacts.outputBindings {
		artifact: binding.artifactRef
		matches: [for artifactRef in realizationSupport.artifacts.requiredRefs if artifactRef == binding.artifactRef {artifactRef}] & list.MinItems(1) & list.MaxItems(1)
	}]
	_outputBindingUnitOutputs: [for binding in realizationSupport.artifacts.outputBindings {
		unit:   binding.unitRef
		output: binding.outputRef
		matches: [
			for unit in renderUnits
			if unit.id == binding.unitRef
			for outputRef in unit.outputs
			if outputRef == binding.outputRef {outputRef},
		] & list.MinItems(1) & list.MaxItems(1)
	}]
	_renderInstanceOutputsRequired: [
		for unit in renderUnits
		for instance in unit.instances
		for binding in realizationSupport.artifacts.outputBindings
		if binding.unitRef == unit.id {
			unit:     unit.id
			instance: instance.id
			output:   binding.outputRef
			matches: [
				for output in instance.outputs
				if unit.placement.scope == "module" && output.ref == binding.outputRef && output.artifactRef == binding.artifactRef && output.path == binding.outputRef {output.artifactRef},
				for output in instance.outputs
				if unit.placement.scope == "node-local" && output.ref == binding.outputRef && output.artifactRef == "\(binding.artifactRef)-instance-\(instance.id)" && output.path == path.Join(["instances", id, instance.id, binding.outputRef]) {output.artifactRef},
			] & list.MinItems(1) & list.MaxItems(1)
		},
	]
	_renderInstanceOutputsGoverned: [
		for unit in renderUnits
		for instance in unit.instances
		for output in instance.outputs {
			unit:     unit.id
			instance: instance.id
			output:   output.ref
			matches: [
				for binding in realizationSupport.artifacts.outputBindings
				if unit.placement.scope == "module" && binding.unitRef == unit.id && output.ref == binding.outputRef && output.artifactRef == binding.artifactRef && output.path == binding.outputRef {binding.artifactRef},
				for binding in realizationSupport.artifacts.outputBindings
				if unit.placement.scope == "node-local" && binding.unitRef == unit.id && output.ref == binding.outputRef && output.artifactRef == "\(binding.artifactRef)-instance-\(instance.id)" && output.path == path.Join(["instances", id, instance.id, binding.outputRef]) {binding.artifactRef},
			] & list.MinItems(1) & list.MaxItems(1)
		},
	]

	if realizationSupport.scope == "umbrella" {
		renderUnits: list.MaxItems(0)
	}
	if realizationSupport.level != "contract-only" {
		renderUnits: list.MinItems(1)
		realizationSupport: artifacts: contracts: list.MinItems(1)
	}
}

#ResolvedPlacementV2: {
	workloadRef: #ContractID
	siteRefs: [...#SiteID] & list.MinItems(1)
	nodeRefs: [...#NodeID] & list.MinItems(1)
	// Until placement explanations have a separately governed reason-code
	// contract, this is the sole deterministic compiler projection.
	reason: "matched explicit site, node, and role constraints"
}

#ResolvedIdentityPlan: {
	humanAuthoritySiteRef:   #SiteID
	deviceAuthoritySiteRef?: #SiteID
	edgeVerifierSiteRefs?: [...#SiteID]
	deviceEnrollment?:       #DeviceEnrollmentPolicy
	possessionBoundSessions: true
	lanLocationIsIdentity:   false
}

#ResolvedInstallPlanV2: {
	mode:    "bootstrapped" | "bare" | "advanced"
	runtime: "docker" | "native"
	platform: {
		management:      "selected-provider" | "standalone" | "native"
		providerRef?:    #ContractID
		fallbackAllowed: bool
		setupPolicy: {
			platform:           "automatic" | "on-demand" | "manual"
			applicationDefault: "automatic" | "on-demand" | "manual"
		}
	}
	if runtime == "native" {
		platform: management: "native"
	}
	if platform.management == "selected-provider" {
		platform: providerRef: #ContractID
	}
	if platform.management != "standalone" {
		platform: fallbackAllowed: false
	}
}

#CompatibilityPlanV2: {
	minCLI:         #SemanticVersion
	minRuntime:     #SemanticVersion
	minGenerator:   #SemanticVersion
	specAPIVersion: #ArchitectureAPIVersion
	planAPIVersion: "stackkit.resolved-plan/v1"
}

#GeneratedArtifactOwnerV2: {
	kind:         "plan" | "render-instance"
	moduleRef?:   #ContractID
	unitRef?:     #ContractID
	instanceRef?: #ContractID
	outputRef?:   #ModuleArtifactOutputPathV2

	if kind == "plan" {
		moduleRef?:   _|_
		unitRef?:     _|_
		instanceRef?: _|_
		outputRef?:   _|_
	}
	if kind == "render-instance" {
		moduleRef:   #ContractID
		unitRef:     #ContractID
		instanceRef: #ContractID
		outputRef:   #ModuleArtifactOutputPathV2
	}
}

#GeneratedArtifactContractV2: {
	id:       #ContractID
	kind:     #GenerationArtifactKindV2
	path:     #ArtifactPath
	format:   #GenerationArtifactFormatV2
	mode:     string & =~"^0[0-7]{3}$"
	required: bool | *true
	owner:    #GeneratedArtifactOwnerV2
}

#ResolvedGenerationPlanV2: {
	contractVersion:     #SemanticVersion
	strategy:            #GenerationStrategy
	target:              #GenerationTarget
	outputRoot:          #OutputRootPath
	profileContractHash: #ContentHash
	renderer: {
		id:      #ContractID
		version: string & =~"^.+$"
	}
	artifacts: [...#GeneratedArtifactContractV2] & list.MinItems(1)
	rawSpecFallback: false
	_artifactIDsUnique: list.UniqueItems([for artifact in artifacts {artifact.id}]) & true
	_artifactPathsUnique: list.UniqueItems([for artifact in artifacts {artifact.path}]) & true
	_artifactPortablePathsUnique: list.UniqueItems([for artifact in artifacts {strings.ToLower(artifact.path)}]) & true
	_artifactPathClosure: #ArtifactPathSetClosureV2 & {
		paths: [for artifact in artifacts {artifact.path}]
	}
	_reservedControlArtifacts: [
		for artifact in artifacts
		if strings.ToLower(artifact.path) == strings.ToLower(path.Join([outputRoot, ".stackkit/generation-manifest.json"])) || strings.ToLower(artifact.path) == strings.ToLower(path.Join([outputRoot, ".stackkit/generation-receipt.json"])) {artifact.id},
	] & list.MaxItems(0)
	_resolvedPlanArtifact: [
		for artifact in artifacts
		if artifact.id == "resolved-plan" && artifact.kind == "metadata" && artifact.path == path.Join([outputRoot, ".stackkit/resolved-plan.json"]) && artifact.format == "json" && artifact.mode == "0600" && artifact.required == true && artifact.owner.kind == "plan" {artifact.id},
	] & list.MinItems(1) & list.MaxItems(1)
}

#ResolvedNetworkPlanV2: {
	configuration: #NetworkIntentV2
	routes: [...#ResolvedServiceRouteV2] | *[]
	_routeIDsUnique: list.UniqueItems([for route in routes {route.id}]) & true
}

#ResolvedSystemPlanV2: {
	host:       #SystemIntentV2
	container?: #ContainerRuntimeIntentV2
}

#ResolvedSourceLineageV2: {
	kind: "native-v2" | "migrated-v1" | "registry"
	ref?: string & =~"^[^[:space:]]+$"
	intent: {
		apiVersion: string & =~"^stackkit/.+$"
		hash:       #ContentHash
	}
	normalizedSpec: {
		apiVersion: #ArchitectureAPIVersion
		hash:       #ContentHash
	}
	inventory: {
		apiVersion: "stackkit.inventory/v1"
		hash:       #ContentHash
	}
	kitDefinitionHash: #ContentHash
	migration?:        #MigrationSourceLineage

	if kind == "native-v2" || kind == "registry" {
		migration?: _|_
	}
	if kind == "migrated-v1" {
		migration: #MigrationSourceLineage
	}
}

#ResolvedHealthGateV2: {
	id:             #ContractID
	phase:          "pre-apply" | "post-apply" | "continuous"
	kind:           "contract" | "http" | "tcp" | "container" | "process"
	path?:          string & =~"^/"
	port?:          int & >=1 & <=65535
	timeoutSeconds: int & >=1 & <=300
	expectedStatuses?: [...int & >=100 & <=599]
	contractHash: #ContentHash
	targetKind:   "capability" | "provider" | "module" | "route" | "node"
	targetRef:    #ContractID
	siteRefs: [...#SiteID] & list.MinItems(1)
	nodeRefs?: [...#NodeID]
	routeRef?: #ContractID
	required:  true

	if kind == "http" {
		path: string & =~"^/"
		port: int & >=1 & <=65535
		expectedStatuses: [...int & >=100 & <=599] & list.MinItems(1)
	}
	if kind == "tcp" {
		port: int & >=1 & <=65535
	}
}

#ResolvedEvidenceGateV2: {
	id:       #ContractID
	scenario: string & =~"^.+$"
	phase:    "validate" | "generate" | "apply" | "verify" | "release"
	producer: "compiler" | "generator" | "runtime" | "evidence-runner"
	required: true
	healthGateRefs?: [...#ContractID]
	artifactRefs?: [...#ContractID]
}

#ResolvedPlanGatesV2: {
	health: [...#ResolvedHealthGateV2] & list.MinItems(1)
	evidence: [...#ResolvedEvidenceGateV2] & list.MinItems(1)
	apply: {
		requireFreshPlanHash:       true
		requireCompatibleCLI:       true
		requireCompatibleRuntime:   true
		requireGenerationArtifacts: true
		requireResolvedSecrets:     true
	}
	_healthIDsUnique: list.UniqueItems([for gate in health {gate.id}]) & true
	_evidenceIDsUnique: list.UniqueItems([for gate in evidence {gate.id}]) & true
}

#ExecutionReadinessBlockerCodeV1: "module-umbrella" |
	"module-contract-only" |
	"module-render-units-missing" |
	"provider-realization-none" |
	"provider-owner-umbrella" |
	"provider-owner-contract-only" |
	"provider-owner-apply-support-missing" |
	"renderer-incompatible" |
	"input-contract-incomplete" |
	"required-input-missing" |
	"required-artifact-missing" |
	"artifact-output-mismatch" |
	"module-apply-support-missing" |
	"required-evidence-missing" |
	"bridge-overlay-unverified" |
	"bridge-control-agent-unverified" |
	"policy-enforcement-unverified" |
	"device-verifier-unbound" |
	"bridge-renderer-missing" |
	"origin-identity-unbound" |
	"tls-profile-unbound" |
	"health-gate-not-executable"

// #ExecutionReadinessBlockerV1 uses stable machine-readable codes and refs.
// Human prose deliberately stays outside the signed plan contract.
#ExecutionReadinessBlockerV1: {
	code: #ExecutionReadinessBlockerCodeV1
	refs: [...string & =~"^(module|unit|provider|renderer|input|artifact|evidence|bridge|publication|identity|tls|health):[^[:space:]]+$"] & list.MinItems(1)
	_refsUnique: list.UniqueItems(refs) & true
}

// #ExecutionReadinessRequirementV1 is a validation-only assertion used by
// #ResolvedPlan. It prevents a persisted plan from deleting a blocker and
// recomputing planHash while the selected realization is still non-executable.
// The compiler owns the exact normalized blocker set; CUE independently owns
// the safety invariant that every known blocking condition remains blocked.
#ExecutionReadinessRequirementV1: {
	phase: #ExecutionPhaseReadinessV1
	code:  #ExecutionReadinessBlockerCodeV1
	refs: [...string] & list.MinItems(1)

	_codeMatches: [for blocker in phase.blockers if blocker.code == code {blocker.code}] & list.MinItems(1)
	_refMatches: [for requiredRef in refs {
		ref: requiredRef
		matches: [
			for blocker in phase.blockers
			if blocker.code == code
			for actualRef in blocker.refs
			if actualRef == requiredRef {actualRef},
		] & list.MinItems(1)
	}]
}

#ExecutionPhaseReadinessV1: {
	status: "ready" | "blocked"
	blockers: [...#ExecutionReadinessBlockerV1] | *[]
	if status == "ready" {
		blockers: list.MaxItems(0)
	}
	if status == "blocked" {
		blockers: list.MinItems(1)
	}
}

// #ExecutionReadinessV1 is static contract readiness. Runtime apply still has
// to satisfy the freshness, artifact materialization, secret and evidence
// gates in #ResolvedPlanGatesV2.
#ExecutionReadinessV1: {
	contractVersion: "1.0.0"
	generation:      #ExecutionPhaseReadinessV1
	apply:           #ExecutionPhaseReadinessV1
	if generation.status == "blocked" {
		apply: status: "blocked"
	}
}

#ResolvedAddOnSelectionV2: #AddOnSelection & {
	enabled: true
	config?: #PublicSettings
	secretRefs?: [string]: #SecretReference
}

#ResolvedPlanAuthorityV1: {
	class:                 "product" | "contract-fixture" | "development"
	document:              "catalog" | "contractFixtureCatalog"
	graduationEligible:    bool
	issuer:                string & =~"^[^[:space:]]+$"
	authorityFingerprint?: #ContentHash
	catalogHash:           #ContentHash

	if class == "product" {
		document:             "catalog"
		graduationEligible:   true
		issuer:               "stackkits-product-authority/v1"
		authorityFingerprint: #ContentHash
	}
	if class == "contract-fixture" {
		document:              "contractFixtureCatalog"
		graduationEligible:    false
		issuer:                "stackkits-contract-fixture-authority/v1"
		authorityFingerprint?: _|_
	}
	if class == "development" {
		document:              "catalog"
		graduationEligible:    false
		issuer:                "stackkits-development-authority/v1"
		authorityFingerprint?: _|_
	}
}

#ResolvedPlan: {
	apiVersion: "stackkit.resolved-plan/v1"
	kind:       "ResolvedPlan"

	stackId:   #ContractID
	fleetRef?: #ContractID
	kit: {
		slug:           #KitSlug
		version:        string
		definitionHash: #ContentHash
	}
	compilerVersion:  string
	sourceIntentHash: #ContentHash
	specHash:         #ContentHash
	inventoryHash:    #ContentHash
	planHash:         #ContentHash
	authority:        #ResolvedPlanAuthorityV1

	install:       #ResolvedInstallPlanV2
	compatibility: #CompatibilityPlanV2
	generation: #ResolvedGenerationPlanV2 & {profileContractHash: kit.definitionHash}
	source: #ResolvedSourceLineageV2 & {
		intent: hash:         sourceIntentHash
		normalizedSpec: hash: specHash
		inventory: hash:      inventoryHash
		kitDefinitionHash: kit.definitionHash
	}

	sites: [...#SiteSpec] & list.MinItems(1)
	nodes: [...#NodeSpecV2] & list.MinItems(1)
	_siteIDsUnique: list.UniqueItems([for site in sites {site.id}]) & true
	_nodeIDsUnique: list.UniqueItems([for node in nodes {node.id}]) & true
	controlPlane: #ControlPlaneIntent
	capabilities: [...#ResolvedCapability] & list.MinItems(1)
	providers: [...#ResolvedProvider] & list.MinItems(1)
	// The field is mandatory, but an empty list is valid when every selected
	// provider realizes behavior through host/external contracts. Providers do
	// not become modules by name.
	modules: [...#ResolvedModuleV2] | *[]
	runtimeNetworks!: [...#ResolvedRuntimeNetworkInstanceV1]
	privilegedInterfaceApprovals: [...#ResolvedPrivilegedInterfaceApprovalV2] | *[]
	placement: [...#ResolvedPlacementV2] | *[]
	_placementWorkloadRefsUnique: list.UniqueItems([for item in placement {item.workloadRef}]) & true
	_placementSiteRefsUnique: [for item in placement {
		workloadRef: item.workloadRef
		valid:       list.UniqueItems(item.siteRefs) & true
	}]
	_placementNodeRefsUnique: [for item in placement {
		workloadRef: item.workloadRef
		valid:       list.UniqueItems(item.nodeRefs) & true
	}]
	_placementSitesExist: [for item in placement for selectedSiteRef in item.siteRefs {
		workloadRef: item.workloadRef
		siteRef:     selectedSiteRef
		matches: [for site in sites if site.id == selectedSiteRef {site.id}] & list.MinItems(1) & list.MaxItems(1)
	}]
	_placementNodesExact: [for item in placement for selectedNodeRef in item.nodeRefs {
		workloadRef: item.workloadRef
		nodeRef:     selectedNodeRef
		matches: [
			for node in nodes
			if node.id == selectedNodeRef && node.enabled && list.Contains(item.siteRefs, node.siteRef) {node.id},
		] & list.MinItems(1) & list.MaxItems(1)
	}]
	// siteRefs is the exact unique site projection of selected nodeRefs. This
	// reverse check prevents an attacker from appending an unused, existing
	// site while keeping all selected nodes otherwise valid.
	_placementSitesBackedByNodes: [for item in placement for selectedSiteRef in item.siteRefs {
		workloadRef: item.workloadRef
		siteRef:     selectedSiteRef
		matches: [
			for node in nodes
			if node.enabled && node.siteRef == selectedSiteRef && list.Contains(item.nodeRefs, node.id) {node.id},
		] & list.MinItems(1)
	}]
	if data != _|_ {
		if data.defaultAuthority != "per-workload" {
			_dataDefaultAuthorityKind: [for site in sites if site.kind == data.defaultAuthority {site.id}] & list.MinItems(1)
		}
		if data.bindings != _|_ {
			_dataPrimarySites: [for bindingID, dataBinding in data.bindings {
				binding: bindingID
				matches: [for site in sites if site.id == dataBinding.primarySiteRef {site.id}] & list.MinItems(1) & list.MaxItems(1)
			}]
			_dataReplicaSites: [for bindingID, dataBinding in data.bindings if dataBinding.replicaSiteRefs != _|_ for replicaRef in dataBinding.replicaSiteRefs {
				binding: bindingID
				replica: replicaRef
				matches: [for site in sites if site.id == replicaRef {site.id}] & list.MinItems(1) & list.MaxItems(1)
			}]
			_dataReplicaSets: [for bindingID, dataBinding in data.bindings if dataBinding.replicaSiteRefs != _|_ {
				binding: bindingID
				unique:  list.UniqueItems(dataBinding.replicaSiteRefs) & true
				primaryExcluded: [for replicaRef in dataBinding.replicaSiteRefs if replicaRef == dataBinding.primarySiteRef {replicaRef}] & list.MaxItems(0)
			}]
		}
	}
	if access != _|_ {
		_accessAllowedSiteSets: [for policyID, accessPolicy in access if accessPolicy.allowedSiteRefs != _|_ {
			policy: policyID
			unique: list.UniqueItems(accessPolicy.allowedSiteRefs) & true
		}]
		_accessAllowedSites: [for policyID, accessPolicy in access if accessPolicy.allowedSiteRefs != _|_ for allowedSiteRef in accessPolicy.allowedSiteRefs {
			policy: policyID
			site:   allowedSiteRef
			matches: [for site in sites if site.id == allowedSiteRef {site.id}] & list.MinItems(1) & list.MaxItems(1)
		}]
	}
	addons?: [string]: #ResolvedAddOnSelectionV2
	access?: [string]: #AccessPolicyV2
	bridge?:            #ResolvedBridgeContractV2
	availability:       #ResolvedAvailabilityV2
	identity:           #ResolvedIdentityPlan
	data:               #DataPlacementIntent
	failurePolicy:      #PartitionPolicy
	system:             #ResolvedSystemPlanV2
	storage:            #StorageIntentV2
	network:            #ResolvedNetworkPlanV2
	gates:              #ResolvedPlanGatesV2
	executionReadiness: #ExecutionReadinessV1
	evidence: [...string] & list.MinItems(1)
	if bridge != _|_ {
		_resolvedBridgePeersExist: [for peer in bridge.overlay.peerSiteRefs {
			site: peer
			matches: [for site in sites if site.id == peer {site.id}] & list.MinItems(1) & list.MaxItems(1)
		}]
		_resolvedBridgePublicationSites: [for publication in bridge.publications {
			service: publication.serviceRef
			source: [for site in sites if site.id == publication.sourceSiteRef {site.id}] & list.MinItems(1) & list.MaxItems(1)
			edge: [for site in sites if site.id == publication.edgeSiteRef {site.id}] & list.MinItems(1) & list.MaxItems(1)
		}]
		_resolvedBridgeFlowSites: [for flow in bridge.policy.allowedFlows {
			service: flow.serviceRef
			from: [for site in sites if site.id == flow.fromSiteRef {site.id}] & list.MinItems(1) & list.MaxItems(1)
			to: [for site in sites if site.id == flow.toSiteRef {site.id}] & list.MinItems(1) & list.MaxItems(1)
		}]
		_resolvedBridgeFlowCountExact: len(bridge.policy.allowedFlows) & len(bridge.publications)
		_resolvedBridgeFlowsBoundToPublications: [for flow in bridge.policy.allowedFlows {
			service: flow.serviceRef
			matches: [
				for publication in bridge.publications
				if publication.serviceRef == flow.serviceRef
				if publication.edgeSiteRef == flow.fromSiteRef
				if publication.sourceSiteRef == flow.toSiteRef
				if publication.upstreamProtocol == flow.protocol
				if list.Contains(flow.ports, publication.targetPort) {publication.serviceRef},
			] & list.MinItems(1) & list.MaxItems(1)
		}]
		if len(bridge.publications) > 0 {
			access: [string]: #AccessPolicyV2
			data: bindings: [string]: {
				classes: [...#DataClass] & list.MinItems(1)
				primarySiteRef: #SiteID
				replicaSiteRefs?: [...#SiteID]
				cloudCopyAllowed: bool | *false
			}
			_resolvedPublicationPolicyRefs: [for publication in bridge.publications {
				service: publication.serviceRef
				matches: [
					for policyID, policy in access
					if policyID == publication.auth.policyRef
					if policy.exposure == "public"
					if policy.authentication != "none"
					if policy.allowedMethods != _|_
					if publication.access.exposure == "public"
					if publication.access.policyRef == policyID
					if publication.access.authentication == policy.authentication
					if publication.access.privilege == policy.privilege
					if publication.access.enrolledDeviceRequired == policy.enrolledDeviceRequired
					if publication.access.ownerStepUpRequired == policy.ownerStepUpRequired
					if publication.access.defaultClosed == true
					if publication.access.allowedMethods != _|_
					if len(publication.access.allowedMethods) == len(policy.allowedMethods) {policyID},
				] & list.MinItems(1) & list.MaxItems(1)
			}]
			_resolvedPublicationAccessMethods: [
				for publication in bridge.publications
				for policyID, policy in access
				if policyID == publication.auth.policyRef
				for allowed in policy.allowedMethods {
					service:    publication.serviceRef
					httpMethod: allowed
					matches: [for resolvedMethod in publication.access.allowedMethods if resolvedMethod == allowed {resolvedMethod}] & list.MinItems(1) & list.MaxItems(1)
				},
			]
			_resolvedPublicationFlows: [for publication in bridge.publications {
				service: publication.serviceRef
				matches: [
					for flow in bridge.policy.allowedFlows
					if flow.fromSiteRef == publication.edgeSiteRef
					if flow.toSiteRef == publication.sourceSiteRef
					if flow.serviceRef == publication.serviceRef
					if flow.protocol == publication.upstreamProtocol
					for port in flow.ports
					if port == publication.targetPort {flow.serviceRef},
				] & list.MinItems(1)
			}]
			_resolvedPublicationFlowMethods: [
				for publication in bridge.publications
				for flow in bridge.policy.allowedFlows
				if flow.fromSiteRef == publication.edgeSiteRef
				if flow.toSiteRef == publication.sourceSiteRef
				if flow.serviceRef == publication.serviceRef
				if flow.protocol == "http" || flow.protocol == "https"
				for method in flow.methods {
					service:    publication.serviceRef
					httpMethod: method
					matches: [for allowed in publication.access.allowedMethods if allowed == method {allowed}] & list.MinItems(1) & list.MaxItems(1)
				},
			]
			_resolvedPublicationData: [for publication in bridge.publications {
				service: publication.serviceRef
				matches: [
					for bindingID, binding in data.bindings
					if bindingID == publication.dataBindingRef
					if binding.primarySiteRef == publication.sourceSiteRef
					if binding.cloudCopyAllowed == false {bindingID},
				] & list.MinItems(1) & list.MaxItems(1)
			}]
		}
		if len(bridge.policy.allowedFlows) > 0 {
			data: bindings: [string]: {classes: [...#DataClass] & list.MinItems(1)}
			_resolvedBridgeFlowData: [for flow in bridge.policy.allowedFlows {
				service: flow.serviceRef
				binding: [for bindingID, _ in data.bindings if bindingID == flow.serviceRef {bindingID}] & list.MinItems(1) & list.MaxItems(1)
				classes: [for flowClass in flow.dataClasses {
					class: flowClass
					matches: [
						for bindingID, binding in data.bindings
						if bindingID == flow.serviceRef
						for bindingClass in binding.classes
						if bindingClass == flowClass {bindingClass},
					] & list.MinItems(1) & list.MaxItems(1)
				}]
			}]
		}
	}

	// No current compiler path emits warnings. Keep the field closed until a
	// deterministic, provenance-bound warning source is part of the contract.
	warnings?: _|_
	let selectedRendererID = generation.renderer.id
	let resolvedModules = modules
	let resolvedNodes = nodes
	let resolvedOutputRoot = generation.outputRoot
	modules: [...{
		renderUnits: [...{
			instances: [...{
				scope:    "module" | "node-local"
				siteRef?: #SiteID
				nodeRef?: #NodeID
				if scope == "node-local" {
					siteRef: #SiteID
					nodeRef: #NodeID
					let instanceSiteRef = siteRef
					let instanceNodeRef = nodeRef
					_nodeSiteExact: [
						for node in resolvedNodes
						if node.id == instanceNodeRef && node.siteRef == instanceSiteRef {node.id},
					] & list.MinItems(1) & list.MaxItems(1)
				}
			}]
		}]
	}]

	// Generated artifacts are owned by the exact render instance/output that
	// materializes them. Derive metadata from the logical artifact contract so
	// changing mode, format, kind, path, or owner cannot be legalized by merely
	// recomputing the plan hash.
	generation: artifacts: [...{
		id:       #ContractID
		kind:     #GenerationArtifactKindV2
		path:     #ArtifactPath
		format:   #GenerationArtifactFormatV2
		mode:     string & =~"^0[0-7]{3}$"
		required: bool
		owner:    #GeneratedArtifactOwnerV2
		let artifactOwner = owner
		if artifactOwner.kind == "plan" {
			id: "resolved-plan"
		}
		if artifactOwner.kind == "render-instance" {
			let ownerMatches = [
				for module in resolvedModules
				if module.id == artifactOwner.moduleRef
				for unit in module.renderUnits
				if unit.id == artifactOwner.unitRef
				for instance in unit.instances
				if instance.id == artifactOwner.instanceRef
				for output in instance.outputs
				if output.ref == artifactOwner.outputRef
				for contract in module.realizationSupport.artifacts.contracts
				if contract.unitRef == unit.id && contract.outputRef == output.ref {
					id:       output.artifactRef
					kind:     contract.kind
					path:     #ArtifactPath
					format:   contract.format
					mode:     contract.mode
					required: contract.required
					if resolvedOutputRoot == "." {
						path: output.path
					}
					if resolvedOutputRoot != "." {
						path: "\(resolvedOutputRoot)/\(output.path)"
					}
				},
			]
			_ownerMatchExact: ownerMatches & list.MinItems(1) & list.MaxItems(1)
			if len(ownerMatches) == 1 {
				id:       ownerMatches[0].id
				kind:     ownerMatches[0].kind
				path:     ownerMatches[0].path
				format:   ownerMatches[0].format
				mode:     ownerMatches[0].mode
				required: ownerMatches[0].required
			}
		}
	}]
	_renderOwnedArtifactCountExact: len([
		for artifact in generation.artifacts
		if artifact.owner.kind == "render-instance" {artifact.id},
	]) & len([
		for module in modules
		for unit in module.renderUnits
		for instance in unit.instances
		for output in instance.outputs {output.artifactRef},
	])

	if install.runtime == "docker" {
		system: container: #ContainerRuntimeIntentV2
	}
	if install.runtime == "native" {
		system: container?: _|_
	}
	_haCapabilityRefs: [for capability in capabilities if capability.id == "availability-ha" {capability.id}]
	if controlPlane.mode == "single" {
		availability: enabled: false
		availability: {
			policyRef?:          _|_
			realizationRef?:     _|_
			providerRef?:        _|_
			moduleRef?:          _|_
			selector?:           _|_
			failureModel?:       _|_
			healthAcceptance?:   _|_
			evidenceAcceptance?: _|_
			selectedMembers?:    _|_
		}
		addons: ha?: _|_
		_haCapabilityRefs: list.MaxItems(0)
	}
	if controlPlane.mode != "single" {
		availability: enabled: true
		addons: ha: enabled: true
		_haCapabilityRefs: list.MinItems(1) & list.MaxItems(1)
	}
	if availability.enabled == true {
		controlPlane: mode: "warm-standby" | "quorum"
		addons: ha: enabled: true
		availability: {
			policyRef:          #ContractID
			realizationRef:     #ContractID
			providerRef:        #ContractID
			moduleRef:          #ContractID
			selector:           "control-plane-members"
			failureModel:       #AvailabilityFailureModelV2
			healthAcceptance:   #AvailabilityHealthAcceptanceV2
			evidenceAcceptance: #AvailabilityEvidenceAcceptanceV2
			selectedMembers: [...#ResolvedAvailabilityMemberV2] & list.MinItems(2)
		}
		_haSelectedMemberCount: len(availability.selectedMembers) & len(controlPlane.members)
		_haSelectedMembersExact: [for selected in availability.selectedMembers {
			nodeRef: selected.nodeRef
			matches: [
				for member in controlPlane.members
				for node in nodes
				if node.id == member && selected.nodeRef == node.id && selected.siteRef == node.siteRef && selected.failureDomain == node.failureDomain {node.id},
			] & list.MinItems(1) & list.MaxItems(1)
		}]
		_haControlMembersSelected: [for member in controlPlane.members {
			nodeRef: member
			matches: [for selected in availability.selectedMembers if selected.nodeRef == member {selected.nodeRef}] & list.MinItems(1) & list.MaxItems(1)
		}]
		_haRealizationProviders: [
			for provider in providers
			if provider.id == availability.providerRef {
				providerRef: provider.id
				moduleMatches: [
					for moduleRef in provider.realization.moduleRefs.required
					if moduleRef == availability.moduleRef {moduleRef},
				] & list.MinItems(1) & list.MaxItems(1)
			},
		] & list.MinItems(1) & list.MaxItems(1)
		_haRealizationModules: [
			for module in modules
			if module.id == availability.moduleRef && module.providerRef == availability.providerRef {
				moduleRef: module.id
				nodeCount: len(module.nodeRefs) & len(controlPlane.members)
				members: [for member in controlPlane.members {
					nodeRef: member
					matches: [for nodeRef in module.nodeRefs if nodeRef == member {nodeRef}] & list.MinItems(1) & list.MaxItems(1)
				}]
				nodes: [for nodeRef in module.nodeRefs {
					nodeRef: nodeRef
					matches: [for member in controlPlane.members if member == nodeRef {member}] & list.MinItems(1) & list.MaxItems(1)
				}]
			},
		] & list.MinItems(1) & list.MaxItems(1)
		_haRequiredHealthGates: [for requiredRef in availability.healthAcceptance.requiredGateRefs {
			gateRef: requiredRef
			matches: [
				for gate in gates.health
				if gate.id == requiredRef && (gate.targetRef == availability.providerRef || gate.targetRef == availability.moduleRef) {
					id:        gate.id
					nodeCount: len(gate.nodeRefs) & len(controlPlane.members)
					members: [for member in controlPlane.members {
						nodeRef: member
						matches: [for nodeRef in gate.nodeRefs if nodeRef == member {nodeRef}] & list.MinItems(1) & list.MaxItems(1)
					}]
				},
			] & list.MinItems(1) & list.MaxItems(1)
		}]
		_haRequiredEvidence: [for requiredRef in availability.evidenceAcceptance.requiredRefs {
			evidenceRef: requiredRef
			planMatches: [for actual in evidence if actual == requiredRef {actual}] & list.MinItems(1) & list.MaxItems(1)
			gateMatches: [for gate in gates.evidence if gate.scenario == requiredRef {gate.id}] & list.MinItems(1) & list.MaxItems(1)
		}]
	}
	_enabledControllerIDs: [
		for node in nodes
		if node.enabled
		for role in node.roles
		if role == "controller" {node.id},
	]
	_controlMembersExact: [for member in controlPlane.members {
		nodeRef: member
		matches: [for controller in _enabledControllerIDs if controller == member {controller}] & list.MinItems(1) & list.MaxItems(1)
	}]
	_controlAuthoritySiteExact: [for site in sites if site.id == controlPlane.authoritySiteRef {site.id}] & list.MinItems(1) & list.MaxItems(1)
	_controlMemberFailureDomains: [
		for member in controlPlane.members
		for node in nodes
		if node.id == member {node.failureDomain},
	]
	if controlPlane.mode != "single" {
		_controlMemberFailureDomainsUnique: list.UniqueItems(_controlMemberFailureDomains) & true
		_controlMemberFailureDomainSpread:  _controlMemberFailureDomains & list.MinItems(availability.failureDomainSpread)
	}

	_moduleIDsUnique: list.UniqueItems([for module in modules {module.id}]) & true
	_resolvedProvidedInterfaceIDsUnique: list.UniqueItems([
		for module in modules
		for unit in module.renderUnits
		for contract in unit.providesInterfaces {contract.id},
	]) & true
	_resolvedDockerProxyDaemonPoliciesUnique: list.UniqueItems([
		for module in modules
		for unit in module.renderUnits
		for contract in unit.providesInterfaces {"\(contract.daemonRef)/\(contract.policyProfile)"},
	]) & true
	_privilegedInterfaceApprovalIDsUnique: list.UniqueItems([for approval in privilegedInterfaceApprovals {approval.id}]) & true
	_privilegedInterfaceApprovalSubjectsUnique: list.UniqueItems([
		for approval in privilegedInterfaceApprovals {"\(approval.moduleRef)/\(approval.unitRef)/\(approval.daemonRef)/\(approval.policyProfile)"},
	]) & true
	_renderInstanceSubjectsUnique: list.UniqueItems([
		for module in modules
		for unit in module.renderUnits
		for instance in unit.instances {"\(module.id)/\(unit.id)/\(instance.id)"},
	]) & true
	_renderInstanceArtifactRefsUnique: list.UniqueItems([
		for module in modules
		for unit in module.renderUnits
		for instance in unit.instances
		for output in instance.outputs {output.artifactRef},
	]) & true
	_renderInstancePathsUnique: list.UniqueItems([
		for module in modules
		for unit in module.renderUnits
		for instance in unit.instances
		for output in instance.outputs {strings.ToLower(output.path)},
	]) & true
	_renderInstancePlacementExact: [
		for module in modules
		for unit in module.renderUnits
		for instance in unit.instances {
			module:   module.id
			unit:     unit.id
			instance: instance.id
			scopeMatches: [if instance.scope == unit.placement.scope {instance.scope}] & list.MinItems(1) & list.MaxItems(1)
			if instance.scope == "node-local" {
				nodeMatches: [
					for node in nodes
					if node.id == instance.nodeRef && node.siteRef == instance.siteRef {node.id},
				] & list.MinItems(1) & list.MaxItems(1)
			}
		},
	]
	_runtimeNetworkIDsUnique: list.UniqueItems([for network in runtimeNetworks {network.id}]) & true
	_runtimeNetworkOwnerSubjectsUnique: list.UniqueItems([
		for network in runtimeNetworks {"\(network.owner.moduleRef)/\(network.owner.unitRef)/\(network.owner.instanceRef)/\(network.owner.interfaceRef)"},
	]) & true
	_runtimeNetworkMemberSubjectsUnique: list.UniqueItems([
		for network in runtimeNetworks
		for member in network.members {"\(member.role)/\(member.moduleRef)/\(member.unitRef)/\(member.instanceRef)/\(member.interfaceRef)"},
	]) & true

	// A network owner is the exact daemon-local provider instance and provided
	// interface. The logical networkRef is only one attribute of that identity.
	_runtimeNetworkOwnersExact: [for runtimeNetwork in runtimeNetworks {
		network: runtimeNetwork.id
		matches: [
			for module in modules
			if module.id == runtimeNetwork.owner.moduleRef
			for unit in module.renderUnits
			if unit.id == runtimeNetwork.owner.unitRef
			for instance in unit.instances
			if instance.id == runtimeNetwork.owner.instanceRef && instance.scope == "node-local" && instance.siteRef == runtimeNetwork.siteRef && instance.nodeRef == runtimeNetwork.nodeRef && instance.daemonRef == runtimeNetwork.daemonRef && instance.daemonInstanceRef == runtimeNetwork.daemonInstanceRef
			for contract in unit.providesInterfaces
			if contract.id == runtimeNetwork.owner.interfaceRef && contract.endpoint.networkRef == runtimeNetwork.networkRef && contract.daemonRef == runtimeNetwork.daemonRef {"\(module.id)/\(unit.id)/\(instance.id)/\(contract.id)"},
		] & list.MinItems(1) & list.MaxItems(1)
	}]

	// Conversely, every concrete provider interface instance owns exactly one
	// network instance. This makes an empty runtimeNetworks list legal only when
	// no runtime-network provider exists.
	_runtimeNetworkProvidersComplete: [
		for module in modules
		for unit in module.renderUnits
		for instance in unit.instances
		for contract in unit.providesInterfaces {
			provider: "\(module.id)/\(unit.id)/\(instance.id)/\(contract.id)"
			matches: [
				for network in runtimeNetworks
				if network.owner.moduleRef == module.id && network.owner.unitRef == unit.id && network.owner.instanceRef == instance.id && network.owner.interfaceRef == contract.id && network.networkRef == contract.endpoint.networkRef && network.siteRef == instance.siteRef && network.nodeRef == instance.nodeRef && network.daemonRef == instance.daemonRef && network.daemonRef == contract.daemonRef && network.daemonInstanceRef == instance.daemonInstanceRef {network.id},
			] & list.MinItems(1) & list.MaxItems(1)
		},
	]

	// Every member resolves to one exact local interface instance. Consumer
	// instances may be node-local without owning a daemon; their membership is
	// nevertheless bound to the provider network's exact daemon identity.
	_runtimeNetworkMembersExact: [
		for runtimeNetwork in runtimeNetworks
		for runtimeMember in runtimeNetwork.members {
			network: runtimeNetwork.id
			member:  "\(runtimeMember.role)/\(runtimeMember.moduleRef)/\(runtimeMember.unitRef)/\(runtimeMember.instanceRef)/\(runtimeMember.interfaceRef)"
			if runtimeMember.role == "provider" {
				matches: [
					for module in modules
					if module.id == runtimeMember.moduleRef
					for unit in module.renderUnits
					if unit.id == runtimeMember.unitRef
					for instance in unit.instances
					if instance.id == runtimeMember.instanceRef && instance.scope == "node-local" && instance.siteRef == runtimeNetwork.siteRef && instance.nodeRef == runtimeNetwork.nodeRef && instance.daemonRef == runtimeNetwork.daemonRef && instance.daemonInstanceRef == runtimeNetwork.daemonInstanceRef
					for contract in unit.providesInterfaces
					if contract.id == runtimeMember.interfaceRef && contract.endpoint.networkRef == runtimeNetwork.networkRef && contract.daemonRef == runtimeNetwork.daemonRef {instance.id},
				] & list.MinItems(1) & list.MaxItems(1)
			}
			if runtimeMember.role == "consumer" {
				matches: [
					for module in modules
					if module.id == runtimeMember.moduleRef
					for unit in module.renderUnits
					if unit.id == runtimeMember.unitRef
					for instance in unit.instances
					if instance.id == runtimeMember.instanceRef && instance.scope == "node-local" && instance.siteRef == runtimeNetwork.siteRef && instance.nodeRef == runtimeNetwork.nodeRef
					for requirement in unit.requiresInterfaces
					if requirement.id == runtimeMember.interfaceRef && requirement.kind == "docker-http-readonly-v1" && requirement.endpoint.networkRef == runtimeNetwork.networkRef && requirement.daemonRef == runtimeNetwork.daemonRef {instance.id},
				] & list.MinItems(1) & list.MaxItems(1)
			}
		},
	]

	_runtimeNetworkConsumerDaemonLocalityExact: [
		for runtimeNetwork in runtimeNetworks
		for runtimeMember in runtimeNetwork.members
		if runtimeMember.role == "consumer"
		for module in modules
		if module.id == runtimeMember.moduleRef
		for unit in module.renderUnits
		if unit.id == runtimeMember.unitRef
		for instance in unit.instances
		if instance.id == runtimeMember.instanceRef && instance.daemonRef != _|_ {
			member: runtimeMember.instanceRef
			matches: [if instance.daemonRef == runtimeNetwork.daemonRef && instance.daemonInstanceRef == runtimeNetwork.daemonInstanceRef {instance.id}] & list.MinItems(1) & list.MaxItems(1)
		},
	]

	// Membership and per-instance bindings are a bidirectional projection. A
	// stale or relabelled object on either side therefore fails closed.
	_runtimeNetworkMembersBoundExactly: [
		for runtimeNetwork in runtimeNetworks
		for runtimeMember in runtimeNetwork.members {
			network: runtimeNetwork.id
			member:  runtimeMember.instanceRef
			matches: [
				for module in modules
				if module.id == runtimeMember.moduleRef
				for unit in module.renderUnits
				if unit.id == runtimeMember.unitRef
				for instance in unit.instances
				if instance.id == runtimeMember.instanceRef
				for binding in instance.networkBindings
				if binding.networkInstanceRef == runtimeNetwork.id && binding.networkRef == runtimeNetwork.networkRef && binding.role == runtimeMember.role && binding.interfaceRef == runtimeMember.interfaceRef && binding.siteRef == runtimeNetwork.siteRef && binding.nodeRef == runtimeNetwork.nodeRef && binding.daemonRef == runtimeNetwork.daemonRef && binding.daemonInstanceRef == runtimeNetwork.daemonInstanceRef && binding.owner.moduleRef == runtimeNetwork.owner.moduleRef && binding.owner.unitRef == runtimeNetwork.owner.unitRef && binding.owner.instanceRef == runtimeNetwork.owner.instanceRef && binding.owner.interfaceRef == runtimeNetwork.owner.interfaceRef {binding.networkInstanceRef},
			] & list.MinItems(1) & list.MaxItems(1)
		},
	]
	_runtimeNetworkBindingsGoverned: [
		for module in modules
		for unit in module.renderUnits
		for instance in unit.instances
		for runtimeBinding in instance.networkBindings {
			binding: "\(module.id)/\(unit.id)/\(instance.id)/\(runtimeBinding.networkInstanceRef)/\(runtimeBinding.interfaceRef)"
			instanceLocality: [
				if instance.scope == "node-local" && instance.siteRef == runtimeBinding.siteRef && instance.nodeRef == runtimeBinding.nodeRef {instance.id},
			] & list.MinItems(1) & list.MaxItems(1)
			matches: [
				for network in runtimeNetworks
				if network.id == runtimeBinding.networkInstanceRef && network.networkRef == runtimeBinding.networkRef && network.siteRef == runtimeBinding.siteRef && network.nodeRef == runtimeBinding.nodeRef && network.daemonRef == runtimeBinding.daemonRef && network.daemonInstanceRef == runtimeBinding.daemonInstanceRef && network.owner.moduleRef == runtimeBinding.owner.moduleRef && network.owner.unitRef == runtimeBinding.owner.unitRef && network.owner.instanceRef == runtimeBinding.owner.instanceRef && network.owner.interfaceRef == runtimeBinding.owner.interfaceRef
				for member in network.members
				if member.role == runtimeBinding.role && member.moduleRef == module.id && member.unitRef == unit.id && member.instanceRef == instance.id && member.interfaceRef == runtimeBinding.interfaceRef {network.id},
			] & list.MinItems(1) & list.MaxItems(1)
		},
	]

	_runtimeNetworkConsumersComplete: [
		for module in modules
		for unit in module.renderUnits
		for instance in unit.instances
		for requirement in unit.requiresInterfaces
		if requirement.kind == "docker-http-readonly-v1" {
			consumer: "\(module.id)/\(unit.id)/\(instance.id)/\(requirement.id)"
			matches: [
				for network in runtimeNetworks
				if network.networkRef == requirement.endpoint.networkRef && network.siteRef == instance.siteRef && network.nodeRef == instance.nodeRef && network.daemonRef == requirement.daemonRef
				for member in network.members
				if member.role == "consumer" && member.moduleRef == module.id && member.unitRef == unit.id && member.instanceRef == instance.id && member.interfaceRef == requirement.id {network.id},
			] & list.MinItems(1) & list.MaxItems(1)
		},
	]

	_renderUnitNodeRefsOwned: [for module in modules for unit in module.renderUnits for nodeRef in unit.nodeRefs {
		module: module.id
		unit:   unit.id
		node:   nodeRef
		moduleMatches: [for moduleNodeRef in module.nodeRefs if moduleNodeRef == nodeRef {moduleNodeRef}] & list.MinItems(1) & list.MaxItems(1)
		planMatches: [for node in nodes if node.id == nodeRef {node.id}] & list.MinItems(1) & list.MaxItems(1)
	}]
	_renderUnitSiteRefsOwned: [for module in modules for unit in module.renderUnits for siteRef in unit.siteRefs {
		module: module.id
		unit:   unit.id
		site:   siteRef
		moduleMatches: [for moduleSiteRef in module.siteRefs if moduleSiteRef == siteRef {moduleSiteRef}] & list.MinItems(1) & list.MaxItems(1)
		planMatches: [for site in sites if site.id == siteRef {site.id}] & list.MinItems(1) & list.MaxItems(1)
	}]
	_renderUnitNodeSitesExact: [for module in modules for unit in module.renderUnits for nodeRef in unit.nodeRefs {
		module: module.id
		unit:   unit.id
		node:   nodeRef
		matches: [
			for node in nodes
			if node.id == nodeRef
			for siteRef in unit.siteRefs
			if siteRef == node.siteRef {siteRef},
		] & list.MinItems(1) & list.MaxItems(1)
	}]
	_renderUnitPlacementExpansionComplete: [
		for module in modules
		for unit in module.renderUnits {
			module: module.id
			unit:   unit.id
			nodeMatches: [for nodeRef in module.nodeRefs {
				node: nodeRef
				matches: [for unitNodeRef in unit.nodeRefs if unitNodeRef == nodeRef {unitNodeRef}] & list.MinItems(1) & list.MaxItems(1)
			}]
			siteMatches: [for siteRef in module.siteRefs {
				site: siteRef
				matches: [for unitSiteRef in unit.siteRefs if unitSiteRef == siteRef {unitSiteRef}] & list.MinItems(1) & list.MaxItems(1)
			}]
		},
	]
	_renderUnitDaemonBindingsExact: [
		for module in modules
		for unit in module.renderUnits
		if unit.placement.cardinality == "one-per-daemon" {
			module: module.id
			unit:   unit.id
			nodeBindings: [for nodeRef in unit.nodeRefs {
				node: nodeRef
				matches: [for binding in unit.daemonBindings if binding.nodeRef == nodeRef && binding.daemonRef == unit.placement.daemonRef {binding.instanceRef}] & list.MinItems(1) & list.MaxItems(1)
			}]
			bindingsOwned: [for binding in unit.daemonBindings {
				node: binding.nodeRef
				nodeMatches: [for nodeRef in unit.nodeRefs if nodeRef == binding.nodeRef {nodeRef}] & list.MinItems(1) & list.MaxItems(1)
				siteMatches: [for siteRef in unit.siteRefs if siteRef == binding.siteRef {siteRef}] & list.MinItems(1) & list.MaxItems(1)
				planMatches: [for node in nodes if node.id == binding.nodeRef && node.siteRef == binding.siteRef {node.id}] & list.MinItems(1) & list.MaxItems(1)
			}]
		},
	]
	_implementationInterfaceRequirementsResolved: [
		for consumerModule in modules
		for consumerUnit in consumerModule.renderUnits
		for requirement in consumerUnit.requiresInterfaces
		if requirement.kind == "docker-http-readonly-v1" {
			module:    consumerModule.id
			unit:      consumerUnit.id
			interface: requirement.id
			bindings: [for binding in requirement.providerBindings {
				node: binding.nodeRef
				matches: [
					for providerModule in modules
					if providerModule.id == binding.moduleRef
					for providerUnit in providerModule.renderUnits
					if providerUnit.id == binding.unitRef
					if len([for providerNodeRef in providerUnit.nodeRefs if providerNodeRef == binding.nodeRef {providerNodeRef}]) == 1
					if len([for providerSiteRef in providerUnit.siteRefs if providerSiteRef == binding.siteRef {providerSiteRef}]) == 1
					for providerInstance in providerUnit.instances
					if providerInstance.id == binding.providerInstanceRef && providerInstance.siteRef == binding.siteRef && providerInstance.nodeRef == binding.nodeRef && providerInstance.daemonRef == binding.daemonRef && providerInstance.daemonInstanceRef == binding.daemonInstanceRef
					for contract in providerUnit.providesInterfaces
					if contract.id == binding.interfaceRef && contract.kind == requirement.kind && contract.protocol == requirement.protocol && contract.version == requirement.version && contract.daemonRef == requirement.daemonRef && contract.daemonRef == binding.daemonRef && contract.policyProfile == requirement.policyProfile && contract.policyProfile == binding.policyProfile && contract.endpoint.ref == requirement.endpoint.ref && contract.endpoint.ref == binding.endpointRef && contract.endpoint.visibility == requirement.endpoint.visibility && contract.endpoint.transport == requirement.endpoint.transport && contract.endpoint.networkRef == requirement.endpoint.networkRef && contract.endpoint.networkRef == binding.networkRef && contract.endpoint.address == requirement.endpoint.address && contract.endpoint.port == requirement.endpoint.port
					if len([for requiredScope in requirement.scopes for providerScope in contract.scopes if providerScope == requiredScope {requiredScope}]) == len(requirement.scopes) {
						provider: "\(providerModule.id)/\(providerUnit.id)/\(providerInstance.id)/\(contract.id)"
					},
				] & list.MinItems(1) & list.MaxItems(1)
				consumerInstanceMatches: [
					for consumerInstance in consumerUnit.instances
					if consumerInstance.id == binding.consumerInstanceRef && consumerInstance.scope == "node-local" && consumerInstance.siteRef == binding.siteRef && consumerInstance.nodeRef == binding.nodeRef {consumerInstance.id},
				] & list.MinItems(1) & list.MaxItems(1)
				networkMatches: [
					for network in runtimeNetworks
					if network.id == binding.networkInstanceRef && network.networkRef == binding.networkRef && network.siteRef == binding.siteRef && network.nodeRef == binding.nodeRef && network.daemonRef == binding.daemonRef && network.daemonInstanceRef == binding.daemonInstanceRef && network.owner.moduleRef == binding.moduleRef && network.owner.unitRef == binding.unitRef && network.owner.instanceRef == binding.providerInstanceRef && network.owner.interfaceRef == binding.interfaceRef
					for providerMember in network.members
					if providerMember.role == "provider" && providerMember.moduleRef == binding.moduleRef && providerMember.unitRef == binding.unitRef && providerMember.instanceRef == binding.providerInstanceRef && providerMember.interfaceRef == binding.interfaceRef
					for consumerMember in network.members
					if consumerMember.role == "consumer" && consumerMember.moduleRef == consumerModule.id && consumerMember.unitRef == consumerUnit.id && consumerMember.instanceRef == binding.consumerInstanceRef && consumerMember.interfaceRef == requirement.id {network.id},
				] & list.MinItems(1) & list.MaxItems(1)
			}]
			consumerNodesBoundExactly: [for consumerNodeRef in consumerUnit.nodeRefs {
				node: consumerNodeRef
				matches: [for binding in requirement.providerBindings if binding.nodeRef == consumerNodeRef {binding.nodeRef}] & list.MinItems(1) & list.MaxItems(1)
			}]
			bindingsOwnedByConsumer: [for binding in requirement.providerBindings {
				node: binding.nodeRef
				nodeMatches: [for consumerNodeRef in consumerUnit.nodeRefs if consumerNodeRef == binding.nodeRef {consumerNodeRef}] & list.MinItems(1) & list.MaxItems(1)
				siteMatches: [
					for node in nodes
					if node.id == binding.nodeRef && node.siteRef == binding.siteRef
					for consumerSiteRef in consumerUnit.siteRefs
					if consumerSiteRef == binding.siteRef {consumerSiteRef},
				] & list.MinItems(1) & list.MaxItems(1)
			}]
		},
	]
	_directInterfaceRequirementsApproved: [
		for module in modules
		for unit in module.renderUnits
		for requirement in unit.requiresInterfaces
		if requirement.kind == "docker-socket-direct-v1" {
			module:    module.id
			unit:      unit.id
			interface: requirement.id
			matches: [
				for approval in privilegedInterfaceApprovals
				if approval.moduleRef == module.id && approval.unitRef == unit.id && approval.providerRef == module.providerRef && approval.daemonRef == requirement.daemonRef && approval.policyProfile == requirement.policyProfile
				if len([for nodeRef in unit.nodeRefs for approvalNodeRef in approval.nodeRefs if approvalNodeRef == nodeRef {nodeRef}]) == len(unit.nodeRefs)
				if len([for approvalNodeRef in approval.nodeRefs for nodeRef in unit.nodeRefs if nodeRef == approvalNodeRef {approvalNodeRef}]) == len(approval.nodeRefs)
				if len([for siteRef in unit.siteRefs for approvalSiteRef in approval.siteRefs if approvalSiteRef == siteRef {siteRef}]) == len(unit.siteRefs)
				if len([for approvalSiteRef in approval.siteRefs for siteRef in unit.siteRefs if siteRef == approvalSiteRef {approvalSiteRef}]) == len(approval.siteRefs) {approval.id},
			] & list.MinItems(1) & list.MaxItems(1)
		},
	]
	_privilegedApprovalsGovernDirectRequirements: [for approval in privilegedInterfaceApprovals {
		approval: approval.id
		matches: [
			for module in modules
			if module.id == approval.moduleRef && module.providerRef == approval.providerRef
			for unit in module.renderUnits
			if unit.id == approval.unitRef && unit.placement.scope == "node-local" && unit.placement.cardinality == "one-per-daemon" && unit.placement.daemonRef == approval.daemonRef
			for requirement in unit.requiresInterfaces
			if requirement.kind == approval.kind && requirement.daemonRef == approval.daemonRef && requirement.policyProfile == approval.policyProfile {requirement.id},
		] & list.MinItems(1) & list.MaxItems(1)
		evidenceMatches: [for gate in gates.evidence if gate.id == approval.evidenceGateRef && gate.scenario == approval.evidenceRef {gate.id}] & list.MinItems(1) & list.MaxItems(1)
		if approval.reasonCode == "provider-backing" {
			providerBackingMatches: [
				for module in modules
				if module.id == approval.moduleRef
				for unit in module.renderUnits
				if unit.id == approval.unitRef
				for contract in unit.providesInterfaces
				if contract.daemonRef == approval.daemonRef {contract.id},
			] & list.MinItems(1) & list.MaxItems(1)
		}
		if approval.reasonCode == "lifecycle-owner" {
			providerBackingMatches: [
				for module in modules
				if module.id == approval.moduleRef
				for unit in module.renderUnits
				if unit.id == approval.unitRef
				for contract in unit.providesInterfaces
				if contract.daemonRef == approval.daemonRef {contract.id},
			] & list.MaxItems(0)
		}
	}]
	_moduleArtifactRefsUnique: list.UniqueItems([
		for module in modules
		for binding in module.realizationSupport.artifacts.outputBindings {binding.artifactRef},
	]) & true
	_moduleArtifactOutputRefsUnique: list.UniqueItems([
		for module in modules
		for binding in module.realizationSupport.artifacts.outputBindings {binding.outputRef},
	]) & true
	_moduleArtifactsCompatibleWithTarget: [for module in modules for contract in module.realizationSupport.artifacts.contracts {
		module:   module.id
		artifact: contract.id
		matches: [for target in contract.compatibleTargets if target == generation.target {target}] & list.MinItems(1) & list.MaxItems(1)
	}]
	_moduleArtifactsGeneratedExactly: [
		for module in modules
		for unit in module.renderUnits
		for instance in unit.instances
		for output in instance.outputs
		for contract in module.realizationSupport.artifacts.contracts
		if contract.unitRef == unit.id && contract.outputRef == output.ref {
			module:   module.id
			unit:     unit.id
			instance: instance.id
			artifact: output.artifactRef
			matches: [
				for artifact in generation.artifacts
				if artifact.id == output.artifactRef && artifact.kind == contract.kind && artifact.path == path.Join([generation.outputRoot, output.path]) && artifact.format == contract.format && artifact.mode == contract.mode && artifact.required == contract.required && artifact.owner.kind == "render-instance" && artifact.owner.moduleRef == module.id && artifact.owner.unitRef == unit.id && artifact.owner.instanceRef == instance.id && artifact.owner.outputRef == output.ref {artifact.id},
			] & list.MinItems(1) & list.MaxItems(1)
		},
	]
	_generatedModuleArtifactsGoverned: [
		for artifact in generation.artifacts
		if artifact.id != "resolved-plan" {
			artifact: artifact.id
			matches: [
				for module in modules
				for unit in module.renderUnits
				for instance in unit.instances
				for output in instance.outputs
				for contract in module.realizationSupport.artifacts.contracts
				if contract.unitRef == unit.id && contract.outputRef == output.ref && output.artifactRef == artifact.id && contract.kind == artifact.kind && path.Join([generation.outputRoot, output.path]) == artifact.path && contract.format == artifact.format && contract.mode == artifact.mode && contract.required == artifact.required && artifact.owner.kind == "render-instance" && artifact.owner.moduleRef == module.id && artifact.owner.unitRef == unit.id && artifact.owner.instanceRef == instance.id && artifact.owner.outputRef == output.ref {"\(module.id)/\(unit.id)/\(instance.id)/\(output.ref)"},
			] & list.MinItems(1) & list.MaxItems(1)
		},
	]
	_providerIDsUnique: list.UniqueItems([for provider in providers {provider.id}]) & true
	_providerRequiredModuleRefs: [
		for provider in providers
		for moduleRef in provider.realization.moduleRefs.required {
			provider: provider.id
			module:   moduleRef
			matches: [for module in modules if module.id == moduleRef {module.id}] & list.MinItems(1) & list.MaxItems(1)
		},
	]
	_moduleProviderRefs: [for module in modules {
		module:   module.id
		provider: module.providerRef
		matches: [for provider in providers if provider.id == module.providerRef {provider.id}] & list.MinItems(1) & list.MaxItems(1)
		governanceMatches: [
			for provider in providers
			if provider.id == module.providerRef
			for moduleRefs in [provider.realization.moduleRefs.required, provider.realization.moduleRefs.optional]
			for moduleRef in moduleRefs
			if moduleRef == module.id {moduleRef},
		] & list.MinItems(1) & list.MaxItems(1)
	}]
	_providerOwnerHealthRefs: [
		for provider in providers
		if provider.owner != _|_
		for healthRef in provider.owner.healthGateRefs {
			provider: provider.id
			health:   healthRef
			matches: [
				for gate in gates.health
				if gate.id == healthRef && gate.targetKind == "provider" && gate.targetRef == provider.id {gate.id},
			] & list.MinItems(1) & list.MaxItems(1)
		},
	]
	_providerOwnerEvidenceRefs: [
		for provider in providers
		if provider.owner != _|_
		for evidenceRef in provider.owner.evidenceGateRefs {
			provider: provider.id
			evidence: evidenceRef
			matches: [for gate in gates.evidence if gate.id == evidenceRef {gate.id}] & list.MinItems(1) & list.MaxItems(1)
		},
	]
	_noneProviderReadiness: [
		for provider in providers
		if provider.realization.kind == "none" {
			generation: #ExecutionReadinessRequirementV1 & {
				phase: executionReadiness.generation
				code:  "provider-realization-none"
				refs: ["provider:\(provider.id)"]
			}
			apply: #ExecutionReadinessRequirementV1 & {
				phase: executionReadiness.apply
				code:  "provider-realization-none"
				refs: ["provider:\(provider.id)"]
			}
		},
	]
	_providerOwnerUmbrellaReadiness: [
		for provider in providers
		if provider.owner != _|_
		if provider.owner.realizationSupport.scope == "umbrella" {
			generation: #ExecutionReadinessRequirementV1 & {
				phase: executionReadiness.generation
				code:  "provider-owner-umbrella"
				refs: ["provider:\(provider.id)"]
			}
			apply: #ExecutionReadinessRequirementV1 & {
				phase: executionReadiness.apply
				code:  "provider-owner-umbrella"
				refs: ["provider:\(provider.id)"]
			}
		},
	]
	_providerOwnerContractOnlyReadiness: [
		for provider in providers
		if provider.owner != _|_
		if provider.owner.realizationSupport.level == "contract-only" {
			generation: #ExecutionReadinessRequirementV1 & {
				phase: executionReadiness.generation
				code:  "provider-owner-contract-only"
				refs: ["provider:\(provider.id)"]
			}
			apply: #ExecutionReadinessRequirementV1 & {
				phase: executionReadiness.apply
				code:  "provider-owner-contract-only"
				refs: ["provider:\(provider.id)"]
			}
		},
	]
	_providerOwnerRendererReadiness: [
		for provider in providers
		if provider.owner != _|_
		if provider.owner.realizationSupport.level != "contract-only"
		if len([for rendererRef in provider.owner.realizationSupport.compatibleRendererRefs if rendererRef == selectedRendererID {rendererRef}]) == 0 {
			generation: #ExecutionReadinessRequirementV1 & {
				phase: executionReadiness.generation
				code:  "renderer-incompatible"
				refs: ["provider:\(provider.id)", "renderer:\(selectedRendererID)"]
			}
			apply: #ExecutionReadinessRequirementV1 & {
				phase: executionReadiness.apply
				code:  "renderer-incompatible"
				refs: ["provider:\(provider.id)", "renderer:\(selectedRendererID)"]
			}
		},
	]
	_providerOwnerInputContractReadiness: [
		for provider in providers
		if provider.owner != _|_
		if provider.owner.realizationSupport.level != "contract-only"
		if provider.owner.realizationSupport.inputs.contractComplete == false {
			generation: #ExecutionReadinessRequirementV1 & {
				phase: executionReadiness.generation
				code:  "input-contract-incomplete"
				refs: ["provider:\(provider.id)"]
			}
			apply: #ExecutionReadinessRequirementV1 & {
				phase: executionReadiness.apply
				code:  "input-contract-incomplete"
				refs: ["provider:\(provider.id)"]
			}
		},
	]
	_providerOwnerRequiredInputReadiness: [
		for provider in providers
		if provider.owner != _|_
		if provider.owner.realizationSupport.level != "contract-only"
		for inputRef in provider.owner.realizationSupport.inputs.requiredRefs
		if len([for key, _ in provider.owner.inputs.values if key == inputRef {key}]) == 0
		if len([for key, _ in provider.owner.inputs.secretRefs if key == inputRef {key}]) == 0 {
			generation: #ExecutionReadinessRequirementV1 & {
				phase: executionReadiness.generation
				code:  "required-input-missing"
				refs: ["provider:\(provider.id)", "input:\(provider.id)/\(inputRef)"]
			}
			apply: #ExecutionReadinessRequirementV1 & {
				phase: executionReadiness.apply
				code:  "required-input-missing"
				refs: ["provider:\(provider.id)", "input:\(provider.id)/\(inputRef)"]
			}
		},
	]
	_providerOwnerRequiredArtifactReadiness: [
		for provider in providers
		if provider.owner != _|_
		if provider.owner.realizationSupport.level != "contract-only"
		for artifactRef in provider.owner.realizationSupport.artifacts.requiredRefs
		if len([for artifact in generation.artifacts if artifact.id == artifactRef && artifact.required == true {artifact.id}]) == 0 {
			generation: #ExecutionReadinessRequirementV1 & {
				phase: executionReadiness.generation
				code:  "required-artifact-missing"
				refs: ["provider:\(provider.id)", "artifact:\(artifactRef)"]
			}
			apply: #ExecutionReadinessRequirementV1 & {
				phase: executionReadiness.apply
				code:  "required-artifact-missing"
				refs: ["provider:\(provider.id)", "artifact:\(artifactRef)"]
			}
		},
	]
	_providerOwnerApplySupportReadiness: [
		for provider in providers
		if provider.owner != _|_
		if provider.owner.realizationSupport.level != "apply-ready" {
			apply: #ExecutionReadinessRequirementV1 & {
				phase: executionReadiness.apply
				code:  "provider-owner-apply-support-missing"
				refs: ["provider:\(provider.id)"]
			}
		},
	]
	_providerOwnerRequiredEvidenceReadiness: [
		for provider in providers
		if provider.owner != _|_
		if provider.owner.realizationSupport.level == "apply-ready"
		for evidenceRef in provider.owner.realizationSupport.evidence.requiredRefs
		if len([for resolvedEvidence in evidence if resolvedEvidence == evidenceRef {resolvedEvidence}]) == 0 {
			apply: #ExecutionReadinessRequirementV1 & {
				phase: executionReadiness.apply
				code:  "required-evidence-missing"
				refs: ["provider:\(provider.id)", "evidence:\(evidenceRef)"]
			}
		},
	]
	_moduleUmbrellaReadiness: [
		for module in modules
		if module.realizationSupport.scope == "umbrella" {
			generation: #ExecutionReadinessRequirementV1 & {
				phase: executionReadiness.generation
				code:  "module-umbrella"
				refs: ["module:\(module.id)"]
			}
			apply: #ExecutionReadinessRequirementV1 & {
				phase: executionReadiness.apply
				code:  "module-umbrella"
				refs: ["module:\(module.id)"]
			}
		},
	]
	_moduleContractOnlyReadiness: [
		for module in modules
		if module.realizationSupport.level == "contract-only" {
			generation: #ExecutionReadinessRequirementV1 & {
				phase: executionReadiness.generation
				code:  "module-contract-only"
				refs: ["module:\(module.id)"]
			}
			apply: #ExecutionReadinessRequirementV1 & {
				phase: executionReadiness.apply
				code:  "module-contract-only"
				refs: ["module:\(module.id)"]
			}
		},
	]
	_moduleRenderUnitsReadiness: [
		for module in modules
		if len(module.renderUnits) == 0 {
			generation: #ExecutionReadinessRequirementV1 & {
				phase: executionReadiness.generation
				code:  "module-render-units-missing"
				refs: ["module:\(module.id)"]
			}
			apply: #ExecutionReadinessRequirementV1 & {
				phase: executionReadiness.apply
				code:  "module-render-units-missing"
				refs: ["module:\(module.id)"]
			}
		},
	]
	_moduleRendererReadiness: [
		for module in modules
		if module.realizationSupport.level != "contract-only"
		for unit in module.renderUnits
		if unit.rendererRef != selectedRendererID {
			generation: #ExecutionReadinessRequirementV1 & {
				phase: executionReadiness.generation
				code:  "renderer-incompatible"
				refs: ["module:\(module.id)", "unit:\(module.id)/\(unit.id)", "renderer:\(selectedRendererID)"]
			}
			apply: #ExecutionReadinessRequirementV1 & {
				phase: executionReadiness.apply
				code:  "renderer-incompatible"
				refs: ["module:\(module.id)", "unit:\(module.id)/\(unit.id)", "renderer:\(selectedRendererID)"]
			}
		},
	]
	_moduleInputContractReadiness: [
		for module in modules
		if module.realizationSupport.level != "contract-only"
		if module.realizationSupport.inputs.contractComplete == false {
			generation: #ExecutionReadinessRequirementV1 & {
				phase: executionReadiness.generation
				code:  "input-contract-incomplete"
				refs: ["module:\(module.id)"]
			}
			apply: #ExecutionReadinessRequirementV1 & {
				phase: executionReadiness.apply
				code:  "input-contract-incomplete"
				refs: ["module:\(module.id)"]
			}
		},
	]
	_moduleRequiredInputReadiness: [
		for module in modules
		if module.realizationSupport.level != "contract-only"
		for inputRef in module.realizationSupport.inputs.requiredRefs
		for unit in module.renderUnits
		if len([for declaredRef in unit.publicInputRefs if declaredRef == inputRef {declaredRef}]) > 0 || len([for declaredRef in unit.secretInputRefs if declaredRef == inputRef {declaredRef}]) > 0
		if len([for key, _ in unit.values if key == inputRef {key}]) == 0
		if len([for key, _ in unit.secretRefs if key == inputRef {key}]) == 0 {
			generation: #ExecutionReadinessRequirementV1 & {
				phase: executionReadiness.generation
				code:  "required-input-missing"
				refs: ["module:\(module.id)", "unit:\(module.id)/\(unit.id)", "input:\(module.id)/\(inputRef)"]
			}
			apply: #ExecutionReadinessRequirementV1 & {
				phase: executionReadiness.apply
				code:  "required-input-missing"
				refs: ["module:\(module.id)", "unit:\(module.id)/\(unit.id)", "input:\(module.id)/\(inputRef)"]
			}
		},
	]
	_moduleRequiredArtifactReadiness: [
		for module in modules
		if module.realizationSupport.level != "contract-only"
		for artifactRef in module.realizationSupport.artifacts.requiredRefs
		for binding in module.realizationSupport.artifacts.outputBindings
		if binding.artifactRef == artifactRef
		for unit in module.renderUnits
		if unit.id == binding.unitRef
		for instance in unit.instances
		for output in instance.outputs
		if output.ref == binding.outputRef
		if len([for artifact in generation.artifacts if artifact.id == output.artifactRef && artifact.required == true {artifact.id}]) == 0 {
			generation: #ExecutionReadinessRequirementV1 & {
				phase: executionReadiness.generation
				code:  "required-artifact-missing"
				refs: ["module:\(module.id)", "unit:\(module.id)/\(unit.id)", "artifact:\(module.id)/\(output.artifactRef)"]
			}
			apply: #ExecutionReadinessRequirementV1 & {
				phase: executionReadiness.apply
				code:  "required-artifact-missing"
				refs: ["module:\(module.id)", "unit:\(module.id)/\(unit.id)", "artifact:\(module.id)/\(output.artifactRef)"]
			}
		},
	]
	_moduleArtifactOutputReadiness: [
		for module in modules
		if module.realizationSupport.level != "contract-only"
		for binding in module.realizationSupport.artifacts.outputBindings
		for unit in module.renderUnits
		if unit.id == binding.unitRef
		for instance in unit.instances
		for output in instance.outputs
		if output.ref == binding.outputRef
		if len([
			for artifact in generation.artifacts
			if artifact.id == output.artifactRef
			if artifact.required == true
			if artifact.path == path.Join([generation.outputRoot, output.path]) {artifact.id},
		]) == 0 {
			generation: #ExecutionReadinessRequirementV1 & {
				phase: executionReadiness.generation
				code:  "artifact-output-mismatch"
				refs: ["module:\(module.id)", "unit:\(module.id)/\(unit.id)", "artifact:\(module.id)/\(output.artifactRef)"]
			}
			apply: #ExecutionReadinessRequirementV1 & {
				phase: executionReadiness.apply
				code:  "artifact-output-mismatch"
				refs: ["module:\(module.id)", "unit:\(module.id)/\(unit.id)", "artifact:\(module.id)/\(output.artifactRef)"]
			}
		},
	]
	_moduleApplySupportReadiness: [
		for module in modules
		if module.realizationSupport.level != "apply-ready" {
			apply: #ExecutionReadinessRequirementV1 & {
				phase: executionReadiness.apply
				code:  "module-apply-support-missing"
				refs: ["module:\(module.id)"]
			}
		},
	]
	_moduleRequiredEvidenceReadiness: [
		for module in modules
		if module.realizationSupport.level == "apply-ready"
		for evidenceRef in module.realizationSupport.evidence.requiredRefs
		if len([for resolvedEvidence in evidence if resolvedEvidence == evidenceRef {resolvedEvidence}]) == 0 {
			apply: #ExecutionReadinessRequirementV1 & {
				phase: executionReadiness.apply
				code:  "required-evidence-missing"
				refs: ["module:\(module.id)", "evidence:\(evidenceRef)"]
			}
		},
	]
	if bridge != _|_ {
		// A bridge remains non-executable even when it currently publishes no
		// service. Overlay establishment, the outbound control agent, default-deny
		// policy enforcement and device verification are independent runtime seams.
		_bridgeContractReadiness: {
			overlayGeneration: #ExecutionReadinessRequirementV1 & {
				phase: executionReadiness.generation
				code:  "bridge-overlay-unverified"
				refs: ["bridge:overlay"]
			}
			overlayApply: #ExecutionReadinessRequirementV1 & {
				phase: executionReadiness.apply
				code:  "bridge-overlay-unverified"
				refs: ["bridge:overlay"]
			}
			controlAgentGeneration: #ExecutionReadinessRequirementV1 & {
				phase: executionReadiness.generation
				code:  "bridge-control-agent-unverified"
				refs: ["bridge:control-agent"]
			}
			controlAgentApply: #ExecutionReadinessRequirementV1 & {
				phase: executionReadiness.apply
				code:  "bridge-control-agent-unverified"
				refs: ["bridge:control-agent"]
			}
			policyGeneration: #ExecutionReadinessRequirementV1 & {
				phase: executionReadiness.generation
				code:  "policy-enforcement-unverified"
				refs: ["bridge:policy"]
			}
			policyApply: #ExecutionReadinessRequirementV1 & {
				phase: executionReadiness.apply
				code:  "policy-enforcement-unverified"
				refs: ["bridge:policy"]
			}
			deviceVerifierGeneration: #ExecutionReadinessRequirementV1 & {
				phase: executionReadiness.generation
				code:  "device-verifier-unbound"
				refs: ["identity:edge-device-verifier"]
			}
			deviceVerifierApply: #ExecutionReadinessRequirementV1 & {
				phase: executionReadiness.apply
				code:  "device-verifier-unbound"
				refs: ["identity:edge-device-verifier"]
			}
		}
		_bridgePublicationReadiness: [for publication in bridge.publications {
			bridgeRendererGeneration: #ExecutionReadinessRequirementV1 & {
				phase: executionReadiness.generation
				code:  "bridge-renderer-missing"
				refs: ["publication:\(publication.serviceRef)", "renderer:bridge-edge"]
			}
			bridgeRendererApply: #ExecutionReadinessRequirementV1 & {
				phase: executionReadiness.apply
				code:  "bridge-renderer-missing"
				refs: ["publication:\(publication.serviceRef)", "renderer:bridge-edge"]
			}
			originIdentityGeneration: #ExecutionReadinessRequirementV1 & {
				phase: executionReadiness.generation
				code:  "origin-identity-unbound"
				refs: ["publication:\(publication.serviceRef)", "identity:\(publication.origin.identityRef)"]
			}
			originIdentityApply: #ExecutionReadinessRequirementV1 & {
				phase: executionReadiness.apply
				code:  "origin-identity-unbound"
				refs: ["publication:\(publication.serviceRef)", "identity:\(publication.origin.identityRef)"]
			}
			tlsGeneration: #ExecutionReadinessRequirementV1 & {
				phase: executionReadiness.generation
				code:  "tls-profile-unbound"
				refs: ["publication:\(publication.serviceRef)", "tls:\(publication.serviceRef)"]
			}
			tlsApply: #ExecutionReadinessRequirementV1 & {
				phase: executionReadiness.apply
				code:  "tls-profile-unbound"
				refs: ["publication:\(publication.serviceRef)", "tls:\(publication.serviceRef)"]
			}
			healthGeneration: #ExecutionReadinessRequirementV1 & {
				phase: executionReadiness.generation
				code:  "health-gate-not-executable"
				refs: ["publication:\(publication.serviceRef)", "health:\(publication.healthGateRef)"]
			}
			healthApply: #ExecutionReadinessRequirementV1 & {
				phase: executionReadiness.apply
				code:  "health-gate-not-executable"
				refs: ["publication:\(publication.serviceRef)", "health:\(publication.healthGateRef)"]
			}
		}]
	}
	_routeModuleRefs: [for route in network.routes {
		route:  route.id
		module: route.moduleRef
		matches: [for module in modules if module.id == route.moduleRef {module.id}] & list.MinItems(1) & list.MaxItems(1)
	}]
	_routeServiceEndpointBindings: [for serviceRoute in network.routes {
		route:   serviceRoute.id
		service: serviceRoute.serviceRef
		matches: [
			for module in modules
			if module.id == serviceRoute.moduleRef
			for unit in module.renderUnits
			for endpoint in unit.serviceEndpoints
			if endpoint.serviceRef == serviceRoute.serviceRef && endpoint.upstreamProtocol == serviceRoute.upstreamProtocol && endpoint.targetPort == serviceRoute.targetPort
			if serviceRoute.healthGateRef == "module-\(module.id)-\(endpoint.healthRef)"
			for allowedProtocol in endpoint.allowedIngressProtocols
			if allowedProtocol == serviceRoute.protocol
			for allowedExposure in endpoint.allowedExposures
			if allowedExposure == serviceRoute.exposure {unit.id},
		] & list.MinItems(1) & list.MaxItems(1)
	}]
	_routeServiceOriginSites: [
		for serviceRoute in network.routes
		for module in modules
		if module.id == serviceRoute.moduleRef
		for unit in module.renderUnits
		for endpoint in unit.serviceEndpoints
		if endpoint.serviceRef == serviceRoute.serviceRef {
			route: serviceRoute.id
			matches: [for siteRef in unit.siteRefs if siteRef == serviceRoute.originSiteRef {siteRef}] & list.MinItems(1) & list.MaxItems(1)
			if endpoint.originSelector == "single-site" {
				unitSiteCount: len(unit.siteRefs) & 1
			}
			if endpoint.originSelector == "control-authority-site" {
				authoritySite: serviceRoute.originSiteRef & controlPlane.authoritySiteRef
			}
		},
	]
	_routeServiceOriginNodes: [
		for serviceRoute in network.routes
		for module in modules
		if module.id == serviceRoute.moduleRef
		for unit in module.renderUnits
		for endpoint in unit.serviceEndpoints
		if endpoint.serviceRef == serviceRoute.serviceRef
		for nodeRef in serviceRoute.originNodeRefs {
			route: serviceRoute.id
			node:  nodeRef
			matches: [
				for unitNodeRef in unit.nodeRefs
				for topologyNode in nodes
				if topologyNode.id == unitNodeRef && unitNodeRef == nodeRef && topologyNode.siteRef == serviceRoute.originSiteRef {unitNodeRef},
			] & list.MinItems(1) & list.MaxItems(1)
		},
	]
	_routeServiceOriginNodesComplete: [
		for serviceRoute in network.routes
		for module in modules
		if module.id == serviceRoute.moduleRef
		for unit in module.renderUnits
		for endpoint in unit.serviceEndpoints
		if endpoint.serviceRef == serviceRoute.serviceRef
		for unitNodeRef in unit.nodeRefs
		for topologyNode in nodes
		if topologyNode.id == unitNodeRef && topologyNode.siteRef == serviceRoute.originSiteRef {
			route: serviceRoute.id
			node:  unitNodeRef
			matches: [for routeNodeRef in serviceRoute.originNodeRefs if routeNodeRef == unitNodeRef {routeNodeRef}] & list.MinItems(1) & list.MaxItems(1)
		},
	]
	_routeServiceDataBindings: [
		for serviceRoute in network.routes
		for module in modules
		if module.id == serviceRoute.moduleRef
		for unit in module.renderUnits
		for endpoint in unit.serviceEndpoints
		if endpoint.serviceRef == serviceRoute.serviceRef && endpoint.data != _|_ {
			route: serviceRoute.id
			matches: [for bindingID, _ in data.bindings if bindingID == endpoint.data.bindingRef {bindingID}] & list.MinItems(1) & list.MaxItems(1)
			classes: [
				for requiredClass in endpoint.data.requiredClasses
				for bindingID, dataBinding in data.bindings
				if bindingID == endpoint.data.bindingRef
				for dataClass in dataBinding.classes
				if dataClass == requiredClass {dataClass},
			] & list.MinItems(len(endpoint.data.requiredClasses)) & list.MaxItems(len(endpoint.data.requiredClasses))
			if endpoint.data.locality == "primary-site" {
				locality: [
					for bindingID, dataBinding in data.bindings
					if bindingID == endpoint.data.bindingRef && dataBinding.primarySiteRef == serviceRoute.originSiteRef {bindingID},
				] & list.MinItems(1) & list.MaxItems(1)
			}
			if endpoint.data.locality == "primary-or-replica" {
				locality: [
					for bindingID, dataBinding in data.bindings
					if bindingID == endpoint.data.bindingRef && dataBinding.primarySiteRef == serviceRoute.originSiteRef {bindingID},
					for bindingID, dataBinding in data.bindings
					if bindingID == endpoint.data.bindingRef && dataBinding.replicaSiteRefs != _|_
					for replicaRef in dataBinding.replicaSiteRefs
					if replicaRef == serviceRoute.originSiteRef {bindingID},
				] & list.MinItems(1) & list.MaxItems(1)
			}
		},
	]
	_routeServiceHealthGates: [for serviceRoute in network.routes {
		route: serviceRoute.id
		matches: [
			for healthGate in gates.health
			if healthGate.id == serviceRoute.healthGateRef && healthGate.targetKind == "module" && healthGate.targetRef == serviceRoute.moduleRef {healthGate.id},
		] & list.MinItems(1) & list.MaxItems(1)
	}]
	_capabilityProviderRefs: [for capability in capabilities if capability.providerRef != _|_ {
		capability: capability.id
		provider:   capability.providerRef
		matches: [for provider in providers if provider.id == capability.providerRef {provider.id}] & list.MinItems(1) & list.MaxItems(1)
	}]
	_installProviderRef: [if install.platform.management == "selected-provider" {
		provider: install.platform.providerRef
		matches: [for candidate in providers if candidate.id == install.platform.providerRef {candidate.id}] & list.MinItems(1) & list.MaxItems(1)
	}]
	_providerSiteRefs: [for provider in providers for siteRef in provider.siteRefs {
		provider: provider.id
		site:     siteRef
		matches: [for site in sites if site.id == siteRef {site.id}] & list.MinItems(1) & list.MaxItems(1)
	}]
	_moduleSiteRefs: [for module in modules for siteRef in module.siteRefs {
		module: module.id
		site:   siteRef
		matches: [for site in sites if site.id == siteRef {site.id}] & list.MinItems(1) & list.MaxItems(1)
	}]
	_moduleNodeRefs: [for module in modules for nodeRef in module.nodeRefs {
		module: module.id
		node:   nodeRef
		matches: [for node in nodes if node.id == nodeRef {node.id}] & list.MinItems(1) & list.MaxItems(1)
	}]
	_moduleRequirementRefs: [for module in modules if module.requires != _|_ for requirement in module.requires {
		module:   module.id
		requires: requirement
		matches: [for candidate in modules if candidate.id == requirement {candidate.id}] & list.MinItems(1) & list.MaxItems(1)
	}]
	_routeOriginSiteRefs: [for route in network.routes {
		route: route.id
		site:  route.originSiteRef
		matches: [for site in sites if site.id == route.originSiteRef {site.id}] & list.MinItems(1) & list.MaxItems(1)
	}]
	_routeOriginNodeRefs: [for route in network.routes for nodeRef in route.originNodeRefs {
		route: route.id
		node:  nodeRef
		matches: [for node in nodes if node.id == nodeRef {node.id}] & list.MinItems(1) & list.MaxItems(1)
	}]
	_evidenceHealthRefs: [for gate in gates.evidence if gate.healthGateRefs != _|_ for healthRef in gate.healthGateRefs {
		evidence: gate.id
		health:   healthRef
		matches: [for candidate in gates.health if candidate.id == healthRef {candidate.id}] & list.MinItems(1) & list.MaxItems(1)
	}]
	_evidenceArtifactRefs: [for gate in gates.evidence if gate.artifactRefs != _|_ for artifactRef in gate.artifactRefs {
		evidence: gate.id
		artifact: artifactRef
		matches: [for artifact in generation.artifacts if artifact.id == artifactRef {artifact.id}] & list.MinItems(1) & list.MaxItems(1)
	}]
	_healthSiteRefs: [for gate in gates.health for siteRef in gate.siteRefs {
		health: gate.id
		site:   siteRef
		matches: [for site in sites if site.id == siteRef {site.id}] & list.MinItems(1) & list.MaxItems(1)
	}]
	_healthNodeRefs: [for gate in gates.health if gate.nodeRefs != _|_ for nodeRef in gate.nodeRefs {
		health: gate.id
		node:   nodeRef
		matches: [for node in nodes if node.id == nodeRef {node.id}] & list.MinItems(1) & list.MaxItems(1)
	}]
	_healthRouteRefs: [for gate in gates.health if gate.routeRef != _|_ {
		health: gate.id
		route:  gate.routeRef
		matches: [for route in network.routes if route.id == gate.routeRef {route.id}] & list.MinItems(1) & list.MaxItems(1)
	}]
}

// #ResolvedPlanValidation makes package-hidden cross-graph proofs enforceable
// when the plan contract is imported by the Go authority. The regular
// integrity projection belongs to this validation envelope, not to the
// persisted plan: callers supply only plan, CUE derives every proof, and the Go
// validator decodes only plan after the complete envelope has validated.
// Attackers therefore cannot delete or forge a proof sidecar, and the public
// stackkit.resolved-plan/v1 JSON/OpenAPI surface remains unchanged.
#ResolvedPlanValidation: {
	plan: #ResolvedPlan
	integrity: topology: {
		siteIDsUnique:             plan._siteIDsUnique
		nodeIDsUnique:             plan._nodeIDsUnique
		controlMembersExact:       plan._controlMembersExact
		controlAuthoritySiteExact: plan._controlAuthoritySiteExact
	}
	integrity: placement: {
		workloadRefsUnique: plan._placementWorkloadRefsUnique
		siteRefsUnique:     plan._placementSiteRefsUnique
		nodeRefsUnique:     plan._placementNodeRefsUnique
		sitesExist:         plan._placementSitesExist
		nodesExact:         plan._placementNodesExact
		sitesBackedByNodes: plan._placementSitesBackedByNodes
	}
	integrity: data: {
		if plan.data != _|_ {
			if plan.data.defaultAuthority != "per-workload" {
				defaultAuthorityKind: plan._dataDefaultAuthorityKind
			}
			if plan.data.bindings != _|_ {
				primarySites: plan._dataPrimarySites
				replicaSites: plan._dataReplicaSites
				replicaSets:  plan._dataReplicaSets
			}
		}
	}
	integrity: access: {
		if plan.access != _|_ {
			allowedSiteSets: plan._accessAllowedSiteSets
			allowedSites:    plan._accessAllowedSites
		}
	}
	integrity: availability: {
		haCapabilityRefs:     plan._haCapabilityRefs
		memberFailureDomains: plan._controlMemberFailureDomains
		if plan.controlPlane.mode != "single" {
			memberFailureDomainsUnique: plan._controlMemberFailureDomainsUnique
			memberFailureDomainSpread:  plan._controlMemberFailureDomainSpread
		}
	}
	integrity: install: providerRef: plan._installProviderRef
	integrity: routes: {
		serviceEndpointBindings:    plan._routeServiceEndpointBindings
		serviceOriginSites:         plan._routeServiceOriginSites
		serviceOriginNodes:         plan._routeServiceOriginNodes
		serviceOriginNodesComplete: plan._routeServiceOriginNodesComplete
		serviceDataBindings:        plan._routeServiceDataBindings
		serviceHealthGates:         plan._routeServiceHealthGates
	}
	integrity: runtimeNetworks: {
		idsUnique:                   plan._runtimeNetworkIDsUnique
		ownerSubjectsUnique:         plan._runtimeNetworkOwnerSubjectsUnique
		memberSubjectsUnique:        plan._runtimeNetworkMemberSubjectsUnique
		ownersExact:                 plan._runtimeNetworkOwnersExact
		providersComplete:           plan._runtimeNetworkProvidersComplete
		membersExact:                plan._runtimeNetworkMembersExact
		consumerDaemonLocalityExact: plan._runtimeNetworkConsumerDaemonLocalityExact
		membersBoundExactly:         plan._runtimeNetworkMembersBoundExactly
		bindingsGoverned:            plan._runtimeNetworkBindingsGoverned
		consumersComplete:           plan._runtimeNetworkConsumersComplete
	}
}

// #KitSpecBinding applies one concrete KitDefinition to desired intent. This
// is the canonical CUE seam the future Go compiler fills with spec + inventory.
#KitSpecBinding: {
	definition: #KitDefinition
	spec: #StackSpecV2 & {
		kit: slug: definition.metadata.slug
		sites:           list.MinItems(definition.topology.minSites) & list.MaxItems(definition.topology.maxSites)
		partitionPolicy: definition.partitionPolicy
	}

	_siteKindChecks: [for site in spec.sites {
		site: site.id
		matches: [for allowed in definition.topology.allowedSiteKinds if site.kind == allowed {allowed}] & list.MinItems(1)
	}]
	_requiredSiteKindChecks: [for required in definition.topology.requiredSiteKinds {
		kind: required
		matches: [for site in spec.sites if site.kind == required {site.id}] & list.MinItems(1)
	}]
	_controlModeCheck: [for allowed in definition.topology.controlPlane.allowedModes if spec.controlPlane.mode == allowed {allowed}] & list.MinItems(1)
	if spec.controlPlane.mode == "warm-standby" {
		let selectedAvailabilityPolicy = definition.availability.policies["warm-standby"]
		spec: availability: {
			enabled:             true
			mode:                "warm-standby"
			rpoSeconds:          *selectedAvailabilityPolicy.defaults.rpoSeconds | (int & >=0 & <=selectedAvailabilityPolicy.limits.maxRpoSeconds)
			rtoSeconds:          *selectedAvailabilityPolicy.defaults.rtoSeconds | (int & >0 & <=selectedAvailabilityPolicy.limits.maxRtoSeconds)
			failureDomainSpread: *selectedAvailabilityPolicy.defaults.failureDomainSpread | (int & >=selectedAvailabilityPolicy.limits.minFailureDomainSpread & <=selectedAvailabilityPolicy.limits.maxFailureDomainSpread)
			fencing:             *selectedAvailabilityPolicy.defaults.fencing | ("manual" | "automatic")
		}
		_availabilityFencingAllowed: [for allowed in selectedAvailabilityPolicy.limits.allowedFencing if spec.availability.fencing == allowed {allowed}] & list.MinItems(1) & list.MaxItems(1)
	}
	if spec.controlPlane.mode == "quorum" {
		let selectedAvailabilityPolicy = definition.availability.policies.quorum
		spec: availability: {
			enabled:             true
			mode:                "quorum"
			rpoSeconds:          *selectedAvailabilityPolicy.defaults.rpoSeconds | (int & >=0 & <=selectedAvailabilityPolicy.limits.maxRpoSeconds)
			rtoSeconds:          *selectedAvailabilityPolicy.defaults.rtoSeconds | (int & >0 & <=selectedAvailabilityPolicy.limits.maxRtoSeconds)
			failureDomainSpread: *selectedAvailabilityPolicy.defaults.failureDomainSpread | (3 | 5 | 7) & >=selectedAvailabilityPolicy.limits.minFailureDomainSpread & <=selectedAvailabilityPolicy.limits.maxFailureDomainSpread
			fencing:             *selectedAvailabilityPolicy.defaults.fencing | ("manual" | "automatic")
		}
		_availabilityFencingAllowed: [for allowed in selectedAvailabilityPolicy.limits.allowedFencing if spec.availability.fencing == allowed {allowed}] & list.MinItems(1) & list.MaxItems(1)
	}
	_generationStrategyCheck: [for allowed in definition.generation.allowedStrategies if spec.generation.strategy == allowed {allowed}] & list.MinItems(1)
	_generationTargetCheck: [for allowed in definition.generation.allowedTargets if spec.generation.target == allowed {allowed}] & list.MinItems(1)
	_networkModeCheck:    definition.network.mode & spec.network.mode
	_networkTLSModeCheck: definition.network.defaultTLSMode & spec.network.tls.defaultMode
	if definition.network.domainRequired == true {
		spec: network: domain: base: string & =~"^[A-Za-z0-9-]+(\\.[A-Za-z0-9-]+)+$" & !~"(?i)(^|\\.)(localhost|local)$"
	}
	_authorityKindCheck: [
		for site in spec.sites
		if site.id == spec.controlPlane.authoritySiteRef
		for allowed in definition.topology.controlPlane.allowedAuthorityKinds
		if site.kind == allowed {allowed},
	] & list.MinItems(1)
	if definition.topology.controlPlane.memberSiteScope == "authority-site" {
		_controlMembersAtAuthoritySite: [for member in spec.controlPlane.members {
			node: member
			matches: [
				for node in spec.nodes
				if node.id == member
				if node.siteRef == spec.controlPlane.authoritySiteRef {node.id},
			] & list.MinItems(1) & list.MaxItems(1)
		}]
	}

	if spec.data != _|_ if definition.dataDefaults.authority != "policy" {
		_dataDefaultAuthorityCheck: spec.data.defaultAuthority & definition.dataDefaults.authority
	}
	if spec.data != _|_ if definition.dataDefaults.authority == "home" if definition.dataDefaults.cloudCopyRequiresPolicy == true {
		_homeAuthorityCloudCopyPolicy: #HomeAuthorityCloudCopyBindingV2 & {
			sites: spec.sites
			data:  spec.data
		}
	}

	_forbiddenCapabilityChecks: [for requested in spec.capabilities.enable {
		capability: requested
		matches: [for denied in definition.capabilities.forbidden if requested == denied {denied}] & list.MaxItems(0)
	}]
	_requiredDisableChecks: [for disabled in spec.capabilities.disable {
		capability: disabled
		matches: [for required in definition.capabilities.required if disabled == required {required}] & list.MaxItems(0)
	}]

	_effectiveCapabilities: [
		for capability in list.Concat([definition.capabilities.required, definition.capabilities.defaults, spec.capabilities.enable])
		if len([for disabled in spec.capabilities.disable if capability == disabled {disabled}]) == 0 {capability},
	]
	if spec.access != _|_ {
		_accessPolicyExposureChecks: [for policyID, accessPolicy in spec.access {
			policy: policyID
			matches: [
				for allowed in definition.reachability.accessPolicies.allowedExposures
				if accessPolicy.exposure == allowed {allowed},
			] & list.MinItems(1)
		}]
		_lanStepDownChecks: [for policyID, accessPolicy in spec.access if accessPolicy.lanStepDown != _|_ if accessPolicy.lanStepDown == true {
			policy:  policyID
			allowed: definition.reachability.accessPolicies.lanStepDownAllowed & true
		}]
	}
	if spec.routes != _|_ {
		_routeReachabilityChecks: [for routeID, serviceRoute in spec.routes {
			route:   routeID
			allowed: definition.reachability.routes[serviceRoute.exposure].allowed & true
		}]
		_routeCapabilityChecks: [
			for routeID, serviceRoute in spec.routes
			for required in definition.reachability.routes[serviceRoute.exposure].requiredCapabilities {
				route:      routeID
				capability: required
				matches: [
					for selected in _effectiveCapabilities
					if required == selected {selected},
				] & list.MinItems(1)
			},
		]
		_routeAccessPolicyChecks: [for routeID, serviceRoute in spec.routes {
			route: routeID
			references: [
				for policyID, _ in spec.access
				if serviceRoute.accessPolicyRef == policyID {policyID},
			] & list.MinItems(1) & list.MaxItems(1)
			exposureMatches: [
				for policyID, accessPolicy in spec.access
				if serviceRoute.accessPolicyRef == policyID
				if (serviceRoute.exposure == "local" && (accessPolicy.exposure == "private" || accessPolicy.exposure == "lan")) ||
					(serviceRoute.exposure == "remote-private" && accessPolicy.exposure == "private") ||
					(serviceRoute.exposure == "public" && accessPolicy.exposure == "public") {accessPolicy.exposure},
			] & list.MinItems(1) & list.MaxItems(1)
		}]
	}

	if definition.bridge.required {
		spec: bridge: #BridgeContract
	}
	if definition.deviceEnrollment.required {
		spec: deviceEnrollment: #DeviceEnrollmentPolicy & {
			mode: definition.deviceEnrollment.mode
		}
		_deviceEnrollmentKindCheck: [
			for site in spec.sites
			if site.id == spec.deviceEnrollment.authoritySiteRef
			for allowed in definition.deviceEnrollment.authorityKinds
			if site.kind == allowed {allowed},
		] & list.MinItems(1)
	}
	if definition.deviceEnrollment.required == false {
		spec: deviceEnrollment?: _|_
	}
	if spec.bridge != _|_ if len(definition.bridge.sourceKinds) > 0 {
		_overlaySourceKindPeerCheck: [
			for peer in spec.bridge.overlay.peerSiteRefs
			for site in spec.sites
			if site.id == peer
			for allowed in definition.bridge.sourceKinds
			if site.kind == allowed {site.id},
		] & list.MinItems(1)
		_bridgeSourceKindChecks: [for publication in spec.bridge.publications {
			service: publication.serviceRef
			matches: [
				for site in spec.sites
				if site.id == publication.sourceSiteRef
				for allowed in definition.bridge.sourceKinds
				if site.kind == allowed {allowed},
			] & list.MinItems(1)
		}]
	}
	if spec.bridge != _|_ if len(definition.bridge.edgeKinds) > 0 {
		_overlayEdgeKindPeerCheck: [
			for peer in spec.bridge.overlay.peerSiteRefs
			for site in spec.sites
			if site.id == peer
			for allowed in definition.bridge.edgeKinds
			if site.kind == allowed {site.id},
		] & list.MinItems(1)
		_bridgeEdgeKindChecks: [for publication in spec.bridge.publications {
			service: publication.serviceRef
			matches: [
				for site in spec.sites
				if site.id == publication.edgeSiteRef
				for allowed in definition.bridge.edgeKinds
				if site.kind == allowed {allowed},
			] & list.MinItems(1)
		}]
	}
}
