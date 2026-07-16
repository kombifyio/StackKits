package resolvedplan

import (
	"sort"
)

type resolution struct {
	capabilityIDs   []string
	providerByCap   map[string]string
	providerIDs     []string
	addonIDs        []string
	addonSelection  map[string]any
	settingsByCap   map[string]map[string]any
	secretRefsByCap map[string]map[string]any
	availability    *availabilityPolicyView
}

func resolveContracts(profile *profileView, spec *specView, catalog *indexedCatalog) (*resolution, error) {
	enabled, allowed, forbidden, disabled, err := initializeCapabilitySelection(profile, spec)
	if err != nil {
		return nil, err
	}

	settings, secretRefs, requestedProviders, err := capabilitySettings(spec.capabilities)
	if err != nil {
		return nil, err
	}
	addonIDs, addonSelection, err := resolveAddOns(profile, spec, catalog, enabled, allowed, forbidden, disabled)
	if err != nil {
		return nil, err
	}
	if err := validateHASelection(spec, addonIDs, enabled); err != nil {
		return nil, err
	}
	availability, err := bindHAProviderSelection(profile, spec, catalog, requestedProviders)
	if err != nil {
		return nil, err
	}

	providerByCap, err := resolveCapabilityProviders(enabled, allowed, forbidden, requestedProviders, spec.siteKinds, catalog)
	if err != nil {
		return nil, err
	}
	if err := validateConflicts(enabled, providerByCap, addonIDs, catalog); err != nil {
		return nil, err
	}

	capabilityIDs := sortedSet(enabled)
	providerSet := make(map[string]struct{})
	for _, provider := range providerByCap {
		providerSet[provider] = struct{}{}
	}
	return &resolution{
		capabilityIDs: capabilityIDs, providerByCap: providerByCap,
		providerIDs: sortedSet(providerSet), addonIDs: addonIDs,
		addonSelection: addonSelection, settingsByCap: settings,
		secretRefsByCap: secretRefs, availability: availability,
	}, nil
}

func initializeCapabilitySelection(profile *profileView, spec *specView) (map[string]struct{}, map[string]struct{}, map[string]struct{}, map[string]struct{}, error) {
	allowed := stringSet(append(append(append([]string{}, profile.requiredCapabilities...), profile.defaultCapabilities...), profile.optionalCapabilities...))
	forbidden := stringSet(profile.forbiddenCapabilities)
	enable, err := stringListField(spec.capabilities, "spec.capabilities", "enable", false)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	if contains(profile.requiredCapabilities, "availability-ha") || contains(profile.defaultCapabilities, "availability-ha") {
		return nil, nil, nil, nil, fail(ErrProfileMismatch, "definition.capabilities", "availability-ha must be optional and selected only through addons.ha")
	}
	if contains(enable, "availability-ha") {
		return nil, nil, nil, nil, fail(ErrProfileMismatch, "spec.capabilities.enable", "availability-ha cannot be enabled directly; use addons.ha")
	}
	disable, err := stringListField(spec.capabilities, "spec.capabilities", "disable", false)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	disabled, err := validateDisabledCapabilities(profile, disable, allowed)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	enabled := make(map[string]struct{})
	for _, id := range append(append([]string{}, profile.requiredCapabilities...), profile.defaultCapabilities...) {
		if _, isDisabled := disabled[id]; !isDisabled {
			enabled[id] = struct{}{}
		}
	}
	if err := applyExplicitCapabilities(profile, enable, enabled, allowed, forbidden, disabled); err != nil {
		return nil, nil, nil, nil, err
	}
	return enabled, allowed, forbidden, disabled, nil
}

func validateDisabledCapabilities(profile *profileView, disable []string, allowed map[string]struct{}) (map[string]struct{}, error) {
	disabled := make(map[string]struct{}, len(disable))
	for _, id := range disable {
		disabled[id] = struct{}{}
		if contains(profile.requiredCapabilities, id) {
			return nil, fail(ErrProfileMismatch, "spec.capabilities.disable", "required capability %q cannot be disabled", id)
		}
		if _, exists := allowed[id]; !exists {
			return nil, fail(ErrUnknownCapability, "spec.capabilities.disable", "capability %q is not declared by the kit", id)
		}
	}
	return disabled, nil
}

func applyExplicitCapabilities(profile *profileView, enable []string, enabled, allowed, forbidden, disabled map[string]struct{}) error {
	for _, id := range enable {
		if _, denied := forbidden[id]; denied {
			return fail(ErrForbiddenCapability, "spec.capabilities.enable", "%q is forbidden by %s", id, profile.slug)
		}
		if _, exists := allowed[id]; !exists {
			return fail(ErrUnknownCapability, "spec.capabilities.enable", "capability %q is not declared by the kit", id)
		}
		if _, isDisabled := disabled[id]; isDisabled {
			return fail(ErrInvalidInput, "spec.capabilities", "capability %q is both enabled and disabled", id)
		}
		enabled[id] = struct{}{}
	}
	return nil
}

