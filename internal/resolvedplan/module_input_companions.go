package resolvedplan

import (
	"fmt"
	"sort"
)

// workloadCompanionsForModule returns the selected add-on workload
// alternatives that join the internal network of a workload realized by
// moduleID (a runtime component declares peerNetworks naming that workload)
// and share at least one node with it. It is the closed view a primary
// application needs to wire its selected add-ons; nothing else about the
// other workloads is projected.
func workloadCompanionsForModule(workloads, modules []any, moduleID string) ([]any, error) {
	own := map[string][]string{}
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
		companions = append(companions, map[string]any{"workloadRef": id, "alternativeRef": alternativeID})
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
