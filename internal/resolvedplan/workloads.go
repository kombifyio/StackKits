package resolvedplan

import (
	"fmt"
	"sort"
)

type resolvedWorkloadSelection struct {
	id                        string
	alternativeID             string
	providerID                string
	moduleID                  string
	runtimeAdapterID          string
	runtimeAdapterProviderID  string
	runtimeAdapterModuleID    string
	runtimeAdapterFallbackIDs []string
	contract                  map[string]any
	alternative               map[string]any
	settings                  map[string]any
	secretRefs                map[string]any
	siteRefs                  []string
	nodeRefs                  []string
}

func resolveWorkloadSelections(profile *profileView, spec *specView, catalog *indexedCatalog) (map[string]*resolvedWorkloadSelection, error) {
	allowed := stringSet(append(append(append([]string{}, profile.requiredWorkloads...), profile.defaultWorkloads...), profile.optionalWorkloads...))
	forbidden := stringSet(profile.forbiddenWorkloads)
	rawSelections := make(map[string]any, len(spec.workloads)+len(profile.requiredWorkloads)+len(profile.defaultWorkloads))
	for id, raw := range spec.workloads {
		rawSelections[id] = raw
	}
	for _, id := range append(append([]string{}, profile.requiredWorkloads...), profile.defaultWorkloads...) {
		if _, exists := rawSelections[id]; exists {
			continue
		}
		if !spec.legacyComputeTier {
			return nil, fail(ErrUnknownWorkloadAlternative, "spec.workloads."+id+".alternative", "native v2alpha2 requires an explicit alternative for kit-mandated workload %q", id)
		}
		contract, exists := catalog.workloads[id]
		if !exists {
			return nil, fail(ErrUnknownWorkload, "definition.workloads", "workload %q has no governed catalog contract", id)
		}
		alternativeID, exists, err := optionalStringField(contract, "catalog.workloads."+id, "defaultAlternative")
		if err != nil {
			return nil, err
		}
		if !exists {
			return nil, fail(ErrUnknownWorkloadAlternative, "definition.workloads", "workload %q has no default alternative", id)
		}
		rawSelections[id] = map[string]any{"alternative": alternativeID, "placement": map[string]any{}}
	}

	// Native v2alpha2 selects workload alternatives explicitly and does not
	// reuse the legacy global compute-tier fit table. Legacy v2alpha1 keeps the
	// existing table behind the single compatibility adapter.
	tier := ""
	if spec.legacyComputeTier {
		var err error
		tier, err = computeTierFromInstall(spec.install)
		if err != nil {
			return nil, err
		}
	}
	resolved := make(map[string]*resolvedWorkloadSelection, len(rawSelections))
	moduleOwners := make(map[string]string)
	for _, id := range sortedStringMapKeys(rawSelections) {
		path := "spec.workloads." + id
		if _, denied := forbidden[id]; denied {
			return nil, fail(ErrForbiddenWorkload, path, "workload is forbidden by %s", profile.slug)
		}
		if _, ok := allowed[id]; !ok {
			return nil, fail(ErrUnknownWorkload, path, "workload is not declared by %s", profile.slug)
		}
		contract, exists := catalog.workloads[id]
		if !exists {
			return nil, fail(ErrUnknownWorkload, path, "no governed workload contract exists")
		}
		if spec.legacyComputeTier {
			if fit := CatalogWorkloadComputeTierFit(contract, tier); fit.Declared && !fit.Included {
				return nil, fail(ErrForbiddenWorkload, path, "workload is not included on computeTier %q", tier)
			}
		}
		selection, err := asObject(rawSelections[id], path)
		if err != nil {
			return nil, err
		}
		alternativeID, err := stringField(selection, path, "alternative")
		if err != nil {
			return nil, err
		}
		alternative, err := workloadAlternative(contract, id, alternativeID)
		if err != nil {
			return nil, err
		}
		providerID, err := stringField(alternative, "catalog.workloads."+id+".alternatives."+alternativeID, "providerRef")
		if err != nil {
			return nil, err
		}
		moduleID, err := stringField(alternative, "catalog.workloads."+id+".alternatives."+alternativeID, "moduleRef")
		if err != nil {
			return nil, err
		}
		if err := validateWorkloadImplementation(id, providerID, moduleID, contract, alternative, catalog); err != nil {
			return nil, err
		}
		adapterID, adapterProviderID, adapterModuleID, err := resolveWorkloadRuntimeAdapter(id, selection, alternative, moduleID, catalog)
		if err != nil {
			return nil, err
		}
		fallbackIDs, err := resolveWorkloadRuntimeFallbacks(id, selection, alternative, adapterID)
		if err != nil {
			return nil, err
		}
		if owner, exists := moduleOwners[moduleID]; exists && owner != id {
			return nil, fail(ErrContractConflict, path+".alternative", "module %q is already owned by workload %q", moduleID, owner)
		}
		moduleOwners[moduleID] = id
		settings, secretRefs, err := workloadInputs(selection, alternative, path)
		if err != nil {
			return nil, err
		}
		siteRefs, nodeRefs, err := workloadPlacement(id, selection, contract, spec)
		if err != nil {
			return nil, err
		}
		resolved[id] = &resolvedWorkloadSelection{
			id: id, alternativeID: alternativeID, providerID: providerID, moduleID: moduleID,
			runtimeAdapterID: adapterID, runtimeAdapterProviderID: adapterProviderID, runtimeAdapterModuleID: adapterModuleID,
			runtimeAdapterFallbackIDs: fallbackIDs,
			contract:                  contract, alternative: alternative, settings: settings, secretRefs: secretRefs,
			siteRefs: siteRefs, nodeRefs: nodeRefs,
		}
	}
	return resolved, nil
}

