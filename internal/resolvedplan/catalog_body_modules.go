package resolvedplan

import (
	"fmt"
	"reflect"
)

//nolint:gocyclo // Module-body validation exhaustively binds placement, inputs, render units, and selected capabilities.
func validateResolvedModuleBodies(plan ResolvedPlan, catalog *indexedCatalog, capabilityProviders, workloadProviders, runtimeAdapterProviders, workloadModules, runtimeAdapterModules map[string]string, workloadPlacements map[string]resolvedWorkloadPlacement) error {
	values, err := objectListField(map[string]any(plan), "resolvedPlan", "modules")
	if err != nil {
		return err
	}
	actual := make(map[string]map[string]any, len(values))
	for index, value := range values {
		path := fmt.Sprintf("resolvedPlan.modules[%d]", index)
		id, err := stringField(value, path, "id")
		if err != nil {
			return err
		}
		if _, duplicate := actual[id]; duplicate {
			return fmt.Errorf("%s duplicates module %q", path, id)
		}
		actual[id] = value
	}

	selectedProviders := make(map[string]struct{})
	for _, providerRef := range capabilityProviders {
		selectedProviders[providerRef] = struct{}{}
	}
	for _, providerRef := range workloadProviders {
		selectedProviders[providerRef] = struct{}{}
	}
	for _, providerRef := range runtimeAdapterProviders {
		selectedProviders[providerRef] = struct{}{}
	}
	requiredModules := make(map[string]struct{})
	allowedModules := make(map[string]struct{})
	for providerID := range selectedProviders {
		realization, err := objectField(catalog.providers[providerID], "catalog.providers."+providerID, "realization")
		if err != nil {
			return err
		}
		moduleRefs, err := objectField(realization, "catalog.providers."+providerID+".realization", "moduleRefs")
		if err != nil {
			return err
		}
		for _, field := range []string{"required", "optional"} {
			ids, err := stringListField(moduleRefs, "catalog.providers."+providerID+".realization.moduleRefs", field, false)
			if err != nil {
				return err
			}
			for _, id := range ids {
				allowedModules[id] = struct{}{}
				if field == "required" {
					requiredModules[id] = struct{}{}
				}
			}
		}
	}
	for id := range requiredModules {
		if _, exists := actual[id]; !exists {
			return fmt.Errorf("resolvedPlan.modules omits required bound module %q", id)
		}
	}
	for moduleID := range workloadModules {
		if _, exists := actual[moduleID]; !exists {
			return fmt.Errorf("resolvedPlan.modules omits workload-selected module %q", moduleID)
		}
	}
	for moduleID := range runtimeAdapterModules {
		if _, exists := actual[moduleID]; !exists {
			return fmt.Errorf("resolvedPlan.modules omits runtime-adapter-selected module %q", moduleID)
		}
	}
	for id := range actual {
		if _, allowed := allowedModules[id]; !allowed {
			return fmt.Errorf("resolvedPlan.modules.%s is not selected by a bound provider module contract", id)
		}
	}

	nodeSites, nodeKinds, enabledNodes, err := resolvedTopologyIndex(plan)
	if err != nil {
		return err
	}
	moduleTargetSpec, err := resolvedModuleTargetSpec(plan)
	if err != nil {
		return err
	}
	compatibility, err := objectField(map[string]any(plan), "resolvedPlan", "compatibility")
	if err != nil {
		return err
	}
	profileSource := "catalog"
	if compatibility["specAPIVersion"] == "stackkit/v2alpha1" {
		profileSource = "legacy-adapter"
	}
	for _, id := range mapKeys(actual) {
		module := actual[id]
		contract := catalog.modules[id]
		if contract == nil {
			return fmt.Errorf("resolvedPlan.modules.%s has no bound catalog body", id)
		}
		path := "resolvedPlan.modules." + id
		providerRef, err := stringField(contract, "catalog.modules."+id, "providerRef")
		if err != nil {
			return err
		}
		haveProviderRef, err := stringField(module, path, "providerRef")
		if err != nil {
			return err
		}
		if haveProviderRef != providerRef {
			return fmt.Errorf("%s.providerRef does not match the bound module body", path)
		}
		if err := requireCatalogField(module, contract, path, "role"); err != nil {
			return err
		}
		if err := requireCatalogOptionalField(module, contract, path, "planOnly"); err != nil {
			return err
		}
		if err := requireCatalogObjectField(module, contract, path, "runtime"); err != nil {
			return err
		}
		if err := validateResolvedServiceControls(module, contract, id, path); err != nil {
			return err
		}
		if err := validateResolvedModuleSupportProjection(module, contract, path); err != nil {
			return err
		}
		for _, field := range []string{
			"nodeSelection", "enforcementRequirement", "runtimeOwnerRequirement",
			"runtimeAdapter", "runtimeAdapterAgent", "storageAllocationContract", "dataBindingContract",
			"backupSourceContract", "snapshotContract", "restoreContract", "recoveryContract",
		} {
			if err := requireCatalogOptionalObjectField(module, contract, path, field); err != nil {
				return err
			}
		}
		if err := validateResolvedModuleProfileBindings(module, contract, path, profileSource); err != nil {
			return err
		}
		wantRequires, err := stringListField(contract, "catalog.modules."+id, "requires", false)
		if err != nil {
			return err
		}
		haveRequires, err := stringListField(module, path, "requires", false)
		if err != nil {
			return err
		}
		haveRequiresUnique := sortStringsUnique(haveRequires)
		if len(haveRequires) != len(haveRequiresUnique) || !reflect.DeepEqual(haveRequiresUnique, sortStringsUnique(wantRequires)) {
			return fmt.Errorf("%s.requires does not match the bound module body", path)
		}
		wantProvides, err := selectedModuleCapabilities(contract, providerRef, capabilityProviders)
		if err != nil {
			return err
		}
		haveProvides, err := stringListField(module, path, "provides", true)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(haveProvides, wantProvides) {
			return fmt.Errorf("%s.provides is not the exact selected capability projection", path)
		}
		providerKinds, err := stringListField(catalog.providers[providerRef], "catalog.providers."+providerRef, "supportedSiteKinds", true)
		if err != nil {
			return err
		}
		providerNodes := map[string][]string{providerRef: eligibleNodesForKinds(enabledNodes, nodeKinds, providerKinds)}
		runtimeRequirements, _, err := optionalObjectField(module, path, "runtimeRequirements")
		if err != nil {
			return err
		}
		targetContract := moduleContractWithRuntimeRequirements(contract, runtimeRequirements)
		wantSites, wantNodes, err := resolveModuleTargetsWithInventoryAttestation(id, providerRef, targetContract, moduleTargetSpec, providerNodes, false)
		if err != nil {
			return fmt.Errorf("%s placement cannot be reconstructed from its bound contract: %w", path, err)
		}
		if placement, isWorkloadModule := workloadPlacements[id]; isWorkloadModule {
			wantSites, wantNodes = placement.siteRefs, placement.nodeRefs
		}
		if id == "stackkits-bridge-origin-mtls-runtime" {
			wantSites, wantNodes, err = resolvedBridgeOriginMTLSTargets(plan, nodeSites)
			if err != nil {
				return fmt.Errorf("%s placement cannot be reconstructed from its resolved publication origins: %w", path, err)
			}
		}
		if _, isRuntimeAdapterModule := runtimeAdapterModules[id]; isRuntimeAdapterModule {
			wantSites, wantNodes, err = resolvedRuntimeAdapterModuleTargets(plan, id, nodeSites)
			if err != nil {
				return fmt.Errorf("%s placement cannot be reconstructed from its resolved workload adapter bindings: %w", path, err)
			}
		}
		haveNodes, err := stringListField(module, path, "nodeRefs", true)
		if err != nil {
			return err
		}
		haveSites, err := stringListField(module, path, "siteRefs", true)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(haveNodes, wantNodes) || !reflect.DeepEqual(haveSites, wantSites) {
			return fmt.Errorf("%s site/node placement is not the exact bound module projection", path)
		}
		if err := validateResolvedRenderUnitBodies(module, contract, path); err != nil {
			return err
		}
		if err := validateResolvedModuleInputProjection(module, contract, path); err != nil {
			return err
		}
		if err := validateResolvedModuleSecretInputProjection(plan, module, path); err != nil {
			return err
		}
		if err := validateResolvedModulePlanInputProjection(plan, module, path); err != nil {
			return err
		}
	}
	return nil
}

