package resolvedplan

import "fmt"

// validateRuntimeAdmissionProjection uses the compiler's existing admission
// logic, never the persisted decision, to bind readiness to inventory facts.
// This proves internal consistency, not host attestation or inventory freshness;
// execution still requires the current-source and target preflight gates.
func validateRuntimeAdmissionProjection(plan ResolvedPlan) error {
	source, err := objectField(map[string]any(plan), "resolvedPlan", "source")
	if err != nil {
		return err
	}
	inventory, err := objectField(source, "resolvedPlan.source", "inventory")
	if err != nil {
		return err
	}
	document, err := objectField(inventory, "resolvedPlan.source.inventory", "document")
	if err != nil {
		return err
	}
	hash, err := canonicalInventoryHash(document)
	if err != nil {
		return err
	}
	if inventory["hash"] != hash || plan["inventoryHash"] != hash {
		return fmt.Errorf("resolvedPlan.source.inventory.document does not match the inventory hash")
	}
	view, err := resolvedModuleTargetSpec(plan)
	if err != nil {
		return err
	}
	for index := range view.nodes {
		node := &view.nodes[index]
		node.inventoryFacts = map[string]any{}
		node.runtimeDaemons = map[string]runtimeDaemonFact{}
		view.nodeByID[node.id] = *node
	}
	if err := validateInventory(view, document); err != nil {
		return err
	}
	modules, err := objectListField(map[string]any(plan), "resolvedPlan", "modules")
	if err != nil {
		return err
	}
	expected := make([]any, 0, len(modules))
	for index, module := range modules {
		path := fmt.Sprintf("resolvedPlan.modules[%d]", index)
		clone, err := cloneObject(module, true)
		if err != nil {
			return err
		}
		delete(clone, "runtimeAdmission")
		requirements, declared, err := optionalObjectField(module, path, "runtimeRequirements")
		if err != nil {
			return err
		}
		if declared {
			nodeRefs, err := stringListField(module, path, "nodeRefs", true)
			if err != nil {
				return err
			}
			admission, err := evaluateRuntimeAdmission(requirements, nodeRefs, view.nodes)
			if err != nil {
				return err
			}
			clone["runtimeAdmission"] = admission
		}
		expected = append(expected, clone)
	}
	normalizedSpec, err := objectField(source, "resolvedPlan.source", "normalizedSpec")
	if err != nil {
		return err
	}
	// Match the explicit v2alpha1 adapter's historical per-module admission.
	// Aggregate reservation admission belongs to native module-profile intent.
	if normalizedSpec["apiVersion"] == architectureAPIVersionV2Alpha2 {
		demands, err := objectListField(map[string]any(plan), "resolvedPlan", "resourceDemand")
		if err != nil {
			return err
		}
		if err := applyModuleResourceAdmission(expected, objectMapsAsAny(demands), view.nodes); err != nil {
			return err
		}
	}
	for index, module := range modules {
		want := expected[index].(map[string]any)["runtimeAdmission"]
		equal, err := canonicalEqual(module["runtimeAdmission"], want)
		if err != nil {
			return err
		}
		if !equal {
			return fmt.Errorf("resolvedPlan.modules[%d].runtimeAdmission does not match the hash-bound inventory and module demand", index)
		}
	}
	return nil
}
