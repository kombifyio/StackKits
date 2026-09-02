package resolvedplan

import (
	"encoding/json"
	"fmt"
	"math"
)

// applyDataCapacityAdmission extends the existing module runtime admission
// with explicit DataBinding budgets. A budget is reserved once for each
// actual (binding, node) placement. Data classes, declared replicas, and
// multiple workloads sharing that placement do not multiply the reservation.
// The free-space fact must identify the exact resolved storage target; a
// generic root-filesystem observation cannot authorize this admission.
func applyDataCapacityAdmission(modules, workloads []any, data, storage, system map[string]any, nodes []nodeView) error {
	demands, err := dataCapacityDemands(data)
	if err != nil {
		return err
	}
	if len(demands) == 0 {
		return nil
	}

	moduleIDs := make(map[string]struct{}, len(modules))
	for index, raw := range modules {
		module, err := asObject(raw, fmt.Sprintf("resolvedPlan.modules[%d]", index))
		if err != nil {
			return err
		}
		id, err := stringField(module, fmt.Sprintf("resolvedPlan.modules[%d]", index), "id")
		if err != nil {
			return err
		}
		moduleIDs[id] = struct{}{}
	}

	nodeByID := make(map[string]nodeView, len(nodes))
	for _, node := range nodes {
		nodeByID[node.id] = node
	}
	used := make(map[string]bool, len(demands))
	placements := make(map[dataCapacityPlacement]struct{})
	moduleStatus := make(map[string]string)
	nodeDemand := make(map[string]float64)
	for index, raw := range workloads {
		path := fmt.Sprintf("resolvedPlan.workloads[%d]", index)
		workload, err := asObject(raw, path)
		if err != nil {
			return err
		}
		alternative, err := objectField(workload, path, "alternative")
		if err != nil {
			return err
		}
		infrastructure, exists, err := optionalObjectField(alternative, path+".alternative", "infrastructure")
		if err != nil {
			return err
		}
		if !exists {
			continue
		}
		binding, exists, err := optionalObjectField(infrastructure, path+".alternative.infrastructure", "dataBinding")
		if err != nil {
			return err
		}
		if !exists {
			continue
		}
		bindingRef, err := stringField(binding, path+".alternative.infrastructure.dataBinding", "bindingRef")
		if err != nil {
			return err
		}
		demand, demanded := demands[bindingRef]
		if !demanded {
			continue
		}
		used[bindingRef] = true
		moduleRef, err := stringField(alternative, path+".alternative", "moduleRef")
		if err != nil {
			return err
		}
		if _, exists := moduleIDs[moduleRef]; !exists {
			return fail(ErrContractConflict, path+".alternative.moduleRef", "capacity-demand binding %q is attached to unknown module %q", bindingRef, moduleRef)
		}
		nodeRefs, err := stringListField(workload, path, "nodeRefs", true)
		if err != nil {
			return err
		}
		if len(nodeRefs) == 0 {
			return fail(ErrContractConflict, path+".nodeRefs", "capacity-demand workload requires at least one selected node placement")
		}
		// Every module using the binding is affected, even when another module
		// already registered the same physical (binding, node) reservation.
		moduleStatus[moduleRef] = mergeRuntimeAdmissionStatus(moduleStatus[moduleRef], "ready")
		for _, nodeRef := range nodeRefs {
			if _, exists := nodeByID[nodeRef]; !exists {
				return fail(ErrInvalidInput, path+".nodeRefs", "capacity-demand workload targets unknown node %q", nodeRef)
			}
			placement := dataCapacityPlacement{bindingRef: bindingRef, nodeRef: nodeRef}
			if _, seen := placements[placement]; seen {
				continue
			}
			placements[placement] = struct{}{}
			nodeDemand[nodeRef] += demand.requiredGiB
		}
	}
	for bindingRef := range demands {
		if !used[bindingRef] {
			return fail(ErrContractConflict, "resolvedPlan.data.bindings."+bindingRef+".capacityDemand", "capacity demand is not bound to a selected workload placement")
		}
	}

	volumeDriver, err := stringField(storage, "resolvedPlan.storage", "volumeDriver")
	if err != nil {
		return err
	}
	if volumeDriver != "local" {
		// Local free space cannot attest an NFS or otherwise remote storage
		// authority. Keep generation inspectable, but block Apply explicitly.
		for moduleRef := range moduleStatus {
			moduleStatus[moduleRef] = mergeRuntimeAdmissionStatus(moduleStatus[moduleRef], "unverified")
		}
	} else {
		sourceRef, storagePath, err := resolvedStorageTarget(storage, system)
		if err != nil {
			return err
		}
		for nodeRef, requiredGiB := range nodeDemand {
			status, err := storageCapacityAdmission(nodeByID[nodeRef], sourceRef, storagePath, requiredGiB)
			if err != nil {
				return err
			}
			for index, raw := range workloads {
				path := fmt.Sprintf("resolvedPlan.workloads[%d]", index)
				workload, err := asObject(raw, path)
				if err != nil {
					return err
				}
				alternative, err := objectField(workload, path, "alternative")
				if err != nil {
					return err
				}
				infrastructure, exists, err := optionalObjectField(alternative, path+".alternative", "infrastructure")
				if err != nil || !exists {
					if err != nil {
						return err
					}
					continue
				}
				binding, exists, err := optionalObjectField(infrastructure, path+".alternative.infrastructure", "dataBinding")
				if err != nil || !exists {
					if err != nil {
						return err
					}
					continue
				}
				bindingRef, err := stringField(binding, path+".alternative.infrastructure.dataBinding", "bindingRef")
				if err != nil {
					return err
				}
				if _, demanded := demands[bindingRef]; !demanded {
					continue
				}
				nodeRefs, err := stringListField(workload, path, "nodeRefs", true)
				if err != nil {
					return err
				}
				if !contains(nodeRefs, nodeRef) {
					continue
				}
				moduleRef, err := stringField(alternative, path+".alternative", "moduleRef")
				if err != nil {
					return err
				}
				moduleStatus[moduleRef] = mergeRuntimeAdmissionStatus(moduleStatus[moduleRef], status)
			}
		}
	}

	for index, raw := range modules {
		module, err := asObject(raw, fmt.Sprintf("resolvedPlan.modules[%d]", index))
		if err != nil {
			return err
		}
		moduleRef, err := stringField(module, fmt.Sprintf("resolvedPlan.modules[%d]", index), "id")
		if err != nil {
			return err
		}
		status, selected := moduleStatus[moduleRef]
		if !selected {
			continue
		}
		if existing, ok := module["runtimeAdmission"].(map[string]any); ok {
			existingStatus, err := stringField(existing, "resolvedPlan.modules."+moduleRef+".runtimeAdmission", "status")
			if err != nil {
				return err
			}
			status = mergeRuntimeAdmissionStatus(status, existingStatus)
		}
		module["runtimeAdmission"] = map[string]any{"status": status}
	}
	return nil
}

