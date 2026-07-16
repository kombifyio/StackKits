package resolvedplan

import (
	"reflect"
)

type availabilityPolicyView struct {
	mode               string
	policyRef          string
	realizationRef     string
	providerRef        string
	moduleRef          string
	selector           string
	failureModel       map[string]any
	healthAcceptance   map[string]any
	evidenceAcceptance map[string]any
	requiredGateRefs   []string
	requiredEvidence   []string
}

func parseAvailabilityPolicies(definition map[string]any) (map[string]availabilityPolicyView, error) {
	availability, err := objectField(definition, "definition", "availability")
	if err != nil {
		return nil, err
	}
	policies, err := objectField(availability, "definition.availability", "policies")
	if err != nil {
		return nil, err
	}
	expectedModes := []string{"quorum", "warm-standby"}
	if actual := sortedStringMapKeys(policies); !equalStrings(actual, expectedModes) {
		return nil, fail(ErrInvalidInput, "definition.availability.policies", "expected exactly policies %v, got %v", expectedModes, actual)
	}
	result := make(map[string]availabilityPolicyView, len(expectedModes))
	for _, mode := range expectedModes {
		path := "definition.availability.policies." + mode
		policy, err := objectField(policies, "definition.availability.policies", mode)
		if err != nil {
			return nil, err
		}
		view := availabilityPolicyView{mode: mode}
		for name, target := range map[string]*string{
			"policyRef": &view.policyRef, "realizationRef": &view.realizationRef,
			"providerRef": &view.providerRef, "moduleRef": &view.moduleRef, "selector": &view.selector,
		} {
			if *target, err = stringField(policy, path, name); err != nil {
				return nil, err
			}
		}
		if view.selector != "control-plane-members" {
			return nil, fail(ErrInvalidInput, path+".selector", "HA policy must select control-plane-members")
		}
		if view.failureModel, err = cloneRequiredObject(policy, path, "failureModel"); err != nil {
			return nil, err
		}
		if view.healthAcceptance, err = cloneRequiredObject(policy, path, "healthAcceptance"); err != nil {
			return nil, err
		}
		if view.evidenceAcceptance, err = cloneRequiredObject(policy, path, "evidenceAcceptance"); err != nil {
			return nil, err
		}
		if view.requiredGateRefs, err = stringListField(view.healthAcceptance, path+".healthAcceptance", "requiredGateRefs", true); err != nil {
			return nil, err
		}
		if view.requiredEvidence, err = stringListField(view.evidenceAcceptance, path+".evidenceAcceptance", "requiredRefs", true); err != nil {
			return nil, err
		}
		result[mode] = view
	}
	return result, nil
}

func cloneRequiredObject(object map[string]any, path, name string) (map[string]any, error) {
	value, err := objectField(object, path, name)
	if err != nil {
		return nil, err
	}
	return cloneObject(value, true)
}

func bindHAProviderSelection(profile *profileView, spec *specView, catalog *indexedCatalog, requestedProviders map[string]string) (*availabilityPolicyView, error) {
	if !spec.availabilityEnabled {
		return nil, nil
	}
	policy, exists := profile.availabilityPolicies[spec.controlMode]
	if !exists {
		return nil, fail(ErrProfileMismatch, "spec.availability.mode", "kit %s has no HA policy for %q", profile.slug, spec.controlMode)
	}
	if requested := requestedProviders["availability-ha"]; requested != "" && requested != policy.providerRef {
		return nil, fail(ErrProfileMismatch, "spec.capabilities.config.availability-ha.providerRef", "HA provider must equal kit policy provider %q", policy.providerRef)
	}
	requestedProviders["availability-ha"] = policy.providerRef
	if err := validateHAAddOnPolicyContract(spec.controlMode, policy, catalog); err != nil {
		return nil, err
	}
	return &policy, nil
}

