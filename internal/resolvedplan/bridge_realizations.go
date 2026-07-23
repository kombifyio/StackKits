package resolvedplan

import (
	"fmt"
	"sort"
)

// resolveBridgePlan turns caller intent into an executable publication
// projection. The public listener remains intent-owned; every backend field is
// reconstructed from the selected module service endpoint.
//
//nolint:gocyclo // Bridge realization atomically validates and derives every publication boundary before returning a plan.
func resolveBridgePlan(bridge map[string]any, modules []any, selectedSiteRefs []string, authoritySiteRef string, nodeSites map[string]string, data, access map[string]any, providers []any) (map[string]any, error) {
	resolved, err := cloneObject(bridge, false)
	if err != nil {
		return nil, err
	}
	publications, err := objectListOptional(bridge, "publications")
	if err != nil {
		return nil, err
	}
	policy, err := objectField(bridge, "bridge", "policy")
	if err != nil {
		return nil, err
	}
	flows, err := objectListField(policy, "bridge.policy", "allowedFlows")
	if err != nil {
		return nil, err
	}
	overlay, err := objectField(bridge, "bridge", "overlay")
	if err != nil {
		return nil, err
	}
	resolvedOverlay, resolvedControl, err := resolveBridgeCatalogAuthority(bridge, providers)
	if err != nil {
		return nil, err
	}
	resolved["overlay"] = resolvedOverlay
	resolved["controlAgent"] = resolvedControl
	peerSiteRefs, err := stringListField(overlay, "bridge.overlay", "peerSiteRefs", true)
	if err != nil {
		return nil, err
	}
	endpointIndex, err := indexResolvedServiceEndpoints(modules)
	if err != nil {
		return nil, err
	}
	if err := validateBridgePolicyFlows(flows, selectedSiteRefs, peerSiteRefs, endpointIndex, authoritySiteRef, nodeSites, data); err != nil {
		return nil, err
	}
	resolvedPublications := make([]any, 0, len(publications))
	for index, publication := range publications {
		path := fmt.Sprintf("bridge.publications[%d]", index)
		serviceRef, err := stringField(publication, path, "serviceRef")
		if err != nil {
			return nil, err
		}
		endpoint, err := resolvePublicationServiceEndpoint(endpointIndex, serviceRef, authoritySiteRef, nodeSites, path)
		if err != nil {
			return nil, err
		}
		sourceSiteRef, err := stringField(publication, path, "sourceSiteRef")
		if err != nil {
			return nil, err
		}
		if len(endpoint.siteRefs) != 1 || endpoint.siteRefs[0] != sourceSiteRef {
			return nil, fail(ErrUnresolvedPlacement, path+".sourceSiteRef", "publication source %q is not the catalog-owned service origin %v", sourceSiteRef, endpoint.siteRefs)
		}
		protocol, err := stringField(publication, path, "protocol")
		if err != nil {
			return nil, err
		}
		if !contains(endpoint.allowedIngressProtocols, protocol) || !contains(endpoint.allowedExposures, "public") {
			return nil, fail(ErrContractConflict, path+".serviceRef", "service endpoint %q does not allow %q public publication", serviceRef, protocol)
		}
		if endpoint.data == nil {
			return nil, fail(ErrContractConflict, path+".serviceRef", "published service endpoint %q must declare a governed data contract", serviceRef)
		}
		if endpoint.data.bindingRef != serviceRef {
			return nil, fail(ErrContractConflict, path+".serviceRef", "published service endpoint %q must bind the same data authority key, got %q", serviceRef, endpoint.data.bindingRef)
		}
		if err := validateServiceEndpointData(endpoint, data, "data", path); err != nil {
			return nil, err
		}
		auth, err := objectField(publication, path, "auth")
		if err != nil {
			return nil, err
		}
		authRequired, err := boolFieldDefault(auth, path+".auth", "required", false)
		if err != nil {
			return nil, err
		}
		if !authRequired {
			return nil, fail(ErrProfileMismatch, path+".auth.required", "public publication authentication must be required")
		}
		policyRef, err := stringField(auth, path+".auth", "policyRef")
		if err != nil {
			return nil, err
		}
		rawPolicy, exists := access[policyRef]
		if !exists {
			return nil, fail(ErrProfileMismatch, path+".auth.policyRef", "access policy %q does not exist", policyRef)
		}
		accessDecision, err := resolveAccessDecision(path+".auth.policyRef", "public", policyRef, rawPolicy)
		if err != nil {
			return nil, err
		}
		if err := validateServiceEndpointAccess(endpoint, accessDecision, path+".access"); err != nil {
			return nil, err
		}
		authentication, err := stringField(accessDecision, path+".access", "authentication")
		if err != nil {
			return nil, err
		}
		enrolledDeviceRequired, err := boolFieldDefault(accessDecision, path+".access", "enrolledDeviceRequired", false)
		if err != nil {
			return nil, err
		}
		if authentication != "human+device" || !enrolledDeviceRequired {
			return nil, fail(ErrProfileMismatch, path+".auth.policyRef", "every bridge publication requires human+device authentication and an enrolled device")
		}
		allowedMethods, err := stringListField(accessDecision, path+".access", "allowedMethods", true)
		if err != nil {
			return nil, fail(ErrProfileMismatch, path+".auth.policyRef", "public publication policy must declare a non-empty allowedMethods set")
		}
		if err := validatePublicationBackendFlow(flows, publication, endpoint, allowedMethods, path); err != nil {
			return nil, err
		}
		projection, err := cloneObject(publication, false)
		if err != nil {
			return nil, err
		}
		projection["moduleRef"] = endpoint.moduleRef
		projection["unitRef"] = endpoint.unitRef
		projection["originNodeRefs"] = stringSliceAny(endpoint.nodeRefs)
		projection["originInstanceRefs"] = stringSliceAny(endpoint.instanceRefs)
		originTargets := make([]any, 0, len(endpoint.instanceRefs))
		for _, instanceRef := range endpoint.instanceRefs {
			nodeRef := endpoint.instanceNodes[instanceRef]
			if nodeRef == "" {
				return nil, fail(ErrUnresolvedPlacement, path+".serviceRef", "service endpoint instance %q has no exact node custody", instanceRef)
			}
			originTargets = append(originTargets, map[string]any{"nodeRef": nodeRef, "instanceRef": instanceRef})
		}
		projection["originTargets"] = originTargets
		projection["upstreamProtocol"] = endpoint.upstreamProtocol
		projection["targetPort"] = endpoint.targetPort
		projection["healthGateRef"] = contractID("module-" + endpoint.moduleRef + "-" + endpoint.healthRef)
		projection["dataBindingRef"] = endpoint.data.bindingRef
		projection["access"] = accessDecision
		resolvedPublications = append(resolvedPublications, projection)
	}
	if err := validateBridgeFlowEndpointReachability(flows, resolvedPublications, endpointIndex, authoritySiteRef, nodeSites); err != nil {
		return nil, err
	}
	resolved["publications"] = resolvedPublications
	return resolved, nil
}