type dataCapacityDemand struct {
	requiredGiB float64
}

type dataCapacityPlacement struct {
	bindingRef string
	nodeRef    string
}

func dataCapacityDemands(data map[string]any) (map[string]dataCapacityDemand, error) {
	bindings, exists, err := optionalObjectField(data, "resolvedPlan.data", "bindings")
	if err != nil {
		return nil, err
	}
	if !exists {
		return map[string]dataCapacityDemand{}, nil
	}
	demands := make(map[string]dataCapacityDemand)
	for bindingRef, rawBinding := range bindings {
		path := "resolvedPlan.data.bindings." + bindingRef
		binding, err := asObject(rawBinding, path)
		if err != nil {
			return nil, err
		}
		demand, exists, err := optionalObjectField(binding, path, "capacityDemand")
		if err != nil {
			return nil, err
		}
		if !exists {
			continue
		}
		requiredGiB, err := nonNegativeCapacityNumber(demand, path+".capacityDemand", "requiredGiB")
		if err != nil {
			return nil, err
		}
		demands[bindingRef] = dataCapacityDemand{requiredGiB: requiredGiB}
	}
	return demands, nil
}

func resolvedStorageTarget(storage, system map[string]any) (sourceRef, storagePath string, err error) {
	if container, exists, fieldErr := optionalObjectField(system, "resolvedPlan.system", "container"); fieldErr != nil {
		return "", "", fieldErr
	} else if exists {
		storagePath, err = stringField(container, "resolvedPlan.system.container", "dataRoot")
		return "system.container.dataRoot", storagePath, err
	}
	storagePath, err = stringField(storage, "resolvedPlan.storage", "dataRoot")
	return "storage.dataRoot", storagePath, err
}

func storageCapacityAdmission(node nodeView, sourceRef, storagePath string, requiredGiB float64) (string, error) {
	capacity, exists, err := optionalObjectField(node.inventoryFacts, "inventory.nodes."+node.id, "storageCapacity")
	if err != nil {
		return "", err
	}
	if !exists {
		return "unverified", nil
	}
	observedSource, err := stringField(capacity, "inventory.nodes."+node.id+".storageCapacity", "sourceRef")
	if err != nil {
		return "", err
	}
	observedPath, err := stringField(capacity, "inventory.nodes."+node.id+".storageCapacity", "path")
	if err != nil {
		return "", err
	}
	freeGiB, err := nonNegativeCapacityNumber(capacity, "inventory.nodes."+node.id+".storageCapacity", "freeGiB")
	if err != nil {
		return "", err
	}
	if observedSource != sourceRef || observedPath != storagePath {
		return "unverified", nil
	}
	if freeGiB < requiredGiB {
		return "unsatisfied", nil
	}
	return "ready", nil
}

func nonNegativeCapacityNumber(object map[string]any, path, name string) (float64, error) {
	value, exists := object[name]
	if !exists {
		return 0, fail(ErrInvalidInput, joinPath(path, name), "required capacity number is missing")
	}
	var number float64
	switch typed := value.(type) {
	case json.Number:
		parsed, err := typed.Float64()
		if err != nil {
			return 0, fail(ErrInvalidInput, joinPath(path, name), "invalid capacity number")
		}
		number = parsed
	case float64:
		number = typed
	case int:
		number = float64(typed)
	case int64:
		number = float64(typed)
	case uint64:
		number = float64(typed)
	default:
		return 0, fail(ErrInvalidInput, joinPath(path, name), "expected capacity number")
	}
	if math.IsNaN(number) || math.IsInf(number, 0) || number < 0 {
		return 0, fail(ErrInvalidInput, joinPath(path, name), "capacity number must be finite and non-negative")
	}
	return number, nil
}
