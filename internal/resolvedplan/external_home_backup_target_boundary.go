package resolvedplan

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"time"
)

const (
	homeBackupRequirementAPIVersion = "stackkit.home-backup-target-requirement/v1"
	externalHomeBackupAPIVersion    = "stackkit.external-home-backup-target-binding/v1"
	homeBackupCapability            = "encrypted-offsite-backup"
	maxExternalHomeBackupValidity   = 24 * time.Hour
)

var (
	homeBackupBindingRefPattern = regexp.MustCompile(`^home-backup-target-binding://sha256/[a-f0-9]{64}$`)
	homeBackupTargetRefPattern  = regexp.MustCompile(`^home-backup-target://sha256/[a-f0-9]{64}$`)
	homeBackupCustodyRefPattern = regexp.MustCompile(`^home-backup-custody-attestation://sha256/[a-f0-9]{64}$`)
)

func ComputeHomeBackupTargetRequirementHash(requirement HomeBackupTargetRequirement) (string, error) {
	clone, err := cloneObject(map[string]any(requirement), false)
	if err != nil {
		return "", fmt.Errorf("clone Home backup target requirement: %w", err)
	}
	delete(clone, "requirementsHash")
	return canonicalHash(clone, false)
}

func ComputeExternalHomeBackupTargetBindingHash(binding ExternalHomeBackupTargetBinding) (string, error) {
	clone, err := cloneObject(map[string]any(binding), false)
	if err != nil {
		return "", fmt.Errorf("clone external Home backup target binding: %w", err)
	}
	delete(clone, "bindingHash")
	return canonicalHash(clone, false)
}

func buildHomeBackupTargetProjection(spec *specView, specHash string, sites, nodes, capabilities, modules []any) (map[string]any, map[string]any, error) {
	requirements, err := buildHomeBackupTargetRequirements(spec.stackID, specHash, sites, nodes, capabilities, modules)
	if err != nil {
		return nil, nil, err
	}
	bindings, err := projectExternalHomeBackupTargetBindings(spec.originalInventory, requirements)
	if err != nil {
		return nil, nil, err
	}
	return requirements, bindings, nil
}

