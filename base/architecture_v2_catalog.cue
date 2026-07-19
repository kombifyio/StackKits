// Package base - governed Architecture v2 capability, implementation-adapter,
// and add-on contracts. The established CapabilityProvider/providerRef wire
// names describe StackKits adapters (PaaS, mesh, renderer, host-local modules),
// never server providers. Kit definitions declare what must exist; this catalog
// declares which immutable implementation contracts may realize it.
package base

import "list"

_architectureV2CoreCapabilities: [
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

_architectureV2HostAdmissionCapabilities: [
	"external-host-admission",
	"host-conformance",
]

_architectureV2IdentityCapabilities: [
	"human-identity-core",
	"device-trust-core",
]

_architectureV2CoreModuleCapabilities: [
	for capabilityID in _architectureV2CoreCapabilities
	if !list.Contains(list.Concat([_architectureV2HostAdmissionCapabilities, _architectureV2IdentityCapabilities]), capabilityID) {capabilityID},
]

// Concrete Foundation modules take ownership away from the residual umbrella
// one capability at a time. The umbrella remains useful for shadow planning,
// but it must never duplicate a capability already owned by an executable
// module contract.
_architectureV2CoreUmbrellaCapabilities: [
	for capabilityID in _architectureV2CoreModuleCapabilities
	if capabilityID != "security-baseline" {capabilityID},
]

_architectureV2LocalCapabilities: [
	"site-local",
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

_architectureV2LocalAutonomyCapabilities: ["offline-autonomy"]

_architectureV2HomeAccessCapabilities: ["local-ingress", "lan-access-policy"]

_architectureV2HomeLANDiscoveryCapabilities: ["lan-discovery"]

_architectureV2HomeIdentityAuthorityCapabilities: [
	"device-enrollment-home",
	"local-control-authority",
]

_architectureV2LocalUmbrellaCapabilities: [
	for capabilityID in _architectureV2LocalCapabilities
	if !list.Contains(list.Concat([_architectureV2LocalAutonomyCapabilities, _architectureV2HomeAccessCapabilities, _architectureV2HomeLANDiscoveryCapabilities, _architectureV2HomeIdentityAuthorityCapabilities]), capabilityID) {capabilityID},
]

// The first product Compose lowering lane is Basement-owned rather than a
// generic home-site behavior. Modern Homelab also has home sites, so placing
// this capability on stackkits-local would incorrectly select Basement
// rollout machinery into the hybrid kit.
_architectureV2BasementComposeCapabilities: ["basement-compose-runtime"]

_architectureV2CloudCapabilities: [
	"site-cloud",
	"host-local-internet-firewall",
	"public-edge",
	"public-dns",
	"public-tls",
	"internet-host-hardening",
	"remote-owner-bootstrap",
	"offsite-object-backup",
	"cloud-control-authority",
	"private-admin-mesh",
	"failure-domain-placement",
]

_architectureV2CloudIdentityAuthorityCapabilities: [
	"remote-owner-bootstrap",
	"cloud-control-authority",
]

_architectureV2CloudUmbrellaCapabilities: [
	for capabilityID in _architectureV2CloudCapabilities
	if !list.Contains(_architectureV2CloudIdentityAuthorityCapabilities, capabilityID) {capabilityID},
]

// Public edge, DNS, and TLS capabilities above are desired host/service
// behavior. They never authorize a server-provider or DNS-provider mutation;
// an external platform adapter owns any such realization.
//
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
// a distinct governed capability-adapter/module realization. The KitDefinition owns the
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
		evidenceRef:                             "ha-cloud-warm-standby-failure-domain-proof"
	},
	{
		providerID: "stackkits-ha-cloud-quorum", moduleID: "stackkits-ha-cloud-quorum-runtime"
		supportedSiteKinds: ["cloud"], healthID: "ha-cloud-quorum-contract"
		authoritySelection:                      "any"
		evidenceRef:                             "ha-cloud-quorum-failure-domain-majority-proof"
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
	[for capabilityID in _architectureV2BasementComposeCapabilities {
		metadata: {
			id:          capabilityID
			version:     "1.0.0"
			description: "Basement-owned Compose rollout contract; it is not inferred from a home site or legacy context."
			layer:       "platform"
		}
		requires: [{id: "site-local"}]
		supportedSiteKinds: ["home"]
		evidence: ["basement-compose-contract-governance"]
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
		provides: _architectureV2CoreModuleCapabilities
		supportedSiteKinds: ["home", "cloud"]
		realization: {
			kind: "modules"
			moduleRefs: {
				required: ["stackkits-core-runtime", "security-baseline"]
				optional: []
			}
		}
		selection: defaultForSiteKinds: ["home", "cloud"]
		health: [{id: "stackkits-core-contract", kind: "contract"}]
		evidence: ["resolved-plan-contract"]
	},
	{
		metadata: {id: "stackkits-host-admission", version: "1.0.0"}
		provides: _architectureV2HostAdmissionCapabilities
		requires: [{id: "topology-core"}]
		supportedSiteKinds: ["home", "cloud"]
		realization: {
			kind:               "host"
			ownerRef:           "stackkits-host-admission"
			realizationSupport: _architectureV2HostAdmissionSupport
			inputBindings: {}
		}
		selection: defaultForSiteKinds: ["home", "cloud"]
		health: [{id: "stackkits-host-admission-contract", kind: "contract"}]
		evidence: ["host-conformance-receipt-contract"]
	},
	{
		metadata: {id: "stackkits-home-device-authority", version: "1.0.0"}
		provides: _architectureV2HomeIdentityAuthorityCapabilities
		requires: [{id: "topology-core"}, {id: "device-trust-core"}]
		supportedSiteKinds: ["home"]
		realization: {kind: "modules", moduleRefs: {required: ["stackkits-home-device-authority-policy-manifest"], optional: []}}
		selection: defaultForSiteKinds: ["home"]
		health: [{id: "home-device-authority-policy-contract", kind: "contract"}]
		evidence: ["home-device-authority-policy-contract"]
	},
	{
		metadata: {id: "stackkits-basement-identity-trust-policy", version: "1.0.0"}
		provides: _architectureV2IdentityCapabilities
		requires: [{id: "topology-core"}, {id: "device-enrollment-home"}, {id: "local-control-authority"}]
		supportedSiteKinds: ["home"]
		realization: {kind: "modules", moduleRefs: {required: ["stackkits-basement-identity-trust-policy-manifest"], optional: []}}
		selection: defaultForSiteKinds: ["home"]
		health: [{id: "basement-identity-trust-policy-contract", kind: "contract"}]
		evidence: ["basement-identity-trust-policy-contract"]
	},
	{
		metadata: {id: "stackkits-cloud-identity-trust-policy", version: "1.0.0"}
		provides: list.Concat([_architectureV2IdentityCapabilities, _architectureV2CloudIdentityAuthorityCapabilities])
		requires: [{id: "topology-core"}]
		supportedSiteKinds: ["cloud"]
		realization: {kind: "modules", moduleRefs: {required: ["stackkits-cloud-identity-trust-policy-manifest"], optional: []}}
		selection: defaultForSiteKinds: ["cloud"]
		health: [{id: "cloud-identity-trust-policy-contract", kind: "contract"}]
		evidence: ["cloud-identity-trust-policy-contract"]
	},
	{
		metadata: {id: "stackkits-local", version: "1.0.0"}
		provides: _architectureV2LocalUmbrellaCapabilities
		requires: [{id: "topology-core"}]
		supportedSiteKinds: ["home"]
		realization: {kind: "modules", moduleRefs: {required: ["stackkits-local-runtime"], optional: []}}
		selection: defaultForSiteKinds: ["home"]
		health: [{id: "stackkits-local-contract", kind: "contract"}]
		evidence: ["SK-S1"]
	},
	{
		metadata: {id: "stackkits-local-autonomy-policy", version: "1.0.0"}
		provides: _architectureV2LocalAutonomyCapabilities
		requires: [{id: "site-local"}, {id: "local-control-authority"}, {id: "device-enrollment-home"}]
		supportedSiteKinds: ["home"]
		realization: {kind: "modules", moduleRefs: {required: ["stackkits-local-autonomy-policy-manifest"], optional: []}}
		selection: defaultForSiteKinds: ["home"]
		health: [{id: "stackkits-local-autonomy-contract", kind: "contract"}]
		evidence: ["local-autonomy-policy-contract"]
	},
	{
		metadata: {id: "stackkits-home-access-policy", version: "1.0.0"}
		provides: _architectureV2HomeAccessCapabilities
		requires: [{id: "site-local"}, {id: "device-enrollment-home"}, {id: "device-trust-core"}, {id: "access-policy"}]
		supportedSiteKinds: ["home"]
		realization: {kind: "modules", moduleRefs: {required: ["stackkits-home-access-policy-manifest"], optional: []}}
		selection: defaultForSiteKinds: ["home"]
		health: [{id: "stackkits-home-access-policy-contract", kind: "contract"}]
		evidence: ["home-access-policy-contract"]
	},
	{
		metadata: {id: "stackkits-home-lan-discovery-policy", version: "1.0.0"}
		provides: _architectureV2HomeLANDiscoveryCapabilities
		requires: [{id: "site-local"}, {id: "local-ingress"}, {id: "lan-access-policy"}]
		supportedSiteKinds: ["home"]
		realization: {kind: "modules", moduleRefs: {required: ["stackkits-home-lan-discovery-policy-manifest"], optional: []}}
		selection: defaultForSiteKinds: ["home"]
		health: [{id: "stackkits-home-lan-discovery-policy-contract", kind: "contract"}]
		evidence: ["home-lan-discovery-policy-contract"]
	},
	{
		metadata: {id: "stackkits-cloud", version: "1.0.0"}
		provides: _architectureV2CloudUmbrellaCapabilities
		requires: [{id: "topology-core"}]
		supportedSiteKinds: ["cloud"]
		realization: {kind: "modules", moduleRefs: {required: ["stackkits-cloud-runtime"], optional: []}}
		selection: defaultForSiteKinds: ["cloud"]
		health: [{id: "stackkits-cloud-contract", kind: "contract"}]
		evidence: ["SK-S2", "SK-S3"]
	},
	{
		metadata: {id: "stackkits-basement-compose", version: "1.0.0"}
		provides: _architectureV2BasementComposeCapabilities
		requires: [{id: "topology-core"}, {id: "site-local"}]
		supportedSiteKinds: ["home"]
		realization: {
			kind: "modules"
			moduleRefs: {
				required: ["stackkits-basement-compose-runtime", "socket-proxy"]
				optional: []
			}
		}
		health: [{id: "stackkits-basement-compose-contract", kind: "contract"}]
		evidence: ["basement-compose-contract-governance"]
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

// Host admission and conformance are pre-generation runtime gates, not
// generated files. Their concrete implementation is the binding/receipt
// producer and admission path; modeling them as a host owner keeps them out of
// the residual Core module without fabricating a render unit or artifact.
_architectureV2HostAdmissionSupport: #NonRenderingProviderOwnerRealizationSupportV2 & {
	contractVersion: "1.0.0"
	scope:           "concrete"
	level:           "apply-ready"
	compatibleRendererRefs: []
	inputs: {
		contractComplete: true
		requiredRefs: []
	}
	artifacts: requiredRefs: []
	evidence: requiredRefs: ["host-conformance-receipt-contract"]
}

_architectureV2SecurityBaselineSupport: #ModuleRealizationSupportV2 & {
	contractVersion: "1.0.0"
	scope:           "concrete"
	level:           "generation-ready"
	compatibleRendererRefs: ["stackkit"]
	inputs: {
		contractComplete: true
		requiredRefs: []
	}
	artifacts: {
		requiredRefs: ["security-baseline-host-policy"]
		outputBindings: [{
			artifactRef: "security-baseline-host-policy"
			unitRef:     "host-policy"
			outputRef:   "foundation/security-baseline/apply.sh"
		}]
		contracts: [{
			id:       "security-baseline-host-policy"
			kind:     "script"
			format:   "shell"
			mode:     "0700"
			required: true
			compatibleTargets: ["compose", "opentofu"]
			unitRef:   "host-policy"
			outputRef: "foundation/security-baseline/apply.sh"
		}]
	}
	evidence: requiredRefs: []
}

_architectureV2LocalAutonomySupport: #ModuleRealizationSupportV2 & {
	contractVersion: "1.0.0"
	scope:           "concrete"
	level:           "generation-ready"
	compatibleRendererRefs: ["stackkit"]
	inputs: {
		contractComplete: true
		requiredRefs: []
	}
	planInputs: {
		contractComplete: true
		requiredRefs: ["stackId", "kit", "sites", "controlPlane", "identity", "data", "failurePolicy"]
	}
	artifacts: {
		requiredRefs: ["local-autonomy-policy"]
		outputBindings: [{
			artifactRef: "local-autonomy-policy"
			unitRef:     "policy-bundle"
			outputRef:   "local/autonomy/policy.json"
		}]
		contracts: [{
			id:       "local-autonomy-policy"
			kind:     "native-config"
			format:   "json"
			mode:     "0640"
			required: true
			compatibleTargets: ["compose", "opentofu"]
			unitRef:   "policy-bundle"
			outputRef: "local/autonomy/policy.json"
		}]
	}
	evidence: requiredRefs: []
}

_architectureV2HomeAccessSupport: #ModuleRealizationSupportV2 & {
	contractVersion: "1.0.0"
	scope:           "concrete"
	level:           "generation-ready"
	compatibleRendererRefs: ["stackkit"]
	inputs: {
		contractComplete: true
		requiredRefs: []
	}
	planInputs: {
		contractComplete: true
		requiredRefs: ["stackId", "kit", "sites", "identity", "localReachability"]
	}
	artifacts: {
		requiredRefs: ["home-access-policy"]
		outputBindings: [{
			artifactRef: "home-access-policy"
			unitRef:     "policy-bundle"
			outputRef:   "local/network/access-policy.json"
		}]
		contracts: [{
			id:       "home-access-policy"
			kind:     "native-config"
			format:   "json"
			mode:     "0640"
			required: true
			compatibleTargets: ["compose", "opentofu"]
			unitRef:   "policy-bundle"
			outputRef: "local/network/access-policy.json"
		}]
	}
	evidence: requiredRefs: []
}