func resolveBridgeCatalogAuthority(bridge map[string]any, providers []any) (map[string]any, map[string]any, error) {
	overlayIntent, err := objectField(bridge, "bridge", "overlay")
	if err != nil {
		return nil, nil, err
	}
	controlIntent, err := objectField(bridge, "bridge", "controlAgent")
	if err != nil {
		return nil, nil, err
	}
	linkProvider, err := selectedBridgeProvider(providers, "inter-site-link")
	if err != nil {
		return nil, nil, err
	}
	controlProvider, err := selectedBridgeProvider(providers, "outbound-control-agent")
	if err != nil {
		return nil, nil, err
	}
	overlayRef, err := stringField(overlayIntent, "bridge.overlay", "contractRef")
	if err != nil {
		return nil, nil, err
	}
	overlayContract, err := providerSubcontract(linkProvider, "overlayContracts", overlayRef, "bridge.overlay.contractRef")
	if err != nil {
		return nil, nil, err
	}
	if err := validateBridgeSubcontractOwner(linkProvider, overlayContract, "inter-site-link", "bridge.overlay.contractRef"); err != nil {
		return nil, nil, err
	}
	if err := validateOverlaySecurityContract(overlayContract, "catalog.providers.overlayContracts."+overlayRef); err != nil {
		return nil, nil, err
	}
	trafficMode, err := stringField(overlayIntent, "bridge.overlay", "trafficMode")
	if err != nil {
		return nil, nil, err
	}
	allowedModes, err := stringListField(overlayContract, "catalog.providers.overlayContracts."+overlayRef, "allowedTrafficModes", true)
	if err != nil {
		return nil, nil, err
	}
	if !contains(allowedModes, trafficMode) {
		return nil, nil, fail(ErrProfileMismatch, "bridge.overlay.trafficMode", "overlay contract %q does not allow traffic mode %q", overlayRef, trafficMode)
	}
	resolvedOverlay, err := cloneObject(overlayContract, false)
	if err != nil {
		return nil, nil, err
	}
	delete(resolvedOverlay, "id")
	delete(resolvedOverlay, "capabilityRef")
	delete(resolvedOverlay, "allowedTrafficModes")
	resolvedOverlay["contractRef"] = overlayRef
	resolvedOverlay["providerRef"] = linkProvider["id"]
	resolvedOverlay["providerContractHash"] = linkProvider["contractHash"]
	resolvedOverlay["trafficMode"] = trafficMode
	peerSiteRefs, err := stringListField(overlayIntent, "bridge.overlay", "peerSiteRefs", true)
	if err != nil {
		return nil, nil, err
	}
	resolvedOverlay["peerSiteRefs"] = stringSliceAny(peerSiteRefs)

	enabled, err := boolFieldDefault(controlIntent, "bridge.controlAgent", "enabled", false)
	if err != nil {
		return nil, nil, err
	}
	if !enabled {
		return nil, nil, fail(ErrProfileMismatch, "bridge.controlAgent.enabled", "Modern federation requires the outbound control agent")
	}
	actionRefs, err := stringListField(controlIntent, "bridge.controlAgent", "actionAllowlist", true)
	if err != nil {
		return nil, nil, err
	}
	// The CUE contract treats the allowlist as a set. Sort before deriving the
	// parallel action projection so persisted plans remain idempotent when the
	// catalog body binding recomputes the bridge from normalized CUE output.
	sort.Strings(actionRefs)
	resolvedActions := make([]any, 0, len(actionRefs))
	var controlModuleRef string
	seenActionRefs := make(map[string]struct{}, len(actionRefs))
	for index, actionRef := range actionRefs {
		path := fmt.Sprintf("bridge.controlAgent.actionAllowlist[%d]", index)
		if _, duplicate := seenActionRefs[actionRef]; duplicate {
			return nil, nil, fail(ErrContractConflict, path, "duplicate remote action %q", actionRef)
		}
		seenActionRefs[actionRef] = struct{}{}
		action, err := providerSubcontract(controlProvider, "remoteActionContracts", actionRef, path)
		if err != nil {
			return nil, nil, err
		}
		if err := validateBridgeSubcontractOwner(controlProvider, action, "outbound-control-agent", path); err != nil {
			return nil, nil, err
		}
		if err := validateRemoteActionSecurityContract(action, path); err != nil {
			return nil, nil, err
		}
		moduleRef, err := stringField(action, "catalog.providers.remoteActionContracts."+actionRef, "moduleRef")
		if err != nil {
			return nil, nil, err
		}
		if controlModuleRef == "" {
			controlModuleRef = moduleRef
		} else if controlModuleRef != moduleRef {
			return nil, nil, fail(ErrContractConflict, path, "remote actions span multiple control-agent modules")
		}
		projection, err := cloneObject(action, false)
		if err != nil {
			return nil, nil, err
		}
		projection["contractRef"] = actionRef
		projection["providerRef"] = controlProvider["id"]
		projection["providerContractHash"] = controlProvider["contractHash"]
		resolvedActions = append(resolvedActions, projection)
	}
	resolvedControl := map[string]any{
		"enabled": true, "providerRef": controlProvider["id"], "providerContractHash": controlProvider["contractHash"],
		"moduleRef": controlModuleRef, "actionAllowlist": stringSliceAny(actionRefs), "actions": resolvedActions,
	}
	return resolvedOverlay, resolvedControl, nil
}

