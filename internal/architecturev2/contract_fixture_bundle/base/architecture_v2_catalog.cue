// Package base - governed Architecture v2 capability, provider, and add-on
// contracts. Kit definitions declare what must exist; this catalog declares
// which immutable implementation contracts are allowed to realize it.
package base

import "list"

_architectureV2CoreCapabilities: [
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

_architectureV2LocalCapabilities: [
	"site-local",
	"provision-local",
	"local-hardware-preflight",
	"lan-discovery",
	"local-ingress",
	"lan-access-policy",
	"device-enrollment-home",
	"local-control-authority",
	"offline-autonomy",
	"local-backup-target",
	"lan-dns",
	"internal-pki",
	"private-remote-access",
	"public-publish-egress",
	"encrypted-offsite-backup",
]

_architectureV2CloudCapabilities: [
	"site-cloud",
	"provision-vps",
	"provider-metadata",
	"cloud-firewall",
	"public-edge",
	"public-dns",
	"public-tls",
	"internet-host-hardening",
	"remote-owner-bootstrap",
	"offsite-object-backup",
	"provider-lifecycle",
	"cloud-control-authority",
	"private-admin-mesh",
	"provider-snapshot",
	"multi-zone-placement",
]

// Application capabilities stay independent of kit/site semantics. A kit may
// allow an application without making it part of the kit baseline, while the
// selected provider and module retain ownership of its runtime contract.
_architectureV2ApplicationCapabilityContracts: [#CapabilityContract & {
	metadata: {
		id:          "photos"
		version:     "1.0.0"
		description: "Self-hosted photo management service with governed endpoint and personal-data locality."
		layer:       "application"
	}
	requires: [
		{id: "runtime-paas"},
		{id: "service-catalog"},
		{id: "storage-data-policy"},
	]
	supportedSiteKinds: ["home", "cloud"]
	dataClasses: ["personal"]
	evidence: ["SK-S1", "SK-S2", "SK-S4"]
}]

