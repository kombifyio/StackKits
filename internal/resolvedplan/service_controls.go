package resolvedplan

import (
	"fmt"
	"sort"
)

var serviceControlActions = map[string]struct{}{
	"start": {}, "stop": {}, "restart": {}, "logs": {},
}

func resolveModuleServiceControls(moduleID string, contract map[string]any) ([]any, error) {
	path := "catalog.modules." + moduleID
	controls, err := objectListOptional(contract, "serviceControls")
	if err != nil {
		return nil, fail(ErrContractConflict, path+".serviceControls", "%v", err)
	}
	if len(controls) == 0 {
		return []any{}, nil
	}
	runtimeContract, err := objectField(contract, path, "runtime")
	if err != nil {
		return nil, err
	}
	components, err := objectListField(runtimeContract, path+".runtime", "components")
	if err != nil {
		return nil, err
	}
	componentIDs := make(map[string]struct{}, len(components))
	for index, component := range components {
		id, err := stringField(component, fmt.Sprintf("%s.runtime.components[%d]", path, index), "id")
		if err != nil {
			return nil, err
		}
		componentIDs[id] = struct{}{}
	}
	serviceRefs, err := moduleServiceEndpointRefs(contract, path)
	if err != nil {
		return nil, err
	}
	seenKeys, seenServices, seenComponents := map[string]struct{}{}, map[string]struct{}{}, map[string]struct{}{}
	normalized := make([]map[string]any, 0, len(controls))
	for index, control := range controls {
		controlPath := fmt.Sprintf("%s.serviceControls[%d]", path, index)
		key, err := stringField(control, controlPath, "key")
		if err != nil {
			return nil, err
		}
		if _, duplicate := seenKeys[key]; duplicate {
			return nil, fail(ErrContractConflict, controlPath+".key", "service key %q is controlled more than once", key)
		}
		seenKeys[key] = struct{}{}
		serviceRef, err := stringField(control, controlPath, "serviceRef")
		if err != nil {
			return nil, err
		}
		if _, duplicate := seenServices[serviceRef]; duplicate {
			return nil, fail(ErrContractConflict, controlPath+".serviceRef", "service %q is controlled more than once", serviceRef)
		}
		if _, declared := serviceRefs[serviceRef]; !declared {
			return nil, fail(ErrContractConflict, controlPath+".serviceRef", "service %q has no module endpoint", serviceRef)
		}
		seenServices[serviceRef] = struct{}{}
		adapter, err := stringField(control, controlPath, "adapter")
		if err != nil {
			return nil, err
		}
		if adapter != "compose" && adapter != "komodo" {
			return nil, fail(ErrContractConflict, controlPath+".adapter", "unsupported service-control adapter %q", adapter)
		}
		runtimeRef, err := stringField(control, controlPath, "runtimeRef")
		if err != nil {
			return nil, err
		}
		componentRefs, err := stringListField(control, controlPath, "componentRefs", true)
		if err != nil {
			return nil, err
		}
		componentRefs = sortStringsUnique(componentRefs)
		for _, componentRef := range componentRefs {
			if _, declared := componentIDs[componentRef]; !declared {
				return nil, fail(ErrContractConflict, controlPath+".componentRefs", "component %q is not declared by the module runtime", componentRef)
			}
			if _, duplicate := seenComponents[componentRef]; duplicate {
				return nil, fail(ErrContractConflict, controlPath+".componentRefs", "component %q belongs to more than one service", componentRef)
			}
			seenComponents[componentRef] = struct{}{}
		}
		actions, err := stringListField(control, controlPath, "allowedActions", true)
		if err != nil {
			return nil, err
		}
		actions = sortStringsUnique(actions)
		for _, action := range actions {
			if _, allowed := serviceControlActions[action]; !allowed {
				return nil, fail(ErrContractConflict, controlPath+".allowedActions", "unsupported service action %q", action)
			}
		}
		critical, err := boolFieldDefault(control, controlPath, "critical", false)
		if err != nil {
			return nil, err
		}
		if critical && contains(actions, "stop") {
			return nil, fail(ErrContractConflict, controlPath+".allowedActions", "critical service cannot allow stop")
		}
		normalized = append(normalized, map[string]any{
			"key": key, "serviceRef": serviceRef, "adapter": adapter, "runtimeRef": runtimeRef,
			"componentRefs": stringSliceAny(componentRefs), "allowedActions": stringSliceAny(actions), "critical": critical,
		})
	}
	sort.Slice(normalized, func(i, j int) bool {
		return normalized[i]["key"].(string) < normalized[j]["key"].(string)
	})
	return objectMapsAsAny(normalized), nil
}

func moduleServiceEndpointRefs(contract map[string]any, path string) (map[string]struct{}, error) {
	units, err := objectListField(contract, path, "renderUnits")
	if err != nil {
		return nil, err
	}
	result := map[string]struct{}{}
	for unitIndex, unit := range units {
		endpoints, err := objectListOptional(unit, "serviceEndpoints")
		if err != nil {
			return nil, fail(ErrContractConflict, fmt.Sprintf("%s.renderUnits[%d].serviceEndpoints", path, unitIndex), "%v", err)
		}
		for endpointIndex, endpoint := range endpoints {
			serviceRef, err := stringField(endpoint, fmt.Sprintf("%s.renderUnits[%d].serviceEndpoints[%d]", path, unitIndex, endpointIndex), "serviceRef")
			if err != nil {
				return nil, err
			}
			result[serviceRef] = struct{}{}
		}
	}
	return result, nil
}