func validateOverlaySecurityContract(contract map[string]any, path string) error {
	initiation, err := stringField(contract, path, "initiation")
	if err != nil {
		return err
	}
	outboundEstablished, err := boolFieldDefault(contract, path, "outboundEstablished", false)
	if err != nil {
		return err
	}
	advertiseDefaultRoute, err := boolFieldDefault(contract, path, "advertiseDefaultRoute", true)
	if err != nil {
		return err
	}
	advertisePrivateSubnets, err := boolFieldDefault(contract, path, "advertisePrivateSubnets", true)
	if err != nil {
		return err
	}
	allowBroadRoutes, err := boolFieldDefault(contract, path, "allowBroadRoutes", true)
	if err != nil {
		return err
	}
	if initiation != "local-outbound" || !outboundEstablished || advertiseDefaultRoute || advertisePrivateSubnets || allowBroadRoutes {
		return fail(ErrContractConflict, path, "overlay contract must remain outbound-only without default, private-subnet, or broad route advertisement")
	}
	return nil
}

func validateRemoteActionSecurityContract(contract map[string]any, path string) error {
	transport, err := stringField(contract, path, "transport")
	if err != nil {
		return err
	}
	if transport != "managed-agent" && transport != "mtls-agent" {
		return fail(ErrContractConflict, path+".transport", "unsupported remote action transport %q", transport)
	}
	if _, err := stringField(contract, path, "audience"); err != nil {
		return err
	}
	if _, err := stringField(contract, path, "issuerRef"); err != nil {
		return err
	}
	ttl, err := intField(contract, path, "maxTTLSeconds")
	if err != nil {
		return err
	}
	if ttl < 1 || ttl > 300 {
		return fail(ErrContractConflict, path+".maxTTLSeconds", "remote action TTL must be between 1 and 300 seconds")
	}
	for _, field := range []string{"capabilityScopedActions", "requiresSignedActions", "requiresNonce", "requiresIdempotencyKey", "requiresResolvedPlanHash", "replayProtection", "requiresApprovalForDestructive"} {
		required, err := boolFieldDefault(contract, path, field, false)
		if err != nil {
			return err
		}
		if !required {
			return fail(ErrContractConflict, path+"."+field, "remote action security invariant must be enabled")
		}
	}
	destructive, err := boolFieldDefault(contract, path, "destructive", false)
	if err != nil {
		return err
	}
	approvalRequired, err := boolFieldDefault(contract, path, "approvalReceiptRequired", false)
	if err != nil {
		return err
	}
	approvalClass, err := stringField(contract, path, "approvalClass")
	if err != nil {
		return err
	}
	if approvalClass != "none" && approvalClass != "owner-step-up" && approvalClass != "break-glass" {
		return fail(ErrContractConflict, path+".approvalClass", "unsupported remote action approval class %q", approvalClass)
	}
	if (destructive || approvalClass != "none") && !approvalRequired {
		return fail(ErrContractConflict, path+".approvalReceiptRequired", "destructive remote actions require an approval receipt")
	}
	if destructive && approvalClass == "none" {
		return fail(ErrContractConflict, path+".approvalClass", "destructive remote actions require owner step-up or break-glass approval")
	}
	return nil
}

