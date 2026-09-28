package resolvedplan

import (
	"fmt"
	"slices"
)

// applySandboxRuntimeAdmission admits a module whose component runs under a
// sandbox runtime (gVisor's runsc) against the container runtimes attested on
// each placed node. Like the accelerator pass it only ever tightens
// runtimeAdmission: a node whose inventory carries no containerRuntimes fact
// was not observed (unverified); an observed node whose Docker daemon has not
// registered the runtime is unsatisfied, so Apply refuses before the agent
// harness is installed on a host that would run it without the sandbox.
func applySandboxRuntimeAdmission(modules []any, nodes []nodeView) error {
	nodeByID := make(map[string]nodeView, len(nodes))
	for _, node := range nodes {
		nodeByID[node.id] = node
	}
	for index, raw := range modules {
		path := fmt.Sprintf("resolvedPlan.modules[%d]", index)
		module, err := asObject(raw, path)
		if err != nil {
			return err
		}
		required, err := moduleSandboxRuntimes(module, path)
		if err != nil {
			return err
		}
		if len(required) == 0 {
			continue
		}
		nodeRefs, err := stringListField(module, path, "nodeRefs", true)
		if err != nil {
			return err
		}
		status := "ready"
		for _, nodeRef := range nodeRefs {
			node, exists := nodeByID[nodeRef]
			if !exists {
				return fail(ErrInvalidInput, path+".nodeRefs", "placed module node is absent from the inventory")
			}
			nodeStatus, err := sandboxRuntimeNodeAdmission(required, node.inventoryFacts)
			if err != nil {
				return err
			}
			status = mergeRuntimeAdmissionStatus(status, nodeStatus)
		}
		if existing, ok := module["runtimeAdmission"].(map[string]any); ok {
			existingStatus, err := stringField(existing, path+".runtimeAdmission", "status")
			if err != nil {
				return err
			}
			status = mergeRuntimeAdmissionStatus(status, existingStatus)
		}
		module["runtimeAdmission"] = map[string]any{"status": status}
	}
	return nil
}

// moduleSandboxRuntimes lists the distinct sandbox runtimes the module's
// runtime components declare.
func moduleSandboxRuntimes(module map[string]any, path string) ([]string, error) {
	runtime, declared, err := optionalObjectField(module, path, "runtime")
	if err != nil || !declared {
		return nil, err
	}
	rawComponents, exists := runtime["components"]
	if !exists {
		return nil, nil
	}
	components, ok := rawComponents.([]any)
	if !ok {
		return nil, fail(ErrInvalidInput, path+".runtime.components", "must be a list")
	}
	var required []string
	for position, entry := range components {
		componentPath := fmt.Sprintf("%s.runtime.components[%d]", path, position)
		component, err := asObject(entry, componentPath)
		if err != nil {
			return nil, err
		}
		runtimeName, present, err := optionalStringField(component, componentPath, "sandboxRuntime")
		if err != nil {
			return nil, err
		}
		if present && !slices.Contains(required, runtimeName) {
			required = append(required, runtimeName)
		}
	}
	return required, nil
}

func sandboxRuntimeNodeAdmission(required []string, facts map[string]any) (string, error) {
	raw, observed := facts["containerRuntimes"]
	if !observed || raw == nil {
		return "unverified", nil
	}
	entries, ok := raw.([]any)
	if !ok {
		return "", fail(ErrInvalidInput, "inventory.containerRuntimes", "must be a list")
	}
	registered := make([]string, 0, len(entries))
	for position, entry := range entries {
		name, ok := entry.(string)
		if !ok {
			return "", fail(ErrInvalidInput, fmt.Sprintf("inventory.containerRuntimes[%d]", position), "must be a string")
		}
		registered = append(registered, name)
	}
	for _, runtime := range required {
		if !slices.Contains(registered, runtime) {
			return "unsatisfied", nil
		}
	}
	return "ready", nil
}
