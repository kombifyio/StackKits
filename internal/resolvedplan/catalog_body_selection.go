package resolvedplan

import (
	"fmt"
	"reflect"
	"sort"
)

//nolint:gocyclo // Selection-graph validation is the fail-closed cross-product of dependencies, conflicts, providers, and modules.
func validateResolvedSelectionGraph(plan ResolvedPlan, catalog *indexedCatalog, capabilityProviders, workloadProviders, runtimeAdapterProviders map[string]string) error {
	selectedCapabilities := make(map[string]struct{}, len(capabilityProviders))
	selectedProviders := make(map[string]struct{})
	for capabilityID, providerID := range capabilityProviders {
		selectedCapabilities[capabilityID] = struct{}{}
		selectedProviders[providerID] = struct{}{}
	}
	for _, providerID := range workloadProviders {
		selectedProviders[providerID] = struct{}{}
	}
	for _, providerID := range runtimeAdapterProviders {
		selectedProviders[providerID] = struct{}{}
	}
	moduleValues, err := objectListField(map[string]any(plan), "resolvedPlan", "modules")
	if err != nil {
		return err
	}
	selectedModules := make(map[string]struct{}, len(moduleValues))
	selectedModuleProviders := make(map[string]string, len(moduleValues))
	for index, module := range moduleValues {
		id, err := stringField(module, fmt.Sprintf("resolvedPlan.modules[%d]", index), "id")
		if err != nil {
			return err
		}
		selectedModules[id] = struct{}{}
		providerRef, _, err := optionalStringField(module, fmt.Sprintf("resolvedPlan.modules[%d]", index), "providerRef")
		if err != nil {
			return err
		}
		selectedModuleProviders[id] = providerRef
	}
	addons, hasAddons, err := optionalObjectField(map[string]any(plan), "resolvedPlan", "addons")
	if err != nil {
		return err
	}
	selectedAddons := make(map[string]struct{})
	if hasAddons {
		for id := range addons {
			selectedAddons[id] = struct{}{}
		}
	}
	selectedContracts := make(map[string]struct{}, len(selectedCapabilities)+len(selectedProviders)+len(selectedAddons)+len(workloadProviders))
	for id := range selectedCapabilities {
		selectedContracts[id] = struct{}{}
	}
	for id := range selectedProviders {
		selectedContracts[id] = struct{}{}
	}
	for id := range selectedAddons {
		selectedContracts[id] = struct{}{}
	}
	for workloadID := range workloadProviders {
		selectedContracts[workloadID] = struct{}{}
	}
	for adapterID := range runtimeAdapterProviders {
		selectedContracts[adapterID] = struct{}{}
	}

	sites, err := objectListField(map[string]any(plan), "resolvedPlan", "sites")
	if err != nil {
		return err
	}
	selectedSiteKinds := map[string]struct{}{}
	for index, site := range sites {
		kind, err := stringField(site, fmt.Sprintf("resolvedPlan.sites[%d]", index), "kind")
		if err != nil {
			return err
		}
		selectedSiteKinds[kind] = struct{}{}
	}
	for capabilityID := range selectedCapabilities {
		contract := catalog.capabilities[capabilityID]
		if err := requireSelectedCapabilityRequirements(contract, "catalog.capabilities."+capabilityID, selectedCapabilities, catalog); err != nil {
			return err
		}
		conflicts, err := stringListField(contract, "catalog.capabilities."+capabilityID, "conflicts", false)
		if err != nil {
			return err
		}
		if err := rejectSelectedConflicts("catalog.capabilities."+capabilityID, conflicts, selectedContracts); err != nil {
			return err
		}
		supportedKinds, err := stringListField(contract, "catalog.capabilities."+capabilityID, "supportedSiteKinds", true)
		if err != nil {
			return err
		}
		if !setsIntersect(selectedSiteKinds, supportedKinds) {
			return fmt.Errorf("resolved capability %q supports no site kind in the persisted topology", capabilityID)
		}
	}
	for providerID := range selectedProviders {
		contract := catalog.providers[providerID]
		if err := requireSelectedCapabilityRequirements(contract, "catalog.providers."+providerID, selectedCapabilities, catalog); err != nil {
			return err
		}
		conflicts, err := stringListField(contract, "catalog.providers."+providerID, "conflicts", false)
		if err != nil {
			return err
		}
		if err := rejectSelectedConflicts("catalog.providers."+providerID, conflicts, selectedContracts); err != nil {
			return err
		}
	}
	kit, err := objectField(map[string]any(plan), "resolvedPlan", "kit")
	if err != nil {
		return err
	}
	kitSlug, err := stringField(kit, "resolvedPlan.kit", "slug")
	if err != nil {
		return err
	}
	for addonID := range selectedAddons {
		contract := catalog.addons[addonID]
		if contract == nil {
			return fmt.Errorf("resolvedPlan.addons.%s has no bound add-on body", addonID)
		}
		supportedKits, err := stringListField(contract, "catalog.addons."+addonID, "supportedKits", true)
		if err != nil {
			return err
		}
		if !contains(supportedKits, kitSlug) {
			return fmt.Errorf("resolvedPlan.addons.%s does not support bound kit %q", addonID, kitSlug)
		}
		provides, err := stringListField(contract, "catalog.addons."+addonID, "provides", true)
		if err != nil {
			return err
		}
		for _, capabilityID := range provides {
			if _, selected := selectedCapabilities[capabilityID]; !selected {
				return fmt.Errorf("resolvedPlan.addons.%s omits provided capability %q", addonID, capabilityID)
			}
		}
		if err := requireSelectedCapabilityRequirements(contract, "catalog.addons."+addonID, selectedCapabilities, catalog); err != nil {
			return err
		}
		conflicts, err := stringListField(contract, "catalog.addons."+addonID, "conflicts", false)
		if err != nil {
			return err
		}
		if err := rejectSelectedConflicts("catalog.addons."+addonID, conflicts, selectedContracts); err != nil {
			return err
		}
	}
	for moduleID := range selectedModules {
		contract := catalog.modules[moduleID]
		if contract == nil {
			return fmt.Errorf("resolvedPlan.modules.%s has no bound module body", moduleID)
		}
		requires, err := stringListField(contract, "catalog.modules."+moduleID, "requires", false)
		if err != nil {
			return err
		}
		for _, dependencyID := range requires {
			if _, selected := selectedModules[dependencyID]; !selected {
				return fmt.Errorf("resolvedPlan.modules.%s omits bound dependency %q", moduleID, dependencyID)
			}
		}
	}
	if err := detectModuleCycles(selectedModuleProviders, catalog); err != nil {
		return fmt.Errorf("resolvedPlan.modules contains a dependency cycle rejected by the compiler: %w", err)
	}
	return nil
}