func selectedBridgeProvider(providers []any, capabilityRef string) (map[string]any, error) {
	var match map[string]any
	for index, raw := range providers {
		provider, err := asObject(raw, fmt.Sprintf("resolvedPlan.providers[%d]", index))
		if err != nil {
			return nil, err
		}
		provides, err := stringListField(provider, fmt.Sprintf("resolvedPlan.providers[%d]", index), "provides", true)
		if err != nil {
			return nil, err
		}
		if !contains(provides, capabilityRef) {
			continue
		}
		if match != nil {
			return nil, fail(ErrContractConflict, "resolvedPlan.providers", "multiple selected providers claim bridge capability %q", capabilityRef)
		}
		match = provider
	}
	if match == nil {
		return nil, fail(ErrUnknownProvider, "bridge", "no selected provider owns bridge capability %q", capabilityRef)
	}
	return match, nil
}

func providerSubcontract(provider map[string]any, field, contractRef, path string) (map[string]any, error) {
	contracts, err := objectListOptional(provider, field)
	if err != nil {
		return nil, err
	}
	var match map[string]any
	for index, contract := range contracts {
		id, err := stringField(contract, fmt.Sprintf("resolvedPlan.providers.%s[%d]", field, index), "id")
		if err != nil {
			return nil, err
		}
		if id != contractRef {
			continue
		}
		if match != nil {
			return nil, fail(ErrContractConflict, path, "provider duplicates contract %q", contractRef)
		}
		match = contract
	}
	if match == nil {
		return nil, fail(ErrProfileMismatch, path, "selected provider does not own contract %q", contractRef)
	}
	return match, nil
}

func validateBridgeSubcontractOwner(provider, contract map[string]any, capabilityRef, path string) error {
	contractCapability, err := stringField(contract, path, "capabilityRef")
	if err != nil {
		return err
	}
	if contractCapability != capabilityRef {
		return fail(ErrContractConflict, path, "contract capability %q does not match selected authority %q", contractCapability, capabilityRef)
	}
	moduleRef, err := stringField(contract, path, "moduleRef")
	if err != nil {
		return err
	}
	realization, err := objectField(provider, path, "realization")
	if err != nil {
		return err
	}
	moduleRefs, err := objectField(realization, path+".realization", "moduleRefs")
	if err != nil {
		return err
	}
	required, err := stringListField(moduleRefs, path+".realization.moduleRefs", "required", false)
	if err != nil {
		return err
	}
	optional, err := stringListField(moduleRefs, path+".realization.moduleRefs", "optional", false)
	if err != nil {
		return err
	}
	if !contains(append(required, optional...), moduleRef) {
		return fail(ErrContractConflict, path, "contract module %q is not owned by the selected provider", moduleRef)
	}
	return nil
}