_architectureV2HomeLANDiscoverySupport: #ModuleRealizationSupportV2 & {
	contractVersion: "1.0.0"
	scope:           "concrete"
	level:           "generation-ready"
	compatibleRendererRefs: ["stackkit"]
	inputs: {
		contractComplete: true
		requiredRefs: []
	}
	planInputs: {
		contractComplete: true
		requiredRefs: ["stackId", "kit", "sites", "homeLANDiscovery"]
	}
	artifacts: {
		requiredRefs: ["home-lan-discovery-policy"]
		outputBindings: [{
			artifactRef: "home-lan-discovery-policy"
			unitRef:     "policy-bundle"
			outputRef:   "local/network/discovery-policy.json"
		}]
		contracts: [{
			id:       "home-lan-discovery-policy"
			kind:     "native-config"
			format:   "json"
			mode:     "0640"
			required: true
			compatibleTargets: ["compose", "opentofu"]
			unitRef:   "policy-bundle"
			outputRef: "local/network/discovery-policy.json"
		}]
	}
	evidence: requiredRefs: []
}

_architectureV2HomeDeviceAuthoritySupport: #ModuleRealizationSupportV2 & {
	contractVersion: "1.0.0"
	scope:           "concrete"
	level:           "generation-ready"
	compatibleRendererRefs: ["stackkit"]
	inputs: {contractComplete: true, requiredRefs: []}
	planInputs: {
		contractComplete: true
		requiredRefs: ["stackId", "kit", "sites", "identityTrust"]
	}
	artifacts: {
		requiredRefs: ["home-device-authority-policy"]
		outputBindings: [{
			artifactRef: "home-device-authority-policy"
			unitRef:     "policy-bundle"
			outputRef:   "local/identity/device-authority-policy.json"
		}]
		contracts: [{
			id: "home-device-authority-policy", kind: "native-config", format: "json", mode: "0640", required: true
			compatibleTargets: ["compose", "opentofu"]
			unitRef: "policy-bundle", outputRef: "local/identity/device-authority-policy.json"
		}]
	}
	evidence: requiredRefs: ["home-device-authority-runtime-proof"]
}