//nolint:gocyclo // The HA add-on contract is an explicit matrix of mode and provider invariants that must all fail closed.
func validateHAAddOnPolicyContract(mode string, policy availabilityPolicyView, catalog *indexedCatalog) error {
	addon := catalog.addons["ha"]
	if addon == nil {
		return fail(ErrUnsupportedAddOn, "catalog.addons.ha", "governed HA add-on contract is missing")
	}
	addonAvailability, err := objectField(addon, "catalog.addons.ha", "availability")
	if err != nil {
		return err
	}
	authority, err := stringField(addonAvailability, "catalog.addons.ha.availability", "policyAuthority")
	if err != nil {
		return err
	}
	selector, err := stringField(addonAvailability, "catalog.addons.ha.availability", "selector")
	if err != nil {
		return err
	}
	modes, err := stringListField(addonAvailability, "catalog.addons.ha.availability", "supportedModes", true)
	if err != nil {
		return err
	}
	if authority != "kit-definition" || selector != policy.selector || !contains(modes, mode) {
		return fail(ErrContractConflict, "catalog.addons.ha.availability", "add-on does not delegate %q to the selected kit control-member policy", mode)
	}

	provider := catalog.providers[policy.providerRef]
	if provider == nil {
		return fail(ErrUnknownProvider, "definition.availability.policies."+mode+".providerRef", "provider %q is not governed", policy.providerRef)
	}
	providerRealization, err := objectField(provider, "catalog.providers."+policy.providerRef, "realization")
	if err != nil {
		return err
	}
	providerModules, err := objectField(providerRealization, "catalog.providers."+policy.providerRef+".realization", "moduleRefs")
	if err != nil {
		return err
	}
	requiredModules, err := stringListField(providerModules, "catalog.providers."+policy.providerRef+".realization.moduleRefs", "required", true)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(requiredModules, []string{policy.moduleRef}) {
		return fail(ErrContractConflict, "definition.availability.policies."+mode+".moduleRef", "provider %q must require exactly module %q", policy.providerRef, policy.moduleRef)
	}
	module := catalog.modules[policy.moduleRef]
	if module == nil {
		return fail(ErrUnrealizedModule, "definition.availability.policies."+mode+".moduleRef", "module %q is not governed", policy.moduleRef)
	}
	moduleProvider, err := stringField(module, "catalog.modules."+policy.moduleRef, "providerRef")
	if err != nil {
		return err
	}
	selection, err := objectField(module, "catalog.modules."+policy.moduleRef, "nodeSelection")
	if err != nil {
		return err
	}
	membership, err := stringField(selection, "catalog.modules."+policy.moduleRef+".nodeSelection", "controlPlaneMembers")
	if err != nil {
		return err
	}
	if moduleProvider != policy.providerRef || membership != "only" {
		return fail(ErrContractConflict, "catalog.modules."+policy.moduleRef, "HA module must belong to %q and select only control-plane members", policy.providerRef)
	}
	memberSiteScope, err := stringField(policy.failureModel, "definition.availability.policies."+mode+".failureModel", "memberSiteScope")
	if err != nil {
		return err
	}
	if memberSiteScope == "authority-site-control-members" {
		authoritySelection, _, err := optionalStringField(selection, "catalog.modules."+policy.moduleRef+".nodeSelection", "authority")
		if err != nil {
			return err
		}
		if authoritySelection != "control-authority-site" {
			return fail(ErrContractConflict, "catalog.modules."+policy.moduleRef+".nodeSelection.authority", "authority-site HA policy requires control-authority-site module placement")
		}
	}

	expectedHealth, err := expectedHAGateRefs(policy, provider, module)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(sortStringsUnique(policy.requiredGateRefs), expectedHealth) {
		return fail(ErrContractConflict, "definition.availability.policies."+mode+".healthAcceptance.requiredGateRefs", "must equal governed provider/module health gates %v", expectedHealth)
	}
	expectedEvidence, err := expectedHAEvidence(provider, module)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(sortStringsUnique(policy.requiredEvidence), expectedEvidence) {
		return fail(ErrContractConflict, "definition.availability.policies."+mode+".evidenceAcceptance.requiredRefs", "must equal governed provider/module evidence %v", expectedEvidence)
	}
	return nil
}

func expectedHAGateRefs(policy availabilityPolicyView, provider, module map[string]any) ([]string, error) {
	var result []string
	for _, target := range []struct {
		kind     string
		ref      string
		contract map[string]any
	}{{"provider", policy.providerRef, provider}, {"module", policy.moduleRef, module}} {
		health, err := objectListOptional(target.contract, "health")
		if err != nil {
			return nil, err
		}
		for _, contract := range health {
			id, err := stringField(contract, "health", "id")
			if err != nil {
				return nil, err
			}
			result = append(result, contractID(target.kind+"-"+target.ref+"-"+id))
		}
	}
	return sortStringsUnique(result), nil
}

func expectedHAEvidence(provider, module map[string]any) ([]string, error) {
	var result []string
	for path, contract := range map[string]map[string]any{"catalog.provider": provider, "catalog.module": module} {
		values, err := stringListField(contract, path, "evidence", true)
		if err != nil {
			return nil, err
		}
		result = append(result, values...)
	}
	return sortStringsUnique(result), nil
}

func buildResolvedAvailability(profile *profileView, spec *specView) (map[string]any, error) {
	resolved, err := cloneObject(spec.availability, true)
	if err != nil {
		return nil, err
	}
	if !spec.availabilityEnabled {
		return resolved, nil
	}
	policy, exists := profile.availabilityPolicies[spec.controlMode]
	if !exists {
		return nil, fail(ErrProfileMismatch, "spec.availability.mode", "kit %s has no HA policy for %q", profile.slug, spec.controlMode)
	}
	for key, value := range map[string]string{
		"policyRef": policy.policyRef, "realizationRef": policy.realizationRef,
		"providerRef": policy.providerRef, "moduleRef": policy.moduleRef, "selector": policy.selector,
	} {
		resolved[key] = value
	}
	for key, value := range map[string]map[string]any{
		"failureModel": policy.failureModel, "healthAcceptance": policy.healthAcceptance, "evidenceAcceptance": policy.evidenceAcceptance,
	} {
		clone, err := cloneObject(value, true)
		if err != nil {
			return nil, err
		}
		resolved[key] = clone
	}
	members, err := stringListField(spec.controlPlane, "spec.controlPlane", "members", true)
	if err != nil {
		return nil, err
	}
	selected := make([]any, 0, len(members))
	for _, member := range members {
		node, exists := spec.nodeByID[member]
		if !exists {
			return nil, fail(ErrProfileMismatch, "spec.controlPlane.members", "member %q has no resolved node", member)
		}
		failureDomain, err := stringField(node.object, "spec.nodes."+member, "failureDomain")
		if err != nil {
			return nil, err
		}
		selected = append(selected, map[string]any{"nodeRef": member, "siteRef": node.siteRef, "failureDomain": failureDomain})
	}
	resolved["selectedMembers"] = selected
	return resolved, nil
}

func availabilityEvidence(profile *profileView, spec *specView) []string {
	if !spec.availabilityEnabled {
		return nil
	}
	policy, exists := profile.availabilityPolicies[spec.controlMode]
	if !exists {
		return nil
	}
	return append([]string(nil), policy.requiredEvidence...)
}
