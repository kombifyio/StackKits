package resolvedplan

import "github.com/kombifyio/stackkits/internal/capacityfit"

// AssessCapacity projects a freshly compiled plan and its exact inventory into
// the shared numeric engine. It grants no execution or host-attestation rights.
func AssessCapacity(plan ResolvedPlan) (capacityfit.Assessment, error) {
	if _, err := VerifyPlanHash(plan); err != nil {
		return capacityfit.Assessment{}, err
	}
	source, err := objectField(plan, "resolvedPlan", "source")
	if err != nil {
		return capacityfit.Assessment{}, err
	}
	boundInventory, err := objectField(source, "resolvedPlan.source", "inventory")
	if err != nil {
		return capacityfit.Assessment{}, err
	}
	inventory, err := objectField(boundInventory, "resolvedPlan.source.inventory", "document")
	if err != nil {
		return capacityfit.Assessment{}, err
	}
	hash, err := canonicalInventoryHash(inventory)
	if err != nil {
		return capacityfit.Assessment{}, err
	}
	if plan["inventoryHash"] != hash || boundInventory["hash"] != hash {
		return capacityfit.Assessment{}, fail(ErrInvalidInput, "inventory", "capacity inventory differs from the compiled plan")
	}
	rawModules, ok := plan["modules"].([]any)
	if !ok {
		return capacityfit.Assessment{}, fail(ErrInvalidInput, "resolvedPlan.modules", "expected compiled modules")
	}
	modules, err := capacityModules(rawModules)
	if err != nil {
		return capacityfit.Assessment{}, err
	}
	nodes, err := objectField(inventory, "inventory", "nodes")
	if err != nil {
		return capacityfit.Assessment{}, err
	}
	input := capacityfit.Input{Modules: modules}
	for id, raw := range nodes {
		facts, err := asObject(raw, "inventory.nodes."+id)
		if err != nil {
			return capacityfit.Assessment{}, err
		}
		capacity := capacityfit.NodeCapacity{NodeRef: id, Origin: capacityfit.Inventory}
		for _, axis := range []capacityfit.Axis{capacityfit.CPU, capacityfit.RAM, capacityfit.Storage} {
			value, exists, err := inventoryIntFact(facts, string(axis))
			if err != nil {
				return capacityfit.Assessment{}, err
			}
			if exists {
				capacity.Values.Set(axis, float64(value))
			}
		}
		input.Capacities = append(input.Capacities, capacity)
	}
	assessment, err := capacityfit.Assess(input)
	if err != nil {
		return capacityfit.Assessment{}, err
	}
	checks, err := compiledDataCapacityChecks(plan, rawModules, nodes)
	if err != nil {
		return capacityfit.Assessment{}, err
	}
	return capacityfit.WithDataStorageChecks(assessment, checks)
}