func validateResolvedServiceControls(module, contract map[string]any, moduleID, path string) error {
	have, err := objectListOptional(module, "serviceControls")
	if err != nil {
		return err
	}
	wantValues, err := resolveModuleServiceControls(moduleID, contract)
	if err != nil {
		return err
	}
	want := make([]map[string]any, len(wantValues))
	for index, value := range wantValues {
		want[index], err = asObject(value, fmt.Sprintf("catalog.modules.%s.serviceControls[%d]", moduleID, index))
		if err != nil {
			return err
		}
	}
	haveByKey, err := indexServiceControlBodies(have, path+".serviceControls")
	if err != nil {
		return err
	}
	wantByKey, err := indexServiceControlBodies(want, "catalog.modules."+moduleID+".serviceControls")
	if err != nil {
		return err
	}
	equal, err := canonicalEqual(haveByKey, wantByKey)
	if err != nil {
		return err
	}
	if !equal {
		return fmt.Errorf("%s.serviceControls does not match the bound catalog body", path)
	}
	return nil
}

func indexServiceControlBodies(values []map[string]any, path string) (map[string]map[string]any, error) {
	result := make(map[string]map[string]any, len(values))
	for index, value := range values {
		key, err := stringField(value, fmt.Sprintf("%s[%d]", path, index), "key")
		if err != nil {
			return nil, err
		}
		if _, duplicate := result[key]; duplicate {
			return nil, fmt.Errorf("%s duplicates key %q", path, key)
		}
		result[key] = value
	}
	return result, nil
}

func validateResolvedModuleSupportProjection(module, contract map[string]any, path string) error {
	moduleID, err := stringField(module, path, "id")
	if err != nil {
		return err
	}
	target, err := stringField(module, path, "renderTarget")
	if err != nil {
		return err
	}
	rawUnits, err := objectListField(module, path, "renderUnits")
	if err != nil {
		return err
	}
	units := make([]map[string]any, len(rawUnits))
	selectedUnitIDs := make(map[string]struct{}, len(rawUnits))
	for index, rawUnit := range rawUnits {
		unit, err := asObject(rawUnit, fmt.Sprintf("%s.renderUnits[%d]", path, index))
		if err != nil {
			return err
		}
		units[index] = unit
		unitID, err := stringField(unit, fmt.Sprintf("%s.renderUnits[%d]", path, index), "id")
		if err != nil {
			return err
		}
		selectedUnitIDs[unitID] = struct{}{}
	}
	_, declared, err := indexModuleRenderUnits(moduleID, units)
	if err != nil {
		return err
	}
	resolvedVariant, hasResolvedVariant, err := optionalObjectField(module, path, "renderVariant")
	if err != nil {
		return err
	}
	contractUnits, err := objectListField(contract, "catalog.modules."+moduleID, "renderUnits")
	if err != nil {
		return err
	}
	_, _, wantVariant, err := selectExplicitModuleRenderVariant(moduleID, contract, contractUnits, target)
	if err != nil {
		return err
	}
	if (wantVariant != nil) != hasResolvedVariant {
		return fmt.Errorf("%s.renderVariant presence does not match the target-selected bound catalog body", path)
	}
	if wantVariant != nil {
		equal, err := canonicalEqual(resolvedVariant, wantVariant)
		if err != nil {
			return err
		}
		if !equal {
			return fmt.Errorf("%s.renderVariant does not match the target-selected bound catalog body", path)
		}
	}
	contractSupport, err := objectField(contract, "catalog.modules."+moduleID, "realizationSupport")
	if err != nil {
		return err
	}
	wantSupport, err := cloneObject(contractSupport, true)
	if err != nil {
		return err
	}
	if err := selectModuleRealizationSupport(moduleID, wantSupport, selectedUnitIDs, declared, wantVariant, target); err != nil {
		return err
	}
	haveSupport, err := objectField(module, path, "realizationSupport")
	if err != nil {
		return err
	}
	equal, err := canonicalEqual(haveSupport, wantSupport)
	if err != nil {
		return err
	}
	if !equal {
		return fmt.Errorf("%s.realizationSupport does not match the target-selected bound catalog body", path)
	}
	return nil
}

func resolvedBridgeOriginMTLSTargets(plan ResolvedPlan, nodeSites map[string]string) ([]string, []string, error) {
	bridge, err := objectField(map[string]any(plan), "resolvedPlan", "bridge")
	if err != nil {
		return nil, nil, err
	}
	publications, err := objectListField(bridge, "resolvedPlan.bridge", "publications")
	if err != nil {
		return nil, nil, err
	}
	nodeSet := map[string]struct{}{}
	siteSet := map[string]struct{}{}
	for index, publication := range publications {
		path := fmt.Sprintf("resolvedPlan.bridge.publications[%d]", index)
		nodeRefs, err := stringListField(publication, path, "originNodeRefs", true)
		if err != nil {
			return nil, nil, err
		}
		sourceSiteRef, err := stringField(publication, path, "sourceSiteRef")
		if err != nil {
			return nil, nil, err
		}
		for _, nodeRef := range nodeRefs {
			if nodeSites[nodeRef] != sourceSiteRef {
				return nil, nil, fmt.Errorf("%s.originNodeRefs contains node %q outside source Site %q", path, nodeRef, sourceSiteRef)
			}
			nodeSet[nodeRef] = struct{}{}
			siteSet[sourceSiteRef] = struct{}{}
		}
	}
	if len(nodeSet) == 0 {
		return nil, nil, fmt.Errorf("resolvedPlan.bridge.publications has no origin nodes")
	}
	return sortedSet(siteSet), sortedSet(nodeSet), nil
}

