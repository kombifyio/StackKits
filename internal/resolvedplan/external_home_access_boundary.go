package resolvedplan

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"time"
)

const (
	homeAccessRequirementAPIVersion      = "stackkit.home-access-requirement/v1"
	externalHomeAccessAPIVersion         = "stackkit.external-home-access-binding/v1"
	maxExternalHomeAccessBindingValidity = 24 * time.Hour
)

var (
	homeAccessBindingRefPattern = regexp.MustCompile(`^home-access-binding://sha256/[a-f0-9]{64}$`)
	homeAccessFabricRefPattern  = regexp.MustCompile(`^home-access-fabric://sha256/[a-f0-9]{64}$`)
	homeAccessCapabilities      = []string{"private-remote-access", "public-publish-egress"}
)

// ComputeHomeAccessRequirementHash binds the complete provider-neutral
// requirement body while avoiding a self-referential digest.
func ComputeHomeAccessRequirementHash(requirement HomeAccessRequirement) (string, error) {
	clone, err := cloneObject(map[string]any(requirement), false)
	if err != nil {
		return "", fmt.Errorf("clone Home access requirement: %w", err)
	}
	delete(clone, "requirementsHash")
	return canonicalHash(clone, false)
}

// ComputeExternalHomeAccessBindingHash binds the opaque external realization
// envelope. No provider, endpoint, address, transport, or credential is part
// of this authority.
func ComputeExternalHomeAccessBindingHash(binding ExternalHomeAccessBinding) (string, error) {
	clone, err := cloneObject(map[string]any(binding), false)
	if err != nil {
		return "", fmt.Errorf("clone external Home access binding: %w", err)
	}
	delete(clone, "bindingHash")
	return canonicalHash(clone, false)
}

func buildHomeAccessProjection(spec *specView, specHash string, sites, nodes, capabilities, modules []any) (map[string]any, map[string]any, error) {
	requirements, err := buildHomeAccessRequirements(spec.stackID, specHash, sites, nodes, capabilities, modules)
	if err != nil {
		return nil, nil, err
	}
	bindings, err := projectExternalHomeAccessBindings(spec.originalInventory, requirements)
	if err != nil {
		return nil, nil, err
	}
	return requirements, bindings, nil
}