func resolveWorkloadRuntimeFallbacks(workloadID string, selection, alternative map[string]any, primary string) ([]string, error) {
	runtimeContract, err := objectField(alternative, "catalog.workloads."+workloadID+".alternative.runtime", "runtime")
	if err != nil {
		return nil, err
	}
	allowed, err := stringListField(runtimeContract, "catalog.workloads."+workloadID+".alternative.runtime", "allowedAdapterRefs", false)
	if err != nil {
		return nil, err
	}
	_, explicit := selection["runtimeAdapterFallbackRefs"]
	refs, err := stringListField(selection, "spec.workloads."+workloadID, "runtimeAdapterFallbackRefs", false)
	if err != nil {
		return nil, err
	}
	if !explicit {
		refs, err = stringListField(runtimeContract, "catalog.workloads."+workloadID+".alternative.runtime", "defaultFallbackAdapterRefs", false)
		if err != nil {
			return nil, err
		}
	}
	resolved := make([]string, 0, len(refs))
	seen := map[string]bool{}
	for _, ref := range refs {
		if ref == primary {
			if explicit {
				return nil, fail(ErrContractConflict, "spec.workloads."+workloadID+".runtimeAdapterFallbackRefs", "fallback adapter %q duplicates the preferred adapter", ref)
			}
			continue
		}
		if seen[ref] {
			return nil, fail(ErrContractConflict, "spec.workloads."+workloadID+".runtimeAdapterFallbackRefs", "fallback adapter %q is duplicated", ref)
		}
		if !contains(allowed, ref) {
			return nil, fail(ErrContractConflict, "spec.workloads."+workloadID+".runtimeAdapterFallbackRefs", "fallback adapter %q is not allowed by the selected workload alternative", ref)
		}
		seen[ref] = true
		resolved = append(resolved, ref)
	}
	return resolved, nil
}

