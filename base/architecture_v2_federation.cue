// Package base extends the private Architecture v2 authority with the Modern
// Homelab federation realization. Public authority projections omit this file.
package base

import "list"

_architectureV2FederationRuntimeCapabilities: [
	"inter-site-link",
	"outbound-control-agent",
	"cross-site-backup",
	"bridge-observability",
]

_architectureV2FederationTopologyCapabilities: ["site-federation"]
_architectureV2ServicePublicationCapabilities: ["service-publication"]
_architectureV2CrossSitePlacementCapabilities: ["cross-site-placement"]
_architectureV2DataResidencyCapabilities: ["data-residency"]
_architectureV2SplitHorizonNamingCapabilities: ["split-horizon-naming"]

_architectureV2FederationDeclarativeCapabilities: list.Concat([
	_architectureV2FederationTopologyCapabilities,
	_architectureV2ServicePublicationCapabilities,
	_architectureV2CrossSitePlacementCapabilities,
	_architectureV2DataResidencyCapabilities,
	_architectureV2SplitHorizonNamingCapabilities,
])

_architectureV2FederationIdentityCapabilities: ["edge-identity-verifier"]

_architectureV2ModernIdentityCapabilities: list.Concat([
	_architectureV2IdentityCapabilities,
	_architectureV2FederationIdentityCapabilities,
])

_architectureV2FederationPolicyCapabilities: [
	"bridge-policy",
	"partition-failure-contract",
]

_architectureV2ProfileExtensionCapabilities: list.Concat([
	_architectureV2FederationRuntimeCapabilities,
	_architectureV2FederationDeclarativeCapabilities,
	_architectureV2FederationIdentityCapabilities,
	_architectureV2FederationPolicyCapabilities,
])

_architectureV2ProfileExtensionCapabilityContracts: list.Concat([
	[for capabilityID in list.Concat([_architectureV2FederationRuntimeCapabilities, _architectureV2FederationDeclarativeCapabilities]) {
		metadata: {
			id:          capabilityID
			version:     "1.0.0"
			description: "Hybrid federation Architecture v2 contract for \(capabilityID)."
			layer:       "platform"
		}
		supportedSiteKinds: ["home", "cloud"]
		evidence: ["SK-S4"]
	}],
	[for capabilityID in _architectureV2FederationPolicyCapabilities {
		metadata: {
			id:          capabilityID
			version:     "1.0.0"
			description: "Generation-only Modern federation policy contract for \(capabilityID); runtime enforcement is not claimed."
			layer:       "platform"
		}
		supportedSiteKinds: ["home", "cloud"]
		evidence: ["resolved-plan-contract"]
	}],
	[for capabilityID in _architectureV2FederationIdentityCapabilities {
		metadata: {
			id:          capabilityID
			version:     "1.0.0"
			description: "Verifier-only Cloud-side identity contract for Modern Homelab; authority and signing remain Home-owned."
			layer:       "foundation"
		}
		supportedSiteKinds: ["home", "cloud"]
		evidence: ["modern-identity-trust-policy-contract"]
	}],
])

_architectureV2ModernFederationPolicySupport: #ModuleRealizationSupportV2 & {
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
		requiredRefs: ["stackId", "kit", "sites", "controlPlane", "bridge", "identity", "data", "failurePolicy"]
	}
	artifacts: {
		requiredRefs: ["modern-federation-policy-manifest"]
		outputBindings: [{
			artifactRef: "modern-federation-policy-manifest"
			unitRef:     "policy-bundle"
			outputRef:   "modern/federation/policy.json"
		}]
		contracts: [{
			id:       "modern-federation-policy-manifest"
			kind:     "native-config"
			format:   "json"
			mode:     "0640"
			required: true
			compatibleTargets: ["compose", "opentofu"]
			unitRef:   "policy-bundle"
			outputRef: "modern/federation/policy.json"
		}]
	}
	evidence: requiredRefs: []
}