func buildHomeAccessRequirements(stackID, specHash string, sites, nodes, capabilities, modules []any) (map[string]any, error) {
	siteKinds := map[string]string{}
	for index, value := range sites {
		site, err := asObject(value, fmt.Sprintf("resolvedPlan.sites[%d]", index))
		if err != nil {
			return nil, err
		}
		id, err := stringField(site, fmt.Sprintf("resolvedPlan.sites[%d]", index), "id")
		if err != nil {
			return nil, err
		}
		kind, err := stringField(site, fmt.Sprintf("resolvedPlan.sites[%d]", index), "kind")
		if err != nil {
			return nil, err
		}
		siteKinds[id] = kind
	}
	nodeSites := map[string]string{}
	for index, value := range nodes {
		node, err := asObject(value, fmt.Sprintf("resolvedPlan.nodes[%d]", index))
		if err != nil {
			return nil, err
		}
		id, err := stringField(node, fmt.Sprintf("resolvedPlan.nodes[%d]", index), "id")
		if err != nil {
			return nil, err
		}
		siteRef, err := stringField(node, fmt.Sprintf("resolvedPlan.nodes[%d]", index), "siteRef")
		if err != nil {
			return nil, err
		}
		nodeSites[id] = siteRef
	}
	selectedCapabilities := map[string]map[string]any{}
	for index, value := range capabilities {
		capability, err := asObject(value, fmt.Sprintf("resolvedPlan.capabilities[%d]", index))
		if err != nil {
			return nil, err
		}
		id, err := stringField(capability, fmt.Sprintf("resolvedPlan.capabilities[%d]", index), "id")
		if err != nil {
			return nil, err
		}
		if slices.Contains(homeAccessCapabilities, id) {
			selectedCapabilities[id] = capability
		}
	}
	result := map[string]any{}
	seenCapability := map[string]bool{}
	for index, value := range modules {
		modulePath := fmt.Sprintf("resolvedPlan.modules[%d]", index)
		module, err := asObject(value, modulePath)
		if err != nil {
			return nil, err
		}
		provides, err := stringListField(module, modulePath, "provides", false)
		if err != nil {
			return nil, err
		}
		capabilityRef := ""
		for _, candidate := range homeAccessCapabilities {
			if slices.Contains(provides, candidate) {
				capabilityRef = candidate
				break
			}
		}
		if capabilityRef == "" {
			continue
		}
		if seenCapability[capabilityRef] {
			return nil, fail(ErrContractConflict, modulePath+".provides", "Home access capability %q has multiple runtime modules", capabilityRef)
		}
		seenCapability[capabilityRef] = true
		capability, selected := selectedCapabilities[capabilityRef]
		if !selected {
			return nil, fail(ErrContractConflict, modulePath+".provides", "Home access module provides an unselected capability %q", capabilityRef)
		}
		contractOwnerRef, err := stringField(capability, "resolvedPlan.capabilities."+capabilityRef, "providerRef")
		if err != nil {
			return nil, err
		}
		contractHash, err := stringField(capability, "resolvedPlan.capabilities."+capabilityRef, "contractHash")
		if err != nil {
			return nil, err
		}
		siteRefs, err := stringListField(module, modulePath, "siteRefs", true)
		if err != nil {
			return nil, err
		}
		nodeRefs, err := stringListField(module, modulePath, "nodeRefs", true)
		if err != nil {
			return nil, err
		}
		sort.Strings(nodeRefs)
		for _, siteRef := range siteRefs {
			if siteKinds[siteRef] != "home" {
				return nil, fail(ErrProfileMismatch, modulePath+".siteRefs", "Home access capability %q targets non-Home Site %q", capabilityRef, siteRef)
			}
			identityMode := "device-bound"
			if capabilityRef == "public-publish-egress" {
				identityMode = "service-identity"
			}
			targetNodeRefs := make([]string, 0, len(nodeRefs))
			for _, nodeRef := range nodeRefs {
				if nodeSites[nodeRef] == siteRef {
					targetNodeRefs = append(targetNodeRefs, nodeRef)
				}
			}
			if len(targetNodeRefs) == 0 {
				return nil, fail(ErrUnresolvedPlacement, modulePath+".nodeRefs", "Home access capability %q has no target node at Site %q", capabilityRef, siteRef)
			}
			requirement := HomeAccessRequirement{
				"apiVersion": homeAccessRequirementAPIVersion,
				"kind":       "HomeAccessRequirement",
				"stackId":    stackID, "siteRef": siteRef, "capabilityRef": capabilityRef,
				"contractOwnerRef": contractOwnerRef, "capabilityContractHash": contractHash,
				"targetNodeRefs": targetNodeRefs,
				"policy": map[string]any{
					"defaultDeny": true, "initiation": "home-outbound", "routeScope": "declared-services-only",
					"allowDefaultRoute": false, "allowBroadLAN": false, "identityMode": identityMode,
					"credentialCustody": "external", "fabricLifecycle": "external",
				},
				"specHash": specHash,
			}
			hash, err := ComputeHomeAccessRequirementHash(requirement)
			if err != nil {
				return nil, err
			}
			requirement["requirementsHash"] = hash
			byCapability, _ := result[siteRef].(map[string]any)
			if byCapability == nil {
				byCapability = map[string]any{}
				result[siteRef] = byCapability
			}
			byCapability[capabilityRef] = map[string]any(requirement)
		}
	}
	for capabilityRef := range selectedCapabilities {
		if !seenCapability[capabilityRef] {
			return nil, fail(ErrUnrealizedCapability, "resolvedPlan.capabilities."+capabilityRef, "selected Home access capability has no exact runtime module")
		}
	}
	return result, nil
}