// validateBridgePolicyFlows keeps every policy-only/private flow inside the
// closed site, data, protocol, method, port, and workload-identity contract.
// A flow does not need a public publication; publication matching is a
// separate, stricter edge-to-origin check below.
//
//nolint:gocyclo // Flow admission is the atomic cross-product of endpoint identity, selector, locality, reachability, and data-policy constraints.
func validateBridgePolicyFlows(flows []map[string]any, selectedSiteRefs, peerSiteRefs []string, endpointIndex resolvedServiceEndpointIndex, authoritySiteRef string, nodeSites map[string]string, data map[string]any) error {
	selectedSites := make(map[string]struct{}, len(selectedSiteRefs))
	for _, siteRef := range selectedSiteRefs {
		if _, duplicate := selectedSites[siteRef]; duplicate {
			return fail(ErrContractConflict, "sites", "duplicate selected site %q", siteRef)
		}
		selectedSites[siteRef] = struct{}{}
	}
	sites := make(map[string]struct{}, len(peerSiteRefs))
	for _, siteRef := range peerSiteRefs {
		if _, duplicate := sites[siteRef]; duplicate {
			return fail(ErrContractConflict, "bridge.overlay.peerSiteRefs", "duplicate peer site %q", siteRef)
		}
		if _, selected := selectedSites[siteRef]; !selected {
			return fail(ErrUnresolvedPlacement, "bridge.overlay.peerSiteRefs", "peer site %q is not selected", siteRef)
		}
		sites[siteRef] = struct{}{}
	}
	if len(flows) == 0 {
		return nil
	}
	bindings, err := objectField(data, "data", "bindings")
	if err != nil {
		return err
	}
	allowedMethods := []string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"}
	allowedClasses := []string{"public", "internal", "personal", "sensitive", "secret"}
	for index, flow := range flows {
		flowPath := fmt.Sprintf("bridge.policy.allowedFlows[%d]", index)
		fromSiteRef, err := stringField(flow, flowPath, "fromSiteRef")
		if err != nil {
			return err
		}
		toSiteRef, err := stringField(flow, flowPath, "toSiteRef")
		if err != nil {
			return err
		}
		if fromSiteRef == toSiteRef {
			return fail(ErrContractConflict, flowPath, "cross-site flow endpoints must be distinct")
		}
		if _, exists := sites[fromSiteRef]; !exists {
			return fail(ErrUnresolvedPlacement, flowPath+".fromSiteRef", "flow source site %q is not selected", fromSiteRef)
		}
		if _, exists := sites[toSiteRef]; !exists {
			return fail(ErrUnresolvedPlacement, flowPath+".toSiteRef", "flow destination site %q is not selected", toSiteRef)
		}
		serviceRef, err := stringField(flow, flowPath, "serviceRef")
		if err != nil {
			return err
		}
		endpoint, err := resolvePublicationServiceEndpoint(endpointIndex, serviceRef, authoritySiteRef, nodeSites, flowPath)
		if err != nil {
			return err
		}
		if len(endpoint.siteRefs) != 1 || endpoint.siteRefs[0] != toSiteRef {
			return fail(ErrUnresolvedPlacement, flowPath+".toSiteRef", "flow destination %q is not the catalog-owned service origin %v", toSiteRef, endpoint.siteRefs)
		}
		if endpoint.data == nil || endpoint.data.bindingRef != serviceRef {
			return fail(ErrContractConflict, flowPath+".serviceRef", "cross-site service %q requires a same-key catalog data contract", serviceRef)
		}
		if err := validateServiceEndpointData(endpoint, data, "data", flowPath); err != nil {
			return err
		}
		rawBinding, exists := bindings[serviceRef]
		if !exists {
			return fail(ErrContractConflict, flowPath+".serviceRef", "flow service %q has no governed data binding", serviceRef)
		}
		binding, ok := rawBinding.(map[string]any)
		if !ok {
			return fail(ErrInvalidInput, "data.bindings."+serviceRef, "must be an object")
		}
		bindingClasses, err := stringListField(binding, "data.bindings."+serviceRef, "classes", true)
		if err != nil {
			return err
		}
		serviceIdentityRequired, err := boolFieldDefault(flow, flowPath, "serviceIdentityRequired", false)
		if err != nil {
			return err
		}
		if !serviceIdentityRequired {
			return fail(ErrContractConflict, flowPath+".serviceIdentityRequired", "cross-site flows require workload identity")
		}
		protocol, err := stringField(flow, flowPath, "protocol")
		if err != nil {
			return err
		}
		if !contains([]string{"tcp", "udp", "http", "https"}, protocol) {
			return fail(ErrContractConflict, flowPath+".protocol", "unsupported cross-site protocol %q", protocol)
		}
		if protocol != endpoint.upstreamProtocol {
			return fail(ErrContractConflict, flowPath+".protocol", "flow protocol %q differs from catalog endpoint %q", protocol, endpoint.upstreamProtocol)
		}
		ports, err := bridgePortListField(flow, flowPath)
		if err != nil {
			return err
		}
		seenPorts := make(map[int]struct{}, len(ports))
		for portIndex, port := range ports {
			if port < 1 || port > 65535 {
				return fail(ErrContractConflict, fmt.Sprintf("%s.ports[%d]", flowPath, portIndex), "port %d is outside 1..65535", port)
			}
			if _, duplicate := seenPorts[port]; duplicate {
				return fail(ErrContractConflict, fmt.Sprintf("%s.ports[%d]", flowPath, portIndex), "duplicate port %d", port)
			}
			seenPorts[port] = struct{}{}
		}
		if len(ports) != 1 || ports[0] != endpoint.targetPort {
			return fail(ErrContractConflict, flowPath+".ports", "flow ports must exactly bind catalog target port %d", endpoint.targetPort)
		}
		methodsRaw, methodsPresent := flow["methods"]
		if protocol == "http" || protocol == "https" {
			methods, err := stringListField(flow, flowPath, "methods", true)
			if err != nil {
				return err
			}
			if err := validateClosedStringSet(methods, allowedMethods, flowPath+".methods"); err != nil {
				return err
			}
		} else if methodsPresent {
			return fail(ErrContractConflict, flowPath+".methods", "TCP and UDP flows cannot declare HTTP methods, got %v", methodsRaw)
		}
		classes, err := stringListField(flow, flowPath, "dataClasses", true)
		if err != nil {
			return err
		}
		if err := validateClosedStringSet(classes, allowedClasses, flowPath+".dataClasses"); err != nil {
			return err
		}
		if !equalSortedStrings(classes, endpoint.data.requiredClasses) {
			return fail(ErrContractConflict, flowPath+".dataClasses", "flow data classes must exactly bind catalog endpoint classes %v", endpoint.data.requiredClasses)
		}
		for _, dataClass := range classes {
			if !contains(bindingClasses, dataClass) {
				return fail(ErrContractConflict, flowPath+".dataClasses", "data class %q is not governed by binding %q", dataClass, serviceRef)
			}
		}
	}
	return nil
}

