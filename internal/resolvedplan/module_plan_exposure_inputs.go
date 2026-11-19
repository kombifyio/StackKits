package resolvedplan

import (
	"fmt"
)

func safeModuleCloudAdminMesh(moduleID string, module map[string]any, capabilities, nodes []any, network, gates map[string]any) (map[string]any, error) {
	const capabilityID = "private-admin-mesh"
	provided, err := stringListField(module, "modules."+moduleID, "provides", true)
	if err != nil {
		return nil, err
	}
	if len(provided) != 1 || provided[0] != capabilityID {
		return nil, fmt.Errorf("module %q cloudAdminMesh projection requires exactly capability %q", moduleID, capabilityID)
	}
	providerRef, err := stringField(module, "modules."+moduleID, "providerRef")
	if err != nil {
		return nil, err
	}
	capability, err := resolvedPlanObjectByID(capabilities, capabilityID, "capabilities")
	if err != nil {
		return nil, err
	}
	capabilityProviderRef, err := stringField(capability, "capabilities."+capabilityID, "providerRef")
	if err != nil {
		return nil, err
	}
	if capabilityProviderRef != providerRef {
		return nil, fmt.Errorf("module %q provider %q does not own resolved capability %q", moduleID, providerRef, capabilityID)
	}
	targets, err := safeModuleTargets(moduleID, module, nodes)
	if err != nil {
		return nil, err
	}
	nodeRefs := make([]string, 0, len(targets))
	siteSet := make(map[string]struct{}, len(targets))
	for index, rawTarget := range targets {
		target, err := asObject(rawTarget, fmt.Sprintf("cloudAdminMesh.moduleTargets[%d]", index))
		if err != nil {
			return nil, err
		}
		nodeRef, err := stringField(target, fmt.Sprintf("cloudAdminMesh.moduleTargets[%d]", index), "id")
		if err != nil {
			return nil, err
		}
		siteRef, err := stringField(target, fmt.Sprintf("cloudAdminMesh.moduleTargets[%d]", index), "siteRef")
		if err != nil {
			return nil, err
		}
		nodeRefs = append(nodeRefs, nodeRef)
		siteSet[siteRef] = struct{}{}
	}

	rawRoutes, err := objectListField(network, "network", "routes")
	if err != nil {
		return nil, err
	}
	selectedRoutes := make([]any, 0, len(rawRoutes))
	selectedPoolRefs := map[string]struct{}{}
	selectedHealthRefs := map[string]struct{}{}
	for index, route := range rawRoutes {
		path := fmt.Sprintf("network.routes[%d]", index)
		requirements, err := routeCapabilityRequirementsFromProjection(route, path)
		if err != nil {
			return nil, err
		}
		owned := false
		for _, requirement := range requirements {
			if requirement.capabilityRef == capabilityID && requirement.role == "access" {
				owned = true
			}
		}
		if !owned {
			continue
		}
		exposure, err := stringField(route, path, "exposure")
		if err != nil || exposure != "remote-private" {
			return nil, fmt.Errorf("%s owned by private-admin-mesh is not remote-private", path)
		}
		access, err := objectField(route, path, "access")
		if err != nil {
			return nil, err
		}
		policyExposure, err := stringField(access, path+".access", "policyExposure")
		if err != nil || policyExposure != "remote-private" {
			return nil, fmt.Errorf("%s requires remote-private policy exposure", path)
		}
		authentication, err := stringField(access, path+".access", "authentication")
		if err != nil || authentication != "human+device" {
			return nil, fmt.Errorf("%s requires human+device authentication", path)
		}
		for field, expected := range map[string]bool{
			"enrolledDeviceRequired": true,
			"defaultClosed":          true,
		} {
			actual, ok := access[field].(bool)
			if !ok || actual != expected {
				return nil, fmt.Errorf("%s.access.%s must be %t", path, field, expected)
			}
		}
		poolRef, err := stringField(route, path, "backendPoolRef")
		if err != nil {
			return nil, err
		}
		healthRef, err := stringField(route, path, "healthGateRef")
		if err != nil {
			return nil, err
		}
		selectedPoolRefs[poolRef] = struct{}{}
		selectedHealthRefs[healthRef] = struct{}{}
		selectedRoutes = append(selectedRoutes, route)
	}
	selectedPools, err := selectReferencedObjects(network, "network", "backendPools", selectedPoolRefs)
	if err != nil {
		return nil, err
	}
	selectedHealth, err := selectReferencedObjects(gates, "gates", "health", selectedHealthRefs)
	if err != nil {
		return nil, err
	}
	routes, err := projectPublicRouteListFromNetwork(
		map[string]any{"routes": selectedRoutes, "backendPools": selectedPools},
		map[string]any{"health": selectedHealth},
		"cloudAdminMesh",
		true,
		true,
	)
	if err != nil {
		return nil, err
	}
	if len(routes) == 0 {
		return nil, fmt.Errorf("module %q cloudAdminMesh projection requires at least one governed private route", moduleID)
	}
	networkProjection, err := safeModuleCloudNetworkPosture(network)
	if err != nil {
		return nil, err
	}
	return normalizedObject(map[string]any{
		"capabilityRef": capabilityID,
		"siteRefs":      stringSliceAny(sortedStringMapKeys(siteSet)),
		"nodeRefs":      stringSliceAny(sortStringsUnique(nodeRefs)),
		"network":       networkProjection,
		"routes":        routes,
	}, "cloudAdminMesh")
}