func resolveWorkloadRuntimeAdapter(workloadID string, selection, alternative map[string]any, workloadModuleID string, catalog *indexedCatalog) (string, string, string, error) {
	path := "spec.workloads." + workloadID + ".runtimeAdapterRef"
	runtimeContract, err := objectField(alternative, "catalog.workloads."+workloadID+".alternative.runtime", "runtime")
	if err != nil {
		return "", "", "", err
	}
	allowed, err := stringListField(runtimeContract, "catalog.workloads."+workloadID+".alternative.runtime", "allowedAdapterRefs", false)
	if err != nil {
		return "", "", "", err
	}
	selected, explicitlySelected, err := optionalStringField(selection, "spec.workloads."+workloadID, "runtimeAdapterRef")
	if err != nil {
		return "", "", "", err
	}
	if len(allowed) == 0 {
		if explicitlySelected {
			return "", "", "", fail(ErrContractConflict, path, "workload alternative does not allow a runtime adapter")
		}
		return "", "", "", nil
	}
	if !explicitlySelected {
		selected, _, err = optionalStringField(runtimeContract, "catalog.workloads."+workloadID+".alternative.runtime", "defaultAdapterRef")
		if err != nil {
			return "", "", "", err
		}
		if selected == "" {
			return "", "", "", fail(ErrContractConflict, path, "workload alternative requires one exact default runtime adapter")
		}
	}
	if !contains(allowed, selected) {
		return "", "", "", fail(ErrContractConflict, path, "runtime adapter %q is not allowed by the selected workload alternative", selected)
	}

	providerID := ""
	for _, candidateID := range sortedStringMapKeys(catalog.providers) {
		candidate := catalog.providers[candidateID]
		refs, fieldErr := stringListField(candidate, "catalog.providers."+candidateID, "runtimeAdapterRefs", false)
		if fieldErr != nil {
			return "", "", "", fieldErr
		}
		if contains(refs, selected) {
			if providerID != "" {
				return "", "", "", fail(ErrContractConflict, path, "runtime adapter %q has multiple catalog owners", selected)
			}
			providerID = candidateID
		}
	}
	if providerID == "" {
		return "", "", "", fail(ErrUnknownProvider, path, "runtime adapter %q has no catalog owner", selected)
	}

	moduleID := ""
	for _, candidateID := range sortedStringMapKeys(catalog.modules) {
		candidate := catalog.modules[candidateID]
		declaredProvider, fieldErr := stringField(candidate, "catalog.modules."+candidateID, "providerRef")
		if fieldErr != nil || declaredProvider != providerID {
			continue
		}
		adapter, exists, fieldErr := optionalObjectField(candidate, "catalog.modules."+candidateID, "runtimeAdapter")
		if fieldErr != nil {
			return "", "", "", fieldErr
		}
		if !exists {
			continue
		}
		adapterID, fieldErr := stringField(adapter, "catalog.modules."+candidateID+".runtimeAdapter", "id")
		if fieldErr != nil {
			return "", "", "", fieldErr
		}
		if adapterID == selected {
			if moduleID != "" {
				return "", "", "", fail(ErrContractConflict, path, "runtime adapter %q has multiple module implementations", selected)
			}
			moduleID = candidateID
		}
	}
	if moduleID == "" {
		return "", "", "", fail(ErrUnknownModule, path, "runtime adapter %q has no governed module", selected)
	}
	if err := validateRuntimeAdapterCompatibility(workloadID, workloadModuleID, selected, moduleID, catalog); err != nil {
		return "", "", "", err
	}
	return selected, providerID, moduleID, nil
}

func validateRuntimeAdapterCompatibility(workloadID, workloadModuleID, adapterID, adapterModuleID string, catalog *indexedCatalog) error {
	workloadRuntime, err := objectField(catalog.modules[workloadModuleID], "catalog.modules."+workloadModuleID, "runtime")
	if err != nil {
		return err
	}
	kind, err := stringField(workloadRuntime, "catalog.modules."+workloadModuleID+".runtime", "kind")
	if err != nil {
		return err
	}
	delivery, err := stringField(workloadRuntime, "catalog.modules."+workloadModuleID+".runtime", "delivery")
	if err != nil {
		return err
	}
	adapter, err := objectField(catalog.modules[adapterModuleID], "catalog.modules."+adapterModuleID, "runtimeAdapter")
	if err != nil {
		return err
	}
	kinds, err := stringListField(adapter, "catalog.modules."+adapterModuleID+".runtimeAdapter", "supportedKinds", true)
	if err != nil {
		return err
	}
	deliveries, err := stringListField(adapter, "catalog.modules."+adapterModuleID+".runtimeAdapter", "supportedDeliveries", true)
	if err != nil {
		return err
	}
	if !contains(kinds, kind) || !contains(deliveries, delivery) {
		return fail(ErrContractConflict, "spec.workloads."+workloadID+".runtimeAdapterRef", "runtime adapter %q does not support %s/%s", adapterID, kind, delivery)
	}
	return nil
}