func projectExternalHomeAccessBindings(inventory, requirements map[string]any) (map[string]any, error) {
	raw, exists := inventory["externalHomeAccessBindings"]
	if !exists || raw == nil {
		return map[string]any{}, nil
	}
	sites, err := asObject(raw, "inventory.externalHomeAccessBindings")
	if err != nil {
		return nil, err
	}
	result := map[string]any{}
	for _, siteRef := range sortedStringMapKeys(sites) {
		capabilityBindings, err := asObject(sites[siteRef], "inventory.externalHomeAccessBindings."+siteRef)
		if err != nil {
			return nil, err
		}
		for _, capabilityRef := range sortedStringMapKeys(capabilityBindings) {
			requirement, err := homeAccessRequirementAt(requirements, siteRef, capabilityRef)
			if err != nil {
				return nil, fail(ErrExternalHomeAccessBindingMismatch, "inventory.externalHomeAccessBindings."+siteRef+"."+capabilityRef, "binding has no exact selected Home access requirement")
			}
			binding, err := asObject(capabilityBindings[capabilityRef], "inventory.externalHomeAccessBindings."+siteRef+"."+capabilityRef)
			if err != nil {
				return nil, err
			}
			if err := validateExternalHomeAccessBinding(binding, requirement, "inventory.externalHomeAccessBindings."+siteRef+"."+capabilityRef); err != nil {
				return nil, err
			}
			clone, err := cloneObject(binding, false)
			if err != nil {
				return nil, err
			}
			byCapability, _ := result[siteRef].(map[string]any)
			if byCapability == nil {
				byCapability = map[string]any{}
				result[siteRef] = byCapability
			}
			byCapability[capabilityRef] = clone
		}
	}
	return result, nil
}

func homeAccessRequirementAt(requirements map[string]any, siteRef, capabilityRef string) (map[string]any, error) {
	byCapability, err := asObject(requirements[siteRef], "resolvedPlan.homeAccessRequirements."+siteRef)
	if err != nil {
		return nil, err
	}
	return asObject(byCapability[capabilityRef], "resolvedPlan.homeAccessRequirements."+siteRef+"."+capabilityRef)
}

func validateHomeAccessPlanProjection(plan ResolvedPlan) error {
	top := map[string]any(plan)
	stackID, err := stringField(top, "resolvedPlan", "stackId")
	if err != nil {
		return err
	}
	specHash, err := stringField(top, "resolvedPlan", "specHash")
	if err != nil {
		return err
	}
	sites, err := objectListField(top, "resolvedPlan", "sites")
	if err != nil {
		return err
	}
	nodes, err := objectListField(top, "resolvedPlan", "nodes")
	if err != nil {
		return err
	}
	capabilities, err := objectListField(top, "resolvedPlan", "capabilities")
	if err != nil {
		return err
	}
	modules, err := objectListField(top, "resolvedPlan", "modules")
	if err != nil {
		return err
	}
	wantRequirements, err := buildHomeAccessRequirements(
		stackID, specHash, objectMapsAsAny(sites), objectMapsAsAny(nodes), objectMapsAsAny(capabilities), objectMapsAsAny(modules),
	)
	if err != nil {
		return err
	}
	haveRequirements, err := objectField(top, "resolvedPlan", "homeAccessRequirements")
	if err != nil {
		return err
	}
	equal, err := canonicalEqual(haveRequirements, wantRequirements)
	if err != nil {
		return err
	}
	if !equal {
		return fail(ErrExternalHomeAccessBindingMismatch, "resolvedPlan.homeAccessRequirements", "projection is not exactly compiler-derived from the selected Home access modules")
	}
	bindings, err := objectField(top, "resolvedPlan", "externalHomeAccessBindings")
	if err != nil {
		return err
	}
	for _, siteRef := range sortedStringMapKeys(bindings) {
		capabilityBindings, err := asObject(bindings[siteRef], "resolvedPlan.externalHomeAccessBindings."+siteRef)
		if err != nil {
			return err
		}
		for _, capabilityRef := range sortedStringMapKeys(capabilityBindings) {
			requirement, err := homeAccessRequirementAt(wantRequirements, siteRef, capabilityRef)
			if err != nil {
				return fail(ErrExternalHomeAccessBindingMismatch, "resolvedPlan.externalHomeAccessBindings."+siteRef+"."+capabilityRef, "binding has no exact compiler-derived requirement")
			}
			binding, err := asObject(capabilityBindings[capabilityRef], "resolvedPlan.externalHomeAccessBindings."+siteRef+"."+capabilityRef)
			if err != nil {
				return err
			}
			if err := validateExternalHomeAccessBinding(binding, requirement, "resolvedPlan.externalHomeAccessBindings."+siteRef+"."+capabilityRef); err != nil {
				return err
			}
		}
	}
	return nil
}

