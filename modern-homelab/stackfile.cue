// Modern Homelab is the Architecture-v2 federation profile. It requires at
// least one Home Site and one Cloud Site, while keeping Home as the control,
// enrollment, and default data authority. Concrete transports, PaaS products,
// server providers, host lifecycle, and workload realizations belong to their
// catalog modules or to TechStack; they are not kit identity.

package modern_homelab

import (
	"list"

	"github.com/kombifyio/stackkits/base"
)

// Definition is the sole Modern Homelab architecture authority.
Definition: base.#ProductKitDefinition & {
	apiVersion: "stackkit/v2alpha1"
	kind:       "KitDefinition"
	metadata: {
		slug:        "modern-homelab"
		version:     "1.0.0-alpha"
		displayName: "Modern Homelab"
		description: "Federated local and cloud StackKit with a policy-constrained bridge"
	}
	topology: {
		allowedSiteKinds: ["home", "cloud"]
		requiredSiteKinds: ["home", "cloud"]
		minSites:  2
		maxSites:  64
		multiNode: true
		controlPlane: {
			defaultMode: "single"
			allowedModes: ["single", "warm-standby", "quorum"]
			allowedAuthorityKinds: ["home"]
			memberSiteScope: "authority-site"
		}
	}
	availability: policies: {
		"warm-standby": {
			policyRef:      "modern-ha-warm-standby-policy"
			realizationRef: "modern-ha-warm-standby-v1"
			providerRef:    "stackkits-ha-modern-warm"
			moduleRef:      "stackkits-ha-modern-warm-runtime"
			selector:       "control-plane-members"
			defaults: {rpoSeconds: 120, rtoSeconds: 600, failureDomainSpread: 2, fencing: "automatic"}
			limits: {
				maxRpoSeconds:          600, maxRtoSeconds:        1200
				minFailureDomainSpread: 2, maxFailureDomainSpread: 3
				allowedFencing: ["automatic"]
			}
			failureModel: {
				basis:             "site-and-link", memberSiteScope: "authority-site-control-members"
				partitionBehavior: "home-authority-continues-cloud-edge-fails-closed"
			}
			healthAcceptance: {
				requiredGateRefs: [
					"provider-stackkits-ha-modern-warm-ha-modern-warm-contract",
					"module-stackkits-ha-modern-warm-runtime-ha-modern-warm-contract",
				]
				memberReadiness: "all"
			}
			evidenceAcceptance: requiredRefs: ["ha-modern-warm-standby-partition-isolation-proof"]
		}
		quorum: {
			policyRef:      "modern-ha-quorum-policy"
			realizationRef: "modern-ha-quorum-v1"
			providerRef:    "stackkits-ha-modern-quorum"
			moduleRef:      "stackkits-ha-modern-quorum-runtime"
			selector:       "control-plane-members"
			defaults: {rpoSeconds: 30, rtoSeconds: 180, failureDomainSpread: 3, fencing: "automatic"}
			limits: {
				maxRpoSeconds:          120, maxRtoSeconds:        600
				minFailureDomainSpread: 3, maxFailureDomainSpread: 7
				allowedFencing: ["automatic"]
			}
			failureModel: {
				basis:             "site-and-link", memberSiteScope: "authority-site-control-members"
				partitionBehavior: "home-authority-continues-cloud-edge-fails-closed"
			}
			healthAcceptance: {
				requiredGateRefs: [
					"provider-stackkits-ha-modern-quorum-ha-modern-quorum-contract",
					"module-stackkits-ha-modern-quorum-runtime-ha-modern-quorum-contract",
				]
				memberReadiness: "majority"
			}
			evidenceAcceptance: requiredRefs: ["ha-modern-quorum-partition-majority-proof"]
		}
	}
	capabilities: {
		required: list.Concat([base.#CommonCapabilityIDs, [
			"site-local",
			"site-cloud",
			"site-federation",
			"inter-site-link",
			"service-publication",
			"host-local-internet-firewall",
			"internet-host-hardening",
			"public-edge",
			"public-dns",
			"public-tls",
			"bridge-policy",
			"private-remote-access",
			"outbound-control-agent",
			"edge-identity-verifier",
			"local-control-authority",
			"device-enrollment-home",
			"local-ingress",
			"lan-access-policy",
			"offline-autonomy",
			"cross-site-placement",
			"data-residency",
			"split-horizon-naming",
			"cross-site-backup",
			"partition-failure-contract",
			"bridge-observability",
		]])
		defaults: []
		optional: ["lan-discovery", "lan-dns", "internal-pki", "private-admin-mesh", "failure-domain-placement", "availability-ha"]
		forbidden: ["cloud-enrollment-authority", "broad-lan-route-advertisement"]
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
				requiredRealizations: [{capabilityRef: "public-edge", role: "edge"}]
				// Home-origin publication belongs exclusively to bridge.publications,
				// which binds the exact source Site, inter-Site link and Cloud edge.
				allowedOriginKinds: ["cloud"]
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
		onCloudLoss:                     "local-continues"
		onLinkLoss:                      "local-continues"
		cloudEdge:                       "fail-closed"
		localIdentityAuthorityAvailable: true
		maxStaleVerificationSeconds:     300
		denyNewCrossSiteSessions:        true
	}
	identityTrust: {
		authorities: [
			{id: "home-human-authority", principal: "human", trustDomainRef: "home-stackkit-trust", placement: {selector: "control-authority-site"}, owner: {kind: "catalog", providerRef: "stackkits-home-device-authority", moduleRef: "stackkits-home-device-authority-policy-manifest"}},
			{id: "home-device-authority", principal: "device", trustDomainRef: "home-stackkit-trust", placement: {selector: "control-authority-site"}, owner: {kind: "catalog", providerRef: "stackkits-home-device-authority", moduleRef: "stackkits-home-device-authority-policy-manifest"}},
			{id: "home-workload-authority", principal: "workload", trustDomainRef: "home-stackkit-trust", placement: {selector: "control-authority-site"}, owner: {kind: "catalog", providerRef: "stackkits-home-device-authority", moduleRef: "stackkits-home-device-authority-policy-manifest"}},
		]
		credentialIssuers: [
			{id: "home-human-credential-issuer", authorityRef: "home-human-authority", principal: "human", issuerRef: "home-human-issuer", audienceRefs: ["stackkit-human-session"], verificationKeySetRef: "home-human-verification-keys", placement: {selector: "control-authority-site"}, owner: {kind: "catalog", providerRef: "stackkits-home-device-authority", moduleRef: "stackkits-home-device-authority-policy-manifest"}, issuanceWithinStackKit: true, credentialTTLSeconds: 86400, sessionTTLSeconds: 900, proofOfPossessionRequired: false, revocationSupported: true, revocationMaxStalenessSeconds: 300, enrollment: {mode: "none", exposure: "none"}},
			{id: "home-device-credential-issuer", authorityRef: "home-device-authority", principal: "device", issuerRef: "home-device-issuer", audienceRefs: ["stackkit-device-session"], verificationKeySetRef: "home-device-verification-keys", placement: {selector: "control-authority-site"}, owner: {kind: "catalog", providerRef: "stackkits-home-device-authority", moduleRef: "stackkits-home-device-authority-policy-manifest"}, issuanceWithinStackKit: true, credentialTTLSeconds: 3600, sessionTTLSeconds: 900, proofOfPossessionRequired: true, revocationSupported: true, revocationMaxStalenessSeconds: 300, enrollment: {mode: "local-only", exposure: "lan"}},
			{id: "home-workload-credential-issuer", authorityRef: "home-workload-authority", principal: "workload", issuerRef: "home-workload-issuer", audienceRefs: ["stackkit-workload"], verificationKeySetRef: "home-workload-verification-keys", placement: {selector: "control-authority-site"}, owner: {kind: "catalog", providerRef: "stackkits-home-device-authority", moduleRef: "stackkits-home-device-authority-policy-manifest"}, issuanceWithinStackKit: true, credentialTTLSeconds: 300, sessionTTLSeconds: 300, proofOfPossessionRequired: true, revocationSupported: true, revocationMaxStalenessSeconds: 300, enrollment: {mode: "none", exposure: "none"}},
		]
		verifierPlacements: [
			{id: "modern-home-human-verifier", issuerRef: "home-human-issuer", principal: "human", audienceRefs: ["stackkit-human-session"], verificationKeySetRef: "home-human-verification-keys", placement: {selector: "control-authority-site"}, owner: {kind: "catalog", providerRef: "stackkits-modern-identity-trust-policy", moduleRef: "stackkits-modern-home-identity-trust-policy-manifest"}, proofOfPossessionRequired: false, revocationMaxStalenessSeconds: 0},
			{id: "modern-cloud-human-verifier", issuerRef: "home-human-issuer", principal: "human", audienceRefs: ["stackkit-human-session"], verificationKeySetRef: "home-human-verification-keys", placement: {selector: "cloud-sites"}, owner: {kind: "catalog", providerRef: "stackkits-modern-identity-trust-policy", moduleRef: "stackkits-modern-cloud-identity-verifier-policy-manifest"}, proofOfPossessionRequired: false, revocationMaxStalenessSeconds: 300},
			{id: "modern-home-device-verifier", issuerRef: "home-device-issuer", principal: "device", audienceRefs: ["stackkit-device-session"], verificationKeySetRef: "home-device-verification-keys", placement: {selector: "control-authority-site"}, owner: {kind: "catalog", providerRef: "stackkits-modern-identity-trust-policy", moduleRef: "stackkits-modern-home-identity-trust-policy-manifest"}, proofOfPossessionRequired: true, revocationMaxStalenessSeconds: 0},
			{id: "modern-cloud-device-verifier", issuerRef: "home-device-issuer", principal: "device", audienceRefs: ["stackkit-device-session"], verificationKeySetRef: "home-device-verification-keys", placement: {selector: "cloud-sites"}, owner: {kind: "catalog", providerRef: "stackkits-modern-identity-trust-policy", moduleRef: "stackkits-modern-cloud-identity-verifier-policy-manifest"}, proofOfPossessionRequired: true, revocationMaxStalenessSeconds: 300},
			{id: "modern-home-workload-verifier", issuerRef: "home-workload-issuer", principal: "workload", audienceRefs: ["stackkit-workload"], verificationKeySetRef: "home-workload-verification-keys", placement: {selector: "control-authority-site"}, owner: {kind: "catalog", providerRef: "stackkits-modern-identity-trust-policy", moduleRef: "stackkits-modern-home-identity-trust-policy-manifest"}, proofOfPossessionRequired: true, revocationMaxStalenessSeconds: 0},
			{id: "modern-cloud-workload-verifier", issuerRef: "home-workload-issuer", principal: "workload", audienceRefs: ["stackkit-workload"], verificationKeySetRef: "home-workload-verification-keys", placement: {selector: "cloud-sites"}, owner: {kind: "catalog", providerRef: "stackkits-modern-identity-trust-policy", moduleRef: "stackkits-modern-cloud-identity-verifier-policy-manifest"}, proofOfPossessionRequired: true, revocationMaxStalenessSeconds: 300},
		]
		verifierDistributions: [
			{id: "modern-human-verifier-distribution", issuerRef: "home-human-issuer", from: {selector: "control-authority-site"}, to: {selector: "cloud-sites"}, materials: ["revocation-state", "verification-key-reference"], includesSigningAuthority: false, includesEnrollmentAuthority: false, includesPrivateKeyMaterial: false, includesCredentialMaterial: false, reverseAllowed: false, maxStalenessSeconds: 300, owner: {kind: "catalog", providerRef: "stackkits-modern-identity-trust-policy", moduleRef: "stackkits-modern-home-identity-trust-policy-manifest"}},
			{id: "modern-device-verifier-distribution", issuerRef: "home-device-issuer", from: {selector: "control-authority-site"}, to: {selector: "cloud-sites"}, materials: ["revocation-state", "verification-key-reference"], includesSigningAuthority: false, includesEnrollmentAuthority: false, includesPrivateKeyMaterial: false, includesCredentialMaterial: false, reverseAllowed: false, maxStalenessSeconds: 300, owner: {kind: "catalog", providerRef: "stackkits-modern-identity-trust-policy", moduleRef: "stackkits-modern-home-identity-trust-policy-manifest"}},
			{id: "modern-workload-verifier-distribution", issuerRef: "home-workload-issuer", from: {selector: "control-authority-site"}, to: {selector: "cloud-sites"}, materials: ["revocation-state", "verification-key-reference"], includesSigningAuthority: false, includesEnrollmentAuthority: false, includesPrivateKeyMaterial: false, includesCredentialMaterial: false, reverseAllowed: false, maxStalenessSeconds: 300, owner: {kind: "catalog", providerRef: "stackkits-modern-identity-trust-policy", moduleRef: "stackkits-modern-home-identity-trust-policy-manifest"}},
		]
	}
	generation: {
		defaultStrategy: "kit-template"
		allowedStrategies: ["kit-template", "module-fragments"]
		defaultTarget: "opentofu"
		allowedTargets: ["opentofu", "compose"]
		contractVersion: "1.0.0"
	}
	network: {
		mode:           "hybrid"
		domainRequired: true
		defaultTLSMode: "public"
	}
	authoring: {
		contractVersion:   "1.0.0"
		initialSpecStatus: "preview"
		requiredOverrides: ["network.domain.base"]
		initialSpec: {
			apiVersion: "stackkit/v2alpha1"
			kind:       "StackSpec"
			metadata: name: "modern-homelab-preview"
			source: kind:   "native-v2"
			kit: slug:      "modern-homelab"
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
				mode: "hybrid"
				domain: base: "example.invalid"
				transport: {}
				dns: {}
				tls: defaultMode: "public"
			}
			sites: [{
				id:            "home"
				kind:          "home"
				failureDomain: "home-primary"
			}, {
				id:            "cloud"
				kind:          "cloud"
				failureDomain: "cloud-primary"
			}]
			nodes: [{
				id:      "home-main"
				siteRef: "home"
				roles: ["controller", "worker"]
				hardware: {}
				failureDomain: "node-home-main"
			}, {
				id:      "cloud-edge"
				siteRef: "cloud"
				roles: ["edge", "worker"]
				hardware: {}
				failureDomain: "node-cloud-edge"
			}]
			controlPlane: {
				mode:             "single"
				authoritySiteRef: "home"
				members: ["home-main"]
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
				hardwareBackedKey:         "required"
				revocationSupported:       true
				credentialTTLSeconds:      3600
			}
			partitionPolicy: {
				onCloudLoss:                     "local-continues"
				onLinkLoss:                      "local-continues"
				cloudEdge:                       "fail-closed"
				localIdentityAuthorityAvailable: true
				maxStaleVerificationSeconds:     300
				denyNewCrossSiteSessions:        true
			}
			bridge: {
				overlay: {
					contractRef: "outbound-private-mesh"
					trafficMode: "management-only"
					peerSiteRefs: ["home", "cloud"]
				}
				publications: []
				policy: {
					defaultDeny: true
					allowedFlows: []
					allowRFC1918Transit:            false
					cloudMayEnrollDevices:          false
					cloudMayIssueDeviceCredentials: false
				}
				controlAgent: {
					enabled: true
					actionAllowlist: ["plan", "apply", "verify"]
				}
			}
			data: defaultAuthority: "home"
		}
	}
	bridge: {required: true, sourceKinds: ["home"], edgeKinds: ["cloud"]}
	evidenceScenarios: ["SK-S4"]
}

#ModernHomelabStackV2: base.#KitSpecBinding & {definition: Definition}

#ModernHomelabAuthoringBinding: base.#KitSpecBinding & {
	definition: Definition
	spec:       Definition.authoring.initialSpec
}
