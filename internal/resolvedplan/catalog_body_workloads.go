package resolvedplan

import (
	"fmt"
)

//nolint:gocyclo // Provider-body validation keeps every resolved provider field bound to its catalog authority.
type resolvedWorkloadPlacement struct {
	siteRefs []string
	nodeRefs []string
}

func validateResolvedWorkloadBodies(plan ResolvedPlan, catalog *indexedCatalog) (map[string]string, map[string]string, map[string]resolvedWorkloadPlacement, error) {
	values, err := objectListField(map[string]any(plan), "resolvedPlan", "workloads")
	if err != nil {
		return nil, nil, nil, err
	}
	providers := make(map[string]string, len(values))
	modules := make(map[string]string, len(values))
	placements := make(map[string]resolvedWorkloadPlacement, len(values))
	nodeSites, nodeKinds, enabledNodes, err := resolvedTopologyIndex(plan)
	if err != nil {
		return nil, nil, nil, err
	}
	resolvedModules, err := objectListField(map[string]any(plan), "resolvedPlan", "modules")
	if err != nil {
		return nil, nil, nil, err
	}
	modulesByID, err := indexObjectsByID(resolvedModules, "resolvedPlan.modules")
	if err != nil {
		return nil, nil, nil, err
	}
	for index, value := range values {
		path := fmt.Sprintf("resolvedPlan.workloads[%d]", index)
		id, err := stringField(value, path, "id")
		if err != nil {
			return nil, nil, nil, err
		}
		contract := catalog.workloads[id]
		if contract == nil {
			return nil, nil, nil, fmt.Errorf("%s id %q has no bound workload body", path, id)
		}
		version, err := metadataVersion(contract, "catalog.workloads."+id)
		if err != nil {
			return nil, nil, nil, err
		}
		haveVersion, err := stringField(value, path, "version")
		if err != nil || haveVersion != version {
			return nil, nil, nil, fmt.Errorf("%s.version does not match the bound workload body", path)
		}
		wantHash, err := canonicalHash(contract, true)
		if err != nil {
			return nil, nil, nil, err
		}
		haveHash, err := stringField(value, path, "contractHash")
		if err != nil || haveHash != wantHash {
			return nil, nil, nil, fmt.Errorf("%s.contractHash does not match the bound workload body", path)
		}
		for _, field := range []string{"kind", "functionalCapabilities", "dataClasses"} {
			if err := requireCatalogField(value, contract, path, field); err != nil {
				return nil, nil, nil, err
			}
		}
		resolvedAlternative, err := objectField(value, path, "alternative")
		if err != nil {
			return nil, nil, nil, err
		}
		alternativeID, err := stringField(resolvedAlternative, path+".alternative", "id")
		if err != nil {
			return nil, nil, nil, err
		}
		alternative, err := workloadAlternative(contract, id, alternativeID)
		if err != nil {
			return nil, nil, nil, err
		}
		wantAlternativeHash, err := canonicalHash(alternative, true)
		if err != nil {
			return nil, nil, nil, err
		}
		haveAlternativeHash, err := stringField(resolvedAlternative, path+".alternative", "contractHash")
		if err != nil || haveAlternativeHash != wantAlternativeHash {
			return nil, nil, nil, fmt.Errorf("%s.alternative.contractHash does not match the bound alternative body", path)
		}
		for _, field := range []string{"providerRef", "moduleRef"} {
			if err := requireCatalogField(resolvedAlternative, alternative, path+".alternative", field); err != nil {
				return nil, nil, nil, err
			}
		}
		for _, field := range []string{"route", "setup"} {
			if err := requireCatalogObjectField(resolvedAlternative, alternative, path+".alternative", field); err != nil {
				return nil, nil, nil, err
			}
		}
		_, hasInfrastructure, err := optionalObjectField(alternative, path+".alternative", "infrastructure")
		if err != nil {
			return nil, nil, nil, err
		}
		if hasInfrastructure {
			if err := validateResolvedWorkloadInfrastructure(resolvedAlternative, alternative, modulesByID, path+".alternative"); err != nil {
				return nil, nil, nil, err
			}
		}
		if err := validateResolvedWorkloadRuntimeAdapter(resolvedAlternative, alternative, catalog, path+".alternative"); err != nil {
			return nil, nil, nil, err
		}
		inputs, err := objectField(alternative, path+".alternative", "inputs")
		if err != nil {
			return nil, nil, nil, err
		}
		settingsContract, err := objectField(inputs, path+".alternative.inputs", "settings")
		if err != nil {
			return nil, nil, nil, err
		}
		secretContract, err := objectField(inputs, path+".alternative.inputs", "secretInputs")
		if err != nil {
			return nil, nil, nil, err
		}
		settings, _, err := optionalObjectField(value, path, "settings")
		if err != nil {
			return nil, nil, nil, err
		}
		secretRefs, _, err := optionalObjectField(value, path, "secretRefs")
		if err != nil {
			return nil, nil, nil, err
		}
		if err := validateWorkloadInputMap(settings, settingsContract, path+".settings"); err != nil {
			return nil, nil, nil, err
		}
		if err := validateWorkloadInputMap(secretRefs, secretContract, path+".secretRefs"); err != nil {
			return nil, nil, nil, err
		}
		providerID, err := stringField(resolvedAlternative, path+".alternative", "providerRef")
		if err != nil {
			return nil, nil, nil, err
		}
		moduleID, err := stringField(resolvedAlternative, path+".alternative", "moduleRef")
		if err != nil {
			return nil, nil, nil, err
		}
		if owner, duplicate := modules[moduleID]; duplicate && owner != id {
			return nil, nil, nil, fmt.Errorf("%s module %q is already bound to workload %q", path, moduleID, owner)
		}
		siteRefs, err := stringListField(value, path, "siteRefs", true)
		if err != nil {
			return nil, nil, nil, err
		}
		nodeRefs, err := stringListField(value, path, "nodeRefs", true)
		if err != nil {
			return nil, nil, nil, err
		}
		supportedKinds, err := stringListField(contract, "catalog.workloads."+id, "supportedSiteKinds", true)
		if err != nil {
			return nil, nil, nil, err
		}
		for _, nodeRef := range nodeRefs {
			if !contains(enabledNodes, nodeRef) || !contains(siteRefs, nodeSites[nodeRef]) || !contains(supportedKinds, nodeKinds[nodeRef]) {
				return nil, nil, nil, fmt.Errorf("%s.nodeRefs contains node %q outside the bound workload placement envelope", path, nodeRef)
			}
		}
		resolvedModule := modulesByID[moduleID]
		if resolvedModule == nil {
			return nil, nil, nil, fmt.Errorf("%s alternative module %q is absent", path, moduleID)
		}
		moduleContract := catalog.modules[moduleID]
		if moduleContract == nil {
			return nil, nil, nil, fmt.Errorf("%s alternative module %q has no bound module body", path, moduleID)
		}
		if err := validateResolvedWorkloadInputFanout(resolvedModule, moduleContract, moduleID, settings, secretRefs, settingsContract, secretContract, path); err != nil {
			return nil, nil, nil, err
		}
		providers[id], modules[moduleID], placements[moduleID] = providerID, id, resolvedWorkloadPlacement{siteRefs: siteRefs, nodeRefs: nodeRefs}
	}
	return providers, modules, placements, nil
}