func safeModuleHomeAccessProjection(moduleID string, module, projection map[string]any, required bool) (map[string]any, error) {
	provides, err := stringListField(module, "modules."+moduleID, "provides", false)
	if err != nil {
		return nil, err
	}
	capabilityRef := ""
	for _, candidate := range homeAccessCapabilities {
		if slices.Contains(provides, candidate) {
			capabilityRef = candidate
			break
		}
	}
	if capabilityRef == "" {
		return nil, fail(ErrContractConflict, "modules."+moduleID+".planInputRefs", "module requests a Home access projection without owning one exact Home access capability")
	}
	siteRefs, err := stringListField(module, "modules."+moduleID, "siteRefs", true)
	if err != nil {
		return nil, err
	}
	result := map[string]any{}
	for _, siteRef := range siteRefs {
		byCapability, err := asObject(projection[siteRef], "homeAccessProjection."+siteRef)
		if err != nil {
			if required {
				return nil, fail(ErrContractConflict, "modules."+moduleID+".planInputs", "required Home access projection is missing for Site %q", siteRef)
			}
			continue
		}
		value, exists := byCapability[capabilityRef]
		if !exists {
			if required {
				return nil, fail(ErrContractConflict, "modules."+moduleID+".planInputs", "required Home access projection is missing for capability %q", capabilityRef)
			}
			continue
		}
		object, err := asObject(value, "homeAccessProjection."+siteRef+"."+capabilityRef)
		if err != nil {
			return nil, err
		}
		clone, err := cloneObject(object, false)
		if err != nil {
			return nil, err
		}
		result[siteRef] = map[string]any{capabilityRef: clone}
	}
	return result, nil
}

func validateExternalHomeAccessBinding(binding, requirement map[string]any, path string) error {
	allowed := map[string]struct{}{
		"apiVersion": {}, "kind": {}, "bindingRef": {}, "stackId": {}, "siteRef": {}, "capabilityRef": {},
		"contractOwnerRef": {}, "capabilityContractHash": {}, "requirementsHash": {}, "accessFabricRef": {},
		"stackkitsVersion": {}, "candidateDigest": {}, "specHash": {}, "issuedAt": {}, "validUntil": {}, "bindingHash": {},
	}
	for key := range binding {
		if _, ok := allowed[key]; !ok {
			return fail(ErrExternalHomeAccessBindingMismatch, path+"."+key, "field is outside the closed external Home access binding contract")
		}
	}
	if binding["apiVersion"] != externalHomeAccessAPIVersion || binding["kind"] != "ExternalHomeAccessBinding" {
		return fail(ErrExternalHomeAccessBindingMismatch, path+".apiVersion", "unsupported external Home access binding contract")
	}
	bindingRef, err := stringField(binding, path, "bindingRef")
	if err != nil || !homeAccessBindingRefPattern.MatchString(bindingRef) {
		return fail(ErrExternalHomeAccessBindingMismatch, path+".bindingRef", "must be an opaque home-access-binding sha256 reference")
	}
	fabricRef, err := stringField(binding, path, "accessFabricRef")
	if err != nil || !homeAccessFabricRefPattern.MatchString(fabricRef) {
		return fail(ErrExternalHomeAccessBindingMismatch, path+".accessFabricRef", "must be an opaque home-access-fabric sha256 reference")
	}
	for _, field := range []string{"stackId", "siteRef", "capabilityRef", "contractOwnerRef", "capabilityContractHash", "requirementsHash", "specHash"} {
		have, err := stringField(binding, path, field)
		if err != nil {
			return err
		}
		want, err := stringField(requirement, "resolvedPlan.homeAccessRequirement", field)
		if err != nil {
			return err
		}
		if have != want {
			return fail(ErrExternalHomeAccessBindingMismatch, path+"."+field, "binding value %q does not match exact requirement value %q", have, want)
		}
	}
	if _, err := stringField(binding, path, "stackkitsVersion"); err != nil {
		return err
	}
	for _, field := range []string{"candidateDigest", "bindingHash"} {
		value, err := stringField(binding, path, field)
		if err != nil || !externalHostContentHashPattern.MatchString(value) {
			return fail(ErrExternalHomeAccessBindingMismatch, path+"."+field, "must be a sha256 content hash")
		}
	}
	issuedAt, err := externalHostTimestamp(binding, path, "issuedAt")
	if err != nil {
		return err
	}
	validUntil, err := externalHostTimestamp(binding, path, "validUntil")
	if err != nil {
		return err
	}
	if !issuedAt.Before(validUntil) {
		return fail(ErrExternalHomeAccessBindingMismatch, path+".validUntil", "must be after issuedAt")
	}
	if validUntil.Sub(issuedAt) > maxExternalHomeAccessBindingValidity {
		return fail(ErrExternalHomeAccessBindingMismatch, path+".validUntil", "validity must not exceed %s", maxExternalHomeAccessBindingValidity)
	}
	wantHash, err := ComputeExternalHomeAccessBindingHash(ExternalHomeAccessBinding(binding))
	if err != nil {
		return err
	}
	if binding["bindingHash"] != wantHash {
		return fail(ErrExternalHomeAccessBindingMismatch, path+".bindingHash", "declared binding hash does not match canonical binding body")
	}
	return nil
}

