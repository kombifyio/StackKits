package resolvedplan

import (
	"fmt"
	"slices"
	"sort"
)

// workloadCompanionsForModule returns the selected add-on workload
// alternatives that join the internal network of a workload realized by
// moduleID (a runtime component declares peerNetworks naming that workload)
// and share at least one node with it. It is the closed view a primary
// application needs to wire its selected add-ons; nothing else about the
// other workloads is projected, except the add-on's opaque secret references
// for exactly the slots a component of moduleID names in
// companionSecretEnvironment for that alternative.
func workloadCompanionsForModule(workloads, modules []any, moduleID string) ([]any, error) {
	own := map[string][]string{}
	secretSlots := map[string][]string{}
	for index, raw := range workloads {
		path := fmt.Sprintf("workloads[%d]", index)
		workload, err := asObject(raw, path)
		if err != nil {
			return nil, err
		}
		alternative, err := objectField(workload, path, "alternative")
		if err != nil {
			return nil, err
		}
		moduleRef, err := stringField(alternative, path+".alternative", "moduleRef")
		if err != nil {
			return nil, err
		}
		if moduleRef != moduleID {
			continue
		}
		id, err := stringField(workload, path, "id")
		if err != nil {
			return nil, err
		}
		nodeRefs, err := stringListField(workload, path, "nodeRefs", false)
		if err != nil {
			return nil, err
		}
		own[id] = nodeRefs
	}
	if len(own) == 0 {
		return []any{}, nil
	}
	peersByModule := map[string][]string{}
	for index, raw := range modules {
		path := fmt.Sprintf("modules[%d]", index)
		module, err := asObject(raw, path)
		if err != nil {
			return nil, err
		}
		id, err := stringField(module, path, "id")
		if err != nil {
			return nil, err
		}
		runtime, ok := module["runtime"].(map[string]any)
		if !ok {
			continue
		}
		components, _ := runtime["components"].([]any)
		for _, rawComponent := range components {
			component, ok := rawComponent.(map[string]any)
			if !ok {
				continue
			}
			if id == moduleID {
				entries, _ := component["companionSecretEnvironment"].([]any)
				for _, rawEntry := range entries {
					entry, ok := rawEntry.(map[string]any)
					if !ok {
						return nil, fmt.Errorf("%s companion secret environment entry is not an object", path)
					}
					key, _ := entry["workloadRef"].(string)
					key += "/"
					alternativeRef, _ := entry["alternativeRef"].(string)
					key += alternativeRef
					bindings, _ := entry["secretEnvironment"].(map[string]any)
					for _, rawSlot := range bindings {
						slot, _ := rawSlot.(string)
						if slot != "" && !slices.Contains(secretSlots[key], slot) {
							secretSlots[key] = append(secretSlots[key], slot)
						}
					}
				}
			}
			peers, _ := component["peerNetworks"].([]any)
			for _, rawPeer := range peers {
				peer, ok := rawPeer.(map[string]any)
				if !ok {
					return nil, fmt.Errorf("%s peer network is not an object", path)
				}
				ref, _ := peer["workloadRef"].(string)
				if _, joined := own[ref]; joined {
					peersByModule[id] = append(peersByModule[id], ref)
				}
			}
		}
	}
	companions := []map[string]any{}
	for index, raw := range workloads {
		path := fmt.Sprintf("workloads[%d]", index)
		workload, err := asObject(raw, path)
		if err != nil {
			return nil, err
		}
		alternative, err := objectField(workload, path, "alternative")
		if err != nil {
			return nil, err
		}
		moduleRef, err := stringField(alternative, path+".alternative", "moduleRef")
		if err != nil {
			return nil, err
		}
		joined := peersByModule[moduleRef]
		if len(joined) == 0 || moduleRef == moduleID {
			continue
		}
		nodeRefs, err := stringListField(workload, path, "nodeRefs", false)
		if err != nil {
			return nil, err
		}
		shared := false
		for _, workloadRef := range joined {
			if sharesNode(own[workloadRef], nodeRefs) {
				shared = true
			}
		}
		if !shared {
			continue
		}
		id, err := stringField(workload, path, "id")
		if err != nil {
			return nil, err
		}
		alternativeID, err := stringField(alternative, path+".alternative", "id")
		if err != nil {
			return nil, err
		}
		companion := map[string]any{"workloadRef": id, "alternativeRef": alternativeID}
		if slots := secretSlots[id+"/"+alternativeID]; len(slots) > 0 {
			declared, _ := workload["secretRefs"].(map[string]any)
			sort.Strings(slots)
			custody := make([]any, 0, len(slots))
			for _, slot := range slots {
				ref, _ := declared[slot].(string)
				if ref == "" {
					return nil, fmt.Errorf("%s does not declare secret slot %q that module %q binds as a companion secret", path, slot, moduleID)
				}
				custody = append(custody, map[string]any{"slot": slot, "ref": ref})
			}
			companion["custody"] = custody
		}
		companions = append(companions, companion)
	}
	sort.Slice(companions, func(i, j int) bool {
		return companions[i]["workloadRef"].(string) < companions[j]["workloadRef"].(string)
	})
	result := make([]any, 0, len(companions))
	for _, companion := range companions {
		result = append(result, companion)
	}
	return result, nil
}

func sharesNode(left, right []string) bool {
	for _, a := range left {
		for _, b := range right {
			if a == b {
				return true
			}
		}
	}
	return false
}
