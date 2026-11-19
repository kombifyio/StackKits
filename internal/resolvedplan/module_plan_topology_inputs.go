package resolvedplan

import (
	"fmt"
)

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
