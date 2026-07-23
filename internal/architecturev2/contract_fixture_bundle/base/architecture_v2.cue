// Package base - additive v2 architecture contracts for typed StackKit planning.
//
// These contracts deliberately do not replace #StackBase yet. They define the
// next canonical input and resolved-plan boundary while the existing rollout
// renderer continues to consume the v1 compatibility projection.
package base

import (
	"list"
	"net"
	"path"
	"strings"
	"struct"
)

#ArchitectureAPIVersion: "stackkit/v2alpha1"

#ContractID:   string & =~"^[a-z][a-z0-9-]*$"
#CapabilityID: #ContractID
#WorkloadID:   #ContractID
#SiteID:       #ContractID
#NodeID:       #ContractID

#SemanticVersion: string & =~"^v?[0-9]+\\.[0-9]+\\.[0-9]+(-[0-9A-Za-z.-]+)?$"
#ContentHash:     string & =~"^sha256:[a-f0-9]{64}$"
#SecretReference: string & =~"^(secret|vault|doppler|techstack)://[^[:space:]]+$"
#AbsolutePath:    string & =~"^/[^[:cntrl:]]*$"

// External host references are opaque control-plane handles. Their fixed
// schemes deliberately carry no server-provider, account, region, native
// resource, address, or credential semantics into StackKits.
#ExternalHostBindingRef:             string & =~"^host-binding://sha256/[a-f0-9]{64}$"
#ExternalHostRef:                    string & =~"^host://sha256/[a-f0-9]{64}$"
#ExternalInventoryRef:               string & =~"^host-inventory://sha256/[a-f0-9]{64}$"
#ExecutionChannelRef:                string & =~"^execution-channel://sha256/[a-f0-9]{64}$"
#HostConformanceRef:                 string & =~"^host-conformance://sha256/[a-f0-9]{64}$"
#ExternalHomeAccessBindingRef:       string & =~"^home-access-binding://sha256/[a-f0-9]{64}$"
#HomeAccessFabricRef:                string & =~"^home-access-fabric://sha256/[a-f0-9]{64}$"
#ExternalBackupTargetBindingRef:     string & =~"^backup-target-binding://sha256/[a-f0-9]{64}$"
#ExternalBackupTargetRef:            string & =~"^backup-target://sha256/[a-f0-9]{64}$"
#BackupCustodyAttestationRef:        string & =~"^backup-custody-attestation://sha256/[a-f0-9]{64}$"
#ExternalHomeBackupTargetBindingRef: string & =~"^home-backup-target-binding://sha256/[a-f0-9]{64}$"
#ExternalHomeBackupTargetRef:        string & =~"^home-backup-target://sha256/[a-f0-9]{64}$"
#HomeBackupCustodyAttestationRef:    string & =~"^home-backup-custody-attestation://sha256/[a-f0-9]{64}$"
#ExternalFederationLinkBindingRef:   string & =~"^federation-link-binding://sha256/[a-f0-9]{64}$"
#FederationLinkFabricRef:            string & =~"^federation-link-fabric://sha256/[a-f0-9]{64}$"
#FederationLinkCustodyRef:           string & =~"^federation-link-custody-attestation://sha256/[a-f0-9]{64}$"
#RFC3339Timestamp:                   string & =~"^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\\.[0-9]{0,8}[1-9])?Z$"

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
	"external-host-admission",
	"host-conformance",
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
	workloads: #KitWorkloadPolicyV2

	accessDefaults:   #KitAccessDefaults
	reachability:     #KitReachabilityContractV2
	dataDefaults:     #KitDataDefaults
	deviceEnrollment: #KitDeviceEnrollment
	partitionPolicy:  #PartitionPolicy
	identityTrust:    #KitIdentityTrustContractV2
	generation:       #KitGenerationContract
	network:          #KitNetworkContract
	availability:     #KitAvailabilityContractV2
	authoring?:       #KitAuthoringContractV2

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
		for requirement in rule.requiredRealizations {
			capability: requirement.capabilityRef
			declared: [
				for declared in list.Concat([capabilities.required, capabilities.defaults, capabilities.optional])
				if requirement.capabilityRef == declared {declared},
			] & list.MinItems(1)
			forbidden: [
				for denied in capabilities.forbidden
				if requirement.capabilityRef == denied {denied},
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
	_identityTrustFreshnessBounds: [
		for issuer in identityTrust.credentialIssuers {value: (issuer.revocationMaxStalenessSeconds <= partitionPolicy.maxStaleVerificationSeconds) & true},
		for verifier in identityTrust.verifierPlacements {value: (verifier.revocationMaxStalenessSeconds <= partitionPolicy.maxStaleVerificationSeconds) & true},
		for distribution in identityTrust.verifierDistributions {value: (distribution.maxStalenessSeconds <= partitionPolicy.maxStaleVerificationSeconds) & true},
	]
	_identityTrustIssuerFreshness: [
		for issuer in identityTrust.credentialIssuers {value: issuer.revocationMaxStalenessSeconds & partitionPolicy.maxStaleVerificationSeconds},
	]
	_identityTrustVerifierFreshness: [
		for verifier in identityTrust.verifierPlacements
		if verifier.placement.selector == "control-authority-site" {value: verifier.revocationMaxStalenessSeconds & 0},
		for verifier in identityTrust.verifierPlacements
		if verifier.placement.selector == "cloud-sites" {value: verifier.revocationMaxStalenessSeconds & partitionPolicy.maxStaleVerificationSeconds},
	]
	_identityTrustDistributionFreshness: [
		for distribution in identityTrust.verifierDistributions {value: distribution.maxStalenessSeconds & partitionPolicy.maxStaleVerificationSeconds},
	]
	if deviceEnrollment.required == true {
		_identityTrustLocalDeviceIssuer: [
			for issuer in identityTrust.credentialIssuers
			if issuer.principal == "device" && issuer.issuanceWithinStackKit == true && issuer.placement.selector == "control-authority-site" && issuer.enrollment.mode == "local-only" && issuer.enrollment.exposure == "lan" {issuer.id},
		] & list.MinItems(1) & list.MaxItems(1)
	}
	if deviceEnrollment.required == false {
		_identityTrustNoStackKitDeviceIssuer: [
			for issuer in identityTrust.credentialIssuers
			if issuer.principal == "device" && issuer.issuanceWithinStackKit == true {issuer.id},
		] & list.MaxItems(0)
	}

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
#KitRouteRealizationRoleV2: "access" | "transport" | "edge" | "egress"

#KitRouteCapabilityRealizationRequirementV2: {
	capabilityRef: #CapabilityID
	role:          #KitRouteRealizationRoleV2
}

#KitRouteExposureRuleV2: {
	allowed: bool
	requiredRealizations: [...#KitRouteCapabilityRealizationRequirementV2] | *[]
	allowedOriginKinds: [...#SiteKind] | *[]

	_requiredRealizationCapabilitiesUnique: list.UniqueItems([for requirement in requiredRealizations {requirement.capabilityRef}]) & true
	_requiredRealizationRolesUnique: list.UniqueItems([for requirement in requiredRealizations {requirement.role}]) & true
	_allowedOriginKindsUnique: list.UniqueItems(allowedOriginKinds) & true

	if allowed == true {
		allowedOriginKinds: list.MinItems(1)
	}
	if allowed == false {
		requiredRealizations: []
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
		local: requiredRealizations: []
	}
	if routes["remote-private"].allowed == true {
		routes: "remote-private": requiredRealizations: list.MinItems(1)
	}
	if routes.public.allowed == true {
		routes: public: requiredRealizations: list.MinItems(1)
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

#IdentityTrustPrincipalV2: "human" | "device" | "workload"

// Definition placements are selectors only. They never carry discovered or
// caller-selected site references.
#KitIdentityTrustPlacementV2: {
	selector: "control-authority-site"
} | {
	selector: "cloud-sites"
} | {
	selector:    "external"
	contractRef: #ContractID
}

#IdentityTrustCatalogOwnerV2: {
	kind:        "catalog"
	providerRef: #ContractID
	moduleRef:   #ContractID
}

#IdentityTrustExternalOwnerV2: {
	kind:        "external"
	contractRef: #ContractID
}

#IdentityTrustOwnerV2: #IdentityTrustCatalogOwnerV2 | #IdentityTrustExternalOwnerV2

#KitIdentityAuthorityV2: {
	id:             #ContractID
	principal:      #IdentityTrustPrincipalV2
	trustDomainRef: #ContractID
	placement:      #KitIdentityTrustPlacementV2
	owner:          #IdentityTrustOwnerV2

	if placement.selector == "external" {
		owner: #IdentityTrustExternalOwnerV2 & {contractRef: placement.contractRef}
	}
	if placement.selector != "external" {
		owner: #IdentityTrustCatalogOwnerV2
	}
}

#IdentityEnrollmentContractV2: {
	mode:     "local-only"
	exposure: "lan"
} | {
	mode:     "none"
	exposure: "none"
}

#KitCredentialIssuerV2: {
	id:           #ContractID
	authorityRef: #ContractID
	principal:    #IdentityTrustPrincipalV2
	issuerRef:    #ContractID
	audienceRefs: [...#ContractID] & list.MinItems(1)
	verificationKeySetRef:         #ContractID
	placement:                     #KitIdentityTrustPlacementV2
	owner:                         #IdentityTrustOwnerV2
	issuanceWithinStackKit:        bool
	credentialTTLSeconds:          int & >=300 & <=86400
	sessionTTLSeconds:             int & >=60 & <=86400
	proofOfPossessionRequired:     bool
	revocationSupported:           true
	revocationMaxStalenessSeconds: int & >=0
	enrollment:                    #IdentityEnrollmentContractV2

	_audienceRefsUnique: list.UniqueItems(audienceRefs) & true
	if placement.selector == "external" {
		owner: #IdentityTrustExternalOwnerV2 & {contractRef: placement.contractRef}
		issuanceWithinStackKit: false
		enrollment: {mode: "none", exposure: "none"}
	}
	if placement.selector != "external" {
		owner: #IdentityTrustCatalogOwnerV2
	}
	if placement.selector == "control-authority-site" {
		issuanceWithinStackKit: true
	}
	if placement.selector == "cloud-sites" if principal == "device" {
		issuanceWithinStackKit: false
		enrollment: {mode: "none", exposure: "none"}
	}
	if placement.selector == "cloud-sites" if principal != "device" {
		issuanceWithinStackKit: true
	}
	if principal == "device" if issuanceWithinStackKit == true {
		proofOfPossessionRequired: true
		enrollment: {mode: "local-only", exposure: "lan"}
	}
	if principal == "device" if issuanceWithinStackKit == false {
		enrollment: {mode: "none", exposure: "none"}
	}
	if principal != "device" {
		enrollment: {mode: "none", exposure: "none"}
	}
}

#KitVerifierPlacementV2: {
	id:        #ContractID
	issuerRef: #ContractID
	principal: #IdentityTrustPrincipalV2
	audienceRefs: [...#ContractID] & list.MinItems(1)
	verificationKeySetRef: #ContractID
	placement: #KitIdentityTrustPlacementV2 & {selector: "control-authority-site" | "cloud-sites"}
	owner:                         #IdentityTrustCatalogOwnerV2
	proofOfPossessionRequired:     bool
	revocationMaxStalenessSeconds: int & >=0

	_audienceRefsUnique: list.UniqueItems(audienceRefs) & true
}

#IdentityVerifierDistributionMaterialV2: "verification-key-reference" | "revocation-state"

#KitVerifierDistributionV2: {
	id:        #ContractID
	issuerRef: #ContractID
	from: #KitIdentityTrustPlacementV2 & {selector: "control-authority-site"}
	to: #KitIdentityTrustPlacementV2 & {selector: "cloud-sites"}
	materials: ["revocation-state", "verification-key-reference"]
	includesSigningAuthority:    false
	includesEnrollmentAuthority: false
	includesPrivateKeyMaterial:  false
	includesCredentialMaterial:  false
	reverseAllowed:              false
	maxStalenessSeconds:         int & >=0
	owner:                       #IdentityTrustCatalogOwnerV2
}