func resolvedRuntimeAdapterModuleTargets(plan ResolvedPlan, moduleID string, nodeSites map[string]string) ([]string, []string, error) {
	workloads, err := objectListField(map[string]any(plan), "resolvedPlan", "workloads")
	if err != nil {
		return nil, nil, err
	}
	nodeSet := map[string]struct{}{}
	siteSet := map[string]struct{}{}
	for index, workload := range workloads {
		path := fmt.Sprintf("resolvedPlan.workloads[%d]", index)
		alternative, err := objectField(workload, path, "alternative")
		if err != nil {
			return nil, nil, err
		}
		runtime, err := objectField(alternative, path+".alternative", "runtime")
		if err != nil {
			return nil, nil, err
		}
		adapter, exists, err := optionalObjectField(runtime, path+".alternative.runtime", "adapter")
		if err != nil {
			return nil, nil, err
		}
		if !exists {
			continue
		}
		adapterModuleID, err := stringField(adapter, path+".alternative.runtime.adapter", "moduleRef")
		if err != nil {
			return nil, nil, err
		}
		if adapterModuleID != moduleID {
			continue
		}
		nodeRefs, err := stringListField(workload, path, "nodeRefs", true)
		if err != nil {
			return nil, nil, err
		}
		for _, nodeRef := range nodeRefs {
			siteRef, exists := nodeSites[nodeRef]
			if !exists {
				return nil, nil, fmt.Errorf("%s.nodeRefs contains unknown node %q", path, nodeRef)
			}
			nodeSet[nodeRef] = struct{}{}
			siteSet[siteRef] = struct{}{}
		}
	}
	if len(nodeSet) == 0 {
		return nil, nil, fmt.Errorf("runtime adapter module %q has no workload targets", moduleID)
	}
	return sortedSet(siteSet), sortedSet(nodeSet), nil
}

// validateResolvedModuleProfileBindings binds every selected resource profile
// back to the exact catalog body. Profile-derived runtime requirements are the
// only allowed departure from the module's legacy top-level requirements.
func validateResolvedModuleProfileBindings(module, contract map[string]any, path, expectedSource string) error {
	profileID, hasProfile, err := optionalStringField(module, path, "computeProfile")
	if err != nil {
		return err
	}
	binding, hasBinding, err := optionalObjectField(module, path, "computeProfileBinding")
	if err != nil {
		return err
	}
	profileHash, hasHash, err := optionalStringField(module, path, "computeProfileHash")
	if err != nil {
		return err
	}
	source, hasSource, err := optionalStringField(module, path, "computeProfileSource")
	if err != nil {
		return err
	}
	if !hasProfile {
		if _, declaresProfiles := contract["computeProfiles"]; declaresProfiles {
			return fmt.Errorf("%s omits the module's required compute-profile binding", path)
		}
		if hasBinding || hasHash || hasSource {
			return fmt.Errorf("%s has an incomplete compute-profile binding", path)
		}
		if err := requireCatalogOptionalObjectField(module, contract, path, "runtimeRequirements"); err != nil {
			return err
		}
	} else {
		if !hasBinding || !hasHash || !hasSource {
			return fmt.Errorf("%s compute profile %q has no complete body/hash/source binding", path, profileID)
		}
		if source != expectedSource {
			return fmt.Errorf("%s.computeProfileSource does not match the StackSpec compatibility contract", path)
		}
		profiles, declared, err := optionalObjectField(contract, "catalog.modules", "computeProfiles")
		if err != nil {
			return err
		}
		if !declared {
			return fmt.Errorf("%s selects compute profile %q but the bound module has no compute profiles", path, profileID)
		}
		rawProfile, exists := profiles[profileID]
		if !exists {
			return fmt.Errorf("%s selects compute profile %q outside the bound module body", path, profileID)
		}
		expectedProfile, err := asObject(rawProfile, "catalog.modules.computeProfiles."+profileID)
		if err != nil {
			return err
		}
		if expectedSource == "catalog" {
			if err := requireExecutableModuleProfile(expectedProfile, path+".computeProfile", true); err != nil {
				return err
			}
		}
		expectedProfile, err = cloneObject(expectedProfile, true)
		if err != nil {
			return err
		}
		expectedHash, err := moduleProfileHash(path+"."+profileID, expectedProfile)
		if err != nil {
			return err
		}
		expectedProfile["profileHash"] = expectedHash
		if profileHash != expectedHash {
			return fmt.Errorf("%s.computeProfileHash does not match the bound profile body", path)
		}
		if equal, err := canonicalEqual(binding, expectedProfile); err != nil {
			return err
		} else if !equal {
			return fmt.Errorf("%s.computeProfileBinding does not match the bound profile body", path)
		}
		expectedRequirements, err := runtimeRequirementsForProfile(path, contract, expectedProfile)
		if err != nil {
			return err
		}
		actualRequirements, hasRequirements, err := optionalObjectField(module, path, "runtimeRequirements")
		if err != nil {
			return err
		}
		if (expectedRequirements != nil) != hasRequirements {
			return fmt.Errorf("%s.runtimeRequirements presence does not match the bound compute profile", path)
		}
		if hasRequirements {
			if equal, err := canonicalEqual(actualRequirements, expectedRequirements); err != nil {
				return err
			} else if !equal {
				return fmt.Errorf("%s.runtimeRequirements does not match the bound compute profile", path)
			}
		}
	}
	for _, axis := range []struct {
		idField, hashField, bindingField, contractField string
	}{
		{"storageProfile", "storageProfileHash", "storageProfileBinding", "storageProfiles"},
		{"acceleratorProfile", "acceleratorProfileHash", "acceleratorProfileBinding", "acceleratorProfiles"},
	} {
		selectedID, selected, err := optionalStringField(module, path, axis.idField)
		if err != nil {
			return err
		}
		hash, hasAxisHash, err := optionalStringField(module, path, axis.hashField)
		if err != nil {
			return err
		}
		axisBinding, hasAxisBinding, err := optionalObjectField(module, path, axis.bindingField)
		if err != nil {
			return err
		}
		if !selected {
			if _, declared := contract[axis.contractField]; declared && expectedSource == "catalog" {
				return fmt.Errorf("%s omits the module's required %s binding", path, axis.idField)
			}
			if hasAxisHash || hasAxisBinding {
				return fmt.Errorf("%s has an incomplete %s binding", path, axis.idField)
			}
			continue
		}
		if !hasAxisHash || !hasAxisBinding {
			return fmt.Errorf("%s %s %q has no complete body/hash binding", path, axis.idField, selectedID)
		}
		profiles, declared, err := optionalObjectField(contract, "catalog.modules", axis.contractField)
		if err != nil {
			return err
		}
		if !declared {
			return fmt.Errorf("%s selects %s %q but the bound module has no %s", path, axis.idField, selectedID, axis.contractField)
		}
		rawProfile, exists := profiles[selectedID]
		if !exists {
			return fmt.Errorf("%s selects %s %q outside the bound module body", path, axis.idField, selectedID)
		}
		expectedProfile, err := asObject(rawProfile, "catalog.modules."+axis.contractField+"."+selectedID)
		if err != nil {
			return err
		}
		if expectedSource == "catalog" {
			if err := requireExecutableModuleProfile(expectedProfile, path+"."+axis.idField, false); err != nil {
				return err
			}
		}
		expectedProfile, err = cloneObject(expectedProfile, true)
		if err != nil {
			return err
		}
		expectedHash, err := moduleProfileHash(path+"."+axis.contractField+"."+selectedID, expectedProfile)
		if err != nil {
			return err
		}
		expectedProfile["profileHash"] = expectedHash
		if hash != expectedHash {
			return fmt.Errorf("%s.%s does not match the bound profile body", path, axis.hashField)
		}
		if equal, err := canonicalEqual(axisBinding, expectedProfile); err != nil {
			return err
		} else if !equal {
			return fmt.Errorf("%s.%s does not match the bound profile body", path, axis.bindingField)
		}
	}
	return nil
}