_architectureV2BasementIdentityTrustSupport: #ModuleRealizationSupportV2 & {
	contractVersion: "1.0.0"
	scope:           "concrete"
	level:           "generation-ready"
	compatibleRendererRefs: ["stackkit"]
	inputs: {contractComplete: true, requiredRefs: []}
	planInputs: {
		contractComplete: true
		requiredRefs: ["stackId", "kit", "sites", "identityTrust"]
	}
	artifacts: {
		requiredRefs: ["basement-identity-trust-policy"]
		outputBindings: [{
			artifactRef: "basement-identity-trust-policy"
			unitRef:     "policy-bundle"
			outputRef:   "local/identity/trust-policy.json"
		}]
		contracts: [{
			id: "basement-identity-trust-policy", kind: "native-config", format: "json", mode: "0640", required: true
			compatibleTargets: ["compose", "opentofu"]
			unitRef: "policy-bundle", outputRef: "local/identity/trust-policy.json"
		}]
	}
	evidence: requiredRefs: ["basement-identity-trust-runtime-proof"]
}

_architectureV2CloudIdentityTrustSupport: #ModuleRealizationSupportV2 & {
	contractVersion: "1.0.0"
	scope:           "concrete"
	level:           "generation-ready"
	compatibleRendererRefs: ["stackkit"]
	inputs: {contractComplete: true, requiredRefs: []}
	planInputs: {
		contractComplete: true
		requiredRefs: ["stackId", "kit", "sites", "identityTrust"]
	}
	artifacts: {
		requiredRefs: ["cloud-identity-trust-policy"]
		outputBindings: [{
			artifactRef: "cloud-identity-trust-policy"
			unitRef:     "policy-bundle"
			outputRef:   "cloud/identity/trust-policy.json"
		}]
		contracts: [{
			id: "cloud-identity-trust-policy", kind: "native-config", format: "json", mode: "0640", required: true
			compatibleTargets: ["compose", "opentofu"]
			unitRef: "policy-bundle", outputRef: "cloud/identity/trust-policy.json"
		}]
	}
	evidence: requiredRefs: ["cloud-identity-verifier-runtime-proof"]
}