func workloadAlternative(contract map[string]any, workloadID, alternativeID string) (map[string]any, error) {
	alternatives, err := objectListField(contract, "catalog.workloads."+workloadID, "alternatives")
	if err != nil {
		return nil, err
	}
	for _, alternative := range alternatives {
		id, err := stringField(alternative, "catalog.workloads."+workloadID+".alternatives", "id")
		if err != nil {
			return nil, err
		}
		if id == alternativeID {
			return alternative, nil
		}
	}
	return nil, fail(ErrUnknownWorkloadAlternative, "spec.workloads."+workloadID+".alternative", "alternative %q is not governed", alternativeID)
}

func validateWorkloadImplementation(workloadID, providerID, moduleID string, workloadContract, alternative map[string]any, catalog *indexedCatalog) error {
	provider, exists := catalog.providers[providerID]
	if !exists {
		return fail(ErrUnknownProvider, "catalog.workloads."+workloadID, "alternative references unknown provider %q", providerID)
	}
	workloadRefs, err := stringListField(provider, "catalog.providers."+providerID, "workloadRefs", false)
	if err != nil {
		return err
	}
	if !contains(workloadRefs, workloadID) {
		return fail(ErrContractConflict, "catalog.providers."+providerID+".workloadRefs", "provider does not own workload %q", workloadID)
	}
	module, exists := catalog.modules[moduleID]
	if !exists {
		return fail(ErrUnknownModule, "catalog.workloads."+workloadID, "alternative references unknown module %q", moduleID)
	}
	role, err := stringField(module, "catalog.modules."+moduleID, "role")
	if err != nil {
		return err
	}
	if role != "workload" {
		return fail(ErrContractConflict, "catalog.modules."+moduleID+".role", "workload alternative requires role workload")
	}
	declaredProvider, err := stringField(module, "catalog.modules."+moduleID, "providerRef")
	if err != nil {
		return err
	}
	if declaredProvider != providerID {
		return fail(ErrContractConflict, "catalog.modules."+moduleID+".providerRef", "module belongs to %q, want %q", declaredProvider, providerID)
	}
	kind, err := stringField(workloadContract, "catalog.workloads."+workloadID, "kind")
	if err != nil {
		return err
	}
	if kind != "application" {
		return nil
	}
	return validateWorkloadInfrastructure(workloadID, moduleID, alternative, catalog)
}