func requireSelectedCapabilityRequirements(contract map[string]any, path string, selected map[string]struct{}, catalog *indexedCatalog) error {
	required, err := requirements(contract, path)
	if err != nil {
		return err
	}
	for _, requirement := range required {
		_, exists := selected[requirement.id]
		if exists {
			if err := validateRequirementVersion(requirement, path+".requires", catalog); err != nil {
				return err
			}
		}
		if requirement.optional {
			continue
		}
		if !exists {
			return fmt.Errorf("%s requires selected capability %q", path, requirement.id)
		}
	}
	return nil
}

func rejectSelectedConflicts(path string, conflicts []string, selected map[string]struct{}) error {
	for _, conflict := range conflicts {
		if _, exists := selected[conflict]; exists {
			return fmt.Errorf("%s conflicts with selected contract %q", path, conflict)
		}
	}
	return nil
}

func setsIntersect(selected map[string]struct{}, candidates []string) bool {
	for _, candidate := range candidates {
		if _, exists := selected[candidate]; exists {
			return true
		}
	}
	return false
}

func resolvedCapabilityProviders(plan ResolvedPlan, catalog *indexedCatalog) (map[string]string, error) {
	values, err := objectListField(map[string]any(plan), "resolvedPlan", "capabilities")
	if err != nil {
		return nil, err
	}
	result := make(map[string]string, len(values))
	for index, value := range values {
		path := fmt.Sprintf("resolvedPlan.capabilities[%d]", index)
		id, err := stringField(value, path, "id")
		if err != nil {
			return nil, err
		}
		if _, exists := catalog.capabilities[id]; !exists {
			return nil, fmt.Errorf("%s id %q has no bound catalog body", path, id)
		}
		if err := requireCatalogOptionalObjectField(value, catalog.capabilities[id], path, "tlsProfile"); err != nil {
			return nil, err
		}
		providerRef, err := stringField(value, path, "providerRef")
		if err != nil {
			return nil, err
		}
		provider, exists := catalog.providers[providerRef]
		if !exists {
			return nil, fmt.Errorf("%s providerRef %q has no bound provider body", path, providerRef)
		}
		provides, err := stringListField(provider, "catalog.providers."+providerRef, "provides", true)
		if err != nil {
			return nil, err
		}
		if !contains(provides, id) {
			return nil, fmt.Errorf("%s providerRef %q does not provide %q in the bound catalog", path, providerRef, id)
		}
		result[id] = providerRef
	}
	if err := validateResolvedCapabilityProviderEligibility(plan, catalog, result); err != nil {
		return nil, err
	}
	return result, nil
}