func validateResolvedWorkloadInfrastructure(resolvedAlternative, alternative map[string]any, modulesByID map[string]map[string]any, path string) error {
	if err := requireCatalogObjectField(resolvedAlternative, alternative, path, "infrastructure"); err != nil {
		return err
	}
	infrastructure, err := objectField(resolvedAlternative, path, "infrastructure")
	if err != nil {
		return err
	}
	moduleRefs := make([]struct {
		field         string
		contractField string
	}, 0, 2)
	moduleRefs = append(moduleRefs,
		struct {
			field         string
			contractField string
		}{field: "storageAllocation", contractField: "storageAllocationContract"},
		struct {
			field         string
			contractField string
		}{field: "dataBinding", contractField: "dataBindingContract"},
		struct {
			field         string
			contractField string
		}{field: "backupSource", contractField: "backupSourceContract"},
		struct {
			field         string
			contractField string
		}{field: "snapshot", contractField: "snapshotContract"},
		struct {
			field         string
			contractField string
		}{field: "restore", contractField: "restoreContract"},
		struct {
			field         string
			contractField string
		}{field: "recovery", contractField: "recoveryContract"},
	)
	for _, moduleRef := range moduleRefs {
		binding, err := objectField(infrastructure, path+".infrastructure", moduleRef.field)
		if err != nil {
			return err
		}
		moduleID, err := stringField(binding, path+".infrastructure."+moduleRef.field, "moduleRef")
		if err != nil {
			return err
		}
		module := modulesByID[moduleID]
		if module == nil {
			return fmt.Errorf("%s.infrastructure.%s.moduleRef %q is not selected in resolvedPlan.modules", path, moduleRef.field, moduleID)
		}
		if _, exists, err := optionalObjectField(module, "resolvedPlan.modules."+moduleID, moduleRef.contractField); err != nil {
			return err
		} else if !exists {
			return fmt.Errorf("%s.infrastructure.%s.moduleRef %q lacks %s", path, moduleRef.field, moduleID, moduleRef.contractField)
		}
	}
	return nil
}