_architectureV2SocketProxySupport: #ModuleRealizationSupportV2 & {
	contractVersion: "1.0.0"
	scope:           "concrete"
	level:           "generation-ready"
	compatibleRendererRefs: ["stackkit"]
	inputs: {
		contractComplete: true
		requiredRefs: []
	}
	artifacts: {
		requiredRefs: ["socket-proxy-compose"]
		outputBindings: [{
			artifactRef: "socket-proxy-compose"
			unitRef:     "compose"
			outputRef:   "foundation/socket-proxy/compose.yaml"
		}]
		contracts: [{
			id:       "socket-proxy-compose"
			kind:     "compose"
			format:   "yaml"
			mode:     "0640"
			required: true
			compatibleTargets: ["compose"]
			unitRef:   "compose"
			outputRef: "foundation/socket-proxy/compose.yaml"
		}]
	}
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
			description: "Residual shared topology, bootstrap, storage, backup, observability, and lifecycle runtime contract; identity ownership is kit-specific."
		}
		providerRef: "stackkits-core"
		provides:    _architectureV2CoreUmbrellaCapabilities
		supportedSiteKinds: ["home", "cloud"]
		runtime: {kind: "host", delivery: "stackkit"}
		renderUnits: []
		realizationSupport: _architectureV2UmbrellaSupport
		health: [{id: "stackkits-core-contract", kind: "contract"}]
		evidence: ["resolved-plan-contract"]
	},
	{
		metadata: {
			id:          "security-baseline"
			version:     "1.0.0"
			description: "Deterministic target-neutral OS-hardening policy generated once for every managed StackKit node; access and network controls remain delegated."
		}
		providerRef: "stackkits-core"
		provides: ["security-baseline"]
		requires: ["stackkits-core-runtime"]
		supportedSiteKinds: ["home", "cloud"]
		nodeSelection: {
			authority:           "any"
			controlPlaneMembers: "any"
		}
		runtime: {kind: "host", delivery: "stackkit"}
		renderUnits: [{
			id:           "host-policy"
			kind:         "host"
			rendererRef:  "stackkit"
			templateRef:  "builtin://foundation/security-baseline/apply.sh"
			version:      "1.0.0"
			contractHash: "sha256:0ac2e74a7bc0ed9cff5a1f00d9a6e0b913d75500a00f824562e34438dbe54eab"
			publicInputRefs: []
			secretInputRefs: []
			outputs: ["foundation/security-baseline/apply.sh"]
			placement: {
				scope:       "node-local"
				cardinality: "one-per-node"
			}
		}]
		realizationSupport: _architectureV2SecurityBaselineSupport
		health: [{id: "security-baseline-contract", kind: "contract"}]
		evidence: ["resolved-plan-contract"]
	},
	{
		metadata: {
			id:          "stackkits-local-runtime"
			version:     "1.0.0"
			description: "Residual Home-site admission, local ingress, autonomy, and backup runtime contract for an already supplied host; device authority is separately owned."
		}
		providerRef: "stackkits-local"
		provides:    _architectureV2LocalUmbrellaCapabilities
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
			id:          "stackkits-home-device-authority-policy-manifest"
			version:     "1.0.0"
			description: "Generation-only Home enrollment and credential-authority policy; credential material, endpoints, and runtime enforcement are excluded."
		}
		providerRef: "stackkits-home-device-authority"
		provides:    _architectureV2HomeIdentityAuthorityCapabilities
		requires: ["stackkits-local-runtime"]
		supportedSiteKinds: ["home"]
		runtime: {kind: "native", delivery: "stackkit"}
		renderUnits: [{
			id:           "policy-bundle", kind:                                     "native-config", rendererRef: "stackkit"
			templateRef:  "builtin://home/device-authority-policy/v1.json", version: "1.0.0"
			contractHash: "sha256:1da194be46fdd36215a3cc9a3f877a100a6fee92d5520a1522885f33798488a7"
			publicInputRefs: [], secretInputRefs: []
			planInputRefs: ["stackId", "kit", "sites", "identityTrust"]
			outputs: ["local/identity/device-authority-policy.json"]
			placement: {scope: "module", cardinality: "single"}
		}]
		realizationSupport: _architectureV2HomeDeviceAuthoritySupport
		health: [{id: "home-device-authority-policy-contract", kind: "contract"}]
		evidence: ["home-device-authority-policy-contract"]
	},
	{
		metadata: {
			id:          "stackkits-basement-identity-trust-policy-manifest"
			version:     "1.0.0"
			description: "Generation-only Basement identity verifier and trust policy; runtime enforcement and credential material are excluded."
		}
		providerRef: "stackkits-basement-identity-trust-policy"
		provides:    _architectureV2IdentityCapabilities
		requires: ["stackkits-core-runtime", "stackkits-home-device-authority-policy-manifest"]
		supportedSiteKinds: ["home"]
		runtime: {kind: "native", delivery: "stackkit"}
		renderUnits: [{
			id:           "policy-bundle", kind:                                       "native-config", rendererRef: "stackkit"
			templateRef:  "builtin://basement/identity-trust-policy/v1.json", version: "1.0.0"
			contractHash: "sha256:b377cd7476396aa820f2cd0002b8a526fbef3c9943ee14dcb69aec67b1578115"
			publicInputRefs: [], secretInputRefs: []
			planInputRefs: ["stackId", "kit", "sites", "identityTrust"]
			outputs: ["local/identity/trust-policy.json"]
			placement: {scope: "module", cardinality: "single"}
		}]
		realizationSupport: _architectureV2BasementIdentityTrustSupport
		health: [{id: "basement-identity-trust-policy-contract", kind: "contract"}]
		evidence: ["basement-identity-trust-policy-contract"]
	},
	{
		metadata: {
			id:          "stackkits-local-autonomy-policy-manifest"
			version:     "1.0.0"
			description: "Generation-only Home-site offline-autonomy policy; runtime and air-gapped installation enforcement remain separate claims."
		}
		providerRef: "stackkits-local-autonomy-policy"
		provides:    _architectureV2LocalAutonomyCapabilities
		requires: ["stackkits-local-runtime"]
		supportedSiteKinds: ["home"]
		runtime: {kind: "native", delivery: "stackkit"}
		renderUnits: [{
			id:           "policy-bundle"
			kind:         "native-config"
			rendererRef:  "stackkit"
			templateRef:  "builtin://home/local-autonomy/v1.json"
			version:      "1.0.0"
			contractHash: "sha256:86071b1b898dffdcf2330ea7376ae5e7178a7a54bb42d7278475d9f56d033dee"
			publicInputRefs: []
			secretInputRefs: []
			planInputRefs: ["stackId", "kit", "sites", "controlPlane", "identity", "data", "failurePolicy"]
			outputs: ["local/autonomy/policy.json"]
			placement: {
				scope:       "module"
				cardinality: "single"
			}
		}]
		realizationSupport: _architectureV2LocalAutonomySupport
		health: [{id: "local-autonomy-policy-contract", kind: "contract"}]
		evidence: ["local-autonomy-policy-contract"]
	},
	{
		metadata: {
			id:          "stackkits-home-access-policy-manifest"
			version:     "1.0.0"
			description: "Generation-only Home local-ingress and LAN access policy; discovery and runtime enforcement remain separate claims."
		}
		providerRef: "stackkits-home-access-policy"
		provides:    _architectureV2HomeAccessCapabilities
		requires: ["stackkits-local-runtime"]
		supportedSiteKinds: ["home"]
		runtime: {kind: "native", delivery: "stackkit"}
		renderUnits: [{
			id:           "policy-bundle"
			kind:         "native-config"
			rendererRef:  "stackkit"
			templateRef:  "builtin://home/access/v1.json"
			version:      "1.0.0"
			contractHash: "sha256:d194da4490b393393fb60327a3567583dee4a489a88cda7b3b029a06ea923577"
			publicInputRefs: []
			secretInputRefs: []
			planInputRefs: ["stackId", "kit", "sites", "identity", "localReachability"]
			outputs: ["local/network/access-policy.json"]
			placement: {
				scope:       "module"
				cardinality: "single"
			}
		}]
		realizationSupport: _architectureV2HomeAccessSupport
		health: [{id: "home-access-policy-contract", kind: "contract"}]
		evidence: ["home-access-policy-contract"]
	},
	{
		metadata: {
			id:          "stackkits-home-lan-discovery-policy-manifest"
			version:     "1.0.0"
			description: "Generation-only explicit Home LAN discovery policy; empty intent emits no advertisements and runtime publication remains separately verified."
		}
		providerRef: "stackkits-home-lan-discovery-policy"
		provides:    _architectureV2HomeLANDiscoveryCapabilities
		requires: ["stackkits-home-access-policy-manifest"]
		supportedSiteKinds: ["home"]
		runtime: {kind: "native", delivery: "stackkit"}
		renderUnits: [{
			id:           "policy-bundle"
			kind:         "native-config"
			rendererRef:  "stackkit"
			templateRef:  "builtin://home/lan-discovery/v1.json"
			version:      "1.0.0"
			contractHash: "sha256:48eb8152174d0a9de62e57b7eb3d93dad25ee36894611caf5005d05f452ec68d"
			publicInputRefs: []
			secretInputRefs: []
			planInputRefs: ["stackId", "kit", "sites", "homeLANDiscovery"]
			outputs: ["local/network/discovery-policy.json"]
			placement: {
				scope:       "module"
				cardinality: "single"
			}
		}]
		realizationSupport: _architectureV2HomeLANDiscoverySupport
		health: [{id: "home-lan-discovery-policy-contract", kind: "contract"}]
		evidence: ["home-lan-discovery-policy-contract"]
	},
	{
		metadata: {
			id:          "stackkits-cloud-runtime"
			version:     "1.0.0"
			description: "Residual Cloud-host admission, host-local firewall/hardening, and public edge/DNS/TLS intent for an already supplied host; identity authority is separately owned."
		}
		providerRef: "stackkits-cloud"
		provides:    _architectureV2CloudUmbrellaCapabilities
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
			id:          "stackkits-cloud-identity-trust-policy-manifest"
			version:     "1.0.0"
			description: "Generation-only Cloud identity trust policy with external device authority and Cloud-local verification; no device issuance or enrollment is owned."
		}
		providerRef: "stackkits-cloud-identity-trust-policy"
		provides: list.Concat([_architectureV2IdentityCapabilities, _architectureV2CloudIdentityAuthorityCapabilities])
		requires: ["stackkits-core-runtime", "stackkits-cloud-runtime"]
		supportedSiteKinds: ["cloud"]
		runtime: {kind: "native", delivery: "stackkit"}
		renderUnits: [{
			id:           "policy-bundle", kind:                                    "native-config", rendererRef: "stackkit"
			templateRef:  "builtin://cloud/identity-trust-policy/v1.json", version: "1.0.0"
			contractHash: "sha256:84b7e97941c6b36d24ee0a59c594cef45926e22dc20f7d16acbf6f8fa0c95ca4"
			publicInputRefs: [], secretInputRefs: []
			planInputRefs: ["stackId", "kit", "sites", "identityTrust"]
			outputs: ["cloud/identity/trust-policy.json"]
			placement: {scope: "module", cardinality: "single"}
		}]
		realizationSupport: _architectureV2CloudIdentityTrustSupport
		health: [{id: "cloud-identity-trust-policy-contract", kind: "contract"}]
		evidence: ["cloud-identity-trust-policy-contract"]
	},
	{
		metadata: {
			id:          "stackkits-basement-compose-runtime"
			version:     "1.0.0"
			description: "Residual Basement-only Compose rollout owner; concrete foundation helpers lower independently beneath it."
		}
		providerRef: "stackkits-basement-compose"
		provides:    _architectureV2BasementComposeCapabilities
		requires: ["stackkits-local-runtime"]
		supportedSiteKinds: ["home"]
		runtime: {kind: "host", delivery: "stackkit"}
		renderUnits: []
		realizationSupport: _architectureV2UmbrellaSupport
		health: [{id: "stackkits-basement-compose-contract", kind: "contract"}]
		evidence: ["basement-compose-contract-governance"]
	},
	{
		metadata: {
			id:          "socket-proxy"
			version:     "1.0.0"
			description: "Basement node-local Docker API isolation proxy backed by one explicitly approved daemon socket."
		}
		providerRef: "stackkits-basement-compose"
		// This helper provides an implementation interface, not a product
		// capability. The Basement Compose runtime owner above carries the
		// capability selection and readiness responsibility.
		provides: []
		requires: ["stackkits-basement-compose-runtime"]
		supportedSiteKinds: ["home"]
		nodeSelection: {
			authority:           "any"
			controlPlaneMembers: "any"
		}
		runtime: {
			kind:     "container"
			delivery: "stackkit"
			engine:   "docker"
			image: {
				ref:    "ghcr.io/tecnativa/docker-socket-proxy:v0.4.2"
				digest: "sha256:1f3a6f303320723d199d2316a3e82b2e2685d86c275d5e3deeaf182573b47476"
			}
		}
		renderUnits: [{
			id:           "compose"
			kind:         "compose"
			rendererRef:  "stackkit"
			templateRef:  "builtin://foundation/socket-proxy/compose.yaml"
			version:      "1.0.0"
			contractHash: "sha256:3ea559c5ba0c528ad9ae99341bc2380ca781012a863a2c389a1565b4f183bd9d"
			publicInputRefs: []
			secretInputRefs: []
			outputs: ["foundation/socket-proxy/compose.yaml"]
			placement: {
				scope:       "node-local"
				cardinality: "one-per-daemon"
				daemonRef:   "docker-default"
			}
			providesInterfaces: [{
				id:       "docker-api-readonly"
				kind:     "docker-http-readonly-v1"
				protocol: "docker-http"
				version:  "v1"
				endpoint: {
					ref:        "docker-api"
					visibility: "node-local"
					transport:  "tcp"
					networkRef: "docker-api-readonly"
					address:    "socket-proxy"
					port:       2375
				}
				scopes: ["CONTAINERS", "EVENTS", "NETWORKS", "PING", "VERSION"]
				coLocation:    "same-node-and-network"
				daemonRef:     "docker-default"
				policyProfile: "docker-readonly-baseline"
			}]
			requiresInterfaces: [{
				id:       "docker-provider-backing"
				kind:     "docker-socket-direct-v1"
				protocol: "docker-engine"
				version:  "v1"
				endpoint: {
					visibility: "node-local"
					transport:  "unix-socket"
					pathSource: "daemon-binding"
				}
				scopes: ["docker-api:full"]
				coLocation:    "same-node"
				daemonRef:     "docker-default"
				policyProfile: "docker-provider-backing"
			}]
		}]
		realizationSupport: _architectureV2SocketProxySupport
		health: [{id: "socket-proxy-contract", kind: "contract"}]
		evidence: ["socket-proxy-provider-backing-governance"]
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

_architectureV2PrivilegedInterfaceApprovals: list.Concat([[
	{
		id:            "approve-socket-proxy-backing"
		kind:          "docker-socket-direct-v1"
		moduleRef:     "socket-proxy"
		unitRef:       "compose"
		providerRef:   "stackkits-basement-compose"
		daemonRef:     "docker-default"
		policyProfile: "docker-provider-backing"
		reasonCode:    "provider-backing"
		evidenceRef:   "socket-proxy-provider-backing-governance"
	},
], _architectureV2ProfileExtensionPrivilegedInterfaceApprovals])

ArchitectureV2Catalog: #ArchitectureV2CatalogContract & {
	capabilities: [for contract in _architectureV2Capabilities {#CapabilityContract & contract}]
	providers: [for contract in _architectureV2Providers {#CapabilityProvider & contract}]
	addons: [for contract in _architectureV2AddOns {#AddOnContract & contract}]
	modules: [for contract in _architectureV2Modules {#ModuleContractV2 & contract}]
	privilegedInterfaceApprovals: [for contract in _architectureV2PrivilegedInterfaceApprovals {#PrivilegedInterfaceApprovalV2 & contract}]
	planArtifacts: _architectureV2PlanArtifacts

	_capabilityIDsUnique: list.UniqueItems([for contract in capabilities {contract.metadata.id}]) & true
	_providerIDsUnique: list.UniqueItems([for contract in providers {contract.metadata.id}]) & true
	_addOnIDsUnique: list.UniqueItems([for contract in addons {contract.metadata.id}]) & true
}
