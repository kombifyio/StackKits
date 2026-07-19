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
func resolveBridgePlan(bridge map[string]any, modules []any, selectedSiteRefs []string, authoritySiteRef string, nodeSites map[string]string, data, access map[string]any) (map[string]any, error) {
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
		if _, exists := index[moduleRef][serviceRef]; !exists {
			continue
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
	want, err := resolveBridgePlan(bridge, objectMapsAsAny(modules), selectedSiteRefs, authoritySiteRef, nodeSites, data, access)
	if err != nil {
		return fmt.Errorf("recompute resolvedPlan.bridge publications: %w", err)
	}
	if equal, err := canonicalEqual(bridge, want); err != nil {
		return err
	} else if !equal {
		return fmt.Errorf("resolvedPlan.bridge is not the exact catalog-owned publication projection")
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
