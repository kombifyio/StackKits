package resolvedplan

import (
	"fmt"
	"sort"
)

// buildRouteCapabilityRealizations binds every Definition-required route
// capability to its selected provider authority and, for module-backed
// providers, to exactly one concrete selected module placement. The projection
// is provider-neutral: it contains contract identities and logical topology
// refs only, never infrastructure-provider configuration.
func buildRouteCapabilityRealizations(requirements []routeCapabilityRealizationRequirement, resolved *resolution, modules []any, providerSites map[string][]string, nodeSites map[string]string, catalog *indexedCatalog, path string) ([]any, error) {
	required := append([]routeCapabilityRealizationRequirement(nil), requirements...)
	sort.Slice(required, func(i, j int) bool { return required[i].capabilityRef < required[j].capabilityRef })
	if len(required) == 0 {
		return []any{}, nil
	}
	result := make([]any, 0, len(required))
	seen := map[string]struct{}{}
	for _, requirement := range required {
		capabilityRef := requirement.capabilityRef
		if _, duplicate := seen[capabilityRef]; duplicate {
			return nil, fail(ErrContractConflict, path, "route capability realization %q is duplicated", capabilityRef)
		}
		seen[capabilityRef] = struct{}{}
		providerRef, selected := resolved.providerByCap[capabilityRef]
		if !selected || providerRef == "" {
			return nil, fail(ErrUnrealizedCapability, path, "route capability %q has no selected provider", capabilityRef)
		}
		capabilityContract := catalog.capabilities[capabilityRef]
		if capabilityContract == nil {
			return nil, fail(ErrUnrealizedCapability, path, "route capability %q has no catalog contract", capabilityRef)
		}
		providerContract := catalog.providers[providerRef]
		if providerContract == nil {
			return nil, fail(ErrUnrealizedCapability, path, "route capability %q selects unknown provider %q", capabilityRef, providerRef)
		}
		capabilityHash, err := canonicalHash(capabilityContract, true)
		if err != nil {
			return nil, fmt.Errorf("hash route capability %s: %w", capabilityRef, err)
		}
		providerHash, err := canonicalHash(providerContract, true)
		if err != nil {
			return nil, fmt.Errorf("hash route capability provider %s: %w", providerRef, err)
		}
		realization, err := objectField(providerContract, "catalog.providers."+providerRef, "realization")
		if err != nil {
			return nil, err
		}
		kind, err := stringField(realization, "catalog.providers."+providerRef+".realization", "kind")
		if err != nil {
			return nil, err
		}
		switch kind {
		case "modules", "contract", "topology", "host", "external":
		case "none":
			return nil, fail(ErrUnrealizedCapability, path, "route capability %q uses non-realizing provider %q", capabilityRef, providerRef)
		default:
			return nil, fail(ErrContractConflict, path, "route capability %q uses unsupported provider realization %q", capabilityRef, kind)
		}
		siteRefs := sortStringsUnique(providerSites[providerRef])
		if len(siteRefs) == 0 {
			return nil, fail(ErrUnrealizedCapability, path, "route capability provider %q has no selected site scope", providerRef)
		}
		entry := map[string]any{
			"capabilityRef": capabilityRef, "role": requirement.role, "capabilityContractHash": capabilityHash,
			"providerRef": providerRef, "providerContractHash": providerHash,
			"realizationKind": kind, "providerSiteRefs": stringSliceAny(siteRefs),
		}
		if kind == "modules" {
			owner, err := selectRouteCapabilityModuleOwner(capabilityRef, providerRef, modules, siteRefs, nodeSites, catalog, path)
			if err != nil {
				return nil, err
			}
			entry["moduleOwner"] = owner
		}
		result = append(result, entry)
	}
	return result, nil
}

