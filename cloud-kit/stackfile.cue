// =============================================================================
// STACKKIT: cloud-kit - Cloud single-environment homelab (VPS)
// =============================================================================
//
// The legacy v1 rollout shape below still derives from base.#StackBase and
// pins cloud context for one-minor compatibility. The additive v2 Definition
// is the architecture authority for new work: cloud Sites, explicit VPS/edge/
// DNS/TLS/hardening capabilities, and default-closed service publication
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
Definition: base.#KitDefinition & {
	apiVersion: "stackkit/v2alpha1"
	kind:       "KitDefinition"
	metadata: {
		slug:        "cloud-kit"
		version:     "5.0.0"
		displayName: "Cloud Kit"
		description: "Cloud-only StackKit for one or more VPS or cloud nodes"
	}
	topology: {
		allowedSiteKinds: ["cloud"]
		requiredSiteKinds: ["cloud"]
		minSites:  1
		maxSites:  64
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
				basis:             "provider-zone", memberSiteScope: "control-member-sites"
				partitionBehavior: "provider-network-failover"
			}
			healthAcceptance: {
				requiredGateRefs: [
					"provider-stackkits-ha-cloud-warm-ha-cloud-warm-contract",
					"module-stackkits-ha-cloud-warm-runtime-ha-cloud-warm-contract",
				]
				memberReadiness: "all"
			}
			evidenceAcceptance: requiredRefs: ["ha-cloud-warm-standby-zone-failover-proof"]
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
				basis:             "provider-zone", memberSiteScope: "control-member-sites"
				partitionBehavior: "provider-network-failover"
			}
			healthAcceptance: {
				requiredGateRefs: [
					"provider-stackkits-ha-cloud-quorum-ha-cloud-quorum-contract",
					"module-stackkits-ha-cloud-quorum-runtime-ha-cloud-quorum-contract",
				]
				memberReadiness: "majority"
			}
			evidenceAcceptance: requiredRefs: ["ha-cloud-quorum-zone-majority-proof"]
		}
	}
	capabilities: {
		required: list.Concat([base.#CommonCapabilityIDs, [
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
		]])
		defaults: []
		optional: ["private-admin-mesh", "provider-snapshot", "multi-zone-placement", "photos", "availability-ha"]
		forbidden: ["site-local", "lan-access-policy", "device-enrollment-home", "local-hardware-preflight"]
	}
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
				requiredCapabilities: []
				allowedOriginKinds: ["cloud"]
			}
			"remote-private": {
				allowed: true
				requiredCapabilities: ["private-admin-mesh"]
				allowedOriginKinds: ["cloud"]
			}
			public: {
				allowed: true
				requiredCapabilities: ["public-edge"]
				allowedOriginKinds: ["cloud"]
			}
		}
	}
	dataDefaults: {
		authority:               "cloud"
		cloudCopyRequiresPolicy: true
	}
	failureDefaults: {
		offlineOperation:              false
		localServicesSurviveCloudLoss: false
		cloudEdgeFailsClosed:          true
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
	bridge: {required: false, sourceKinds: [], edgeKinds: []}
	evidenceScenarios: ["SK-S2", "SK-S3"]
}

#CloudKitStackV2: base.#KitSpecBinding & {definition: Definition}
