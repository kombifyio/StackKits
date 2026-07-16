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
Definition: base.#KitDefinition & {
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
		maxSites:  64
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
			"provision-local",
			"local-hardware-preflight",
			"lan-discovery",
			"local-ingress",
			"lan-access-policy",
			"device-enrollment-home",
			"local-control-authority",
			"offline-autonomy",
			"local-backup-target",
		]])
		defaults: []
		optional: [
			"lan-dns",
			"internal-pki",
			"private-remote-access",
			"public-publish-egress",
			"encrypted-offsite-backup",
			"photos",
			"availability-ha",
		]
		forbidden: ["site-cloud", "cloud-control-authority", "inter-site-link"]
	}
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
				requiredCapabilities: []
				allowedOriginKinds: ["home"]
			}
			"remote-private": {
				allowed: true
				requiredCapabilities: ["private-remote-access"]
				allowedOriginKinds: ["home"]
			}
			public: {
				allowed: true
				requiredCapabilities: ["public-publish-egress"]
				allowedOriginKinds: ["home"]
			}
		}
	}
	dataDefaults: {
		authority:               "home"
		cloudCopyRequiresPolicy: true
	}
	failureDefaults: {
		offlineOperation:              true
		localServicesSurviveCloudLoss: true
		cloudEdgeFailsClosed:          true
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
	bridge: {required: false, sourceKinds: [], edgeKinds: []}
	evidenceScenarios: ["SK-S1"]
}

#BasementKitStackV2: base.#KitSpecBinding & {definition: Definition}