// validateBridgeFlowEndpointReachability prevents a cross-site allow flow from
// widening a catalog endpoint's reachability. Private policy flows require an
// explicit remote-private exposure and the origin protocol as an allowed
// ingress protocol. A public-only endpoint is reachable only through an exact,
// already validated publication whose listener protocol is independently
// governed by the endpoint contract.
func validateBridgeFlowEndpointReachability(flows []map[string]any, publications []any, endpointIndex resolvedServiceEndpointIndex, authoritySiteRef string, nodeSites map[string]string) error {
	for index, flow := range flows {
		flowPath := fmt.Sprintf("bridge.policy.allowedFlows[%d]", index)
		serviceRef, err := stringField(flow, flowPath, "serviceRef")
		if err != nil {
			return err
		}
		endpoint, err := resolvePublicationServiceEndpoint(endpointIndex, serviceRef, authoritySiteRef, nodeSites, flowPath)
		if err != nil {
			return err
		}
		protocol, err := stringField(flow, flowPath, "protocol")
		if err != nil {
			return err
		}
		if contains(endpoint.allowedExposures, "remote-private") && contains(endpoint.allowedIngressProtocols, protocol) {
			continue
		}
		if !contains(endpoint.allowedExposures, "public") {
			return fail(ErrContractConflict, flowPath+".serviceRef", "cross-site service %q allows neither governed remote-private ingress for %q nor public publication", serviceRef, protocol)
		}
		matched := false
		for publicationIndex, rawPublication := range publications {
			publication, err := asObject(rawPublication, fmt.Sprintf("bridge.publications[%d]", publicationIndex))
			if err != nil {
				return err
			}
			exact, err := bridgeFlowMatchesResolvedPublication(flow, publication, endpoint, flowPath)
			if err != nil {
				return err
			}
			if exact {
				matched = true
				break
			}
		}
		if !matched {
			return fail(ErrContractConflict, flowPath+".serviceRef", "public-only cross-site service %q requires an exact validated publication", serviceRef)
		}
	}
	return nil
}