func validateResolvedModuleCoverage(plan ResolvedPlan, catalog *indexedCatalog, capabilityProviders map[string]string) error {
	moduleValues, err := objectListField(map[string]any(plan), "resolvedPlan", "modules")
	if err != nil {
		return err
	}
	for _, capabilityID := range mapKeys(capabilityProviders) {
		providerID := capabilityProviders[capabilityID]
		realization, err := objectField(catalog.providers[providerID], "catalog.providers."+providerID, "realization")
		if err != nil {
			return err
		}
		kind, err := stringField(realization, "catalog.providers."+providerID+".realization", "kind")
		if err != nil {
			return err
		}
		switch kind {
		case "host", "external":
			ownerRef, err := stringField(realization, "catalog.providers."+providerID+".realization", "ownerRef")
			if err != nil {
				return err
			}
			if ownerRef != providerID {
				return fmt.Errorf("resolved capability %q selects provider %q without its exact owner contract", capabilityID, providerID)
			}
			continue
		case "contract":
			continue
		case "topology":
			if err := validateTopologyProviderRealization(providerID, catalog.providers[providerID], realization); err != nil {
				return err
			}
			continue
		case "none":
			return fmt.Errorf("resolved capability %q selects provider %q without an approved realization", capabilityID, providerID)
		case "modules":
		default:
			return fmt.Errorf("catalog.providers.%s.realization.kind %q is unsupported", providerID, kind)
		}

		covered := false
		for index, module := range moduleValues {
			path := fmt.Sprintf("resolvedPlan.modules[%d]", index)
			moduleProvider, err := stringField(module, path, "providerRef")
			if err != nil {
				return err
			}
			if moduleProvider != providerID {
				continue
			}
			provides, err := stringListField(module, path, "provides", true)
			if err != nil {
				return err
			}
			if contains(provides, capabilityID) {
				covered = true
				break
			}
		}
		if !covered {
			return fmt.Errorf("resolved capability %q has no selected bound module covering provider %q", capabilityID, providerID)
		}
	}
	return nil
}

//nolint:gocyclo // Input projection intentionally checks presence, classification, defaults, and exact catalog equality together.
func validateResolvedModuleInputProjection(module, contract map[string]any, path string) error {
	defaults, hasDefaults, err := optionalObjectField(contract, "catalog.modules", "inputDefaults")
	if err != nil {
		return err
	}
	if !hasDefaults {
		defaults = map[string]any{}
	}
	units, err := objectListField(module, path, "renderUnits")
	if err != nil {
		return err
	}
	type resolvedUnitInputs struct {
		id         string
		publicRefs []string
		secretRefs []string
		values     map[string]any
		secrets    map[string]any
	}
	resolvedUnits := make([]resolvedUnitInputs, 0, len(units))
	publicValues := map[string]any{}
	secretValues := map[string]any{}
	publicPresent := map[string]bool{}
	for index, unit := range units {
		unitPath := fmt.Sprintf("%s.renderUnits[%d]", path, index)
		id, err := stringField(unit, unitPath, "id")
		if err != nil {
			return err
		}
		publicRefs, err := stringListField(unit, unitPath, "publicInputRefs", false)
		if err != nil {
			return err
		}
		secretRefs, err := stringListField(unit, unitPath, "secretInputRefs", false)
		if err != nil {
			return err
		}
		values, err := objectField(unit, unitPath, "values")
		if err != nil {
			return err
		}
		secrets, err := objectField(unit, unitPath, "secretRefs")
		if err != nil {
			return err
		}
		resolvedUnits = append(resolvedUnits, resolvedUnitInputs{id: id, publicRefs: publicRefs, secretRefs: secretRefs, values: values, secrets: secrets})
		for key, value := range values {
			publicPresent[key] = true
			if first, exists := publicValues[key]; exists {
				equal, err := canonicalEqual(first, value)
				if err != nil {
					return err
				}
				if !equal {
					return fmt.Errorf("%s public input %q resolves differently across render units", path, key)
				}
			} else {
				publicValues[key] = value
			}
		}
		for key, value := range secrets {
			if first, exists := secretValues[key]; exists {
				equal, err := canonicalEqual(first, value)
				if err != nil {
					return err
				}
				if !equal {
					return fmt.Errorf("%s secret input %q resolves differently across render units", path, key)
				}
			} else {
				secretValues[key] = value
			}
		}
	}
	for _, unit := range resolvedUnits {
		for _, inputRef := range unit.publicRefs {
			_, defaulted := defaults[inputRef]
			if !defaulted && !publicPresent[inputRef] {
				continue
			}
			if _, exists := unit.values[inputRef]; !exists {
				return fmt.Errorf("%s.renderUnits.%s.values omits module-level public input %q", path, unit.id, inputRef)
			}
		}
		for _, inputRef := range unit.secretRefs {
			if _, exists := unit.secrets[inputRef]; !exists {
				return fmt.Errorf("%s.renderUnits.%s.secretRefs omits required module-level secret input %q", path, unit.id, inputRef)
			}
		}
	}
	return nil
}