// Product-private catalog extensions override these defaults in a separate CUE
// source owned by the corresponding authority profile. A public projection can
// therefore omit the entire extension source without editing catalog text.
_architectureV2ProfileExtensionCapabilityContracts: [...#CapabilityContract] | *[]
_architectureV2ProfileExtensionProviders: [...#CapabilityProvider] | *[]
_architectureV2ProfileExtensionModules: [...#ModuleContractV2] | *[]
_architectureV2ProfileExtensionPrivilegedInterfaceApprovals: [...#PrivilegedInterfaceApprovalV2] | *[]

_architectureV2HACapabilities: ["availability-ha"]

// HA remains one add-on surface, while each kit and control-plane mode selects
// a distinct governed provider/module realization. The KitDefinition owns the
// policy envelope and references these immutable catalog IDs.
_architectureV2HARealizations: [
	{
		providerID: "stackkits-ha-basement-warm", moduleID: "stackkits-ha-basement-warm-runtime"
		supportedSiteKinds: ["home"], healthID: "ha-basement-warm-contract"
		authoritySelection:                     "any"
		evidenceRef:                            "ha-basement-warm-standby-failure-domain-proof"
	},
	{
		providerID: "stackkits-ha-basement-quorum", moduleID: "stackkits-ha-basement-quorum-runtime"
		supportedSiteKinds: ["home"], healthID: "ha-basement-quorum-contract"
		authoritySelection:                     "any"
		evidenceRef:                            "ha-basement-quorum-failure-domain-proof"
	},
	{
		providerID: "stackkits-ha-cloud-warm", moduleID: "stackkits-ha-cloud-warm-runtime"
		supportedSiteKinds: ["cloud"], healthID: "ha-cloud-warm-contract"
		authoritySelection:                      "any"
		evidenceRef:                             "ha-cloud-warm-standby-zone-failover-proof"
	},
	{
		providerID: "stackkits-ha-cloud-quorum", moduleID: "stackkits-ha-cloud-quorum-runtime"
		supportedSiteKinds: ["cloud"], healthID: "ha-cloud-quorum-contract"
		authoritySelection:                      "any"
		evidenceRef:                             "ha-cloud-quorum-zone-majority-proof"
	},
	{
		providerID: "stackkits-ha-modern-warm", moduleID: "stackkits-ha-modern-warm-runtime"
		supportedSiteKinds: ["home", "cloud"], healthID: "ha-modern-warm-contract"
		authoritySelection:                              "control-authority-site"
		evidenceRef:                                     "ha-modern-warm-standby-partition-isolation-proof"
	},
	{
		providerID: "stackkits-ha-modern-quorum", moduleID: "stackkits-ha-modern-quorum-runtime"
		supportedSiteKinds: ["home", "cloud"], healthID: "ha-modern-quorum-contract"
		authoritySelection:                              "control-authority-site"
		evidenceRef:                                     "ha-modern-quorum-partition-majority-proof"
	},
]

// The canonical plan snapshot is the only artifact that exists independently
// of selected modules. Compose/OpenTofu outputs belong to concrete module
// contracts and must never be enumerated by Go compiler configuration.
_architectureV2PlanArtifacts: [#CatalogPlanArtifactV2 & {
	id:       "resolved-plan"
	kind:     "metadata"
	path:     ".stackkit/resolved-plan.json"
	format:   "json"
	mode:     "0600"
	required: true
	compatibleTargets: ["compose", "opentofu"]
}]

// These capabilities are intentionally catalogued without a provider. A kit
// that forbids one can therefore explain the governed contract, while any
// attempt to enable it still fails closed because no realization is approved.
_architectureV2DeniedCapabilities: [
	"cloud-enrollment-authority",
	"broad-lan-route-advertisement",
]

_architectureV2Capabilities: list.Concat([
	[for capabilityID in _architectureV2CoreCapabilities {
		metadata: {
			id:          capabilityID
			version:     "1.0.0"
			description: "Shared StackKits Architecture v2 contract for \(capabilityID)."
			layer:       "foundation"
		}
		supportedSiteKinds: ["home", "cloud"]
		evidence: ["resolved-plan-contract"]
	}],
	[for capabilityID in _architectureV2LocalCapabilities {
		metadata: {
			id:          capabilityID
			version:     "1.0.0"
			description: "Home-site Architecture v2 contract for \(capabilityID)."
			layer:       "platform"
		}
		supportedSiteKinds: ["home"]
		evidence: ["SK-S1"]
	}],
	[for capabilityID in _architectureV2CloudCapabilities {
		metadata: {
			id:          capabilityID
			version:     "1.0.0"
			description: "Cloud-site Architecture v2 contract for \(capabilityID)."
			layer:       "platform"
		}
		supportedSiteKinds: ["cloud"]
		evidence: ["SK-S2", "SK-S3"]
	}],
	_architectureV2ApplicationCapabilityContracts,
	_architectureV2ProfileExtensionCapabilityContracts,
	[for capabilityID in _architectureV2HACapabilities {
		metadata: {
			id:          capabilityID
			version:     "1.0.0"
			description: "High-availability add-on contract for \(capabilityID)."
			layer:       "operations"
		}
		supportedSiteKinds: ["home", "cloud"]
		evidence: ["ha-failure-domain-proof"]
	}],
	[for capabilityID in _architectureV2DeniedCapabilities {
		metadata: {
			id:          capabilityID
			version:     "1.0.0"
			description: "Known unsafe or profile-incompatible contract for \(capabilityID)."
			layer:       "foundation"
		}
		supportedSiteKinds: ["home", "cloud"]
	}],
])

_architectureV2Providers: list.Concat([[
	{
		metadata: {id: "stackkits-core", version: "1.0.0"}
		provides: _architectureV2CoreCapabilities
		supportedSiteKinds: ["home", "cloud"]
		realization: {kind: "modules", moduleRefs: {required: ["stackkits-core-runtime"], optional: []}}
		selection: defaultForSiteKinds: ["home", "cloud"]
		health: [{id: "stackkits-core-contract", kind: "contract"}]
		evidence: ["resolved-plan-contract"]
	},
	{
		metadata: {id: "stackkits-local", version: "1.0.0"}
		provides: _architectureV2LocalCapabilities
		requires: [{id: "topology-core"}]
		supportedSiteKinds: ["home"]
		realization: {kind: "modules", moduleRefs: {required: ["stackkits-local-runtime"], optional: []}}
		selection: defaultForSiteKinds: ["home"]
		health: [{id: "stackkits-local-contract", kind: "contract"}]
		evidence: ["SK-S1"]
	},
	{
		metadata: {id: "stackkits-cloud", version: "1.0.0"}
		provides: _architectureV2CloudCapabilities
		requires: [{id: "topology-core"}]
		supportedSiteKinds: ["cloud"]
		realization: {kind: "modules", moduleRefs: {required: ["stackkits-cloud-runtime"], optional: []}}
		selection: defaultForSiteKinds: ["cloud"]
		health: [{id: "stackkits-cloud-contract", kind: "contract"}]
		evidence: ["SK-S2", "SK-S3"]
	},
	{
		metadata: {id: "stackkits-immich", version: "1.0.0"}
		provides: ["photos"]
		requires: [
			{id: "runtime-paas"},
			{id: "service-catalog"},
			{id: "storage-data-policy"},
		]
		supportedSiteKinds: ["home", "cloud"]
		realization: {
			kind: "modules"
			moduleRefs: {
				required: ["stackkits-immich-runtime"]
				optional: []
			}
		}
		evidence: ["SK-S1", "SK-S2", "SK-S4"]
	},
],
	_architectureV2ProfileExtensionProviders,
	[for haRealization in _architectureV2HARealizations {
		metadata: {id: haRealization.providerID, version: "1.0.0"}
		provides: _architectureV2HACapabilities
		requires: [{id: "topology-core"}]
		supportedSiteKinds: haRealization.supportedSiteKinds
		realization: {kind: "modules", moduleRefs: {required: [haRealization.moduleID], optional: []}}
		health: [{id: haRealization.healthID, kind: "contract"}]
		evidence: [haRealization.evidenceRef]
	}],
])

_architectureV2UmbrellaSupport: #ModuleRealizationSupportV2 & {
	contractVersion: "1.0.0"
	scope:           "umbrella"
	level:           "contract-only"
	compatibleRendererRefs: []
	inputs: {
		contractComplete: false
		requiredRefs: []
	}
	artifacts: requiredRefs: []
	evidence: requiredRefs: []
}

// The existing v1 Immich module is enough to bind service identity, locality,
// backend protocol, health, and data ownership, but it has not yet graduated
// to an Architecture v2 renderer/apply implementation. Keeping this concrete
// contract at contract-only prevents the plan from claiming runtime readiness.
_architectureV2ImmichSupport: #ModuleRealizationSupportV2 & {
	contractVersion: "1.0.0"
	scope:           "concrete"
	level:           "contract-only"
	compatibleRendererRefs: ["stackkit"]
	inputs: {
		contractComplete: false
		requiredRefs: []
	}
	artifacts: {
		requiredRefs: ["immich-service-contract"]
		outputBindings: [{
			artifactRef: "immich-service-contract"
			unitRef:     "immich-server"
			outputRef:   "modules/immich/service-contract.yaml"
		}]
		contracts: [{
			id:       "immich-service-contract"
			kind:     "native-config"
			format:   "yaml"
			mode:     "0640"
			required: true
			compatibleTargets: ["compose", "opentofu"]
			unitRef:   "immich-server"
			outputRef: "modules/immich/service-contract.yaml"
		}]
	}
	evidence: requiredRefs: []
}

_architectureV2Modules: list.Concat([[
	{
		metadata: {
			id:          "stackkits-core-runtime"
			version:     "1.0.0"
			description: "Shared topology, bootstrap, security, identity, storage, backup, observability, and lifecycle runtime contract."
		}
		providerRef: "stackkits-core"
		provides:    _architectureV2CoreCapabilities
		supportedSiteKinds: ["home", "cloud"]
		runtime: {kind: "host", delivery: "stackkit"}
		renderUnits: []
		realizationSupport: _architectureV2UmbrellaSupport
		health: [{id: "stackkits-core-contract", kind: "contract"}]
		evidence: ["resolved-plan-contract"]
	},
	{
		metadata: {
			id:          "stackkits-local-runtime"
			version:     "1.0.0"
			description: "Home-site provisioning, local ingress, enrollment, autonomy, and backup runtime contract."
		}
		providerRef: "stackkits-local"
		provides:    _architectureV2LocalCapabilities
		requires: ["stackkits-core-runtime"]
		supportedSiteKinds: ["home"]
		runtime: {kind: "host", delivery: "stackkit"}
		renderUnits: []
		realizationSupport: _architectureV2UmbrellaSupport
		health: [{id: "stackkits-local-contract", kind: "contract"}]
		evidence: ["SK-S1"]
	},
	{
		metadata: {
			id:          "stackkits-cloud-runtime"
			version:     "1.0.0"
			description: "VPS provisioning, public edge, DNS, TLS, hardening, and provider lifecycle runtime contract."
		}
		providerRef: "stackkits-cloud"
		provides:    _architectureV2CloudCapabilities
		requires: ["stackkits-core-runtime"]
		supportedSiteKinds: ["cloud"]
		runtime: {kind: "host", delivery: "stackkit"}
		renderUnits: []
		realizationSupport: _architectureV2UmbrellaSupport
		health: [{id: "stackkits-cloud-contract", kind: "contract"}]
		evidence: ["SK-S2", "SK-S3"]
	},
	{
		metadata: {
			id:          "stackkits-immich-runtime"
			version:     "1.0.0"
			description: "Immich photo service contract bound to the control-authority site and its personal-data primary."
		}
		providerRef: "stackkits-immich"
		provides: ["photos"]
		requires: ["stackkits-core-runtime"]
		supportedSiteKinds: ["home", "cloud"]
		nodeSelection: {
			authority: "control-authority-site"
			requiredRoles: ["worker"]
		}
		runtime: {
			kind:     "container"
			delivery: "selected-paas"
			engine:   "docker"
			image: ref: "ghcr.io/immich-app/immich-server:release"
		}
		renderUnits: [{
			id:           "immich-server"
			kind:         "native-config"
			rendererRef:  "stackkit"
			templateRef:  "modules/immich/module.cue"
			version:      "1.0.0"
			contractHash: "sha256:6a3dffc4782bb3c19bf2a61aff3b99b12d6f7f3e9c3f1ee90de41296ddfac305"
			publicInputRefs: []
			secretInputRefs: []
			outputs: ["modules/immich/service-contract.yaml"]
			placement: {
				scope:       "node-local"
				cardinality: "one-per-node"
			}
			serviceEndpoints: [{
				serviceRef:       "photos"
				upstreamProtocol: "http"
				targetPort:       2283
				allowedIngressProtocols: ["http", "https"]
				allowedExposures: ["local", "remote-private", "public"]
				originSelector: "control-authority-site"
				healthRef:      "immich-http"
				data: {
					bindingRef: "photos"
					requiredClasses: ["personal"]
					locality: "primary-site"
				}
			}]
		}]
		realizationSupport: _architectureV2ImmichSupport
		health: [{
			id:             "immich-http"
			phase:          "continuous"
			kind:           "http"
			path:           "/api/server/ping"
			port:           2283
			timeoutSeconds: 10
			expectedStatuses: [200]
		}]
		evidence: ["SK-S1", "SK-S2", "SK-S4"]
	},
],
	_architectureV2ProfileExtensionModules,
	[for haRealization in _architectureV2HARealizations {
		metadata: {
			id:          haRealization.moduleID
			version:     "1.0.0"
			description: "Kit and mode specific high-availability realization bound exclusively to explicit control-plane members."
		}
		providerRef: haRealization.providerID
		provides:    _architectureV2HACapabilities
		requires: ["stackkits-core-runtime"]
		supportedSiteKinds: haRealization.supportedSiteKinds
		nodeSelection: {
			authority:           haRealization.authoritySelection
			controlPlaneMembers: "only"
		}
		runtime: {kind: "host", delivery: "stackkit"}
		renderUnits: []
		realizationSupport: _architectureV2UmbrellaSupport
		health: [{id: haRealization.healthID, kind: "contract"}]
		evidence: [haRealization.evidenceRef]
	}],
])

_architectureV2AddOns: [{
	metadata: {
		id:          "ha"
		version:     "1.0.0"
		description: "High availability policy and topology overlay; never a standalone kit."
	}
	supportedKits: [for profile in ArchitectureV2AuthorityProfiles {profile.slug}]
	provides: ["availability-ha"]
	requires: [{id: "topology-core"}]
	availability: {
		policyAuthority: "kit-definition"
		supportedModes: ["warm-standby", "quorum"]
		selector: "control-plane-members"
	}
}]

ArchitectureV2Catalog: #ArchitectureV2CatalogContract & {
	capabilities: [for contract in _architectureV2Capabilities {#CapabilityContract & contract}]
	providers: [for contract in _architectureV2Providers {#CapabilityProvider & contract}]
	addons: [for contract in _architectureV2AddOns {#AddOnContract & contract}]
	modules: [for contract in _architectureV2Modules {#ModuleContractV2 & contract}]
	privilegedInterfaceApprovals: _architectureV2ProfileExtensionPrivilegedInterfaceApprovals
	planArtifacts:                _architectureV2PlanArtifacts

	_capabilityIDsUnique: list.UniqueItems([for contract in capabilities {contract.metadata.id}]) & true
	_providerIDsUnique: list.UniqueItems([for contract in providers {contract.metadata.id}]) & true
	_addOnIDsUnique: list.UniqueItems([for contract in addons {contract.metadata.id}]) & true
}