func validateWorkloadInfrastructure(workloadID, workloadModuleID string, alternative map[string]any, catalog *indexedCatalog) error {
	path := "catalog.workloads." + workloadID + ".alternative.infrastructure"
	infrastructure, exists, err := optionalObjectField(alternative, "catalog.workloads."+workloadID+".alternative", "infrastructure")
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	storage, err := objectField(infrastructure, path, "storageAllocation")
	if err != nil {
		return err
	}
	dataBinding, err := objectField(infrastructure, path, "dataBinding")
	if err != nil {
		return err
	}
	storageModuleID, err := stringField(storage, path+".storageAllocation", "moduleRef")
	if err != nil {
		return err
	}
	dataBindingModuleID, err := stringField(dataBinding, path+".dataBinding", "moduleRef")
	if err != nil {
		return err
	}
	if storageModuleID == dataBindingModuleID {
		return fail(ErrContractConflict, path, "storage allocation and data binding require distinct shared module identities")
	}
	storageModule := catalog.modules[storageModuleID]
	if storageModule == nil {
		return fail(ErrUnknownModule, path+".storageAllocation.moduleRef", "shared storage allocation module %q is not governed", storageModuleID)
	}
	if _, exists, fieldErr := optionalObjectField(storageModule, "catalog.modules."+storageModuleID, "storageAllocationContract"); fieldErr != nil {
		return fieldErr
	} else if !exists {
		return fail(ErrContractConflict, path+".storageAllocation.moduleRef", "module %q does not implement the storage allocation contract", storageModuleID)
	}
	dataBindingModule := catalog.modules[dataBindingModuleID]
	if dataBindingModule == nil {
		return fail(ErrUnknownModule, path+".dataBinding.moduleRef", "shared data binding module %q is not governed", dataBindingModuleID)
	}
	dataBindingContract, exists, err := optionalObjectField(dataBindingModule, "catalog.modules."+dataBindingModuleID, "dataBindingContract")
	if err != nil {
		return err
	}
	if !exists {
		return fail(ErrContractConflict, path+".dataBinding.moduleRef", "module %q does not implement the workload data binding contract", dataBindingModuleID)
	}
	requiredStorageModuleID, err := stringField(dataBindingContract, "catalog.modules."+dataBindingModuleID+".dataBindingContract", "storageAllocationModuleRef")
	if err != nil {
		return err
	}
	if requiredStorageModuleID != storageModuleID {
		return fail(ErrContractConflict, path+".dataBinding.moduleRef", "data binding module is not bound to storage allocation module %q", storageModuleID)
	}
	storageProviderID, err := stringField(storageModule, "catalog.modules."+storageModuleID, "providerRef")
	if err != nil {
		return err
	}
	dataBindingProviderID, err := stringField(dataBindingModule, "catalog.modules."+dataBindingModuleID, "providerRef")
	if err != nil {
		return err
	}
	if storageProviderID != dataBindingProviderID {
		return fail(ErrContractConflict, path, "shared infrastructure modules belong to different providers")
	}
	infrastructureProvider := catalog.providers[storageProviderID]
	if infrastructureProvider == nil {
		return fail(ErrUnknownProvider, path, "shared infrastructure provider %q is not governed", storageProviderID)
	}
	provides, err := stringListField(infrastructureProvider, "catalog.providers."+storageProviderID, "provides", true)
	if err != nil {
		return err
	}
	if !contains(provides, "storage-data-policy") {
		return fail(ErrContractConflict, path, "shared infrastructure provider does not own storage-data-policy")
	}
	workloadProviderID, err := stringField(alternative, "catalog.workloads."+workloadID+".alternative", "providerRef")
	if err != nil {
		return err
	}
	workloadProvider := catalog.providers[workloadProviderID]
	workloadRequirements, err := requirements(workloadProvider, "catalog.providers."+workloadProviderID)
	if err != nil {
		return err
	}
	requiresStorageDataPolicy := false
	requiresBackupCore := false
	for _, requirement := range workloadRequirements {
		if requirement.id == "storage-data-policy" && !requirement.optional {
			requiresStorageDataPolicy = true
		}
		if requirement.id == "backup-core" && !requirement.optional {
			requiresBackupCore = true
		}
	}
	if !requiresStorageDataPolicy {
		return fail(ErrContractConflict, path, "Application Kit provider must require storage-data-policy")
	}
	if !requiresBackupCore {
		return fail(ErrContractConflict, path, "Application Kit provider must require backup-core")
	}
	if err := validateWorkloadBackupInfrastructure(infrastructure, storage, storageModuleID, dataBindingModuleID, catalog, path); err != nil {
		return err
	}
	return validateWorkloadInfrastructureRuntimeProjection(workloadID, workloadModuleID, infrastructure, catalog)
}