func validateResolvedCapabilityProviderEligibility(plan ResolvedPlan, catalog *indexedCatalog, selections map[string]string) error {
	sites, err := objectListField(map[string]any(plan), "resolvedPlan", "sites")
	if err != nil {
		return err
	}
	topologyKinds := make(map[string]struct{}, len(sites))
	for index, site := range sites {
		kind, err := stringField(site, fmt.Sprintf("resolvedPlan.sites[%d]", index), "kind")
		if err != nil {
			return err
		}
		topologyKinds[kind] = struct{}{}
	}
	for _, capabilityID := range mapKeys(selections) {
		contract := catalog.capabilities[capabilityID]
		supportedKinds, err := stringListField(contract, "catalog.capabilities."+capabilityID, "supportedSiteKinds", true)
		if err != nil {
			return err
		}
		var requiredSiteKinds []string
		for kind := range topologyKinds {
			if contains(supportedKinds, kind) {
				requiredSiteKinds = append(requiredSiteKinds, kind)
			}
		}
		sort.Strings(requiredSiteKinds)
		if len(requiredSiteKinds) == 0 {
			return fmt.Errorf("resolved capability %q supports none of the persisted topology site kinds", capabilityID)
		}
		candidates, err := catalog.providerCandidates(capabilityID, requiredSiteKinds)
		if err != nil {
			return err
		}
		providerID := selections[capabilityID]
		if !contains(candidates, providerID) {
			return fmt.Errorf("resolved capability %q selects provider %q, which cannot realize every required site kind %v", capabilityID, providerID, requiredSiteKinds)
		}
	}
	return nil
}