_architectureV2ModernHomeIdentityTrustSupport: #ModuleRealizationSupportV2 & {
	contractVersion: "1.0.0"
	scope:           "concrete"
	level:           "apply-ready"
	compatibleRendererRefs: ["stackkit"]
	inputs: {contractComplete: true, requiredRefs: ["modern-home-identity-authority"]}
	planInputs: {
		contractComplete: true
		requiredRefs: ["stackId", "kit"]
	}
	artifacts: {
		requiredRefs: ["modern-identity-trust-policy"]
		outputBindings: [{artifactRef: "modern-identity-trust-policy", unitRef: "policy-bundle", outputRef: "modern/identity/trust-policy.json"}]
		contracts: [{
			id: "modern-identity-trust-policy", kind: "native-config", format: "json", mode: "0640", required: true
			compatibleTargets: ["compose", "opentofu"]
			unitRef: "policy-bundle", outputRef: "modern/identity/trust-policy.json"
		}]
	}
	evidence: requiredRefs: ["modern-home-identity-trust-enforcement"]
}

_architectureV2ModernCloudIdentityVerifierSupport: #ModuleRealizationSupportV2 & {
	contractVersion: "1.0.0"
	scope:           "concrete"
	level:           "apply-ready"
	compatibleRendererRefs: ["stackkit"]
	inputs: {contractComplete: true, requiredRefs: ["modern-cloud-identity-verification"]}
	planInputs: {contractComplete: true, requiredRefs: ["stackId", "kit"]}
	artifacts: {
		requiredRefs: ["modern-identity-verifier-distribution-policy"]
		outputBindings: [{artifactRef: "modern-identity-verifier-distribution-policy", unitRef: "policy-bundle", outputRef: "modern/identity/verifier-distribution-policy.json"}]
		contracts: [{
			id: "modern-identity-verifier-distribution-policy", kind: "native-config", format: "json", mode: "0640", required: true
			compatibleTargets: ["compose", "opentofu"]
			unitRef: "policy-bundle", outputRef: "modern/identity/verifier-distribution-policy.json"
		}]
	}
	evidence: requiredRefs: ["modern-cloud-identity-verifier-enforcement"]
}

_architectureV2FederationRuntimeArtifacts: {
	link: {id: "federation-link-executor-contract", outputRef: "modern/federation/link/executor-contract.json"}
	controlAgent: {id: "federation-control-agent-executor-contract", outputRef: "modern/federation/control-agent/executor-contract.json"}
	backup: {id: "federation-backup-executor-contract", outputRef: "modern/federation/backup/executor-contract.json"}
	observability: {id: "federation-observability-executor-contract", outputRef: "modern/federation/observability/executor-contract.json"}
}

_architectureV2FederationRuntimeSupports: {
	for runtimeName, artifact in _architectureV2FederationRuntimeArtifacts {
		"\(runtimeName)": #ModuleRealizationSupportV2 & {
			contractVersion: "1.0.0"
			scope:           "concrete"
			level:           "generation-ready"
			compatibleRendererRefs: ["stackkit"]
			inputs: {contractComplete: true, requiredRefs: []}
			planInputs: {
				contractComplete: true
				if runtimeName == "link" {
					requiredRefs: ["stackId", "kit", "moduleTargets", "moduleCapabilities", "sites", "controlPlane", "bridge", "identity", "data", "failurePolicy", "federationLinkRequirements", "externalFederationLinkBindings"]
				}
				if runtimeName != "link" {
					requiredRefs: ["stackId", "kit", "moduleTargets", "moduleCapabilities", "sites", "controlPlane", "bridge", "identity", "data", "failurePolicy"]
				}
			}
			artifacts: {
				requiredRefs: [artifact.id]
				outputBindings: [{artifactRef: artifact.id, unitRef: "executor-contract", outputRef: artifact.outputRef}]
				contracts: [{
					id: artifact.id, kind: "native-config", format: "json", mode: "0640", required: true
					compatibleTargets: ["compose", "opentofu"]
					unitRef: "executor-contract", outputRef: artifact.outputRef
				}]
			}
			evidence: requiredRefs: []
		}
	}
}