func resolveCapabilityProviders(enabled, allowed, forbidden map[string]struct{}, requestedProviders map[string]string, siteKinds map[string]struct{}, catalog *indexedCatalog) (map[string]string, error) {
	for {
		capabilityChanged, err := closeCapabilityRequirements(enabled, allowed, forbidden, catalog)
		if err != nil {
			return nil, err
		}
		providerByCap, err := selectProviders(enabled, requestedProviders, siteKinds, catalog)
		if err != nil {
			return nil, err
		}
		providerChanged, err := closeProviderRequirements(enabled, allowed, forbidden, providerByCap, catalog)
		if err != nil {
			return nil, err
		}
		if !capabilityChanged && !providerChanged {
			// Resolve once more after the dependency closure stabilized.
			return selectProviders(enabled, requestedProviders, siteKinds, catalog)
		}
	}
}

func stringSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func capabilitySettings(selection map[string]any) (map[string]map[string]any, map[string]map[string]any, map[string]string, error) {
	config, exists, err := optionalObjectField(selection, "spec.capabilities", "config")
	if err != nil || !exists {
		return map[string]map[string]any{}, map[string]map[string]any{}, map[string]string{}, err
	}
	result := make(map[string]map[string]any, len(config))
	secretRefs := make(map[string]map[string]any, len(config))
	providerRefs := make(map[string]string, len(config))
	for id, raw := range config {
		selectionConfig, err := asObject(raw, "spec.capabilities.config."+id)
		if err != nil {
			return nil, nil, nil, err
		}
		if providerRef, exists, err := optionalStringField(selectionConfig, "spec.capabilities.config."+id, "providerRef"); err != nil {
			return nil, nil, nil, err
		} else if exists {
			providerRefs[id] = providerRef
		}
		if public, exists, err := optionalObjectField(selectionConfig, "spec.capabilities.config."+id, "settings"); err != nil {
			return nil, nil, nil, err
		} else if exists {
			cloned, err := cloneObject(public, false)
			if err != nil {
				return nil, nil, nil, err
			}
			result[id] = cloned
		}
		if refs, exists, err := optionalObjectField(selectionConfig, "spec.capabilities.config."+id, "secretRefs"); err != nil {
			return nil, nil, nil, err
		} else if exists {
			cloned, err := cloneObject(refs, false)
			if err != nil {
				return nil, nil, nil, err
			}
			secretRefs[id] = cloned
		}
	}
	return result, secretRefs, providerRefs, nil
}