func validateResolvedProviderBodies(plan ResolvedPlan, catalog *indexedCatalog, capabilityProviders, workloadProviders, runtimeAdapterProviders map[string]string) error {
	capabilityValues, err := objectListField(map[string]any(plan), "resolvedPlan", "capabilities")
	if err != nil {
		return err
	}
	capabilities, err := indexObjectsByID(capabilityValues, "resolvedPlan.capabilities")
	if err != nil {
		return err
	}
	values, err := objectListField(map[string]any(plan), "resolvedPlan", "providers")
	if err != nil {
		return err
	}
	actual := make(map[string]map[string]any, len(values))
	for index, value := range values {
		path := fmt.Sprintf("resolvedPlan.providers[%d]", index)
		id, err := stringField(value, path, "id")
		if err != nil {
			return err
		}
		if _, duplicate := actual[id]; duplicate {
			return fmt.Errorf("%s duplicates provider %q", path, id)
		}
		actual[id] = value
	}
	expectedIDs := make(map[string]struct{})
	for _, providerRef := range capabilityProviders {
		expectedIDs[providerRef] = struct{}{}
	}
	for _, providerRef := range workloadProviders {
		expectedIDs[providerRef] = struct{}{}
	}
	for _, providerRef := range runtimeAdapterProviders {
		expectedIDs[providerRef] = struct{}{}
	}
	if !sameStringSet(mapKeys(actual), mapKeys(expectedIDs)) {
		return fmt.Errorf("resolvedPlan.providers is not the exact provider set selected by resolved capabilities, workloads, and runtime adapters")
	}

	nodeSites, nodeKinds, enabledNodes, err := resolvedTopologyIndex(plan)
	if err != nil {
		return err
	}
	haProviderRef, haProviderSites, _, err := resolvedHAProviderPlacement(plan)
	if err != nil {
		return err
	}
	for _, id := range mapKeys(actual) {
		provider := actual[id]
		contract := catalog.providers[id]
		if contract == nil {
			return fmt.Errorf("resolvedPlan.providers.%s has no bound catalog body", id)
		}
		path := "resolvedPlan.providers." + id
		if err := requireCatalogObjectField(provider, contract, path, "realization"); err != nil {
			return err
		}
		if err := requireCatalogField(provider, contract, path, "certificateIssuers"); err != nil {
			return err
		}
		if err := requireCatalogField(provider, contract, path, "overlayContracts"); err != nil {
			return err
		}
		if err := requireCatalogField(provider, contract, path, "remoteActionContracts"); err != nil {
			return err
		}
		wantProvides := capabilitiesForProvider(capabilityProviders, id)
		haveProvides, err := stringListField(provider, path, "provides", true)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(haveProvides, wantProvides) {
			return fmt.Errorf("%s.provides is not the exact selected capability projection", path)
		}
		var wantWorkloads []string
		for workloadID, providerRef := range workloadProviders {
			if providerRef == id {
				wantWorkloads = append(wantWorkloads, workloadID)
			}
		}
		wantWorkloads = sortStringsUnique(wantWorkloads)
		haveWorkloads, err := stringListField(provider, path, "workloadRefs", false)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(haveWorkloads, wantWorkloads) {
			return fmt.Errorf("%s.workloadRefs is not the exact selected workload projection", path)
		}
		var wantAdapters []string
		for adapterID, providerRef := range runtimeAdapterProviders {
			if providerRef == id {
				wantAdapters = append(wantAdapters, adapterID)
			}
		}
		wantAdapters = sortStringsUnique(wantAdapters)
		haveAdapters, err := stringListField(provider, path, "runtimeAdapterRefs", false)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(haveAdapters, wantAdapters) {
			return fmt.Errorf("%s.runtimeAdapterRefs is not the exact selected adapter projection", path)
		}
		supportedKinds, err := stringListField(contract, "catalog.providers."+id, "supportedSiteKinds", true)
		if err != nil {
			return err
		}
		wantSites := eligibleTopologySites(enabledNodes, nodeSites, nodeKinds, supportedKinds)
		if id == haProviderRef {
			wantSites = haProviderSites
		}
		haveSites, err := stringListField(provider, path, "siteRefs", true)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(haveSites, wantSites) {
			return fmt.Errorf("%s.siteRefs is not the exact bound provider placement projection", path)
		}
		if err := validateResolvedProviderOwnerBody(provider, contract, capabilities, capabilityProviders, id, path); err != nil {
			return err
		}
	}
	return nil
}

func resolvedHAProviderPlacement(plan ResolvedPlan) (string, []string, []string, error) {
	availability, err := objectField(map[string]any(plan), "resolvedPlan", "availability")
	if err != nil {
		return "", nil, nil, err
	}
	enabled, err := boolFieldDefault(availability, "resolvedPlan.availability", "enabled", false)
	if err != nil || !enabled {
		return "", nil, nil, err
	}
	providerRef, err := stringField(availability, "resolvedPlan.availability", "providerRef")
	if err != nil {
		return "", nil, nil, err
	}
	members, err := objectListField(availability, "resolvedPlan.availability", "selectedMembers")
	if err != nil {
		return "", nil, nil, err
	}
	siteRefs := make([]string, 0, len(members))
	nodeRefs := make([]string, 0, len(members))
	for index, member := range members {
		path := fmt.Sprintf("resolvedPlan.availability.selectedMembers[%d]", index)
		siteRef, err := stringField(member, path, "siteRef")
		if err != nil {
			return "", nil, nil, err
		}
		nodeRef, err := stringField(member, path, "nodeRef")
		if err != nil {
			return "", nil, nil, err
		}
		siteRefs = append(siteRefs, siteRef)
		nodeRefs = append(nodeRefs, nodeRef)
	}
	return providerRef, sortStringsUnique(siteRefs), sortStringsUnique(nodeRefs), nil
}