func validateResolvedWorkloadRuntimeAdapter(resolvedAlternative, alternative map[string]any, catalog *indexedCatalog, path string) error {
	resolvedRuntime, err := objectField(resolvedAlternative, path, "runtime")
	if err != nil {
		return err
	}
	contractRuntime, err := objectField(alternative, path, "runtime")
	if err != nil {
		return err
	}
	allowed, err := stringListField(contractRuntime, path+".runtime", "allowedAdapterRefs", false)
	if err != nil {
		return err
	}
	resolvedAdapter, exists, err := optionalObjectField(resolvedRuntime, path+".runtime", "adapter")
	if err != nil {
		return err
	}
	if len(allowed) == 0 {
		if exists {
			return fmt.Errorf("%s.runtime.adapter is not allowed by the bound workload alternative", path)
		}
		return nil
	}
	if !exists {
		return fmt.Errorf("%s.runtime.adapter is required by the bound workload alternative", path)
	}
	adapterID, err := stringField(resolvedAdapter, path+".runtime.adapter", "id")
	if err != nil || !contains(allowed, adapterID) {
		return fmt.Errorf("%s.runtime.adapter.id is not allowed by the bound workload alternative", path)
	}
	providerID, err := stringField(resolvedAdapter, path+".runtime.adapter", "providerRef")
	if err != nil {
		return err
	}
	provider := catalog.providers[providerID]
	if provider == nil {
		return fmt.Errorf("%s.runtime.adapter.providerRef has no bound provider body", path)
	}
	ownedRefs, err := stringListField(provider, "catalog.providers."+providerID, "runtimeAdapterRefs", false)
	if err != nil || !contains(ownedRefs, adapterID) {
		return fmt.Errorf("%s.runtime.adapter provider does not own adapter %q", path, adapterID)
	}
	providerVersion, err := metadataVersion(provider, "catalog.providers."+providerID)
	if err != nil {
		return err
	}
	providerHash, err := canonicalHash(provider, true)
	if err != nil {
		return err
	}
	if resolvedAdapter["providerVersion"] != providerVersion || resolvedAdapter["providerContractHash"] != providerHash {
		return fmt.Errorf("%s.runtime.adapter provider authority does not match the bound catalog body", path)
	}
	moduleID, err := stringField(resolvedAdapter, path+".runtime.adapter", "moduleRef")
	if err != nil {
		return err
	}
	module := catalog.modules[moduleID]
	if module == nil {
		return fmt.Errorf("%s.runtime.adapter.moduleRef has no bound module body", path)
	}
	declaredProvider, err := stringField(module, "catalog.modules."+moduleID, "providerRef")
	if err != nil || declaredProvider != providerID {
		return fmt.Errorf("%s.runtime.adapter module belongs to another provider", path)
	}
	moduleAdapter, err := objectField(module, "catalog.modules."+moduleID, "runtimeAdapter")
	if err != nil || moduleAdapter["id"] != adapterID {
		return fmt.Errorf("%s.runtime.adapter module does not implement adapter %q", path, adapterID)
	}
	moduleVersion, err := metadataVersion(module, "catalog.modules."+moduleID)
	if err != nil {
		return err
	}
	moduleHash, err := canonicalHash(module, true)
	if err != nil {
		return err
	}
	if resolvedAdapter["moduleVersion"] != moduleVersion || resolvedAdapter["moduleContractHash"] != moduleHash {
		return fmt.Errorf("%s.runtime.adapter module authority does not match the bound catalog body", path)
	}
	return nil
}

func resolvedWorkloadRuntimeAdapterOwners(plan ResolvedPlan) (map[string]string, map[string]string, error) {
	workloads, err := objectListField(map[string]any(plan), "resolvedPlan", "workloads")
	if err != nil {
		return nil, nil, err
	}
	providers := make(map[string]string)
	modules := make(map[string]string)
	for index, workload := range workloads {
		path := fmt.Sprintf("resolvedPlan.workloads[%d].alternative.runtime", index)
		alternative, err := objectField(workload, path, "alternative")
		if err != nil {
			return nil, nil, err
		}
		runtime, err := objectField(alternative, path, "runtime")
		if err != nil {
			return nil, nil, err
		}
		adapter, exists, err := optionalObjectField(runtime, path, "adapter")
		if err != nil {
			return nil, nil, err
		}
		if !exists {
			continue
		}
		adapterID, err := stringField(adapter, path+".adapter", "id")
		if err != nil {
			return nil, nil, err
		}
		providerID, err := stringField(adapter, path+".adapter", "providerRef")
		if err != nil {
			return nil, nil, err
		}
		moduleID, err := stringField(adapter, path+".adapter", "moduleRef")
		if err != nil {
			return nil, nil, err
		}
		if owner, exists := providers[adapterID]; exists && owner != providerID {
			return nil, nil, fmt.Errorf("%s.adapter %q resolves to conflicting providers", path, adapterID)
		}
		if owner, exists := modules[moduleID]; exists && owner != adapterID {
			return nil, nil, fmt.Errorf("%s.adapter module %q resolves to conflicting adapters", path, moduleID)
		}
		providers[adapterID] = providerID
		modules[moduleID] = adapterID
	}
	return providers, modules, nil
}

