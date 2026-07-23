// =============================================================================
// STACKKIT: basement-kit - Local single-environment homelab (home base)
// =============================================================================
//
// The legacy v1 rollout shape below still derives from base.#StackBase and
// accepts local/pi context for one-minor compatibility. The additive v2
// Definition is the architecture authority for new work: home Sites, explicit
// local capabilities, and hardware separated from locality (ADR-0029).
// "base-kit" is retired as a kit; the shared contracts live in base/.
//
// Installer: https://base.stackkit.cc  (base = home base)
// =============================================================================

package basement_kit

import (
	"github.com/kombifyio/stackkits/base"
	"list"
)

// #BasementKitStack is the legacy v1 compatibility projection. Do not add new
// product semantics here; add them to Definition/capability contracts.
#BasementKitStack: base.#StackBase & {
	context: *"local" | "pi"
}

// Definition is the additive v2 architecture profile. The existing
// #BasementKitStack remains untouched until the resolved-plan compiler cutover.
Definition: base.#ProductKitDefinition & {
	apiVersion: "stackkit/v2alpha1"
	kind:       "KitDefinition"
	metadata: {
		slug:        "basement-kit"
		version:     "5.0.0"
		displayName: "Basement Kit"
		description: "Local-first StackKit for one or more nodes on private home sites"
	}
	topology: {
		allowedSiteKinds: ["home"]
		requiredSiteKinds: ["home"]
		minSites:  1
		maxSites:  1
		multiNode: true
		controlPlane: {
			defaultMode: "single"
			allowedModes: ["single", "warm-standby", "quorum"]
			allowedAuthorityKinds: ["home"]
		}
	}
	availability: policies: {
		"warm-standby": {
			policyRef:      "basement-ha-warm-standby-policy"
			realizationRef: "basement-ha-warm-standby-v1"
			providerRef:    "stackkits-ha-basement-warm"
			moduleRef:      "stackkits-ha-basement-warm-runtime"
			selector:       "control-plane-members"
			defaults: {rpoSeconds: 300, rtoSeconds: 900, failureDomainSpread: 2, fencing: "manual"}
			limits: {
				maxRpoSeconds:          900, maxRtoSeconds:        1800
				minFailureDomainSpread: 2, maxFailureDomainSpread: 3
				allowedFencing: ["manual", "automatic"]
			}
			failureModel: {
				basis:             "local-device", memberSiteScope: "control-member-sites"
				partitionBehavior: "local-control-continues"
			}
			healthAcceptance: {
				requiredGateRefs: [
					"provider-stackkits-ha-basement-warm-ha-basement-warm-contract",
					"module-stackkits-ha-basement-warm-runtime-ha-basement-warm-contract",
				]
				memberReadiness: "all"
			}
			evidenceAcceptance: requiredRefs: ["ha-basement-warm-standby-failure-domain-proof"]
		}
		quorum: {
			policyRef:      "basement-ha-quorum-policy"
			realizationRef: "basement-ha-quorum-v1"
			providerRef:    "stackkits-ha-basement-quorum"
			moduleRef:      "stackkits-ha-basement-quorum-runtime"
			selector:       "control-plane-members"
			defaults: {rpoSeconds: 60, rtoSeconds: 300, failureDomainSpread: 3, fencing: "automatic"}
			limits: {
				maxRpoSeconds:          300, maxRtoSeconds:        900
				minFailureDomainSpread: 3, maxFailureDomainSpread: 7
				allowedFencing: ["automatic"]
			}
			failureModel: {
				basis:             "local-device", memberSiteScope: "control-member-sites"
				partitionBehavior: "local-control-continues"
			}
			healthAcceptance: {
				requiredGateRefs: [
					"provider-stackkits-ha-basement-quorum-ha-basement-quorum-contract",
					"module-stackkits-ha-basement-quorum-runtime-ha-basement-quorum-contract",
				]
				memberReadiness: "majority"
			}
			evidenceAcceptance: requiredRefs: ["ha-basement-quorum-failure-domain-proof"]
		}
	}
	capabilities: {
		required: list.Concat([base.#CommonCapabilityIDs, [
			"site-local",
			"local-ingress",
			"lan-access-policy",
			"device-enrollment-home",
			"local-control-authority",
			"offline-autonomy",
			"local-backup-target",
		]])
		defaults: []
		optional: [
			"lan-discovery",
			"lan-dns",
			"internal-pki",
			"private-remote-access",
			"public-publish-egress",
			"encrypted-offsite-backup",
			"basement-compose-runtime",
			"availability-ha",
		]
		forbidden: ["site-cloud", "cloud-control-authority", "inter-site-link"]
	}
	workloads: {required: [], defaults: [], optional: ["photos"], forbidden: []}
	accessDefaults: {
		publicRoutesDefaultClosed: true
		lanLocationIsIdentity:     false
		privilegedStepUpRequired:  true
		deviceEnrollment:          "local-only"
		deviceBoundSessions:       true
	}
	reachability: {
		accessPolicies: {
			allowedExposures: ["private", "lan", "public"]
			lanStepDownAllowed: true
		}
		routes: {
			local: {
				allowed: true
				requiredRealizations: []
				allowedOriginKinds: ["home"]
			}
			"remote-private": {
				allowed: true
				requiredRealizations: [{capabilityRef: "private-remote-access", role: "access"}]
				allowedOriginKinds: ["home"]
			}
			public: {
				allowed: true
				requiredRealizations: [{capabilityRef: "public-publish-egress", role: "egress"}]
				allowedOriginKinds: ["home"]
			}
		}
	}
	dataDefaults: {
		authority:               "home"
		cloudCopyRequiresPolicy: true
	}
	deviceEnrollment: {
		required: true
		mode:     "local-only"
		authorityKinds: ["home"]
	}
	partitionPolicy: {
		onCloudLoss:                     "not-applicable"
		onLinkLoss:                      "local-continues"
		cloudEdge:                       "not-applicable"
		localIdentityAuthorityAvailable: true
		maxStaleVerificationSeconds:     0
		denyNewCrossSiteSessions:        true
	}
	identityTrust: {
		authorities: [
			{id: "home-human-authority", principal: "human", trustDomainRef: "home-stackkit-trust", placement: {selector: "control-authority-site"}, owner: {kind: "catalog", providerRef: "stackkits-home-device-authority", moduleRef: "stackkits-home-device-authority-policy-manifest"}},
			{id: "home-device-authority", principal: "device", trustDomainRef: "home-stackkit-trust", placement: {selector: "control-authority-site"}, owner: {kind: "catalog", providerRef: "stackkits-home-device-authority", moduleRef: "stackkits-home-device-authority-policy-manifest"}},
			{id: "home-workload-authority", principal: "workload", trustDomainRef: "home-stackkit-trust", placement: {selector: "control-authority-site"}, owner: {kind: "catalog", providerRef: "stackkits-home-device-authority", moduleRef: "stackkits-home-device-authority-policy-manifest"}},
		]
		credentialIssuers: [
			{id: "home-human-credential-issuer", authorityRef: "home-human-authority", principal: "human", issuerRef: "home-human-issuer", audienceRefs: ["stackkit-human-session"], verificationKeySetRef: "home-human-verification-keys", placement: {selector: "control-authority-site"}, owner: {kind: "catalog", providerRef: "stackkits-home-device-authority", moduleRef: "stackkits-home-device-authority-policy-manifest"}, issuanceWithinStackKit: true, credentialTTLSeconds: 86400, sessionTTLSeconds: 900, proofOfPossessionRequired: false, revocationSupported: true, revocationMaxStalenessSeconds: 0, enrollment: {mode: "none", exposure: "none"}},
			{id: "home-device-credential-issuer", authorityRef: "home-device-authority", principal: "device", issuerRef: "home-device-issuer", audienceRefs: ["stackkit-device-session"], verificationKeySetRef: "home-device-verification-keys", placement: {selector: "control-authority-site"}, owner: {kind: "catalog", providerRef: "stackkits-home-device-authority", moduleRef: "stackkits-home-device-authority-policy-manifest"}, issuanceWithinStackKit: true, credentialTTLSeconds: 3600, sessionTTLSeconds: 900, proofOfPossessionRequired: true, revocationSupported: true, revocationMaxStalenessSeconds: 0, enrollment: {mode: "local-only", exposure: "lan"}},
			{id: "home-workload-credential-issuer", authorityRef: "home-workload-authority", principal: "workload", issuerRef: "home-workload-issuer", audienceRefs: ["stackkit-workload"], verificationKeySetRef: "home-workload-verification-keys", placement: {selector: "control-authority-site"}, owner: {kind: "catalog", providerRef: "stackkits-home-device-authority", moduleRef: "stackkits-home-device-authority-policy-manifest"}, issuanceWithinStackKit: true, credentialTTLSeconds: 300, sessionTTLSeconds: 300, proofOfPossessionRequired: true, revocationSupported: true, revocationMaxStalenessSeconds: 0, enrollment: {mode: "none", exposure: "none"}},
		]
		verifierPlacements: [
			{id: "basement-human-verifier", issuerRef: "home-human-issuer", principal: "human", audienceRefs: ["stackkit-human-session"], verificationKeySetRef: "home-human-verification-keys", placement: {selector: "control-authority-site"}, owner: {kind: "catalog", providerRef: "stackkits-basement-identity-trust-policy", moduleRef: "stackkits-basement-identity-trust-policy-manifest"}, proofOfPossessionRequired: false, revocationMaxStalenessSeconds: 0},
			{id: "basement-device-verifier", issuerRef: "home-device-issuer", principal: "device", audienceRefs: ["stackkit-device-session"], verificationKeySetRef: "home-device-verification-keys", placement: {selector: "control-authority-site"}, owner: {kind: "catalog", providerRef: "stackkits-basement-identity-trust-policy", moduleRef: "stackkits-basement-identity-trust-policy-manifest"}, proofOfPossessionRequired: true, revocationMaxStalenessSeconds: 0},
			{id: "basement-workload-verifier", issuerRef: "home-workload-issuer", principal: "workload", audienceRefs: ["stackkit-workload"], verificationKeySetRef: "home-workload-verification-keys", placement: {selector: "control-authority-site"}, owner: {kind: "catalog", providerRef: "stackkits-basement-identity-trust-policy", moduleRef: "stackkits-basement-identity-trust-policy-manifest"}, proofOfPossessionRequired: true, revocationMaxStalenessSeconds: 0},
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
		mode:           "private"
		domainRequired: false
		defaultDomain:  "home.localhost"
		defaultTLSMode: "internal"
	}
	authoring: {
		contractVersion:   "1.0.0"
		initialSpecStatus: "supported"
		requiredOverrides: []
		initialSpec: {
			apiVersion: "stackkit/v2alpha1"
			kind:       "StackSpec"
			metadata: name: "my-homelab"
			source: kind:   "native-v2"
			kit: slug:      "basement-kit"
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
				mode: "private"
				domain: base: "home.localhost"
				transport: {}
				dns: {}
				tls: defaultMode: "internal"
			}
			sites: [{
				id:            "home"
				kind:          "home"
				failureDomain: "home-primary"
			}]
			nodes: [{
				id:      "main"
				siteRef: "home"
				roles: ["controller", "worker"]
				hardware: {}
				failureDomain: "node-main"
			}]
			controlPlane: {
				mode:             "single"
				authoritySiteRef: "home"
				members: ["main"]
			}
			capabilities: {enable: [], disable: []}
			availability: {}
			deviceEnrollment: {
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
			partitionPolicy: {
				onCloudLoss:                     "not-applicable"
				onLinkLoss:                      "local-continues"
				cloudEdge:                       "not-applicable"
				localIdentityAuthorityAvailable: true
				maxStaleVerificationSeconds:     0
				denyNewCrossSiteSessions:        true
			}
			data: defaultAuthority: "home"
		}
	}
	bridge: {required: false, sourceKinds: [], edgeKinds: []}
	evidenceScenarios: ["SK-S1"]
}

#BasementKitStackV2: base.#KitSpecBinding & {definition: Definition}

#BasementKitAuthoringBinding: base.#KitSpecBinding & {
	definition: Definition
	spec:       Definition.authoring.initialSpec
}