func bridgeFlowMatchesResolvedPublication(flow, publication map[string]any, endpoint resolvedServiceEndpoint, flowPath string) (bool, error) {
	fromSiteRef, err := stringField(flow, flowPath, "fromSiteRef")
	if err != nil {
		return false, err
	}
	toSiteRef, err := stringField(flow, flowPath, "toSiteRef")
	if err != nil {
		return false, err
	}
	serviceRef, err := stringField(flow, flowPath, "serviceRef")
	if err != nil {
		return false, err
	}
	if publication["edgeSiteRef"] != fromSiteRef || publication["sourceSiteRef"] != toSiteRef || publication["serviceRef"] != serviceRef ||
		publication["moduleRef"] != endpoint.moduleRef || publication["unitRef"] != endpoint.unitRef || publication["dataBindingRef"] != endpoint.data.bindingRef {
		return false, nil
	}
	protocol, err := stringField(flow, flowPath, "protocol")
	if err != nil {
		return false, err
	}
	ports, err := bridgePortListField(flow, flowPath)
	if err != nil {
		return false, err
	}
	if publication["upstreamProtocol"] != protocol || len(ports) != 1 || publication["targetPort"] != ports[0] {
		return false, nil
	}
	if protocol != "http" && protocol != "https" {
		return true, nil
	}
	methods, err := stringListField(flow, flowPath, "methods", true)
	if err != nil {
		return false, err
	}
	access, err := objectField(publication, "bridge.publications", "access")
	if err != nil {
		return false, err
	}
	allowedMethods, err := stringListField(access, "bridge.publications.access", "allowedMethods", true)
	if err != nil {
		return false, err
	}
	return equalSortedStrings(methods, allowedMethods), nil
}

func validateClosedStringSet(values, allowed []string, path string) error {
	seen := make(map[string]struct{}, len(values))
	for index, value := range values {
		if !contains(allowed, value) {
			return fail(ErrContractConflict, fmt.Sprintf("%s[%d]", path, index), "unsupported value %q", value)
		}
		if _, duplicate := seen[value]; duplicate {
			return fail(ErrContractConflict, fmt.Sprintf("%s[%d]", path, index), "duplicate value %q", value)
		}
		seen[value] = struct{}{}
	}
	return nil
}

//nolint:gocyclo // Flow matching intentionally checks the complete publication tuple and rejects partial or widened matches.
func validatePublicationBackendFlow(flows []map[string]any, publication map[string]any, endpoint resolvedServiceEndpoint, allowedMethods []string, path string) error {
	sourceSiteRef, err := stringField(publication, path, "sourceSiteRef")
	if err != nil {
		return err
	}
	edgeSiteRef, err := stringField(publication, path, "edgeSiteRef")
	if err != nil {
		return err
	}
	matchedIndex := -1
	for index, flow := range flows {
		flowPath := fmt.Sprintf("bridge.policy.allowedFlows[%d]", index)
		fromSiteRef, err := stringField(flow, flowPath, "fromSiteRef")
		if err != nil {
			return err
		}
		toSiteRef, err := stringField(flow, flowPath, "toSiteRef")
		if err != nil {
			return err
		}
		serviceRef, err := stringField(flow, flowPath, "serviceRef")
		if err != nil {
			return err
		}
		if fromSiteRef != edgeSiteRef || toSiteRef != sourceSiteRef || serviceRef != endpoint.serviceRef {
			continue
		}
		protocol, err := stringField(flow, flowPath, "protocol")
		if err != nil {
			return err
		}
		ports, err := bridgePortListField(flow, flowPath)
		if err != nil {
			return err
		}
		classes, err := stringListField(flow, flowPath, "dataClasses", true)
		if err != nil {
			return err
		}
		if protocol != endpoint.upstreamProtocol || len(ports) != 1 || ports[0] != endpoint.targetPort || !equalSortedStrings(classes, endpoint.data.requiredClasses) {
			continue
		}
		if protocol == "http" || protocol == "https" {
			methods, err := stringListField(flow, flowPath, "methods", true)
			if err != nil {
				return err
			}
			if !equalSortedStrings(methods, allowedMethods) {
				continue
			}
		}
		if matchedIndex == -1 {
			matchedIndex = index
		}
	}
	if matchedIndex == -1 {
		return fail(ErrContractConflict, path+".serviceRef", "published service %q requires at least one exact edge-to-origin flow for %s/%d, access methods, and data classes %v; got none", endpoint.serviceRef, endpoint.upstreamProtocol, endpoint.targetPort, endpoint.data.requiredClasses)
	}
	return nil
}

func bridgePortListField(object map[string]any, path string) ([]int, error) {
	raw, exists := object["ports"]
	if !exists {
		return nil, fail(ErrInvalidInput, path+".ports", "field is required")
	}
	values, ok := raw.([]any)
	if !ok || len(values) == 0 {
		return nil, fail(ErrInvalidInput, path+".ports", "must be a non-empty array")
	}
	result := make([]int, 0, len(values))
	for index, value := range values {
		port, err := intField(map[string]any{"value": value}, fmt.Sprintf("%s.ports[%d]", path, index), "value")
		if err != nil {
			return nil, err
		}
		result = append(result, port)
	}
	return result, nil
}

