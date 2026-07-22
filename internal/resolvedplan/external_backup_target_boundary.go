package resolvedplan

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"time"
)

const (
	backupTargetRequirementAPIVersion = "stackkit.backup-target-requirement/v1"
	externalBackupTargetAPIVersion    = "stackkit.external-backup-target-binding/v1"
	backupTargetCapability            = "offsite-object-backup"
	maxExternalBackupBindingValidity  = 24 * time.Hour
)

var (
	backupTargetBindingRefPattern = regexp.MustCompile(`^backup-target-binding://sha256/[a-f0-9]{64}$`)
	backupTargetRefPattern        = regexp.MustCompile(`^backup-target://sha256/[a-f0-9]{64}$`)
	backupCustodyRefPattern       = regexp.MustCompile(`^backup-custody-attestation://sha256/[a-f0-9]{64}$`)
)

func ComputeBackupTargetRequirementHash(requirement BackupTargetRequirement) (string, error) {
	clone, err := cloneObject(map[string]any(requirement), false)
	if err != nil {
		return "", fmt.Errorf("clone backup target requirement: %w", err)
	}
	delete(clone, "requirementsHash")
	return canonicalHash(clone, false)
}

func ComputeExternalBackupTargetBindingHash(binding ExternalBackupTargetBinding) (string, error) {
	clone, err := cloneObject(map[string]any(binding), false)
	if err != nil {
		return "", fmt.Errorf("clone external backup target binding: %w", err)
	}
	delete(clone, "bindingHash")
	return canonicalHash(clone, false)
}

func buildBackupTargetProjection(spec *specView, specHash string, sites, nodes, capabilities, modules []any) (map[string]any, map[string]any, error) {
	requirements, err := buildBackupTargetRequirements(spec.stackID, specHash, sites, nodes, capabilities, modules)
	if err != nil {
		return nil, nil, err
	}
	bindings, err := projectExternalBackupTargetBindings(spec.originalInventory, requirements)
	if err != nil {
		return nil, nil, err
	}
	return requirements, bindings, nil
}

func buildBackupTargetRequirements(stackID, specHash string, sites, nodes, capabilities, modules []any) (map[string]any, error) {
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
		if id == backupTargetCapability {
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
		if !slices.Contains(provides, backupTargetCapability) {
			continue
		}
		if seen {
			return nil, fail(ErrContractConflict, path+".provides", "Cloud backup capability has multiple runtime modules")
		}
		seen = true
		if capability == nil {
			return nil, fail(ErrContractConflict, path+".provides", "Cloud backup module provides an unselected capability")
		}
		ownerRef, err := stringField(capability, "resolvedPlan.capabilities."+backupTargetCapability, "providerRef")
		if err != nil {
			return nil, err
		}
		contractHash, err := stringField(capability, "resolvedPlan.capabilities."+backupTargetCapability, "contractHash")
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
			if siteKinds[siteRef] != "cloud" {
				return nil, fail(ErrProfileMismatch, path+".siteRefs", "Cloud backup capability targets non-Cloud Site %q", siteRef)
			}
			targetNodeRefs := make([]string, 0, len(nodeRefs))
			for _, nodeRef := range nodeRefs {
				if nodeSites[nodeRef] == siteRef {
					targetNodeRefs = append(targetNodeRefs, nodeRef)
				}
			}
			if len(targetNodeRefs) == 0 {
				return nil, fail(ErrUnresolvedPlacement, path+".nodeRefs", "Cloud backup capability has no target node at Site %q", siteRef)
			}
			requirement := BackupTargetRequirement{
				"apiVersion": backupTargetRequirementAPIVersion, "kind": "BackupTargetRequirement",
				"stackId": stackID, "siteRef": siteRef, "capabilityRef": backupTargetCapability,
				"contractOwnerRef": ownerRef, "capabilityContractHash": contractHash,
				"targetNodeRefs": targetNodeRefs,
				"policy": map[string]any{
					"scope": "governed-data-only", "encryptionRequired": true, "credentialCustody": "external",
					"targetLifecycle": "external", "restoreVerificationRequired": true, "providerSelection": "external",
				},
				"specHash": specHash,
			}
			hash, err := ComputeBackupTargetRequirementHash(requirement)
			if err != nil {
				return nil, err
			}
			requirement["requirementsHash"] = hash
			result[siteRef] = map[string]any{backupTargetCapability: map[string]any(requirement)}
		}
	}
	if capability != nil && !seen {
		return nil, fail(ErrUnrealizedCapability, "resolvedPlan.capabilities."+backupTargetCapability, "selected Cloud backup capability has no exact runtime module")
	}
	return result, nil
}