// ValidateExternalHomeAccessBindingsFreshness revalidates exact requirement
// parity and the externally issued validity window at execution time.
func ValidateExternalHomeAccessBindingsFreshness(plan ResolvedPlan, at time.Time) error {
	if at.IsZero() {
		return fail(ErrInvalidInput, "resolvedPlan.externalHomeAccessBindings", "execution time is required")
	}
	planObject := map[string]any(plan)
	requirementsRaw, hasRequirements := planObject["homeAccessRequirements"]
	bindingsRaw, hasBindings := planObject["externalHomeAccessBindings"]
	if !hasRequirements && !hasBindings {
		return nil
	}
	if !hasRequirements || !hasBindings {
		return fail(ErrExternalHomeAccessBindingMismatch, "resolvedPlan.externalHomeAccessBindings", "Home access requirement and binding projections must appear together")
	}
	requirements, err := asObject(requirementsRaw, "resolvedPlan.homeAccessRequirements")
	if err != nil {
		return err
	}
	bindings, err := asObject(bindingsRaw, "resolvedPlan.externalHomeAccessBindings")
	if err != nil {
		return err
	}
	for _, siteRef := range sortedStringMapKeys(bindings) {
		capabilityBindings, err := asObject(bindings[siteRef], "resolvedPlan.externalHomeAccessBindings."+siteRef)
		if err != nil {
			return err
		}
		for _, capabilityRef := range sortedStringMapKeys(capabilityBindings) {
			requirement, err := homeAccessRequirementAt(requirements, siteRef, capabilityRef)
			if err != nil {
				return fail(ErrExternalHomeAccessBindingMismatch, "resolvedPlan.externalHomeAccessBindings."+siteRef+"."+capabilityRef, "binding has no exact requirement")
			}
			binding, err := asObject(capabilityBindings[capabilityRef], "resolvedPlan.externalHomeAccessBindings."+siteRef+"."+capabilityRef)
			if err != nil {
				return err
			}
			path := "resolvedPlan.externalHomeAccessBindings." + siteRef + "." + capabilityRef
			if err := validateExternalHomeAccessBinding(binding, requirement, path); err != nil {
				return err
			}
			issuedAt, err := externalHostTimestamp(binding, path, "issuedAt")
			if err != nil {
				return err
			}
			validUntil, err := externalHostTimestamp(binding, path, "validUntil")
			if err != nil {
				return err
			}
			if at.Before(issuedAt) || !at.Before(validUntil) {
				return fail(ErrExternalHomeAccessBindingStale, path+".validUntil", "binding is outside its validity window")
			}
		}
	}
	return nil
}
