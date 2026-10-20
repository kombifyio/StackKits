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

// validateWorkloadCompanionPlacement rejects a selected add-on workload whose
// resolved nodes the peer primary cannot wire. A runtime component that
// declares peerNetworks joins the internal network of the named primary
// workload, and a container network never spans two nodes, so every node the
// add-on resolves to must also host that primary. workloadCompanionsForModule
// silently omits an add-on that shares no node with its primary; without this
// check a spec that splits the pair compiles and the add-on is left unwired.
// It runs once, after module targets narrowed every workload's nodeRefs and
// before any module input binds.
func validateWorkloadCompanionPlacement(workloads, modules []any) error {
	nodesByWorkload := map[string][]string{}
	moduleByWorkload := map[string]string{}
	order := []string{}
	for index, raw := range workloads {
		path := fmt.Sprintf("workloads[%d]", index)
		workload, err := asObject(raw, path)
		if err != nil {
			return err
		}
		id, err := stringField(workload, path, "id")
		if err != nil {
			return err
		}
		alternative, err := objectField(workload, path, "alternative")
		if err != nil {
			return err
		}
		moduleRef, err := stringField(alternative, path+".alternative", "moduleRef")
		if err != nil {
			return err
		}
		nodeRefs, err := stringListField(workload, path, "nodeRefs", false)
		if err != nil {
			return err
		}
		nodesByWorkload[id] = nodeRefs
		moduleByWorkload[id] = moduleRef
		order = append(order, id)
	}
	peersByModule := map[string][]string{}
	for index, raw := range modules {
		path := fmt.Sprintf("modules[%d]", index)
		module, err := asObject(raw, path)
		if err != nil {
			return err
		}
		id, err := stringField(module, path, "id")
		if err != nil {
			return err
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
					return fmt.Errorf("%s peer network is not an object", path)
				}
				if ref, _ := peer["workloadRef"].(string); ref != "" {
					peersByModule[id] = append(peersByModule[id], ref)
				}
			}
		}
	}
	for _, id := range order {
		for _, peerRef := range sortStringsUnique(peersByModule[moduleByWorkload[id]]) {
			primary, selected := nodesByWorkload[peerRef]
			if !selected || peerRef == id {
				continue
			}
			for _, nodeRef := range nodesByWorkload[id] {
				if !slices.Contains(primary, nodeRef) {
					return fail(ErrUnresolvedPlacement, "spec.workloads."+id+".placement",
						"add-on workload %q joins the internal network of workload %q, which does not run on node %q; place the add-on only on nodes that host %q (resolved add-on nodes %v, primary nodes %v)",
						id, peerRef, nodeRef, peerRef, nodesByWorkload[id], primary)
				}
			}
		}
	}
	return nil
}

// companionPeerWorkloads names the selected workloads whose internal network a
// workload's module joins through peerNetworks (an add-on and its primary). The
// workload itself is never its own peer.
func companionPeerWorkloads(catalog *indexedCatalog, workloads map[string]*resolvedWorkloadSelection, workloadID, moduleID string) ([]string, error) {
	contract, exists := catalog.modules[moduleID]
	if !exists {
		return nil, nil
	}
	runtime, declared, err := optionalObjectField(contract, "catalog.modules."+moduleID, "runtime")
	if err != nil || !declared {
		return nil, err
	}
	components, err := objectListOptional(runtime, "components")
	if err != nil {
		return nil, err
	}
	var peers []string
	for _, component := range components {
		entries, err := objectListOptional(component, "peerNetworks")
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			ref, _ := entry["workloadRef"].(string)
			if _, selected := workloads[ref]; selected && ref != workloadID {
				peers = append(peers, ref)
			}
		}
	}
	return sortStringsUnique(peers), nil
}

// workloadNodesPinned reports whether the spec pins the workload to explicit
// nodes. Site and role filters are not pins.
func workloadNodesPinned(spec *specView, workloadID string) (bool, error) {
	selection, declared, err := optionalObjectField(spec.workloads, "spec.workloads", workloadID)
	if err != nil || !declared {
		return false, err
	}
	placement, _, err := optionalObjectField(selection, "spec.workloads."+workloadID, "placement")
	if err != nil {
		return false, err
	}
	pins, err := stringListField(placement, "spec.workloads."+workloadID+".placement", "nodeRefs", false)
	return len(pins) > 0, err
}

// followCompanionPrimaries narrows every add-on that pins no node to the nodes
// of the selected primary it joins, before any module targets resolve: the
// add-on joins the primary's internal network and a network never spans nodes.
// Only an add-on the spec pins explicitly can then end up outside its primary's
// nodes, and validateWorkloadCompanionPlacement rejects exactly that.
//
// A companion of a companion narrows against its peer's already narrowed nodes,
// so the pass repeats until nothing changes; each repetition only removes
// nodes, so it ends within len(workloads)+1 rounds and never depends on the
// order the workload IDs sort in.
func followCompanionPrimaries(spec *specView, catalog *indexedCatalog, workloads map[string]*resolvedWorkloadSelection) error {
	ids := sortedStringMapKeys(workloads)
	for round := 0; round <= len(ids); round++ {
		changed := false
		for _, id := range ids {
			workload := workloads[id]
			pinned, err := workloadNodesPinned(spec, id)
			if err != nil {
				return err
			}
			if pinned {
				continue
			}
			peers, err := companionPeerWorkloads(catalog, workloads, id, workload.moduleID)
			if err != nil {
				return err
			}
			nodeRefs := workload.nodeRefs
			for _, peer := range peers {
				nodeRefs = intersectStrings(nodeRefs, workloads[peer].nodeRefs)
			}
			if len(peers) == 0 || len(nodeRefs) == len(workload.nodeRefs) {
				continue
			}
			if len(nodeRefs) == 0 {
				return fail(ErrUnresolvedPlacement, "spec.workloads."+id+".placement", "add-on workload %q joins the internal network of %v, but no node is eligible for both", id, peers)
			}
			sites := make([]string, 0, len(nodeRefs))
			for _, nodeRef := range nodeRefs {
				sites = append(sites, spec.nodeByID[nodeRef].siteRef)
			}
			workload.nodeRefs, workload.siteRefs = nodeRefs, sortStringsUnique(sites)
			changed = true
		}
		if !changed {
			break
		}
	}
	return nil
}
