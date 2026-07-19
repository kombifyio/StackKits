// Package base - authority-owned ResolvedPlan to KitDefinition binding.
package base

import "list"

// #ResolvedPlanDefinitionBinding binds a persisted plan to the exact,
// service-selected KitDefinition that owns its kit slug. The definition is a
// validation input supplied by the authority service; it is deliberately not
// part of the persisted ResolvedPlan document.
//
// This binding covers definition-observable plan semantics. It binds the
// resolved data authority itself; whether it originated as an explicit value or
// a compiler-applied default is intentionally irrelevant at this boundary.
#ResolvedPlanDefinitionBinding: {
	definition: #KitDefinition
	let resolvedControlPlane = plan.controlPlane
	plan: #ResolvedPlan & {
		kit: {
			slug:    definition.metadata.slug
			version: definition.metadata.version
		}

		failurePolicy: definition.partitionPolicy
		generation: {
			contractVersion: definition.generation.contractVersion
		}
		network: configuration: {
			mode: definition.network.mode
			tls: defaultMode: definition.network.defaultTLSMode
		}
		identity: {
			humanAuthoritySiteRef:   resolvedControlPlane.authoritySiteRef
			possessionBoundSessions: definition.accessDefaults.deviceBoundSessions
			lanLocationIsIdentity:   definition.accessDefaults.lanLocationIsIdentity
		}
		identityTrust: {
			authorities: [for authority in definition.identityTrust.authorities {
				id:             authority.id
				principal:      authority.principal
				trustDomainRef: authority.trustDomainRef
				owner:          authority.owner
				if authority.placement.selector == "control-authority-site" {
					placement: {kind: "sites", siteRefs: [resolvedControlPlane.authoritySiteRef]}
				}
				if authority.placement.selector == "cloud-sites" {
					placement: {kind: "sites", siteRefs: [for site in plan.sites if site.kind == "cloud" {site.id}] & list.MinItems(1)}
				}
				if authority.placement.selector == "external" {
					placement: {kind: "external", contractRef: authority.placement.contractRef}
				}
			}]
			credentialIssuers: [for credentialIssuer in definition.identityTrust.credentialIssuers {
				id:           credentialIssuer.id
				authorityRef: credentialIssuer.authorityRef
				principal:    credentialIssuer.principal
				issuer:       "urn:stackkit:\(plan.stackId):issuer:\(credentialIssuer.issuerRef)"
				audiences: [for audienceRef in credentialIssuer.audienceRefs {"urn:stackkit:\(plan.stackId):audience:\(audienceRef)"}]
				verificationKeySetRef:         "urn:stackkit:\(plan.stackId):keyset:\(credentialIssuer.verificationKeySetRef)"
				owner:                         credentialIssuer.owner
				issuanceWithinStackKit:        credentialIssuer.issuanceWithinStackKit
				credentialTTLSeconds:          credentialIssuer.credentialTTLSeconds
				sessionTTLSeconds:             credentialIssuer.sessionTTLSeconds
				proofOfPossessionRequired:     credentialIssuer.proofOfPossessionRequired
				revocationSupported:           credentialIssuer.revocationSupported
				revocationMaxStalenessSeconds: credentialIssuer.revocationMaxStalenessSeconds
				enrollment:                    credentialIssuer.enrollment
				if credentialIssuer.placement.selector == "control-authority-site" {
					placement: {kind: "sites", siteRefs: [resolvedControlPlane.authoritySiteRef]}
				}
				if credentialIssuer.placement.selector == "cloud-sites" {
					placement: {kind: "sites", siteRefs: [for site in plan.sites if site.kind == "cloud" {site.id}] & list.MinItems(1)}
				}
				if credentialIssuer.placement.selector == "external" {
					placement: {kind: "external", contractRef: credentialIssuer.placement.contractRef}
				}
			}]
			verifierPlacements: [for verifier in definition.identityTrust.verifierPlacements {
				id: verifier.id
				credentialIssuerRef: [for issuer in definition.identityTrust.credentialIssuers if issuer.issuerRef == verifier.issuerRef {issuer.id}][0]
				issuer:    "urn:stackkit:\(plan.stackId):issuer:\(verifier.issuerRef)"
				principal: verifier.principal
				audiences: [for audienceRef in verifier.audienceRefs {"urn:stackkit:\(plan.stackId):audience:\(audienceRef)"}]
				verificationKeySetRef:         "urn:stackkit:\(plan.stackId):keyset:\(verifier.verificationKeySetRef)"
				owner:                         verifier.owner
				proofOfPossessionRequired:     verifier.proofOfPossessionRequired
				revocationMaxStalenessSeconds: verifier.revocationMaxStalenessSeconds
				if verifier.placement.selector == "control-authority-site" {
					placement: {kind: "sites", siteRefs: [resolvedControlPlane.authoritySiteRef]}
				}
				if verifier.placement.selector == "cloud-sites" {
					placement: {kind: "sites", siteRefs: [for site in plan.sites if site.kind == "cloud" {site.id}] & list.MinItems(1)}
				}
			}]
			verifierDistributions: [for distribution in definition.identityTrust.verifierDistributions {
				id: distribution.id
				credentialIssuerRef: [for issuer in definition.identityTrust.credentialIssuers if issuer.issuerRef == distribution.issuerRef {issuer.id}][0]
				issuer: "urn:stackkit:\(plan.stackId):issuer:\(distribution.issuerRef)"
				from: {kind: "sites", siteRefs: [resolvedControlPlane.authoritySiteRef]}
				to: {kind: "sites", siteRefs: [for site in plan.sites if site.kind == "cloud" {site.id}] & list.MinItems(1)}
				materials:                   distribution.materials
				includesSigningAuthority:    distribution.includesSigningAuthority
				includesEnrollmentAuthority: distribution.includesEnrollmentAuthority
				includesPrivateKeyMaterial:  distribution.includesPrivateKeyMaterial
				includesCredentialMaterial:  distribution.includesCredentialMaterial
				reverseAllowed:              distribution.reverseAllowed
				maxStalenessSeconds:         distribution.maxStalenessSeconds
				owner:                       distribution.owner
			}]
		}
		availability: mode: resolvedControlPlane.mode
	}

	_identityTrustComponents: list.Concat([
		plan.identityTrust.authorities,
		plan.identityTrust.credentialIssuers,
		plan.identityTrust.verifierPlacements,
		plan.identityTrust.verifierDistributions,
	])
	integrity: identityTrustCatalogOwners: [for component in _identityTrustComponents if component.owner.kind == "catalog" {
		componentID: component.id
		providerMatches: [for catalogProvider in plan.providers if catalogProvider.id == component.owner.providerRef {catalogProvider.id}] & list.MinItems(1) & list.MaxItems(1)
		moduleMatches: [for catalogModule in plan.modules if catalogModule.id == component.owner.moduleRef && catalogModule.providerRef == component.owner.providerRef {catalogModule.id}] & list.MinItems(1) & list.MaxItems(1)
	}]
	if plan.access != _|_ {
		integrity: identityTrustAccessVerifiers: [
			for policyID, policy in plan.access
			for principal in ["human", "device", "workload"]
			if (principal == "human" && (policy.authentication == "human" || policy.authentication == "human+device")) ||
				(principal == "device" && (policy.authentication == "device" || policy.authentication == "human+device")) ||
				(principal == "workload" && policy.authentication == "workload") {
				policyRef:     policyID
				principalKind: principal
				matches: [for verifier in plan.identityTrust.verifierPlacements if verifier.principal == principal {verifier.id}] & list.MinItems(1)
			},
		]
	}
	integrity: identityTrustRouteVerifiers: [
		for route in plan.network.routes
		for principal in ["human", "device", "workload"]
		if (principal == "human" && (route.access.authentication == "human" || route.access.authentication == "human+device")) ||
			(principal == "device" && (route.access.authentication == "device" || route.access.authentication == "human+device")) ||
			(principal == "workload" && route.access.authentication == "workload") {
			routeRef:      route.id
			principalKind: principal
			matches: [for verifier in plan.identityTrust.verifierPlacements if verifier.principal == principal && list.Contains(verifier.placement.siteRefs, route.originSiteRef) {verifier.id}] & list.MinItems(1)
		},
	]
	if plan.bridge != _|_ {
		integrity: identityTrustPublicationVerifiers: [
			for publication in plan.bridge.publications
			for principal in ["human", "device", "workload"]
			if (principal == "human" && (publication.access.authentication == "human" || publication.access.authentication == "human+device")) ||
				(principal == "device" && (publication.access.authentication == "device" || publication.access.authentication == "human+device")) ||
				(principal == "workload" && publication.access.authentication == "workload") {
				serviceRef:    publication.serviceRef
				principalKind: principal
				matches: [for verifier in plan.identityTrust.verifierPlacements if verifier.principal == principal && list.Contains(verifier.placement.siteRefs, publication.edgeSiteRef) {verifier.id}] & list.MinItems(1)
			},
		]
	}
	if plan.controlPlane.mode == "warm-standby" {
		let selectedAvailabilityPolicy = definition.availability.policies["warm-standby"]
		plan: availability: {
			policyRef:           selectedAvailabilityPolicy.policyRef
			realizationRef:      selectedAvailabilityPolicy.realizationRef
			providerRef:         selectedAvailabilityPolicy.providerRef
			moduleRef:           selectedAvailabilityPolicy.moduleRef
			selector:            selectedAvailabilityPolicy.selector
			failureModel:        selectedAvailabilityPolicy.failureModel
			healthAcceptance:    selectedAvailabilityPolicy.healthAcceptance
			evidenceAcceptance:  selectedAvailabilityPolicy.evidenceAcceptance
			rpoSeconds:          int & >=0 & <=selectedAvailabilityPolicy.limits.maxRpoSeconds
			rtoSeconds:          int & >0 & <=selectedAvailabilityPolicy.limits.maxRtoSeconds
			failureDomainSpread: int & >=selectedAvailabilityPolicy.limits.minFailureDomainSpread & <=selectedAvailabilityPolicy.limits.maxFailureDomainSpread
		}
		integrity: availabilityFencing: [for allowed in selectedAvailabilityPolicy.limits.allowedFencing if plan.availability.fencing == allowed {allowed}] & list.MinItems(1) & list.MaxItems(1)
	}
	if plan.controlPlane.mode == "quorum" {
		let selectedAvailabilityPolicy = definition.availability.policies.quorum
		plan: availability: {
			policyRef:           selectedAvailabilityPolicy.policyRef
			realizationRef:      selectedAvailabilityPolicy.realizationRef
			providerRef:         selectedAvailabilityPolicy.providerRef
			moduleRef:           selectedAvailabilityPolicy.moduleRef
			selector:            selectedAvailabilityPolicy.selector
			failureModel:        selectedAvailabilityPolicy.failureModel
			healthAcceptance:    selectedAvailabilityPolicy.healthAcceptance
			evidenceAcceptance:  selectedAvailabilityPolicy.evidenceAcceptance
			rpoSeconds:          int & >=0 & <=selectedAvailabilityPolicy.limits.maxRpoSeconds
			rtoSeconds:          int & >0 & <=selectedAvailabilityPolicy.limits.maxRtoSeconds
			failureDomainSpread: (3 | 5 | 7) & >=selectedAvailabilityPolicy.limits.minFailureDomainSpread & <=selectedAvailabilityPolicy.limits.maxFailureDomainSpread
		}
		integrity: availabilityFencing: [for allowed in selectedAvailabilityPolicy.limits.allowedFencing if plan.availability.fencing == allowed {allowed}] & list.MinItems(1) & list.MaxItems(1)
	}

	// Site shape and control authority are kit identity, not runtime context.
	plan: sites: list.MinItems(definition.topology.minSites) & list.MaxItems(definition.topology.maxSites)
	integrity: siteKinds: [for site in plan.sites {
		siteRef: site.id
		matches: [for allowed in definition.topology.allowedSiteKinds if site.kind == allowed {allowed}] & list.MinItems(1) & list.MaxItems(1)
	}]
	integrity: requiredSiteKinds: [for required in definition.topology.requiredSiteKinds {
		kind: required
		matches: [for site in plan.sites if site.kind == required {site.id}] & list.MinItems(1)
	}]
	integrity: nodeSites: [for node in plan.nodes {
		nodeRef: node.id
		matches: [for site in plan.sites if site.id == node.siteRef {site.id}] & list.MinItems(1) & list.MaxItems(1)
	}]

	integrity: controlMode: [for allowed in definition.topology.controlPlane.allowedModes if plan.controlPlane.mode == allowed {allowed}] & list.MinItems(1) & list.MaxItems(1)
	integrity: controlAuthorityKind: [
		for site in plan.sites
		if site.id == plan.controlPlane.authoritySiteRef
		for allowed in definition.topology.controlPlane.allowedAuthorityKinds
		if site.kind == allowed {site.id},
	] & list.MinItems(1) & list.MaxItems(1)
	_enabledControllerIDs: [for node in plan.nodes if node.enabled for role in node.roles if role == "controller" {node.id}]
	integrity: controlMembersAreEnabledControllers: [for member in plan.controlPlane.members {
		node: member
		matches: [for controller in _enabledControllerIDs if controller == member {controller}] & list.MinItems(1) & list.MaxItems(1)
	}]
	if definition.topology.controlPlane.memberSiteScope == "authority-site" {
		integrity: controlMembersAtAuthoritySite: [for member in plan.controlPlane.members {
			node: member
			matches: [
				for node in plan.nodes
				if node.id == member
				if node.siteRef == plan.controlPlane.authoritySiteRef {node.id},
			] & list.MinItems(1) & list.MaxItems(1)
		}]
	}

	if definition.dataDefaults.authority != "policy" {
		plan: data: defaultAuthority: definition.dataDefaults.authority
	}
	if definition.dataDefaults.authority == "home" if definition.dataDefaults.cloudCopyRequiresPolicy == true {
		integrity: homeAuthorityCloudCopyPolicy: #HomeAuthorityCloudCopyBindingV2 & {
			sites: plan.sites
			data:  plan.data
		}
	}

	// Required capabilities cannot be removed and recomputed away. Defaults are
	// intentionally not required because StackSpec may explicitly disable them.
	_declaredCapabilities: list.Concat([
		definition.capabilities.required,
		definition.capabilities.defaults,
		definition.capabilities.optional,
	])
	integrity: requiredCapabilities: [for required in definition.capabilities.required {
		capability: required
		matches: [for selected in plan.capabilities if selected.id == required {selected.id}] & list.MinItems(1) & list.MaxItems(1)
	}]
	integrity: selectedCapabilities: [for selected in plan.capabilities {
		capability: selected.id
		declared: [for allowed in _declaredCapabilities if selected.id == allowed {allowed}] & list.MinItems(1)
		forbidden: [for denied in definition.capabilities.forbidden if selected.id == denied {denied}] & list.MaxItems(0)
	}]

	integrity: generationStrategy: [for allowed in definition.generation.allowedStrategies if plan.generation.strategy == allowed {allowed}] & list.MinItems(1) & list.MaxItems(1)
	integrity: generationTarget: [for allowed in definition.generation.allowedTargets if plan.generation.target == allowed {allowed}] & list.MinItems(1) & list.MaxItems(1)
	if definition.network.domainRequired == true {
		plan: network: configuration: domain: base: string & =~"^[A-Za-z0-9-]+(\\.[A-Za-z0-9-]+)+$" & !~"(?i)(^|\\.)(localhost|local)$"
	}

	// Access and routes remain constrained by the Definition even after the
	// original StackSpec is no longer present at a persistence boundary.
	if plan.access != _|_ {
		integrity: accessExposures: [for policyID, accessPolicy in plan.access {
			policyRef: policyID
			matches: [for allowed in definition.reachability.accessPolicies.allowedExposures if accessPolicy.exposure == allowed {allowed}] & list.MinItems(1) & list.MaxItems(1)
		}]
		integrity: lanStepDown: [for policyID, accessPolicy in plan.access if accessPolicy.lanStepDown != _|_ if accessPolicy.lanStepDown == true {
			policyRef: policyID
			allowed:   definition.reachability.accessPolicies.lanStepDownAllowed & true
		}]
	}
	integrity: routeReachability: [for route in plan.network.routes {
		routeRef: route.id
		allowed:  definition.reachability.routes[route.exposure].allowed & true
		originKind: [
			for site in plan.sites
			if site.id == route.originSiteRef
			for allowed in definition.reachability.routes[route.exposure].allowedOriginKinds
			if site.kind == allowed {site.kind},
		] & list.MinItems(1) & list.MaxItems(1)
		requiredCapabilities: [for required in definition.reachability.routes[route.exposure].requiredCapabilities {
			capability: required
			matches: [for selected in plan.capabilities if selected.id == required {selected.id}] & list.MinItems(1) & list.MaxItems(1)
		}]
	}]
	if len(plan.network.routes) > 0 {
		plan: access: [string]: #AccessPolicyV2
	}
	integrity: routeAccessPolicies: [for route in plan.network.routes {
		routeRef: route.id
		references: [
			for policyID, _ in plan.access
			if route.access.policyRef == policyID {policyID},
		] & list.MinItems(1) & list.MaxItems(1)
		exposureMatches: [
			for policyID, accessPolicy in plan.access
			if route.access.policyRef == policyID
			if (route.exposure == "local" && (accessPolicy.exposure == "private" || accessPolicy.exposure == "lan")) ||
				(route.exposure == "remote-private" && accessPolicy.exposure == "private") ||
				(route.exposure == "public" && accessPolicy.exposure == "public") {policyID},
		] & list.MinItems(1) & list.MaxItems(1)
		authenticationMatches: [
			for policyID, accessPolicy in plan.access
			if route.access.policyRef == policyID
			if route.access.authentication == accessPolicy.authentication {policyID},
		] & list.MinItems(1) & list.MaxItems(1)
		privilegeMatches: [
			for policyID, accessPolicy in plan.access
			if route.access.policyRef == policyID
			if route.access.privilege == accessPolicy.privilege {policyID},
		] & list.MinItems(1) & list.MaxItems(1)
		enrolledDeviceMatches: [
			for policyID, accessPolicy in plan.access
			if route.access.policyRef == policyID
			if route.access.enrolledDeviceRequired == accessPolicy.enrolledDeviceRequired {policyID},
		] & list.MinItems(1) & list.MaxItems(1)
		ownerStepUpMatches: [
			for policyID, accessPolicy in plan.access
			if route.access.policyRef == policyID
			if route.access.ownerStepUpRequired == accessPolicy.ownerStepUpRequired {policyID},
		] & list.MinItems(1) & list.MaxItems(1)
		policyExposureMatches: [
			for policyID, accessPolicy in plan.access
			if route.access.policyRef == policyID
			if route.access.policyExposure == accessPolicy.exposure {policyID},
		] & list.MinItems(1) & list.MaxItems(1)
		lanStepDownMatches: [
			for policyID, accessPolicy in plan.access
			if route.access.policyRef == policyID
			if route.access.lanStepDown == accessPolicy.lanStepDown {policyID},
		] & list.MinItems(1) & list.MaxItems(1)
		allowedSiteRefsForward: [
			for policyID, accessPolicy in plan.access
			if route.access.policyRef == policyID
			if accessPolicy.allowedSiteRefs != _|_ {
				policyRef: policyID
				value:     route.access.allowedSiteRefs & accessPolicy.allowedSiteRefs
			},
		]
		if route.access.allowedSiteRefs != _|_ {
			allowedSiteRefsReverse: [
				for policyID, accessPolicy in plan.access
				if route.access.policyRef == policyID
				if accessPolicy.allowedSiteRefs != _|_ {
					policyRef: policyID
					value:     route.access.allowedSiteRefs & accessPolicy.allowedSiteRefs
				},
			] & list.MinItems(1) & list.MaxItems(1)
		}
		allowedMethodsForward: [
			for policyID, accessPolicy in plan.access
			if route.access.policyRef == policyID
			if accessPolicy.allowedMethods != _|_ {
				policyRef: policyID
				value:     route.access.allowedMethods & accessPolicy.allowedMethods
			},
		]
		if route.access.allowedMethods != _|_ {
			allowedMethodsReverse: [
				for policyID, accessPolicy in plan.access
				if route.access.policyRef == policyID
				if accessPolicy.allowedMethods != _|_ {
					policyRef: policyID
					value:     route.access.allowedMethods & accessPolicy.allowedMethods
				},
			] & list.MinItems(1) & list.MaxItems(1)
		}
	}]
	integrity: routeTLS: [for route in plan.network.routes {
		if route.tls.required == true if plan.network.configuration.tls.defaultMode == "internal" {
			mode: route.tls.mode & "internal"
		}
		if route.tls.required == true if plan.network.configuration.tls.defaultMode == "public" {
			mode: route.tls.mode & "terminate-at-edge"
		}
		if route.tls.required == true {
			minVersion: route.tls.minVersion & plan.network.configuration.tls.minVersion
		}
	}]

	// Device authority remains local to the site kinds declared by the kit.
	if definition.deviceEnrollment.required == true {
		plan: identity: {
			deviceAuthoritySiteRef: string
			deviceEnrollment: #DeviceEnrollmentPolicy & {
				mode:             definition.deviceEnrollment.mode
				authoritySiteRef: plan.identity.deviceAuthoritySiteRef
			}
		}
		integrity: deviceEnrollmentAuthorityKinds: [
			for site in plan.sites
			if site.id == plan.identity.deviceAuthoritySiteRef
			for allowed in definition.deviceEnrollment.authorityKinds
			if site.kind == allowed {site.id},
		] & list.MinItems(1) & list.MaxItems(1)
	}
	if definition.deviceEnrollment.required == false {
		plan: identity: {
			deviceAuthoritySiteRef?: _|_
			deviceEnrollment?:       _|_
		}
	}

	// A required mixed-site bridge cannot be removed from a rehashed Modern
	// Home Lab plan. When source/edge kinds are declared, every publication and
	// at least one overlay peer on each side must retain the correct site role.
	if definition.bridge.required == true {
		plan: bridge: #BridgeContract
	}
	if plan.bridge != _|_ {
		integrity: overlayPeerRefs: [for peer in plan.bridge.overlay.peerSiteRefs {
			site: peer
			matches: [for site in plan.sites if site.id == peer {site.id}] & list.MinItems(1) & list.MaxItems(1)
		}]
		integrity: publicationRefs: [for publication in plan.bridge.publications {
			service: publication.serviceRef
			source: [for site in plan.sites if site.id == publication.sourceSiteRef {site.id}] & list.MinItems(1) & list.MaxItems(1)
			edge: [for site in plan.sites if site.id == publication.edgeSiteRef {site.id}] & list.MinItems(1) & list.MaxItems(1)
		}]
		integrity: bridgeFlowRefs: [for flow in plan.bridge.policy.allowedFlows {
			service: flow.serviceRef
			from: [for site in plan.sites if site.id == flow.fromSiteRef {site.id}] & list.MinItems(1) & list.MaxItems(1)
			to: [for site in plan.sites if site.id == flow.toSiteRef {site.id}] & list.MinItems(1) & list.MaxItems(1)
		}]
		if len(plan.bridge.publications) > 0 {
			plan: access: [string]: #AccessPolicyV2
			plan: data: bindings: [string]: {
				classes: [...#DataClass] & list.MinItems(1)
				primarySiteRef: #SiteID
				replicaSiteRefs?: [...#SiteID]
				cloudCopyAllowed: bool | *false
			}
			integrity: publicationEndpoints: [for publication in plan.bridge.publications {
				service: publication.serviceRef
				matches: [
					for module in plan.modules
					for unit in module.renderUnits
					for endpoint in unit.serviceEndpoints
					if endpoint.serviceRef == publication.serviceRef
					for allowedExposure in endpoint.allowedExposures
					if allowedExposure == "public"
					for allowedProtocol in endpoint.allowedIngressProtocols
					if allowedProtocol == publication.protocol
					if endpoint.upstreamProtocol == publication.upstreamProtocol && endpoint.targetPort == publication.targetPort
					if endpoint.data.bindingRef == publication.dataBindingRef {
						moduleRef:     publication.moduleRef & module.id
						unitRef:       publication.unitRef & unit.id
						healthGateRef: publication.healthGateRef & "module-\(module.id)-\(endpoint.healthRef)"
						originNodes: [for originNodeRef in publication.originNodeRefs {
							node: originNodeRef
							matches: [
								for unitNodeRef in unit.nodeRefs
								for node in plan.nodes
								if unitNodeRef == originNodeRef && node.id == unitNodeRef && node.siteRef == publication.sourceSiteRef {unitNodeRef},
							] & list.MinItems(1) & list.MaxItems(1)
						}]
						originNodesComplete: [
							for unitNodeRef in unit.nodeRefs
							for node in plan.nodes
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
							authoritySite: publication.sourceSiteRef & plan.controlPlane.authoritySiteRef
						}
						if endpoint.data.locality == "primary-site" {
							dataPrimarySite: [
								for bindingID, binding in plan.data.bindings
								if bindingID == endpoint.data.bindingRef && binding.primarySiteRef == publication.sourceSiteRef {bindingID},
							] & list.MinItems(1) & list.MaxItems(1)
						}
						if endpoint.data.locality == "primary-or-replica" {
							dataOriginSite: [
								for bindingID, binding in plan.data.bindings
								if bindingID == endpoint.data.bindingRef && binding.primarySiteRef == publication.sourceSiteRef {bindingID},
								for bindingID, binding in plan.data.bindings
								if bindingID == endpoint.data.bindingRef
								for replicaSiteRef in binding.replicaSiteRefs
								if replicaSiteRef == publication.sourceSiteRef {bindingID},
							] & list.MinItems(1) & list.MaxItems(1)
						}
					},
				] & list.MinItems(1) & list.MaxItems(1)
			}]
			integrity: publicationPolicyRefs: [for publication in plan.bridge.publications {
				service: publication.serviceRef
				policy:  publication.auth.policyRef
				matches: [
					for policyID, policy in plan.access
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
			integrity: publicationAccessMethods: [
				for publication in plan.bridge.publications
				for policyID, policy in plan.access
				if policyID == publication.auth.policyRef
				for allowed in policy.allowedMethods {
					service:    publication.serviceRef
					httpMethod: allowed
					matches: [for resolvedMethod in publication.access.allowedMethods if resolvedMethod == allowed {resolvedMethod}] & list.MinItems(1) & list.MaxItems(1)
				},
			]
			integrity: publicationFlowContracts: [for publication in plan.bridge.publications {
				service: publication.serviceRef
				matches: [
					for flow in plan.bridge.policy.allowedFlows
					if flow.fromSiteRef == publication.edgeSiteRef
					if flow.toSiteRef == publication.sourceSiteRef
					if flow.serviceRef == publication.serviceRef
					if flow.protocol == publication.upstreamProtocol
					if len(flow.ports) == 1
					for port in flow.ports
					if port == publication.targetPort
					if flow.protocol == "tcp" || flow.protocol == "udp" {flow.serviceRef},
					for flow in plan.bridge.policy.allowedFlows
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
			integrity: publicationDataContracts: [for publication in plan.bridge.publications {
				service: publication.serviceRef
				matches: [
					for bindingID, binding in plan.data.bindings
					if bindingID == publication.serviceRef
					if binding.primarySiteRef == publication.sourceSiteRef
					if binding.cloudCopyAllowed == false {bindingID},
				] & list.MinItems(1) & list.MaxItems(1)
			}]
		}
		if len(plan.bridge.policy.allowedFlows) > 0 {
			integrity: bridgeFlowEndpoints: [for flow in plan.bridge.policy.allowedFlows {
				service: flow.serviceRef
				matches: [
					for module in plan.modules
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
						service: endpoint.serviceRef
						originNodes: [
							for unitNodeRef in unit.nodeRefs
							for node in plan.nodes
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
							authoritySite: flow.toSiteRef & plan.controlPlane.authoritySiteRef
						}
						if endpoint.data.locality == "primary-site" {
							dataPrimarySite: [
								for bindingID, binding in plan.data.bindings
								if bindingID == endpoint.data.bindingRef && binding.primarySiteRef == flow.toSiteRef {bindingID},
							] & list.MinItems(1) & list.MaxItems(1)
						}
						if endpoint.data.locality == "primary-or-replica" {
							dataOriginSite: [
								for bindingID, binding in plan.data.bindings
								if bindingID == endpoint.data.bindingRef && binding.primarySiteRef == flow.toSiteRef {bindingID},
								for bindingID, binding in plan.data.bindings
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
							for publication in plan.bridge.publications
							if publication.edgeSiteRef == flow.fromSiteRef && publication.sourceSiteRef == flow.toSiteRef
							if publication.serviceRef == flow.serviceRef && publication.moduleRef == module.id && publication.unitRef == unit.id
							if publication.upstreamProtocol == flow.protocol && publication.targetPort == endpoint.targetPort
							if publication.dataBindingRef == endpoint.data.bindingRef
							for allowedProtocol in endpoint.allowedIngressProtocols
							if allowedProtocol == publication.protocol
							if flow.protocol == "tcp" || flow.protocol == "udp" {"public"},
							for allowedExposure in endpoint.allowedExposures
							if allowedExposure == "public"
							for publication in plan.bridge.publications
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
		}
	}
	if plan.bridge != _|_ if len(definition.bridge.sourceKinds) > 0 {
		integrity: overlaySourcePeers: [
			for peer in plan.bridge.overlay.peerSiteRefs
			for site in plan.sites
			if site.id == peer
			for allowed in definition.bridge.sourceKinds
			if site.kind == allowed {site.id},
		] & list.MinItems(1)
		integrity: bridgeSourceKinds: [for publication in plan.bridge.publications {
			service: publication.serviceRef
			matches: [
				for site in plan.sites
				if site.id == publication.sourceSiteRef
				for allowed in definition.bridge.sourceKinds
				if site.kind == allowed {allowed},
			] & list.MinItems(1) & list.MaxItems(1)
		}]
	}
	if plan.bridge != _|_ if len(definition.bridge.edgeKinds) > 0 {
		integrity: overlayEdgePeers: [
			for peer in plan.bridge.overlay.peerSiteRefs
			for site in plan.sites
			if site.id == peer
			for allowed in definition.bridge.edgeKinds
			if site.kind == allowed {site.id},
		] & list.MinItems(1)
		integrity: bridgeEdgeKinds: [for publication in plan.bridge.publications {
			service: publication.serviceRef
			matches: [
				for site in plan.sites
				if site.id == publication.edgeSiteRef
				for allowed in definition.bridge.edgeKinds
				if site.kind == allowed {allowed},
			] & list.MinItems(1) & list.MaxItems(1)
		}]
	}

	_expectedEdgeVerifierSiteRefs: [
		for site in plan.sites
		for edgeKind in definition.bridge.edgeKinds
		if site.kind == edgeKind {site.id},
	]
	if len(_expectedEdgeVerifierSiteRefs) > 0 {
		plan: identity: edgeVerifierSiteRefs: [...#SiteID] & list.MinItems(1)
		integrity: edgeVerifierRefsUnique: list.UniqueItems(plan.identity.edgeVerifierSiteRefs) & true
		integrity: edgeVerifierCountExact: len(plan.identity.edgeVerifierSiteRefs) & len(_expectedEdgeVerifierSiteRefs)
		integrity: edgeVerifierRefsExact: [for expected in _expectedEdgeVerifierSiteRefs {
			site: expected
			matches: [for actual in plan.identity.edgeVerifierSiteRefs if actual == expected {actual}] & list.MinItems(1) & list.MaxItems(1)
		}]
	}
	if len(_expectedEdgeVerifierSiteRefs) == 0 {
		plan: identity: edgeVerifierSiteRefs?: _|_
	}
}
