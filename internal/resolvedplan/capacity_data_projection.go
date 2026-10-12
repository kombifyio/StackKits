package resolvedplan

import (
	"github.com/kombifyio/stackkits/internal/capacityfit"
	"maps"
)

// Reuse the compiler placement ledger and numeric engine. Cloned module maps
// isolate the admission bookkeeping; advisory assessment does not mutate a plan.
func compiledDataCapacityChecks(plan ResolvedPlan, modules []any, inventoryNodes map[string]any) (map[string]capacityfit.Check, error) {
	data, err := objectField(plan, "resolvedPlan", "data")
	if err != nil {
		return nil, err
	}
	storage, err := objectField(plan, "resolvedPlan", "storage")
	if err != nil {
		return nil, err
	}
	system, _, err := optionalObjectField(plan, "resolvedPlan", "system")
	if err != nil {
		return nil, err
	}
	workloads, ok := plan["workloads"].([]any)
	if !ok {
		return nil, fail(ErrInvalidInput, "resolvedPlan.workloads", "expected compiled workload placements")
	}
	isolated := make([]any, len(modules))
	for index, raw := range modules {
		module, err := asObject(raw, "resolvedPlan.modules")
		if err != nil {
			return nil, err
		}
		isolated[index] = maps.Clone(module)
	}
	nodes := make([]nodeView, 0, len(inventoryNodes))
	for id, raw := range inventoryNodes {
		facts, err := asObject(raw, "inventory.nodes."+id)
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, nodeView{id: id, inventoryFacts: facts})
	}
	checks, err := dataCapacityAdmissionChecks(isolated, workloads, data, storage, system, nodes)
	if err != nil {
		return nil, err
	}
	bindings, _ := data["bindings"].(map[string]any)
	for _, raw := range workloads {
		workload, _ := raw.(map[string]any)
		alternative, _ := workload["alternative"].(map[string]any)
		infrastructure, _ := alternative["infrastructure"].(map[string]any)
		binding, _ := infrastructure["dataBinding"].(map[string]any)
		ref, _ := binding["bindingRef"].(string)
		if ref == "" {
			continue
		}
		declared, _ := bindings[ref].(map[string]any)
		if _, known := declared["capacityDemand"]; known {
			continue
		}
		nodeRefs, err := stringListField(workload, "resolvedPlan.workloads", "nodeRefs", true)
		if err != nil {
			return nil, err
		}
		for _, nodeRef := range nodeRefs {
			check, known := checks[nodeRef]
			if check.Status == capacityfit.Fail {
				continue
			}
			if !known {
				check.Axis = capacityfit.DataStorage
				if storage["volumeDriver"] == "local" {
					source, path, err := resolvedStorageTarget(storage, system)
					if err != nil {
						return nil, err
					}
					for _, node := range nodes {
						if node.id == nodeRef {
							check.Observed, err = observedStorageFreeGiB(node, source, path)
							if err != nil {
								return nil, err
							}
						}
					}
				}
			}
			check.Status, check.ReasonCode = capacityfit.Unverified, "DATA_CAPACITY_INTENT_MISSING"
			checks[nodeRef] = check
		}
	}
	return checks, nil
}