func validateWorkloadBackupInfrastructure(infrastructure, storage map[string]any, storageModuleID, dataBindingModuleID string, catalog *indexedCatalog, path string) error {
	type backupModuleBinding struct {
		field, contractField, dependencyField, dependencyID string
	}
	bindings := []backupModuleBinding{
		{field: "backupSource", contractField: "backupSourceContract"},
		{field: "snapshot", contractField: "snapshotContract", dependencyField: "backupSourceModuleRef"},
		{field: "restore", contractField: "restoreContract", dependencyField: "snapshotModuleRef"},
		{field: "recovery", contractField: "recoveryContract", dependencyField: "restoreModuleRef"},
	}
	moduleIDs := map[string]struct{}{storageModuleID: {}, dataBindingModuleID: {}}
	backupProviderID := ""
	previousModuleID := ""
	for index := range bindings {
		binding := &bindings[index]
		value, err := objectField(infrastructure, path, binding.field)
		if err != nil {
			return err
		}
		moduleID, err := stringField(value, path+"."+binding.field, "moduleRef")
		if err != nil {
			return err
		}
		if _, duplicate := moduleIDs[moduleID]; duplicate {
			return fail(ErrContractConflict, path+"."+binding.field+".moduleRef", "shared infrastructure module identities must be distinct")
		}
		moduleIDs[moduleID] = struct{}{}
		module := catalog.modules[moduleID]
		if module == nil {
			return fail(ErrUnknownModule, path+"."+binding.field+".moduleRef", "shared %s module %q is not governed", binding.field, moduleID)
		}
		contract, exists, err := optionalObjectField(module, "catalog.modules."+moduleID, binding.contractField)
		if err != nil {
			return err
		}
		if !exists {
			return fail(ErrContractConflict, path+"."+binding.field+".moduleRef", "module %q does not implement %s", moduleID, binding.contractField)
		}
		planOnly, err := boolFieldDefault(module, "catalog.modules."+moduleID, "planOnly", false)
		if err != nil {
			return err
		}
		if !planOnly {
			return fail(ErrContractConflict, path+"."+binding.field+".moduleRef", "shared backup lifecycle modules must be plan-only")
		}
		providerID, err := stringField(module, "catalog.modules."+moduleID, "providerRef")
		if err != nil {
			return err
		}
		if backupProviderID == "" {
			backupProviderID = providerID
		} else if providerID != backupProviderID {
			return fail(ErrContractConflict, path, "shared backup lifecycle modules belong to different providers")
		}
		if binding.dependencyField != "" {
			binding.dependencyID = previousModuleID
			requiredModuleID, err := stringField(contract, "catalog.modules."+moduleID+"."+binding.contractField, binding.dependencyField)
			if err != nil {
				return err
			}
			if requiredModuleID != binding.dependencyID {
				return fail(ErrContractConflict, path+"."+binding.field+".moduleRef", "%s module is not bound to %q", binding.field, binding.dependencyID)
			}
		}
		requires, err := stringListField(module, "catalog.modules."+moduleID, "requires", false)
		if err != nil {
			return err
		}
		if index == 0 {
			if !contains(requires, storageModuleID) || !contains(requires, dataBindingModuleID) {
				return fail(ErrContractConflict, path+".backupSource.moduleRef", "backup source module must require the selected storage allocation and data binding modules")
			}
			if err := validateBackupSourceContract(contract, storageModuleID, dataBindingModuleID, moduleID); err != nil {
				return err
			}
		} else if !contains(requires, previousModuleID) {
			return fail(ErrContractConflict, path+"."+binding.field+".moduleRef", "%s module must require %q", binding.field, previousModuleID)
		}
		previousModuleID = moduleID
	}
	provider := catalog.providers[backupProviderID]
	if provider == nil {
		return fail(ErrUnknownProvider, path, "shared backup lifecycle provider %q is not governed", backupProviderID)
	}
	provides, err := stringListField(provider, "catalog.providers."+backupProviderID, "provides", true)
	if err != nil {
		return err
	}
	if !contains(provides, "backup-core") {
		return fail(ErrContractConflict, path, "shared backup lifecycle provider does not own backup-core")
	}
	return validateWorkloadBackupSources(infrastructure, storage, path)
}

func validateBackupSourceContract(contract map[string]any, storageModuleID, dataBindingModuleID, moduleID string) error {
	path := "catalog.modules." + moduleID + ".backupSourceContract"
	storageRef, err := stringField(contract, path, "storageAllocationModuleRef")
	if err != nil {
		return err
	}
	dataBindingRef, err := stringField(contract, path, "workloadDataBindingModuleRef")
	if err != nil {
		return err
	}
	if storageRef != storageModuleID || dataBindingRef != dataBindingModuleID {
		return fail(ErrContractConflict, path, "backup source contract is not bound to the selected storage allocation and data binding modules")
	}
	return nil
}

