package resolvedplan

import (
	"encoding/json"
	"fmt"

	"github.com/kombifyio/stackkits/internal/localbackuppolicy"
)

// localKopiaRenderInputTarget keeps the logical unit placement available while
// binding the source. The source value is logical-unit scoped; the renderer
// filters its explicit application records again for each node-local instance.
// This is necessary because a node-local unit may materialize one value for
// several instances, while the emitted policy must remain instance-local.
func localKopiaRenderInputTarget(unit map[string]any, path string) (moduleRenderInputTarget, error) {
	siteRefs, err := stringListField(unit, path, "siteRefs", true)
	if err != nil {
		return moduleRenderInputTarget{}, err
	}
	nodeRefs, err := stringListField(unit, path, "nodeRefs", true)
	if err != nil {
		return moduleRenderInputTarget{}, err
	}
	if len(siteRefs) == 0 || len(nodeRefs) == 0 {
		return moduleRenderInputTarget{}, fmt.Errorf("local Kopia backup source requires a non-empty node-local render placement")
	}
	return moduleRenderInputTarget{siteRefs: siteRefs, nodeRefs: nodeRefs}, nil
}

// localKopiaBackupSourceProjection is the compiler-owned public projection of
// all backup-enabled Standalone-Compose application allocations. Placement and
// volume identity are retained in every record; the renderer later narrows the
// aggregate logical value to its exact node-local instance. Runtime graphs are
// copied from the already-resolved module contracts; they are never inferred
// from generated Compose output.
func localKopiaBackupSourceProjection(nodes, workloads, modules []any, _ []string, _ []string, coreModuleRef string) (map[string]any, bool, error) {
	nodeSites, err := localKopiaNodeSites(nodes)
	if err != nil {
		return nil, false, err
	}
	applications, runtimes, err := localKopiaApplicationProjection(nodeSites, workloads, modules)
	if err != nil {
		return nil, false, err
	}
	source, err := localbackuppolicy.GovernedSourceWithApplicationVolumesAndRuntimesForCoreModule(coreModuleRef, applications, runtimes)
	if err != nil {
		return nil, false, fmt.Errorf("build governed local Kopia source projection: %w", err)
	}
	encoded, err := json.Marshal(source)
	if err != nil {
		return nil, false, fmt.Errorf("encode governed local Kopia source projection: %w", err)
	}
	var projection map[string]any
	if err := json.Unmarshal(encoded, &projection); err != nil {
		return nil, false, fmt.Errorf("decode governed local Kopia source projection: %w", err)
	}
	return projection, true, nil
}

func localKopiaNodeSites(nodes []any) (map[string]string, error) {
	result := make(map[string]string, len(nodes))
	for index, raw := range nodes {
		path := fmt.Sprintf("resolvedPlan.nodes[%d]", index)
		node, err := asObject(raw, path)
		if err != nil {
			return nil, err
		}
		nodeRef, err := stringField(node, path, "id")
		if err != nil {
			return nil, err
		}
		siteRef, err := stringField(node, path, "siteRef")
		if err != nil {
			return nil, err
		}
		if _, exists := result[nodeRef]; exists {
			return nil, fmt.Errorf("resolved node %q is declared more than once", nodeRef)
		}
		result[nodeRef] = siteRef
	}
	return result, nil
}