func buildHomeBackupTargetRequirements(stackID, specHash string, sites, nodes, capabilities, modules []any) (map[string]any, error) {
	siteKinds := map[string]string{}
	for index, raw := range sites {
		site, err := asObject(raw, fmt.Sprintf("resolvedPlan.sites[%d]", index))
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
	for index, raw := range nodes {
		node, err := asObject(raw, fmt.Sprintf("resolvedPlan.nodes[%d]", index))
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
	var capability map[string]any
	for index, raw := range capabilities {
		candidate, err := asObject(raw, fmt.Sprintf("resolvedPlan.capabilities[%d]", index))
		if err != nil {
			return nil, err
		}
		id, err := stringField(candidate, fmt.Sprintf("resolvedPlan.capabilities[%d]", index), "id")
		if err != nil {
			return nil, err
		}
		if id == homeBackupCapability {
			capability = candidate
		}
	}
	result := map[string]any{}
	seen := false
	for index, raw := range modules {
		path := fmt.Sprintf("resolvedPlan.modules[%d]", index)
		module, err := asObject(raw, path)
		if err != nil {
			return nil, err
		}
		provides, err := stringListField(module, path, "provides", false)
		if err != nil {
			return nil, err
		}
		if !slices.Contains(provides, homeBackupCapability) {
			continue
		}
		if seen {
			return nil, fail(ErrContractConflict, path+".provides", "Home backup capability has multiple runtime modules")
		}
		seen = true
		if capability == nil {
			return nil, fail(ErrContractConflict, path+".provides", "Home backup module provides an unselected capability")
		}
		ownerRef, err := stringField(capability, "resolvedPlan.capabilities."+homeBackupCapability, "providerRef")
		if err != nil {
			return nil, err
		}
		contractHash, err := stringField(capability, "resolvedPlan.capabilities."+homeBackupCapability, "contractHash")
		if err != nil {
			return nil, err
		}
		siteRefs, err := stringListField(module, path, "siteRefs", true)
		if err != nil {
			return nil, err
		}
		nodeRefs, err := stringListField(module, path, "nodeRefs", true)
		if err != nil {
			return nil, err
		}
		sort.Strings(nodeRefs)
		for _, siteRef := range siteRefs {
			if siteKinds[siteRef] != "home" {
				return nil, fail(ErrProfileMismatch, path+".siteRefs", "Home backup capability targets non-Home Site %q", siteRef)
			}
			targetNodeRefs := make([]string, 0, len(nodeRefs))
			for _, nodeRef := range nodeRefs {
				if nodeSites[nodeRef] == siteRef {
					targetNodeRefs = append(targetNodeRefs, nodeRef)
				}
			}
			if len(targetNodeRefs) == 0 {
				return nil, fail(ErrUnresolvedPlacement, path+".nodeRefs", "Home backup capability has no target node at Site %q", siteRef)
			}
			requirement := HomeBackupTargetRequirement{
				"apiVersion": homeBackupRequirementAPIVersion, "kind": "HomeBackupTargetRequirement",
				"stackId": stackID, "siteRef": siteRef, "capabilityRef": homeBackupCapability,
				"contractOwnerRef": ownerRef, "capabilityContractHash": contractHash, "targetNodeRefs": targetNodeRefs,
				"policy": map[string]any{
					"scope": "governed-home-data-only", "encryptionRequired": true, "encryptionAuthority": "home",
					"plaintextEgressAllowed": false, "credentialCustody": "external", "targetLifecycle": "external",
					"restoreVerificationRequired": true, "providerSelection": "external",
				},
				"specHash": specHash,
			}
			hash, err := ComputeHomeBackupTargetRequirementHash(requirement)
			if err != nil {
				return nil, err
			}
			requirement["requirementsHash"] = hash
			result[siteRef] = map[string]any{homeBackupCapability: map[string]any(requirement)}
		}
	}
	if capability != nil && !seen {
		return nil, fail(ErrUnrealizedCapability, "resolvedPlan.capabilities."+homeBackupCapability, "selected Home backup capability has no exact runtime module")
	}
	return result, nil
}

func projectExternalHomeBackupTargetBindings(inventory, requirements map[string]any) (map[string]any, error) {
	raw, exists := inventory["externalHomeBackupTargetBindings"]
	if !exists || raw == nil {
		return map[string]any{}, nil
	}
	sites, err := asObject(raw, "inventory.externalHomeBackupTargetBindings")
	if err != nil {
		return nil, err
	}
	result := map[string]any{}
	for _, siteRef := range sortedStringMapKeys(sites) {
		capabilityBindings, err := asObject(sites[siteRef], "inventory.externalHomeBackupTargetBindings."+siteRef)
		if err != nil {
			return nil, err
		}
		for _, capabilityRef := range sortedStringMapKeys(capabilityBindings) {
			requirement, err := homeBackupTargetRequirementAt(requirements, siteRef, capabilityRef)
			if err != nil {
				return nil, fail(ErrExternalHomeBackupTargetBindingMismatch, "inventory.externalHomeBackupTargetBindings."+siteRef+"."+capabilityRef, "binding has no exact selected Home backup target requirement")
			}
			path := "inventory.externalHomeBackupTargetBindings." + siteRef + "." + capabilityRef
			binding, err := asObject(capabilityBindings[capabilityRef], path)
			if err != nil {
				return nil, err
			}
			if err := validateExternalHomeBackupTargetBinding(binding, requirement, path); err != nil {
				return nil, err
			}
			clone, err := cloneObject(binding, false)
			if err != nil {
				return nil, err
			}
			result[siteRef] = map[string]any{capabilityRef: clone}
		}
	}
	return result, nil
}

func homeBackupTargetRequirementAt(requirements map[string]any, siteRef, capabilityRef string) (map[string]any, error) {
	byCapability, err := asObject(requirements[siteRef], "resolvedPlan.homeBackupTargetRequirements."+siteRef)
	if err != nil {
		return nil, err
	}
	return asObject(byCapability[capabilityRef], "resolvedPlan.homeBackupTargetRequirements."+siteRef+"."+capabilityRef)
}

func safeModuleHomeBackupTargetProjection(moduleID string, module, projection map[string]any, required bool) (map[string]any, error) {
	provides, err := stringListField(module, "modules."+moduleID, "provides", false)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(provides, homeBackupCapability) {
		return nil, fail(ErrContractConflict, "modules."+moduleID+".planInputRefs", "module requests a Home backup target projection without owning %q", homeBackupCapability)
	}
	siteRefs, err := stringListField(module, "modules."+moduleID, "siteRefs", true)
	if err != nil {
		return nil, err
	}
	result := map[string]any{}
	for _, siteRef := range siteRefs {
		byCapability, err := asObject(projection[siteRef], "homeBackupTargetProjection."+siteRef)
		if err != nil {
			if required {
				return nil, fail(ErrContractConflict, "modules."+moduleID+".planInputs", "required Home backup target projection is missing for Site %q", siteRef)
			}
			continue
		}
		value, exists := byCapability[homeBackupCapability]
		if !exists {
			if required {
				return nil, fail(ErrContractConflict, "modules."+moduleID+".planInputs", "required Home backup target projection is missing")
			}
			continue
		}
		object, err := asObject(value, "homeBackupTargetProjection."+siteRef+"."+homeBackupCapability)
		if err != nil {
			return nil, err
		}
		clone, err := cloneObject(object, false)
		if err != nil {
			return nil, err
		}
		result[siteRef] = map[string]any{homeBackupCapability: clone}
	}
	return result, nil
}

func validateExternalHomeBackupTargetBinding(binding, requirement map[string]any, path string) error {
	allowed := map[string]struct{}{
		"apiVersion": {}, "kind": {}, "bindingRef": {}, "backupTargetRef": {}, "custodyAttestationRef": {},
		"stackId": {}, "siteRef": {}, "capabilityRef": {}, "contractOwnerRef": {}, "capabilityContractHash": {},
		"requirementsHash": {}, "stackkitsVersion": {}, "candidateDigest": {}, "specHash": {}, "issuedAt": {}, "validUntil": {}, "bindingHash": {},
	}
	for key := range binding {
		if _, ok := allowed[key]; !ok {
			return fail(ErrExternalHomeBackupTargetBindingMismatch, path+"."+key, "field is outside the closed external Home backup target binding contract")
		}
	}
	if binding["apiVersion"] != externalHomeBackupAPIVersion || binding["kind"] != "ExternalHomeBackupTargetBinding" {
		return fail(ErrExternalHomeBackupTargetBindingMismatch, path+".apiVersion", "unsupported external Home backup target binding contract")
	}
	refs := []struct {
		field string
		match *regexp.Regexp
	}{{"bindingRef", homeBackupBindingRefPattern}, {"backupTargetRef", homeBackupTargetRefPattern}, {"custodyAttestationRef", homeBackupCustodyRefPattern}}
	for _, ref := range refs {
		value, err := stringField(binding, path, ref.field)
		if err != nil || !ref.match.MatchString(value) {
			return fail(ErrExternalHomeBackupTargetBindingMismatch, path+"."+ref.field, "must be an opaque sha256 reference")
		}
	}
	for _, field := range []string{"stackId", "siteRef", "capabilityRef", "contractOwnerRef", "capabilityContractHash", "requirementsHash", "specHash"} {
		have, err := stringField(binding, path, field)
		if err != nil {
			return err
		}
		want, err := stringField(requirement, "resolvedPlan.homeBackupTargetRequirement", field)
		if err != nil {
			return err
		}
		if have != want {
			return fail(ErrExternalHomeBackupTargetBindingMismatch, path+"."+field, "binding does not match exact Home backup target requirement")
		}
	}
	version, err := stringField(binding, path, "stackkitsVersion")
	if err != nil || !externalHostSemVerPattern.MatchString(version) {
		return fail(ErrExternalHomeBackupTargetBindingMismatch, path+".stackkitsVersion", "must be a semantic version")
	}
	for _, field := range []string{"candidateDigest", "bindingHash"} {
		value, err := stringField(binding, path, field)
		if err != nil || !externalHostContentHashPattern.MatchString(value) {
			return fail(ErrExternalHomeBackupTargetBindingMismatch, path+"."+field, "must be a sha256 content hash")
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
	if !issuedAt.Before(validUntil) || validUntil.Sub(issuedAt) > maxExternalHomeBackupValidity {
		return fail(ErrExternalHomeBackupTargetBindingMismatch, path+".validUntil", "must be after issuedAt with validity no greater than %s", maxExternalHomeBackupValidity)
	}
	wantHash, err := ComputeExternalHomeBackupTargetBindingHash(ExternalHomeBackupTargetBinding(binding))
	if err != nil {
		return err
	}
	if binding["bindingHash"] != wantHash {
		return fail(ErrExternalHomeBackupTargetBindingMismatch, path+".bindingHash", "declared binding hash does not match canonical binding body")
	}
	return nil
}

func validateHomeBackupTargetPlanProjection(plan ResolvedPlan) error {
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
	want, err := buildHomeBackupTargetRequirements(stackID, specHash, objectMapsAsAny(sites), objectMapsAsAny(nodes), objectMapsAsAny(capabilities), objectMapsAsAny(modules))
	if err != nil {
		return err
	}
	have, err := objectField(top, "resolvedPlan", "homeBackupTargetRequirements")
	if err != nil {
		return err
	}
	equal, err := canonicalEqual(have, want)
	if err != nil {
		return err
	}
	if !equal {
		return fail(ErrExternalHomeBackupTargetBindingMismatch, "resolvedPlan.homeBackupTargetRequirements", "projection is not exactly compiler-derived")
	}
	bindings, err := objectField(top, "resolvedPlan", "externalHomeBackupTargetBindings")
	if err != nil {
		return err
	}
	for _, siteRef := range sortedStringMapKeys(bindings) {
		capabilityBindings, err := asObject(bindings[siteRef], "resolvedPlan.externalHomeBackupTargetBindings."+siteRef)
		if err != nil {
			return err
		}
		for _, capabilityRef := range sortedStringMapKeys(capabilityBindings) {
			requirement, err := homeBackupTargetRequirementAt(want, siteRef, capabilityRef)
			if err != nil {
				return fail(ErrExternalHomeBackupTargetBindingMismatch, "resolvedPlan.externalHomeBackupTargetBindings."+siteRef+"."+capabilityRef, "binding has no exact compiler-derived requirement")
			}
			binding, err := asObject(capabilityBindings[capabilityRef], "resolvedPlan.externalHomeBackupTargetBindings."+siteRef+"."+capabilityRef)
			if err != nil {
				return err
			}
			if err := validateExternalHomeBackupTargetBinding(binding, requirement, "resolvedPlan.externalHomeBackupTargetBindings."+siteRef+"."+capabilityRef); err != nil {
				return err
			}
		}
	}
	return nil
}

func ValidateExternalHomeBackupTargetBindingsFreshness(plan ResolvedPlan, at time.Time) error {
	if at.IsZero() {
		return fail(ErrInvalidInput, "resolvedPlan.externalHomeBackupTargetBindings", "execution time is required")
	}
	top := map[string]any(plan)
	requirementsRaw, hasRequirements := top["homeBackupTargetRequirements"]
	bindingsRaw, hasBindings := top["externalHomeBackupTargetBindings"]
	if !hasRequirements && !hasBindings {
		return nil
	}
	if !hasRequirements || !hasBindings {
		return fail(ErrExternalHomeBackupTargetBindingMismatch, "resolvedPlan.externalHomeBackupTargetBindings", "Home backup target requirement and binding projections must appear together")
	}
	requirements, err := asObject(requirementsRaw, "resolvedPlan.homeBackupTargetRequirements")
	if err != nil {
		return err
	}
	bindings, err := asObject(bindingsRaw, "resolvedPlan.externalHomeBackupTargetBindings")
	if err != nil {
		return err
	}
	for _, siteRef := range sortedStringMapKeys(bindings) {
		capabilityBindings, err := asObject(bindings[siteRef], "resolvedPlan.externalHomeBackupTargetBindings."+siteRef)
		if err != nil {
			return err
		}
		for _, capabilityRef := range sortedStringMapKeys(capabilityBindings) {
			requirement, err := homeBackupTargetRequirementAt(requirements, siteRef, capabilityRef)
			if err != nil {
				return fail(ErrExternalHomeBackupTargetBindingMismatch, "resolvedPlan.externalHomeBackupTargetBindings."+siteRef+"."+capabilityRef, "binding has no exact requirement")
			}
			path := "resolvedPlan.externalHomeBackupTargetBindings." + siteRef + "." + capabilityRef
			binding, err := asObject(capabilityBindings[capabilityRef], path)
			if err != nil {
				return err
			}
			if err := validateExternalHomeBackupTargetBinding(binding, requirement, path); err != nil {
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
				return fail(ErrExternalHomeBackupTargetBindingStale, path+".validUntil", "binding is outside its validity window")
			}
		}
	}
	return nil
}