func validateResolvedRenderUnitBodies(module, contract map[string]any, path string) error {
	haveUnits, err := objectListField(module, path, "renderUnits")
	if err != nil {
		return err
	}
	allWantUnits, err := objectListField(contract, "catalog.modules", "renderUnits")
	if err != nil {
		return err
	}
	moduleID, err := stringField(module, path, "id")
	if err != nil {
		return err
	}
	target, err := stringField(module, path, "renderTarget")
	if err != nil {
		return err
	}
	wantUnits, _, _, err := selectExplicitModuleRenderVariant(moduleID, contract, allWantUnits, target)
	if err != nil {
		return err
	}
	haveByID, err := indexObjectsByID(haveUnits, path+".renderUnits")
	if err != nil {
		return err
	}
	wantByID, err := indexObjectsByID(wantUnits, "catalog.modules.renderUnits")
	if err != nil {
		return err
	}
	if !sameStringSet(mapKeys(haveByID), mapKeys(wantByID)) {
		return fmt.Errorf("%s.renderUnits is not the exact bound render-unit set", path)
	}
	for _, id := range mapKeys(haveByID) {
		have := haveByID[id]
		want := wantByID[id]
		unitPath := path + ".renderUnits." + id
		for _, field := range []string{"id", "kind", "rendererRef", "applyMode", "templateRef", "version", "contractHash"} {
			if !reflect.DeepEqual(have[field], want[field]) {
				return fmt.Errorf("%s.%s does not match the bound render-unit body", unitPath, field)
			}
		}
		for _, field := range []string{"publicInputRefs", "secretInputRefs", "planInputRefs", "outputs"} {
			haveList, err := stringListField(have, unitPath, field, false)
			if err != nil {
				return err
			}
			wantList, err := stringListField(want, "catalog.modules.renderUnits."+id, field, false)
			if err != nil {
				return err
			}
			if !reflect.DeepEqual(sortStringsUnique(haveList), sortStringsUnique(wantList)) {
				return fmt.Errorf("%s.%s does not match the bound render-unit body", unitPath, field)
			}
		}
		if equal, err := canonicalEqual(
			map[string]any{"inputBindings": have["inputBindings"], "secretInputBindings": have["secretInputBindings"]},
			map[string]any{"inputBindings": want["inputBindings"], "secretInputBindings": want["secretInputBindings"]},
		); err != nil {
			return err
		} else if !equal {
			return fmt.Errorf("%s input bindings do not match the bound render-unit body", unitPath)
		}
		if err := requireCatalogObjectField(have, want, unitPath, "placement"); err != nil {
			return err
		}
		if err := validateResolvedServiceEndpointBodies(have, want, unitPath); err != nil {
			return err
		}
		if err := validateResolvedRuntimeListenerBodies(have, want, unitPath); err != nil {
			return err
		}
		if err := validateResolvedProvidedInterfaceBodies(have, want, unitPath); err != nil {
			return err
		}
		if err := validateResolvedRequirementBodies(have, want, unitPath); err != nil {
			return err
		}
	}
	return nil
}

func validateResolvedModuleSecretInputProjection(plan ResolvedPlan, module map[string]any, path string) error {
	moduleID, err := stringField(module, path, "id")
	if err != nil {
		return err
	}
	providerRef, err := stringField(module, path, "providerRef")
	if err != nil {
		return err
	}
	rawCapabilities, err := objectListField(map[string]any(plan), "resolvedPlan", "capabilities")
	if err != nil {
		return err
	}
	capabilities, err := indexObjectsByID(rawCapabilities, "resolvedPlan.capabilities")
	if err != nil {
		return err
	}
	units, err := objectListField(module, path, "renderUnits")
	if err != nil {
		return err
	}
	for index, unit := range units {
		unitPath := fmt.Sprintf("%s.renderUnits[%d]", path, index)
		unitID, err := stringField(unit, unitPath, "id")
		if err != nil {
			return err
		}
		unitPath = path + ".renderUnits." + unitID
		bindings, err := moduleRenderSecretInputBindings(unit, unitPath)
		if err != nil {
			return err
		}
		secrets, err := objectField(unit, unitPath, "secretRefs")
		if err != nil {
			return err
		}
		for targetRef, binding := range bindings {
			capability, exists := capabilities[binding.capabilityRef]
			if !exists {
				return fmt.Errorf("%s.secretInputBindings.%s references absent capability %q", unitPath, targetRef, binding.capabilityRef)
			}
			capabilityProvider, err := stringField(capability, "resolvedPlan.capabilities."+binding.capabilityRef, "providerRef")
			if err != nil {
				return err
			}
			if capabilityProvider != providerRef {
				return fmt.Errorf("%s.secretInputBindings.%s capability is not owned by module provider %q", unitPath, targetRef, providerRef)
			}
			capabilitySecrets, exists, err := optionalObjectField(capability, "resolvedPlan.capabilities."+binding.capabilityRef, "secretRefs")
			if err != nil || !exists {
				return fmt.Errorf("%s.secretInputBindings.%s capability secret source is unavailable", unitPath, targetRef)
			}
			sourceSecret, sourceExists := capabilitySecrets[binding.key]
			targetSecret, targetExists := secrets[targetRef]
			if !sourceExists || !targetExists {
				return fmt.Errorf("%s.secretInputBindings.%s does not resolve an exact source and target", unitPath, targetRef)
			}
			equal, err := canonicalEqual(sourceSecret, targetSecret)
			if err != nil {
				return err
			}
			if !equal {
				return fmt.Errorf("%s.secretRefs.%s is not the exact capability secret projection for module %q", unitPath, targetRef, moduleID)
			}
		}
	}
	return nil
}