// #KitIdentityTrustContractV2 is immutable kit authority. It names logical
// trust contracts and selectors, but never contains keys, URLs, secrets, or
// runtime site references.
#KitIdentityTrustContractV2: {
	authorities: [...#KitIdentityAuthorityV2] & list.MinItems(1)
	credentialIssuers: [...#KitCredentialIssuerV2] & list.MinItems(1)
	verifierPlacements: [...#KitVerifierPlacementV2] & list.MinItems(1)
	verifierDistributions: [...#KitVerifierDistributionV2] | *[]

	_authorityIDsUnique: list.UniqueItems([for authority in authorities {authority.id}]) & true
	_issuerIDsUnique: list.UniqueItems([for issuer in credentialIssuers {issuer.id}]) & true
	_issuerRefsUnique: list.UniqueItems([for issuer in credentialIssuers {issuer.issuerRef}]) & true
	_verifierIDsUnique: list.UniqueItems([for verifier in verifierPlacements {verifier.id}]) & true
	_distributionIDsUnique: list.UniqueItems([for distribution in verifierDistributions {distribution.id}]) & true

	_issuerAuthorities: [for issuer in credentialIssuers {
		issuer: issuer.id
		matches: [for authority in authorities if authority.id == issuer.authorityRef && authority.principal == issuer.principal {authority.id}] & list.MinItems(1) & list.MaxItems(1)
	}]
	_verifierIssuers: [for verifier in verifierPlacements {
		verifier: verifier.id
		matches: [for issuer in credentialIssuers if issuer.issuerRef == verifier.issuerRef && issuer.principal == verifier.principal && issuer.verificationKeySetRef == verifier.verificationKeySetRef && issuer.proofOfPossessionRequired == verifier.proofOfPossessionRequired {issuer.id}] & list.MinItems(1) & list.MaxItems(1)
		audiences: [for audience in verifier.audienceRefs {
			value: audience
			matches: [for issuer in credentialIssuers if issuer.issuerRef == verifier.issuerRef for accepted in issuer.audienceRefs if accepted == audience {accepted}] & list.MinItems(1) & list.MaxItems(1)
		}]
	}]
	_issuerVerifiers: [for issuer in credentialIssuers {
		issuer: issuer.id
		matches: [for verifier in verifierPlacements if verifier.issuerRef == issuer.issuerRef && verifier.principal == issuer.principal && verifier.verificationKeySetRef == issuer.verificationKeySetRef && verifier.proofOfPossessionRequired == issuer.proofOfPossessionRequired {verifier.id}] & list.MinItems(1)
	}]
	_distributionIssuers: [for distribution in verifierDistributions {
		distribution: distribution.id
		matches: [for issuer in credentialIssuers if issuer.issuerRef == distribution.issuerRef && issuer.issuanceWithinStackKit == true {issuer.id}] & list.MinItems(1) & list.MaxItems(1)
	}]
	// Exported integrity is intentional: hidden fields in an imported CUE
	// package are package-scoped and therefore cannot be the sole authority for
	// a cross-package KitDefinition validation boundary.
	integrity: {
		authorityIDsUnique: list.UniqueItems([for authority in authorities {authority.id}]) & true
		issuerIDsUnique: list.UniqueItems([for issuer in credentialIssuers {issuer.id}]) & true
		issuerRefsUnique: list.UniqueItems([for issuer in credentialIssuers {issuer.issuerRef}]) & true
		verifierIDsUnique: list.UniqueItems([for verifier in verifierPlacements {verifier.id}]) & true
		distributionIDsUnique: list.UniqueItems([for distribution in verifierDistributions {distribution.id}]) & true
		issuerAuthorities: [for issuer in credentialIssuers {
			issuerID: issuer.id
			matches: [for authority in authorities if authority.id == issuer.authorityRef && authority.principal == issuer.principal {authority.id}] & list.MinItems(1) & list.MaxItems(1)
		}]
		verifierIssuers: [for verifier in verifierPlacements {
			verifierID: verifier.id
			matches: [for issuer in credentialIssuers if issuer.issuerRef == verifier.issuerRef && issuer.principal == verifier.principal && issuer.verificationKeySetRef == verifier.verificationKeySetRef && issuer.proofOfPossessionRequired == verifier.proofOfPossessionRequired {issuer.id}] & list.MinItems(1) & list.MaxItems(1)
			audiences: [for audience in verifier.audienceRefs {
				value: audience
				matches: [for issuer in credentialIssuers if issuer.issuerRef == verifier.issuerRef for accepted in issuer.audienceRefs if accepted == audience {accepted}] & list.MinItems(1) & list.MaxItems(1)
			}]
		}]
		issuerVerifiers: [for issuer in credentialIssuers {
			issuerID: issuer.id
			matches: [for verifier in verifierPlacements if verifier.issuerRef == issuer.issuerRef && verifier.principal == issuer.principal && verifier.verificationKeySetRef == issuer.verificationKeySetRef && verifier.proofOfPossessionRequired == issuer.proofOfPossessionRequired {verifier.id}] & list.MinItems(1)
		}]
		distributionIssuers: [for distribution in verifierDistributions {
			distributionID: distribution.id
			matches: [for issuer in credentialIssuers if issuer.issuerRef == distribution.issuerRef && issuer.issuanceWithinStackKit == true {issuer.id}] & list.MinItems(1) & list.MaxItems(1)
		}]
	}
}

#SiteSpec: {
	id:   #SiteID
	kind: #SiteKind

	name?:         string
	failureDomain: string & =~"^.+$"

	network?: {
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

#DNSNameV2:        string & =~"^([A-Za-z0-9]|[A-Za-z0-9][A-Za-z0-9.-]*[A-Za-z0-9])$" & net.FQDN
#PublicDNSNameV2:  #DNSNameV2 & =~"[A-Za-z]" & =~"\\."
#NetworkHostV2:    string & (net.IP | #DNSNameV2)
#NetworkAddressV2: string & net.IP
#NetworkCIDRV2:    string & net.IPCIDR

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
			server: #NetworkHostV2
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
		subnet:   #NetworkCIDRV2 | *"172.20.0.0/16"
		gateway?: #NetworkAddressV2
		mtu:      int & >=576 & <=9216 | *1500
		ipv6:     bool | *false
	}
	dns: {
		servers: [...#NetworkAddressV2] & list.MinItems(1) | *["1.1.1.1", "8.8.8.8"]
		searchDomains: [...#DNSNameV2] | *[]
	}
	tls: {
		// Intent selects policy only. Certificate issuers, challenges,
		// credentials, and termination adapters are catalog-owned realizations.
		defaultMode: "internal" | "off" | "public"
		minVersion:  *"TLS1.2" | "TLS1.3"
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
	lanDiscovery: #LANDiscoveryIntentV2 | *{advertiseRouteRefs: []}
	bridge?: #BridgeContract
	availability: #AvailabilityIntent & {mode: controlPlane.mode}
	deviceEnrollment?: #DeviceEnrollmentPolicy
	partitionPolicy:   #PartitionPolicy
	workloads?: [#WorkloadID]: #WorkloadSelectionV2
	modules?: [string]:        #ModuleIntentV2
	routes?: [string]:         #ServiceRouteIntentV2
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

	_controllerIDs: [for node in nodes if node.enabled for role in node.roles if role == "controller" {node.id}]
	_memberControllers: [for member in controlPlane.members {
		node: member
		matches: [for controller in _controllerIDs if controller == member {controller}] & list.MinItems(1) & list.MaxItems(1)
	}]
	_controllersAreMembers: [for controller in _controllerIDs {
		node: controller
		matches: [for member in controlPlane.members if member == controller {member}] & list.MinItems(1) & list.MaxItems(1)
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
	if len(lanDiscovery.advertiseRouteRefs) > 0 {
		routes: [string]: #ServiceRouteIntentV2
		access: [string]: #AccessPolicyV2
		_lanDiscoveryRouteChecks: [for advertisedRouteRef in lanDiscovery.advertiseRouteRefs {
			routeRef: advertisedRouteRef
			matches: [
				for routeRef, route in routes
				if routeRef == advertisedRouteRef
				if route.exposure == "local"
				if route.host != _|_
				if route.host !~ "(?i)(^|\\.)localhost$"
				for policyRef, policy in access
				if route.accessPolicyRef == policyRef
				if policy.exposure == "lan" {
					routeRef:  routeRef
					policyRef: policyRef
				},
			] & list.MinItems(1) & list.MaxItems(1)
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
		// Policy-scoped federation may contain private cross-site flows that are
		// not public edge publications. Public publications still require their
		// own exact edge-to-source flow below; arbitrary flows remain default-deny,
		// peer-bound, identity-bound, and data-class-bound.
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

// #KitAuthoringContractV2 is the CUE-owned starting intent for native v2
// authoring. It deliberately contains no operator identity, SSH transport,
// provider lease, discovered host capacity, or application selection. Those
// concerns are supplied later through their governed operational contracts.
#KitAuthoringOverrideV2: "metadata.name" | "network.domain.base"

#KitAuthoringContractV2: {
	contractVersion:   #SemanticVersion
	initialSpecStatus: "supported" | "preview"
	requiredOverrides: [...#KitAuthoringOverrideV2] | *[]
	initialSpec: #StackSpecV2

	_requiredOverridesUnique: list.UniqueItems(requiredOverrides) & true
}

// #ProductKitDefinition makes authoring intent mandatory only for the three
// product KitDefinitions. Non-product contract fixtures remain valid generic
// #KitDefinition values and cannot become user-facing authoring authorities by
// inheriting a product seed accidentally.
#ProductKitDefinition: #KitDefinition & {
	authoring: #KitAuthoringContractV2
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
		if mode == "warm-standby" {
			members:        list.MinItems(2)
			haAddOnEnabled: true
		}
		if mode == "quorum" {
			members:        list.MinItems(3)
			_memberCount:   len(members) & (3 | 5 | 7)
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
	_nodeRolesUnique: [for node in nodes {list.UniqueItems(node.roles) & true}]
	_authoritySiteRef: [for site in sites if site.id == controlAuthority.siteRef {site.id}] & list.MinItems(1) & list.MaxItems(1)
	_controllerIDs: [for node in nodes for role in node.roles if role == "controller" {node.id}]
	_memberControllers: [for member in controlAuthority.members {
		node: member
		matches: [for controller in _controllerIDs if controller == member {controller}] & list.MinItems(1) & list.MaxItems(1)
	}]
	_controllersAreMembers: [for controller in _controllerIDs {
		node: controller
		matches: [for member in controlAuthority.members if member == controller {member}] & list.MinItems(1) & list.MaxItems(1)
	}]
	_memberAuthoritySites: [for member in controlAuthority.members {
		node: member
		matches: [for node in nodes if node.id == member && node.siteRef == controlAuthority.siteRef {node.id}] & list.MinItems(1) & list.MaxItems(1)
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
	siteRefs: [...#SiteID] | *[]
	nodeRefs: [...#NodeID] | *[]
	requiresRoles: [...#NodeRoleV2] | *[]

	_siteRefsUnique:      list.UniqueItems(siteRefs) & true
	_nodeRefsUnique:      list.UniqueItems(nodeRefs) & true
	_requiresRolesUnique: list.UniqueItems(requiresRoles) & true
}

#WorkloadSelectionV2: {
	alternative: #ContractID
	// runtimeAdapterRef selects one workload-scoped delivery adapter. It is
	// never a Kit identity, capability provider default, or host/provider
	// lifecycle selection. When omitted, the governed workload alternative may
	// supply one exact default.
	runtimeAdapterRef?: #ContractID
	placement: #WorkloadPlacementIntent | *{}
	settings?: #PublicSettings
	secretRefs?: [string]: #SecretReference
}

#KitWorkloadPolicyV2: {
	required: [...#WorkloadID] | *[]
	defaults: [...#WorkloadID] | *[]
	optional: [...#WorkloadID] | *[]
	forbidden: [...#WorkloadID] | *[]

	_requiredUnique:  list.UniqueItems(required) & true
	_defaultsUnique:  list.UniqueItems(defaults) & true
	_optionalUnique:  list.UniqueItems(optional) & true
	_forbiddenUnique: list.UniqueItems(forbidden) & true
	_selectedDisjoint: list.UniqueItems(list.Concat([required, defaults, optional])) & true
	_forbiddenDisjoint: [
		for selected in list.Concat([required, defaults, optional])
		for denied in forbidden
		if selected == denied {selected},
	] & list.MaxItems(0)
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
	privilege:      *"user" | "admin" | "identity" | "secrets" | "vault" | "recovery"
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
	if privilege == "admin" || privilege == "identity" || privilege == "secrets" || privilege == "vault" || privilege == "recovery" {
		authentication:         "human+device"
		ownerStepUpRequired:    true
		enrolledDeviceRequired: true
	}
}

#ServiceExposureV2: "local" | "remote-private" | "public"

#ServiceEndpointOriginSelectionV2: {
	siteKinds: [...#SiteKind] & list.MinItems(1)
	minSites:                int & >=1
	siteFailureDomainSpread: int & >=1
	nodeFailureDomainSpread: int & >=1
	requiredRoles: [...#NodeRoleV2] | *[]

	_siteKindsUnique:     list.UniqueItems(siteKinds) & true
	_requiredRolesUnique: list.UniqueItems(requiredRoles) & true
}

#ResolvedServiceEndpointOriginSelectionV2: {
	#ServiceEndpointOriginSelectionV2
	siteFailureDomainSpread: int & >=1
	nodeFailureDomainSpread: int & >=1
	siteFailureDomains: [...string & =~"^.+$"] & list.MinItems(siteFailureDomainSpread)
	nodeFailureDomains: [...{
		siteRef:       #SiteID
		failureDomain: string & =~"^.+$"
	}] & list.MinItems(nodeFailureDomainSpread)

	_siteFailureDomainsUnique: list.UniqueItems(siteFailureDomains) & true
	_nodeFailureDomainsUnique: list.UniqueItems([for domain in nodeFailureDomains {"\(domain.siteRef)/\(domain.failureDomain)"}]) & true
}

// #ModuleServiceEndpointV2 is the catalog-owned backend contract for one
// routable service. A StackSpec may choose the listener port and exposure, but
// it cannot invent a backend service, protocol, or target port. Placement is
// inherited from the owning resolved render unit and therefore remains bound
// to the unit's exact site, node, daemon, and instance projection.
#ModuleServiceEndpointV2: {
	serviceRef:       #ContractID
	upstreamProtocol: #NetworkProtocol
	targetPort:       int & >=1 & <=65535
	// A route may not downgrade a catalog-owned sensitive service surface to
	// ordinary user access. Vault and recovery surfaces therefore retain their
	// mandatory device-bound owner step-up through compilation and publication.
	requiredPrivilege: *"user" | "admin" | "identity" | "secrets" | "vault" | "recovery"
	allowedIngressProtocols: [...#NetworkProtocol] & list.MinItems(1)
	allowedExposures: [...#ServiceExposureV2] & list.MinItems(1)
	originSelector:   *"single-site" | "control-authority-site" | "multi-zone" | "edge-pool"
	originSelection?: #ServiceEndpointOriginSelectionV2
	healthRef:        #ContractID
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
	if originSelector == "multi-zone" {
		originSelection: #ServiceEndpointOriginSelectionV2 & {
			nodeFailureDomainSpread: int & >=2
			requiredRoles: []
		}
	}
	if originSelector == "edge-pool" {
		originSelection: #ServiceEndpointOriginSelectionV2 & {
			requiredRoles: ["edge"]
		}
	}
	if originSelector == "single-site" || originSelector == "control-authority-site" {
		originSelection?: _|_
	}
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
	if exposure == "public" {
		protocol: "https"
		host:     #PublicDNSNameV2
	}
}

// #LANDiscoveryIntentV2 is an explicit allowlist of routes that may be
// advertised inside a Home LAN. An empty list is the safe default: a local
// route never becomes discoverable merely because it exists.
#LANDiscoveryIntentV2: {
	advertiseRouteRefs: [...#ContractID] | *[]

	_advertiseRouteRefsUnique: list.UniqueItems(advertiseRouteRefs) & true
}

// #HomeLANDiscoveryProjectionV2 is the complete renderer-visible discovery
// authority. It contains only the route identity and listener fields needed to
// materialize a local advertisement. Raw network/access objects, credentials,
// provider authority, target ports, paths, TLS, methods, and health state are
// deliberately unreachable.
#HomeLANDiscoveryProjectionV2: {
	homeSiteRefs: [...#SiteID]
	advertisements: [...{
		routeRef:      #ContractID
		serviceRef:    #ContractID
		originSiteRef: #SiteID
		originNodeRefs: [...#NodeID] & list.MinItems(1)
		protocol: #NetworkProtocol
		port:     int & >=1 & <=65535
		host:     string & =~"^[A-Za-z0-9][A-Za-z0-9.-]*[A-Za-z0-9]$" & !~"(?i)(^|\\.)localhost$"
		access: {
			policyRef:      #ContractID
			policyExposure: "lan"
			defaultClosed:  true
		}

		_originNodeRefsUnique: list.UniqueItems(originNodeRefs) & true
	}]

	_homeSiteRefsUnique: list.UniqueItems(homeSiteRefs) & true
	_advertisementRefsUnique: list.UniqueItems([for advertisement in advertisements {advertisement.routeRef}]) & true
}

#ResolvedAccessDecisionV2: {
	exposure:               #ServiceExposureV2
	policyExposure:         #AccessPolicyExposureV2
	authentication:         "human" | "device" | "human+device" | "workload" | "none"
	privilege:              *"user" | "admin" | "identity" | "secrets" | "vault" | "recovery"
	enrolledDeviceRequired: bool | *false
	ownerStepUpRequired:    bool | *false
	lanStepDown:            bool | *false
	allowedSiteRefs?: [...#SiteID] & list.MinItems(1)
	allowedMethods?: [...#HTTPMethod] & list.MinItems(1)
	defaultClosed: true
	policyRef:     #ContractID
	if allowedMethods != _|_ {
		_allowedMethodsUnique: list.UniqueItems(allowedMethods) & true
	}

	if exposure == "remote-private" || exposure == "public" {
		authentication: "human" | "device" | "human+device" | "workload"
	}
	if lanStepDown == true {
		exposure:               "local"
		policyExposure:         "lan"
		authentication:         "device" | "human+device"
		enrolledDeviceRequired: true
	}
	if privilege == "admin" || privilege == "identity" || privilege == "secrets" || privilege == "vault" || privilege == "recovery" {
		authentication:         "human+device"
		enrolledDeviceRequired: true
		ownerStepUpRequired:    true
	}
}

#ResolvedRouteTLSV2: {
	required:            bool
	mode:                "off" | "internal" | "terminate-at-edge" | "external"
	minVersion?:         "TLS1.2" | "TLS1.3"
	profileRef?:         #ContractID
	issuerRef?:          #ContractID
	ownerCapabilityRef?: #CapabilityID

	if required == false {
		mode:                "off"
		profileRef?:         _|_
		issuerRef?:          _|_
		ownerCapabilityRef?: _|_
	}
	if required == true {
		mode:       "internal" | "terminate-at-edge" | "external"
		minVersion: "TLS1.2" | "TLS1.3"
	}
	if mode == "internal" || mode == "terminate-at-edge" {
		profileRef:          #ContractID
		issuerRef:           #ContractID
		ownerCapabilityRef?: _|_
	}
	if mode == "external" {
		ownerCapabilityRef: #CapabilityID
		profileRef?:        _|_
		issuerRef?:         _|_
	}
}

// #ResolvedRouteCapabilityRealizationV2 is the immutable, provider-neutral
// proof of which selected contract owns one Definition-required route
// capability. Module-backed providers must resolve to exactly one concrete
// module placement; contract/topology/host/external providers remain explicit
// without leaking infrastructure-provider configuration into StackKits.
#ResolvedRouteCapabilityRealizationV2: {
	capabilityRef:          #CapabilityID
	role:                   #KitRouteRealizationRoleV2
	capabilityContractHash: #ContentHash
	providerRef:            #ContractID
	providerContractHash:   #ContentHash
	realizationKind:        "modules" | "contract" | "topology" | "host" | "external"
	providerSiteRefs: [...#SiteID] & list.MinItems(1)
	moduleOwner?: {
		moduleRef:          #ContractID
		moduleContractHash: #ContentHash
		siteRefs: [...#SiteID] & list.MinItems(1)
		nodeRefs: [...#NodeID] & list.MinItems(1)
		_siteRefsUnique: list.UniqueItems(siteRefs) & true
		_nodeRefsUnique: list.UniqueItems(nodeRefs) & true
	}
	_providerSiteRefsUnique: list.UniqueItems(providerSiteRefs) & true

	if realizationKind == "modules" {
		moduleOwner: _
	}
	if realizationKind != "modules" {
		moduleOwner?: _|_
	}
}

#ResolvedServiceRouteV2: {
	id:             #ContractID
	serviceRef:     #ContractID
	moduleRef:      #ContractID
	originSiteRef?: #SiteID
	originSiteRefs: [...#SiteID] & list.MinItems(1)
	originNodeRefs: [...#NodeID] & list.MinItems(1)
	originSelector:   "single-site" | "control-authority-site" | "multi-zone" | "edge-pool"
	originSelection?: #ResolvedServiceEndpointOriginSelectionV2
	exposure:         #ServiceExposureV2
	protocol:         #NetworkProtocol
	upstreamProtocol: #NetworkProtocol
	port:             int & >=1 & <=65535
	targetPort:       int & >=1 & <=65535
	host?:            string & =~"^.+$"
	path?:            string & =~"^/"
	access: #ResolvedAccessDecisionV2 & {exposure: exposure}
	capabilityRealizations: [...#ResolvedRouteCapabilityRealizationV2] | *[]
	tls:            #ResolvedRouteTLSV2
	healthGateRef:  #ContractID
	backendPoolRef: #ContractID

	_originSiteRefsUnique: list.UniqueItems(originSiteRefs) & true
	if originSelector == "single-site" || originSelector == "control-authority-site" {
		originSiteRef:    originSiteRefs[0]
		originSiteRefs:   list.MaxItems(1)
		originSelection?: _|_
	}
	if originSelector == "multi-zone" || originSelector == "edge-pool" {
		originSiteRef?:  _|_
		originSelection: #ResolvedServiceEndpointOriginSelectionV2
	}

	if protocol == "http" || protocol == "https" {
		host: string & =~"^.+$"
		path: string & =~"^/"
	}
	if protocol == "https" {
		tls: required: true
	}
	if exposure == "public" {
		protocol: "https"
		host:     #PublicDNSNameV2
		access: defaultClosed: true
		tls: {
			required: true
			mode:     "terminate-at-edge" | "external"
		}
	}
	if exposure == "local" {
		capabilityRealizations: []
	}
	if exposure == "remote-private" || exposure == "public" {
		capabilityRealizations: list.MinItems(1)
	}
	_capabilityRealizationRefsUnique: list.UniqueItems([for realization in capabilityRealizations {realization.capabilityRef}]) & true
}

// #ResolvedRouteBackendMemberV2 is one exact node-local render instance that
// may receive traffic for a resolved service route. It deliberately carries
// logical identities only; addresses, sockets and provider metadata remain
// outside renderer-visible routing authority.
#ResolvedRouteBackendMemberV2: {
	siteRef:     #SiteID
	nodeRef:     #NodeID
	instanceRef: #ContractID
}

// #ResolvedRouteBackendPoolV2 is compiler-owned routing authority. Membership
// is reconstructed from the catalog endpoint and exact resolved render-unit
// instances, never inferred by a renderer from labels, exposure or context.
#ResolvedRouteBackendPoolV2: {
	id:               #ContractID
	routeRef:         #ContractID
	serviceRef:       #ContractID
	moduleRef:        #ContractID
	unitRef:          #ContractID
	originSelector:   "single-site" | "control-authority-site" | "multi-zone" | "edge-pool"
	originSelection?: #ResolvedServiceEndpointOriginSelectionV2
	upstreamProtocol: #NetworkProtocol
	targetPort:       int & >=1 & <=65535
	members: [...#ResolvedRouteBackendMemberV2] & list.MinItems(1)

	_memberRefsUnique: list.UniqueItems([for member in members {member.instanceRef}]) & true
	if originSelector == "multi-zone" || originSelector == "edge-pool" {
		originSelection: #ResolvedServiceEndpointOriginSelectionV2
	}
	if originSelector == "single-site" || originSelector == "control-authority-site" {
		originSelection?: _|_
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

#ConnectivityOverlayIntentV2: {
	contractRef: #ContractID
	trafficMode: "management-only" | "policy-scoped"
	peerSiteRefs: [...#SiteID] & list.MinItems(2)

	_peerUnique: list.UniqueItems(peerSiteRefs) & true
}

// #OverlayProviderContractV2 is catalog authority for one outbound-only
// federation transport. StackSpec selects the contract, never a concrete
// server provider, endpoint, credential, route advertisement, or lifecycle.
#OverlayProviderContractV2: {
	id:                  #ContractID
	capabilityRef:       "inter-site-link"
	moduleRef:           #ContractID
	implementation:      "wireguard" | "headscale" | "tailscale" | "netbird" | "pangolin"
	initiation:          "local-outbound"
	outboundEstablished: true
	allowedTrafficModes: [...("management-only" | "policy-scoped")] & list.MinItems(1)
	advertiseDefaultRoute:   false
	advertisePrivateSubnets: false
	allowBroadRoutes:        false

	_allowedTrafficModesUnique: list.UniqueItems(allowedTrafficModes) & true
}

#ResolvedConnectivityOverlayV2: {
	contractRef:             #ContractID
	providerRef:             #ContractID
	providerContractHash:    #ContentHash
	moduleRef:               #ContractID
	implementation:          "wireguard" | "headscale" | "tailscale" | "netbird" | "pangolin"
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

#OutboundControlIntentV2: {
	enabled: true
	actionAllowlist: [...#ContractID] & list.MinItems(1)
	_actionAllowlistUnique: list.UniqueItems(actionAllowlist) & true
}

// Each remote action is a closed catalog-owned command envelope. Destructive
// actions cannot be admitted without an approval receipt requirement.
#RemoteActionContractV2: {
	id:                             #ContractID
	capabilityRef:                  "outbound-control-agent"
	moduleRef:                      #ContractID
	transport:                      "managed-agent" | "mtls-agent"
	issuerRef:                      #ContractID
	audience:                       #ContractID
	maxTTLSeconds:                  int & >=1 & <=300
	destructive:                    bool
	approvalClass:                  "none" | "owner-step-up" | "break-glass"
	approvalReceiptRequired:        bool
	capabilityScopedActions:        true
	requiresSignedActions:          true
	requiresNonce:                  true
	requiresIdempotencyKey:         true
	requiresResolvedPlanHash:       true
	replayProtection:               true
	requiresApprovalForDestructive: true
	if destructive {
		approvalClass:           "owner-step-up" | "break-glass"
		approvalReceiptRequired: true
	}
	if approvalClass != "none" {
		approvalReceiptRequired: true
	}
}

#ResolvedRemoteActionContractV2: {
	id:                             #ContractID
	contractRef:                    #ContractID
	capabilityRef:                  "outbound-control-agent"
	providerRef:                    #ContractID
	providerContractHash:           #ContentHash
	moduleRef:                      #ContractID
	transport:                      "managed-agent" | "mtls-agent"
	issuerRef:                      #ContractID
	audience:                       #ContractID
	maxTTLSeconds:                  int & >=1 & <=300
	destructive:                    bool
	approvalClass:                  "none" | "owner-step-up" | "break-glass"
	approvalReceiptRequired:        bool
	capabilityScopedActions:        true
	requiresSignedActions:          true
	requiresNonce:                  true
	requiresIdempotencyKey:         true
	requiresResolvedPlanHash:       true
	replayProtection:               true
	requiresApprovalForDestructive: true
	if destructive {
		approvalClass:           "owner-step-up" | "break-glass"
		approvalReceiptRequired: true
	}
	if approvalClass != "none" {
		approvalReceiptRequired: true
	}
}

#ResolvedOutboundControlContractV2: {
	enabled:              true
	providerRef:          #ContractID
	providerContractHash: #ContentHash
	moduleRef:            #ContractID
	actionAllowlist: [...#ContractID] & list.MinItems(1)
	actions: [...#ResolvedRemoteActionContractV2] & list.MinItems(1)

	_actionAllowlistUnique: list.UniqueItems(actionAllowlist) & true
	_actionIDsUnique: list.UniqueItems([for action in actions {action.id}]) & true
	_actionClosure: [for actionRef in actionAllowlist {
		matches: [for action in actions if action.id == actionRef && action.contractRef == actionRef {action.id}] & list.MinItems(1) & list.MaxItems(1)
	}]
	_noExtraActions: [for action in actions {
		matches: [for actionRef in actionAllowlist if actionRef == action.id {actionRef}] & list.MinItems(1) & list.MaxItems(1)
	}]
	_ownerClosure: [for action in actions {
		providerRef:          action.providerRef & providerRef
		providerContractHash: action.providerContractHash & providerContractHash
		moduleRef:            action.moduleRef & moduleRef
	}]
}

// RIL actions are catalog-owned references to governed StackKits operations.
// They never carry raw commands, provider credentials, transport endpoints, or
// arbitrary paths. `contract-only` is intentionally non-executable: a later
// product slice must bind the exact contract hash to an authenticated runtime
// owner before an approved action can leave admission.
#RILActionPrimitiveInputV1: {
	id:                  #ContractID
	type:                "opaque-reference" | "boolean" | "integer" | "string-enum"
	required:            bool
	source:              "approved-action-card"
	opaqueReferenceOnly: true
	inlineMaterial:      false
}

#RILActionExecutorContractV1: {
	schemaVersion: "stackkit.ril-action-executor/v1"
	ref:           #ContractID
	version:       string & =~"^[0-9]+\\.[0-9]+\\.[0-9]+$"
	owner: {
		authority: "stackkits"
	}
	operationClasses: [...#ContractID] & list.MinItems(1)
	_operationClassesUnique: list.UniqueItems(operationClasses) & true
	prohibitions: {
		providerLifecycle:    true
		providerInputs:       true
		leaseAuthority:       true
		credentialResolution: true
		transport:            true
		callerCommands:       true
		arbitraryPaths:       true
	}
}

#RILActionPrimitiveContractV1: {
	schemaVersion: "stackkit.ril-action-primitive/v1"
	id:            #ContractID
	version:       string & =~"^[0-9]+\\.[0-9]+\\.[0-9]+$"
	title:         string & =~"^.{3,120}$"
	category:      "plan" | "apply" | "verify" | "rollback" | "service" | "certificate" | "backup"
	support:       "contract-only" | "executor-bound"
	mutation:      bool
	destructive:   bool
	risk:          "read-only" | "low" | "high" | "critical"
	owner: {
		authority:      "stackkits"
		operationClass: #ContractID
	}
	// extensionAuthority is present only when the primitive originates from
	// one exact module contract. It binds discovery and target admission to
	// catalog authority; callers never supply or widen this identity.
	extensionAuthority?: {
		kind:        "module"
		moduleRef:   #ContractID
		providerRef: #ContractID
	}
	approval: {
		required:        true
		authority:       "techstack"
		policyAuthority: "gateway"
		class:           "owner-step-up" | "break-glass"
		receiptRequired: true
	}
	grant: {
		required: true
		audience: "stackkits"
		scopes: [...#ContractID] & list.MinItems(1)
		connectorBindingRequired: true
		_scopesUnique:            list.UniqueItems(scopes) & true
	}
	target: {
		scope:                      "stack" | "module-instance" | "runtime-instance"
		requiresStackID:            true
		requiresResolvedPlanHash:   true
		requiresNodeRef:            bool
		requiresRuntimeInstanceRef: bool
	}
	inputs: [...#RILActionPrimitiveInputV1] | *[]
	_inputsUnique: list.UniqueItems([for input in inputs {input.id}]) & true
	verification: {
		required:       true
		evidenceSchema: "stackkit.ril-action-evidence/v1"
		phases: [...("preflight" | "post-action" | "readback")] & list.MinItems(1)
		_phasesUnique: list.UniqueItems(phases) & true
	}
	recovery: {
		kind:              "none" | "primitive" | "manual"
		requiredOnFailure: bool
		primitiveRef?:     #ContractID
		if kind == "primitive" {
			primitiveRef: #ContractID
		}
		if kind != "primitive" {
			primitiveRef?: _|_
		}
	}
	evidence: {
		requiredFields: [
			"action-card-id",
			"execution-id",
			"primitive-id",
			"primitive-contract-hash",
			"resolved-plan-hash",
			"trace-id",
			"target-ref",
			"status",
			"verification",
			"recovery",
		]
		redactedLogsOnly: true
		protectedDiagnostics: {
			allowed:          true
			required:         false
			referenceScheme:  "diagnostic"
			custodyAuthority: "techstack"
			inlineMaterial:   false
			directAccess:     false
		}
	}
	prohibitions: {
		rawSSH:            true
		rawDocker:         true
		rawOpenTofu:       true
		providerInputs:    true
		callerCommands:    true
		arbitraryPaths:    true
		providerLifecycle: true
	}
	if support == "contract-only" {
		executorRef?: _|_
	}
	if support == "executor-bound" {
		executorRef: #ContractID
	}
	if destructive {
		mutation: true
		risk:     "critical"
		approval: class: "break-glass"
	}
	if mutation {
		recovery: {
			kind:              "primitive" | "manual"
			requiredOnFailure: true
		}
	}
	if !mutation {
		destructive: false
		risk:        "read-only"
		recovery: {
			kind:              "none"
			requiredOnFailure: false
		}
	}
}

#BridgeContract: {
	overlay: #ConnectivityOverlayIntentV2
	publications: [...#ServicePublication] | *[]
	policy:       #BridgePolicy
	controlAgent: #OutboundControlIntentV2

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
	overlay: #ResolvedConnectivityOverlayV2
	publications: [...#ResolvedServicePublicationV2] | *[]
	policy:       #BridgePolicy
	controlAgent: #ResolvedOutboundControlContractV2

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
	basis:             "local-device" | "failure-domain" | "site-and-link"
	memberSiteScope:   "control-member-sites" | "authority-site-control-members"
	partitionBehavior: "local-control-continues" | "failure-domain-failover" | "home-authority-continues-cloud-edge-fails-closed"
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
	health?: [...#HealthCheckContractV2]
	evidence?: [...string]
	tlsProfile?: #TLSProfileV2
}

// TLS issuers and profiles are immutable catalog realizations. StackSpec may
// select only the desired TLS mode; it cannot select an issuer, challenge,
// credential location, or termination adapter.
#TLSCatalogOwnerV2: {
	providerRef:            #ContractID
	moduleRef:              #ContractID
	materializationSupport: "contract-only"
}

#TLSMaterialSlotV2: {
	id:          #ContractID
	purpose:     "certificate-chain" | "private-key" | "trust-root" | "issuer-account-key"
	sensitivity: "public" | "secret"

	if purpose == "certificate-chain" || purpose == "trust-root" {
		sensitivity: "public"
	}
	if purpose == "private-key" || purpose == "issuer-account-key" {
		sensitivity: "secret"
	}
}

#CertificateIssuerV2: {
	id:            #ContractID
	capabilityRef: "internal-pki" | "public-tls"
	kind:          "internal-ca" | "acme"
	challenge:     "none" | "tls-alpn-01" | "http-01" | "dns-01"
	supportedSiteKinds: [...#SiteKind] & list.MinItems(1)
	owner:           #TLSCatalogOwnerV2
	validitySeconds: int & >3600
	requiredInputSlotIDs: [...#ContractID] | *[]
	materialSlots: [...#TLSMaterialSlotV2] & list.MinItems(2)
	_requiredInputSlotIDsUnique: list.UniqueItems(requiredInputSlotIDs) & true
	_materialSlotIDsUnique: list.UniqueItems([for slot in materialSlots {slot.id}]) & true
	renewal: {
		required:           true
		healthGateRef:      #ContractID
		renewBeforeSeconds: int & >=3600
	}
	_renewBeforeValidity: renewal.renewBeforeSeconds & <validitySeconds

	if kind == "internal-ca" {
		capabilityRef: "internal-pki"
		challenge:     "none"
	}
	if kind == "acme" {
		capabilityRef: "public-tls"
		challenge:     "tls-alpn-01" | "http-01" | "dns-01"
	}
}

#TLSProfileV2: {
	id:             #ContractID
	capabilityRef:  "internal-pki" | "public-tls"
	mode:           "internal" | "terminate-at-edge"
	trustDomain:    "private" | "web-pki"
	minimumVersion: "TLS1.2" | "TLS1.3"
	allowedIssuerKinds: [...("internal-ca" | "acme")] & list.MinItems(1)
	_allowedIssuerKindsUnique: list.UniqueItems(allowedIssuerKinds) & true

	if mode == "internal" {
		capabilityRef: "internal-pki"
		trustDomain:   "private"
		allowedIssuerKinds: ["internal-ca"]
	}
	if mode == "terminate-at-edge" {
		capabilityRef: "public-tls"
		trustDomain:   "web-pki"
		allowedIssuerKinds: ["acme"]
	}
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

// #NonRenderingProviderOwnerRealizationSupportV2 governs host/external owners
// whose implementation is an admission or runtime boundary rather than a
// renderer. It deliberately retains the support wire fields while forcing an
// empty renderer/artifact closure. Apply evidence that is conditional on an
// actual ExternalHostBinding remains governed by the binding/receipt path.
#NonRenderingProviderOwnerRealizationSupportV2: {
	contractVersion: "1.0.0"
	scope:           "umbrella" | "concrete"
	level:           "contract-only" | "generation-ready" | "apply-ready"
	compatibleRendererRefs: []
	inputs: {
		contractComplete: bool
		requiredRefs: [...#ContractID] | *[]
	}
	planInputs: {
		contractComplete: bool | *true
		requiredRefs: []
	}
	artifacts: {
		requiredRefs: []
		outputBindings: []
		contracts: []
	}
	evidence: requiredRefs: [...string & =~"^[^[:space:]]+$"] | *[]

	_compatibleRendererRefsUnique: list.UniqueItems(compatibleRendererRefs) & true
	_inputRefsUnique:              list.UniqueItems(inputs.requiredRefs) & true
	_planInputRefsUnique:          list.UniqueItems(planInputs.requiredRefs) & true
	_artifactRefsUnique:           list.UniqueItems(artifacts.requiredRefs) & true
	_evidenceRefsUnique:           list.UniqueItems(evidence.requiredRefs) & true

	if scope == "umbrella" {
		level: "contract-only"
	}
	if level != "contract-only" {
		scope: "concrete"
		inputs: contractComplete:     true
		planInputs: contractComplete: true
	}
	if level == "apply-ready" {
		evidence: requiredRefs: list.MinItems(1)
	}
}

// Existing host/external owners may be rendering owners. Admission-only owners
// opt into the stricter non-rendering subtype explicitly in the catalog.
#ProviderOwnerRealizationSupportV2: *#ModuleRealizationSupportV2 | #NonRenderingProviderOwnerRealizationSupportV2

// #ProviderRealizationContractV2 makes provider ownership explicit. Required
// modules are selected by the compiler; optional modules are selected only by
// governed StackSpec module intent. A contract realization is selectable
// declarative authority without runtime work. Host/external owners remain
// providers and must never be projected into synthetic modules.
#ProviderRealizationContractV2: {
	kind:                *"none" | "contract" | "host" | "external" | "modules" | "topology"
	ownerRef?:           #ContractID
	realizationSupport?: #ProviderOwnerRealizationSupportV2
	inputBindings?: [#ContractID]: #ProviderOwnerInputBindingV2
	topology?: {
		siteKinds: [...#SiteKind] & list.MinItems(1)
		_siteKindsUnique: list.UniqueItems(siteKinds) & true
	}
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
		topology?:           _|_
	}
	if kind == "host" || kind == "external" {
		ownerRef:           #ContractID
		realizationSupport: #ProviderOwnerRealizationSupportV2
		inputBindings: [#ContractID]: #ProviderOwnerInputBindingV2 | *{}
		moduleRefs: {
			required: []
			optional: []
		}
		topology?: _|_
	}
	if kind == "topology" {
		topology: siteKinds: [...#SiteKind] & list.MinItems(1)
		ownerRef?:           _|_
		realizationSupport?: _|_
		inputBindings?:      _|_
		moduleRefs: {
			required: []
			optional: []
		}
	}
	if kind == "contract" {
		ownerRef?:           _|_
		realizationSupport?: _|_
		inputBindings?:      _|_
		topology?:           _|_
		moduleRefs: {
			required: []
			optional: []
		}
	}
	if kind == "none" {
		ownerRef?:           _|_
		realizationSupport?: _|_
		inputBindings?:      _|_
		topology?:           _|_
		moduleRefs: {
			required: []
			optional: []
		}
	}
}

// #CapabilityProvider is a StackKits implementation adapter selected from the
// governed catalog (for example a PaaS, mesh, renderer, or host-local module
// owner). It never denotes, selects, configures, or manages a server provider.
// The established name remains for v0.6 wire compatibility.
#CapabilityProvider: {
	metadata: {
		id:      #ContractID
		version: string
	}
	provides: [...#CapabilityID]
	workloadRefs: [...#WorkloadID] | *[]
	// runtimeAdapterRefs is a separate ownership namespace from capabilities
	// and workloads. A PaaS adapter therefore cannot become a Kit-wide
	// capability merely by being selected for one workload.
	runtimeAdapterRefs: [...#ContractID] | *[]
	requires?: [...#CapabilityRequirement]
	conflicts?: [...#ContractID]
	supportedSiteKinds: [...#SiteKind] & list.MinItems(1)
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
	certificateIssuers: [...#CertificateIssuerV2] | *[]
	overlayContracts: [...#OverlayProviderContractV2] | *[]
	remoteActionContracts: [...#RemoteActionContractV2] | *[]
	_providesUnique:           list.UniqueItems(provides) & true
	_workloadRefsUnique:       list.UniqueItems(workloadRefs) & true
	_runtimeAdapterRefsUnique: list.UniqueItems(runtimeAdapterRefs) & true
	_overlayContractIDsUnique: list.UniqueItems([for contract in overlayContracts {contract.id}]) & true
	_remoteActionIDsUnique: list.UniqueItems([for contract in remoteActionContracts {contract.id}]) & true

	_tlsIssuerIDsUnique: list.UniqueItems([for issuer in certificateIssuers {issuer.id}]) & true
	_tlsIssuerBindings: [for issuerContract in certificateIssuers {
		issuer:   issuerContract.id
		provider: issuerContract.owner.providerRef & metadata.id
		capabilityMatches: [for capabilityRef in provides if capabilityRef == issuerContract.capabilityRef {capabilityRef}] & list.MinItems(1) & list.MaxItems(1)
		moduleMatches: [for moduleRef in realization._allModuleRefs if moduleRef == issuerContract.owner.moduleRef {moduleRef}] & list.MinItems(1) & list.MaxItems(1)
		healthMatches: [for healthContract in health if healthContract.id == issuerContract.renewal.healthGateRef {healthContract.id}] & list.MinItems(1) & list.MaxItems(1)
	}]
	_tlsCapabilityBindings: [for capabilityRef in provides if capabilityRef == "internal-pki" || capabilityRef == "public-tls" {
		capability: capabilityRef
		issuerMatches: [for issuer in certificateIssuers if issuer.capabilityRef == capabilityRef {issuer.id}] & list.MinItems(1) & list.MaxItems(1)
	}]
	_overlayContractBindings: [for contract in overlayContracts {
		capabilityMatches: [for capabilityRef in provides if capabilityRef == contract.capabilityRef {capabilityRef}] & list.MinItems(1) & list.MaxItems(1)
		moduleMatches: [for moduleRef in realization._allModuleRefs if moduleRef == contract.moduleRef {moduleRef}] & list.MinItems(1) & list.MaxItems(1)
	}]
	_remoteActionBindings: [for contract in remoteActionContracts {
		capabilityMatches: [for capabilityRef in provides if capabilityRef == contract.capabilityRef {capabilityRef}] & list.MinItems(1) & list.MaxItems(1)
		moduleMatches: [for moduleRef in realization._allModuleRefs if moduleRef == contract.moduleRef {moduleRef}] & list.MinItems(1) & list.MaxItems(1)
	}]

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
	if realization.kind == "topology" {
		realization: topology: siteKinds: supportedSiteKinds
	}
}

#ModuleRuntimeComponentV2: {
	id:        #ContractID
	role:      "application" | "machine-learning" | "database" | "cache" | "database-init"
	lifecycle: "daemon" | "one-shot"
	image: {
		ref:    string & =~"^[^[:space:]]+$"
		digest: #ContentHash
	}
	dependsOn: [...#ContractID] | *[]
	networkRefs: [...#ContractID] & list.MinItems(1)
	command?: [...string & =~"^[^[:cntrl:]]+$"] & list.MinItems(1)
	environment?: [string]:       string
	secretEnvironment?: [string]: #ContractID
	volumes?: [...{
		id:     #ContractID
		target: #AbsolutePath
		class:  "persistent" | "cache"
		backup: bool
	}]
	health: {
		kind:  "http" | "command" | "image" | "completion"
		path?: string & =~"^/.*$"
		port?: int & >=1 & <=65535
		command?: [...string & =~"^[^[:cntrl:]]+$"] & list.MinItems(1)
	}

	_dependsOnUnique:   list.UniqueItems(dependsOn) & true
	_networkRefsUnique: list.UniqueItems(networkRefs) & true
	if volumes != _|_ {
		_volumeIDsUnique: list.UniqueItems([for volume in volumes {volume.id}]) & true
		_volumeTargetsUnique: list.UniqueItems([for volume in volumes {volume.target}]) & true
	}
	if lifecycle == "one-shot" {
		command: list.MinItems(1)
		health: kind: "completion"
	}
	if lifecycle == "daemon" {command?: [...string & =~"^[^[:cntrl:]]+$"] & list.MinItems(1)}
	if health.kind == "http" {health: {path: string, port: int}}
	if health.kind == "command" {health: command: list.MinItems(1)}
	if health.kind == "image" || health.kind == "completion" {
		health: {path?: _|_, port?: _|_, command?: _|_}
	}
}

#ModuleRuntimeContractV2: {
	// A rendered contract is not automatically executable authority. Modules
	// that only project an executor handoff or policy document use
	// contract-handoff and therefore produce no Apply runtime target.
	execution: *"executable" | "contract-handoff"
	kind:      "container" | "native" | "host" | "external" | "control-plane"
	delivery:  "stackkit" | "selected-paas" | "external-control-plane"
	engine?:   "docker" | "podman" | "systemd" | "binary" | "api"
	image?: {
		ref:     string & =~"^[^[:space:]]+$"
		digest?: #ContentHash
	}
	entryComponentRef?: #ContractID
	components?: [...#ModuleRuntimeComponentV2] & list.MinItems(1)
	settings?: #PublicSettings

	if components != _|_ {
		entryComponentRef: #ContractID
		_componentIDsUnique: list.UniqueItems([for component in components {component.id}]) & true
		_entryComponentExact: [for component in components if component.id == entryComponentRef {component.id}] & list.MinItems(1) & list.MaxItems(1)
		_componentDependenciesClosed: [for component in components for dependencyRef in component.dependsOn {
			component:  component.id
			dependency: dependencyRef
			matches: [for candidate in components if candidate.id == dependencyRef {candidate.id}] & list.MinItems(1) & list.MaxItems(1)
		}]
	}

	if kind == "container" {
		engine: "docker" | "podman"
		image: {ref: string & =~"^[^[:space:]]+$"}
	}
	if kind == "external" || kind == "control-plane" {
		delivery: "external-control-plane"
	}
	if execution == "contract-handoff" {
		engine?:            _|_
		image?:             _|_
		entryComponentRef?: _|_
		components?:        _|_
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

// #PolicyEnforcementRequirementV1 names the runtime authority that consumes a
// generated policy. A bound declaration is valid only for an apply-ready,
// executable module; execution and fresh postconditions remain required and
// are never implied by this catalog declaration alone.
#PolicyEnforcementRequirementV1: {
	status:   "unbound" | "bound"
	ownerRef: #ContractID
	policyArtifactRefs: [...#ContractID] & list.MinItems(1)
	targetScope: "home-sites" | "home-control-authority" | "cloud-sites" | "federated-sites"
	operations: [...#ContractID] & list.MinItems(1)
	requiredHealthRef:   #ContractID
	requiredEvidenceRef: #ContractID

	_policyArtifactRefsUnique: list.UniqueItems(policyArtifactRefs) & true
	_operationsUnique:         list.UniqueItems(operations) & true
}

// #RuntimeOwnerRequirementV1 names an exact kit/runtime authority that is
// required but not yet bound. Unlike a policy enforcer it need not consume a
// generated policy artifact. v1 remains deliberately unbound so a catalog
// declaration can never be mistaken for successful execution.
#RuntimeOwnerRequirementV1: {
	status:   "unbound"
	ownerRef: #ContractID
	capabilityRefs: [...#CapabilityID] & list.MinItems(1)
	targetScope: "home-sites" | "cloud-sites" | "federated-sites"
	operations: [...#ContractID] & list.MinItems(1)
	requiredHealthRef:   #ContractID
	requiredEvidenceRef: #ContractID

	_capabilityRefsUnique: list.UniqueItems(capabilityRefs) & true
	_operationsUnique:     list.UniqueItems(operations) & true
}

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

// #ModulePlanInputRefV2 is the deliberately closed set of compiler-owned
// resolved-plan views that a render unit may consume. These are not user
// settings and they never expose the full plan, node inventory, secretRefs,
// management endpoints, or daemon/socket bindings.
#ModulePlanInputRefV2: "stackId" | "kit" | "sites" | "controlPlane" | "bridge" | "bridgePublications" | "bridgeOriginMTLS" | "identity" | "identityTrust" | "data" | "failurePolicy" | "localReachability" | "homeLANDiscovery" | "homeAccessRequirements" | "externalHomeAccessBindings" | "homeAccessHandoff" | "backupTargetRequirements" | "externalBackupTargetBindings" | "homeBackupTargetRequirements" | "externalHomeBackupTargetBindings" | "homeOffsiteBackup" | "cloudOffsiteBackup" | "federationLinkRequirements" | "externalFederationLinkBindings" | "moduleTargets" | "moduleCapabilities" | "hostRuntimePolicy" | "storagePolicy" | "localNetworkPolicy" | "cloudNetworkPolicy" | "publicEdge" | "publicTLS" | "internalPKI" | "cloudAdminMesh"

// #ModuleRenderInputBindingV2 is the closed field-level seam from resolved
// architecture authority into public renderer inputs. Sources are finite and
// typed; arbitrary paths, raw StackSpec access, module outputs, and secrets are
// intentionally unrepresentable.
#ModuleRenderInputSourceRefV2:   "identity.deviceEnrollment" | "identityTrust.homeDeviceAuthority" | "identityTrust.basementVerification" | "identityTrust.cloudAuthority" | "identityTrust.modernHomeAuthority" | "identityTrust.modernCloudVerification" | "access.homeEnforcement" | "localAutonomy.policy" | "network.routes" | "network.cloudHostSecurity" | "host.bootstrapRuntime" | "storage.hostRoots" | "storage.backupRoot"
#ModuleRenderInputValueTypeV2:   "device-enrollment-public-v1" | "home-device-authority-v1" | "basement-identity-verification-v1" | "cloud-identity-authority-v1" | "modern-home-identity-authority-v1" | "modern-cloud-identity-verification-v1" | "home-access-enforcement-v1" | "local-autonomy-policy-v1" | "authority-bound-service-route-list-v4" | "cloud-host-security-network-v1" | "host-bootstrap-runtime-v1" | "host-storage-roots-v1" | "local-backup-root-v1"
#ModuleRenderInputCardinalityV2: "single" | "list"

// This projection renames credentialTTLSeconds to lifetimeSeconds so the
// compiler-derived public value cannot collide with the recursively closed
// secret-like key namespace used by #PublicSettings.
#ModulePublicDeviceEnrollmentV2: {
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
	lifetimeSeconds:           int & >=300 & <=86400
}

// #ModulePublicHomeAccessEnforcementV1 is the complete compiler-owned access
// decision available to the Home access renderer. Fixed local/default-deny
// invariants are validated before projection and therefore do not become
// caller-controlled runtime fields. Identity enrollment, discovery, network
// configuration, endpoints, credentials and provider lifecycle are absent.
#ModulePublicHomeAccessEnforcementV1: {
	stackId: #ContractID
	routes: [...{
		id:            #ContractID
		serviceRef:    #ContractID
		moduleRef:     #ContractID
		originSiteRef: #SiteID
		originNodeRefs: [...#NodeID] & list.MinItems(1)
		protocol:               #NetworkProtocol
		upstreamProtocol:       #NetworkProtocol
		port:                   int & >=1 & <=65535
		targetPort:             int & >=1 & <=65535
		host?:                  string & =~"^.+$"
		path?:                  string & =~"^/"
		policyRef:              #ContractID
		policyExposure:         "private" | "lan"
		authentication:         "human" | "device" | "human+device" | "workload" | "none"
		privilege:              "user" | "admin" | "identity" | "secrets" | "vault" | "recovery"
		enrolledDeviceRequired: bool
		ownerStepUpRequired:    bool
		lanStepDown:            bool
		allowedSiteRefs: [...#SiteID] & list.MinItems(1)
		allowedMethods?: [...#HTTPMethod] & list.MinItems(1)
		tlsRequired:    bool
		tlsMode:        "off" | "internal" | "terminate-at-edge"
		tlsMinVersion?: "TLS1.2" | "TLS1.3"
	}] | []

	_routeIDsUnique: list.UniqueItems([for route in routes {route.id}]) & true
}

#ModulePublicLocalAutonomyDataBindingV1: {
	bindingRef:     #ContractID
	primarySiteRef: #SiteID
	replicaSiteRefs: [...#SiteID] | []
	cloudPlacement:      "denied" | "policy-authorized"
	cloudCopyPolicyRef?: #ContractID
	if cloudPlacement == "denied" {
		cloudCopyPolicyRef?: _|_
	}
	if cloudPlacement == "policy-authorized" {
		cloudCopyPolicyRef: #ContractID
	}
	_replicaSiteRefsUnique: list.UniqueItems(replicaSiteRefs) & true
}

// #ModulePublicLocalAutonomyPolicyV1 is an atomic, operation-shaped partition
// and local-control decision. Device enrollment ceremony, data classes,
// provider/transport authority and general LAN reachability stay with their
// dedicated owners.
#ModulePublicLocalAutonomyPolicyV1: {
	stackId: #ContractID
	kitSlug: "basement-kit" | "modern-homelab"
	topology: {
		authorityHomeSiteRef: #SiteID
		cloudSiteRefs: [...#SiteID] | []
	}
	control: {
		mode:             #ControlPlaneMode
		authoritySiteRef: #SiteID
		memberNodeRefs: [...#NodeID] & list.MinItems(1)
	}
	identity: {
		authoritySiteRef: #SiteID
		enrollmentMode:   "local-only"
		edgeVerifierSiteRefs: [...#SiteID] | []
		possessionBoundSessions:  true
		lanLocationIsIdentity:    false
		availableDuringPartition: true
	}
	data: {
		defaultAuthoritySiteRef: #SiteID
		bindings: [...#ModulePublicLocalAutonomyDataBindingV1] | []
	}
	failure: {
		onCloudLoss:                 "local-continues" | "not-applicable"
		onLinkLoss:                  "local-continues"
		cloudEdge:                   "fail-closed" | "not-applicable"
		maxStaleVerificationSeconds: int & >=0
		denyNewCrossSiteSessions:    true
	}

	_cloudSiteRefsUnique:        list.UniqueItems(topology.cloudSiteRefs) & true
	_memberNodeRefsUnique:       list.UniqueItems(control.memberNodeRefs) & true
	_edgeVerifierSiteRefsUnique: list.UniqueItems(identity.edgeVerifierSiteRefs) & true
	_bindingRefsUnique: list.UniqueItems([for binding in data.bindings {binding.bindingRef}]) & true
}

#ModulePublicHomeDeviceAuthorityV1: {
	authority: {
		id:             #ContractID
		trustDomainRef: #ContractID
		siteRef:        #SiteID
	}
	issuer: {
		id:           #ContractID
		authorityRef: #ContractID
		issuer:       #ResolvedIdentityIssuerURNV2
		audiences: [...#ResolvedIdentityAudienceURNV2] & list.MinItems(1)
		verificationKeySetRef: #ResolvedIdentityKeySetURNV2
		// lifetimeSeconds is the public, non-secret projection of the issuer's
		// credential TTL. The source graph retains credentialTTLSeconds.
		lifetimeSeconds:               int & >=300 & <=86400
		sessionTTLSeconds:             int & >=60 & <=86400
		proofOfPossessionRequired:     true
		revocationSupported:           true
		revocationMaxStalenessSeconds: int & >=0 & <=lifetimeSeconds
		enrollment: {
			mode:     "local-only"
			exposure: "lan"
		}
	}

	_issuerAuthorityClosed: issuer.authorityRef & authority.id
}

#ModulePublicBasementIdentityVerifierV1: {
	id:        #ContractID
	principal: "device" | "human" | "workload"
	issuerRef: #ContractID
	issuer:    #ResolvedIdentityIssuerURNV2
	audiences: [...#ResolvedIdentityAudienceURNV2] & list.MinItems(1)
	verificationKeySetRef:         #ResolvedIdentityKeySetURNV2
	proofOfPossessionRequired:     bool
	revocationMaxStalenessSeconds: int & >=0
	// lifetimeSeconds is the non-secret public projection of the source
	// credential TTL. Enrollment and issuance authority are not projected.
	lifetimeSeconds:   int & >=300 & <=86400
	sessionTTLSeconds: int & >=60 & <=86400

	_revocationWithinLifetime: revocationMaxStalenessSeconds <= lifetimeSeconds
}

#ModulePublicBasementVerificationV1: {
	siteRef: #SiteID
	verifiers: [...#ModulePublicBasementIdentityVerifierV1] & list.MinItems(3) & list.MaxItems(3)

	_idsUnique: list.UniqueItems([for verifier in verifiers {verifier.id}]) & true
	_principalsUnique: list.UniqueItems([for verifier in verifiers {verifier.principal}]) & true
	_deviceVerifier: [for verifier in verifiers if verifier.principal == "device" {verifier.id}] & list.MinItems(1) & list.MaxItems(1)
	_humanVerifier: [for verifier in verifiers if verifier.principal == "human" {verifier.id}] & list.MinItems(1) & list.MaxItems(1)
	_workloadVerifier: [for verifier in verifiers if verifier.principal == "workload" {verifier.id}] & list.MinItems(1) & list.MaxItems(1)
}

#ModulePublicCloudIdentityIssuerV1: {
	id:           #ContractID
	authorityRef: #ContractID
	principal:    "human" | "workload"
	issuer:       #ResolvedIdentityIssuerURNV2
	audiences: [...#ResolvedIdentityAudienceURNV2] & list.MinItems(1)
	verificationKeySetRef:         #ResolvedIdentityKeySetURNV2
	proofOfPossessionRequired:     bool
	revocationMaxStalenessSeconds: int & >=0
	lifetimeSeconds:               int & >=300 & <=86400
	sessionTTLSeconds:             int & >=60 & <=86400

	_revocationWithinLifetime: revocationMaxStalenessSeconds <= lifetimeSeconds
}

#ModulePublicCloudIdentityVerifierV1: {
	id:        #ContractID
	principal: "device" | "human" | "workload"
	issuerRef: #ContractID
	issuer:    #ResolvedIdentityIssuerURNV2
	audiences: [...#ResolvedIdentityAudienceURNV2] & list.MinItems(1)
	verificationKeySetRef:         #ResolvedIdentityKeySetURNV2
	proofOfPossessionRequired:     bool
	revocationMaxStalenessSeconds: int & >=0 & <=86400
}

#ModulePublicCloudIdentityAuthorityV1: {
	siteRef: #SiteID
	issuers: [...#ModulePublicCloudIdentityIssuerV1] & list.MinItems(2) & list.MaxItems(2)
	verifiers: [...#ModulePublicCloudIdentityVerifierV1] & list.MinItems(3) & list.MaxItems(3)

	_issuerIDsUnique: list.UniqueItems([for issuer in issuers {issuer.id}]) & true
	_issuerPrincipalsUnique: list.UniqueItems([for issuer in issuers {issuer.principal}]) & true
	_humanIssuer: [for issuer in issuers if issuer.principal == "human" {issuer.id}] & list.MinItems(1) & list.MaxItems(1)
	_workloadIssuer: [for issuer in issuers if issuer.principal == "workload" {issuer.id}] & list.MinItems(1) & list.MaxItems(1)
	_verifierIDsUnique: list.UniqueItems([for verifier in verifiers {verifier.id}]) & true
	_verifierPrincipalsUnique: list.UniqueItems([for verifier in verifiers {verifier.principal}]) & true
	_deviceVerifier: [for verifier in verifiers if verifier.principal == "device" {verifier.id}] & list.MinItems(1) & list.MaxItems(1)
	_humanVerifier: [for verifier in verifiers if verifier.principal == "human" {verifier.id}] & list.MinItems(1) & list.MaxItems(1)
	_workloadVerifier: [for verifier in verifiers if verifier.principal == "workload" {verifier.id}] & list.MinItems(1) & list.MaxItems(1)
}

#ModulePublicModernHomeIdentityIssuerV1: {
	id:           #ContractID
	authorityRef: #ContractID
	principal:    "device" | "human" | "workload"
	issuer:       #ResolvedIdentityIssuerURNV2
	audiences: [...#ResolvedIdentityAudienceURNV2] & list.MinItems(1)
	verificationKeySetRef:         #ResolvedIdentityKeySetURNV2
	proofOfPossessionRequired:     bool
	revocationMaxStalenessSeconds: int & >=0
	lifetimeSeconds:               int & >=300 & <=86400
	sessionTTLSeconds:             int & >=60 & <=86400
	enrollmentMode:                "local-only" | "none"
	enrollmentExposure:            "lan" | "none"

	_revocationWithinLifetime: revocationMaxStalenessSeconds <= lifetimeSeconds
	if principal == "device" {
		enrollmentMode:     "local-only"
		enrollmentExposure: "lan"
	}
	if principal != "device" {
		enrollmentMode:     "none"
		enrollmentExposure: "none"
	}
}

#ModulePublicModernIdentityVerifierV1: {
	id:        #ContractID
	principal: "device" | "human" | "workload"
	issuerRef: #ContractID
	issuer:    #ResolvedIdentityIssuerURNV2
	audiences: [...#ResolvedIdentityAudienceURNV2] & list.MinItems(1)
	verificationKeySetRef:         #ResolvedIdentityKeySetURNV2
	proofOfPossessionRequired:     bool
	revocationMaxStalenessSeconds: int & >=0 & <=86400
}

#ModulePublicModernVerifierDistributionV1: {
	id:        #ContractID
	principal: "device" | "human" | "workload"
	issuerRef: #ContractID
	issuer:    #ResolvedIdentityIssuerURNV2
	materials: ["revocation-state", "verification-key-reference"]
	maxStalenessSeconds: int & >=0 & <=86400
}

#ModulePublicModernHomeIdentityAuthorityV1: {
	homeSiteRef: #SiteID
	cloudSiteRefs: [...#SiteID] & list.MinItems(1)
	partition: {
		onCloudLoss:                     "local-continues"
		onLinkLoss:                      "local-continues"
		localIdentityAuthorityAvailable: true
		maxStaleVerificationSeconds:     int & >=0 & <=86400
		denyNewCrossSiteSessions:        true
	}
	issuers: [...#ModulePublicModernHomeIdentityIssuerV1] & list.MinItems(3) & list.MaxItems(3)
	verifiers: [...#ModulePublicModernIdentityVerifierV1] & list.MinItems(3) & list.MaxItems(3)
	distributions: [...#ModulePublicModernVerifierDistributionV1] & list.MinItems(3) & list.MaxItems(3)

	_cloudSitesUnique: list.UniqueItems(cloudSiteRefs) & true
	_issuerPrincipalsUnique: list.UniqueItems([for issuer in issuers {issuer.principal}]) & true
	_verifierPrincipalsUnique: list.UniqueItems([for verifier in verifiers {verifier.principal}]) & true
	_distributionPrincipalsUnique: list.UniqueItems([for distribution in distributions {distribution.principal}]) & true
}

#ModulePublicModernCloudIdentityVerificationV1: {
	homeSiteRef: #SiteID
	cloudSiteRefs: [...#SiteID] & list.MinItems(1)
	partition: {
		cloudEdge:                   "fail-closed"
		maxStaleVerificationSeconds: int & >=0 & <=86400
		denyNewCrossSiteSessions:    true
	}
	verifiers: [...#ModulePublicModernIdentityVerifierV1] & list.MinItems(3) & list.MaxItems(3)
	distributions: [...#ModulePublicModernVerifierDistributionV1] & list.MinItems(3) & list.MaxItems(3)

	_cloudSitesUnique: list.UniqueItems(cloudSiteRefs) & true
	_verifierPrincipalsUnique: list.UniqueItems([for verifier in verifiers {verifier.principal}]) & true
	_distributionPrincipalsUnique: list.UniqueItems([for distribution in distributions {distribution.principal}]) & true
}

#ModulePublicHostBootstrapRuntimeV1: {
	installMode: "bootstrapped"
	runtime:     "docker"
	engine:      "docker"
	dataRoot:    #AbsolutePath
}

#ModulePublicHostStorageRootsV1: {
	dataRoot:     #AbsolutePath
	backupRoot:   #AbsolutePath
	stacksRoot:   #AbsolutePath
	mediaRoot?:   #AbsolutePath
	volumeDriver: "local"
}

#ModulePublicLocalBackupRootV1: {
	path:         #AbsolutePath
	volumeDriver: "local"
}

#ModulePublicCloudHostSecurityNetworkV1: {
	networkMode:     "public-capable" | "hybrid"
	transportSubnet: #NetworkCIDRV2
	ipv6:            bool
	tlsMinVersion:   "TLS1.2" | "TLS1.3"
}

// #ModulePublicResolvedRouteV2 excludes TLS credentialRefs and providerRef.
// It is the only network.routes representation a public renderer input can
// receive through a field binding.
#ModulePublicResolvedRouteV2: {
	id:            #ContractID
	serviceRef:    #ContractID
	moduleRef:     #ContractID
	originSiteRef: #SiteID
	originNodeRefs: [...#NodeID] & list.MinItems(1)
	backendPoolRef: #ContractID
	backendPool: {
		upstreamProtocol: #NetworkProtocol
		targetPort:       int & >=1 & <=65535
		members: [...#ResolvedRouteBackendMemberV2] & list.MinItems(1)
	}
	exposure:         #ServiceExposureV2
	protocol:         #NetworkProtocol
	upstreamProtocol: #NetworkProtocol
	port:             int & >=1 & <=65535
	targetPort:       int & >=1 & <=65535
	host?:            string & =~"^.+$"
	path?:            string & =~"^/"
	access:           #ResolvedAccessDecisionV2
	tls:              #ResolvedRouteTLSV2
	healthGateRef:    #ContractID

	_originNodeRefsUnique: list.UniqueItems(originNodeRefs) & true
	_backendMemberRefsUnique: list.UniqueItems([for member in backendPool.members {member.instanceRef}]) & true
	if exposure == "public" {
		host: #PublicDNSNameV2
	}
}

#ModulePublicRouteHealthProbeV3: {
	kind:             "http" | "tcp"
	protocol:         "http" | "https" | "tcp"
	port:             int & >=1 & <=65535
	timeoutSeconds:   int & >=1 & <=300
	method?:          "GET"
	followRedirects?: false
	path?:            string & =~"^/"
	expectedStatuses?: [...int & >=100 & <=599]
	if kind == "http" {
		protocol:        "http" | "https"
		method:          "GET"
		followRedirects: false
		path:            string & =~"^/"
		expectedStatuses: [...int & >=100 & <=599] & list.MinItems(1)
		_statusesUnique: list.UniqueItems(expectedStatuses) & true
	}
	if kind == "tcp" {
		protocol:          "tcp"
		method?:           _|_
		followRedirects?:  _|_
		path?:             _|_
		expectedStatuses?: _|_
	}
}

#ModulePublicResolvedRouteV3: {
	#ModulePublicResolvedRouteV2
	healthProbe: #ModulePublicRouteHealthProbeV3
}

#ModulePublicRouteOriginSelectionV4: {
	siteKinds: [...#SiteKind] & list.MinItems(1)
	minSites:                int & >=1
	siteFailureDomainSpread: int & >=1
	nodeFailureDomainSpread: int & >=1
	requiredRoles: [...#NodeRoleV2]
	siteFailureDomains: [...string & =~"^.+$"] & list.MinItems(siteFailureDomainSpread)
	nodeFailureDomains: [...{
		siteRef:       #SiteID
		failureDomain: string & =~"^.+$"
	}] & list.MinItems(nodeFailureDomainSpread)

	_siteKindsUnique:          list.UniqueItems(siteKinds) & true
	_requiredRolesUnique:      list.UniqueItems(requiredRoles) & true
	_siteFailureDomainsUnique: list.UniqueItems(siteFailureDomains) & true
	_nodeFailureDomainsUnique: list.UniqueItems([for domain in nodeFailureDomains {"\(domain.siteRef)/\(domain.failureDomain)"}]) & true
}

// V4 is the current renderer boundary. It carries the exact selected origin
// set and only the capability identity plus route-relative role from each
// compiler-owned realization. Provider, module, credential, endpoint, and
// lifecycle authority remain unreachable.
#ModulePublicRouteCapabilityAuthorityV4: {
	capabilityRef: #CapabilityID
	role:          #KitRouteRealizationRoleV2
}

#ModulePublicResolvedRouteV4: {
	id:             #ContractID
	serviceRef:     #ContractID
	moduleRef:      #ContractID
	originSiteRef?: #SiteID
	originSiteRefs: [...#SiteID] & list.MinItems(1)
	originNodeRefs: [...#NodeID] & list.MinItems(1)
	originSelector:   "single-site" | "control-authority-site" | "multi-zone" | "edge-pool"
	originSelection?: #ModulePublicRouteOriginSelectionV4
	backendPoolRef:   #ContractID
	backendPool: {
		upstreamProtocol: #NetworkProtocol
		targetPort:       int & >=1 & <=65535
		members: [...#ResolvedRouteBackendMemberV2] & list.MinItems(1)
	}
	exposure:         #ServiceExposureV2
	protocol:         #NetworkProtocol
	upstreamProtocol: #NetworkProtocol
	port:             int & >=1 & <=65535
	targetPort:       int & >=1 & <=65535
	host?:            string & =~"^.+$"
	path?:            string & =~"^/"
	access:           #ResolvedAccessDecisionV2
	tls:              #ResolvedRouteTLSV2
	healthGateRef:    #ContractID
	healthProbe:      #ModulePublicRouteHealthProbeV3
	capabilityAuthorities: [...#ModulePublicRouteCapabilityAuthorityV4] | *[]

	_originSiteRefsUnique: list.UniqueItems(originSiteRefs) & true
	_originNodeRefsUnique: list.UniqueItems(originNodeRefs) & true
	_backendMemberRefsUnique: list.UniqueItems([for member in backendPool.members {member.instanceRef}]) & true
	_capabilityRefsUnique: list.UniqueItems([for authority in capabilityAuthorities {authority.capabilityRef}]) & true
	_rolesUnique: list.UniqueItems([for authority in capabilityAuthorities {authority.role}]) & true
	if originSelector == "single-site" || originSelector == "control-authority-site" {
		originSiteRef:    originSiteRefs[0]
		originSiteRefs:   list.MaxItems(1)
		originSelection?: _|_
	}
	if originSelector == "multi-zone" || originSelector == "edge-pool" {
		originSiteRef?:  _|_
		originSelection: #ModulePublicRouteOriginSelectionV4
		if originSelector == "multi-zone" {
			originSelection: {
				nodeFailureDomainSpread: int & >=2
				requiredRoles: []
			}
		}
		if originSelector == "edge-pool" {
			originSelection: requiredRoles: ["edge"]
		}
	}
	if exposure == "local" {
		capabilityAuthorities: []
	}
	if exposure != "local" {
		capabilityAuthorities: list.MinItems(1)
	}
	if exposure == "public" && tls.mode == "external" {
		_egressAuthority: [for authority in capabilityAuthorities if authority.capabilityRef == tls.ownerCapabilityRef && authority.role == "egress" {authority.capabilityRef}] & list.MinItems(1) & list.MaxItems(1)
		_edgeAuthorities: [for authority in capabilityAuthorities if authority.role == "edge" {authority.capabilityRef}] & list.MaxItems(0)
	}
	if exposure == "public" && tls.mode == "terminate-at-edge" {
		_edgeAuthorities: [for authority in capabilityAuthorities if authority.role == "edge" {authority.capabilityRef}] & list.MinItems(1) & list.MaxItems(1)
		_egressAuthorities: [for authority in capabilityAuthorities if authority.role == "egress" {authority.capabilityRef}] & list.MaxItems(0)
	}
}

#ModuleRenderInputBindingV2: {
	targetRef:   #ContractID
	sourceRef:   #ModuleRenderInputSourceRefV2
	valueType:   #ModuleRenderInputValueTypeV2
	cardinality: #ModuleRenderInputCardinalityV2
	required:    bool

	if sourceRef == "identity.deviceEnrollment" {
		valueType:   "device-enrollment-public-v1"
		cardinality: "single"
		if required == false {defaultValue: #ModulePublicDeviceEnrollmentV2}
	}
	if sourceRef == "identityTrust.homeDeviceAuthority" {
		valueType:   "home-device-authority-v1"
		cardinality: "single"
		if required == false {defaultValue: #ModulePublicHomeDeviceAuthorityV1}
	}
	if sourceRef == "identityTrust.basementVerification" {
		valueType:   "basement-identity-verification-v1"
		cardinality: "single"
		if required == false {defaultValue: #ModulePublicBasementVerificationV1}
	}
	if sourceRef == "identityTrust.cloudAuthority" {
		valueType:   "cloud-identity-authority-v1"
		cardinality: "single"
		if required == false {defaultValue: #ModulePublicCloudIdentityAuthorityV1}
	}
	if sourceRef == "identityTrust.modernHomeAuthority" {
		valueType:   "modern-home-identity-authority-v1"
		cardinality: "single"
		if required == false {defaultValue: #ModulePublicModernHomeIdentityAuthorityV1}
	}
	if sourceRef == "identityTrust.modernCloudVerification" {
		valueType:   "modern-cloud-identity-verification-v1"
		cardinality: "single"
		if required == false {defaultValue: #ModulePublicModernCloudIdentityVerificationV1}
	}
	if sourceRef == "access.homeEnforcement" {
		valueType:   "home-access-enforcement-v1"
		cardinality: "single"
		if required == false {defaultValue: #ModulePublicHomeAccessEnforcementV1}
	}
	if sourceRef == "localAutonomy.policy" {
		valueType:   "local-autonomy-policy-v1"
		cardinality: "single"
		if required == false {defaultValue: #ModulePublicLocalAutonomyPolicyV1}
	}
	if sourceRef == "network.routes" {
		valueType:   "authority-bound-service-route-list-v4"
		cardinality: "list"
		if required == false && valueType == "authority-bound-service-route-list-v4" {defaultValue: [...#ModulePublicResolvedRouteV4]}
	}
	if sourceRef == "network.cloudHostSecurity" {
		valueType:   "cloud-host-security-network-v1"
		cardinality: "single"
		if required == false {defaultValue: #ModulePublicCloudHostSecurityNetworkV1}
	}
	if sourceRef == "host.bootstrapRuntime" {
		valueType:   "host-bootstrap-runtime-v1"
		cardinality: "single"
		if required == false {defaultValue: #ModulePublicHostBootstrapRuntimeV1}
	}
	if sourceRef == "storage.hostRoots" {
		valueType:   "host-storage-roots-v1"
		cardinality: "single"
		if required == false {defaultValue: #ModulePublicHostStorageRootsV1}
	}
	if sourceRef == "storage.backupRoot" {
		valueType:   "local-backup-root-v1"
		cardinality: "single"
		if required == false {defaultValue: #ModulePublicLocalBackupRootV1}
	}
} & ({
	required:      true
	defaultValue?: _|_
} | {
	required: false
	defaultValue!: #ModulePublicDeviceEnrollmentV2 | #ModulePublicHomeDeviceAuthorityV1 | #ModulePublicBasementVerificationV1 | #ModulePublicCloudIdentityAuthorityV1 | #ModulePublicModernHomeIdentityAuthorityV1 | #ModulePublicModernCloudIdentityVerificationV1 | [...#ModulePublicResolvedRouteV4] | #ModulePublicCloudHostSecurityNetworkV1 | #ModulePublicHostBootstrapRuntimeV1 | #ModulePublicHostStorageRootsV1 | #ModulePublicLocalBackupRootV1
})

// #ModuleSecretInputBindingV2 is the closed compiler-owned seam from a
// selected capability secret into a module renderer secret slot. The binding
// carries authority and key provenance; renderers still receive only the
// opaque secret reference.
#ModuleSecretInputBindingV2: {
	source:        "capability-secret"
	capabilityRef: #CapabilityID
	key:           #ContractID
}

#ModulePlanSiteV2: {
	id:            #SiteID
	kind:          #SiteKind
	failureDomain: string & =~"^.+$"
}

// The executor-contract projections deliberately contain desired intent only.
// Inventory facts, management addresses, sockets, provider lifecycle, and
// credentials are not representable on this renderer seam.
#ModulePlanTargetV2: {
	id:      #NodeID
	siteRef: #SiteID
	roles: [...#NodeRoleV2] & list.MinItems(1)
	failureDomain: string & =~"^.+$"
	declaredHardware: {
		arch:            "amd64" | "arm64"
		virtualization?: #RuntimeVirtualizationV2
		profile:         "standard" | "pi" | "gpu" | "storage"
		cpuCores?:       int & >=1
		ramGB?:          int & >=1
		storageGB?:      int & >=1
	}
	_rolesUnique: list.UniqueItems(roles) & true
}

#ModulePlanCapabilityV2: {
	id:           #CapabilityID
	contractHash: #ContentHash
}

#ModuleHostRuntimePolicyV2: {
	install: {
		mode:    "bootstrapped" | "bare" | "advanced"
		runtime: "docker" | "native"
		platform: {
			management:      "selected-provider" | "standalone" | "native"
			fallbackAllowed: bool
			setupPolicy: {
				platform:           "automatic" | "on-demand" | "manual"
				applicationDefault: "on-demand" | "automatic" | "manual"
			}
		}
	}
	host: #SystemIntentV2
	container?: {
		engine:         "docker" | "podman"
		rootless:       bool
		liveRestore:    bool
		storageDriver?: "overlay2" | "btrfs" | "zfs" | "vfs"
		dataRoot:       #AbsolutePath
		logDriver:      "json-file" | "journald" | "syslog" | "none"
	}
	if install.runtime == "docker" {container: _}
	if install.runtime == "native" {container?: _|_}
}

#ModuleStoragePolicyV2: #StorageIntentV2

#ModuleNetworkPolicyV2: {
	mode: #NetworkModeV2
	domain: {
		base:             string & =~"^[A-Za-z0-9][A-Za-z0-9.-]*[A-Za-z0-9]$"
		subdomainPrefix?: #ContractID
	}
	transport: {
		subnet:   #NetworkCIDRV2
		gateway?: #NetworkAddressV2
		mtu:      int & >=576 & <=9216
		ipv6:     bool
	}
	dns: {
		servers: [...#NetworkAddressV2] & list.MinItems(1)
		searchDomains: [...#DNSNameV2]
	}
	tls: {
		defaultMode: "internal" | "off" | "public"
		minVersion:  "TLS1.2" | "TLS1.3"
	}
}

#ModuleLocalNetworkPolicyV2: #ModuleNetworkPolicyV2 & {mode: "private" | "hybrid"}
#ModuleCloudNetworkPolicyV2: #ModuleNetworkPolicyV2 & {mode: "public-capable" | "private" | "hybrid"}

// #ModulePublicTLSV2 is a generation-only executor handoff. It contains the
// exact catalog profile, issuer policy, typed logical material slots, and
// public route bindings, but never certificate/key bytes, SecretReferences,
// DNS/provider lifecycle, management addresses, or observed runtime state.
#ModulePublicTLSV2: {
	capabilityRef: "public-tls"
	providerRef:   #ContractID
	profile: #TLSProfileV2 & {
		capabilityRef: "public-tls"
		mode:          "terminate-at-edge"
		trustDomain:   "web-pki"
	}
	issuer: {
		id:            #ContractID
		capabilityRef: "public-tls"
		kind:          "acme"
		challenge:     "tls-alpn-01" | "http-01" | "dns-01"
		supportedSiteKinds: [...#SiteKind] & list.MinItems(1)
		validitySeconds: int & >3600
		requiredInputSlotIDs: [...#ContractID] | *[]
		materialSlots: [...#TLSMaterialSlotV2] & list.MinItems(2)
		renewal: {
			required:           true
			healthGateRef:      #ContractID
			renewBeforeSeconds: int & >=3600
		}
	}
	routes: [...{
		id:       #ContractID
		host:     #PublicDNSNameV2
		port:     int & >=1 & <=65535
		path:     string & =~"^/"
		exposure: "public"
		protocol: "https"
		tls: {
			mode:       "terminate-at-edge"
			minVersion: "TLS1.2" | "TLS1.3"
			profileRef: profile.id
			issuerRef:  issuer.id
		}
	}] | *[]

	_routeIDsUnique: list.UniqueItems([for route in routes {route.id}]) & true
}

#ModuleInternalPKIV1: {
	capabilityRef: "internal-pki"
	providerRef:   #ContractID
	profile: #TLSProfileV2 & {
		capabilityRef: "internal-pki"
		mode:          "internal"
		trustDomain:   "private"
	}
	issuer: {
		id:            #ContractID
		capabilityRef: "internal-pki"
		kind:          "internal-ca"
		challenge:     "none"
		supportedSiteKinds: ["home"]
		validitySeconds: int & >3600
		requiredInputSlotIDs: []
		materialSlots: [...#TLSMaterialSlotV2] & list.MinItems(3)
		renewal: {
			required:           true
			healthGateRef:      #ContractID
			renewBeforeSeconds: int & >=3600
		}
	}
}

#ModuleBridgeOriginMTLSV1: {
	publications: [...{
		serviceRef:    #ContractID
		identityRef:   #ContractID
		sourceSiteRef: #SiteID
		edgeSiteRef:   #SiteID
		moduleRef:     #ContractID
		unitRef:       #ContractID
		originNodeRefs: [...#NodeID] & list.MinItems(1)
		originInstanceRefs: [...#ContractID] & list.MinItems(1)
		upstreamProtocol: #NetworkProtocol
		targetPort:       int & >=1 & <=65535
		transport: {
			mode:              "mtls-origin-proxy"
			minimumTLSVersion: "TLS1.3"
			serverName:        #DNSNameV2
			outboundOnly:      true
			generalLANAccess:  false
		}
		workloadIdentity: {
			credentialIssuerRef:       #ContractID
			issuer:                    string & =~"^urn:stackkit:"
			audience:                  string & =~"^urn:stackkit:"
			verificationKeySetRef:     string & =~"^urn:stackkit:"
			proofOfPossessionRequired: true
		}
		edgeVerifier: {
			verifierRef:                #ContractID
			distributionRef:            #ContractID
			verificationKeySetRef:      string & =~"^urn:stackkit:"
			maxStalenessSeconds:        int & >=1 & <=300
			includesPrivateKeyMaterial: false
			includesSigningAuthority:   false
			reverseAllowed:             false
		}
	}] & list.MinItems(1)
	_serviceRefsUnique: list.UniqueItems([for publication in publications {publication.serviceRef}]) & true
}

// #ModuleLocalReachabilityV2 is the only network/access view available to a
// Home-local policy renderer. It deliberately excludes network configuration,
// DNS/provider settings, credential refs, management addresses, bridge data,
// and every remote or public route.
#ModuleLocalReachabilityV2: {
	homeSiteRefs: [...#SiteID] & list.MinItems(1)
	routes: [...{
		id:            #ContractID
		serviceRef:    #ContractID
		moduleRef:     #ContractID
		originSiteRef: #SiteID
		originNodeRefs: [...#NodeID] & list.MinItems(1)
		exposure:         "local"
		protocol:         #NetworkProtocol
		upstreamProtocol: #NetworkProtocol
		port:             int & >=1 & <=65535
		targetPort:       int & >=1 & <=65535
		host?:            string & =~"^.+$"
		path?:            string & =~"^/"
		healthGateRef:    #ContractID
		access: {
			policyRef:              #ContractID
			policyExposure:         "private" | "lan"
			authentication:         "human" | "device" | "human+device" | "workload" | "none"
			privilege:              "user" | "admin" | "identity" | "secrets" | "vault" | "recovery"
			enrolledDeviceRequired: bool
			ownerStepUpRequired:    bool
			lanStepDown:            bool
			allowedSiteRefs: [...#SiteID] & list.MinItems(1)
			allowedMethods?: [...#HTTPMethod] & list.MinItems(1)
			defaultClosed: true
		}
		tls: #ResolvedRouteTLSV2
	}] | *[]

	_homeSiteRefsUnique: list.UniqueItems(homeSiteRefs) & true
	_routeIDsUnique: list.UniqueItems([for route in routes {route.id}]) & true
}

// Definitions are closed in CUE, so planInputs rejects every field outside
// the catalog-governed projection union. Presence is bound 1:1 to
// planInputRefs by #ResolvedModuleRenderUnitV2 below.
#ModuleCloudNetworkPostureV1: {
	mode: "public-capable" | "private" | "hybrid"
	transport: {
		subnet: #NetworkCIDRV2
		ipv6:   bool
	}
	tlsMinVersion: "TLS1.2" | "TLS1.3"
}

#ModulePublicEdgeV1: {
	capabilityRef: "public-edge"
	network:       #ModuleCloudNetworkPostureV1
	routes: [...#ModulePublicResolvedRouteV4] | *[]
	_routeIDsUnique: list.UniqueItems([for route in routes {route.id}]) & true
	_exactEdgeRoutes: [for route in routes {
		exposure: route.exposure & "public"
		tls: {
			required: route.tls.required & true
			mode:     route.tls.mode & "terminate-at-edge"
		}
		access: defaultClosed: route.access.defaultClosed & true
		edgeAuthority: [for authority in route.capabilityAuthorities if authority.capabilityRef == capabilityRef && authority.role == "edge" {authority.capabilityRef}] & list.MinItems(1) & list.MaxItems(1)
	}]
}

#ModuleCloudAdminMeshV1: {
	capabilityRef: "private-admin-mesh"
	siteRefs: [...#SiteID] & list.MinItems(1)
	nodeRefs: [...#NodeID] & list.MinItems(1)
	network: #ModuleCloudNetworkPostureV1
	routes: [...#ModulePublicResolvedRouteV4] & list.MinItems(1)

	_siteRefsUnique: list.UniqueItems(siteRefs) & true
	_nodeRefsUnique: list.UniqueItems(nodeRefs) & true
	_routeIDsUnique: list.UniqueItems([for route in routes {route.id}]) & true
	_exactPrivateRoutes: [for route in routes {
		exposure: "private"
		access: {
			policyExposure:         "private"
			authentication:         "human+device"
			enrolledDeviceRequired: true
			lanStepDown:            false
			defaultClosed:          true
		}
		meshAuthority: [for authority in route.capabilityAuthorities if authority.capabilityRef == capabilityRef && authority.role == "access" {authority.capabilityRef}] & list.MinItems(1) & list.MaxItems(1)
	}]
}

#ModuleHomeOffsiteBackupV1: {
	requirements: [#SiteID]: [#CapabilityID]: #HomeBackupTargetRequirementV1
	bindings: [#SiteID]: [#CapabilityID]:     #ExternalHomeBackupTargetBindingV1
}

#ModuleHomeAccessHandoffV1: {
	requirements: [#SiteID]: [#CapabilityID]: #HomeAccessRequirementV1
	bindings: [#SiteID]: [#CapabilityID]:     #ExternalHomeAccessBindingV1
}

#ModuleCloudOffsiteBackupV1: {
	requirements: [#SiteID]: [#CapabilityID]: #BackupTargetRequirementV1
	bindings: [#SiteID]: [#CapabilityID]:     #ExternalBackupTargetBindingV1
}

#ModulePlanInputsV2: {
	stackId?: #ContractID
	kit?: {
		slug:           #KitSlug
		version:        string
		definitionHash: #ContentHash
	}
	sites?: [...#ModulePlanSiteV2] & list.MinItems(1)
	controlPlane?: #ControlPlaneIntent
	bridge?:       #ResolvedBridgeContractV2
	bridgePublications?: [...#ResolvedServicePublicationV2] & list.MinItems(1)
	identity?:          #ResolvedIdentityPlan
	identityTrust?:     #ResolvedIdentityTrustV2
	data?:              #DataPlacementIntent
	failurePolicy?:     #PartitionPolicy
	localReachability?: #ModuleLocalReachabilityV2
	homeLANDiscovery?:  #HomeLANDiscoveryProjectionV2
	homeAccessRequirements?: [#SiteID]: [#CapabilityID]:           #HomeAccessRequirementV1
	externalHomeAccessBindings?: [#SiteID]: [#CapabilityID]:       #ExternalHomeAccessBindingV1
	backupTargetRequirements?: [#SiteID]: [#CapabilityID]:         #BackupTargetRequirementV1
	externalBackupTargetBindings?: [#SiteID]: [#CapabilityID]:     #ExternalBackupTargetBindingV1
	homeBackupTargetRequirements?: [#SiteID]: [#CapabilityID]:     #HomeBackupTargetRequirementV1
	externalHomeBackupTargetBindings?: [#SiteID]: [#CapabilityID]: #ExternalHomeBackupTargetBindingV1
	federationLinkRequirements?: [#CapabilityID]:     #FederationLinkRequirementV1
	externalFederationLinkBindings?: [#CapabilityID]: #ExternalFederationLinkBindingV1
	moduleTargets?: [...#ModulePlanTargetV2] & list.MinItems(1)
	moduleCapabilities?: [...#ModulePlanCapabilityV2] & list.MinItems(1)
	hostRuntimePolicy?:  #ModuleHostRuntimePolicyV2
	storagePolicy?:      #ModuleStoragePolicyV2
	localNetworkPolicy?: #ModuleLocalNetworkPolicyV2
	cloudNetworkPolicy?: #ModuleCloudNetworkPolicyV2
	publicEdge?:         #ModulePublicEdgeV1
	publicTLS?:          #ModulePublicTLSV2
	internalPKI?:        #ModuleInternalPKIV1
	bridgeOriginMTLS?:   #ModuleBridgeOriginMTLSV1
	cloudAdminMesh?:     #ModuleCloudAdminMeshV1
	homeAccessHandoff?:  #ModuleHomeAccessHandoffV1
	homeOffsiteBackup?:  #ModuleHomeOffsiteBackupV1
	cloudOffsiteBackup?: #ModuleCloudOffsiteBackupV1
}

// #ModuleRenderUnitContractV2 is one independently renderable, hash-bound
// implementation unit. rendererRef is exact: selection never falls back to a
// compatible engine or infers a renderer from kind/templateRef.
#ModuleRenderUnitContractV2: {
	id:          #ContractID
	kind:        #ModuleRenderUnitKindV2
	rendererRef: #ContractID
	// compatibleTargets makes target selection an explicit module contract.
	// The compiler selects units before resolving inputs and artifacts; callers
	// never filter a mixed realization after compilation.
	compatibleTargets: [...#GenerationTarget] & list.MinItems(1) | *["compose", "opentofu"]
	templateRef:  string & =~"^[^[:space:]]+$"
	version:      #SemanticVersion
	contractHash: #ContentHash

	publicInputRefs: [...#ContractID] | *[]
	secretInputRefs: [...#ContractID] | *[]
	planInputRefs: [...#ModulePlanInputRefV2] | *[]
	inputBindings: [...#ModuleRenderInputBindingV2] | *[]
	secretInputBindings: [#ContractID]: #ModuleSecretInputBindingV2 | *{}
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
	_planInputRefsUnique:   list.UniqueItems(planInputRefs) & true
	_inputBindingTargetsUnique: list.UniqueItems([for binding in inputBindings {binding.targetRef}]) & true
	_inputBindingTargetsDeclared: [for binding in inputBindings {
		target: binding.targetRef
		matches: [for publicRef in publicInputRefs if publicRef == binding.targetRef {publicRef}] & list.MinItems(1) & list.MaxItems(1)
	}]
	_secretInputBindingTargetsDeclared: [for targetRef, _ in secretInputBindings {
		target: targetRef
		matches: [for secretRef in secretInputRefs if secretRef == targetRef {secretRef}] & list.MinItems(1) & list.MaxItems(1)
	}]
	_providedInterfaceIDsUnique: list.UniqueItems([for contract in providesInterfaces {contract.id}]) & true
	_requiredInterfaceIDsUnique: list.UniqueItems([for contract in requiresInterfaces {contract.id}]) & true
	_inputKindsDisjoint: [for inputRef in publicInputRefs {
		input: inputRef
		matches: [for secretRef in secretInputRefs if secretRef == inputRef {secretRef}] & list.MaxItems(0)
	}]
	_planInputKindsDisjoint: [for planInputRef in planInputRefs {
		input: planInputRef
		publicMatches: [for publicRef in publicInputRefs if publicRef == planInputRef {publicRef}] & list.MaxItems(0)
		secretMatches: [for secretRef in secretInputRefs if secretRef == planInputRef {secretRef}] & list.MaxItems(0)
	}]
	_outputsUnique:           list.UniqueItems(outputs) & true
	_compatibleTargetsUnique: list.UniqueItems(compatibleTargets) & true
	if kind == "compose" {
		compatibleTargets: ["compose"]
	}
	if kind == "opentofu" {
		compatibleTargets: ["opentofu"]
	}
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

// #ModuleRenderVariantContractV2 binds one exact target-specific realization.
// A variant may contain multiple render units, but a module may expose at most
// one variant for a given target. All referenced inputs and artifacts remain
// explicit so selection cannot be implemented as an ungoverned output filter.
#ModuleRenderVariantContractV2: {
	id:           #ContractID
	target:       #GenerationTarget
	rendererRef:  #ContractID
	contractHash: #ContentHash
	unitRefs: [...#ContractID] & list.MinItems(1)
	artifactRefs: [...#ContractID] & list.MinItems(1)
	publicInputRefs: [...#ContractID] | *[]
	secretInputRefs: [...#ContractID] | *[]
	planInputRefs: [...#ModulePlanInputRefV2] | *[]

	_unitRefsUnique:        list.UniqueItems(unitRefs) & true
	_artifactRefsUnique:    list.UniqueItems(artifactRefs) & true
	_publicInputRefsUnique: list.UniqueItems(publicInputRefs) & true
	_secretInputRefsUnique: list.UniqueItems(secretInputRefs) & true
	_planInputRefsUnique:   list.UniqueItems(planInputRefs) & true
}

// #ModuleRealizationSupportV2 separates a resolvable module contract from an
// executable implementation. Concrete modules must own artifacts and render
// units. The level is a governed attestation, not a user preference. An
// umbrella contract can describe capability ownership for
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
	// Compiler-owned plan views are governed independently from user/secret
	// inputs so no renderer can satisfy one authority class with the other.
	planInputs: {
		contractComplete: bool | *true
		requiredRefs: [...#ModulePlanInputRefV2] | *[]
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
	_planInputRefsUnique:          list.UniqueItems(planInputs.requiredRefs) & true
	_artifactRefsUnique:           list.UniqueItems(artifacts.requiredRefs) & true
	_evidenceRefsUnique:           list.UniqueItems(evidence.requiredRefs) & true

	if scope == "umbrella" {
		level: "contract-only"
	}
	if level != "contract-only" {
		scope:                  "concrete"
		compatibleRendererRefs: list.MinItems(1)
		inputs: contractComplete:     true
		planInputs: contractComplete: true
		artifacts: requiredRefs:      list.MinItems(1)
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

// #RuntimeAdapterAgentContractV1 is the closed companion seam for an
// adapter-owned node agent. It identifies the agent and its trust direction,
// but deliberately carries no endpoint, credential, provider-lifecycle, host
// lifecycle, socket, or general host authority. Concrete material remains in
// the external runtime-adapter executor's custody.
#RuntimeAdapterAgentContractV1: {
	id:          #ContractID
	adapterRef:  #ContractID
	role:        "node-agent"
	targetScope: "control-authority-site-workers"
	connection: {
		direction:         "outbound-to-control-plane"
		transport:         "tls"
		minimumTLSVersion: "TLS1.3"
		authentication:    "mutual-key"
	}
	credentialCustody: "external-owner"
	hostExecution:     "executor-mediated"
	providerLifecycle: "not-owned"
	evidenceRequired:  true
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
	role:        "foundation" | "platform" | "workload" | "operations"
	providerRef: #ContractID
	let moduleContractID = metadata.id
	let moduleProviderRef = providerRef

	// Implementation-only helper modules may intentionally own no product
	// capability. They are valid only when they provide at least one typed
	// implementation interface; capability-bearing modules keep the existing
	// non-empty projection contract.
	provides: [...#CapabilityID]
	requires?: [...#ContractID] & list.MinItems(1)
	supportedSiteKinds: [...#SiteKind] & list.MinItems(1)
	nodeSelection?:           #ModuleNodeSelectionV2
	runtimeRequirements?:     #ModuleRuntimeRequirementsV2
	enforcementRequirement?:  #PolicyEnforcementRequirementV1
	runtimeOwnerRequirement?: #RuntimeOwnerRequirementV1
	// runtimeAdapter marks a platform module as the exact implementation seam
	// for workload-scoped delivery. It grants no Kit capability and carries no
	// server-provider, lease, transport, endpoint, or credential authority.
	runtimeAdapter?: {
		id: #ContractID
		supportedKinds: [...("container" | "native" | "host" | "external" | "control-plane")] & list.MinItems(1)
		supportedDeliveries: [...("stackkit" | "selected-paas" | "external-control-plane")] & list.MinItems(1)
		operations: [...("apply" | "observe" | "rollback" | "backup" | "restore")] & list.MinItems(1)
		agentRefs: [...#ContractID] | *[]
		credentialCustody: "external-owner"
		providerLifecycle: "not-owned"
		evidenceRequired:  true

		_supportedKindsUnique:      list.UniqueItems(supportedKinds) & true
		_supportedDeliveriesUnique: list.UniqueItems(supportedDeliveries) & true
		_operationsUnique:          list.UniqueItems(operations) & true
		_agentRefsUnique:           list.UniqueItems(agentRefs) & true
	}
	runtimeAdapterAgent?: #RuntimeAdapterAgentContractV1
	runtime:              #ModuleRuntimeContractV2
	// inputDefaults is resolved once per module and then fanned out unchanged to
	// every render unit declaring the public input. Per-unit defaults are
	// intentionally forbidden so one logical input cannot resolve differently
	// across implementation units.
	inputDefaults?: #PublicSettings
	renderUnits: [...#ModuleRenderUnitContractV2] | *[]
	renderVariants: [...#ModuleRenderVariantContractV2] | *[]
	realizationSupport: #ModuleRealizationSupportV2
	health?: [...#HealthCheckContractV2]
	evidence?: [...string]
	// rilActionPrimitives is the only package-action extension seam. The
	// primitive body remains fully governed by StackKits and is bound back to
	// this exact module/provider pair. It grants no executor or runtime
	// authority by itself.
	rilActionPrimitives: [...(#RILActionPrimitiveContractV1 & {
		extensionAuthority: {
			kind:        "module"
			moduleRef:   moduleContractID
			providerRef: moduleProviderRef
		}
		target: scope: "module-instance" | "runtime-instance"
	})] | *[]

	_renderUnitIDsUnique: list.UniqueItems([for unit in renderUnits {unit.id}]) & true
	_renderVariantIDsUnique: list.UniqueItems([for variant in renderVariants {variant.id}]) & true
	_renderVariantTargetsUnique: list.UniqueItems([for variant in renderVariants {variant.target}]) & true
	_renderVariantUnitsGoverned: [for variant in renderVariants for unitRef in variant.unitRefs {
		variant: variant.id
		unit:    unitRef
		matches: [for unit in renderUnits if unit.id == unitRef && unit.rendererRef == variant.rendererRef for target in unit.compatibleTargets if target == variant.target {unit.id}] & list.MinItems(1) & list.MaxItems(1)
	}]
	_renderVariantArtifactsGoverned: [for variant in renderVariants for artifactRef in variant.artifactRefs {
		variant:  variant.id
		artifact: artifactRef
		matches: [for contract in realizationSupport.artifacts.contracts if contract.id == artifactRef for unitRef in variant.unitRefs if unitRef == contract.unitRef for target in contract.compatibleTargets if target == variant.target {contract.id}] & list.MinItems(1) & list.MaxItems(1)
	}]
	_providesUnique: list.UniqueItems(provides) & true
	if len(provides) == 0 && role != "workload" {
		realizationSupport: {
			scope: "concrete"
			level: "contract-only" | "generation-ready" | "apply-ready"
		}
		_typedImplementationContract: [
			for unit in renderUnits
			for contract in unit.providesInterfaces {"\(unit.id)/\(contract.id)"},
			if runtimeAdapter != _|_ {"runtime-adapter/\(runtimeAdapter.id)"},
			if runtimeAdapterAgent != _|_ {"runtime-adapter-agent/\(runtimeAdapterAgent.id)"},
		] & list.MinItems(1)
	}
	if runtimeAdapter != _|_ {
		role: "platform"
	}
	if runtimeAdapterAgent != _|_ {
		role: "platform"
	}
	if role == "workload" {
		provides: []
	}
	if requires != _|_ {
		_requiresUnique: list.UniqueItems(requires) & true
	}
	if enforcementRequirement != _|_ {
		if enforcementRequirement.status == "unbound" {
			runtime: execution: "contract-handoff"
		}
		if enforcementRequirement.status == "bound" {
			runtime: execution: "executable"
		}
		_enforcementArtifactsOwned: [for artifactRef in enforcementRequirement.policyArtifactRefs {
			artifact: artifactRef
			matches: [for requiredRef in realizationSupport.artifacts.requiredRefs if requiredRef == artifactRef {requiredRef}] & list.MinItems(1) & list.MaxItems(1)
		}]
		if enforcementRequirement.status == "bound" {
			realizationSupport: level: "apply-ready"
			_enforcementHealthOwned: [for healthContract in health if healthContract.id == enforcementRequirement.requiredHealthRef && healthContract.scope == "each-node" {healthContract.id}] & list.MinItems(1) & list.MaxItems(1)
			_enforcementEvidenceOwned: [for evidenceRef in realizationSupport.evidence.requiredRefs if evidenceRef == enforcementRequirement.requiredEvidenceRef {evidenceRef}] & list.MinItems(1) & list.MaxItems(1)
			_enforcementUnitPlacement: [for unit in renderUnits if unit.placement.scope == "node-local" && unit.placement.cardinality == "one-per-node" {unit.id}] & list.MinItems(1)
		}
	}
	if runtimeOwnerRequirement != _|_ {
		runtime: execution: "contract-handoff"
		_runtimeOwnerCapabilitiesOwned: [for capabilityRef in runtimeOwnerRequirement.capabilityRefs {
			capability: capabilityRef
			matches: [for providedRef in provides if providedRef == capabilityRef {providedRef}] & list.MinItems(1) & list.MaxItems(1)
		}]
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
	_renderUnitPlanInputKindsDisjoint: [for unit in renderUnits for planInputRef in unit.planInputRefs {
		unit:  unit.id
		input: planInputRef
		publicMatches: [for candidate in renderUnits for publicRef in candidate.publicInputRefs if publicRef == planInputRef {candidate.id}] & list.MaxItems(0)
		secretMatches: [for candidate in renderUnits for secretRef in candidate.secretInputRefs if secretRef == planInputRef {candidate.id}] & list.MaxItems(0)
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
	_inputDefaultsDoNotOverrideBindings: [if inputDefaults != _|_ for key, _ in inputDefaults {
		input: key
		matches: [
			for unit in renderUnits
			for binding in unit.inputBindings
			if binding.targetRef == key {unit.id},
		] & list.MaxItems(0)
	}]
	_secretInputBindingCapabilitiesProvided: [for unit in renderUnits for inputRef, binding in unit.secretInputBindings {
		unit:       unit.id
		input:      inputRef
		capability: binding.capabilityRef
		matches: [for capabilityRef in provides if capabilityRef == binding.capabilityRef {capabilityRef}] & list.MinItems(1) & list.MaxItems(1)
	}]
	_requiredInputBindingsGoverned: [for unit in renderUnits for binding in unit.inputBindings if binding.required {
		unit:  unit.id
		input: binding.targetRef
		matches: [for requiredRef in realizationSupport.inputs.requiredRefs if requiredRef == binding.targetRef {requiredRef}] & list.MinItems(1) & list.MaxItems(1)
	}]
	_optionalInputBindingsNotRequired: [for unit in renderUnits for binding in unit.inputBindings if binding.required == false {
		unit:  unit.id
		input: binding.targetRef
		matches: [for requiredRef in realizationSupport.inputs.requiredRefs if requiredRef == binding.targetRef {requiredRef}] & list.MaxItems(0)
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
	_renderUnitRequiredPlanInputsDeclared: [for requiredRef in realizationSupport.planInputs.requiredRefs {
		input: requiredRef
		matches: [
			for unit in renderUnits
			for inputRef in unit.planInputRefs
			if inputRef == requiredRef {unit.id},
		] & list.MinItems(1)
	}]
	_renderUnitPlanInputsRequired: [for unit in renderUnits for planInputRef in unit.planInputRefs {
		unit:  unit.id
		input: planInputRef
		matches: [for requiredRef in realizationSupport.planInputs.requiredRefs if requiredRef == planInputRef {requiredRef}] & list.MinItems(1) & list.MaxItems(1)
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
	if runtime.execution == "contract-handoff" {
		realizationSupport: level: "contract-only" | "generation-ready"
		_handoffSecretInputsForbidden: [for unit in renderUnits {
			unit:         unit.id
			secretInputs: unit.secretInputRefs & list.MaxItems(0)
		}]
	}
}

#HealthCheckContractV2: {
	id:    #ContractID
	phase: *"post-apply" | "pre-apply" | "continuous"
	kind:  "contract" | "http" | "tcp" | "container" | "process"
	// each-node materializes one independently receipted gate per selected
	// node. Omitted scope keeps the aggregate contract.
	scope?:         "each-node"
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
	workloads: [...#WorkloadContractV2] | *[]
	privilegedInterfaceApprovals: [...#PrivilegedInterfaceApprovalV2] | *[]
	rilActionExecutors: [...#RILActionExecutorContractV1] | *[]
	rilActionPrimitives: [...#RILActionPrimitiveContractV1] | *[]
	_rilExecutorClosure: [for primitive in rilActionPrimitives if primitive.support == "executor-bound" {
		primitive: primitive.id
		executor:  primitive.executorRef
		matches: [for executor in rilActionExecutors if executor.ref == primitive.executorRef for operationClass in executor.operationClasses if operationClass == primitive.owner.operationClass {executor.ref}] & list.MinItems(1) & list.MaxItems(1)
	}]
	_rilRecoveryClosure: [for primitive in rilActionPrimitives if primitive.recovery.kind == "primitive" {
		primitive: primitive.id
		recovery:  primitive.recovery.primitiveRef
		matches: [for target in rilActionPrimitives if target.id == primitive.recovery.primitiveRef && target.mutation {target.id}] & list.MinItems(1) & list.MaxItems(1)
	}]
	_rilModuleExtensionClosure: [
		for module in modules
		for primitive in module.rilActionPrimitives {
			primitive: primitive.id
			module:    module.metadata.id
			provider:  module.providerRef
			authority: primitive.extensionAuthority & {
				kind:        "module"
				moduleRef:   module.metadata.id
				providerRef: module.providerRef
			}
			matches: [
				for catalogPrimitive in rilActionPrimitives
				if catalogPrimitive.id == primitive.id &&
					catalogPrimitive.extensionAuthority.kind == "module" &&
					catalogPrimitive.extensionAuthority.moduleRef == module.metadata.id &&
					catalogPrimitive.extensionAuthority.providerRef == module.providerRef {catalogPrimitive.id},
			] & list.MinItems(1) & list.MaxItems(1)
		},
	]
	_tlsIssuerIDsUnique: list.UniqueItems([for provider in providers for issuer in provider.certificateIssuers {issuer.id}]) & true
	_tlsProfileIDsUnique: list.UniqueItems([for capability in capabilities if capability.tlsProfile != _|_ {capability.tlsProfile.id}]) & true
	_tlsCapabilityProfilesExact: [for capabilityContract in capabilities if capabilityContract.metadata.id == "internal-pki" || capabilityContract.metadata.id == "public-tls" {
		capability: capabilityContract.metadata.id
		profile: capabilityContract.tlsProfile & {capabilityRef: capabilityContract.metadata.id}
	}]
	_tlsIssuerOwnersExact: [for providerContract in providers for issuerContract in providerContract.certificateIssuers {
		issuer:                 issuerContract.id
		materializationSupport: issuerContract.owner.materializationSupport & "contract-only"
		moduleMatches: [for module in modules if module.metadata.id == issuerContract.owner.moduleRef && module.providerRef == providerContract.metadata.id for healthContract in module.health if healthContract.id == issuerContract.renewal.healthGateRef {module.metadata.id}] & list.MinItems(1) & list.MaxItems(1)
		providerSiteKinds: [for issuerSiteKind in issuerContract.supportedSiteKinds {
			kind: issuerSiteKind
			matches: [for providerSiteKind in providerContract.supportedSiteKinds if providerSiteKind == issuerSiteKind {providerSiteKind}] & list.MinItems(1) & list.MaxItems(1)
		}]
		capabilitySiteKinds: [for capabilityContract in capabilities if capabilityContract.metadata.id == issuerContract.capabilityRef for capabilitySiteKind in capabilityContract.supportedSiteKinds {
			kind: capabilitySiteKind
			matches: [for issuerSiteKind in issuerContract.supportedSiteKinds if issuerSiteKind == capabilitySiteKind {issuerSiteKind}] & list.MinItems(1) & list.MaxItems(1)
		}]
	}]
	integrity: tls: {
		issuerIDsUnique:         _tlsIssuerIDsUnique
		profileIDsUnique:        _tlsProfileIDsUnique
		capabilityProfilesExact: _tlsCapabilityProfilesExact
		issuerOwnersExact:       _tlsIssuerOwnersExact
		providerBindings: [for providerContract in providers {
			provider:           providerContract.metadata.id
			issuerIDsUnique:    providerContract._tlsIssuerIDsUnique
			issuerBindings:     providerContract._tlsIssuerBindings
			capabilityBindings: providerContract._tlsCapabilityBindings
		}]
	}
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
		workloadIDsUnique: list.UniqueItems([for contract in workloads {contract.metadata.id}]) & true
		workloadAlternativesUnique: list.UniqueItems([
			for workload in workloads
			for alternative in workload.alternatives {"\(workload.metadata.id)/\(alternative.id)"},
		]) & true
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
		rilActionExecutorRefsUnique: list.UniqueItems([for executor in rilActionExecutors {executor.ref}]) & true
		rilActionPrimitiveIDsUnique: list.UniqueItems([for primitive in rilActionPrimitives {primitive.id}]) & true
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
	integrity: ownership: {
		providerContractsNonEmpty: [for provider in providers {
			providerID: provider.metadata.id
			contracts: list.Concat([provider.provides, provider.workloadRefs, provider.runtimeAdapterRefs]) & list.MinItems(1)
		}]
	}
	_runtimeAdapterIDsUnique: list.UniqueItems([for provider in providers for adapterRef in provider.runtimeAdapterRefs {adapterRef}]) & true
	_runtimeAdapterAgentIDsUnique: list.UniqueItems([for module in modules if module.runtimeAdapterAgent != _|_ {module.runtimeAdapterAgent.id}]) & true
	_runtimeAdapterOwnership: [
		for provider in providers
		for adapterRef in provider.runtimeAdapterRefs {
			providerID: provider.metadata.id
			adapterID:  adapterRef
			moduleMatches: [for module in modules if module.providerRef == provider.metadata.id && module.runtimeAdapter != _|_ && module.runtimeAdapter.id == adapterRef for moduleRef in provider.realization._allModuleRefs if moduleRef == module.metadata.id {module.metadata.id}] & list.MinItems(1) & list.MaxItems(1)
		},
	]
	_runtimeAdapterAgentBindings: [
		for module in modules
		if module.runtimeAdapter != _|_
		for agentRef in module.runtimeAdapter.agentRefs {
			providerID: module.providerRef
			adapterID:  module.runtimeAdapter.id
			agentID:    agentRef
			matches: [
				for agentModule in modules
				if agentModule.providerRef == module.providerRef && agentModule.runtimeAdapterAgent != _|_ && agentModule.runtimeAdapterAgent.id == agentRef && agentModule.runtimeAdapterAgent.adapterRef == module.runtimeAdapter.id
				for provider in providers
				if provider.metadata.id == module.providerRef
				for moduleRef in provider.realization._allModuleRefs
				if moduleRef == agentModule.metadata.id {agentModule.metadata.id},
			] & list.MinItems(1) & list.MaxItems(1)
		},
	]
	_workloadRuntimeAdapterBindings: [
		for workload in workloads
		for alternative in workload.alternatives
		for adapterRef in alternative.runtime.allowedAdapterRefs {
			workloadID:    workload.metadata.id
			alternativeID: alternative.id
			adapterID:     adapterRef
			providerMatches: [for provider in providers if list.Contains(provider.runtimeAdapterRefs, adapterRef) {
				providerID: provider.metadata.id
				moduleMatches: [for module in modules if module.providerRef == provider.metadata.id && module.runtimeAdapter != _|_ && module.runtimeAdapter.id == adapterRef for moduleRef in provider.realization._allModuleRefs if moduleRef == module.metadata.id {module.metadata.id}] & list.MinItems(1) & list.MaxItems(1)
			}] & list.MinItems(1) & list.MaxItems(1)
		},
	]
	_workloadAlternativeBindings: [
		for workload in workloads
		for alternative in workload.alternatives {
			workloadID:    workload.metadata.id
			alternativeID: alternative.id
			providerMatches: [for provider in providers if provider.metadata.id == alternative.providerRef {
				providerID: provider.metadata.id
				workloadOwnership: [for workloadRef in provider.workloadRefs if workloadRef == workload.metadata.id {workloadRef}] & list.MinItems(1) & list.MaxItems(1)
				capabilityAliasForbidden: [for capabilityRef in provider.provides if capabilityRef == workload.metadata.id {capabilityRef}] & list.MaxItems(0)
			}] & list.MinItems(1) & list.MaxItems(1)
			moduleMatches: [for module in modules if module.metadata.id == alternative.moduleRef && module.providerRef == alternative.providerRef && module.role == "workload" {
				moduleID: module.metadata.id
				capabilityProvides: module.provides & []
				providerSiteKinds: [for provider in providers if provider.metadata.id == alternative.providerRef {
					workloadCoverage: [for siteKind in workload.supportedSiteKinds {
						kind: siteKind
						matches: [for providerSiteKind in provider.supportedSiteKinds if providerSiteKind == siteKind {providerSiteKind}] & list.MinItems(1) & list.MaxItems(1)
					}]
				}]
				moduleSiteKinds: [for siteKind in workload.supportedSiteKinds {
					kind: siteKind
					matches: [for moduleSiteKind in module.supportedSiteKinds if moduleSiteKind == siteKind {moduleSiteKind}] & list.MinItems(1) & list.MaxItems(1)
				}]
				runtimeKind: [for allowed in alternative.runtime.allowedKinds if module.runtime.kind == allowed {allowed}] & list.MinItems(1) & list.MaxItems(1)
				runtimeDelivery: [for allowed in alternative.runtime.allowedDeliveries if module.runtime.delivery == allowed {allowed}] & list.MinItems(1) & list.MaxItems(1)
				serviceMatches: [for unit in module.renderUnits for endpoint in unit.serviceEndpoints if endpoint.serviceRef == alternative.route.serviceRef && endpoint.healthRef == alternative.route.healthRef {
					unitID: unit.id
					if len(workload.dataClasses) > 0 {
						dataContract: endpoint.data
						workloadDataClasses: [for dataClass in workload.dataClasses {
							class: dataClass
							matches: [for requiredClass in endpoint.data.requiredClasses if requiredClass == dataClass {requiredClass}] & list.MinItems(1) & list.MaxItems(1)
						}]
					}
					if endpoint.data != _|_ {
						endpointDataClasses: [for requiredClass in endpoint.data.requiredClasses {
							class: requiredClass
							matches: [for dataClass in workload.dataClasses if dataClass == requiredClass {dataClass}] & list.MinItems(1) & list.MaxItems(1)
						}]
					}
				}] & list.MinItems(1) & list.MaxItems(1)
				settingsRefs: [for inputRef in alternative.inputs.settings.allowedRefs {
					ref: inputRef
					matches: [for unit in module.renderUnits for publicInputRef in unit.publicInputRefs if publicInputRef == inputRef {unit.id}] & list.MinItems(1) & list.MaxItems(1)
				}]
				secretRefs: [for inputRef in alternative.inputs.secretInputs.allowedRefs {
					ref: inputRef
					matches: [for unit in module.renderUnits for secretInputRef in unit.secretInputRefs if secretInputRef == inputRef {unit.id}] & list.MinItems(1) & list.MaxItems(1)
				}]
			}] & list.MinItems(1) & list.MaxItems(1)
		},
	]
	integrity: workloads: {
		alternativeBindings: _workloadAlternativeBindings
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

// #ExternalHostBindingV1 is issued by the platform that already owns the
// target host. StackKits can install through the opaque execution channel, but
// cannot select, provision, resize, snapshot, delete, or otherwise manage the
// server provider behind it.
#ExternalHostBindingV1: {
	apiVersion:          "stackkit.external-host-binding/v1"
	kind:                "ExternalHostBinding"
	bindingRef:          #ExternalHostBindingRef
	stackId:             #ContractID
	nodeRef:             #NodeID
	hostRef:             #ExternalHostRef
	inventoryRef:        #ExternalInventoryRef
	executionChannelRef: #ExecutionChannelRef
	secretRefs: ({
		[#ContractID]: #SecretReference
	} | *{})
	stackkitsVersion:     #SemanticVersion
	candidateDigest:      #ContentHash
	specHash:             #ContentHash
	hostRequirementsHash: #ContentHash
	inventoryHash:        #ContentHash
	issuedAt:             #RFC3339Timestamp
	validUntil:           #RFC3339Timestamp
	bindingHash:          #ContentHash
}

// #HomeAccessRequirementV1 is StackKits-owned, provider-neutral intent for one
// Home Site and one explicitly selected access capability. It is emitted by a
// Shadow Plan so an external execution platform can prepare a matching access
// fabric without receiving LAN routes, addresses, endpoints, credentials, or
// provider lifecycle authority.
#HomeAccessRequirementV1: {
	apiVersion:             "stackkit.home-access-requirement/v1"
	kind:                   "HomeAccessRequirement"
	stackId:                #ContractID
	siteRef:                #SiteID
	capabilityRef:          "private-remote-access" | "public-publish-egress"
	contractOwnerRef:       #ContractID
	capabilityContractHash: #ContentHash
	targetNodeRefs: [...#NodeID] & list.MinItems(1)
	policy: {
		defaultDeny:       true
		initiation:        "home-outbound"
		routeScope:        "declared-services-only"
		allowDefaultRoute: false
		allowBroadLAN:     false
		identityMode:      "device-bound" | "service-identity"
		credentialCustody: "external"
		fabricLifecycle:   "external"
	}
	specHash:              #ContentHash
	requirementsHash:      #ContentHash
	_targetNodeRefsUnique: list.UniqueItems(targetNodeRefs) & true
	if capabilityRef == "private-remote-access" {
		policy: identityMode: "device-bound"
	}
	if capabilityRef == "public-publish-egress" {
		policy: identityMode: "service-identity"
	}
}

// #ExternalHomeAccessBindingV1 is issued only after the external platform has
// consumed the exact Shadow-Plan requirement. The fabric remains opaque: the
// contract cannot carry transport selection, endpoint/address data, secrets,
// provider resources, accounts, regions, or lifecycle handles.
#ExternalHomeAccessBindingV1: {
	apiVersion:             "stackkit.external-home-access-binding/v1"
	kind:                   "ExternalHomeAccessBinding"
	bindingRef:             #ExternalHomeAccessBindingRef
	stackId:                #ContractID
	siteRef:                #SiteID
	capabilityRef:          "private-remote-access" | "public-publish-egress"
	contractOwnerRef:       #ContractID
	capabilityContractHash: #ContentHash
	requirementsHash:       #ContentHash
	accessFabricRef:        #HomeAccessFabricRef
	stackkitsVersion:       #SemanticVersion
	candidateDigest:        #ContentHash
	specHash:               #ContentHash
	issuedAt:               #RFC3339Timestamp
	validUntil:             #RFC3339Timestamp
	bindingHash:            #ContentHash
}

// #BackupTargetRequirementV1 is the StackKits-owned, provider-neutral
// requirement for one Cloud offsite backup target. It identifies only the
// governed Site/node/capability scope and custody properties. Provider,
// account, region, bucket, endpoint, credential, lease, and lifecycle details
// are deliberately outside this contract.
#BackupTargetRequirementV1: {
	apiVersion:             "stackkit.backup-target-requirement/v1"
	kind:                   "BackupTargetRequirement"
	stackId:                #ContractID
	siteRef:                #SiteID
	capabilityRef:          "offsite-object-backup"
	contractOwnerRef:       #ContractID
	capabilityContractHash: #ContentHash
	targetNodeRefs: [...#NodeID] & list.MinItems(1)
	policy: {
		scope:                       "governed-data-only"
		encryptionRequired:          true
		credentialCustody:           "external"
		targetLifecycle:             "external"
		restoreVerificationRequired: true
		providerSelection:           "external"
	}
	specHash:         #ContentHash
	requirementsHash: #ContentHash

	_targetNodeRefsUnique: list.UniqueItems(targetNodeRefs) & true
}

// #ExternalBackupTargetBindingV1 attests that an external authority has
// prepared one logical target for the exact requirement. References remain
// opaque, so this receipt cannot smuggle provider or secret material into a
// StackKit plan.
#ExternalBackupTargetBindingV1: {
	apiVersion:             "stackkit.external-backup-target-binding/v1"
	kind:                   "ExternalBackupTargetBinding"
	bindingRef:             #ExternalBackupTargetBindingRef
	backupTargetRef:        #ExternalBackupTargetRef
	custodyAttestationRef:  #BackupCustodyAttestationRef
	stackId:                #ContractID
	siteRef:                #SiteID
	capabilityRef:          "offsite-object-backup"
	contractOwnerRef:       #ContractID
	capabilityContractHash: #ContentHash
	requirementsHash:       #ContentHash
	stackkitsVersion:       #SemanticVersion
	candidateDigest:        #ContentHash
	specHash:               #ContentHash
	issuedAt:               #RFC3339Timestamp
	validUntil:             #RFC3339Timestamp
	bindingHash:            #ContentHash
}

// #HomeBackupTargetRequirementV1 is the provider-free Home offsite backup
// handoff. Home owns encryption before egress; the external authority owns
// target custody and lifecycle without exposing infrastructure details.
#HomeBackupTargetRequirementV1: {
	apiVersion:             "stackkit.home-backup-target-requirement/v1"
	kind:                   "HomeBackupTargetRequirement"
	stackId:                #ContractID
	siteRef:                #SiteID
	capabilityRef:          "encrypted-offsite-backup"
	contractOwnerRef:       #ContractID
	capabilityContractHash: #ContentHash
	targetNodeRefs: [...#NodeID] & list.MinItems(1)
	policy: {
		scope:                       "governed-home-data-only"
		encryptionRequired:          true
		encryptionAuthority:         "home"
		plaintextEgressAllowed:      false
		credentialCustody:           "external"
		targetLifecycle:             "external"
		restoreVerificationRequired: true
		providerSelection:           "external"
	}
	specHash:         #ContentHash
	requirementsHash: #ContentHash

	_targetNodeRefsUnique: list.UniqueItems(targetNodeRefs) & true
}

#ExternalHomeBackupTargetBindingV1: {
	apiVersion:             "stackkit.external-home-backup-target-binding/v1"
	kind:                   "ExternalHomeBackupTargetBinding"
	bindingRef:             #ExternalHomeBackupTargetBindingRef
	backupTargetRef:        #ExternalHomeBackupTargetRef
	custodyAttestationRef:  #HomeBackupCustodyAttestationRef
	stackId:                #ContractID
	siteRef:                #SiteID
	capabilityRef:          "encrypted-offsite-backup"
	contractOwnerRef:       #ContractID
	capabilityContractHash: #ContentHash
	requirementsHash:       #ContentHash
	stackkitsVersion:       #SemanticVersion
	candidateDigest:        #ContentHash
	specHash:               #ContentHash
	issuedAt:               #RFC3339Timestamp
	validUntil:             #RFC3339Timestamp
	bindingHash:            #ContentHash
}

// #FederationLinkRequirementV1 is the complete provider-free handoff for the
// exact Modern Homelab Home<->Cloud link scope. The external fabric authority
// receives identities and hashes, never transport, routes, addresses,
// endpoints, credentials, relay handles, or infrastructure lifecycle fields.
#FederationLinkRequirementV1: {
	apiVersion:             "stackkit.federation-link-requirement/v1"
	kind:                   "FederationLinkRequirement"
	stackId:                #ContractID
	capabilityRef:          "inter-site-link"
	contractOwnerRef:       #ContractID
	capabilityContractHash: #ContentHash
	homeSiteRefs: [...#SiteID] & list.MinItems(1)
	cloudSiteRefs: [...#SiteID] & list.MinItems(1)
	targetNodes: [...{
		siteRef: #SiteID
		nodeRef: #NodeID
	}] & list.MinItems(1)
	bridgeContractHash: #ContentHash
	policy: {
		defaultDeny:       true
		initiation:        "home-outbound"
		trafficMode:       "management-only" | "policy-scoped"
		routeScope:        "declared-flows-only"
		allowDefaultRoute: false
		allowBroadLAN:     false
		credentialCustody: "external"
		fabricLifecycle:   "external"
	}
	specHash:         #ContentHash
	requirementsHash: #ContentHash

	_homeSiteRefsUnique:  list.UniqueItems(homeSiteRefs) & true
	_cloudSiteRefsUnique: list.UniqueItems(cloudSiteRefs) & true
	_targetNodesUnique:   list.UniqueItems(targetNodes) & true
}

// #ExternalFederationLinkBindingV1 is a short-lived opaque attestation from
// the external fabric authority for exactly one compiler-owned requirement.
#ExternalFederationLinkBindingV1: {
	apiVersion:             "stackkit.external-federation-link-binding/v1"
	kind:                   "ExternalFederationLinkBinding"
	bindingRef:             #ExternalFederationLinkBindingRef
	fabricRef:              #FederationLinkFabricRef
	custodyAttestationRef:  #FederationLinkCustodyRef
	stackId:                #ContractID
	capabilityRef:          "inter-site-link"
	contractOwnerRef:       #ContractID
	capabilityContractHash: #ContentHash
	homeSiteRefs: [...#SiteID] & list.MinItems(1)
	cloudSiteRefs: [...#SiteID] & list.MinItems(1)
	targetNodes: [...{
		siteRef: #SiteID
		nodeRef: #NodeID
	}] & list.MinItems(1)
	bridgeContractHash: #ContentHash
	requirementsHash:   #ContentHash
	stackkitsVersion:   #SemanticVersion
	candidateDigest:    #ContentHash
	specHash:           #ContentHash
	issuedAt:           #RFC3339Timestamp
	validUntil:         #RFC3339Timestamp
	bindingHash:        #ContentHash
}

// Host facts are conformance diagnostics. Only the OS tuple participates in
// the StackKits support/compatibility statement; kernel, runtime, and
// virtualization observations explain why a particular host is or is not
// currently usable and never name a server provider.
#HostConformanceFactsV1: {
	os: {
		family:       "linux"
		distribution: #ContractID
		version:      string & =~"^[^[:space:]]+$"
	}
	architecture: "amd64" | "arm64"
	kernel: {
		release: string & =~"^[^[:space:]]+$"
	}
	runtime: {
		engine:  "docker" | "podman" | "containerd" | "none"
		version: string & =~"^[^[:space:]]+$"
	}
	virtualization: {
		class:  #RuntimeVirtualizationV2
		nested: bool
	}
}

#HostConformanceCheckV1: {
	id:       #ContractID
	category: "os-compatibility" | "host-diagnostic"
	status:   "pass" | "warning" | "fail" | "unverified"
	summary:  string & =~"^.+$"
}

// #HostConformanceReceiptV1 is StackKits-owned evidence about one bound host.
// It is not provider compatibility and contains no lease/resource lifecycle.
#HostConformanceReceiptV1: {
	apiVersion:       "stackkit.host-conformance-receipt/v1"
	kind:             "HostConformanceReceipt"
	receiptRef:       #HostConformanceRef
	bindingRef:       #ExternalHostBindingRef
	bindingHash:      #ContentHash
	stackId:          #ContractID
	nodeRef:          #NodeID
	stackkitsVersion: #SemanticVersion
	candidateDigest:  #ContentHash
	observedAt:       #RFC3339Timestamp
	validUntil:       #RFC3339Timestamp
	facts:            #HostConformanceFactsV1
	checks: [...#HostConformanceCheckV1] & list.MinItems(1)
	result:        "conformant" | "degraded" | "incompatible" | "unverified"
	receiptDigest: #ContentHash

	_checkIDsUnique: list.UniqueItems([for check in checks {check.id}]) & true
	_osChecks: [for check in checks if check.category == "os-compatibility" {check}] & list.MinItems(1)
	_nonPassingChecks: [for check in checks if check.status != "pass" {check.id}]
	_failedOSChecks: [for check in checks if check.category == "os-compatibility" && check.status == "fail" {check.id}]
	_unverifiedChecks: [for check in checks if check.status == "unverified" {check.id}]
	if result == "conformant" {
		_nonPassingChecks: list.MaxItems(0)
	}
	if result == "degraded" {
		_failedOSChecks:   list.MaxItems(0)
		_unverifiedChecks: list.MaxItems(0)
		_nonPassingChecks: list.MinItems(1)
	}
	if result == "incompatible" {
		_failedOSChecks: list.MinItems(1)
	}
	if result == "unverified" {
		_failedOSChecks:   list.MaxItems(0)
		_unverifiedChecks: list.MinItems(1)
	}
}

// Inventory is a separate compiler input. Facts validate an intent; they never
// select or mutate the kit. Host binding and conformance are optional during
// fast shadow planning; their presence is nevertheless strict and hash-bound.
#InventoryFacts: {
	schemaVersion: "stackkit.inventory/v1"
	externalHomeAccessBindings: [#SiteID]: [#CapabilityID]: #ExternalHomeAccessBindingV1 | *{}
	externalBackupTargetBindings: [#SiteID]: [#CapabilityID]: #ExternalBackupTargetBindingV1 | *{}
	externalHomeBackupTargetBindings: [#SiteID]: [#CapabilityID]: #ExternalHomeBackupTargetBindingV1 | *{}
	externalFederationLinkBindings: [#CapabilityID]: #ExternalFederationLinkBindingV1 | *{}
	_externalHomeAccessBindingKeys: [for siteRef, capabilityBindings in externalHomeAccessBindings for capabilityRef, binding in capabilityBindings {
		site:       siteRef & binding.siteRef
		capability: capabilityRef & binding.capabilityRef
	}]
	_externalBackupTargetBindingKeys: [for siteRef, capabilityBindings in externalBackupTargetBindings for capabilityRef, binding in capabilityBindings {
		site:       siteRef & binding.siteRef
		capability: capabilityRef & binding.capabilityRef
	}]
	_externalHomeBackupTargetBindingKeys: [for siteRef, capabilityBindings in externalHomeBackupTargetBindings for capabilityRef, binding in capabilityBindings {
		site:       siteRef & binding.siteRef
		capability: capabilityRef & binding.capabilityRef
	}]
	_externalFederationLinkBindingKeys: [for capabilityRef, binding in externalFederationLinkBindings {
		capability: capabilityRef & binding.capabilityRef
	}]
	nodes: [#NodeID]: {
		observedSiteKind?:       #SiteKind
		arch?:                   "amd64" | "arm64"
		cpuCores?:               int & >=1
		ramGB?:                  int & >=1
		storageGB?:              int & >=1
		virtualization?:         #RuntimeVirtualizationV2
		externalHostBinding?:    #ExternalHostBindingV1
		hostConformanceReceipt?: #HostConformanceReceiptV1
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
	tlsProfile?:  #TLSProfileV2
	settings?:    #PublicSettings & struct.MinFields(1)
	secretRefs?: {
		[string]: #SecretReference
	} & struct.MinFields(1)
}

#ResolvedProvider: {
	id:           #ContractID
	version:      string
	contractHash: #ContentHash
	provides: [...#CapabilityID]
	workloadRefs: [...#WorkloadID] | *[]
	runtimeAdapterRefs: [...#ContractID] | *[]
	siteRefs: [...#SiteID] & list.MinItems(1)
	realization: #ProviderRealizationContractV2
	owner?:      #ResolvedProviderOwnerV2
	certificateIssuers: [...#CertificateIssuerV2] | *[]
	overlayContracts: [...#OverlayProviderContractV2] | *[]
	remoteActionContracts: [...#RemoteActionContractV2] | *[]
	_providesUnique:           list.UniqueItems(provides) & true
	_workloadRefsUnique:       list.UniqueItems(workloadRefs) & true
	_runtimeAdapterRefsUnique: list.UniqueItems(runtimeAdapterRefs) & true

	if realization.kind == "host" || realization.kind == "external" {
		owner: {
			ref:                realization.ownerRef
			kind:               realization.kind
			version:            version
			contractHash:       contractHash
			realizationSupport: realization.realizationSupport
		}
	}
	if realization.kind == "none" || realization.kind == "contract" || realization.kind == "modules" || realization.kind == "topology" {
		owner?: _|_
	}
}

#ResolvedProviderOwnerV2: {
	ref:                #ContractID
	kind:               "host" | "external"
	version:            string & =~"^.+$"
	contractHash:       #ContentHash
	realizationSupport: #ProviderOwnerRealizationSupportV2
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
	id:          #ContractID
	kind:        #ModuleRenderUnitKindV2
	rendererRef: #ContractID
	compatibleTargets: [...#GenerationTarget] & list.MinItems(1)
	templateRef:  string & =~"^[^[:space:]]+$"
	version:      #SemanticVersion
	contractHash: #ContentHash

	publicInputRefs: [...#ContractID] | *[]
	secretInputRefs: [...#ContractID] | *[]
	planInputRefs: [...#ModulePlanInputRefV2] | *[]
	inputBindings: [...#ModuleRenderInputBindingV2] | *[]
	secretInputBindings: [#ContractID]: #ModuleSecretInputBindingV2 | *{}
	values: #PublicSettings
	secretRefs: [string]: #SecretReference
	planInputs: #ModulePlanInputsV2
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
	_planInputRefsUnique:   list.UniqueItems(planInputRefs) & true
	_inputBindingTargetsUnique: list.UniqueItems([for binding in inputBindings {binding.targetRef}]) & true
	_inputBindingTargetsDeclared: [for binding in inputBindings {
		target: binding.targetRef
		matches: [for publicRef in publicInputRefs if publicRef == binding.targetRef {publicRef}] & list.MinItems(1) & list.MaxItems(1)
	}]
	_secretInputBindingTargetsDeclared: [for targetRef, _ in secretInputBindings {
		target: targetRef
		matches: [for secretRef in secretInputRefs if secretRef == targetRef {secretRef}] & list.MinItems(1) & list.MaxItems(1)
	}]
	_siteRefsUnique: list.UniqueItems(siteRefs) & true
	_nodeRefsUnique: list.UniqueItems(nodeRefs) & true
	_providedInterfaceIDsUnique: list.UniqueItems([for contract in providesInterfaces {contract.id}]) & true
	_requiredInterfaceIDsUnique: list.UniqueItems([for contract in requiresInterfaces {contract.id}]) & true
	_inputKindsDisjoint: [for inputRef in publicInputRefs {
		input: inputRef
		matches: [for secretRef in secretInputRefs if secretRef == inputRef {secretRef}] & list.MaxItems(0)
	}]
	_planInputKindsDisjoint: [for planInputRef in planInputRefs {
		input: planInputRef
		publicMatches: [for publicRef in publicInputRefs if publicRef == planInputRef {publicRef}] & list.MaxItems(0)
		secretMatches: [for secretRef in secretInputRefs if secretRef == planInputRef {secretRef}] & list.MaxItems(0)
	}]
	_valuesDeclared: [for key, _ in values {
		input: key
		matches: [for inputRef in publicInputRefs if inputRef == key {inputRef}] & list.MinItems(1) & list.MaxItems(1)
	}]
	_secretRefsDeclared: [for key, _ in secretRefs {
		input: key
		matches: [for inputRef in secretInputRefs if inputRef == key {inputRef}] & list.MinItems(1) & list.MaxItems(1)
	}]
	_planInputsDeclared: [for key, _ in planInputs {
		input: key
		matches: [for inputRef in planInputRefs if inputRef == key {inputRef}] & list.MinItems(1) & list.MaxItems(1)
	}]
	_planInputsComplete: [for inputRef in planInputRefs {
		input: inputRef
		matches: [for key, _ in planInputs if key == inputRef {key}] & list.MinItems(1) & list.MaxItems(1)
	}]
	_outputsUnique:           list.UniqueItems(outputs) & true
	_compatibleTargetsUnique: list.UniqueItems(compatibleTargets) & true
	if kind == "compose" {
		compatibleTargets: ["compose"]
	}
	if kind == "opentofu" {
		compatibleTargets: ["opentofu"]
	}
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

#ResolvedModuleRenderVariantV2: {
	id:           #ContractID
	target:       #GenerationTarget
	rendererRef:  #ContractID
	contractHash: #ContentHash
	unitRefs: [...#ContractID] & list.MinItems(1)
	artifactRefs: [...#ContractID] & list.MinItems(1)
	publicInputRefs: [...#ContractID] | *[]
	secretInputRefs: [...#ContractID] | *[]
	planInputRefs: [...#ModulePlanInputRefV2] | *[]

	_unitRefsUnique:        list.UniqueItems(unitRefs) & true
	_artifactRefsUnique:    list.UniqueItems(artifactRefs) & true
	_publicInputRefsUnique: list.UniqueItems(publicInputRefs) & true
	_secretInputRefsUnique: list.UniqueItems(secretInputRefs) & true
	_planInputRefsUnique:   list.UniqueItems(planInputRefs) & true
}

#ResolvedModuleV2: {
	id:           #ContractID
	version:      #SemanticVersion
	contractHash: #ContentHash
	role:         "foundation" | "platform" | "workload" | "operations"
	providerRef:  #ContractID
	provides: [...#CapabilityID]
	siteRefs: [...#SiteID] & list.MinItems(1)
	nodeRefs: [...#NodeID] & list.MinItems(1)
	requires?: [...#ContractID] & list.MinItems(1)
	nodeSelection?:           #ModuleNodeSelectionV2
	runtimeRequirements?:     #ModuleRuntimeRequirementsV2
	enforcementRequirement?:  #PolicyEnforcementRequirementV1
	runtimeOwnerRequirement?: #RuntimeOwnerRequirementV1
	runtimeAdapter?: {
		id: #ContractID
		supportedKinds: [...("container" | "native" | "host" | "external" | "control-plane")] & list.MinItems(1)
		supportedDeliveries: [...("stackkit" | "selected-paas" | "external-control-plane")] & list.MinItems(1)
		operations: [...("apply" | "observe" | "rollback" | "backup" | "restore")] & list.MinItems(1)
		agentRefs: [...#ContractID] | *[]
		credentialCustody: "external-owner"
		providerLifecycle: "not-owned"
		evidenceRequired:  true
	}
	runtimeAdapterAgent?: #RuntimeAdapterAgentContractV1
	runtime:              #ModuleRuntimeContractV2
	// renderTarget records the exact target used to select this module's
	// render-unit and artifact projection.
	renderTarget:   #GenerationTarget
	renderVariant?: #ResolvedModuleRenderVariantV2
	renderUnits: [...#ResolvedModuleRenderUnitV2] | *[]
	realizationSupport: #ModuleRealizationSupportV2
	// Gate references are currently projected only onto explicit host/external
	// provider owners. The compiler emits no module-level refs, so accepting
	// caller-authored values here would create unsigned semantic metadata.
	healthGateRefs?:   _|_
	evidenceGateRefs?: _|_
	let moduleID = id
	_providesUnique: list.UniqueItems(provides) & true
	if len(provides) == 0 && role != "workload" {
		realizationSupport: {
			scope: "concrete"
			level: "contract-only" | "generation-ready" | "apply-ready"
		}
		_typedImplementationContract: [
			for unit in renderUnits
			for contract in unit.providesInterfaces {"\(unit.id)/\(contract.id)"},
			if runtimeAdapter != _|_ {"runtime-adapter/\(runtimeAdapter.id)"},
			if runtimeAdapterAgent != _|_ {"runtime-adapter-agent/\(runtimeAdapterAgent.id)"},
		] & list.MinItems(1)
	}
	if runtimeAdapter != _|_ {
		role: "platform"
	}
	if runtimeAdapterAgent != _|_ {
		role: "platform"
	}
	if role == "workload" {
		provides: []
	}
	if requires != _|_ {
		_requiresUnique: list.UniqueItems(requires) & true
	}
	if enforcementRequirement != _|_ {
		if enforcementRequirement.status == "unbound" {
			runtime: execution: "contract-handoff"
		}
		if enforcementRequirement.status == "bound" {
			runtime: execution: "executable"
		}
	}
	if runtimeOwnerRequirement != _|_ {
		runtime: execution: "contract-handoff"
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
	_secretInputBindingCapabilitiesProvided: [for unit in renderUnits for inputRef, binding in unit.secretInputBindings {
		unit:       unit.id
		input:      inputRef
		capability: binding.capabilityRef
		matches: [for capabilityRef in provides if capabilityRef == binding.capabilityRef {capabilityRef}] & list.MinItems(1) & list.MaxItems(1)
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

#WorkloadAlternativeV2: {
	id:          #ContractID
	providerRef: #ContractID
	moduleRef:   #ContractID
	route: {
		serviceRef: #ContractID
		healthRef:  #ContractID
	}
	runtime: {
		allowedKinds: [...("container" | "native" | "host" | "external" | "control-plane")] & list.MinItems(1)
		allowedDeliveries: [...("stackkit" | "selected-paas" | "external-control-plane")] & list.MinItems(1)
		allowedAdapterRefs: [...#ContractID] | *[]
		defaultAdapterRef?: #ContractID
	}
	setup: {
		mode:  "automatic" | "on-demand" | "manual"
		owner: "module" | "operator"
		actionRefs: [...#ContractID] | *[]
	}
	inputs: {
		settings: {
			allowedRefs: [...#ContractID] | *[]
			requiredRefs: [...#ContractID] | *[]
		}
		secretInputs: {
			allowedRefs: [...#ContractID] | *[]
			requiredRefs: [...#ContractID] | *[]
		}
	}

	_runtimeKindsUnique:       list.UniqueItems(runtime.allowedKinds) & true
	_runtimeDeliveriesUnique:  list.UniqueItems(runtime.allowedDeliveries) & true
	_runtimeAdapterRefsUnique: list.UniqueItems(runtime.allowedAdapterRefs) & true
	if runtime.defaultAdapterRef != _|_ {
		_runtimeDefaultAdapterAllowed: [for adapterRef in runtime.allowedAdapterRefs if adapterRef == runtime.defaultAdapterRef {adapterRef}] & list.MinItems(1) & list.MaxItems(1)
	}
	if len(runtime.allowedAdapterRefs) > 0 {
		runtime: defaultAdapterRef: #ContractID
	}
	_setupActionsUnique: list.UniqueItems(setup.actionRefs) & true
	if setup.mode == "manual" {
		setup: {owner: "operator", actionRefs: []}
	}
	if setup.mode == "automatic" || setup.mode == "on-demand" {
		setup: {owner: "module", actionRefs: list.MinItems(1)}
	}
	_settingsAllowedUnique:  list.UniqueItems(inputs.settings.allowedRefs) & true
	_settingsRequiredUnique: list.UniqueItems(inputs.settings.requiredRefs) & true
	_secretsAllowedUnique:   list.UniqueItems(inputs.secretInputs.allowedRefs) & true
	_secretsRequiredUnique:  list.UniqueItems(inputs.secretInputs.requiredRefs) & true
	_settingsRequiredAllowed: [for required in inputs.settings.requiredRefs {
		ref: required
		matches: [for allowed in inputs.settings.allowedRefs if required == allowed {allowed}] & list.MinItems(1) & list.MaxItems(1)
	}]
	_secretsRequiredAllowed: [for required in inputs.secretInputs.requiredRefs {
		ref: required
		matches: [for allowed in inputs.secretInputs.allowedRefs if required == allowed {allowed}] & list.MinItems(1) & list.MaxItems(1)
	}]
}

#WorkloadContractV2: {
	metadata: {
		id:          #WorkloadID
		version:     #SemanticVersion
		description: string & =~"^.+$"
	}
	kind: "application" | "service"
	functionalCapabilities: [...#ContractID] & list.MinItems(1)
	supportedSiteKinds: [...#SiteKind] & list.MinItems(1)
	dataClasses: [...#DataClass] | *[]
	defaultAlternative?: #ContractID
	alternatives: [...#WorkloadAlternativeV2] & list.MinItems(1)

	_functionalCapabilitiesUnique: list.UniqueItems(functionalCapabilities) & true
	_supportedSiteKindsUnique:     list.UniqueItems(supportedSiteKinds) & true
	_dataClassesUnique:            list.UniqueItems(dataClasses) & true
	_alternativeIDsUnique: list.UniqueItems([for alternative in alternatives {alternative.id}]) & true
	if defaultAlternative != _|_ {
		_defaultAlternativeExact: [for alternative in alternatives if alternative.id == defaultAlternative {alternative.id}] & list.MinItems(1) & list.MaxItems(1)
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

#ResolvedWorkloadV2: {
	id:           #WorkloadID
	version:      #SemanticVersion
	contractHash: #ContentHash
	kind:         "application" | "service"
	functionalCapabilities: [...#ContractID] & list.MinItems(1)
	dataClasses: [...#DataClass] | *[]
	alternative: {
		id:           #ContractID
		contractHash: #ContentHash
		providerRef:  #ContractID
		moduleRef:    #ContractID
		route: {
			serviceRef: #ContractID
			healthRef:  #ContractID
		}
		runtime: {
			kind:     "container" | "native" | "host" | "external" | "control-plane"
			delivery: "stackkit" | "selected-paas" | "external-control-plane"
			adapter?: {
				id:                   #ContractID
				providerRef:          #ContractID
				providerVersion:      string
				providerContractHash: #ContentHash
				moduleRef:            #ContractID
				moduleVersion:        #SemanticVersion
				moduleContractHash:   #ContentHash
			}
		}
		setup: {
			mode:  "automatic" | "on-demand" | "manual"
			owner: "module" | "operator"
			actionRefs: [...#ContractID] | *[]
		}
	}
	siteRefs: [...#SiteID] & list.MinItems(1)
	nodeRefs: [...#NodeID] & list.MinItems(1)
	settings: #PublicSettings | *{}
	secretRefs: [string]: #SecretReference | *{}

	_functionalCapabilitiesUnique: list.UniqueItems(functionalCapabilities) & true
	_dataClassesUnique:            list.UniqueItems(dataClasses) & true
	_siteRefsUnique:               list.UniqueItems(siteRefs) & true
	_nodeRefsUnique:               list.UniqueItems(nodeRefs) & true
}

#ResolvedIdentityPlan: {
	humanAuthoritySiteRef:   #SiteID
	deviceAuthoritySiteRef?: #SiteID
	edgeVerifierSiteRefs?: [...#SiteID]
	deviceEnrollment?:       #DeviceEnrollmentPolicy
	possessionBoundSessions: true
	lanLocationIsIdentity:   false
}

#ResolvedIdentityIssuerURNV2:   string & =~"^urn:stackkit:[a-z][a-z0-9-]*:issuer:[a-z][a-z0-9-]*$"
#ResolvedIdentityAudienceURNV2: string & =~"^urn:stackkit:[a-z][a-z0-9-]*:audience:[a-z][a-z0-9-]*$"
#ResolvedIdentityKeySetURNV2:   string & =~"^urn:stackkit:[a-z][a-z0-9-]*:keyset:[a-z][a-z0-9-]*$"

// Resolved placements contain exact compiler materialization only. Definition
// selectors have no representation at this boundary.
#ResolvedIdentityTrustPlacementV2: {
	kind: "sites"
	siteRefs: [...#SiteID] & list.MinItems(1)
	_siteRefsUnique: list.UniqueItems(siteRefs) & true
	_siteRefsSorted: [for index, siteRef in siteRefs if index > 0 {(siteRefs[index-1] < siteRef) & true}]
} | {
	kind:        "external"
	contractRef: #ContractID
}

#ResolvedIdentityAuthorityV2: {
	id:             #ContractID
	principal:      #IdentityTrustPrincipalV2
	trustDomainRef: #ContractID
	placement:      #ResolvedIdentityTrustPlacementV2
	owner:          #IdentityTrustOwnerV2

	if placement.kind == "external" {
		owner: #IdentityTrustExternalOwnerV2 & {contractRef: placement.contractRef}
	}
	if placement.kind == "sites" {
		owner: #IdentityTrustCatalogOwnerV2
	}
}

#ResolvedCredentialIssuerV2: {
	id:           #ContractID
	authorityRef: #ContractID
	principal:    #IdentityTrustPrincipalV2
	issuer:       #ResolvedIdentityIssuerURNV2
	audiences: [...#ResolvedIdentityAudienceURNV2] & list.MinItems(1)
	verificationKeySetRef:         #ResolvedIdentityKeySetURNV2
	placement:                     #ResolvedIdentityTrustPlacementV2
	owner:                         #IdentityTrustOwnerV2
	issuanceWithinStackKit:        bool
	credentialTTLSeconds:          int & >=300 & <=86400
	sessionTTLSeconds:             int & >=60 & <=86400
	proofOfPossessionRequired:     bool
	revocationSupported:           true
	revocationMaxStalenessSeconds: int & >=0
	enrollment:                    #IdentityEnrollmentContractV2

	_audiencesUnique: list.UniqueItems(audiences) & true
	if placement.kind == "external" {
		owner: #IdentityTrustExternalOwnerV2 & {contractRef: placement.contractRef}
		issuanceWithinStackKit: false
		enrollment: {mode: "none", exposure: "none"}
	}
	if placement.kind == "sites" {
		owner: #IdentityTrustCatalogOwnerV2
	}
	if principal == "device" if issuanceWithinStackKit == true {
		proofOfPossessionRequired: true
		enrollment: {mode: "local-only", exposure: "lan"}
	}
	if principal != "device" {
		enrollment: {mode: "none", exposure: "none"}
	}
}

#ResolvedVerifierPlacementV2: {
	id:                  #ContractID
	credentialIssuerRef: #ContractID
	issuer:              #ResolvedIdentityIssuerURNV2
	principal:           #IdentityTrustPrincipalV2
	audiences: [...#ResolvedIdentityAudienceURNV2] & list.MinItems(1)
	verificationKeySetRef: #ResolvedIdentityKeySetURNV2
	placement: #ResolvedIdentityTrustPlacementV2 & {kind: "sites"}
	owner:                         #IdentityTrustCatalogOwnerV2
	proofOfPossessionRequired:     bool
	revocationMaxStalenessSeconds: int & >=0

	_audiencesUnique: list.UniqueItems(audiences) & true
}

#ResolvedVerifierDistributionV2: {
	id:                  #ContractID
	credentialIssuerRef: #ContractID
	issuer:              #ResolvedIdentityIssuerURNV2
	from: #ResolvedIdentityTrustPlacementV2 & {kind: "sites"}
	to: #ResolvedIdentityTrustPlacementV2 & {kind: "sites"}
	materials: ["revocation-state", "verification-key-reference"]
	includesSigningAuthority:    false
	includesEnrollmentAuthority: false
	includesPrivateKeyMaterial:  false
	includesCredentialMaterial:  false
	reverseAllowed:              false
	maxStalenessSeconds:         int & >=0
	owner:                       #IdentityTrustCatalogOwnerV2
}

#ResolvedIdentityTrustV2: {
	authorities: [...#ResolvedIdentityAuthorityV2] & list.MinItems(1)
	credentialIssuers: [...#ResolvedCredentialIssuerV2] & list.MinItems(1)
	verifierPlacements: [...#ResolvedVerifierPlacementV2] & list.MinItems(1)
	verifierDistributions: [...#ResolvedVerifierDistributionV2] | *[]

	_authorityIDsUnique: list.UniqueItems([for authority in authorities {authority.id}]) & true
	_issuerIDsUnique: list.UniqueItems([for issuer in credentialIssuers {issuer.id}]) & true
	_issuerURNsUnique: list.UniqueItems([for issuer in credentialIssuers {issuer.issuer}]) & true
	_verifierIDsUnique: list.UniqueItems([for verifier in verifierPlacements {verifier.id}]) & true
	_distributionIDsUnique: list.UniqueItems([for distribution in verifierDistributions {distribution.id}]) & true
	_issuerAuthorities: [for issuer in credentialIssuers {
		issuer: issuer.id
		matches: [for authority in authorities if authority.id == issuer.authorityRef && authority.principal == issuer.principal {authority.id}] & list.MinItems(1) & list.MaxItems(1)
	}]
	_verifierIssuers: [for verifier in verifierPlacements {
		verifier: verifier.id
		matches: [for issuer in credentialIssuers if issuer.id == verifier.credentialIssuerRef && issuer.issuer == verifier.issuer && issuer.principal == verifier.principal && issuer.verificationKeySetRef == verifier.verificationKeySetRef && issuer.proofOfPossessionRequired == verifier.proofOfPossessionRequired {issuer.id}] & list.MinItems(1) & list.MaxItems(1)
		audiences: [for audience in verifier.audiences {
			value: audience
			matches: [for issuer in credentialIssuers if issuer.id == verifier.credentialIssuerRef for accepted in issuer.audiences if accepted == audience {accepted}] & list.MinItems(1) & list.MaxItems(1)
		}]
	}]
	_issuerVerifiers: [for issuer in credentialIssuers {
		issuer: issuer.id
		matches: [for verifier in verifierPlacements if verifier.credentialIssuerRef == issuer.id && verifier.issuer == issuer.issuer && verifier.principal == issuer.principal && verifier.verificationKeySetRef == issuer.verificationKeySetRef && verifier.proofOfPossessionRequired == issuer.proofOfPossessionRequired {verifier.id}] & list.MinItems(1)
	}]
	_distributionIssuers: [for distribution in verifierDistributions {
		distribution: distribution.id
		matches: [for issuer in credentialIssuers if issuer.id == distribution.credentialIssuerRef && issuer.issuer == distribution.issuer && issuer.issuanceWithinStackKit == true {issuer.id}] & list.MinItems(1) & list.MaxItems(1)
	}]
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
	backendPools: [...#ResolvedRouteBackendPoolV2] | *[]
	_routeIDsUnique: list.UniqueItems([for route in routes {route.id}]) & true
	_backendPoolIDsUnique: list.UniqueItems([for pool in backendPools {pool.id}]) & true
	_routePoolRefsExact: [for route in routes {
		route: route.id
		matches: [for pool in backendPools if pool.id == route.backendPoolRef && pool.routeRef == route.id {pool.id}] & list.MinItems(1) & list.MaxItems(1)
	}]
	_poolRouteRefsExact: [for pool in backendPools {
		pool: pool.id
		matches: [for route in routes if route.id == pool.routeRef && route.backendPoolRef == pool.id {route.id}] & list.MinItems(1) & list.MaxItems(1)
	}]
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
	id:               #ContractID
	sourceRef?:       #ContractID
	phase:            "pre-apply" | "post-apply" | "continuous"
	kind:             "contract" | "http" | "tcp" | "container" | "process"
	execution?:       "contract-only" | "probe"
	protocol?:        "http" | "https" | "tcp" | "udp"
	method?:          "GET" | "HEAD"
	followRedirects?: false
	path?:            string & =~"^/"
	port?:            int & >=1 & <=65535
	timeoutSeconds:   int & >=1 & <=300
	expectedStatuses?: [...int & >=100 & <=599]
	contractHash: #ContentHash
	targetKind:   "capability" | "provider" | "module" | "route" | "node"
	targetRef:    #ContractID
	siteRefs: [...#SiteID] & list.MinItems(1)
	nodeRefs?: [...#NodeID]
	routeRef?:            #ContractID
	backendPoolRef?:      #ContractID
	sourceHealthGateRef?: #ContractID
	scope?:               "each-backend-member" | "each-node"
	required:             true
	if targetKind == "route" {
		sourceRef?:          _|_
		phase:               "post-apply"
		targetRef:           routeRef
		routeRef:            #ContractID
		backendPoolRef:      #ContractID
		sourceHealthGateRef: #ContractID
		scope:               "each-backend-member"
		execution:           "contract-only" | "probe"
		protocol:            "http" | "https" | "tcp" | "udp"
		port:                int & >=1 & <=65535
		kind:                "contract" | "http" | "tcp"
		if execution == "contract-only" {
			kind: "contract"
		}
		if execution == "probe" {
			kind: "http" | "tcp"
		}
		if protocol == "http" || protocol == "https" {
			if execution == "probe" {
				kind:            "http"
				method:          "GET" | "HEAD"
				followRedirects: false
			}
		}
		if protocol == "tcp" && execution == "probe" {
			kind: "tcp"
		}
	}
	if targetKind != "route" {
		sourceRef:            #ContractID
		execution?:           _|_
		protocol?:            _|_
		method?:              _|_
		followRedirects?:     _|_
		routeRef?:            _|_
		backendPoolRef?:      _|_
		sourceHealthGateRef?: _|_
		if scope != _|_ {scope: "each-node"}
	}

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
	"policy-enforcement-owner-unbound" |
	"runtime-owner-unbound" |
	"external-home-access-binding-missing" |
	"external-backup-target-binding-missing" |
	"external-home-backup-target-binding-missing" |
	"external-federation-link-binding-missing" |
	"required-evidence-missing" |
	"bridge-overlay-unverified" |
	"bridge-control-agent-unverified" |
	"policy-enforcement-unverified" |
	"partition-policy-enforcement-unverified" |
	"device-verifier-unbound" |
	"health-gate-not-executable" |
	"route-health-executor-unbound"

// #ExecutionReadinessBlockerV1 uses stable machine-readable codes and refs.
// Human prose deliberately stays outside the signed plan contract.
#ExecutionReadinessBlockerV1: {
	code: #ExecutionReadinessBlockerCodeV1
	refs: [...string & =~"^(module|unit|provider|renderer|input|artifact|evidence|enforcement|runtime-owner|bridge|publication|identity|tls|health|route|backend-pool|site|capability|home-access-requirement|backup-target-requirement|home-backup-target-requirement|federation-link-requirement):[^[:space:]]+$"] & list.MinItems(1)
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
	externalHostBindings: [#NodeID]:    #ExternalHostBindingV1
	hostConformanceReceipts: [#NodeID]: #HostConformanceReceiptV1
	homeAccessRequirements: [#SiteID]: [#CapabilityID]:           #HomeAccessRequirementV1
	externalHomeAccessBindings: [#SiteID]: [#CapabilityID]:       #ExternalHomeAccessBindingV1
	backupTargetRequirements: [#SiteID]: [#CapabilityID]:         #BackupTargetRequirementV1
	externalBackupTargetBindings: [#SiteID]: [#CapabilityID]:     #ExternalBackupTargetBindingV1
	homeBackupTargetRequirements: [#SiteID]: [#CapabilityID]:     #HomeBackupTargetRequirementV1
	externalHomeBackupTargetBindings: [#SiteID]: [#CapabilityID]: #ExternalHomeBackupTargetBindingV1
	federationLinkRequirements: [#CapabilityID]:     #FederationLinkRequirementV1
	externalFederationLinkBindings: [#CapabilityID]: #ExternalFederationLinkBindingV1
	_siteIDsUnique: list.UniqueItems([for site in sites {site.id}]) & true
	_nodeIDsUnique: list.UniqueItems([for node in nodes {node.id}]) & true
	_externalHostBindingNodes: [for nodeRef, binding in externalHostBindings {
		node: nodeRef
		matches: [for node in nodes if node.id == nodeRef && binding.nodeRef == nodeRef && binding.stackId == stackId {node.id}] & list.MinItems(1) & list.MaxItems(1)
	}]
	_hostConformanceNodes: [for nodeRef, receipt in hostConformanceReceipts {
		node: nodeRef
		bindingMatches: [
			for bindingNodeRef, binding in externalHostBindings
			if bindingNodeRef == nodeRef && binding.bindingRef == receipt.bindingRef && binding.bindingHash == receipt.bindingHash && receipt.nodeRef == nodeRef && receipt.stackId == stackId {bindingNodeRef},
		] & list.MinItems(1) & list.MaxItems(1)
	}]
	_homeAccessRequirementKeys: [for siteRef, capabilityRequirements in homeAccessRequirements for capabilityRef, requirement in capabilityRequirements {
		site:       siteRef & requirement.siteRef
		capability: capabilityRef & requirement.capabilityRef
		stack:      stackId & requirement.stackId
		spec:       specHash & requirement.specHash
	}]
	_externalHomeAccessBindingKeys: [for siteRef, capabilityBindings in externalHomeAccessBindings for capabilityRef, binding in capabilityBindings {
		site:       siteRef
		capability: capabilityRef
		matches: [
			for requirementSiteRef, capabilityRequirements in homeAccessRequirements
			for requirementCapabilityRef, requirement in capabilityRequirements
			if requirementSiteRef == siteRef && requirementCapabilityRef == capabilityRef && binding.siteRef == siteRef && binding.capabilityRef == capabilityRef && binding.stackId == stackId && binding.specHash == specHash && binding.contractOwnerRef == requirement.contractOwnerRef && binding.capabilityContractHash == requirement.capabilityContractHash && binding.requirementsHash == requirement.requirementsHash {requirementCapabilityRef},
		] & list.MinItems(1) & list.MaxItems(1)
	}]
	_backupTargetRequirementKeys: [for siteRef, capabilityRequirements in backupTargetRequirements for capabilityRef, requirement in capabilityRequirements {
		site:       siteRef & requirement.siteRef
		capability: capabilityRef & requirement.capabilityRef
		stack:      stackId & requirement.stackId
		spec:       specHash & requirement.specHash
	}]
	_externalBackupTargetBindingKeys: [for siteRef, capabilityBindings in externalBackupTargetBindings for capabilityRef, binding in capabilityBindings {
		site:       siteRef
		capability: capabilityRef
		matches: [
			for requirementSiteRef, capabilityRequirements in backupTargetRequirements
			for requirementCapabilityRef, requirement in capabilityRequirements
			if requirementSiteRef == siteRef && requirementCapabilityRef == capabilityRef && binding.siteRef == siteRef && binding.capabilityRef == capabilityRef && binding.stackId == stackId && binding.specHash == specHash && binding.contractOwnerRef == requirement.contractOwnerRef && binding.capabilityContractHash == requirement.capabilityContractHash && binding.requirementsHash == requirement.requirementsHash {requirementCapabilityRef},
		] & list.MinItems(1) & list.MaxItems(1)
	}]
	_homeBackupTargetRequirementKeys: [for siteRef, capabilityRequirements in homeBackupTargetRequirements for capabilityRef, requirement in capabilityRequirements {
		site:       siteRef & requirement.siteRef
		capability: capabilityRef & requirement.capabilityRef
		stack:      stackId & requirement.stackId
		spec:       specHash & requirement.specHash
	}]
	_externalHomeBackupTargetBindingKeys: [for siteRef, capabilityBindings in externalHomeBackupTargetBindings for capabilityRef, binding in capabilityBindings {
		site:       siteRef
		capability: capabilityRef
		matches: [
			for requirementSiteRef, capabilityRequirements in homeBackupTargetRequirements
			for requirementCapabilityRef, requirement in capabilityRequirements
			if requirementSiteRef == siteRef && requirementCapabilityRef == capabilityRef && binding.siteRef == siteRef && binding.capabilityRef == capabilityRef && binding.stackId == stackId && binding.specHash == specHash && binding.contractOwnerRef == requirement.contractOwnerRef && binding.capabilityContractHash == requirement.capabilityContractHash && binding.requirementsHash == requirement.requirementsHash {requirementCapabilityRef},
		] & list.MinItems(1) & list.MaxItems(1)
	}]
	_federationLinkRequirementKeys: [for capabilityRef, requirement in federationLinkRequirements {
		capability: capabilityRef & requirement.capabilityRef
		stack:      stackId & requirement.stackId
		spec:       specHash & requirement.specHash
	}]
	_externalFederationLinkBindingKeys: [for capabilityRef, binding in externalFederationLinkBindings {
		capability: capabilityRef
		matches: [
			for requirementCapabilityRef, requirement in federationLinkRequirements
			if requirementCapabilityRef == capabilityRef && binding.capabilityRef == capabilityRef && binding.stackId == stackId && binding.specHash == specHash && binding.contractOwnerRef == requirement.contractOwnerRef && binding.capabilityContractHash == requirement.capabilityContractHash && binding.bridgeContractHash == requirement.bridgeContractHash && binding.requirementsHash == requirement.requirementsHash {requirementCapabilityRef},
		] & list.MinItems(1) & list.MaxItems(1)
	}]
	controlPlane: #ControlPlaneIntent
	capabilities: [...#ResolvedCapability] & list.MinItems(1)
	providers: [...#ResolvedProvider] & list.MinItems(1)
	workloads: [...#ResolvedWorkloadV2] | *[]
	// The field is mandatory, but an empty list is valid when every selected
	// provider realizes behavior through host/external contracts. Providers do
	// not become modules by name.
	modules: [...#ResolvedModuleV2] | *[]
	_moduleSecretInputSourcesExact: [
		for module in modules
		for unit in module.renderUnits
		for targetRef, binding in unit.secretInputBindings {
			module:     module.id
			unit:       unit.id
			target:     targetRef
			capability: binding.capabilityRef
			matches: [
				for capability in capabilities
				if capability.id == binding.capabilityRef && capability.providerRef == module.providerRef
				if capability.secretRefs != _|_
				for sourceInputRef, sourceSecretRef in capability.secretRefs
				if sourceInputRef == binding.key && sourceSecretRef == unit.secretRefs[targetRef] {capability.id},
			] & list.MinItems(1) & list.MaxItems(1)
		},
	]
	runtimeNetworks!: [...#ResolvedRuntimeNetworkInstanceV1]
	privilegedInterfaceApprovals: [...#ResolvedPrivilegedInterfaceApprovalV2] | *[]
	placement: [...#ResolvedPlacementV2] | *[]
	_workloadIDsUnique: list.UniqueItems([for workload in workloads {workload.id}]) & true
	_placementWorkloadRefsUnique: list.UniqueItems([for item in placement {item.workloadRef}]) & true
	_placementWorkloadRefsExact: [for item in placement {
		workloadRef: item.workloadRef
		matches: [for workload in workloads if workload.id == item.workloadRef && workload.siteRefs == item.siteRefs && workload.nodeRefs == item.nodeRefs {workload.id}] & list.MinItems(1) & list.MaxItems(1)
	}]
	_workloadBindings: [for workload in workloads {
		workloadID: workload.id
		providerMatches: [for provider in providers if provider.id == workload.alternative.providerRef && list.Contains(provider.workloadRefs, workload.id) {provider.id}] & list.MinItems(1) & list.MaxItems(1)
		moduleMatches: [for module in modules if module.id == workload.alternative.moduleRef && module.providerRef == workload.alternative.providerRef && module.role == "workload" && module.runtime.kind == workload.alternative.runtime.kind && module.runtime.delivery == workload.alternative.runtime.delivery {
			moduleID: module.id
			siteRefs: module.siteRefs & workload.siteRefs
			nodeRefs: module.nodeRefs & workload.nodeRefs
			serviceMatches: [for unit in module.renderUnits for endpoint in unit.serviceEndpoints if endpoint.serviceRef == workload.alternative.route.serviceRef && endpoint.healthRef == workload.alternative.route.healthRef {unit.id}] & list.MinItems(1) & list.MaxItems(1)
		}] & list.MinItems(1) & list.MaxItems(1)
		placementMatches: [for item in placement if item.workloadRef == workload.id && item.siteRefs == workload.siteRefs && item.nodeRefs == workload.nodeRefs {item.workloadRef}] & list.MinItems(1) & list.MaxItems(1)
		if workload.alternative.runtime.adapter != _|_ {
			adapterProviderMatches: [for provider in providers if provider.id == workload.alternative.runtime.adapter.providerRef && provider.version == workload.alternative.runtime.adapter.providerVersion && provider.contractHash == workload.alternative.runtime.adapter.providerContractHash && list.Contains(provider.runtimeAdapterRefs, workload.alternative.runtime.adapter.id) {provider.id}] & list.MinItems(1) & list.MaxItems(1)
			adapterModuleMatches: [for module in modules if module.runtimeAdapter != _|_ if module.id == workload.alternative.runtime.adapter.moduleRef && module.version == workload.alternative.runtime.adapter.moduleVersion && module.contractHash == workload.alternative.runtime.adapter.moduleContractHash && module.providerRef == workload.alternative.runtime.adapter.providerRef && module.runtimeAdapter.id == workload.alternative.runtime.adapter.id {module.id}] & list.MinItems(1) & list.MaxItems(1)
		}
		if len(workload.dataClasses) > 0 {
			dataMatches: [
				for module in modules
				if module.id == workload.alternative.moduleRef
				for unit in module.renderUnits
				for endpoint in unit.serviceEndpoints
				if endpoint.serviceRef == workload.alternative.route.serviceRef && endpoint.healthRef == workload.alternative.route.healthRef
				for bindingID, binding in data.bindings
				if bindingID == endpoint.data.bindingRef && list.Contains(workload.siteRefs, binding.primarySiteRef) {
					bindingRef: bindingID
					classes:    binding.classes & workload.dataClasses
				},
			] & list.MinItems(1) & list.MaxItems(1)
		}
	}]
	_workloadProviderRefsExact: [for provider in providers for workloadRef in provider.workloadRefs {
		providerID: provider.id
		workloadID: workloadRef
		matches: [for workload in workloads if workload.id == workloadRef && workload.alternative.providerRef == provider.id {workload.id}] & list.MinItems(1) & list.MaxItems(1)
	}]
	_providerContractsNonEmpty: [for provider in providers {
		providerID: provider.id
		contracts: list.Concat([provider.provides, provider.workloadRefs, provider.runtimeAdapterRefs]) & list.MinItems(1)
	}]
	_workloadModulesExact: [for module in modules if module.role == "workload" {
		moduleID: module.id
		matches: [for workload in workloads if workload.alternative.moduleRef == module.id && workload.alternative.providerRef == module.providerRef {workload.id}] & list.MinItems(1) & list.MaxItems(1)
	}]
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
	identityTrust:      #ResolvedIdentityTrustV2
	data:               #DataPlacementIntent
	failurePolicy:      #PartitionPolicy
	system:             #ResolvedSystemPlanV2
	storage:            #StorageIntentV2
	network:            #ResolvedNetworkPlanV2
	homeLANDiscovery:   #HomeLANDiscoveryProjectionV2
	gates:              #ResolvedPlanGatesV2
	executionReadiness: #ExecutionReadinessV1
	evidence: [...string] & list.MinItems(1)
	let modulePlanSites = [for site in sites {
		id:            site.id
		kind:          site.kind
		failureDomain: site.failureDomain
	}]
	let modulePlanInstall = install
	let modulePlanSystem = system
	_homeLANDiscoverySitesExact: homeLANDiscovery.homeSiteRefs & [for site in sites if site.kind == "home" {site.id}]
	_homeLANDiscoveryAdvertisementsExact: [for advertisement in homeLANDiscovery.advertisements {
		routeRef: advertisement.routeRef
		matches: [
			for route in network.routes
			if route.id == advertisement.routeRef
			if route.exposure == "local"
			if route.host != _|_
			if route.host !~ "(?i)(^|\\.)localhost$"
			if route.access.policyExposure == "lan" {
				serviceRef:      advertisement.serviceRef & route.serviceRef
				originSiteCount: len(route.originSiteRefs) & 1
				originSiteRef:   advertisement.originSiteRef & route.originSiteRefs[0]
				originNodeRefs:  advertisement.originNodeRefs & route.originNodeRefs
				protocol:        advertisement.protocol & route.protocol
				port:            advertisement.port & route.port
				host:            advertisement.host & route.host
				access: {
					policyRef:      advertisement.access.policyRef & route.access.policyRef
					policyExposure: advertisement.access.policyExposure & route.access.policyExposure
					defaultClosed:  advertisement.access.defaultClosed & route.access.defaultClosed
				}
			},
		] & list.MinItems(1) & list.MaxItems(1)
		homeOrigin: [for homeSiteRef in homeLANDiscovery.homeSiteRefs if homeSiteRef == advertisement.originSiteRef {homeSiteRef}] & list.MinItems(1) & list.MaxItems(1)
	}]
	if len(homeLANDiscovery.advertisements) > 0 {
		_homeLANDiscoveryCapabilitySelected: [for capability in capabilities if capability.id == "lan-discovery" {capability.id}] & list.MinItems(1) & list.MaxItems(1)
	}
	_modulePlanInputsExact: [
		for module in modules
		for unit in module.renderUnits
		for inputRef in unit.planInputRefs {
			module: module.id
			unit:   unit.id
			input:  inputRef
			if inputRef == "stackId" {
				value: unit.planInputs.stackId & stackId
			}
			if inputRef == "kit" {
				value: unit.planInputs.kit & kit
			}
			if inputRef == "sites" {
				value: unit.planInputs.sites & modulePlanSites
			}
			if inputRef == "controlPlane" {
				value: unit.planInputs.controlPlane & controlPlane
			}
			if inputRef == "bridge" {
				value: unit.planInputs.bridge & bridge
			}
			if inputRef == "identity" {
				value: unit.planInputs.identity & identity
			}
			if inputRef == "data" {
				value: unit.planInputs.data & data
			}
			if inputRef == "failurePolicy" {
				value: unit.planInputs.failurePolicy & failurePolicy
			}
			if inputRef == "localReachability" {
				value: unit.planInputs.localReachability
			}
			if inputRef == "homeLANDiscovery" {
				value: unit.planInputs.homeLANDiscovery & homeLANDiscovery
			}
			if inputRef == "moduleTargets" {
				value: unit.planInputs.moduleTargets & [
					for moduleNodeRef in module.nodeRefs
					for node in nodes
					if node.id == moduleNodeRef {
						id:               node.id
						siteRef:          node.siteRef
						roles:            node.roles
						failureDomain:    node.failureDomain
						declaredHardware: node.hardware
					},
				]
			}
			if inputRef == "moduleCapabilities" {
				value: unit.planInputs.moduleCapabilities & [
					for moduleCapabilityRef in module.provides
					for capability in capabilities
					if capability.id == moduleCapabilityRef {
						id:           capability.id
						contractHash: capability.contractHash
					},
				]
			}
			if inputRef == "hostRuntimePolicy" {
				value: unit.planInputs.hostRuntimePolicy & {
					install: {
						mode:    modulePlanInstall.mode
						runtime: modulePlanInstall.runtime
						platform: {
							management:      modulePlanInstall.platform.management
							fallbackAllowed: modulePlanInstall.platform.fallbackAllowed
							setupPolicy:     modulePlanInstall.platform.setupPolicy
						}
					}
					host: modulePlanSystem.host
					if modulePlanSystem.container != _|_ {
						container: {
							engine:      modulePlanSystem.container.engine
							rootless:    modulePlanSystem.container.rootless
							liveRestore: modulePlanSystem.container.liveRestore
							dataRoot:    modulePlanSystem.container.dataRoot
							logDriver:   modulePlanSystem.container.logDriver
							if modulePlanSystem.container.storageDriver != _|_ {storageDriver: modulePlanSystem.container.storageDriver}
						}
					}
				}
			}
			if inputRef == "storagePolicy" {
				value: unit.planInputs.storagePolicy & storage
			}
			if inputRef == "localNetworkPolicy" {
				value: unit.planInputs.localNetworkPolicy & {
					mode:      network.configuration.mode
					domain:    network.configuration.domain
					transport: network.configuration.transport
					dns:       network.configuration.dns
					tls: {
						defaultMode: network.configuration.tls.defaultMode
						minVersion:  network.configuration.tls.minVersion
					}
				}
			}
			if inputRef == "cloudNetworkPolicy" {
				value: unit.planInputs.cloudNetworkPolicy & {
					mode:      network.configuration.mode
					domain:    network.configuration.domain
					transport: network.configuration.transport
					dns:       network.configuration.dns
					tls: {
						defaultMode: network.configuration.tls.defaultMode
						minVersion:  network.configuration.tls.minVersion
					}
				}
			}
			if inputRef == "publicTLS" {
				let publicTLSCapabilities = [for capability in capabilities if capability.id == "public-tls" && capability.providerRef == module.providerRef {capability}]
				let publicTLSIssuers = [for provider in providers if provider.id == module.providerRef for issuer in provider.certificateIssuers if issuer.capabilityRef == "public-tls" {issuer}]
				value: unit.planInputs.publicTLS & {
					_profileMatches: publicTLSCapabilities & list.MinItems(1) & list.MaxItems(1)
					_issuerMatches:  publicTLSIssuers & list.MinItems(1) & list.MaxItems(1)
					capabilityRef:   "public-tls"
					providerRef:     module.providerRef
					profile:         publicTLSCapabilities[0].tlsProfile
					issuer: {
						id:                   publicTLSIssuers[0].id
						capabilityRef:        publicTLSIssuers[0].capabilityRef
						kind:                 publicTLSIssuers[0].kind
						challenge:            publicTLSIssuers[0].challenge
						supportedSiteKinds:   publicTLSIssuers[0].supportedSiteKinds
						validitySeconds:      publicTLSIssuers[0].validitySeconds
						requiredInputSlotIDs: publicTLSIssuers[0].requiredInputSlotIDs
						materialSlots:        publicTLSIssuers[0].materialSlots
						renewal:              publicTLSIssuers[0].renewal
					}
					routes: [for route in network.routes if route.exposure == "public" && route.protocol == "https" && route.tls.mode == "terminate-at-edge" && route.tls.profileRef == publicTLSCapabilities[0].tlsProfile.id && route.tls.issuerRef == publicTLSIssuers[0].id {
						id:       route.id
						host:     route.host
						port:     route.port
						path:     route.path
						exposure: route.exposure
						protocol: route.protocol
						tls: {
							mode:       route.tls.mode
							minVersion: route.tls.minVersion
							profileRef: route.tls.profileRef
							issuerRef:  route.tls.issuerRef
						}
					}]
				}
			}
		},
	]
	if bridge != _|_ {
		_bridgeActionIdentityClosure: [for action in bridge.controlAgent.actions {
			action: action.id
			issuerMatches: [
				for issuer in identityTrust.credentialIssuers
				if issuer.principal == "workload" && strings.HasSuffix(issuer.issuer, ":issuer:\(action.issuerRef)")
				for audience in issuer.audiences
				if strings.HasSuffix(audience, ":audience:\(action.audience)")
				if issuer.placement.kind == "sites"
				for siteRef in issuer.placement.siteRefs
				if siteRef == controlPlane.authoritySiteRef {issuer.id},
			] & list.MinItems(1) & list.MaxItems(1)
		}]
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
		if len(bridge.publications) > 0 {
			access: [string]: #AccessPolicyV2
			data: bindings: [string]: {
				classes: [...#DataClass] & list.MinItems(1)
				primarySiteRef: #SiteID
				replicaSiteRefs?: [...#SiteID]
				cloudCopyAllowed: bool | *false
			}
			_resolvedBridgePublicationEndpoints: [for publication in bridge.publications {
				service: publication.serviceRef
				matches: [
					for module in modules
					for unit in module.renderUnits
					for endpoint in unit.serviceEndpoints
					if endpoint.serviceRef == publication.serviceRef
					for allowedExposure in endpoint.allowedExposures
					if allowedExposure == "public"
					for allowedProtocol in endpoint.allowedIngressProtocols
					if allowedProtocol == publication.protocol
					if endpoint.upstreamProtocol == publication.upstreamProtocol && endpoint.targetPort == publication.targetPort
					if endpoint.data.bindingRef == publication.dataBindingRef {
						moduleRef:               publication.moduleRef & module.id
						unitRef:                 publication.unitRef & unit.id
						supportedOriginSelector: endpoint.originSelector & ("single-site" | "control-authority-site")
						healthGateRef:           publication.healthGateRef & "module-\(module.id)-\(endpoint.healthRef)"
						originNodes: [for originNodeRef in publication.originNodeRefs {
							node: originNodeRef
							matches: [
								for unitNodeRef in unit.nodeRefs
								for node in nodes
								if unitNodeRef == originNodeRef && node.id == unitNodeRef && node.siteRef == publication.sourceSiteRef {unitNodeRef},
							] & list.MinItems(1) & list.MaxItems(1)
						}]
						originNodesComplete: [
							for unitNodeRef in unit.nodeRefs
							for node in nodes
							if node.id == unitNodeRef && node.siteRef == publication.sourceSiteRef {
								node: unitNodeRef
								matches: [for originNodeRef in publication.originNodeRefs if originNodeRef == unitNodeRef {originNodeRef}] & list.MinItems(1) & list.MaxItems(1)
							},
						]
						originInstances: [for originInstanceRef in publication.originInstanceRefs {
							instanceRef: originInstanceRef
							matches: [
								for instance in unit.instances
								if instance.scope == "node-local"
								if instance.id == originInstanceRef && instance.siteRef == publication.sourceSiteRef {instance.id},
							] & list.MinItems(1) & list.MaxItems(1)
						}]
						originInstancesComplete: [
							for instance in unit.instances
							if instance.scope == "node-local"
							if instance.siteRef == publication.sourceSiteRef {
								instanceRef: instance.id
								matches: [for originInstanceRef in publication.originInstanceRefs if originInstanceRef == instance.id {originInstanceRef}] & list.MinItems(1) & list.MaxItems(1)
							},
						]
						if endpoint.originSelector == "single-site" {
							unitSiteCount: len(unit.siteRefs) & 1
							originSite:    unit.siteRefs[0] & publication.sourceSiteRef
						}
						if endpoint.originSelector == "control-authority-site" {
							authoritySite: publication.sourceSiteRef & controlPlane.authoritySiteRef
						}
						if endpoint.data.locality == "primary-site" {
							dataPrimarySite: [
								for bindingID, binding in data.bindings
								if bindingID == endpoint.data.bindingRef && binding.primarySiteRef == publication.sourceSiteRef {bindingID},
							] & list.MinItems(1) & list.MaxItems(1)
						}
						if endpoint.data.locality == "primary-or-replica" {
							dataOriginSite: [
								for bindingID, binding in data.bindings
								if bindingID == endpoint.data.bindingRef && binding.primarySiteRef == publication.sourceSiteRef {bindingID},
								for bindingID, binding in data.bindings
								if bindingID == endpoint.data.bindingRef
								for replicaSiteRef in binding.replicaSiteRefs
								if replicaSiteRef == publication.sourceSiteRef {bindingID},
							] & list.MinItems(1) & list.MaxItems(1)
						}
					},
				] & list.MinItems(1) & list.MaxItems(1)
			}]
			_resolvedPublicationPolicyRefs: [for publication in bridge.publications {
				service: publication.serviceRef
				matches: [
					for policyID, policy in access
					if policyID == publication.auth.policyRef
					if policy.exposure == "public"
					if policy.authentication == "human+device"
					if policy.enrolledDeviceRequired == true
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
					if len(flow.ports) == 1
					for port in flow.ports
					if port == publication.targetPort
					if flow.protocol == "tcp" || flow.protocol == "udp" {flow.serviceRef},
					for flow in bridge.policy.allowedFlows
					if flow.fromSiteRef == publication.edgeSiteRef
					if flow.toSiteRef == publication.sourceSiteRef
					if flow.serviceRef == publication.serviceRef
					if flow.protocol == publication.upstreamProtocol
					if len(flow.ports) == 1
					for port in flow.ports
					if port == publication.targetPort
					if flow.protocol == "http" || flow.protocol == "https"
					if len(flow.methods) == len(publication.access.allowedMethods)
					if len([
						for method in flow.methods
						for allowed in publication.access.allowedMethods
						if method == allowed {method},
					]) == len(flow.methods) {flow.serviceRef},
				] & list.MinItems(1)
			}]
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
			_resolvedBridgeFlowEndpoints: [for flow in bridge.policy.allowedFlows {
				service: flow.serviceRef
				matches: [
					for module in modules
					for unit in module.renderUnits
					for endpoint in unit.serviceEndpoints
					if endpoint.serviceRef == flow.serviceRef
					if list.Contains(unit.siteRefs, flow.toSiteRef)
					if endpoint.upstreamProtocol == flow.protocol
					if len(flow.ports) == 1
					if list.Contains(flow.ports, endpoint.targetPort)
					if endpoint.data.bindingRef == flow.serviceRef
					if len(flow.dataClasses) == len(endpoint.data.requiredClasses)
					if len([
						for flowClass in flow.dataClasses
						for requiredClass in endpoint.data.requiredClasses
						if flowClass == requiredClass {flowClass},
					]) == len(flow.dataClasses) {
						service:                 endpoint.serviceRef
						supportedOriginSelector: endpoint.originSelector & ("single-site" | "control-authority-site")
						originNodes: [
							for unitNodeRef in unit.nodeRefs
							for node in nodes
							if node.id == unitNodeRef && node.siteRef == flow.toSiteRef {unitNodeRef},
						] & list.MinItems(1)
						originInstances: [
							for instance in unit.instances
							if instance.scope == "node-local"
							if instance.siteRef == flow.toSiteRef {instance.id},
						] & list.MinItems(1)
						if endpoint.originSelector == "single-site" {
							unitSiteCount: len(unit.siteRefs) & 1
							originSite:    unit.siteRefs[0] & flow.toSiteRef
						}
						if endpoint.originSelector == "control-authority-site" {
							authoritySite: flow.toSiteRef & controlPlane.authoritySiteRef
						}
						if endpoint.data.locality == "primary-site" {
							dataPrimarySite: [
								for bindingID, binding in data.bindings
								if bindingID == endpoint.data.bindingRef && binding.primarySiteRef == flow.toSiteRef {bindingID},
							] & list.MinItems(1) & list.MaxItems(1)
						}
						if endpoint.data.locality == "primary-or-replica" {
							dataOriginSite: [
								for bindingID, binding in data.bindings
								if bindingID == endpoint.data.bindingRef && binding.primarySiteRef == flow.toSiteRef {bindingID},
								for bindingID, binding in data.bindings
								if bindingID == endpoint.data.bindingRef
								for replicaSiteRef in binding.replicaSiteRefs
								if replicaSiteRef == flow.toSiteRef {bindingID},
							] & list.MinItems(1) & list.MaxItems(1)
						}
						flowReachability: [
							for allowedExposure in endpoint.allowedExposures
							if allowedExposure == "remote-private"
							for allowedProtocol in endpoint.allowedIngressProtocols
							if allowedProtocol == flow.protocol {"remote-private"},
							for allowedExposure in endpoint.allowedExposures
							if allowedExposure == "public"
							for publication in bridge.publications
							if publication.edgeSiteRef == flow.fromSiteRef && publication.sourceSiteRef == flow.toSiteRef
							if publication.serviceRef == flow.serviceRef && publication.moduleRef == module.id && publication.unitRef == unit.id
							if publication.upstreamProtocol == flow.protocol && publication.targetPort == endpoint.targetPort
							if publication.dataBindingRef == endpoint.data.bindingRef
							for allowedProtocol in endpoint.allowedIngressProtocols
							if allowedProtocol == publication.protocol
							if flow.protocol == "tcp" || flow.protocol == "udp" {"public"},
							for allowedExposure in endpoint.allowedExposures
							if allowedExposure == "public"
							for publication in bridge.publications
							if publication.edgeSiteRef == flow.fromSiteRef && publication.sourceSiteRef == flow.toSiteRef
							if publication.serviceRef == flow.serviceRef && publication.moduleRef == module.id && publication.unitRef == unit.id
							if publication.upstreamProtocol == flow.protocol && publication.targetPort == endpoint.targetPort
							if publication.dataBindingRef == endpoint.data.bindingRef
							for allowedProtocol in endpoint.allowedIngressProtocols
							if allowedProtocol == publication.protocol
							if flow.protocol == "http" || flow.protocol == "https"
							if len(flow.methods) == len(publication.access.allowedMethods)
							if len([
								for method in flow.methods
								for allowedMethod in publication.access.allowedMethods
								if method == allowedMethod {method},
							]) == len(flow.methods) {"public"},
						] & list.MinItems(1)
					},
				] & list.MinItems(1) & list.MaxItems(1)
			}]
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
	_enabledControllersExact: [for controller in _enabledControllerIDs {
		nodeRef: controller
		matches: [for member in controlPlane.members if member == controller {member}] & list.MinItems(1) & list.MaxItems(1)
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
		if len(provider.owner.realizationSupport.compatibleRendererRefs) > 0
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
	_modulePlanInputContractReadiness: [
		for module in modules
		if module.realizationSupport.level != "contract-only"
		if module.realizationSupport.planInputs.contractComplete == false {
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
	_moduleRequiredPlanInputReadiness: [
		for module in modules
		if module.realizationSupport.level != "contract-only"
		for inputRef in module.realizationSupport.planInputs.requiredRefs
		for unit in module.renderUnits
		if len([for declaredRef in unit.planInputRefs if declaredRef == inputRef {declaredRef}]) > 0
		if len([for key, _ in unit.planInputs if key == inputRef {key}]) == 0 {
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
	_modulePolicyEnforcementReadiness: [
		for module in modules
		if module.enforcementRequirement != _|_
		if module.enforcementRequirement.status == "unbound" {
			apply: #ExecutionReadinessRequirementV1 & {
				phase: executionReadiness.apply
				code:  "policy-enforcement-owner-unbound"
				refs: [
					"module:\(module.id)",
					"enforcement:\(module.enforcementRequirement.ownerRef)",
					"health:\(module.enforcementRequirement.requiredHealthRef)",
					"evidence:\(module.enforcementRequirement.requiredEvidenceRef)",
				]
			}
		},
	]
	_moduleRuntimeOwnerReadiness: [
		for module in modules
		if module.runtimeOwnerRequirement != _|_
		if module.runtimeOwnerRequirement.status == "unbound" {
			apply: #ExecutionReadinessRequirementV1 & {
				phase: executionReadiness.apply
				code:  "runtime-owner-unbound"
				refs: [
					"module:\(module.id)",
					"runtime-owner:\(module.runtimeOwnerRequirement.ownerRef)",
					"health:\(module.runtimeOwnerRequirement.requiredHealthRef)",
					"evidence:\(module.runtimeOwnerRequirement.requiredEvidenceRef)",
				]
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
			partitionPolicyGeneration: #ExecutionReadinessRequirementV1 & {
				phase: executionReadiness.generation
				code:  "partition-policy-enforcement-unverified"
				refs: ["bridge:partition-policy"]
			}
			partitionPolicyApply: #ExecutionReadinessRequirementV1 & {
				phase: executionReadiness.apply
				code:  "partition-policy-enforcement-unverified"
				refs: ["bridge:partition-policy"]
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
	_routeCapabilityRealizationBindings: [
		for serviceRoute in network.routes
		for realization in serviceRoute.capabilityRealizations {
			route:      serviceRoute.id
			capability: realization.capabilityRef
			capabilityMatches: [for capability in capabilities if capability.id == realization.capabilityRef && capability.contractHash == realization.capabilityContractHash && capability.providerRef == realization.providerRef {capability.id}] & list.MinItems(1) & list.MaxItems(1)
			providerMatches: [for provider in providers if provider.id == realization.providerRef && provider.contractHash == realization.providerContractHash && provider.realization.kind == realization.realizationKind {
				providerRef: provider.id
				siteRefs:    provider.siteRefs & realization.providerSiteRefs
			}] & list.MinItems(1) & list.MaxItems(1)
			if realization.realizationKind == "modules" {
				moduleMatches: [for module in modules if module.id == realization.moduleOwner.moduleRef && module.contractHash == realization.moduleOwner.moduleContractHash && module.providerRef == realization.providerRef && list.Contains(module.provides, realization.capabilityRef) {
					moduleRef: module.id
					siteRefs:  module.siteRefs & realization.moduleOwner.siteRefs
					nodeRefs:  module.nodeRefs & realization.moduleOwner.nodeRefs
				}] & list.MinItems(1) & list.MaxItems(1)
			}
		},
	]
	// TLS policy is derived from selected capabilities. A rehashed plan cannot
	// invent a certificate provider, retain HTTPS after removing its capability,
	// or terminate a public route with the Home-internal trust domain.
	_routeTLSCapabilityBindings: [for serviceRoute in network.routes {
		route: serviceRoute.id
		if serviceRoute.tls.required == false {
			mode: serviceRoute.tls.mode & "off"
		}
		if serviceRoute.tls.required == true {
			mode:           serviceRoute.tls.mode & ("internal" | "terminate-at-edge" | "external")
			minimumVersion: serviceRoute.tls.minVersion & network.configuration.tls.minVersion
			if serviceRoute.tls.mode != "external" {
				profileMinimumMatches: [for capability in capabilities if capability.tlsProfile != _|_ if capability.tlsProfile.id == serviceRoute.tls.profileRef {
					profile: capability.tlsProfile.id
					minimum: capability.tlsProfile.minimumVersion
					if capability.tlsProfile.minimumVersion == "TLS1.3" {
						routeMinimum: serviceRoute.tls.minVersion & "TLS1.3"
					}
				}] & list.MinItems(1) & list.MaxItems(1)
			}
			if serviceRoute.tls.mode == "internal" {
				defaultMode: network.configuration.tls.defaultMode & "internal"
				profileMatches: [for capability in capabilities if capability.id == "internal-pki" if capability.tlsProfile != _|_ if capability.tlsProfile.id == serviceRoute.tls.profileRef && capability.tlsProfile.mode == "internal" {capability.id}] & list.MinItems(1) & list.MaxItems(1)
				issuerMatches: [for capability in capabilities if capability.id == "internal-pki" if capability.tlsProfile != _|_ for provider in providers if provider.id == capability.providerRef for issuer in provider.certificateIssuers for allowedKind in capability.tlsProfile.allowedIssuerKinds if issuer.id == serviceRoute.tls.issuerRef && issuer.capabilityRef == capability.id && issuer.kind == allowedKind {issuer.id}] & list.MinItems(1) & list.MaxItems(1)
			}
			if serviceRoute.tls.mode == "terminate-at-edge" {
				defaultMode: network.configuration.tls.defaultMode & "public"
				profileMatches: [for capability in capabilities if capability.id == "public-tls" if capability.tlsProfile != _|_ if capability.tlsProfile.id == serviceRoute.tls.profileRef && capability.tlsProfile.mode == "terminate-at-edge" {capability.id}] & list.MinItems(1) & list.MaxItems(1)
				issuerMatches: [for capability in capabilities if capability.id == "public-tls" if capability.tlsProfile != _|_ for provider in providers if provider.id == capability.providerRef for issuer in provider.certificateIssuers for allowedKind in capability.tlsProfile.allowedIssuerKinds if issuer.id == serviceRoute.tls.issuerRef && issuer.capabilityRef == capability.id && issuer.kind == allowedKind {issuer.id}] & list.MinItems(1) & list.MaxItems(1)
			}
			if serviceRoute.tls.mode == "external" {
				egressOwnerMatches: [for realization in serviceRoute.capabilityRealizations if realization.capabilityRef == serviceRoute.tls.ownerCapabilityRef && realization.role == "egress" {realization.capabilityRef}] & list.MinItems(1) & list.MaxItems(1)
				homeAccessRequirementMatches: [
					for requirementSiteRef, capabilityRequirements in homeAccessRequirements
					if requirementSiteRef == serviceRoute.originSiteRef
					for capabilityRef, requirement in capabilityRequirements
					if capabilityRef == serviceRoute.tls.ownerCapabilityRef && requirement.capabilityRef == capabilityRef && requirement.siteRef == serviceRoute.originSiteRef {requirement.requirementsHash},
				] & list.MinItems(1) & list.MaxItems(1)
			}
		}
		if serviceRoute.exposure == "public" {
			publicTermination: serviceRoute.tls.mode & ("terminate-at-edge" | "external")
		}
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
			matches: [for originSiteRef in serviceRoute.originSiteRefs for siteRef in unit.siteRefs if siteRef == originSiteRef {siteRef}] & list.MinItems(len(serviceRoute.originSiteRefs)) & list.MaxItems(len(serviceRoute.originSiteRefs))
			if endpoint.originSelector == "single-site" {
				unitSiteCount: len(unit.siteRefs) & 1
				originSite:    serviceRoute.originSiteRef & serviceRoute.originSiteRefs[0]
			}
			if endpoint.originSelector == "control-authority-site" {
				authoritySite: serviceRoute.originSiteRef & serviceRoute.originSiteRefs[0] & controlPlane.authoritySiteRef
			}
			if endpoint.originSelector == "multi-zone" || endpoint.originSelector == "edge-pool" {
				selector:  serviceRoute.originSelector & endpoint.originSelector
				siteCount: len(serviceRoute.originSiteRefs) & int & >=endpoint.originSelection.minSites
				policy: {
					siteKinds:               serviceRoute.originSelection.siteKinds & endpoint.originSelection.siteKinds
					minSites:                serviceRoute.originSelection.minSites & endpoint.originSelection.minSites
					siteFailureDomainSpread: serviceRoute.originSelection.siteFailureDomainSpread & endpoint.originSelection.siteFailureDomainSpread
					nodeFailureDomainSpread: serviceRoute.originSelection.nodeFailureDomainSpread & endpoint.originSelection.nodeFailureDomainSpread
					requiredRoles:           serviceRoute.originSelection.requiredRoles & endpoint.originSelection.requiredRoles
				}
				selectedSites: [for originSiteRef in serviceRoute.originSiteRefs {
					siteRef: originSiteRef
					matches: [
						for site in sites
						if site.id == originSiteRef
						for allowedKind in endpoint.originSelection.siteKinds
						if site.kind == allowedKind {site.id},
					] & list.MinItems(1) & list.MaxItems(1)
				}]
				resolvedSiteFailureDomains: [for failureDomain in serviceRoute.originSelection.siteFailureDomains {
					value: failureDomain
					matches: [
						for site in sites
						for originSiteRef in serviceRoute.originSiteRefs
						if site.id == originSiteRef && site.failureDomain == failureDomain {site.id},
					] & list.MinItems(1)
				}]
				selectedSiteFailureDomains: [
					for site in sites
					for originSiteRef in serviceRoute.originSiteRefs
					if site.id == originSiteRef {
						siteRef: site.id
						matches: [for failureDomain in serviceRoute.originSelection.siteFailureDomains if failureDomain == site.failureDomain {failureDomain}] & list.MinItems(1) & list.MaxItems(1)
					},
				]
			}
		},
	]
	_routeOriginSelectionBindings: [
		for serviceRoute in network.routes
		if serviceRoute.originSelector == "multi-zone" || serviceRoute.originSelector == "edge-pool"
		for module in modules
		if module.id == serviceRoute.moduleRef
		for unit in module.renderUnits
		for endpoint in unit.serviceEndpoints
		if endpoint.serviceRef == serviceRoute.serviceRef {
			route: serviceRoute.id
			selectedNodes: [for originNodeRef in serviceRoute.originNodeRefs {
				nodeRef: originNodeRef
				matches: [
					for topologyNode in nodes
					if topologyNode.id == originNodeRef
					for originSiteRef in serviceRoute.originSiteRefs
					if topologyNode.siteRef == originSiteRef {topologyNode.id},
				] & list.MinItems(1) & list.MaxItems(1)
				requiredRoles: [for requiredRole in endpoint.originSelection.requiredRoles {
					role: requiredRole
					matches: [
						for topologyNode in nodes
						if topologyNode.id == originNodeRef
						for nodeRole in topologyNode.roles
						if nodeRole == requiredRole {nodeRole},
					] & list.MinItems(1) & list.MaxItems(1)
				}]
				failureDomain: [
					for topologyNode in nodes
					if topologyNode.id == originNodeRef
					for domain in serviceRoute.originSelection.nodeFailureDomains
					if domain.siteRef == topologyNode.siteRef && domain.failureDomain == topologyNode.failureDomain {domain.failureDomain},
				] & list.MinItems(1) & list.MaxItems(1)
			}]
			resolvedNodeFailureDomains: [for domain in serviceRoute.originSelection.nodeFailureDomains {
				siteRef:       domain.siteRef
				failureDomain: domain.failureDomain
				matches: [
					for topologyNode in nodes
					for originNodeRef in serviceRoute.originNodeRefs
					if topologyNode.id == originNodeRef && topologyNode.siteRef == domain.siteRef && topologyNode.failureDomain == domain.failureDomain {topologyNode.id},
				] & list.MinItems(1)
			}]
			if serviceRoute.originSelector == "multi-zone" {
				candidatesComplete: [
					for instance in unit.instances
					if instance.scope == "node-local"
					for site in sites
					if site.id == instance.siteRef
					for allowedKind in endpoint.originSelection.siteKinds
					if site.kind == allowedKind {
						instanceRef: instance.id
						siteMatches: [for originSiteRef in serviceRoute.originSiteRefs if originSiteRef == instance.siteRef {originSiteRef}] & list.MinItems(1) & list.MaxItems(1)
						nodeMatches: [for originNodeRef in serviceRoute.originNodeRefs if originNodeRef == instance.nodeRef {originNodeRef}] & list.MinItems(1) & list.MaxItems(1)
					},
				]
			}
			if serviceRoute.originSelector == "edge-pool" {
				candidatesComplete: [
					for instance in unit.instances
					if instance.scope == "node-local"
					for site in sites
					if site.id == instance.siteRef
					for allowedKind in endpoint.originSelection.siteKinds
					if site.kind == allowedKind
					for topologyNode in nodes
					if topologyNode.id == instance.nodeRef
					for nodeRole in topologyNode.roles
					if nodeRole == "edge" {
						instanceRef: instance.id
						siteMatches: [for originSiteRef in serviceRoute.originSiteRefs if originSiteRef == instance.siteRef {originSiteRef}] & list.MinItems(1) & list.MaxItems(1)
						nodeMatches: [for originNodeRef in serviceRoute.originNodeRefs if originNodeRef == instance.nodeRef {originNodeRef}] & list.MinItems(1) & list.MaxItems(1)
					},
				]
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
				if topologyNode.id == unitNodeRef && unitNodeRef == nodeRef
				for originSiteRef in serviceRoute.originSiteRefs
				if topologyNode.siteRef == originSiteRef {unitNodeRef},
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
		for originSiteRef in serviceRoute.originSiteRefs
		if topologyNode.id == unitNodeRef && topologyNode.siteRef == originSiteRef {
			route: serviceRoute.id
			node:  unitNodeRef
			matches: [for routeNodeRef in serviceRoute.originNodeRefs if routeNodeRef == unitNodeRef {routeNodeRef}] & list.MinItems(1) & list.MaxItems(1)
		},
	]
	_routeBackendPoolBindings: [
		for pool in network.backendPools
		for serviceRoute in network.routes
		if serviceRoute.id == pool.routeRef && serviceRoute.backendPoolRef == pool.id
		for module in modules
		if module.id == serviceRoute.moduleRef && module.id == pool.moduleRef
		for unit in module.renderUnits
		if unit.id == pool.unitRef
		for endpoint in unit.serviceEndpoints
		if endpoint.serviceRef == serviceRoute.serviceRef && endpoint.serviceRef == pool.serviceRef {
			poolID:         pool.id
			originSelector: pool.originSelector & endpoint.originSelector
			if endpoint.originSelector == "multi-zone" || endpoint.originSelector == "edge-pool" {
				originSelection: pool.originSelection & serviceRoute.originSelection
			}
			upstreamProtocol: pool.upstreamProtocol & serviceRoute.upstreamProtocol & endpoint.upstreamProtocol
			targetPort:       pool.targetPort & serviceRoute.targetPort & endpoint.targetPort
			if endpoint.originSelector != "edge-pool" {
				memberCountExact: len(pool.members) & len([
					for instance in unit.instances
					if instance.scope == "node-local"
					for originSiteRef in serviceRoute.originSiteRefs
					if instance.siteRef == originSiteRef {instance.id},
				])
			}
			if endpoint.originSelector == "edge-pool" {
				memberCountExact: len(pool.members) & len([
					for instance in unit.instances
					if instance.scope == "node-local"
					for originSiteRef in serviceRoute.originSiteRefs
					if instance.siteRef == originSiteRef
					for topologyNode in nodes
					if topologyNode.id == instance.nodeRef
					for nodeRole in topologyNode.roles
					if nodeRole == "edge" {instance.id},
				])
			}
			members: [for member in pool.members {
				instanceRef: member.instanceRef
				matches: [
					for instance in unit.instances
					if instance.scope == "node-local"
					if instance.id == member.instanceRef && instance.siteRef == member.siteRef && instance.nodeRef == member.nodeRef
					for originSiteRef in serviceRoute.originSiteRefs
					if member.siteRef == originSiteRef
					for routeNodeRef in serviceRoute.originNodeRefs
					if routeNodeRef == member.nodeRef {instance.id},
				] & list.MinItems(1) & list.MaxItems(1)
				if endpoint.originSelector == "edge-pool" {
					edgeNodeMatches: [
						for topologyNode in nodes
						if topologyNode.id == member.nodeRef
						for nodeRole in topologyNode.roles
						if nodeRole == "edge" {topologyNode.id},
					] & list.MinItems(1) & list.MaxItems(1)
				}
			}]
			if endpoint.originSelector != "edge-pool" {
				instancesComplete: [
					for instance in unit.instances
					if instance.scope == "node-local"
					for originSiteRef in serviceRoute.originSiteRefs
					if instance.siteRef == originSiteRef {
						instanceRef: instance.id
						matches: [for member in pool.members if member.instanceRef == instance.id && member.siteRef == instance.siteRef && member.nodeRef == instance.nodeRef {member.instanceRef}] & list.MinItems(1) & list.MaxItems(1)
					},
				]
			}
			if endpoint.originSelector == "edge-pool" {
				instancesComplete: [
					for instance in unit.instances
					if instance.scope == "node-local"
					for originSiteRef in serviceRoute.originSiteRefs
					if instance.siteRef == originSiteRef
					for topologyNode in nodes
					if topologyNode.id == instance.nodeRef
					for nodeRole in topologyNode.roles
					if nodeRole == "edge" {
						instanceRef: instance.id
						matches: [for member in pool.members if member.instanceRef == instance.id && member.siteRef == instance.siteRef && member.nodeRef == instance.nodeRef {member.instanceRef}] & list.MinItems(1) & list.MaxItems(1)
					},
				]
			}
		},
	]
	_routeBackendPoolCatalogMatches: [for pool in network.backendPools {
		poolID: pool.id
		matches: [
			for serviceRoute in network.routes
			if serviceRoute.id == pool.routeRef && serviceRoute.backendPoolRef == pool.id
			for module in modules
			if module.id == serviceRoute.moduleRef && module.id == pool.moduleRef
			for unit in module.renderUnits
			if unit.id == pool.unitRef
			for endpoint in unit.serviceEndpoints
			if endpoint.serviceRef == serviceRoute.serviceRef && endpoint.serviceRef == pool.serviceRef {endpoint.serviceRef},
		] & list.MinItems(1) & list.MaxItems(1)
	}]
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
				originSiteCount: len(serviceRoute.originSiteRefs) & 1
				locality: [for originSiteRef in serviceRoute.originSiteRefs {
					siteRef: originSiteRef
					matches: [
						for bindingID, dataBinding in data.bindings
						if bindingID == endpoint.data.bindingRef && dataBinding.primarySiteRef == originSiteRef {bindingID},
					] & list.MinItems(1) & list.MaxItems(1)
				}]
			}
			if endpoint.data.locality == "primary-or-replica" {
				locality: [for originSiteRef in serviceRoute.originSiteRefs {
					siteRef: originSiteRef
					matches: [
						for bindingID, dataBinding in data.bindings
						if bindingID == endpoint.data.bindingRef && dataBinding.primarySiteRef == originSiteRef {bindingID},
						for bindingID, dataBinding in data.bindings
						if bindingID == endpoint.data.bindingRef && dataBinding.replicaSiteRefs != _|_
						for replicaRef in dataBinding.replicaSiteRefs
						if replicaRef == originSiteRef {bindingID},
					] & list.MinItems(1) & list.MaxItems(1)
				}]
			}
		},
	]
	_routeServiceHealthGates: [
		for serviceRoute in network.routes
		for backendPool in network.backendPools
		if backendPool.id == serviceRoute.backendPoolRef
		for module in modules
		if module.id == serviceRoute.moduleRef
		for unit in module.renderUnits
		for endpoint in unit.serviceEndpoints
		if endpoint.serviceRef == serviceRoute.serviceRef {
			route: serviceRoute.id
			matches: [
				for healthGate in gates.health
				if healthGate.id == serviceRoute.healthGateRef
				if healthGate.targetKind == "route" && healthGate.targetRef == serviceRoute.id
				if healthGate.routeRef == serviceRoute.id && healthGate.backendPoolRef == backendPool.id {healthGate.id},
			] & list.MinItems(1) & list.MaxItems(1)
			bindings: [
				for healthGate in gates.health
				if healthGate.id == serviceRoute.healthGateRef
				for sourceHealthGate in gates.health
				if sourceHealthGate.id == healthGate.sourceHealthGateRef
				if sourceHealthGate.targetKind == "module" && sourceHealthGate.targetRef == module.id {
					sourceHealthGateRef: healthGate.sourceHealthGateRef & "module-\(module.id)-\(endpoint.healthRef)"
					protocol:            healthGate.protocol & serviceRoute.upstreamProtocol & backendPool.upstreamProtocol
					port:                healthGate.port & serviceRoute.targetPort & backendPool.targetPort
					scope:               healthGate.scope & "each-backend-member"
					siteRefs:            healthGate.siteRefs & serviceRoute.originSiteRefs
					nodeRefs:            healthGate.nodeRefs & serviceRoute.originNodeRefs
					if sourceHealthGate.kind == "http" && serviceRoute.upstreamProtocol == "http" {
						sourcePort:       sourceHealthGate.port & serviceRoute.targetPort
						execution:        healthGate.execution & "probe"
						kind:             healthGate.kind & "http"
						method:           healthGate.method & "GET"
						followRedirects:  healthGate.followRedirects & false
						path:             healthGate.path & sourceHealthGate.path
						expectedStatuses: healthGate.expectedStatuses & sourceHealthGate.expectedStatuses
					}

					// HTTPS needs an executor-private binding for SNI, peer identity,
					// and trust roots. A route descriptor cannot honestly make that
					// probe executable on its own.
					if serviceRoute.upstreamProtocol == "https" {
						execution: healthGate.execution & "contract-only"
						kind:      healthGate.kind & "contract"
						if sourceHealthGate.kind == "http" {
							sourcePort: sourceHealthGate.port & serviceRoute.targetPort
						}
					}
					if sourceHealthGate.kind == "tcp" && serviceRoute.upstreamProtocol == "tcp" {
						sourcePort: sourceHealthGate.port & serviceRoute.targetPort
						execution:  healthGate.execution & "probe"
						kind:       healthGate.kind & "tcp"
					}
					if sourceHealthGate.kind != "http" && serviceRoute.upstreamProtocol == "http" {
						execution: healthGate.execution & "contract-only"
						kind:      healthGate.kind & "contract"
					}
					if sourceHealthGate.kind != "tcp" && serviceRoute.upstreamProtocol == "tcp" {
						execution: healthGate.execution & "contract-only"
						kind:      healthGate.kind & "contract"
					}
					if serviceRoute.upstreamProtocol == "udp" {
						execution: healthGate.execution & "contract-only"
						kind:      healthGate.kind & "contract"
					}
				},
			] & list.MinItems(1) & list.MaxItems(1)
			sourceMatches: [
				for healthGate in gates.health
				if healthGate.id == "module-\(module.id)-\(endpoint.healthRef)"
				if healthGate.targetKind == "module" && healthGate.targetRef == module.id {healthGate.id},
			] & list.MinItems(1) & list.MaxItems(1)
		},
	]
	_routeHealthReadinessRequirements: [
		for serviceRoute in network.routes
		for healthGate in gates.health
		if healthGate.id == serviceRoute.healthGateRef {
			executor: #ExecutionReadinessRequirementV1 & {
				phase: executionReadiness.apply
				code:  "route-health-executor-unbound"
				refs: ["route:\(serviceRoute.id)", "health:\(healthGate.id)", "backend-pool:\(serviceRoute.backendPoolRef)"]
			}
			if healthGate.execution == "contract-only" {
				executable: #ExecutionReadinessRequirementV1 & {
					phase: executionReadiness.apply
					code:  "health-gate-not-executable"
					refs: ["route:\(serviceRoute.id)", "health:\(healthGate.id)", "backend-pool:\(serviceRoute.backendPoolRef)"]
				}
			}
		},
	]
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
	_routeOriginSiteRefs: [for route in network.routes for originSiteRef in route.originSiteRefs {
		route: route.id
		site:  originSiteRef
		matches: [for site in sites if site.id == originSiteRef {site.id}] & list.MinItems(1) & list.MaxItems(1)
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
		workloadRefsExact:  plan._placementWorkloadRefsExact
		siteRefsUnique:     plan._placementSiteRefsUnique
		nodeRefsUnique:     plan._placementNodeRefsUnique
		sitesExist:         plan._placementSitesExist
		nodesExact:         plan._placementNodesExact
		sitesBackedByNodes: plan._placementSitesBackedByNodes
	}
	integrity: workloads: {
		bindings:                  plan._workloadBindings
		providerRefsExact:         plan._workloadProviderRefsExact
		providerContractsNonEmpty: plan._providerContractsNonEmpty
		modulesExact:              plan._workloadModulesExact
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
		serviceEndpointBindings:     plan._routeServiceEndpointBindings
		serviceOriginSites:          plan._routeServiceOriginSites
		serviceOriginNodes:          plan._routeServiceOriginNodes
		serviceOriginNodesComplete:  plan._routeServiceOriginNodesComplete
		backendPoolBindings:         plan._routeBackendPoolBindings
		backendPoolCatalogMatches:   plan._routeBackendPoolCatalogMatches
		serviceDataBindings:         plan._routeServiceDataBindings
		serviceHealthGates:          plan._routeServiceHealthGates
		healthReadinessRequirements: plan._routeHealthReadinessRequirements
		tlsCapabilityBindings:       plan._routeTLSCapabilityBindings
		capabilityRealizations:      plan._routeCapabilityRealizationBindings
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
	if spec.workloads != _|_ {
		_requestedWorkloadPolicyChecks: [for workloadID, selection in spec.workloads {
			_workloadID: workloadID
			_allowed: [for declared in list.Concat([definition.workloads.required, definition.workloads.defaults, definition.workloads.optional]) if declared == workloadID {declared}] & list.MinItems(1) & list.MaxItems(1)
			_forbidden: [for denied in definition.workloads.forbidden if denied == workloadID {denied}] & list.MaxItems(0)
			_selectedSites: [for siteRef in selection.placement.siteRefs {
				_siteRef: siteRef
				_matches: [for site in spec.sites if site.id == siteRef {site.id}] & list.MinItems(1) & list.MaxItems(1)
			}]
			_selectedNodes: [for nodeRef in selection.placement.nodeRefs {
				_nodeRef: nodeRef
				_matches: [for node in spec.nodes if node.id == nodeRef && node.enabled {node.id}] & list.MinItems(1) & list.MaxItems(1)
			}]
			_eligibleNodes: [for node in spec.nodes
				if node.enabled
				if len(selection.placement.siteRefs) == 0 || list.Contains(selection.placement.siteRefs, node.siteRef)
				if len(selection.placement.nodeRefs) == 0 || list.Contains(selection.placement.nodeRefs, node.id)
				if len([for requiredRole in selection.placement.requiresRoles if list.Contains(node.roles, requiredRole) {requiredRole}]) == len(selection.placement.requiresRoles) {node.id},
			] & list.MinItems(1)
		}]
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
		if definition.authoring != _|_ {
			_authoringDomainOverrideRequired: [for field in definition.authoring.requiredOverrides if field == "network.domain.base" {field}] & list.MinItems(1) & list.MaxItems(1)
		}
	}
	if definition.network.domainRequired == false if definition.authoring != _|_ {
		_authoringDomainOverrideForbidden: [for field in definition.authoring.requiredOverrides if field == "network.domain.base" {field}] & list.MaxItems(0)
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
			for required in definition.reachability.routes[serviceRoute.exposure].requiredRealizations {
				route:      routeID
				capability: required.capabilityRef
				matches: [
					for selected in _effectiveCapabilities
					if required.capabilityRef == selected {selected},
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
	if len(spec.lanDiscovery.advertiseRouteRefs) > 0 {
		_lanDiscoveryCapabilityCheck: [
			for selected in _effectiveCapabilities
			if selected == "lan-discovery" {selected},
		] & list.MinItems(1) & list.MaxItems(1)
	}

	if definition.bridge.required {
		spec: bridge: #BridgeContract
		_bridgePublicationDeviceBoundPolicies: [for publication in spec.bridge.publications {
			service: publication.serviceRef
			matches: [
				for policyID, policy in spec.access
				if policyID == publication.auth.policyRef
				if policy.exposure == "public"
				if policy.authentication == "human+device"
				if policy.enrolledDeviceRequired == true {policyID},
			] & list.MinItems(1) & list.MaxItems(1)
		}]
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