_architectureV2ProfileExtensionProviders: [
	{
		metadata: {id: "stackkits-federation-topology-contract", version: "1.0.0"}
		provides: _architectureV2FederationTopologyCapabilities
		requires: [{id: "site-local"}, {id: "site-cloud"}]
		supportedSiteKinds: ["home", "cloud"]
		realization: {kind: "contract"}
	},
	{
		metadata: {id: "stackkits-service-publication-contract", version: "1.0.0"}
		provides: _architectureV2ServicePublicationCapabilities
		requires: [{id: "site-federation"}, {id: "public-edge"}]
		supportedSiteKinds: ["home", "cloud"]
		realization: {kind: "contract"}
	},
	{
		metadata: {id: "stackkits-cross-site-placement-policy", version: "1.0.0"}
		provides: _architectureV2CrossSitePlacementCapabilities
		requires: [{id: "site-federation"}, {id: "storage-data-policy"}]
		supportedSiteKinds: ["home", "cloud"]
		realization: {kind: "contract"}
	},
	{
		metadata: {id: "stackkits-data-residency-policy", version: "1.0.0"}
		provides: _architectureV2DataResidencyCapabilities
		requires: [{id: "site-federation"}, {id: "storage-data-policy"}]
		supportedSiteKinds: ["home", "cloud"]
		realization: {kind: "contract"}
	},
	{
		metadata: {id: "stackkits-split-horizon-naming-contract", version: "1.0.0"}
		provides: _architectureV2SplitHorizonNamingCapabilities
		requires: [{id: "site-federation"}, {id: "public-dns"}]
		supportedSiteKinds: ["home", "cloud"]
		realization: {kind: "contract"}
	},
	{
		metadata: {id: "stackkits-federation-link", version: "1.1.0"}
		provides: ["inter-site-link"]
		requires: [{id: "site-federation"}, {id: "bridge-policy"}]
		supportedSiteKinds: ["home", "cloud"]
		realization: {kind: "modules", moduleRefs: {required: ["stackkits-federation-link-runtime"], optional: []}}
		overlayContracts: [{
			id:                  "outbound-private-mesh"
			capabilityRef:       "inter-site-link"
			moduleRef:           "stackkits-federation-link-runtime"
			implementation:      "wireguard"
			initiation:          "local-outbound"
			outboundEstablished: true
			allowedTrafficModes: ["management-only", "policy-scoped"]
			advertiseDefaultRoute:   false
			advertisePrivateSubnets: false
			allowBroadRoutes:        false
		}]
		health: [{id: "federation-link-contract", kind: "contract"}]
		evidence: ["federation-link-contract"]
	},
	{
		metadata: {id: "stackkits-federation-control-agent", version: "1.1.0"}
		provides: ["outbound-control-agent"]
		requires: [{id: "site-federation"}, {id: "bridge-policy"}, {id: "edge-identity-verifier"}]
		supportedSiteKinds: ["home", "cloud"]
		realization: {kind: "modules", moduleRefs: {required: ["stackkits-federation-control-agent-runtime"], optional: []}}
		remoteActionContracts: [
			for actionID in ["plan", "verify"] {
				id:                             actionID
				capabilityRef:                  "outbound-control-agent"
				moduleRef:                      "stackkits-federation-control-agent-runtime"
				transport:                      "mtls-agent"
				issuerRef:                      "home-workload-issuer"
				audience:                       "stackkit-workload"
				maxTTLSeconds:                  60
				destructive:                    false
				approvalClass:                  "none"
				approvalReceiptRequired:        false
				capabilityScopedActions:        true
				requiresSignedActions:          true
				requiresNonce:                  true
				requiresIdempotencyKey:         true
				requiresResolvedPlanHash:       true
				replayProtection:               true
				requiresApprovalForDestructive: true
			},
			{
				id:                             "apply", capabilityRef:         "outbound-control-agent", moduleRef:      "stackkits-federation-control-agent-runtime"
				transport:                      "mtls-agent", issuerRef:        "home-workload-issuer", audience:         "stackkit-workload", maxTTLSeconds: 60
				destructive:                    false, approvalClass:           "owner-step-up", approvalReceiptRequired: true
				capabilityScopedActions:        true, requiresSignedActions:    true, requiresNonce:                      true
				requiresIdempotencyKey:         true, requiresResolvedPlanHash: true, replayProtection:                   true
				requiresApprovalForDestructive: true
			},
			{
				id:                             "destroy", capabilityRef:       "outbound-control-agent", moduleRef:      "stackkits-federation-control-agent-runtime"
				transport:                      "mtls-agent", issuerRef:        "home-workload-issuer", audience:         "stackkit-workload", maxTTLSeconds: 30
				destructive:                    true, approvalClass:            "owner-step-up", approvalReceiptRequired: true
				capabilityScopedActions:        true, requiresSignedActions:    true, requiresNonce:                      true
				requiresIdempotencyKey:         true, requiresResolvedPlanHash: true, replayProtection:                   true
				requiresApprovalForDestructive: true
			},
		]
		health: [{id: "federation-control-agent-contract", kind: "contract"}]
		evidence: ["federation-control-agent-contract"]
	},
	{
		metadata: {id: "stackkits-federation-backup", version: "1.0.0"}
		provides: ["cross-site-backup"]
		requires: [{id: "site-federation"}, {id: "data-residency"}, {id: "cross-site-placement"}, {id: "backup-core"}]
		supportedSiteKinds: ["home", "cloud"]
		realization: {kind: "modules", moduleRefs: {required: ["stackkits-federation-backup-runtime"], optional: []}}
		health: [{id: "federation-backup-contract", kind: "contract"}]
		evidence: ["federation-backup-contract"]
	},
	{
		metadata: {id: "stackkits-federation-observability", version: "1.0.0"}
		provides: ["bridge-observability"]
		requires: [{id: "site-federation"}, {id: "bridge-policy"}, {id: "observability-evidence"}]
		supportedSiteKinds: ["home", "cloud"]
		realization: {kind: "modules", moduleRefs: {required: ["stackkits-federation-observability-runtime"], optional: []}}
		health: [{id: "federation-observability-contract", kind: "contract"}]
		evidence: ["federation-observability-contract"]
	},
	{
		metadata: {id: "stackkits-modern-federation-policy", version: "1.0.0"}
		provides: _architectureV2FederationPolicyCapabilities
		requires: [{id: "site-federation"}, {id: "local-control-authority"}, {id: "device-enrollment-home"}]
		supportedSiteKinds: ["home", "cloud"]
		realization: {kind: "modules", moduleRefs: {required: ["stackkits-modern-federation-policy-manifest"], optional: []}}
		evidence: ["resolved-plan-contract"]
	},
	{
		metadata: {id: "stackkits-modern-identity-trust-policy", version: "1.0.0"}
		provides: _architectureV2ModernIdentityCapabilities
		requires: [{id: "site-federation"}, {id: "local-control-authority"}, {id: "device-enrollment-home"}]
		supportedSiteKinds: ["home", "cloud"]
		realization: {kind: "modules", moduleRefs: {required: ["stackkits-modern-home-identity-trust-policy-manifest", "stackkits-modern-cloud-identity-verifier-policy-manifest"], optional: []}}
		health: [{id: "modern-identity-trust-policy-contract", kind: "contract"}]
		evidence: ["modern-identity-trust-policy-contract"]
	},
]