// validateResolvedModulePlanInputProjection reconstructs every compiler-owned
// plan input from the persisted plan. Re-hashing a plan after changing,
// omitting, or widening planInputs therefore cannot manufacture authority.
//
//nolint:gocyclo // Persisted-plan rebound validation exhaustively compares every governed module input projection.
func validateResolvedModulePlanInputProjection(plan ResolvedPlan, module map[string]any, path string) error {
	source, err := modulePlanInputSourceFromResolvedPlan(plan)
	if err != nil {
		return err
	}
	moduleID, err := stringField(module, path, "id")
	if err != nil {
		return err
	}
	units, err := objectListField(module, path, "renderUnits")
	if err != nil {
		return err
	}
	for index, unit := range units {
		unitPath := fmt.Sprintf("%s.renderUnits[%d]", path, index)
		unitID, err := stringField(unit, unitPath, "id")
		if err != nil {
			return err
		}
		unitPath = path + ".renderUnits." + unitID
		refs, err := stringListField(unit, unitPath, "planInputRefs", false)
		if err != nil {
			return err
		}
		if normalized := sortStringsUnique(refs); len(normalized) != len(refs) {
			return fmt.Errorf("%s.planInputRefs contains duplicate or empty refs", unitPath)
		}
		inputs, err := objectField(unit, unitPath, "planInputs")
		if err != nil {
			return err
		}
		if !sameStringSet(mapKeys(inputs), refs) {
			return fmt.Errorf("%s.planInputs is not the exact 1:1 planInputRefs projection", unitPath)
		}
		for _, ref := range refs {
			want, err := source.resolve(ref, moduleID, module)
			if err != nil {
				return fmt.Errorf("recompute %s.planInputs.%s: %w", unitPath, ref, err)
			}
			equal, err := canonicalEqual(inputs[ref], want)
			if err != nil {
				return err
			}
			if !equal {
				return fmt.Errorf("%s.planInputs.%s is not the exact compiler-derived projection", unitPath, ref)
			}
		}
		bindings, err := moduleRenderInputBindings(unit, unitPath)
		if err != nil {
			return err
		}
		if len(bindings) == 0 {
			continue
		}
		values, err := objectField(unit, unitPath, "values")
		if err != nil {
			return err
		}
		identity, err := objectField(map[string]any(plan), "resolvedPlan", "identity")
		if err != nil {
			return err
		}
		identityTrust, err := objectField(map[string]any(plan), "resolvedPlan", "identityTrust")
		if err != nil {
			return err
		}
		failurePolicy, err := objectField(map[string]any(plan), "resolvedPlan", "failurePolicy")
		if err != nil {
			return err
		}
		controlPlane, err := objectField(map[string]any(plan), "resolvedPlan", "controlPlane")
		if err != nil {
			return err
		}
		data, err := objectField(map[string]any(plan), "resolvedPlan", "data")
		if err != nil {
			return err
		}
		sites, err := objectListField(map[string]any(plan), "resolvedPlan", "sites")
		if err != nil {
			return err
		}
		nodes, err := objectListField(map[string]any(plan), "resolvedPlan", "nodes")
		if err != nil {
			return err
		}
		stackID, err := stringField(map[string]any(plan), "resolvedPlan", "stackId")
		if err != nil {
			return err
		}
		network, err := objectField(map[string]any(plan), "resolvedPlan", "network")
		if err != nil {
			return err
		}
		gates, err := objectField(map[string]any(plan), "resolvedPlan", "gates")
		if err != nil {
			return err
		}
		install, err := objectField(map[string]any(plan), "resolvedPlan", "install")
		if err != nil {
			return err
		}
		system, err := objectField(map[string]any(plan), "resolvedPlan", "system")
		if err != nil {
			return err
		}
		storage, err := objectField(map[string]any(plan), "resolvedPlan", "storage")
		if err != nil {
			return err
		}
		kit, err := objectField(map[string]any(plan), "resolvedPlan", "kit")
		if err != nil {
			return err
		}
		workloads, err := objectListField(map[string]any(plan), "resolvedPlan", "workloads")
		if err != nil {
			return err
		}
		modules, err := objectListField(map[string]any(plan), "resolvedPlan", "modules")
		if err != nil {
			return err
		}
		bindingSource := moduleRenderInputSource{
			stackID: stackID, kit: kit, sites: objectMapsAsAny(sites),
			identity: identity, identityTrust: identityTrust, controlPlane: controlPlane, data: data,
			failurePolicy: failurePolicy, network: network, gates: gates,
			install: install, system: system, storage: storage, nodes: objectMapsAsAny(nodes), workloads: objectMapsAsAny(workloads), modules: objectMapsAsAny(modules),
		}
		target := moduleRenderInputTarget{}
		for _, binding := range bindings {
			if binding.sourceRef != moduleInputSourceLocalKopiaBackup {
				continue
			}
			target, err = localKopiaRenderInputTarget(unit, unitPath)
			if err != nil {
				return err
			}
			break
		}
		for _, binding := range bindings {
			want, available, err := bindingSource.resolve(binding, moduleID, target)
			if err != nil {
				return fmt.Errorf("recompute %s.values.%s: %w", unitPath, binding.targetRef, err)
			}
			if !available {
				if binding.required {
					return fmt.Errorf("recompute %s.values.%s: required source %s is unavailable", unitPath, binding.targetRef, binding.sourceRef)
				}
				want = binding.defaultValue
			}
			have, exists := values[binding.targetRef]
			if !exists {
				return fmt.Errorf("%s.values.%s omits compiler-bound public input", unitPath, binding.targetRef)
			}
			equal, err := canonicalEqual(have, want)
			if err != nil {
				return err
			}
			if !equal {
				return fmt.Errorf("%s.values.%s is not the exact compiler-derived input binding", unitPath, binding.targetRef)
			}
		}
	}
	return nil
}

func validateResolvedServiceEndpointBodies(haveUnit, wantUnit map[string]any, path string) error {
	return validateResolvedIndexedBodies(
		haveUnit,
		wantUnit,
		path,
		"serviceEndpoints",
		"service endpoint",
		indexServiceEndpointBodies,
	)
}

func validateResolvedRuntimeListenerBodies(haveUnit, wantUnit map[string]any, path string) error {
	return validateResolvedIndexedBodies(
		haveUnit,
		wantUnit,
		path,
		"runtimeListeners",
		"runtime listener",
		indexObjectsByID,
	)
}

func indexServiceEndpointBodies(values []map[string]any, path string) (map[string]map[string]any, error) {
	result := make(map[string]map[string]any, len(values))
	for index, value := range values {
		serviceRef, err := stringField(value, fmt.Sprintf("%s[%d]", path, index), "serviceRef")
		if err != nil {
			return nil, err
		}
		if _, duplicate := result[serviceRef]; duplicate {
			return nil, fmt.Errorf("%s duplicates serviceRef %q", path, serviceRef)
		}
		result[serviceRef] = value
	}
	return result, nil
}

func validateResolvedProvidedInterfaceBodies(haveUnit, wantUnit map[string]any, path string) error {
	return validateResolvedIndexedBodies(
		haveUnit,
		wantUnit,
		path,
		"providesInterfaces",
		"provider-interface",
		indexObjectsByID,
	)
}

type resolvedBodyIndexer func([]map[string]any, string) (map[string]map[string]any, error)