func validateWorkloadBackupSources(infrastructure, storage map[string]any, path string) error {
	allocations, err := objectListField(storage, path+".storageAllocation", "allocations")
	if err != nil {
		return err
	}
	backed := make(map[string][]string)
	for index, allocation := range allocations {
		allocationPath := fmt.Sprintf("%s.storageAllocation.allocations[%d]", path, index)
		backup, err := boolFieldDefault(allocation, allocationPath, "backup", false)
		if err != nil {
			return err
		}
		if !backup {
			continue
		}
		componentRef, err := stringField(allocation, allocationPath, "componentRef")
		if err != nil {
			return err
		}
		volumeRef, err := stringField(allocation, allocationPath, "volumeRef")
		if err != nil {
			return err
		}
		classes, err := stringListField(allocation, allocationPath, "dataClasses", true)
		if err != nil {
			return err
		}
		backed[componentRef+"/"+volumeRef] = classes
	}
	backupSource, err := objectField(infrastructure, path, "backupSource")
	if err != nil {
		return err
	}
	sources, err := objectListField(backupSource, path+".backupSource", "allocations")
	if err != nil {
		return err
	}
	if len(sources) != len(backed) {
		return fail(ErrContractConflict, path+".backupSource.allocations", "backup sources do not exactly cover backup-enabled storage allocations")
	}
	for index, source := range sources {
		sourcePath := fmt.Sprintf("%s.backupSource.allocations[%d]", path, index)
		componentRef, err := stringField(source, sourcePath, "componentRef")
		if err != nil {
			return err
		}
		volumeRef, err := stringField(source, sourcePath, "volumeRef")
		if err != nil {
			return err
		}
		classes, err := stringListField(source, sourcePath, "dataClasses", true)
		if err != nil {
			return err
		}
		want, exists := backed[componentRef+"/"+volumeRef]
		if !exists || !equalStringSets(classes, want) {
			return fail(ErrContractConflict, sourcePath, "backup source is not an exact backup-enabled storage allocation")
		}
		delete(backed, componentRef+"/"+volumeRef)
	}
	if len(backed) != 0 {
		return fail(ErrContractConflict, path+".backupSource.allocations", "backup-enabled storage allocations are missing from the backup source contract")
	}
	return nil
}

func validateWorkloadInfrastructureRuntimeProjection(workloadID, workloadModuleID string, infrastructure map[string]any, catalog *indexedCatalog) error {
	path := "catalog.workloads." + workloadID + ".alternative.infrastructure"
	dataBinding, err := objectField(infrastructure, path, "dataBinding")
	if err != nil {
		return err
	}
	bindingRef, err := stringField(dataBinding, path+".dataBinding", "bindingRef")
	if err != nil {
		return err
	}
	classes, err := stringListField(dataBinding, path+".dataBinding", "classes", true)
	if err != nil {
		return err
	}
	locality, err := stringField(dataBinding, path+".dataBinding", "locality")
	if err != nil {
		return err
	}
	module := catalog.modules[workloadModuleID]
	units, err := objectListField(module, "catalog.modules."+workloadModuleID, "renderUnits")
	if err != nil {
		return err
	}
	matches := 0
	for _, unit := range units {
		endpoints, err := objectListOptional(unit, "serviceEndpoints")
		if err != nil {
			return err
		}
		for _, endpoint := range endpoints {
			data, exists, err := optionalObjectField(endpoint, "catalog.modules."+workloadModuleID+".serviceEndpoints", "data")
			if err != nil {
				return err
			}
			if !exists {
				continue
			}
			endpointBindingRef, err := stringField(data, "catalog.modules."+workloadModuleID+".serviceEndpoints.data", "bindingRef")
			if err != nil {
				return err
			}
			endpointClasses, err := stringListField(data, "catalog.modules."+workloadModuleID+".serviceEndpoints.data", "requiredClasses", true)
			if err != nil {
				return err
			}
			endpointLocality, err := stringField(data, "catalog.modules."+workloadModuleID+".serviceEndpoints.data", "locality")
			if err != nil {
				return err
			}
			if endpointBindingRef == bindingRef && endpointLocality == locality && equalStringSets(endpointClasses, classes) {
				matches++
			}
		}
	}
	if matches != 1 {
		return fail(ErrContractConflict, path+".dataBinding", "requires exactly one workload endpoint bound to the shared data contract, got %d", matches)
	}
	return nil
}