func localKopiaApplicationProjection(nodeSites map[string]string, workloads, modules []any) ([]localbackuppolicy.ApplicationVolume, []localbackuppolicy.ApplicationRuntime, error) {
	applications := make([]localbackuppolicy.ApplicationVolume, 0)
	runtimes := make([]localbackuppolicy.ApplicationRuntime, 0)
	moduleByID, err := localKopiaRuntimeModules(modules)
	if err != nil {
		return nil, nil, err
	}
	for index, raw := range workloads {
		path := fmt.Sprintf("resolvedPlan.workloads[%d]", index)
		workload, err := asObject(raw, path)
		if err != nil {
			return nil, nil, err
		}
		kind, err := stringField(workload, path, "kind")
		if err != nil {
			return nil, nil, err
		}
		if kind != "application" {
			continue
		}
		alternative, err := objectField(workload, path, "alternative")
		if err != nil {
			return nil, nil, err
		}
		runtime, exists, err := optionalObjectField(alternative, path+".alternative", "runtime")
		if err != nil {
			return nil, nil, err
		}
		if !exists {
			continue
		}
		adapter, exists, err := optionalObjectField(runtime, path+".alternative.runtime", "adapter")
		if err != nil {
			return nil, nil, err
		}
		if !exists {
			continue
		}
		adapterID, err := stringField(adapter, path+".alternative.runtime.adapter", "id")
		if err != nil {
			return nil, nil, err
		}
		if adapterID != "standalone-compose" {
			continue
		}
		workloadRef, err := stringField(workload, path, "id")
		if err != nil {
			return nil, nil, err
		}
		moduleRef, err := stringField(alternative, path+".alternative", "moduleRef")
		if err != nil {
			return nil, nil, err
		}
		siteRefs, err := stringListField(workload, path, "siteRefs", true)
		if err != nil {
			return nil, nil, err
		}
		nodeRefs, err := stringListField(workload, path, "nodeRefs", true)
		if err != nil {
			return nil, nil, err
		}
		infrastructure, exists, err := optionalObjectField(alternative, path+".alternative", "infrastructure")
		if err != nil {
			return nil, nil, err
		}
		if !exists {
			continue
		}
		selected, err := localKopiaWorkloadBackupAllocations(infrastructure, path+".alternative.infrastructure")
		if err != nil {
			return nil, nil, err
		}
		if len(selected) == 0 {
			continue
		}
		module, exists := moduleByID[moduleRef]
		if !exists {
			return nil, nil, fmt.Errorf("Standalone-Compose workload %q backup selection has no resolved module %q", workloadRef, moduleRef)
		}
		components, err := localKopiaRuntimeComponents(module, "resolvedPlan.modules."+moduleRef)
		if err != nil {
			return nil, nil, err
		}
		for _, nodeRef := range nodeRefs {
			siteRef, exists := nodeSites[nodeRef]
			if !exists {
				return nil, nil, fmt.Errorf("Standalone-Compose workload %q targets unresolved node %q", workloadRef, nodeRef)
			}
			if !contains(siteRefs, siteRef) {
				return nil, nil, fmt.Errorf("Standalone-Compose workload %q node %q is outside its declared Site placement", workloadRef, nodeRef)
			}
			runtimes = append(runtimes, localbackuppolicy.ApplicationRuntime{
				WorkloadRef: workloadRef, SiteRef: siteRef, NodeRef: nodeRef,
				ComposeProject: localbackuppolicy.StandaloneComposeProjectName(workloadRef, nodeRef),
				Components:     append([]localbackuppolicy.ApplicationRuntimeComponent(nil), components...),
			})
			for _, allocation := range selected {
				applications = append(applications, localbackuppolicy.ApplicationVolume{
					WorkloadRef: workloadRef, SiteRef: siteRef, NodeRef: nodeRef,
					ComposeProject: localbackuppolicy.StandaloneComposeProjectName(workloadRef, nodeRef),
					ComponentRef:   allocation.componentRef, VolumeRef: allocation.volumeRef,
					LogicalName: localbackuppolicy.StandaloneComposeLogicalVolumeName(allocation.componentRef, allocation.volumeRef),
					VolumeName:  localbackuppolicy.StandaloneComposeVolumeName(workloadRef, nodeRef, allocation.componentRef, allocation.volumeRef),
					Target:      allocation.target, Class: allocation.class, Backup: true,
					DataClasses: append([]string(nil), allocation.dataClasses...), DataBindingRef: allocation.dataBindingRef,
				})
			}
		}
	}
	return applications, runtimes, nil
}

func localKopiaRuntimeModules(modules []any) (map[string]map[string]any, error) {
	result := make(map[string]map[string]any, len(modules))
	for index, raw := range modules {
		path := fmt.Sprintf("resolvedPlan.modules[%d]", index)
		module, err := asObject(raw, path)
		if err != nil {
			return nil, err
		}
		id, err := stringField(module, path, "id")
		if err != nil {
			return nil, err
		}
		if _, exists := result[id]; exists {
			return nil, fmt.Errorf("resolved module %q is declared more than once", id)
		}
		result[id] = module
	}
	return result, nil
}

func localKopiaRuntimeComponents(module map[string]any, path string) ([]localbackuppolicy.ApplicationRuntimeComponent, error) {
	runtime, err := objectField(module, path, "runtime")
	if err != nil {
		return nil, err
	}
	rawComponents, err := objectListField(runtime, path+".runtime", "components")
	if err != nil {
		return nil, fmt.Errorf("%s.runtime.components is required for a selected backup graph: %w", path, err)
	}
	components := make([]localbackuppolicy.ApplicationRuntimeComponent, len(rawComponents))
	for index, raw := range rawComponents {
		componentPath := fmt.Sprintf("%s.runtime.components[%d]", path, index)
		component, err := asObject(raw, componentPath)
		if err != nil {
			return nil, err
		}
		id, err := stringField(component, componentPath, "id")
		if err != nil {
			return nil, err
		}
		role, err := stringField(component, componentPath, "role")
		if err != nil {
			return nil, err
		}
		lifecycle, err := stringField(component, componentPath, "lifecycle")
		if err != nil {
			return nil, err
		}
		image, err := objectField(component, componentPath, "image")
		if err != nil {
			return nil, err
		}
		imageRef, err := stringField(image, componentPath+".image", "ref")
		if err != nil {
			return nil, err
		}
		imageDigest, err := stringField(image, componentPath+".image", "digest")
		if err != nil {
			return nil, err
		}
		dependsOn, err := stringListField(component, componentPath, "dependsOn", false)
		if err != nil {
			return nil, err
		}
		components[index] = localbackuppolicy.ApplicationRuntimeComponent{
			ComponentRef: id, Role: role, Lifecycle: lifecycle,
			ImageRef: imageRef, ImageDigest: imageDigest,
			DependsOn: append([]string(nil), dependsOn...),
		}
	}
	return components, nil
}

