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
func resolveBridgePlan(bridge map[string]any, modules []any, authoritySiteRef string, nodeSites map[string]string, data, access map[string]any) (map[string]any, error) {
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
	if len(flows) != len(publications) {
		return nil, fail(ErrContractConflict, "bridge.policy.allowedFlows", "bridge requires exactly one catalog-backed allow-flow per publication; got %d flows for %d publications", len(flows), len(publications))
	}
	endpointIndex, err := indexResolvedServiceEndpoints(modules)
	if err != nil {
		return nil, err
	}
	resolvedPublications := make([]any, 0, len(publications))
	matchedFlows := make(map[int]string, len(publications))
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
		allowedMethods, err := stringListField(accessDecision, path+".access", "allowedMethods", true)
		if err != nil {
			return nil, fail(ErrProfileMismatch, path+".auth.policyRef", "public publication policy must declare a non-empty allowedMethods set")
		}
		flowIndex, err := validatePublicationBackendFlow(flows, publication, endpoint, allowedMethods, path)
		if err != nil {
			return nil, err
		}
		if previousService, duplicate := matchedFlows[flowIndex]; duplicate {
			return nil, fail(ErrContractConflict, fmt.Sprintf("bridge.policy.allowedFlows[%d]", flowIndex), "allow-flow is shared by publications %q and %q", previousService, serviceRef)
		}
		matchedFlows[flowIndex] = serviceRef
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
	if len(matchedFlows) != len(flows) {
		return nil, fail(ErrContractConflict, "bridge.policy.allowedFlows", "bridge contains an orphan allow-flow without an exact catalog-backed publication")
	}
	resolved["publications"] = resolvedPublications
	return resolved, nil
}

//nolint:gocyclo // Flow matching intentionally checks the complete publication tuple and rejects partial or widened matches.
func validatePublicationBackendFlow(flows []map[string]any, publication map[string]any, endpoint resolvedServiceEndpoint, allowedMethods []string, path string) (int, error) {
	sourceSiteRef, err := stringField(publication, path, "sourceSiteRef")
	if err != nil {
		return -1, err
	}
	edgeSiteRef, err := stringField(publication, path, "edgeSiteRef")
	if err != nil {
		return -1, err
	}
	matchedIndex := -1
	for index, flow := range flows {
		flowPath := fmt.Sprintf("bridge.policy.allowedFlows[%d]", index)
		fromSiteRef, err := stringField(flow, flowPath, "fromSiteRef")
		if err != nil {
			return -1, err
		}
		toSiteRef, err := stringField(flow, flowPath, "toSiteRef")
		if err != nil {
			return -1, err
		}
		serviceRef, err := stringField(flow, flowPath, "serviceRef")
		if err != nil {
			return -1, err
		}
		if fromSiteRef != edgeSiteRef || toSiteRef != sourceSiteRef || serviceRef != endpoint.serviceRef {
			continue
		}
		protocol, err := stringField(flow, flowPath, "protocol")
		if err != nil {
			return -1, err
		}
		ports, err := bridgePortListField(flow, flowPath)
		if err != nil {
			return -1, err
		}
		classes, err := stringListField(flow, flowPath, "dataClasses", true)
		if err != nil {
			return -1, err
		}
		if protocol == "http" || protocol == "https" {
			methods, err := stringListField(flow, flowPath, "methods", true)
			if err != nil {
				return -1, err
			}
			for _, method := range methods {
				if !contains(allowedMethods, method) {
					return -1, fail(ErrContractConflict, flowPath+".methods", "method %q exceeds publication access policy %v", method, allowedMethods)
				}
			}
		}
		if protocol != endpoint.upstreamProtocol || len(ports) != 1 || ports[0] != endpoint.targetPort || !equalSortedStrings(classes, endpoint.data.requiredClasses) {
			continue
		}
		if matchedIndex != -1 {
			return -1, fail(ErrContractConflict, path+".serviceRef", "published service %q has more than one exact edge-to-origin flow", endpoint.serviceRef)
		}
		matchedIndex = index
	}
	if matchedIndex == -1 {
		return -1, fail(ErrContractConflict, path+".serviceRef", "published service %q requires exactly one edge-to-origin flow for %s/%d and data classes %v; got none", endpoint.serviceRef, endpoint.upstreamProtocol, endpoint.targetPort, endpoint.data.requiredClasses)
	}
	return matchedIndex, nil
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
	want, err := resolveBridgePlan(bridge, objectMapsAsAny(modules), authoritySiteRef, nodeSites, data, access)
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
