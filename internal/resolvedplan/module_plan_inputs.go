package resolvedplan

import "fmt"

var allowedModulePlanInputRefs = map[string]struct{}{
	"stackId": {}, "kit": {}, "sites": {}, "controlPlane": {},
	"bridge": {}, "identity": {}, "data": {}, "failurePolicy": {},
	"identityTrust":     {},
	"localReachability": {},
	"homeLANDiscovery":  {},
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
	stackID           string
	kit               map[string]any
	sites             []any
	controlPlane      map[string]any
	bridge            map[string]any
	identity          map[string]any
	identityTrust     map[string]any
	data              map[string]any
	failurePolicy     map[string]any
	localReachability map[string]any
	homeLANDiscovery  map[string]any
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
				projection, err := source.resolve(ref)
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

func (source modulePlanInputSource) resolve(ref string) (any, error) {
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
	}
	return normalizeJSON(value, false, ref)
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
	localReachability, err := buildLocalReachability(network, objectMapsAsAny(sites))
	if err != nil {
		return modulePlanInputSource{}, err
	}
	homeLANDiscovery, err := objectField(top, "resolvedPlan", "homeLANDiscovery")
	if err != nil {
		return modulePlanInputSource{}, err
	}
	return modulePlanInputSource{
		stackID: stackID, kit: kit, sites: objectMapsAsAny(sites),
		controlPlane: controlPlane, bridge: bridge, identity: identity, identityTrust: identityTrust,
		data: data, failurePolicy: failurePolicy, localReachability: localReachability,
		homeLANDiscovery: homeLANDiscovery,
	}, nil
}