_architectureV2ProfileExtensionModules: [
	{
		metadata: {id: "stackkits-federation-link-runtime", version: "1.0.0", description: "Typed inter-Site link boundary; transport, endpoint discovery, credentials, provider lifecycle, and general LAN reachability remain external."}
		role: "foundation", providerRef: "stackkits-federation-link", provides: ["inter-site-link"]
		requires: ["stackkits-modern-federation-policy-manifest"]
		supportedSiteKinds: ["home", "cloud"]
		runtime: {execution: "contract-handoff", kind: "host", delivery: "stackkit"}
		runtimeOwnerRequirement: {
			status: "unbound", ownerRef: "stackkits-federation-link-executor", capabilityRefs: ["inter-site-link"]
			targetScope: "federated-sites", operations: ["establish-inter-site-link", "remove-inter-site-link", "verify-inter-site-link"]
			requiredHealthRef: "federation-link-health", requiredEvidenceRef: "federation-link-evidence"
		}
		renderUnits: [{
			id:           "executor-contract", kind:                                             "native-config", rendererRef: "stackkit"
			templateRef:  "builtin://modern/federation/link/executor-contract/v1.json", version: "1.0.0"
			contractHash: "sha256:7884542488c9e95c6dd47431f4b3bd194c4705aa3c164aa3a0d925e9432dc316"
			publicInputRefs: [], secretInputRefs: []
			planInputRefs: ["stackId", "kit", "moduleTargets", "moduleCapabilities", "sites", "controlPlane", "bridge", "identity", "data", "failurePolicy", "federationLinkRequirements", "externalFederationLinkBindings"]
			outputs: ["modern/federation/link/executor-contract.json"]
			placement: {scope: "module", cardinality: "single"}
		}], realizationSupport: _architectureV2FederationRuntimeSupports.link
		health: [{id: "federation-link-contract", kind: "contract"}], evidence: ["federation-link-contract"]
	},
	{
		metadata: {id: "stackkits-federation-control-agent-runtime", version: "1.0.0", description: "Typed outbound-only Federation control-agent boundary; inbound tunnels, credentials, identity issuance, provider lifecycle, and general LAN reachability remain external."}
		role: "platform", providerRef: "stackkits-federation-control-agent", provides: ["outbound-control-agent"]
		requires: ["stackkits-modern-federation-policy-manifest", "stackkits-modern-home-identity-trust-policy-manifest", "stackkits-modern-cloud-identity-verifier-policy-manifest"]
		supportedSiteKinds: ["home", "cloud"]
		runtime: {execution: "contract-handoff", kind: "host", delivery: "stackkit"}
		runtimeOwnerRequirement: {
			status: "unbound", ownerRef: "stackkits-federation-control-agent-executor", capabilityRefs: ["outbound-control-agent"]
			targetScope: "federated-sites", operations: ["bind-outbound-control-agent", "remove-outbound-control-agent", "verify-outbound-control-agent"]
			requiredHealthRef: "federation-control-agent-health", requiredEvidenceRef: "federation-control-agent-evidence"
		}
		renderUnits: [{
			id:           "executor-contract", kind:                                                      "native-config", rendererRef: "stackkit"
			templateRef:  "builtin://modern/federation/control-agent/executor-contract/v1.json", version: "1.0.0"
			contractHash: "sha256:9c3de036682330f48113f5cfbf56ca925df6e345c8a10bbccbfc81d04ca85ebf"
			publicInputRefs: [], secretInputRefs: []
			planInputRefs: ["stackId", "kit", "moduleTargets", "moduleCapabilities", "sites", "controlPlane", "bridge", "identity", "data", "failurePolicy"]
			outputs: ["modern/federation/control-agent/executor-contract.json"]
			placement: {scope: "module", cardinality: "single"}
		}], realizationSupport: _architectureV2FederationRuntimeSupports.controlAgent
		health: [{id: "federation-control-agent-contract", kind: "contract"}], evidence: ["federation-control-agent-contract"]
	},
	{
		metadata: {id: "stackkits-federation-backup-runtime", version: "1.0.0", description: "Typed cross-Site backup boundary; repository provider lifecycle, endpoints, credentials, retention execution, and restore authority remain external."}
		role: "foundation", providerRef: "stackkits-federation-backup", provides: ["cross-site-backup"]
		requires: ["stackkits-modern-federation-policy-manifest"]
		supportedSiteKinds: ["home", "cloud"]
		runtime: {execution: "contract-handoff", kind: "host", delivery: "stackkit"}
		runtimeOwnerRequirement: {
			status: "unbound", ownerRef: "stackkits-federation-backup-executor", capabilityRefs: ["cross-site-backup"]
			targetScope: "federated-sites", operations: ["bind-cross-site-backup", "remove-cross-site-backup-binding", "verify-cross-site-backup"]
			requiredHealthRef: "federation-backup-health", requiredEvidenceRef: "federation-backup-evidence"
		}
		renderUnits: [{
			id:           "executor-contract", kind:                                               "native-config", rendererRef: "stackkit"
			templateRef:  "builtin://modern/federation/backup/executor-contract/v1.json", version: "1.0.0"
			contractHash: "sha256:a6bc9cd6a895b4059a4576dccae77ef5e08696a6c0a4ea480485bbde259c17c5"
			publicInputRefs: [], secretInputRefs: []
			planInputRefs: ["stackId", "kit", "moduleTargets", "moduleCapabilities", "sites", "controlPlane", "bridge", "identity", "data", "failurePolicy"]
			outputs: ["modern/federation/backup/executor-contract.json"]
			placement: {scope: "module", cardinality: "single"}
		}], realizationSupport: _architectureV2FederationRuntimeSupports.backup
		health: [{id: "federation-backup-contract", kind: "contract"}], evidence: ["federation-backup-contract"]
	},
	{
		metadata: {id: "stackkits-federation-observability-runtime", version: "1.0.0", description: "Typed bridge-observability boundary; telemetry backend lifecycle, credentials, transport, and provider configuration remain external."}
		role: "operations", providerRef: "stackkits-federation-observability", provides: ["bridge-observability"]
		requires: ["stackkits-modern-federation-policy-manifest"]
		supportedSiteKinds: ["home", "cloud"]
		runtime: {execution: "contract-handoff", kind: "host", delivery: "stackkit"}
		runtimeOwnerRequirement: {
			status: "unbound", ownerRef: "stackkits-federation-observability-executor", capabilityRefs: ["bridge-observability"]
			targetScope: "federated-sites", operations: ["bind-bridge-observability", "remove-bridge-observability", "verify-bridge-observability"]
			requiredHealthRef: "federation-observability-health", requiredEvidenceRef: "federation-observability-evidence"
		}
		renderUnits: [{
			id:           "executor-contract", kind:                                                      "native-config", rendererRef: "stackkit"
			templateRef:  "builtin://modern/federation/observability/executor-contract/v1.json", version: "1.0.0"
			contractHash: "sha256:a39448203af27efa2e7b638074e3da78b26cd1fd8906cec32620a335d4b40f5d"
			publicInputRefs: [], secretInputRefs: []
			planInputRefs: ["stackId", "kit", "moduleTargets", "moduleCapabilities", "sites", "controlPlane", "bridge", "identity", "data", "failurePolicy"]
			outputs: ["modern/federation/observability/executor-contract.json"]
			placement: {scope: "module", cardinality: "single"}
		}], realizationSupport: _architectureV2FederationRuntimeSupports.observability
		health: [{id: "federation-observability-contract", kind: "contract"}], evidence: ["federation-observability-contract"]
	},
	{
		metadata: {
			id:          "stackkits-modern-home-identity-trust-policy-manifest"
			version:     "1.0.0"
			description: "Node-local Modern Home identity authority and verifier policy; it publishes only bounded verifier state and never owns transport."
		}
		role:        "platform"
		providerRef: "stackkits-modern-identity-trust-policy"
		provides:    _architectureV2IdentityCapabilities
		requires: ["stackkits-home-device-authority-policy-manifest"]
		supportedSiteKinds: ["home"]
		nodeSelection: {authority: "control-authority-site", controlPlaneMembers: "only", requiredRoles: ["controller"]}
		runtime: {execution: "executable", kind: "native", delivery: "stackkit"}
		enforcementRequirement: {
			status: "bound", ownerRef: "stackkits-modern-home-identity-trust-enforcer"
			policyArtifactRefs: ["modern-identity-trust-policy"]
			targetScope: "home-control-authority"
			operations: ["verify-home-session", "publish-revocation-state-reference", "publish-verification-key-reference", "enforce-outbound-only-verifier-distribution"]
			requiredHealthRef:   "modern-home-identity-trust-enforcement"
			requiredEvidenceRef: "modern-home-identity-trust-enforcement"
		}
		renderUnits: [{
			id:           "policy-bundle", kind:                                     "native-config", rendererRef: "stackkit"
			templateRef:  "builtin://modern/home-identity-trust-policy/v1.json", version: "1.0.0"
			contractHash: "sha256:c267902e6021a66ac15e889cf3c041d630e4fedc0544058445409856a230fabc"
			publicInputRefs: ["modern-home-identity-authority"], secretInputRefs: []
			inputBindings: [{
				targetRef: "modern-home-identity-authority", sourceRef: "identityTrust.modernHomeAuthority"
				valueType: "modern-home-identity-authority-v1", cardinality: "single", required: true
			}]
			planInputRefs: ["stackId", "kit"]
			outputs: ["modern/identity/trust-policy.json"]
			placement: {scope: "node-local", cardinality: "one-per-node"}
		}]
		realizationSupport: _architectureV2ModernHomeIdentityTrustSupport
		health: [{id: "modern-home-identity-trust-enforcement", kind: "contract", scope: "each-node"}]
		evidence: ["modern-home-identity-trust-enforcement"]
	},
	{
		metadata: {
			id:          "stackkits-modern-cloud-identity-verifier-policy-manifest"
			version:     "1.0.0"
			description: "Node-local Modern Cloud verifier-only policy; it consumes bounded Home verifier state and can neither issue credentials nor reverse the distribution."
		}
		role:        "platform"
		providerRef: "stackkits-modern-identity-trust-policy"
		provides:    _architectureV2FederationIdentityCapabilities
		requires: ["stackkits-modern-home-identity-trust-policy-manifest"]
		supportedSiteKinds: ["cloud"]
		nodeSelection: {authority: "non-control-authority-sites", controlPlaneMembers: "any", requiredRoles: ["worker"]}
		runtime: {execution: "executable", kind: "native", delivery: "stackkit"}
		enforcementRequirement: {
			status: "bound", ownerRef: "stackkits-modern-cloud-identity-verifier-enforcer"
			policyArtifactRefs: ["modern-identity-verifier-distribution-policy"]
			targetScope: "cloud-sites"
			operations: ["apply-inbound-revocation-state-reference", "apply-inbound-verification-key-reference", "verify-cloud-session", "deny-reverse-verifier-distribution"]
			requiredHealthRef:   "modern-cloud-identity-verifier-enforcement"
			requiredEvidenceRef: "modern-cloud-identity-verifier-enforcement"
		}
		renderUnits: [{
			id:           "policy-bundle", kind:                                     "native-config", rendererRef: "stackkit"
			templateRef:  "builtin://modern/cloud-identity-verifier-policy/v1.json", version: "1.0.0"
			contractHash: "sha256:9906f37e867e7392daea5ff2d581d9b1a1842b00fe15f8ec67ea0e7efe850e1f"
			publicInputRefs: ["modern-cloud-identity-verification"], secretInputRefs: []
			inputBindings: [{
				targetRef: "modern-cloud-identity-verification", sourceRef: "identityTrust.modernCloudVerification"
				valueType: "modern-cloud-identity-verification-v1", cardinality: "single", required: true
			}]
			planInputRefs: ["stackId", "kit"]
			outputs: ["modern/identity/verifier-distribution-policy.json"]
			placement: {scope: "node-local", cardinality: "one-per-node"}
		}]
		realizationSupport: _architectureV2ModernCloudIdentityVerifierSupport
		health: [{id: "modern-cloud-identity-verifier-enforcement", kind: "contract", scope: "each-node"}]
		evidence: ["modern-cloud-identity-verifier-enforcement"]
	},
	{
		metadata: {
			id:          "stackkits-modern-federation-policy-manifest"
			version:     "1.0.0"
			description: "Generation-only Modern federation and partition policy manifest; transport, publication, control-agent, and verifier realization remain separately blocked."
		}
		role:        "platform"
		providerRef: "stackkits-modern-federation-policy"
		provides:    _architectureV2FederationPolicyCapabilities
		requires: ["stackkits-home-device-authority-policy-manifest"]
		supportedSiteKinds: ["home", "cloud"]
		runtime: {execution: "contract-handoff", kind: "native", delivery: "stackkit"}
		renderUnits: [{
			id:           "policy-bundle"
			kind:         "native-config"
			rendererRef:  "stackkit"
			templateRef:  "builtin://modern/federation-policy/v1.json"
			version:      "1.0.0"
			contractHash: "sha256:c01184d17e95defe6550aa7b1292a0205c1de747895af8e2a61bcb350d423c50"
			publicInputRefs: []
			secretInputRefs: []
			planInputRefs: ["stackId", "kit", "sites", "controlPlane", "bridge", "identity", "data", "failurePolicy"]
			outputs: ["modern/federation/policy.json"]
			placement: {
				scope:       "module"
				cardinality: "single"
			}
		}]
		realizationSupport: _architectureV2ModernFederationPolicySupport
		evidence: ["resolved-plan-contract"]
	},
]