func validateResolvedIndexedBodies(
	haveUnit, wantUnit map[string]any,
	path, field, bodyKind string,
	index resolvedBodyIndexer,
) error {
	have, err := objectListOptional(haveUnit, field)
	if err != nil {
		return err
	}
	want, err := objectListOptional(wantUnit, field)
	if err != nil {
		return err
	}
	haveByKey, err := index(have, path+"."+field)
	if err != nil {
		return err
	}
	wantByKey, err := index(want, "catalog.modules.renderUnits."+field)
	if err != nil {
		return err
	}
	if !sameStringSet(mapKeys(haveByKey), mapKeys(wantByKey)) {
		return fmt.Errorf("%s.%s is not the exact bound %s set", path, field, bodyKind)
	}
	for _, key := range mapKeys(haveByKey) {
		if equal, err := canonicalEqual(haveByKey[key], wantByKey[key]); err != nil {
			return err
		} else if !equal {
			return fmt.Errorf("%s.%s.%s does not match the bound %s body", path, field, key, bodyKind)
		}
	}
	return nil
}

func validateResolvedRequirementBodies(haveUnit, wantUnit map[string]any, path string) error {
	have, err := objectListOptional(haveUnit, "requiresInterfaces")
	if err != nil {
		return err
	}
	want, err := objectListOptional(wantUnit, "requiresInterfaces")
	if err != nil {
		return err
	}
	haveByID, err := indexObjectsByID(have, path+".requiresInterfaces")
	if err != nil {
		return err
	}
	wantByID, err := indexObjectsByID(want, "catalog.modules.renderUnits.requiresInterfaces")
	if err != nil {
		return err
	}
	if !sameStringSet(mapKeys(haveByID), mapKeys(wantByID)) {
		return fmt.Errorf("%s.requiresInterfaces is not the exact bound requirement set", path)
	}
	for _, id := range mapKeys(haveByID) {
		projection, err := cloneObject(haveByID[id], false)
		if err != nil {
			return err
		}
		delete(projection, "providerBindings")
		if equal, err := canonicalEqual(projection, wantByID[id]); err != nil {
			return err
		} else if !equal {
			return fmt.Errorf("%s.requiresInterfaces.%s does not match the bound requirement body", path, id)
		}
	}
	return nil
}

