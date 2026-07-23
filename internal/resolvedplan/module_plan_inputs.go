package resolvedplan

import "fmt"

var allowedModulePlanInputRefs = map[string]struct{}{
	"stackId": {}, "kit": {}, "sites": {}, "controlPlane": {},
	"bridge": {}, "identity": {}, "data": {}, "failurePolicy": {},
	"identityTrust": {}, "localReachability": {}, "homeLANDiscovery": {},
	"homeAccessRequirements": {}, "externalHomeAccessBindings": {},
	"backupTargetRequirements": {}, "externalBackupTargetBindings": {},
	"homeBackupTargetRequirements": {}, "externalHomeBackupTargetBindings": {},
	"federationLinkRequirements": {}, "externalFederationLinkBindings": {},
	"moduleTargets": {}, "moduleCapabilities": {}, "hostRuntimePolicy": {},
	"storagePolicy": {}, "localNetworkPolicy": {}, "cloudNetworkPolicy": {}, "publicEdge": {}, "publicTLS": {},
}

func validateModulePlanInputRefs(refs []string, path string) ([]string, error) {
	seen := make(map[string]struct{}, len(refs))
	validated := append([]string(nil), refs...)
	for index, ref := range validated {
		itemPath := fmt.Sprintf("%s[%d]", path, index)
		if _, allowed := allowedModulePlanInputRefs[ref]; !allowed {
			return nil, fail(ErrContractConflict, itemPath, "unsupported compiler plan input %q", ref)
		}
		if _, duplicate := seen[ref]; duplicate {
			return nil, fail(ErrContractConflict, itemPath, "compiler plan input %q is duplicated", ref)
		}
		seen[ref] = struct{}{}
	}
	return sortStringsUnique(validated), nil
}

type modulePlanInputSource struct {
	stackID                          string
	kit                              map[string]any
	sites                            []any
	controlPlane                     map[string]any
	bridge                           map[string]any
	identity                         map[string]any
	identityTrust                    map[string]any
	data                             map[string]any
	failurePolicy                    map[string]any
	localReachability                map[string]any
	homeLANDiscovery                 map[string]any
	homeAccessRequirements           map[string]any
	externalHomeAccessBindings       map[string]any
	backupTargetRequirements         map[string]any
	externalBackupTargetBindings     map[string]any
	homeBackupTargetRequirements     map[string]any
	externalHomeBackupTargetBindings map[string]any
	federationLinkRequirements       map[string]any
	externalFederationLinkBindings   map[string]any
	nodes                            []any
	capabilities                     []any
	providers                        []any
	install                          map[string]any
	system                           map[string]any
	storage                          map[string]any
	network                          map[string]any
	gates                            map[string]any
}

// bindResolvedModulePlanInputs is the only compiler seam that populates a
// render unit's planInputs. It runs after resolveBridgePlan and copies only a
// closed, catalog-declared view. User module settings, secretRefs, raw nodes,
// management addresses, and daemon/socket facts are intentionally unreachable.
func bindResolvedModulePlanInputs(modules []any, source modulePlanInputSource) error {
	for moduleIndex, rawModule := range modules {
		module, err := asObject(rawModule, fmt.Sprintf("modules[%d]", moduleIndex))
		if err != nil {
			return err
		}
		moduleID, err := stringField(module, fmt.Sprintf("modules[%d]", moduleIndex), "id")
		if err != nil {
			return err
		}
		units, err := objectListField(module, "modules."+moduleID, "renderUnits")
		if err != nil {
			return err
		}
		for unitIndex, unit := range units {
			unitPath := fmt.Sprintf("modules.%s.renderUnits[%d]", moduleID, unitIndex)
			refs, err := stringListField(unit, unitPath, "planInputRefs", false)
			if err != nil {
				return err
			}
			refs, err = validateModulePlanInputRefs(refs, unitPath+".planInputRefs")
			if err != nil {
				return err
			}
			inputs := make(map[string]any, len(refs))
			for _, ref := range refs {
				projection, err := source.resolve(ref, moduleID, module)
				if err != nil {
					return fail(ErrContractConflict, unitPath+".planInputRefs", "%v", err)
				}
				inputs[ref] = projection
			}
			unit["planInputRefs"] = stringSliceAny(refs)
			unit["planInputs"] = inputs
		}
	}
	return nil
}