func validateResolvedProviderOwnerBody(provider, contract map[string]any, capabilities map[string]map[string]any, capabilityProviders map[string]string, providerID, path string) error {
	realization, err := objectField(contract, "catalog.providers", "realization")
	if err != nil {
		return err
	}
	kind, err := stringField(realization, "catalog.providers.realization", "kind")
	if err != nil {
		return err
	}
	owner, hasOwner, err := optionalObjectField(provider, path, "owner")
	if err != nil {
		return err
	}
	if kind != "host" && kind != "external" {
		if hasOwner {
			return fmt.Errorf("%s.owner is forbidden by bound provider realization %q", path, kind)
		}
		return nil
	}
	if !hasOwner {
		return fmt.Errorf("%s.owner is required by bound provider realization %q", path, kind)
	}
	wantRef, err := stringField(realization, "catalog.providers.realization", "ownerRef")
	if err != nil {
		return err
	}
	for field, want := range map[string]string{"ref": wantRef, "kind": kind} {
		have, err := stringField(owner, path+".owner", field)
		if err != nil {
			return err
		}
		if have != want {
			return fmt.Errorf("%s.owner.%s does not match the bound provider body", path, field)
		}
	}
	wantSupport, err := objectField(realization, "catalog.providers.realization", "realizationSupport")
	if err != nil {
		return err
	}
	haveSupport, err := objectField(owner, path+".owner", "realizationSupport")
	if err != nil {
		return err
	}
	if equal, err := canonicalEqual(haveSupport, wantSupport); err != nil {
		return err
	} else if !equal {
		return fmt.Errorf("%s.owner.realizationSupport does not match the bound provider body", path)
	}
	wantInputs, err := expectedProviderOwnerInputs(realization, capabilities, capabilityProviders, providerID)
	if err != nil {
		return err
	}
	haveInputs, err := objectField(owner, path+".owner", "inputs")
	if err != nil {
		return err
	}
	if equal, err := canonicalEqual(haveInputs, wantInputs); err != nil {
		return err
	} else if !equal {
		return fmt.Errorf("%s.owner.inputs is not the exact projection of bound inputBindings and resolved capability inputs", path)
	}
	return nil
}

func expectedProviderOwnerInputs(realization map[string]any, capabilities map[string]map[string]any, capabilityProviders map[string]string, providerID string) (map[string]any, error) {
	values := map[string]any{}
	secretRefs := map[string]any{}
	bindings, exists, err := optionalObjectField(realization, "catalog.providers.realization", "inputBindings")
	if err != nil || !exists {
		return map[string]any{"values": values, "secretRefs": secretRefs}, err
	}
	for _, inputRef := range sortedStringMapKeys(bindings) {
		binding, err := asObject(bindings[inputRef], "catalog.providers.realization.inputBindings."+inputRef)
		if err != nil {
			return nil, err
		}
		capabilityRef, err := stringField(binding, "catalog.providers.realization.inputBindings."+inputRef, "capabilityRef")
		if err != nil {
			return nil, err
		}
		capability, exists := capabilities[capabilityRef]
		if !exists {
			return nil, fmt.Errorf("bound provider input %q references unselected capability %q", inputRef, capabilityRef)
		}
		if selectedProvider := capabilityProviders[capabilityRef]; selectedProvider != providerID {
			return nil, fmt.Errorf("bound provider input %q references capability %q selected through provider %q, not owner %q", inputRef, capabilityRef, selectedProvider, providerID)
		}
		key, err := stringField(binding, "catalog.providers.realization.inputBindings."+inputRef, "key")
		if err != nil {
			return nil, err
		}
		source, err := stringField(binding, "catalog.providers.realization.inputBindings."+inputRef, "source")
		if err != nil {
			return nil, err
		}
		switch source {
		case "capability-setting":
			settings, hasSettings, err := optionalObjectField(capability, "resolvedPlan.capabilities."+capabilityRef, "settings")
			if err != nil {
				return nil, err
			}
			if hasSettings {
				if value, present := settings[key]; present {
					clone, err := cloneObject(map[string]any{"value": value}, false)
					if err != nil {
						return nil, err
					}
					values[inputRef] = clone["value"]
				}
			}
		case "capability-secret":
			refs, hasRefs, err := optionalObjectField(capability, "resolvedPlan.capabilities."+capabilityRef, "secretRefs")
			if err != nil {
				return nil, err
			}
			if hasRefs {
				if value, present := refs[key]; present {
					secretRefs[inputRef] = value
				}
			}
		default:
			return nil, fmt.Errorf("bound provider input %q uses unsupported source %q", inputRef, source)
		}
	}
	return map[string]any{"values": values, "secretRefs": secretRefs}, nil
}