type localKopiaBackupAllocation struct {
	componentRef, volumeRef, target, class, dataBindingRef string
	dataClasses                                            []string
}

func localKopiaWorkloadBackupAllocations(infrastructure map[string]any, path string) ([]localKopiaBackupAllocation, error) {
	storage, err := objectField(infrastructure, path, "storageAllocation")
	if err != nil {
		return nil, err
	}
	storageAllocations, err := objectListField(storage, path+".storageAllocation", "allocations")
	if err != nil {
		return nil, err
	}
	binding, err := objectField(infrastructure, path, "dataBinding")
	if err != nil {
		return nil, err
	}
	bindingRef, err := stringField(binding, path+".dataBinding", "bindingRef")
	if err != nil {
		return nil, err
	}
	backupSource, err := objectField(infrastructure, path, "backupSource")
	if err != nil {
		return nil, err
	}
	sourceAllocations, err := objectListField(backupSource, path+".backupSource", "allocations")
	if err != nil {
		return nil, err
	}
	sourceClasses := make(map[string][]string, len(sourceAllocations))
	for index, source := range sourceAllocations {
		sourcePath := fmt.Sprintf("%s.backupSource.allocations[%d]", path, index)
		componentRef, err := stringField(source, sourcePath, "componentRef")
		if err != nil {
			return nil, err
		}
		volumeRef, err := stringField(source, sourcePath, "volumeRef")
		if err != nil {
			return nil, err
		}
		classes, err := stringListField(source, sourcePath, "dataClasses", true)
		if err != nil {
			return nil, err
		}
		key := componentRef + "/" + volumeRef
		if _, exists := sourceClasses[key]; exists {
			return nil, fmt.Errorf("%s repeats backup source %q", sourcePath, key)
		}
		sourceClasses[key] = classes
	}
	selected := make([]localKopiaBackupAllocation, 0, len(storageAllocations))
	seenStorage := make(map[string]struct{}, len(storageAllocations))
	for index, allocation := range storageAllocations {
		allocationPath := fmt.Sprintf("%s.storageAllocation.allocations[%d]", path, index)
		backup, err := boolFieldDefault(allocation, allocationPath, "backup", false)
		if err != nil {
			return nil, err
		}
		if !backup {
			continue
		}
		componentRef, err := stringField(allocation, allocationPath, "componentRef")
		if err != nil {
			return nil, err
		}
		volumeRef, err := stringField(allocation, allocationPath, "volumeRef")
		if err != nil {
			return nil, err
		}
		key := componentRef + "/" + volumeRef
		if _, exists := seenStorage[key]; exists {
			return nil, fmt.Errorf("%s repeats backup-enabled storage allocation %q", allocationPath, key)
		}
		seenStorage[key] = struct{}{}
		classes, err := stringListField(allocation, allocationPath, "dataClasses", true)
		if err != nil {
			return nil, err
		}
		if source, exists := sourceClasses[key]; !exists || !equalStringSets(source, classes) {
			return nil, fmt.Errorf("%s is not an exact backup-source allocation", allocationPath)
		} else {
			delete(sourceClasses, key)
		}
		class, err := stringField(allocation, allocationPath, "class")
		if err != nil {
			return nil, err
		}
		if class != "persistent" {
			return nil, fmt.Errorf("%s backup-enabled allocation must be persistent", allocationPath)
		}
		target, err := stringField(allocation, allocationPath, "target")
		if err != nil {
			return nil, err
		}
		allocationBindingRef, err := stringField(allocation, allocationPath, "dataBindingRef")
		if err != nil {
			return nil, err
		}
		if allocationBindingRef != bindingRef {
			return nil, fmt.Errorf("%s dataBindingRef does not match the workload data binding", allocationPath)
		}
		selected = append(selected, localKopiaBackupAllocation{
			componentRef: componentRef, volumeRef: volumeRef, target: target, class: class,
			dataBindingRef: allocationBindingRef, dataClasses: classes,
		})
	}
	if len(sourceClasses) != 0 {
		return nil, fmt.Errorf("%s.backupSource.allocations contains an unbound backup source", path)
	}
	return selected, nil
}