//nolint:gocyclo // Gate-body validation is the exhaustive fail-closed boundary for provider, module, health, and lifecycle gates.
func validateResolvedGateBodies(plan ResolvedPlan, catalog *indexedCatalog, capabilityProviders map[string]string) error {
	providerValues, err := objectListField(map[string]any(plan), "resolvedPlan", "providers")
	if err != nil {
		return err
	}
	providers, err := indexObjectsByID(providerValues, "resolvedPlan.providers")
	if err != nil {
		return err
	}
	moduleValues, err := objectListField(map[string]any(plan), "resolvedPlan", "modules")
	if err != nil {
		return err
	}
	modules, err := indexObjectsByID(moduleValues, "resolvedPlan.modules")
	if err != nil {
		return err
	}
	nodeSites, nodeKinds, enabledNodes, err := resolvedTopologyIndex(plan)
	if err != nil {
		return err
	}
	providerNodes := make(map[string][]string, len(providers))
	providerSites := make(map[string][]string, len(providers))
	haProviderRef, _, haProviderNodes, err := resolvedHAProviderPlacement(plan)
	if err != nil {
		return err
	}
	for id, provider := range providers {
		supportedKinds, err := stringListField(catalog.providers[id], "catalog.providers."+id, "supportedSiteKinds", true)
		if err != nil {
			return err
		}
		providerNodes[id] = eligibleNodesForKinds(enabledNodes, nodeKinds, supportedKinds)
		if id == haProviderRef {
			providerNodes[id] = haProviderNodes
		}
		providerSites[id], err = stringListField(provider, "resolvedPlan.providers."+id, "siteRefs", true)
		if err != nil {
			return err
		}
	}

	expectedHealth := map[string]map[string]any{}
	appendHealth := func(targetKind, targetRef string, contract map[string]any, siteRefs, nodeRefs []string) error {
		gates, err := materializeContractHealthGates(targetKind, targetRef, contract, siteRefs, nodeRefs, nodeSites)
		if err != nil {
			return err
		}
		for _, gate := range gates {
			gateID := gate["id"].(string)
			if _, duplicate := expectedHealth[gateID]; duplicate {
				return fmt.Errorf("bound catalog projects duplicate health gate %q", gateID)
			}
			expectedHealth[gateID] = gate
		}
		return nil
	}
	for _, capabilityID := range mapKeys(capabilityProviders) {
		providerID := capabilityProviders[capabilityID]
		if err := appendHealth("capability", capabilityID, catalog.capabilities[capabilityID], providerSites[providerID], providerNodes[providerID]); err != nil {
			return err
		}
	}
	for _, providerID := range mapKeys(providers) {
		if err := appendHealth("provider", providerID, catalog.providers[providerID], providerSites[providerID], providerNodes[providerID]); err != nil {
			return err
		}
	}
	for _, moduleID := range mapKeys(modules) {
		siteRefs, err := stringListField(modules[moduleID], "resolvedPlan.modules."+moduleID, "siteRefs", true)
		if err != nil {
			return err
		}
		nodeRefs, err := stringListField(modules[moduleID], "resolvedPlan.modules."+moduleID, "nodeRefs", true)
		if err != nil {
			return err
		}
		if err := appendHealth("module", moduleID, catalog.modules[moduleID], siteRefs, nodeRefs); err != nil {
			return err
		}
	}
	serviceEndpoints, err := indexResolvedServiceEndpoints(objectMapsAsAny(moduleValues))
	if err != nil {
		return err
	}
	if err := bindServiceEndpointHealthContracts(serviceEndpoints, catalog.modules); err != nil {
		return err
	}
	network, err := objectField(map[string]any(plan), "resolvedPlan", "network")
	if err != nil {
		return err
	}
	routes, err := objectListField(network, "resolvedPlan.network", "routes")
	if err != nil {
		return err
	}
	backendPools, err := objectListField(network, "resolvedPlan.network", "backendPools")
	if err != nil {
		return err
	}
	poolsByID, err := indexObjectsByID(backendPools, "resolvedPlan.network.backendPools")
	if err != nil {
		return err
	}
	for routeIndex, route := range routes {
		routePath := fmt.Sprintf("resolvedPlan.network.routes[%d]", routeIndex)
		routeID, err := stringField(route, routePath, "id")
		if err != nil {
			return err
		}
		moduleRef, err := stringField(route, routePath, "moduleRef")
		if err != nil {
			return err
		}
		serviceRef, err := stringField(route, routePath, "serviceRef")
		if err != nil {
			return err
		}
		endpoint, exists := serviceEndpoints[moduleRef][serviceRef]
		if !exists {
			return fmt.Errorf("%s has no bound service endpoint", routePath)
		}
		poolRef, err := stringField(route, routePath, "backendPoolRef")
		if err != nil {
			return err
		}
		backendPool, exists := poolsByID[poolRef]
		if !exists {
			return fmt.Errorf("%s.backendPoolRef %q has no bound pool", routePath, poolRef)
		}
		gate, err := buildRouteHealthGate(routeID, backendPool, endpoint)
		if err != nil {
			return err
		}
		gateID := gate["id"].(string)
		if _, duplicate := expectedHealth[gateID]; duplicate {
			return fmt.Errorf("bound catalog projects duplicate route health gate %q", gateID)
		}
		expectedHealth[gateID] = gate
	}

	gates, err := objectField(map[string]any(plan), "resolvedPlan", "gates")
	if err != nil {
		return err
	}
	healthValues, err := objectListField(gates, "resolvedPlan.gates", "health")
	if err != nil {
		return err
	}
	actualHealth, err := indexObjectsByID(healthValues, "resolvedPlan.gates.health")
	if err != nil {
		return err
	}
	if !sameStringSet(mapKeys(actualHealth), mapKeys(expectedHealth)) {
		return fmt.Errorf("resolvedPlan.gates.health is not the exact selected catalog health projection")
	}
	for _, gateID := range mapKeys(actualHealth) {
		if equal, err := canonicalEqual(actualHealth[gateID], expectedHealth[gateID]); err != nil {
			return err
		} else if !equal {
			return fmt.Errorf("resolvedPlan.gates.health.%s does not match its bound catalog health body", gateID)
		}
	}

	evidenceValues, err := objectListField(gates, "resolvedPlan.gates", "evidence")
	if err != nil {
		return err
	}
	actualEvidence, err := indexObjectsByID(evidenceValues, "resolvedPlan.gates.evidence")
	if err != nil {
		return err
	}
	var scenarios []string
	for _, gate := range evidenceValues {
		scenario, err := stringField(gate, "resolvedPlan.gates.evidence", "scenario")
		if err != nil {
			return err
		}
		scenarios = append(scenarios, scenario)
	}
	scenarios = sortStringsUnique(scenarios)
	healthIDs := mapKeys(expectedHealth)
	artifacts, err := objectField(map[string]any(plan), "resolvedPlan", "generation")
	if err != nil {
		return err
	}
	artifactValues, err := objectListField(artifacts, "resolvedPlan.generation", "artifacts")
	if err != nil {
		return err
	}
	var artifactIDs []string
	for index, artifact := range artifactValues {
		id, err := stringField(artifact, fmt.Sprintf("resolvedPlan.generation.artifacts[%d]", index), "id")
		if err != nil {
			return err
		}
		artifactIDs = append(artifactIDs, id)
	}
	artifactIDs = sortStringsUnique(artifactIDs)
	expectedEvidence := make(map[string]map[string]any, len(scenarios))
	for index, scenario := range scenarios {
		id := fmt.Sprintf("evidence-%03d", index+1)
		expectedEvidence[id] = map[string]any{
			"id": id, "scenario": scenario, "phase": "verify", "producer": "evidence-runner", "required": true,
			"healthGateRefs": stringSliceAny(healthIDs), "artifactRefs": stringSliceAny(artifactIDs),
		}
	}
	if !sameStringSet(mapKeys(actualEvidence), mapKeys(expectedEvidence)) {
		return fmt.Errorf("resolvedPlan.gates.evidence IDs are not the deterministic bound evidence projection")
	}
	for _, gateID := range mapKeys(actualEvidence) {
		if equal, err := canonicalEqual(actualEvidence[gateID], expectedEvidence[gateID]); err != nil {
			return err
		} else if !equal {
			return fmt.Errorf("resolvedPlan.gates.evidence.%s is not the deterministic bound evidence projection", gateID)
		}
	}
	evidenceIDByScenario := make(map[string]string, len(expectedEvidence))
	for gateID, gate := range expectedEvidence {
		evidenceIDByScenario[gate["scenario"].(string)] = gateID
	}
	for _, providerID := range mapKeys(providers) {
		owner, hasOwner, err := optionalObjectField(providers[providerID], "resolvedPlan.providers."+providerID, "owner")
		if err != nil {
			return err
		}
		if !hasOwner {
			continue
		}
		var wantHealthRefs []string
		for gateID, gate := range expectedHealth {
			if gate["targetKind"] == "provider" && gate["targetRef"] == providerID {
				wantHealthRefs = append(wantHealthRefs, gateID)
			}
		}
		wantHealthRefs = sortStringsUnique(wantHealthRefs)
		haveHealthRefs, err := stringListField(owner, "resolvedPlan.providers."+providerID+".owner", "healthGateRefs", true)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(haveHealthRefs, wantHealthRefs) {
			return fmt.Errorf("resolvedPlan.providers.%s.owner.healthGateRefs is not the exact bound provider health projection", providerID)
		}
		providerEvidence, err := stringListField(catalog.providers[providerID], "catalog.providers."+providerID, "evidence", true)
		if err != nil {
			return err
		}
		wantEvidenceRefs := make([]string, 0, len(providerEvidence))
		for _, scenario := range providerEvidence {
			gateID, exists := evidenceIDByScenario[scenario]
			if !exists {
				return fmt.Errorf("bound provider %q evidence scenario %q has no deterministic evidence gate", providerID, scenario)
			}
			wantEvidenceRefs = append(wantEvidenceRefs, gateID)
		}
		wantEvidenceRefs = sortStringsUnique(wantEvidenceRefs)
		haveEvidenceRefs, err := stringListField(owner, "resolvedPlan.providers."+providerID+".owner", "evidenceGateRefs", true)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(haveEvidenceRefs, wantEvidenceRefs) {
			return fmt.Errorf("resolvedPlan.providers.%s.owner.evidenceGateRefs is not the exact bound provider evidence projection", providerID)
		}
	}
	wantApply := map[string]any{
		"requireFreshPlanHash": true, "requireCompatibleCLI": true, "requireCompatibleRuntime": true,
		"requireGenerationArtifacts": true, "requireResolvedSecrets": true,
	}
	haveApply, err := objectField(gates, "resolvedPlan.gates", "apply")
	if err != nil {
		return err
	}
	if equal, err := canonicalEqual(haveApply, wantApply); err != nil {
		return err
	} else if !equal {
		return fmt.Errorf("resolvedPlan.gates.apply does not match the bound compiler gate contract")
	}
	return nil
}