func safeModulePublicEdge(moduleID string, module map[string]any, capabilities []any, network, gates map[string]any) (map[string]any, error) {
	const capabilityID = "public-edge"
	provided, err := stringListField(module, "modules."+moduleID, "provides", true)
	if err != nil {
		return nil, err
	}
	if len(provided) != 1 || provided[0] != capabilityID {
		return nil, fmt.Errorf("module %q publicEdge projection requires exactly capability %q", moduleID, capabilityID)
	}
	providerRef, err := stringField(module, "modules."+moduleID, "providerRef")
	if err != nil {
		return nil, err
	}
	capability, err := resolvedPlanObjectByID(capabilities, capabilityID, "capabilities")
	if err != nil {
		return nil, err
	}
	capabilityProviderRef, err := stringField(capability, "capabilities."+capabilityID, "providerRef")
	if err != nil {
		return nil, err
	}
	if capabilityProviderRef != providerRef {
		return nil, fmt.Errorf("module %q provider %q does not own resolved capability %q", moduleID, providerRef, capabilityID)
	}
	rawRoutes, err := objectListField(network, "network", "routes")
	if err != nil {
		return nil, err
	}
	selectedRoutes := make([]any, 0, len(rawRoutes))
	selectedPoolRefs := map[string]struct{}{}
	selectedHealthRefs := map[string]struct{}{}
	selectedRouteIDs := map[string]struct{}{}
	for index, route := range rawRoutes {
		path := fmt.Sprintf("network.routes[%d]", index)
		requirements, err := routeCapabilityRequirementsFromProjection(route, path)
		if err != nil {
			return nil, err
		}
		owned := false
		for _, requirement := range requirements {
			if requirement.capabilityRef == capabilityID && requirement.role == "edge" {
				owned = true
			}
		}
		if !owned {
			continue
		}
		exposure, err := stringField(route, path, "exposure")
		if err != nil || exposure != "public" {
			return nil, fmt.Errorf("%s owned by public-edge is not public", path)
		}
		id, err := stringField(route, path, "id")
		if err != nil {
			return nil, err
		}
		if _, duplicate := selectedRouteIDs[id]; duplicate {
			return nil, fmt.Errorf("publicEdge projection contains duplicate route %q", id)
		}
		selectedRouteIDs[id] = struct{}{}
		poolRef, err := stringField(route, path, "backendPoolRef")
		if err != nil {
			return nil, err
		}
		healthRef, err := stringField(route, path, "healthGateRef")
		if err != nil {
			return nil, err
		}
		selectedPoolRefs[poolRef] = struct{}{}
		selectedHealthRefs[healthRef] = struct{}{}
		selectedRoutes = append(selectedRoutes, route)
	}
	selectedPools, err := selectReferencedObjects(network, "network", "backendPools", selectedPoolRefs)
	if err != nil {
		return nil, err
	}
	selectedHealth, err := selectReferencedObjects(gates, "gates", "health", selectedHealthRefs)
	if err != nil {
		return nil, err
	}
	projectedRoutes, err := projectPublicRouteListFromNetwork(
		map[string]any{"routes": selectedRoutes, "backendPools": selectedPools},
		map[string]any{"health": selectedHealth},
		"publicEdge",
		true,
		true,
	)
	if err != nil {
		return nil, err
	}
	networkProjection, err := safeModuleCloudNetworkPosture(network)
	if err != nil {
		return nil, err
	}
	return normalizedObject(map[string]any{
		"capabilityRef": capabilityID,
		"network":       networkProjection,
		"routes":        projectedRoutes,
	}, "publicEdge")
}

func safeModuleCloudNetworkPosture(network map[string]any) (map[string]any, error) {
	configuration, err := objectField(network, "network", "configuration")
	if err != nil {
		return nil, err
	}
	mode, err := stringField(configuration, "network.configuration", "mode")
	if err != nil {
		return nil, err
	}
	transport, err := objectField(configuration, "network.configuration", "transport")
	if err != nil {
		return nil, err
	}
	transportProjection, err := selectObjectFields(transport, "network.configuration.transport", []string{"subnet", "ipv6"})
	if err != nil {
		return nil, err
	}
	tls, err := objectField(configuration, "network.configuration", "tls")
	if err != nil {
		return nil, err
	}
	minVersion, err := stringField(tls, "network.configuration.tls", "minVersion")
	if err != nil {
		return nil, err
	}
	return normalizedObject(map[string]any{
		"mode": mode, "transport": transportProjection, "tlsMinVersion": minVersion,
	}, "cloudNetworkPosture")
}

func selectReferencedObjects(source map[string]any, path, field string, refs map[string]struct{}) ([]any, error) {
	objects, err := objectListField(source, path, field)
	if err != nil {
		return nil, err
	}
	selected := make([]any, 0, len(refs))
	found := make(map[string]struct{}, len(refs))
	for index, object := range objects {
		objectPath := fmt.Sprintf("%s.%s[%d]", path, field, index)
		id, err := stringField(object, objectPath, "id")
		if err != nil {
			return nil, err
		}
		if _, required := refs[id]; !required {
			continue
		}
		if _, duplicate := found[id]; duplicate {
			return nil, fail(ErrContractConflict, objectPath+".id", "referenced object %q is duplicated", id)
		}
		found[id] = struct{}{}
		selected = append(selected, object)
	}
	if len(found) != len(refs) {
		return nil, fail(ErrContractConflict, path+"."+field, "not every referenced object exists exactly once")
	}
	return selected, nil
}
