package resolvedplan

import (
	"fmt"

	"github.com/kombifyio/stackkits/internal/capacityfit"
)

// CapacityContext carries the CUE-owned facts a compiled plan does not embed:
// the kit's resident-load policy and each module's declared load. The zero
// value assesses numeric demand only, so every resident-load check stays off.
type CapacityContext struct {
	Policy     *capacityfit.Policy
	ModuleLoad ModuleLoadResolver
}

// CapacityPolicyFromDefinition reads the resident-load thresholds the kit
// Definition declares next to its host requirements: headroomFactor and
// maxAlwaysOnPerCPU. It returns nil when the Definition declares neither, and
// never supplies a default: every threshold comes from the bound CUE policy.
func CapacityPolicyFromDefinition(definition KitDefinition) (*capacityfit.Policy, error) {
	metadata, _ := definition["metadata"].(map[string]any)
	slug, _ := metadata["slug"].(string)
	requirements, declared, err := optionalObjectField(definition, "definition", "hostRequirements")
	if err != nil || !declared {
		return nil, err
	}
	policy := capacityfit.Policy{Ref: fmt.Sprintf("%s.Definition.hostRequirements", slug)}
	for _, field := range []struct {
		name   string
		target **float64
	}{{"headroomFactor", &policy.HeadroomFactor}, {"maxAlwaysOnPerCPU", &policy.MaxAlwaysOnPerCPU}} {
		if _, present := requirements[field.name]; !present {
			continue
		}
		value, err := resourceNumberField(requirements, "definition.hostRequirements", field.name)
		if err != nil {
			return nil, err
		}
		*field.target = &value
	}
	if policy.HeadroomFactor == nil && policy.MaxAlwaysOnPerCPU == nil {
		return nil, nil
	}
	return &policy, nil
}

// AssessCapacity projects a freshly compiled plan and its exact inventory into
// the shared numeric engine without a resident-load policy. It grants no
// execution or host-attestation rights.
func AssessCapacity(plan ResolvedPlan) (capacityfit.Assessment, error) {
	return AssessCapacityWithContext(plan, CapacityContext{})
}

// AssessCapacityWithContext is AssessCapacity plus the resident-load policy and
// the per-module declared load. The assessment stays advisory and never changes
// the plan or any plan hash.
func AssessCapacityWithContext(plan ResolvedPlan, context CapacityContext) (capacityfit.Assessment, error) {
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
	modules, err := capacityModulesWithLoad(rawModules, context.ModuleLoad)
	if err != nil {
		return capacityfit.Assessment{}, err
	}
	nodes, err := objectField(inventory, "inventory", "nodes")
	if err != nil {
		return capacityfit.Assessment{}, err
	}
	input := capacityfit.Input{Modules: modules, Policy: context.Policy}
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
