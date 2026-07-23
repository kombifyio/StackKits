package resolvedplan

import "sort"

type resolvedWorkloadSelection struct {
	id                       string
	alternativeID            string
	providerID               string
	moduleID                 string
	runtimeAdapterID         string
	runtimeAdapterProviderID string
	runtimeAdapterModuleID   string
	contract                 map[string]any
	alternative              map[string]any
	settings                 map[string]any
	secretRefs               map[string]any
	siteRefs                 []string
	nodeRefs                 []string
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
		if err := validateWorkloadImplementation(id, providerID, moduleID, catalog); err != nil {
			return nil, err
		}
		adapterID, adapterProviderID, adapterModuleID, err := resolveWorkloadRuntimeAdapter(id, selection, alternative, moduleID, catalog)
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
			contract: contract, alternative: alternative, settings: settings, secretRefs: secretRefs,
			siteRefs: siteRefs, nodeRefs: nodeRefs,
		}
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

func validateWorkloadImplementation(workloadID, providerID, moduleID string, catalog *indexedCatalog) error {
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
	return nil
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