func resolveAddOns(profile *profileView, spec *specView, catalog *indexedCatalog, enabled, allowed, forbidden, disabled map[string]struct{}) ([]string, map[string]any, error) {
	if len(spec.addons) == 0 {
		return nil, nil, nil
	}
	var ids []string
	selection := make(map[string]any)
	for id, rawSelection := range spec.addons {
		clean, selected, err := resolveAddOn(id, rawSelection, profile, catalog, enabled, allowed, forbidden, disabled)
		if err != nil {
			return nil, nil, err
		}
		if !selected {
			continue
		}
		selection[id] = clean
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, selection, nil
}

func resolveAddOn(id string, rawSelection any, profile *profileView, catalog *indexedCatalog, enabled, allowed, forbidden, disabled map[string]struct{}) (map[string]any, bool, error) {
	selected, err := asObject(rawSelection, "spec.addons."+id)
	if err != nil {
		return nil, false, err
	}
	enabledFlag, err := boolFieldDefault(selected, "spec.addons."+id, "enabled", true)
	if err != nil || !enabledFlag {
		return nil, false, err
	}
	contract, exists := catalog.addons[id]
	if !exists {
		return nil, false, fail(ErrUnknownAddOn, "spec.addons."+id, "no governed add-on contract exists")
	}
	if err := validateAddOnKit(id, profile.slug, contract); err != nil {
		return nil, false, err
	}
	if err := applyAddOnCapabilities(id, contract, enabled, allowed, forbidden, disabled); err != nil {
		return nil, false, err
	}
	if err := applyAddOnRequirements(id, contract, enabled, allowed, catalog); err != nil {
		return nil, false, err
	}
	clean, err := cloneObject(selected, true)
	if err != nil {
		return nil, false, err
	}
	clean["enabled"] = true
	return clean, true, nil
}

func validateAddOnKit(id, kitSlug string, contract map[string]any) error {
	supportedKits, err := stringListField(contract, "catalog.addons."+id, "supportedKits", true)
	if err != nil {
		return err
	}
	if !contains(supportedKits, kitSlug) {
		return fail(ErrUnsupportedAddOn, "spec.addons."+id, "add-on does not support %s", kitSlug)
	}
	return nil
}

func applyAddOnCapabilities(id string, contract map[string]any, enabled, allowed, forbidden, disabled map[string]struct{}) error {
	provides, err := stringListField(contract, "catalog.addons."+id, "provides", true)
	if err != nil {
		return err
	}
	for _, capability := range provides {
		if _, explicitlyDisabled := disabled[capability]; explicitlyDisabled {
			return fail(ErrContractConflict, "spec.addons."+id, "add-on provides explicitly disabled capability %q", capability)
		}
		if _, denied := forbidden[capability]; denied {
			return fail(ErrForbiddenCapability, "catalog.addons."+id+".provides", "add-on provides forbidden capability %q", capability)
		}
		if _, declared := allowed[capability]; !declared {
			return fail(ErrUnknownCapability, "catalog.addons."+id+".provides", "capability %q is not declared by the kit", capability)
		}
		enabled[capability] = struct{}{}
	}
	return nil
}

func applyAddOnRequirements(id string, contract map[string]any, enabled, allowed map[string]struct{}, catalog *indexedCatalog) error {
	requires, err := requirements(contract, "catalog.addons."+id)
	if err != nil {
		return err
	}
	for _, requirement := range requires {
		if requirement.optional {
			if _, selected := enabled[requirement.id]; !selected {
				continue
			}
		}
		if err := validateRequirementVersion(requirement, "catalog.addons."+id+".requires", catalog); err != nil {
			return err
		}
		if requirement.optional {
			continue
		}
		if _, declared := allowed[requirement.id]; !declared {
			return fail(ErrUnknownCapability, "catalog.addons."+id+".requires", "capability %q is not declared by the kit", requirement.id)
		}
		enabled[requirement.id] = struct{}{}
	}
	return nil
}

func validateHASelection(spec *specView, addonIDs []string, enabled map[string]struct{}) error {
	haAddon := contains(addonIDs, "ha")
	_, haCapability := enabled["availability-ha"]
	haTopology := spec.controlMode != "single" || spec.availabilityEnabled
	if haAddon != haCapability || haAddon != haTopology {
		return fail(ErrProfileMismatch, "spec.addons.ha", "addons.ha, availability-ha, availability.enabled, and non-single control-plane mode must resolve together")
	}
	return nil
}

func closeCapabilityRequirements(enabled, allowed, forbidden map[string]struct{}, catalog *indexedCatalog) (bool, error) {
	return closeRequirements("capabilities", sortedSet(enabled), catalog.capabilities, enabled, allowed, forbidden, catalog)
}

func selectProviders(enabled map[string]struct{}, requestedProviders map[string]string, siteKinds map[string]struct{}, catalog *indexedCatalog) (map[string]string, error) {
	selected := make(map[string]string, len(enabled))
	for _, capability := range sortedSet(enabled) {
		contractSupported, err := stringListField(catalog.capabilities[capability], "catalog.capabilities."+capability, "supportedSiteKinds", true)
		if err != nil {
			return nil, err
		}
		var requiredSiteKinds []string
		for kind := range siteKinds {
			if contains(contractSupported, kind) {
				requiredSiteKinds = append(requiredSiteKinds, kind)
			}
		}
		sort.Strings(requiredSiteKinds)
		if len(requiredSiteKinds) == 0 {
			return nil, fail(ErrUnrealizedCapability, "capabilities."+capability, "capability contract supports none of the selected site kinds")
		}
		candidates, err := catalog.providerCandidates(capability, requiredSiteKinds)
		if err != nil {
			return nil, err
		}
		requested := requestedProviders[capability]
		if requested != "" {
			if _, exists := catalog.providers[requested]; !exists {
				return nil, fail(ErrUnknownProvider, "spec.capabilities.config."+capability+".providerRef", "provider %q is not governed", requested)
			}
			if !contains(candidates, requested) {
				return nil, fail(ErrUnrealizedCapability, "spec.capabilities.config."+capability+".providerRef", "provider %q cannot realize %q on this topology", requested, capability)
			}
			selected[capability] = requested
			continue
		}
		switch len(candidates) {
		case 0:
			return nil, fail(ErrUnrealizedCapability, "capabilities."+capability, "no provider can realize the capability on this topology")
		case 1:
			selected[capability] = candidates[0]
		default:
			defaults, err := catalog.defaultProviderCandidates(candidates, requiredSiteKinds)
			if err != nil {
				return nil, err
			}
			if len(defaults) == 1 {
				selected[capability] = defaults[0]
				continue
			}
			return nil, fail(ErrAmbiguousProvider, "capabilities."+capability, "providers %v are eligible; select providerRef explicitly", candidates)
		}
	}
	return selected, nil
}

func closeProviderRequirements(enabled, allowed, forbidden map[string]struct{}, selected map[string]string, catalog *indexedCatalog) (bool, error) {
	return closeRequirements("providers", sortedSet(stringSetFromValues(selected)), catalog.providers, enabled, allowed, forbidden, catalog)
}

func stringSetFromValues(values map[string]string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func closeRequirements(kind string, ids []string, contracts map[string]map[string]any, enabled, allowed, forbidden map[string]struct{}, catalog *indexedCatalog) (bool, error) {
	changed := false
	for _, id := range ids {
		contract, exists := contracts[id]
		if !exists {
			return false, fail(ErrUnknownCapability, kind+"."+id, "no governed contract exists")
		}
		ownerPath := "catalog." + kind + "." + id
		requires, err := requirements(contract, ownerPath)
		if err != nil {
			return false, err
		}
		for _, requirement := range requires {
			added, err := applyContractRequirement(requirement, ownerPath+".requires", enabled, allowed, forbidden, catalog)
			if err != nil {
				return false, err
			}
			changed = changed || added
		}
	}
	return changed, nil
}

func applyContractRequirement(requirement requirement, requirementPath string, enabled, allowed, forbidden map[string]struct{}, catalog *indexedCatalog) (bool, error) {
	if requirement.optional {
		if _, selected := enabled[requirement.id]; !selected {
			return false, nil
		}
	}
	if err := validateRequirementVersion(requirement, requirementPath, catalog); err != nil {
		return false, err
	}
	if requirement.optional {
		return false, nil
	}
	if _, denied := forbidden[requirement.id]; denied {
		return false, fail(ErrContractConflict, requirementPath, "requires forbidden capability %q", requirement.id)
	}
	if _, declared := allowed[requirement.id]; !declared {
		return false, fail(ErrUnknownCapability, requirementPath, "capability %q is not declared by the kit", requirement.id)
	}
	if _, exists := enabled[requirement.id]; exists {
		return false, nil
	}
	enabled[requirement.id] = struct{}{}
	return true, nil
}

func validateConflicts(enabled map[string]struct{}, selected map[string]string, addonIDs []string, catalog *indexedCatalog) error {
	for _, id := range sortedSet(enabled) {
		conflicts, err := stringListField(catalog.capabilities[id], "catalog.capabilities."+id, "conflicts", false)
		if err != nil {
			return err
		}
		for _, conflict := range conflicts {
			if _, exists := enabled[conflict]; exists {
				return fail(ErrContractConflict, "catalog.capabilities."+id+".conflicts", "%q conflicts with enabled capability %q", id, conflict)
			}
		}
	}
	providerSet := make(map[string]struct{})
	for _, provider := range selected {
		providerSet[provider] = struct{}{}
	}
	for _, id := range sortedSet(providerSet) {
		conflicts, err := stringListField(catalog.providers[id], "catalog.providers."+id, "conflicts", false)
		if err != nil {
			return err
		}
		for _, conflict := range conflicts {
			if _, exists := providerSet[conflict]; exists {
				return fail(ErrContractConflict, "catalog.providers."+id+".conflicts", "%q conflicts with selected provider %q", id, conflict)
			}
		}
	}
	addonSet := make(map[string]struct{}, len(addonIDs))
	for _, id := range addonIDs {
		addonSet[id] = struct{}{}
	}
	for _, id := range addonIDs {
		conflicts, err := stringListField(catalog.addons[id], "catalog.addons."+id, "conflicts", false)
		if err != nil {
			return err
		}
		for _, conflict := range conflicts {
			_, addonConflict := addonSet[conflict]
			_, providerConflict := providerSet[conflict]
			_, capabilityConflict := enabled[conflict]
			if addonConflict || providerConflict || capabilityConflict {
				return fail(ErrContractConflict, "catalog.addons."+id+".conflicts", "%q conflicts with selected contract %q", id, conflict)
			}
		}
	}
	return nil
}

func sortedSet(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func validateRequirementVersion(requirement requirement, path string, catalog *indexedCatalog) error {
	contract, exists := catalog.capabilities[requirement.id]
	if !exists {
		return fail(ErrUnknownCapability, path, "required capability %q has no governed contract", requirement.id)
	}
	if requirement.minVersion == "" {
		return nil
	}
	version, err := metadataVersion(contract, "catalog.capabilities."+requirement.id)
	if err != nil {
		return err
	}
	atLeast, err := versionAtLeast(version, requirement.minVersion)
	if err != nil {
		return fail(ErrInvalidInput, path, "cannot compare %q requirement: %v", requirement.id, err)
	}
	if !atLeast {
		return fail(ErrUnrealizedCapability, path, "capability %q is %s but requires at least %s", requirement.id, version, requirement.minVersion)
	}
	return nil
}