func equalStringSets(left, right []string) bool {
	left = sortStringsUnique(left)
	right = sortStringsUnique(right)
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func workloadInputs(selection, alternative map[string]any, path string) (map[string]any, map[string]any, error) {
	settings, _, err := optionalObjectField(selection, path, "settings")
	if err != nil {
		return nil, nil, err
	}
	secretRefs, _, err := optionalObjectField(selection, path, "secretRefs")
	if err != nil {
		return nil, nil, err
	}
	inputs, err := objectField(alternative, path+".alternative", "inputs")
	if err != nil {
		return nil, nil, err
	}
	settingsContract, err := objectField(inputs, path+".alternative.inputs", "settings")
	if err != nil {
		return nil, nil, err
	}
	secretContract, err := objectField(inputs, path+".alternative.inputs", "secretInputs")
	if err != nil {
		return nil, nil, err
	}
	if err := validateWorkloadInputMap(settings, settingsContract, path+".settings"); err != nil {
		return nil, nil, err
	}
	if err := validateWorkloadInputMap(secretRefs, secretContract, path+".secretRefs"); err != nil {
		return nil, nil, err
	}
	return settings, secretRefs, nil
}

func validateWorkloadInputMap(values, contract map[string]any, path string) error {
	allowed, err := stringListField(contract, path, "allowedRefs", false)
	if err != nil {
		return err
	}
	required, err := stringListField(contract, path, "requiredRefs", false)
	if err != nil {
		return err
	}
	for key := range values {
		if !contains(allowed, key) {
			return fail(ErrInvalidInput, path+"."+key, "input is not declared by the selected workload alternative")
		}
	}
	for _, key := range required {
		if _, exists := values[key]; !exists {
			return fail(ErrInvalidInput, path+"."+key, "required workload input is missing")
		}
	}
	return nil
}

func workloadPlacement(workloadID string, selection, contract map[string]any, spec *specView) ([]string, []string, error) {
	path := "spec.workloads." + workloadID + ".placement"
	placement, _, err := optionalObjectField(selection, "spec.workloads."+workloadID, "placement")
	if err != nil {
		return nil, nil, err
	}
	if placement == nil {
		placement = map[string]any{}
	}
	siteFilter, err := stringListField(placement, path, "siteRefs", false)
	if err != nil {
		return nil, nil, err
	}
	nodeFilter, err := stringListField(placement, path, "nodeRefs", false)
	if err != nil {
		return nil, nil, err
	}
	requiredRoles, err := stringListField(placement, path, "requiresRoles", false)
	if err != nil {
		return nil, nil, err
	}
	supportedKinds, err := stringListField(contract, "catalog.workloads."+workloadID, "supportedSiteKinds", true)
	if err != nil {
		return nil, nil, err
	}
	var sites, nodes []string
	for _, node := range spec.nodes {
		if !node.enabled || !contains(supportedKinds, node.siteKind) || (len(siteFilter) > 0 && !contains(siteFilter, node.siteRef)) || (len(nodeFilter) > 0 && !contains(nodeFilter, node.id)) {
			continue
		}
		eligible := true
		for _, role := range requiredRoles {
			if !contains(node.roles, role) {
				eligible = false
				break
			}
		}
		if eligible {
			sites = append(sites, node.siteRef)
			nodes = append(nodes, node.id)
		}
	}
	if len(nodes) == 0 {
		return nil, nil, fail(ErrUnresolvedPlacement, path, "no enabled node satisfies the governed workload placement")
	}
	sort.Strings(nodes)
	return sortStringsUnique(sites), nodes, nil
}