func equalSortedStrings(left, right []string) bool {
	left = sortStringsUnique(left)
	right = sortStringsUnique(right)
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func resolvePublicationServiceEndpoint(index resolvedServiceEndpointIndex, serviceRef, authoritySiteRef string, nodeSites map[string]string, path string) (resolvedServiceEndpoint, error) {
	moduleRefs := make([]string, 0, len(index))
	for moduleRef := range index {
		moduleRefs = append(moduleRefs, moduleRef)
	}
	sort.Strings(moduleRefs)
	candidates := make([]resolvedServiceEndpoint, 0, 1)
	for _, moduleRef := range moduleRefs {
		catalogEndpoint, exists := index[moduleRef][serviceRef]
		if !exists {
			continue
		}
		if catalogEndpoint.originSelector == "multi-zone" || catalogEndpoint.originSelector == "edge-pool" {
			return resolvedServiceEndpoint{}, fail(ErrContractConflict, path+".serviceRef", "bridge publications require one exact source Site; selector %q needs a separately versioned bridge contract", catalogEndpoint.originSelector)
		}
		endpoint, err := resolveRouteServiceEndpoint(index, moduleRef, serviceRef, authoritySiteRef, nodeSites, path)
		if err != nil {
			return resolvedServiceEndpoint{}, err
		}
		candidates = append(candidates, endpoint)
	}
	if len(candidates) == 0 {
		return resolvedServiceEndpoint{}, fail(ErrUnrealizedModule, path+".serviceRef", "service %q is not exported by any selected module", serviceRef)
	}
	if len(candidates) != 1 {
		return resolvedServiceEndpoint{}, fail(ErrContractConflict, path+".serviceRef", "service %q is ambiguously exported by %d selected modules", serviceRef, len(candidates))
	}
	return candidates[0], nil
}

func resolvedNodeSiteIndex(nodes []nodeView) map[string]string {
	result := make(map[string]string, len(nodes))
	for _, node := range nodes {
		result[node.id] = node.siteRef
	}
	return result
}

func validateBridgePublicationProjection(plan ResolvedPlan) error {
	bridge, exists, err := optionalObjectField(map[string]any(plan), "resolvedPlan", "bridge")
	if err != nil || !exists {
		return err
	}
	modules, err := objectListField(map[string]any(plan), "resolvedPlan", "modules")
	if err != nil {
		return err
	}
	providers, err := objectListField(map[string]any(plan), "resolvedPlan", "providers")
	if err != nil {
		return err
	}
	nodeSites, _, _, err := resolvedTopologyIndex(plan)
	if err != nil {
		return err
	}
	selectedSiteRefs, err := resolvedPlanSiteRefs(plan)
	if err != nil {
		return err
	}
	controlPlane, err := objectField(map[string]any(plan), "resolvedPlan", "controlPlane")
	if err != nil {
		return err
	}
	authoritySiteRef, err := stringField(controlPlane, "resolvedPlan.controlPlane", "authoritySiteRef")
	if err != nil {
		return err
	}
	data, err := objectField(map[string]any(plan), "resolvedPlan", "data")
	if err != nil {
		return err
	}
	access, err := objectField(map[string]any(plan), "resolvedPlan", "access")
	if err != nil {
		return err
	}
	want, err := resolveBridgePlan(bridge, objectMapsAsAny(modules), selectedSiteRefs, authoritySiteRef, nodeSites, data, access, objectMapsAsAny(providers))
	if err != nil {
		return fmt.Errorf("recompute resolvedPlan.bridge publications: %w", err)
	}
	if equal, err := canonicalEqual(bridge, want); err != nil {
		return err
	} else if !equal {
		haveHash, _ := canonicalHash(bridge, false)
		wantHash, _ := canonicalHash(want, false)
		return fmt.Errorf("resolvedPlan.bridge is not the exact catalog-owned publication projection (%s != %s)", haveHash, wantHash)
	}
	return nil
}

func resolvedPlanSiteRefs(plan ResolvedPlan) ([]string, error) {
	sites, err := objectListField(map[string]any(plan), "resolvedPlan", "sites")
	if err != nil {
		return nil, err
	}
	result := make([]string, 0, len(sites))
	for index, site := range sites {
		ref, err := stringField(site, fmt.Sprintf("resolvedPlan.sites[%d]", index), "id")
		if err != nil {
			return nil, err
		}
		result = append(result, ref)
	}
	return result, nil
}
