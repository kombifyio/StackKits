package resolvedplan

import (
	"fmt"
	"strings"
)

const storageFilesystemRequirementField = "storageFilesystem"

// applyStorageFilesystemAdmission evaluates the generic filesystem
// requirement projected from a selected module profile. It is deliberately a
// separate admission pass because the expected host target is resolved at the
// plan level, while the observed filesystem fact is attached to each node.
// Callers should run it alongside the existing runtime and capacity passes;
// it does not select profiles or alter workload/storage bindings.
func applyStorageFilesystemAdmission(modules []any, storage, system map[string]any, nodes []nodeView) error {
	var sourceRef, storagePath string
	targetResolved := false
	storageDriverStatus := "ready"
	storageDriverResolved := false
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
		requirements, declared, err := optionalObjectField(module, path, "runtimeRequirements")
		if err != nil || !declared {
			if err != nil {
				return err
			}
			continue
		}
		filesystemRequirement, declared, err := optionalObjectField(requirements, path+".runtimeRequirements", storageFilesystemRequirementField)
		if err != nil || !declared {
			if err != nil {
				return err
			}
			continue
		}
		if !targetResolved {
			sourceRef, storagePath, err = resolvedStorageTarget(storage, system)
			if err != nil {
				return err
			}
			targetResolved = true
		}
		if !storageDriverResolved {
			storageDriverStatus, err = storageFilesystemDriverStatus(storage)
			if err != nil {
				return err
			}
			storageDriverResolved = true
		}
		nodeRefs, err := stringListField(module, path, "nodeRefs", true)
		if err != nil {
			return err
		}
		status := storageDriverStatus
		for _, nodeRef := range nodeRefs {
			node, exists := nodeByID[nodeRef]
			if !exists {
				return fail(ErrInvalidInput, path+".nodeRefs", "placed module node is absent from the inventory")
			}
			nodeStatus, err := storageFilesystemNodeAdmission(filesystemRequirement, node, sourceRef, storagePath)
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

func storageFilesystemDriverStatus(storage map[string]any) (string, error) {
	volumeDriver, err := stringField(storage, "resolvedPlan.storage", "volumeDriver")
	if err != nil {
		return "", err
	}
	if volumeDriver == "local" {
		return "ready", nil
	}
	if strings.EqualFold(volumeDriver, "nfs") || strings.EqualFold(volumeDriver, "nfs4") {
		return "unsatisfied", nil
	}
	return "unverified", nil
}

func storageFilesystemNodeAdmission(requirement map[string]any, node nodeView, sourceRef, storagePath string) (string, error) {
	path := "runtimeRequirements." + storageFilesystemRequirementField
	expectedSourceRef, err := stringField(requirement, path, "sourceRef")
	if err != nil {
		return "", err
	}
	requiredClass, err := stringField(requirement, path, "requiredClass")
	if err != nil {
		return "", err
	}
	if !knownStorageFilesystemClass(requiredClass) {
		return "", fail(ErrInvalidInput, path+".requiredClass", "unsupported required filesystem class %q", requiredClass)
	}
	allowedTypes, err := stringListField(requirement, path, "allowedFilesystemTypes", false)
	if err != nil {
		return "", err
	}
	requireOwnership, err := boolFieldDefault(requirement, path, "requireOwnership", false)
	if err != nil {
		return "", err
	}
	// local-posix is this package's bounded class for a known persistent POSIX
	// filesystem with ownership semantics. Keep that invariant even when a
	// profile omits the redundant flag, so an incomplete profile cannot weaken
	// the host prerequisite.
	if requiredClass == "local-posix" {
		requireOwnership = true
	}

	capacity, exists, err := optionalObjectField(node.inventoryFacts, "inventory.nodes."+node.id, "storageCapacity")
	if err != nil {
		return "", err
	}
	if !exists {
		return "unverified", nil
	}
	observedSourceRef, err := stringField(capacity, "inventory.nodes."+node.id+".storageCapacity", "sourceRef")
	if err != nil {
		return "", err
	}
	observedPath, err := stringField(capacity, "inventory.nodes."+node.id+".storageCapacity", "path")
	if err != nil {
		return "", err
	}
	if observedSourceRef != sourceRef || observedPath != storagePath || expectedSourceRef != sourceRef {
		// The source reference and path are the identity of the observed host
		// target. A generic or custom bind cannot stand in for the resolved one.
		return "unverified", nil
	}
	observedType, typeAttested, err := optionalStringField(capacity, "inventory.nodes."+node.id+".storageCapacity", "filesystemType")
	if err != nil {
		return "", err
	}
	observedClass, classAttested, err := optionalStringField(capacity, "inventory.nodes."+node.id+".storageCapacity", "filesystemClass")
	if err != nil {
		return "", err
	}
	if !typeAttested || !classAttested {
		return "unverified", nil
	}
	if !knownStorageFilesystemClass(observedClass) {
		return "", fail(ErrInvalidInput, "inventory.nodes."+node.id+".storageCapacity.filesystemClass", "unsupported observed filesystem class %q", observedClass)
	}
	if observedClass == "unknown" {
		return "unverified", nil
	}
	if observedClass != requiredClass {
		return "unsatisfied", nil
	}
	if len(allowedTypes) > 0 && !containsFold(allowedTypes, observedType) {
		return "unsatisfied", nil
	}
	if requireOwnership {
		ownership, attested := capacity["supportsOwnership"]
		if !attested {
			return "unverified", nil
		}
		supportsOwnership, ok := ownership.(bool)
		if !ok {
			return "", fail(ErrInvalidInput, "inventory.nodes."+node.id+".storageCapacity.supportsOwnership", "expected boolean")
		}
		if !supportsOwnership {
			return "unsatisfied", nil
		}
	}
	return "ready", nil
}

func knownStorageFilesystemClass(value string) bool {
	switch value {
	case "local-posix", "network", "non-posix", "ephemeral", "unknown":
		return true
	default:
		return false
	}
}

func containsFold(values []string, wanted string) bool {
	for _, value := range values {
		if strings.EqualFold(value, wanted) {
			return true
		}
	}
	return false
}
