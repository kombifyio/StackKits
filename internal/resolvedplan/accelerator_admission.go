package resolvedplan

import (
	"fmt"
	"strconv"
	"strings"
)

// applyAcceleratorAdmission admits a selected accelerator profile against the
// GPUs attested on each placed node. It is a separate pass like the storage
// filesystem admission and only ever tightens runtimeAdmission. A node whose
// inventory carries no accelerators fact was not observed (unverified); an
// observed node without a qualifying GPU is unsatisfied, so Apply refuses
// before anything is installed. Container access (CDI, ROCm device nodes) is
// deliberately not admitted here: host preparation provides it and host
// preflight refuses Apply until it exists.
func applyAcceleratorAdmission(modules []any, nodes []nodeView) error {
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
		binding, declared, err := optionalObjectField(module, path, "acceleratorProfileBinding")
		if err != nil {
			return err
		}
		if !declared {
			continue
		}
		requirement, err := objectField(binding, path+".acceleratorProfileBinding", "accelerator")
		if err != nil {
			return err
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
			nodeStatus, err := acceleratorNodeAdmission(requirement, node.inventoryFacts, path+".acceleratorProfileBinding.accelerator")
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

func acceleratorNodeAdmission(requirement, facts map[string]any, path string) (string, error) {
	vendor, err := stringField(requirement, path, "vendor")
	if err != nil {
		return "", err
	}
	minVRAM, err := optionalIntFact(requirement, path, "minVramGiB")
	if err != nil {
		return "", err
	}
	minDriver, err := optionalIntFact(requirement, path, "minDriverMajor")
	if err != nil {
		return "", err
	}
	raw, observed := facts["accelerators"]
	if !observed || raw == nil {
		return "unverified", nil
	}
	accelerators, ok := raw.([]any)
	if !ok {
		return "", fail(ErrInvalidInput, "inventory.accelerators", "must be a list")
	}
	for position, entry := range accelerators {
		entryPath := fmt.Sprintf("inventory.accelerators[%d]", position)
		accelerator, err := asObject(entry, entryPath)
		if err != nil {
			return "", err
		}
		observedVendor, err := stringField(accelerator, entryPath, "vendor")
		if err != nil {
			return "", err
		}
		if observedVendor != vendor {
			continue
		}
		devices, err := intField(accelerator, entryPath, "devices")
		if err != nil {
			return "", err
		}
		if devices < 1 {
			return "unsatisfied", nil
		}
		if minDriver > 0 {
			version, _, err := optionalStringField(accelerator, entryPath, "driverVersion")
			if err != nil {
				return "", err
			}
			major, _, _ := strings.Cut(version, ".")
			if value, err := strconv.Atoi(major); err != nil || value < minDriver {
				return "unsatisfied", nil
			}
		}
		if minVRAM > 0 {
			vram, err := optionalIntFact(accelerator, entryPath, "minVramGiB")
			if err != nil {
				return "", err
			}
			if vram == 0 {
				return "unverified", nil
			}
			if vram < minVRAM {
				return "unsatisfied", nil
			}
		}
		return "ready", nil
	}
	return "unsatisfied", nil
}

func optionalIntFact(object map[string]any, path, name string) (int, error) {
	if _, exists := object[name]; !exists {
		return 0, nil
	}
	return intField(object, path, name)
}
