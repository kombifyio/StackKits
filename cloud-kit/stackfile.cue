// =============================================================================
// STACKKIT: cloud-kit - Cloud single-environment homelab
// =============================================================================
//
// The legacy v1 rollout shape below still derives from base.#StackBase and
// pins cloud context for one-minor compatibility. The additive v2 Definition
// is the architecture authority for new work: cloud Sites, externally supplied
// host admission, edge/DNS/TLS intent, host-local hardening, and default-closed service publication
// (ADR-0029). It is not Modern Homelab.
//
// Installer: https://cloud.stackkit.cc
// =============================================================================

package cloud_kit

import (
	"github.com/kombifyio/stackkits/base"
	"list"
)

// #CloudKitStack is the legacy v1 compatibility projection. Do not add new
// product semantics here; add them to Definition/capability contracts.
#CloudKitStack: base.#StackBase & {
	context: "cloud"
}

// Definition is the additive v2 architecture profile. Public-capable does not
// mean publicly open: service publications remain default-closed.
Definition: base.#ProductKitDefinition & {
	apiVersion: "stackkit/v2alpha1"
	kind:       "KitDefinition"
	metadata: {
		slug:        "cloud-kit"
		version:     "5.0.0"
		displayName: "Cloud Kit"
		description: "Cloud-only StackKit for one or more externally supplied cloud hosts"
	}
	topology: {
		allowedSiteKinds: ["cloud"]
		requiredSiteKinds: ["cloud"]
		minSites:  1
		maxSites:  1
		multiNode: true
		controlPlane: {
			defaultMode: "single"
			allowedModes: ["single", "warm-standby", "quorum"]
			allowedAuthorityKinds: ["cloud"]
		}
	}
	availability: policies: {
		"warm-standby": {
			policyRef:      "cloud-ha-warm-standby-policy"
			realizationRef: "cloud-ha-warm-standby-v1"
			providerRef:    "stackkits-ha-cloud-warm"
			moduleRef:      "stackkits-ha-cloud-warm-runtime"
			selector:       "control-plane-members"
			defaults: {rpoSeconds: 60, rtoSeconds: 300, failureDomainSpread: 2, fencing: "automatic"}
			limits: {
				maxRpoSeconds:          300, maxRtoSeconds:        900
				minFailureDomainSpread: 2, maxFailureDomainSpread: 3
				allowedFencing: ["automatic"]
			}
			failureModel: {
				basis:             "failure-domain", memberSiteScope: "control-member-sites"
				partitionBehavior: "failure-domain-failover"
			}
			healthAcceptance: {
				requiredGateRefs: [
					"provider-stackkits-ha-cloud-warm-ha-cloud-warm-contract",
					"module-stackkits-ha-cloud-warm-runtime-ha-cloud-warm-contract",
				]
				memberReadiness: "all"
			}
			evidenceAcceptance: requiredRefs: ["ha-cloud-warm-standby-failure-domain-proof"]
		}
		quorum: {
			policyRef:      "cloud-ha-quorum-policy"
			realizationRef: "cloud-ha-quorum-v1"
			providerRef:    "stackkits-ha-cloud-quorum"
			moduleRef:      "stackkits-ha-cloud-quorum-runtime"
			selector:       "control-plane-members"
			defaults: {rpoSeconds: 15, rtoSeconds: 120, failureDomainSpread: 3, fencing: "automatic"}
			limits: {
				maxRpoSeconds:          60, maxRtoSeconds:         300
				minFailureDomainSpread: 3, maxFailureDomainSpread: 7
				allowedFencing: ["automatic"]
			}
			failureModel: {
				basis:             "failure-domain", memberSiteScope: "control-member-sites"
				partitionBehavior: "failure-domain-failover"
			}
			healthAcceptance: {
				requiredGateRefs: [
					"provider-stackkits-ha-cloud-quorum-ha-cloud-quorum-contract",
					"module-stackkits-ha-cloud-quorum-runtime-ha-cloud-quorum-contract",
				]
				memberReadiness: "majority"
			}
			evidenceAcceptance: requiredRefs: ["ha-cloud-quorum-failure-domain-majority-proof"]
		}
	}
	capabilities: {
		required: list.Concat([base.#CommonCapabilityIDs, [
			"site-cloud",
			"host-local-internet-firewall",
			"public-edge",
			"public-dns",
			"public-tls",
			"internet-host-hardening",
			"remote-owner-bootstrap",
			"offsite-object-backup",
			"cloud-control-authority",
		]])
		defaults: []
		optional: ["private-admin-mesh", "failure-domain-placement", "availability-ha"]
		forbidden: ["site-local", "lan-discovery", "local-ingress", "lan-access-policy", "device-enrollment-home"]
	}
	workloads: {required: [], defaults: [], optional: ["photos"], forbidden: []}
	accessDefaults: {
		publicRoutesDefaultClosed: true
		lanLocationIsIdentity:     false
		privilegedStepUpRequired:  true
		deviceEnrollment:          "none"
		deviceBoundSessions:       true
	}
	reachability: {
		accessPolicies: {
			allowedExposures: ["private", "public"]
			lanStepDownAllowed: false
		}
		routes: {
			local: {
				allowed: true
				requiredRealizations: []
				allowedOriginKinds: ["cloud"]
			}
			"remote-private": {
				allowed: true
				requiredRealizations: [{capabilityRef: "private-admin-mesh", role: "access"}]
				allowedOriginKinds: ["cloud"]
			}
			public: {
				allowed: true
				requiredRealizations: [{capabilityRef: "public-edge", role: "edge"}]
				allowedOriginKinds: ["cloud"]
			}
		}
	}
	dataDefaults: {
		authority:               "cloud"
		cloudCopyRequiresPolicy: true
	}
	deviceEnrollment: {
		required: false
		mode:     "disabled"
		authorityKinds: []
	}
	partitionPolicy: {
		onCloudLoss:                     "fail-closed"
		onLinkLoss:                      "not-applicable"
		cloudEdge:                       "fail-closed"
		localIdentityAuthorityAvailable: false
		maxStaleVerificationSeconds:     0
		denyNewCrossSiteSessions:        true
	}
	identityTrust: {
		authorities: [
			{id: "cloud-human-authority", principal: "human", trustDomainRef: "cloud-stackkit-trust", placement: {selector: "control-authority-site"}, owner: {kind: "catalog", providerRef: "stackkits-cloud-identity-trust-policy", moduleRef: "stackkits-cloud-identity-trust-policy-manifest"}},
			{id: "owner-device-authority", principal: "device", trustDomainRef: "owner-bound-device-trust", placement: {selector: "external", contractRef: "owner-bound-device-authority"}, owner: {kind: "external", contractRef: "owner-bound-device-authority"}},
			{id: "cloud-workload-authority", principal: "workload", trustDomainRef: "cloud-stackkit-trust", placement: {selector: "control-authority-site"}, owner: {kind: "catalog", providerRef: "stackkits-cloud-identity-trust-policy", moduleRef: "stackkits-cloud-identity-trust-policy-manifest"}},
		]
		credentialIssuers: [
			{id: "cloud-human-credential-issuer", authorityRef: "cloud-human-authority", principal: "human", issuerRef: "cloud-human-issuer", audienceRefs: ["stackkit-human-session"], verificationKeySetRef: "cloud-human-verification-keys", placement: {selector: "control-authority-site"}, owner: {kind: "catalog", providerRef: "stackkits-cloud-identity-trust-policy", moduleRef: "stackkits-cloud-identity-trust-policy-manifest"}, issuanceWithinStackKit: true, credentialTTLSeconds: 86400, sessionTTLSeconds: 900, proofOfPossessionRequired: false, revocationSupported: true, revocationMaxStalenessSeconds: 0, enrollment: {mode: "none", exposure: "none"}},
			{id: "owner-device-credential-issuer", authorityRef: "owner-device-authority", principal: "device", issuerRef: "owner-bound-device-issuer", audienceRefs: ["stackkit-device-session"], verificationKeySetRef: "owner-bound-device-verification-keys", placement: {selector: "external", contractRef: "owner-bound-device-authority"}, owner: {kind: "external", contractRef: "owner-bound-device-authority"}, issuanceWithinStackKit: false, credentialTTLSeconds: 3600, sessionTTLSeconds: 900, proofOfPossessionRequired: true, revocationSupported: true, revocationMaxStalenessSeconds: 0, enrollment: {mode: "none", exposure: "none"}},
			{id: "cloud-workload-credential-issuer", authorityRef: "cloud-workload-authority", principal: "workload", issuerRef: "cloud-workload-issuer", audienceRefs: ["stackkit-workload"], verificationKeySetRef: "cloud-workload-verification-keys", placement: {selector: "control-authority-site"}, owner: {kind: "catalog", providerRef: "stackkits-cloud-identity-trust-policy", moduleRef: "stackkits-cloud-identity-trust-policy-manifest"}, issuanceWithinStackKit: true, credentialTTLSeconds: 300, sessionTTLSeconds: 300, proofOfPossessionRequired: true, revocationSupported: true, revocationMaxStalenessSeconds: 0, enrollment: {mode: "none", exposure: "none"}},
		]
		verifierPlacements: [
			{id: "cloud-human-verifier", issuerRef: "cloud-human-issuer", principal: "human", audienceRefs: ["stackkit-human-session"], verificationKeySetRef: "cloud-human-verification-keys", placement: {selector: "cloud-sites"}, owner: {kind: "catalog", providerRef: "stackkits-cloud-identity-trust-policy", moduleRef: "stackkits-cloud-identity-trust-policy-manifest"}, proofOfPossessionRequired: false, revocationMaxStalenessSeconds: 0},
			{id: "cloud-device-verifier", issuerRef: "owner-bound-device-issuer", principal: "device", audienceRefs: ["stackkit-device-session"], verificationKeySetRef: "owner-bound-device-verification-keys", placement: {selector: "cloud-sites"}, owner: {kind: "catalog", providerRef: "stackkits-cloud-identity-trust-policy", moduleRef: "stackkits-cloud-identity-trust-policy-manifest"}, proofOfPossessionRequired: true, revocationMaxStalenessSeconds: 0},
			{id: "cloud-workload-verifier", issuerRef: "cloud-workload-issuer", principal: "workload", audienceRefs: ["stackkit-workload"], verificationKeySetRef: "cloud-workload-verification-keys", placement: {selector: "cloud-sites"}, owner: {kind: "catalog", providerRef: "stackkits-cloud-identity-trust-policy", moduleRef: "stackkits-cloud-identity-trust-policy-manifest"}, proofOfPossessionRequired: true, revocationMaxStalenessSeconds: 0},
		]
		verifierDistributions: []
	}
	generation: {
		defaultStrategy: "kit-template"
		allowedStrategies: ["kit-template", "module-fragments"]
		defaultTarget: "opentofu"
		allowedTargets: ["opentofu", "compose"]
		contractVersion: "1.0.0"
	}
	network: {
		mode:           "public-capable"
		domainRequired: true
		defaultTLSMode: "public"
	}
	authoring: {
		contractVersion:   "1.0.0"
		initialSpecStatus: "supported"
		requiredOverrides: ["network.domain.base"]
		initialSpec: {
			apiVersion: "stackkit/v2alpha1"
			kind:       "StackSpec"
			metadata: name: "my-cloud-homelab"
			source: kind:   "native-v2"
			kit: slug:      "cloud-kit"
			install: {
				mode:    "bootstrapped"
				runtime: "docker"
				platform: {
					management:      "selected-provider"
					fallbackAllowed: false
					setupPolicy: {}
				}
			}
			generation: {
				strategy: "kit-template"
				target:   "opentofu"
			}
			system: {}
			storage: {}
			container: {}
			network: {
				mode: "public-capable"
				domain: base: "example.invalid"
				transport: {}
				dns: {}
				tls: defaultMode: "public"
			}
			sites: [{
				id:            "cloud"
				kind:          "cloud"
				failureDomain: "cloud-primary"
			}]
			nodes: [{
				id:      "cloud-main"
				siteRef: "cloud"
				roles: ["controller", "worker", "edge"]
				hardware: {}
				failureDomain: "node-cloud-main"
			}]
			controlPlane: {
				mode:             "single"
				authoritySiteRef: "cloud"
				members: ["cloud-main"]
			}
			capabilities: {enable: [], disable: []}
			availability: {}
			partitionPolicy: {
				onCloudLoss:                     "fail-closed"
				onLinkLoss:                      "not-applicable"
				cloudEdge:                       "fail-closed"
				localIdentityAuthorityAvailable: false
				maxStaleVerificationSeconds:     0
				denyNewCrossSiteSessions:        true
			}
			data: defaultAuthority: "cloud"
		}
	}
	bridge: {required: false, sourceKinds: [], edgeKinds: []}
	evidenceScenarios: ["SK-S2", "SK-S3"]
}

#CloudKitStackV2: base.#KitSpecBinding & {definition: Definition}

#CloudKitAuthoringBinding: base.#KitSpecBinding & {
	definition: Definition
	spec:       Definition.authoring.initialSpec
}