func selectRouteCapabilityModuleOwner(capabilityRef, providerRef string, modules []any, providerSiteRefs []string, nodeSites map[string]string, catalog *indexedCatalog, path string) (map[string]any, error) {
	var matches []map[string]any
	for index, raw := range modules {
		module, err := asObject(raw, fmt.Sprintf("resolvedPlan.modules[%d]", index))
		if err != nil {
			return nil, err
		}
		moduleProvider, err := stringField(module, fmt.Sprintf("resolvedPlan.modules[%d]", index), "providerRef")
		if err != nil {
			return nil, err
		}
		provides, err := stringListField(module, fmt.Sprintf("resolvedPlan.modules[%d]", index), "provides", true)
		if err != nil {
			return nil, err
		}
		if moduleProvider == providerRef && contains(provides, capabilityRef) {
			matches = append(matches, module)
		}
	}
	if len(matches) != 1 {
		return nil, fail(ErrUnrealizedCapability, path, "route capability %q requires exactly one selected module owner for provider %q; got %d", capabilityRef, providerRef, len(matches))
	}
	module := matches[0]
	moduleRef, err := stringField(module, path+".moduleOwner", "id")
	if err != nil {
		return nil, err
	}
	contract := catalog.modules[moduleRef]
	if contract == nil {
		return nil, fail(ErrUnrealizedModule, path+".moduleOwner", "selected route owner module %q has no catalog contract", moduleRef)
	}
	contractHash, err := canonicalHash(contract, true)
	if err != nil {
		return nil, fmt.Errorf("hash route owner module %s: %w", moduleRef, err)
	}
	haveHash, err := stringField(module, path+".moduleOwner", "contractHash")
	if err != nil {
		return nil, err
	}
	if haveHash != contractHash {
		return nil, fail(ErrContractConflict, path+".moduleOwner.contractHash", "module %q does not match its catalog contract", moduleRef)
	}
	siteRefs, err := stringListField(module, path+".moduleOwner", "siteRefs", true)
	if err != nil {
		return nil, err
	}
	nodeRefs, err := stringListField(module, path+".moduleOwner", "nodeRefs", true)
	if err != nil {
		return nil, err
	}
	siteRefs = sortStringsUnique(siteRefs)
	nodeRefs = sortStringsUnique(nodeRefs)
	providerScope := stringSet(providerSiteRefs)
	moduleScope := stringSet(siteRefs)
	for _, siteRef := range siteRefs {
		if _, allowed := providerScope[siteRef]; !allowed {
			return nil, fail(ErrUnresolvedPlacement, path+".moduleOwner.siteRefs", "module %q site %q is outside provider %q scope", moduleRef, siteRef, providerRef)
		}
	}
	for _, nodeRef := range nodeRefs {
		siteRef, exists := nodeSites[nodeRef]
		if !exists {
			return nil, fail(ErrUnresolvedPlacement, path+".moduleOwner.nodeRefs", "module %q node %q has no resolved site", moduleRef, nodeRef)
		}
		if _, selected := moduleScope[siteRef]; !selected {
			return nil, fail(ErrUnresolvedPlacement, path+".moduleOwner.nodeRefs", "module %q node %q is outside its site scope", moduleRef, nodeRef)
		}
	}
	return map[string]any{
		"moduleRef": moduleRef, "moduleContractHash": contractHash,
		"siteRefs": stringSliceAny(siteRefs), "nodeRefs": stringSliceAny(nodeRefs),
	}, nil
}

func routeCapabilityRequirementsFromProjection(route map[string]any, path string) ([]routeCapabilityRealizationRequirement, error) {
	realizations, err := objectListField(route, path, "capabilityRealizations")
	if err != nil {
		return nil, err
	}
	requirements := make([]routeCapabilityRealizationRequirement, 0, len(realizations))
	seen := map[string]struct{}{}
	for index, realization := range realizations {
		realizationPath := fmt.Sprintf("%s.capabilityRealizations[%d]", path, index)
		capabilityRef, err := stringField(realization, realizationPath, "capabilityRef")
		if err != nil {
			return nil, err
		}
		if _, duplicate := seen[capabilityRef]; duplicate {
			return nil, fmt.Errorf("%s.capabilityRealizations duplicates capability %q", path, capabilityRef)
		}
		seen[capabilityRef] = struct{}{}
		role, err := stringField(realization, realizationPath, "role")
		if err != nil {
			return nil, err
		}
		requirements = append(requirements, routeCapabilityRealizationRequirement{capabilityRef: capabilityRef, role: role})
	}
	return requirements, nil
}

func validateRouteCapabilityRealizationBodies(plan ResolvedPlan, catalog *indexedCatalog, capabilityProviders map[string]string) error {
	modules, err := objectListField(map[string]any(plan), "resolvedPlan", "modules")
	if err != nil {
		return err
	}
	providers, err := objectListField(map[string]any(plan), "resolvedPlan", "providers")
	if err != nil {
		return err
	}
	providerSites := make(map[string][]string, len(providers))
	for index, provider := range providers {
		path := fmt.Sprintf("resolvedPlan.providers[%d]", index)
		id, err := stringField(provider, path, "id")
		if err != nil {
			return err
		}
		providerSites[id], err = stringListField(provider, path, "siteRefs", true)
		if err != nil {
			return err
		}
	}
	nodeSites, _, _, err := resolvedTopologyIndex(plan)
	if err != nil {
		return err
	}
	network, err := objectField(map[string]any(plan), "resolvedPlan", "network")
	if err != nil {
		return err
	}
	routes, err := objectListField(network, "resolvedPlan.network", "routes")
	if err != nil {
		return err
	}
	resolved := &resolution{providerByCap: capabilityProviders}
	for routeIndex, route := range routes {
		path := fmt.Sprintf("resolvedPlan.network.routes[%d]", routeIndex)
		realizations, err := objectListField(route, path, "capabilityRealizations")
		if err != nil {
			return err
		}
		requirements, err := routeCapabilityRequirementsFromProjection(route, path)
		if err != nil {
			return err
		}
		want, err := buildRouteCapabilityRealizations(requirements, resolved, objectMapsAsAny(modules), providerSites, nodeSites, catalog, path+".capabilityRealizations")
		if err != nil {
			return fmt.Errorf("recompute %s.capabilityRealizations: %w", path, err)
		}
		if equal, err := canonicalEqual(objectMapsAsAny(realizations), want); err != nil {
			return err
		} else if !equal {
			return fmt.Errorf("%s.capabilityRealizations is not the exact selected capability/provider/module projection", path)
		}
		exposure, err := stringField(route, path, "exposure")
		if err != nil {
			return err
		}
		if exposure == "local" && len(want) != 0 {
			return fmt.Errorf("%s.capabilityRealizations must be empty for local exposure", path)
		}
	}
	return nil
}