func (source modulePlanInputSource) resolve(ref, moduleID string, module map[string]any) (any, error) {
	if _, allowed := allowedModulePlanInputRefs[ref]; !allowed {
		return nil, fmt.Errorf("unsupported compiler plan input %q", ref)
	}
	var value any
	switch ref {
	case "stackId":
		value = source.stackID
	case "kit":
		value = source.kit
	case "sites":
		return safeModulePlanSites(source.sites)
	case "controlPlane":
		value = source.controlPlane
	case "bridge":
		if source.bridge == nil {
			return nil, fmt.Errorf("bridge projection is unavailable")
		}
		value = source.bridge
	case "identity":
		value = source.identity
	case "identityTrust":
		if source.identityTrust == nil {
			return nil, fmt.Errorf("identity trust projection is unavailable")
		}
		value = source.identityTrust
	case "data":
		value = source.data
	case "failurePolicy":
		value = source.failurePolicy
	case "localReachability":
		value = source.localReachability
	case "homeLANDiscovery":
		value = source.homeLANDiscovery
	case "homeAccessRequirements":
		return safeModuleHomeAccessProjection(moduleID, module, source.homeAccessRequirements, true)
	case "externalHomeAccessBindings":
		return safeModuleHomeAccessProjection(moduleID, module, source.externalHomeAccessBindings, false)
	case "backupTargetRequirements":
		return safeModuleBackupTargetProjection(moduleID, module, source.backupTargetRequirements, true)
	case "externalBackupTargetBindings":
		return safeModuleBackupTargetProjection(moduleID, module, source.externalBackupTargetBindings, false)
	case "homeBackupTargetRequirements":
		return safeModuleHomeBackupTargetProjection(moduleID, module, source.homeBackupTargetRequirements, true)
	case "externalHomeBackupTargetBindings":
		return safeModuleHomeBackupTargetProjection(moduleID, module, source.externalHomeBackupTargetBindings, false)
	case "federationLinkRequirements":
		return safeModuleFederationLinkProjection(moduleID, module, source.federationLinkRequirements, true)
	case "externalFederationLinkBindings":
		return safeModuleFederationLinkProjection(moduleID, module, source.externalFederationLinkBindings, false)
	case "moduleTargets":
		return safeModuleTargets(moduleID, module, source.nodes)
	case "moduleCapabilities":
		return safeModuleCapabilities(moduleID, module, source.capabilities)
	case "hostRuntimePolicy":
		return safeModuleHostRuntimePolicy(source.install, source.system)
	case "storagePolicy":
		return safeModuleStoragePolicy(source.storage)
	case "localNetworkPolicy":
		return safeModuleNetworkPolicy(source.network, "localNetworkPolicy")
	case "cloudNetworkPolicy":
		return safeModuleNetworkPolicy(source.network, "cloudNetworkPolicy")
	case "publicEdge":
		return safeModulePublicEdge(moduleID, module, source.capabilities, source.network, source.gates)
	case "publicTLS":
		return safeModulePublicTLS(moduleID, module, source.capabilities, source.providers, source.network)
	}
	return normalizeJSON(value, false, ref)
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
	return normalizedObject(map[string]any{"capabilityRef": capabilityID, "routes": projectedRoutes}, "publicEdge")
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

func safeModuleTargets(moduleID string, module map[string]any, nodes []any) ([]any, error) {
	nodeRefs, err := stringListField(module, "modules."+moduleID, "nodeRefs", true)
	if err != nil {
		return nil, err
	}
	nodeByID := make(map[string]map[string]any, len(nodes))
	for index, rawNode := range nodes {
		node, err := asObject(rawNode, fmt.Sprintf("nodes[%d]", index))
		if err != nil {
			return nil, err
		}
		id, err := stringField(node, fmt.Sprintf("nodes[%d]", index), "id")
		if err != nil {
			return nil, err
		}
		nodeByID[id] = node
	}
	result := make([]any, 0, len(nodeRefs))
	for _, nodeRef := range nodeRefs {
		node, exists := nodeByID[nodeRef]
		if !exists {
			return nil, fmt.Errorf("module %q target node %q is absent from resolved topology", moduleID, nodeRef)
		}
		siteRef, err := stringField(node, "nodes."+nodeRef, "siteRef")
		if err != nil {
			return nil, err
		}
		roles, err := stringListField(node, "nodes."+nodeRef, "roles", true)
		if err != nil {
			return nil, err
		}
		failureDomain, err := stringField(node, "nodes."+nodeRef, "failureDomain")
		if err != nil {
			return nil, err
		}
		hardware, err := objectField(node, "nodes."+nodeRef, "hardware")
		if err != nil {
			return nil, err
		}
		result = append(result, map[string]any{
			"id": nodeRef, "siteRef": siteRef, "roles": stringSliceAny(sortStringsUnique(roles)),
			"failureDomain": failureDomain, "declaredHardware": hardware,
		})
	}
	normalized, err := normalizeJSON(result, false, "moduleTargets")
	if err != nil {
		return nil, err
	}
	return normalized.([]any), nil
}

func safeModuleCapabilities(moduleID string, module map[string]any, capabilities []any) ([]any, error) {
	provided, err := stringListField(module, "modules."+moduleID, "provides", true)
	if err != nil {
		return nil, err
	}
	capabilityByID := make(map[string]map[string]any, len(capabilities))
	for index, rawCapability := range capabilities {
		capability, err := asObject(rawCapability, fmt.Sprintf("capabilities[%d]", index))
		if err != nil {
			return nil, err
		}
		id, err := stringField(capability, fmt.Sprintf("capabilities[%d]", index), "id")
		if err != nil {
			return nil, err
		}
		capabilityByID[id] = capability
	}
	provided = sortStringsUnique(provided)
	result := make([]any, 0, len(provided))
	for _, id := range provided {
		capability, exists := capabilityByID[id]
		if !exists {
			return nil, fmt.Errorf("module %q capability %q is absent from resolved capability contracts", moduleID, id)
		}
		if settings, exists, err := optionalObjectField(capability, "capabilities."+id, "settings"); err != nil {
			return nil, err
		} else if exists && len(settings) > 0 {
			return nil, fmt.Errorf("module %q capability %q has settings not represented by its executor contract", moduleID, id)
		}
		if secretRefs, exists, err := optionalObjectField(capability, "capabilities."+id, "secretRefs"); err != nil {
			return nil, err
		} else if exists && len(secretRefs) > 0 {
			return nil, fmt.Errorf("module %q capability %q has secretRefs not represented by its executor contract", moduleID, id)
		}
		contractHash, err := stringField(capability, "capabilities."+id, "contractHash")
		if err != nil {
			return nil, err
		}
		result = append(result, map[string]any{"id": id, "contractHash": contractHash})
	}
	normalized, err := normalizeJSON(result, false, "moduleCapabilities")
	if err != nil {
		return nil, err
	}
	return normalized.([]any), nil
}

func safeModuleHostRuntimePolicy(install, system map[string]any) (map[string]any, error) {
	installProjection, err := selectObjectFields(install, "install", []string{"mode", "runtime"})
	if err != nil {
		return nil, err
	}
	platform, err := objectField(install, "install", "platform")
	if err != nil {
		return nil, err
	}
	platformProjection, err := selectObjectFields(platform, "install.platform", []string{"management", "fallbackAllowed", "setupPolicy"})
	if err != nil {
		return nil, err
	}
	installProjection["platform"] = platformProjection
	host, err := objectField(system, "system", "host")
	if err != nil {
		return nil, err
	}
	result := map[string]any{"install": installProjection, "host": host}
	if container, exists, err := optionalObjectField(system, "system", "container"); err != nil {
		return nil, err
	} else if exists {
		containerProjection, err := selectOptionalObjectFields(container, "system.container", []string{"engine", "rootless", "liveRestore", "storageDriver", "dataRoot", "logDriver"})
		if err != nil {
			return nil, err
		}
		result["container"] = containerProjection
	}
	return normalizedObject(result, "hostRuntimePolicy")
}

func safeModuleStoragePolicy(storage map[string]any) (map[string]any, error) {
	return normalizedObject(storage, "storagePolicy")
}

func safeModuleNetworkPolicy(network map[string]any, path string) (map[string]any, error) {
	configuration, err := objectField(network, "network", "configuration")
	if err != nil {
		return nil, err
	}
	result, err := selectObjectFields(configuration, "network.configuration", []string{"mode", "domain", "transport", "dns"})
	if err != nil {
		return nil, err
	}
	tls, err := objectField(configuration, "network.configuration", "tls")
	if err != nil {
		return nil, err
	}
	tlsProjection, err := selectOptionalObjectFields(tls, "network.configuration.tls", []string{"defaultMode", "minVersion"})
	if err != nil {
		return nil, err
	}
	result["tls"] = tlsProjection
	return normalizedObject(result, path)
}

func safeModulePublicTLS(moduleID string, module map[string]any, capabilities, providers []any, network map[string]any) (map[string]any, error) {
	const capabilityID = "public-tls"
	provided, err := stringListField(module, "modules."+moduleID, "provides", true)
	if err != nil {
		return nil, err
	}
	if len(provided) != 1 || provided[0] != capabilityID {
		return nil, fmt.Errorf("module %q publicTLS projection requires exactly capability %q", moduleID, capabilityID)
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
	profile, err := objectField(capability, "capabilities."+capabilityID, "tlsProfile")
	if err != nil {
		return nil, err
	}
	profileProjection, err := selectObjectFields(profile, "capabilities."+capabilityID+".tlsProfile", []string{"id", "capabilityRef", "mode", "trustDomain", "minimumVersion", "allowedIssuerKinds"})
	if err != nil {
		return nil, err
	}
	profileID, err := stringField(profileProjection, "publicTLS.profile", "id")
	if err != nil {
		return nil, err
	}
	profileMode, err := stringField(profileProjection, "publicTLS.profile", "mode")
	if err != nil || profileMode != "terminate-at-edge" {
		return nil, fmt.Errorf("public TLS profile must terminate at the edge")
	}
	allowedKinds, err := stringListField(profileProjection, "publicTLS.profile", "allowedIssuerKinds", true)
	if err != nil {
		return nil, err
	}
	provider, err := resolvedPlanObjectByID(providers, providerRef, "providers")
	if err != nil {
		return nil, err
	}
	issuerContracts, err := objectListField(provider, "providers."+providerRef, "certificateIssuers")
	if err != nil {
		return nil, err
	}
	var issuer map[string]any
	for index, candidate := range issuerContracts {
		path := fmt.Sprintf("providers.%s.certificateIssuers[%d]", providerRef, index)
		candidateCapability, fieldErr := stringField(candidate, path, "capabilityRef")
		if fieldErr != nil {
			return nil, fieldErr
		}
		candidateKind, fieldErr := stringField(candidate, path, "kind")
		if fieldErr != nil {
			return nil, fieldErr
		}
		if candidateCapability != capabilityID || !contains(allowedKinds, candidateKind) {
			continue
		}
		if issuer != nil {
			return nil, fmt.Errorf("provider %q has multiple public TLS issuers", providerRef)
		}
		issuer = candidate
	}
	if issuer == nil {
		return nil, fmt.Errorf("provider %q has no public TLS issuer", providerRef)
	}
	issuerProjection, err := selectObjectFields(issuer, "publicTLS.issuer", []string{"id", "capabilityRef", "kind", "challenge", "supportedSiteKinds", "validitySeconds", "requiredInputSlotIDs", "materialSlots", "renewal"})
	if err != nil {
		return nil, err
	}
	issuerID, err := stringField(issuerProjection, "publicTLS.issuer", "id")
	if err != nil {
		return nil, err
	}
	routes, err := objectListField(network, "network", "routes")
	if err != nil {
		return nil, err
	}
	routesByID := map[string]map[string]any{}
	for index, route := range routes {
		path := fmt.Sprintf("network.routes[%d]", index)
		tls, err := objectField(route, path, "tls")
		if err != nil {
			return nil, err
		}
		routeProfileRef, hasProfile, err := optionalStringField(tls, path+".tls", "profileRef")
		if err != nil {
			return nil, err
		}
		routeIssuerRef, hasIssuer, err := optionalStringField(tls, path+".tls", "issuerRef")
		if err != nil {
			return nil, err
		}
		if !hasProfile && !hasIssuer {
			continue
		}
		if routeProfileRef != profileID || routeIssuerRef != issuerID {
			continue
		}
		exposure, err := stringField(route, path, "exposure")
		if err != nil || exposure != "public" {
			return nil, fmt.Errorf("%s bound to public TLS is not public", path)
		}
		protocol, err := stringField(route, path, "protocol")
		if err != nil || protocol != "https" {
			return nil, fmt.Errorf("%s bound to public TLS is not HTTPS", path)
		}
		id, err := stringField(route, path, "id")
		if err != nil {
			return nil, err
		}
		host, err := stringField(route, path, "host")
		if err != nil {
			return nil, err
		}
		port, err := intField(route, path, "port")
		if err != nil {
			return nil, err
		}
		pathValue, err := stringField(route, path, "path")
		if err != nil {
			return nil, err
		}
		minimumVersion, err := stringField(tls, path+".tls", "minVersion")
		if err != nil {
			return nil, err
		}
		routesByID[id] = map[string]any{
			"id": id, "host": host, "port": port, "path": pathValue, "exposure": exposure, "protocol": protocol,
			"tls": map[string]any{"mode": "terminate-at-edge", "minVersion": minimumVersion, "profileRef": profileID, "issuerRef": issuerID},
		}
	}
	projectedRoutes := make([]any, 0, len(routesByID))
	for _, id := range sortedStringMapKeys(routesByID) {
		projectedRoutes = append(projectedRoutes, routesByID[id])
	}
	return normalizedObject(map[string]any{
		"capabilityRef": capabilityID,
		"providerRef":   providerRef,
		"profile":       profileProjection,
		"issuer":        issuerProjection,
		"routes":        projectedRoutes,
	}, "publicTLS")
}

func resolvedPlanObjectByID(values []any, wantedID, path string) (map[string]any, error) {
	var match map[string]any
	for index, raw := range values {
		value, err := asObject(raw, fmt.Sprintf("%s[%d]", path, index))
		if err != nil {
			return nil, err
		}
		id, err := stringField(value, fmt.Sprintf("%s[%d]", path, index), "id")
		if err != nil {
			return nil, err
		}
		if id != wantedID {
			continue
		}
		if match != nil {
			return nil, fmt.Errorf("%s contains duplicate id %q", path, wantedID)
		}
		match = value
	}
	if match == nil {
		return nil, fmt.Errorf("%s has no id %q", path, wantedID)
	}
	return match, nil
}

func selectObjectFields(source map[string]any, path string, fields []string) (map[string]any, error) {
	result := make(map[string]any, len(fields))
	for _, field := range fields {
		value, exists := source[field]
		if !exists {
			return nil, fmt.Errorf("%s.%s is required", path, field)
		}
		result[field] = value
	}
	return result, nil
}

func selectOptionalObjectFields(source map[string]any, path string, fields []string) (map[string]any, error) {
	result := make(map[string]any, len(fields))
	for _, field := range fields {
		if value, exists := source[field]; exists {
			result[field] = value
		}
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("%s has no represented fields", path)
	}
	return result, nil
}

func normalizedObject(value map[string]any, path string) (map[string]any, error) {
	normalized, err := normalizeJSON(value, false, path)
	if err != nil {
		return nil, err
	}
	object, ok := normalized.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s projection has unexpected type %T", path, normalized)
	}
	return object, nil
}

func safeModulePlanSites(sites []any) ([]any, error) {
	result := make([]any, 0, len(sites))
	for index, rawSite := range sites {
		path := fmt.Sprintf("sites[%d]", index)
		site, err := asObject(rawSite, path)
		if err != nil {
			return nil, err
		}
		id, err := stringField(site, path, "id")
		if err != nil {
			return nil, err
		}
		kind, err := stringField(site, path, "kind")
		if err != nil {
			return nil, err
		}
		failureDomain, err := stringField(site, path, "failureDomain")
		if err != nil {
			return nil, err
		}
		result = append(result, map[string]any{
			"id": id, "kind": kind, "failureDomain": failureDomain,
		})
	}
	normalized, err := normalizeJSON(result, false, "sites")
	if err != nil {
		return nil, err
	}
	projected, ok := normalized.([]any)
	if !ok {
		return nil, fmt.Errorf("safe site projection has unexpected type %T", normalized)
	}
	return projected, nil
}

func modulePlanInputSourceFromResolvedPlan(plan ResolvedPlan) (modulePlanInputSource, error) {
	top := map[string]any(plan)
	stackID, err := stringField(top, "resolvedPlan", "stackId")
	if err != nil {
		return modulePlanInputSource{}, err
	}
	kit, err := objectField(top, "resolvedPlan", "kit")
	if err != nil {
		return modulePlanInputSource{}, err
	}
	sites, err := objectListField(top, "resolvedPlan", "sites")
	if err != nil {
		return modulePlanInputSource{}, err
	}
	controlPlane, err := objectField(top, "resolvedPlan", "controlPlane")
	if err != nil {
		return modulePlanInputSource{}, err
	}
	bridge, _, err := optionalObjectField(top, "resolvedPlan", "bridge")
	if err != nil {
		return modulePlanInputSource{}, err
	}
	identity, err := objectField(top, "resolvedPlan", "identity")
	if err != nil {
		return modulePlanInputSource{}, err
	}
	identityTrust, err := objectField(top, "resolvedPlan", "identityTrust")
	if err != nil {
		return modulePlanInputSource{}, err
	}
	data, err := objectField(top, "resolvedPlan", "data")
	if err != nil {
		return modulePlanInputSource{}, err
	}
	failurePolicy, err := objectField(top, "resolvedPlan", "failurePolicy")
	if err != nil {
		return modulePlanInputSource{}, err
	}
	network, err := objectField(top, "resolvedPlan", "network")
	if err != nil {
		return modulePlanInputSource{}, err
	}
	gates, err := objectField(top, "resolvedPlan", "gates")
	if err != nil {
		return modulePlanInputSource{}, err
	}
	localReachability, err := buildLocalReachability(network, objectMapsAsAny(sites))
	if err != nil {
		return modulePlanInputSource{}, err
	}
	homeLANDiscovery, err := objectField(top, "resolvedPlan", "homeLANDiscovery")
	if err != nil {
		return modulePlanInputSource{}, err
	}
	homeAccessRequirements, err := objectField(top, "resolvedPlan", "homeAccessRequirements")
	if err != nil {
		return modulePlanInputSource{}, err
	}
	externalHomeAccessBindings, err := objectField(top, "resolvedPlan", "externalHomeAccessBindings")
	if err != nil {
		return modulePlanInputSource{}, err
	}
	backupTargetRequirements, err := objectField(top, "resolvedPlan", "backupTargetRequirements")
	if err != nil {
		return modulePlanInputSource{}, err
	}
	externalBackupTargetBindings, err := objectField(top, "resolvedPlan", "externalBackupTargetBindings")
	if err != nil {
		return modulePlanInputSource{}, err
	}
	homeBackupTargetRequirements, err := objectField(top, "resolvedPlan", "homeBackupTargetRequirements")
	if err != nil {
		return modulePlanInputSource{}, err
	}
	externalHomeBackupTargetBindings, err := objectField(top, "resolvedPlan", "externalHomeBackupTargetBindings")
	if err != nil {
		return modulePlanInputSource{}, err
	}
	federationLinkRequirements, err := objectField(top, "resolvedPlan", "federationLinkRequirements")
	if err != nil {
		return modulePlanInputSource{}, err
	}
	externalFederationLinkBindings, err := objectField(top, "resolvedPlan", "externalFederationLinkBindings")
	if err != nil {
		return modulePlanInputSource{}, err
	}
	nodes, err := objectListField(top, "resolvedPlan", "nodes")
	if err != nil {
		return modulePlanInputSource{}, err
	}
	capabilities, err := objectListField(top, "resolvedPlan", "capabilities")
	if err != nil {
		return modulePlanInputSource{}, err
	}
	providers, err := objectListField(top, "resolvedPlan", "providers")
	if err != nil {
		return modulePlanInputSource{}, err
	}
	install, err := objectField(top, "resolvedPlan", "install")
	if err != nil {
		return modulePlanInputSource{}, err
	}
	system, err := objectField(top, "resolvedPlan", "system")
	if err != nil {
		return modulePlanInputSource{}, err
	}
	storage, err := objectField(top, "resolvedPlan", "storage")
	if err != nil {
		return modulePlanInputSource{}, err
	}
	return modulePlanInputSource{
		stackID: stackID, kit: kit, sites: objectMapsAsAny(sites),
		controlPlane: controlPlane, bridge: bridge, identity: identity, identityTrust: identityTrust,
		data: data, failurePolicy: failurePolicy, localReachability: localReachability,
		homeLANDiscovery: homeLANDiscovery, homeAccessRequirements: homeAccessRequirements, externalHomeAccessBindings: externalHomeAccessBindings,
		backupTargetRequirements: backupTargetRequirements, externalBackupTargetBindings: externalBackupTargetBindings,
		homeBackupTargetRequirements: homeBackupTargetRequirements, externalHomeBackupTargetBindings: externalHomeBackupTargetBindings,
		federationLinkRequirements: federationLinkRequirements, externalFederationLinkBindings: externalFederationLinkBindings,
		nodes: objectMapsAsAny(nodes), capabilities: objectMapsAsAny(capabilities), providers: objectMapsAsAny(providers),
		install: install, system: system, storage: storage, network: network, gates: gates,
	}, nil
}