func projectExternalBackupTargetBindings(inventory, requirements map[string]any) (map[string]any, error) {
	raw, exists := inventory["externalBackupTargetBindings"]
	if !exists || raw == nil {
		return map[string]any{}, nil
	}
	sites, err := asObject(raw, "inventory.externalBackupTargetBindings")
	if err != nil {
		return nil, err
	}
	result := map[string]any{}
	for _, siteRef := range sortedStringMapKeys(sites) {
		capabilityBindings, err := asObject(sites[siteRef], "inventory.externalBackupTargetBindings."+siteRef)
		if err != nil {
			return nil, err
		}
		for _, capabilityRef := range sortedStringMapKeys(capabilityBindings) {
			requirement, err := backupTargetRequirementAt(requirements, siteRef, capabilityRef)
			if err != nil {
				return nil, fail(ErrExternalBackupTargetBindingMismatch, "inventory.externalBackupTargetBindings."+siteRef+"."+capabilityRef, "binding has no exact selected backup target requirement")
			}
			binding, err := asObject(capabilityBindings[capabilityRef], "inventory.externalBackupTargetBindings."+siteRef+"."+capabilityRef)
			if err != nil {
				return nil, err
			}
			if err := validateExternalBackupTargetBinding(binding, requirement, "inventory.externalBackupTargetBindings."+siteRef+"."+capabilityRef); err != nil {
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

func backupTargetRequirementAt(requirements map[string]any, siteRef, capabilityRef string) (map[string]any, error) {
	byCapability, err := asObject(requirements[siteRef], "resolvedPlan.backupTargetRequirements."+siteRef)
	if err != nil {
		return nil, err
	}
	return asObject(byCapability[capabilityRef], "resolvedPlan.backupTargetRequirements."+siteRef+"."+capabilityRef)
}

func safeModuleBackupTargetProjection(moduleID string, module, projection map[string]any, required bool) (map[string]any, error) {
	provides, err := stringListField(module, "modules."+moduleID, "provides", false)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(provides, backupTargetCapability) {
		return nil, fail(ErrContractConflict, "modules."+moduleID+".planInputRefs", "module requests a backup target projection without owning %q", backupTargetCapability)
	}
	siteRefs, err := stringListField(module, "modules."+moduleID, "siteRefs", true)
	if err != nil {
		return nil, err
	}
	result := map[string]any{}
	for _, siteRef := range siteRefs {
		byCapability, err := asObject(projection[siteRef], "backupTargetProjection."+siteRef)
		if err != nil {
			if required {
				return nil, fail(ErrContractConflict, "modules."+moduleID+".planInputs", "required backup target projection is missing for Site %q", siteRef)
			}
			continue
		}
		value, exists := byCapability[backupTargetCapability]
		if !exists {
			if required {
				return nil, fail(ErrContractConflict, "modules."+moduleID+".planInputs", "required backup target projection is missing")
			}
			continue
		}
		object, err := asObject(value, "backupTargetProjection."+siteRef+"."+backupTargetCapability)
		if err != nil {
			return nil, err
		}
		clone, err := cloneObject(object, false)
		if err != nil {
			return nil, err
		}
		result[siteRef] = map[string]any{backupTargetCapability: clone}
	}
	return result, nil
}

func validateExternalBackupTargetBinding(binding, requirement map[string]any, path string) error {
	allowed := map[string]struct{}{
		"apiVersion": {}, "kind": {}, "bindingRef": {}, "backupTargetRef": {}, "custodyAttestationRef": {},
		"stackId": {}, "siteRef": {}, "capabilityRef": {}, "contractOwnerRef": {}, "capabilityContractHash": {},
		"requirementsHash": {}, "stackkitsVersion": {}, "candidateDigest": {}, "specHash": {}, "issuedAt": {}, "validUntil": {}, "bindingHash": {},
	}
	for key := range binding {
		if _, ok := allowed[key]; !ok {
			return fail(ErrExternalBackupTargetBindingMismatch, path+"."+key, "field is outside the closed external backup target binding contract")
		}
	}
	if binding["apiVersion"] != externalBackupTargetAPIVersion || binding["kind"] != "ExternalBackupTargetBinding" {
		return fail(ErrExternalBackupTargetBindingMismatch, path+".apiVersion", "unsupported external backup target binding contract")
	}
	refs := []struct {
		field string
		match *regexp.Regexp
	}{{"bindingRef", backupTargetBindingRefPattern}, {"backupTargetRef", backupTargetRefPattern}, {"custodyAttestationRef", backupCustodyRefPattern}}
	for _, ref := range refs {
		value, err := stringField(binding, path, ref.field)
		if err != nil || !ref.match.MatchString(value) {
			return fail(ErrExternalBackupTargetBindingMismatch, path+"."+ref.field, "must be an opaque sha256 reference")
		}
	}
	for _, field := range []string{"stackId", "siteRef", "capabilityRef", "contractOwnerRef", "capabilityContractHash", "requirementsHash", "specHash"} {
		have, err := stringField(binding, path, field)
		if err != nil {
			return err
		}
		want, err := stringField(requirement, "resolvedPlan.backupTargetRequirement", field)
		if err != nil {
			return err
		}
		if have != want {
			return fail(ErrExternalBackupTargetBindingMismatch, path+"."+field, "binding does not match exact backup target requirement")
		}
	}
	version, err := stringField(binding, path, "stackkitsVersion")
	if err != nil || !externalHostSemVerPattern.MatchString(version) {
		return fail(ErrExternalBackupTargetBindingMismatch, path+".stackkitsVersion", "must be a semantic version")
	}
	for _, field := range []string{"candidateDigest", "bindingHash"} {
		value, err := stringField(binding, path, field)
		if err != nil || !externalHostContentHashPattern.MatchString(value) {
			return fail(ErrExternalBackupTargetBindingMismatch, path+"."+field, "must be a sha256 content hash")
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
	if !issuedAt.Before(validUntil) || validUntil.Sub(issuedAt) > maxExternalBackupBindingValidity {
		return fail(ErrExternalBackupTargetBindingMismatch, path+".validUntil", "must be after issuedAt with validity no greater than %s", maxExternalBackupBindingValidity)
	}
	wantHash, err := ComputeExternalBackupTargetBindingHash(ExternalBackupTargetBinding(binding))
	if err != nil {
		return err
	}
	if binding["bindingHash"] != wantHash {
		return fail(ErrExternalBackupTargetBindingMismatch, path+".bindingHash", "declared binding hash does not match canonical binding body")
	}
	return nil
}

func validateBackupTargetPlanProjection(plan ResolvedPlan) error {
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
	want, err := buildBackupTargetRequirements(stackID, specHash, objectMapsAsAny(sites), objectMapsAsAny(nodes), objectMapsAsAny(capabilities), objectMapsAsAny(modules))
	if err != nil {
		return err
	}
	have, err := objectField(top, "resolvedPlan", "backupTargetRequirements")
	if err != nil {
		return err
	}
	equal, err := canonicalEqual(have, want)
	if err != nil {
		return err
	}
	if !equal {
		return fail(ErrExternalBackupTargetBindingMismatch, "resolvedPlan.backupTargetRequirements", "projection is not exactly compiler-derived")
	}
	bindings, err := objectField(top, "resolvedPlan", "externalBackupTargetBindings")
	if err != nil {
		return err
	}
	for _, siteRef := range sortedStringMapKeys(bindings) {
		capabilityBindings, err := asObject(bindings[siteRef], "resolvedPlan.externalBackupTargetBindings."+siteRef)
		if err != nil {
			return err
		}
		for _, capabilityRef := range sortedStringMapKeys(capabilityBindings) {
			requirement, err := backupTargetRequirementAt(want, siteRef, capabilityRef)
			if err != nil {
				return fail(ErrExternalBackupTargetBindingMismatch, "resolvedPlan.externalBackupTargetBindings."+siteRef+"."+capabilityRef, "binding has no exact compiler-derived requirement")
			}
			binding, err := asObject(capabilityBindings[capabilityRef], "resolvedPlan.externalBackupTargetBindings."+siteRef+"."+capabilityRef)
			if err != nil {
				return err
			}
			if err := validateExternalBackupTargetBinding(binding, requirement, "resolvedPlan.externalBackupTargetBindings."+siteRef+"."+capabilityRef); err != nil {
				return err
			}
		}
	}
	return nil
}

func ValidateExternalBackupTargetBindingsFreshness(plan ResolvedPlan, at time.Time) error {
	if at.IsZero() {
		return fail(ErrInvalidInput, "resolvedPlan.externalBackupTargetBindings", "execution time is required")
	}
	top := map[string]any(plan)
	requirementsRaw, hasRequirements := top["backupTargetRequirements"]
	bindingsRaw, hasBindings := top["externalBackupTargetBindings"]
	if !hasRequirements && !hasBindings {
		return nil
	}
	if !hasRequirements || !hasBindings {
		return fail(ErrExternalBackupTargetBindingMismatch, "resolvedPlan.externalBackupTargetBindings", "backup target requirement and binding projections must appear together")
	}
	requirements, err := asObject(requirementsRaw, "resolvedPlan.backupTargetRequirements")
	if err != nil {
		return err
	}
	bindings, err := asObject(bindingsRaw, "resolvedPlan.externalBackupTargetBindings")
	if err != nil {
		return err
	}
	for _, siteRef := range sortedStringMapKeys(bindings) {
		capabilityBindings, err := asObject(bindings[siteRef], "resolvedPlan.externalBackupTargetBindings."+siteRef)
		if err != nil {
			return err
		}
		for _, capabilityRef := range sortedStringMapKeys(capabilityBindings) {
			requirement, err := backupTargetRequirementAt(requirements, siteRef, capabilityRef)
			if err != nil {
				return fail(ErrExternalBackupTargetBindingMismatch, "resolvedPlan.externalBackupTargetBindings."+siteRef+"."+capabilityRef, "binding has no exact requirement")
			}
			path := "resolvedPlan.externalBackupTargetBindings." + siteRef + "." + capabilityRef
			binding, err := asObject(capabilityBindings[capabilityRef], path)
			if err != nil {
				return err
			}
			if err := validateExternalBackupTargetBinding(binding, requirement, path); err != nil {
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
				return fail(ErrExternalBackupTargetBindingStale, path+".validUntil", "binding is outside its validity window")
			}
		}
	}
	return nil
}