func validateResolvedWorkloadInputFanout(module, contract map[string]any, moduleID string, settings, secretRefs, settingsContract, secretContract map[string]any, workloadPath string) error {
	defaults, hasDefaults, err := optionalObjectField(contract, "catalog.modules."+moduleID, "inputDefaults")
	if err != nil {
		return err
	}
	if !hasDefaults {
		defaults = map[string]any{}
	}
	allowedSettings, err := stringListField(settingsContract, workloadPath+".alternative.inputs.settings", "allowedRefs", false)
	if err != nil {
		return err
	}
	allowedSecrets, err := stringListField(secretContract, workloadPath+".alternative.inputs.secretInputs", "allowedRefs", false)
	if err != nil {
		return err
	}
	moduleValues := make(map[string]any, len(defaults)+len(settings))
	for key, value := range defaults {
		if !contains(allowedSettings, key) {
			return fmt.Errorf("catalog.modules.%s.inputDefaults.%s is not governed by %s.alternative inputs", moduleID, key, workloadPath)
		}
		moduleValues[key] = value
	}
	for key, value := range settings {
		moduleValues[key] = value
	}
	units, err := objectListField(module, "resolvedPlan.modules."+moduleID, "renderUnits")
	if err != nil {
		return err
	}
	for index, unit := range units {
		unitPath := fmt.Sprintf("resolvedPlan.modules.%s.renderUnits[%d]", moduleID, index)
		publicRefs, err := stringListField(unit, unitPath, "publicInputRefs", false)
		if err != nil {
			return err
		}
		values, _, err := optionalObjectField(unit, unitPath, "values")
		if err != nil {
			return err
		}
		bindings, err := moduleRenderInputBindings(unit, unitPath)
		if err != nil {
			return err
		}
		boundPublicRefs := make(map[string]struct{}, len(bindings))
		for _, binding := range bindings {
			boundPublicRefs[binding.targetRef] = struct{}{}
		}
		expectedValues := map[string]any{}
		for _, key := range publicRefs {
			if _, compilerBound := boundPublicRefs[key]; compilerBound {
				value, exists := values[key]
				if !exists {
					return fmt.Errorf("%s.values.%s omits compiler-bound public input", unitPath, key)
				}
				// Exact compiler reconstruction is enforced by
				// validateResolvedModulePlanInputProjection below. Workload
				// settings must neither duplicate nor override this value.
				expectedValues[key] = value
				continue
			}
			if !contains(allowedSettings, key) {
				return fmt.Errorf("%s.%s is not governed by %s.alternative inputs", unitPath+".publicInputRefs", key, workloadPath)
			}
			if value, exists := moduleValues[key]; exists {
				expectedValues[key] = value
			}
		}
		equal, err := canonicalEqual(values, expectedValues)
		if err != nil {
			return err
		}
		if !equal {
			return fmt.Errorf("%s.values is not the exact workload-governed public input projection", unitPath)
		}
		declaredSecrets, err := stringListField(unit, unitPath, "secretInputRefs", false)
		if err != nil {
			return err
		}
		resolvedSecrets, _, err := optionalObjectField(unit, unitPath, "secretRefs")
		if err != nil {
			return err
		}
		expectedSecrets := map[string]any{}
		for _, key := range declaredSecrets {
			if !contains(allowedSecrets, key) {
				return fmt.Errorf("%s.%s is not governed by %s.alternative inputs", unitPath+".secretInputRefs", key, workloadPath)
			}
			if value, exists := secretRefs[key]; exists {
				expectedSecrets[key] = value
			}
		}
		equal, err = canonicalEqual(resolvedSecrets, expectedSecrets)
		if err != nil {
			return err
		}
		if !equal {
			return fmt.Errorf("%s.secretRefs is not the exact workload-governed secret input projection", unitPath)
		}
	}
	return nil
}
