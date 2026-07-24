package generationartifact

import (
	"slices"
	"time"

	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

const maxApplyBackupTargetBindingValidity = 24 * time.Hour

func appendApplyBackupTargetBindingRequirements(plan resolvedplan.ResolvedPlan, modules map[string]applyModule, result *ApplyRequirements) error {
	requirementSites, err := requiredObject(plan, "backupTargetRequirements", "resolvedPlan.backupTargetRequirements")
	if err != nil {
		return err
	}
	bindingSites, err := requiredObject(plan, "externalBackupTargetBindings", "resolvedPlan.externalBackupTargetBindings")
	if err != nil {
		return err
	}
	for _, siteRef := range sortedApplyMapKeys(bindingSites) {
		capabilityBindings, err := requiredObject(bindingSites, siteRef, "resolvedPlan.externalBackupTargetBindings."+siteRef)
		if err != nil {
			return err
		}
		capabilityRequirements, err := requiredObject(requirementSites, siteRef, "resolvedPlan.backupTargetRequirements."+siteRef)
		if err != nil {
			return err
		}
		for _, capabilityRef := range sortedApplyMapKeys(capabilityBindings) {
			path := "resolvedPlan.externalBackupTargetBindings." + siteRef + "." + capabilityRef
			rawRequirement, exists := capabilityRequirements[capabilityRef]
			if !exists {
				return fail(ErrBindingMismatch, path, "binding has no exact Cloud backup-target requirement")
			}
			requirement, ok := rawRequirement.(map[string]any)
			if !ok {
				return fail(ErrInvalidPlan, "resolvedPlan.backupTargetRequirements."+siteRef+"."+capabilityRef, "must be an object")
			}
			binding, ok := capabilityBindings[capabilityRef].(map[string]any)
			if !ok {
				return fail(ErrInvalidPlan, path, "must be an object")
			}
			if err := verifyApplyBackupTargetAuthority(requirement, binding, siteRef, capabilityRef, path); err != nil {
				return err
			}
			contractOwnerRef, err := requiredString(requirement, "contractOwnerRef", path+".contractOwnerRef")
			if err != nil {
				return err
			}
			targetNodeRefs, err := applyStringList(requirement, "targetNodeRefs", "resolvedPlan.backupTargetRequirements."+siteRef+"."+capabilityRef+".targetNodeRefs")
			if err != nil {
				return err
			}
			runtimeRequirements, err := exactBackupTargetRuntimeRequirements(modules, contractOwnerRef, siteRef, capabilityRef, targetNodeRefs)
			if err != nil {
				return err
			}
			for _, runtime := range runtimeRequirements {
				projected := ApplyBackupTargetBindingRequirement{
					ID: "backup-target/" + siteRef + "/" + capabilityRef + "/" + runtime.nodeRef, RuntimeRequirementID: runtime.id,
					StackID: mustApplyAccessString(requirement, "stackId"), SiteRef: siteRef, CapabilityRef: capabilityRef,
					ContractOwnerRef: contractOwnerRef, CapabilityContractHash: mustApplyAccessString(requirement, "capabilityContractHash"),
					TargetNodeRefs: []string{runtime.nodeRef}, RequirementsHash: mustApplyAccessString(requirement, "requirementsHash"),
					BindingRef: mustApplyAccessString(binding, "bindingRef"), BindingHash: mustApplyAccessString(binding, "bindingHash"),
					BackupTargetRef: mustApplyAccessString(binding, "backupTargetRef"), CustodyAttestationRef: mustApplyAccessString(binding, "custodyAttestationRef"),
					StackKitsVersion: mustApplyAccessString(binding, "stackkitsVersion"), CandidateDigest: mustApplyAccessString(binding, "candidateDigest"),
					SpecHash: mustApplyAccessString(binding, "specHash"), IssuedAt: mustApplyAccessString(binding, "issuedAt"),
					ValidUntil: mustApplyAccessString(binding, "validUntil"),
				}
				result.BackupTargetBindings = append(result.BackupTargetBindings, projected)
				for index := range result.RuntimeInstances {
					if result.RuntimeInstances[index].ID == runtime.id {
						result.RuntimeInstances[index].BackupTargetBindingRefs = append(result.RuntimeInstances[index].BackupTargetBindingRefs, projected.ID)
					}
				}
			}
		}
	}
	return nil
}

func verifyApplyBackupTargetAuthority(requirement, binding map[string]any, siteRef, capabilityRef, path string) error {
	requirementPath := "resolvedPlan.backupTargetRequirements." + siteRef + "." + capabilityRef
	if !exactApplyAccessObjectKeys(requirement, []string{"apiVersion", "kind", "stackId", "siteRef", "capabilityRef", "contractOwnerRef", "capabilityContractHash", "targetNodeRefs", "policy", "specHash", "requirementsHash"}) {
		return fail(ErrInvalidPlan, requirementPath, "must be the closed Cloud backup-target requirement body")
	}
	if !exactApplyAccessObjectKeys(binding, []string{"apiVersion", "kind", "bindingRef", "backupTargetRef", "custodyAttestationRef", "stackId", "siteRef", "capabilityRef", "contractOwnerRef", "capabilityContractHash", "requirementsHash", "stackkitsVersion", "candidateDigest", "specHash", "issuedAt", "validUntil", "bindingHash"}) {
		return fail(ErrInvalidPlan, path, "must be the closed external backup-target binding body")
	}
	if requirement["apiVersion"] != "stackkit.backup-target-requirement/v1" || requirement["kind"] != "BackupTargetRequirement" ||
		binding["apiVersion"] != "stackkit.external-backup-target-binding/v1" || binding["kind"] != "ExternalBackupTargetBinding" {
		return fail(ErrInvalidPlan, path, "uses an unsupported backup-target contract version or kind")
	}
	if capabilityRef != "offsite-object-backup" {
		return fail(ErrInvalidPlan, path+".capabilityRef", "unsupported Cloud backup-target capability %q", capabilityRef)
	}
	policy, err := requiredObject(requirement, "policy", requirementPath+".policy")
	if err != nil {
		return err
	}
	if !exactApplyAccessObjectKeys(policy, []string{"scope", "encryptionRequired", "credentialCustody", "targetLifecycle", "restoreVerificationRequired", "providerSelection"}) ||
		policy["scope"] != "governed-data-only" || policy["encryptionRequired"] != true ||
		policy["credentialCustody"] != "external" || policy["targetLifecycle"] != "external" ||
		policy["restoreVerificationRequired"] != true || policy["providerSelection"] != "external" {
		return fail(ErrInvalidPlan, requirementPath+".policy", "widens the closed provider-free Cloud backup policy")
	}
	requirementHash, err := resolvedplan.ComputeBackupTargetRequirementHash(resolvedplan.BackupTargetRequirement(requirement))
	if err != nil {
		return wrap(ErrInvalidPlan, path, "compute exact backup-target requirement hash", err)
	}
	if requirement["requirementsHash"] != requirementHash {
		return fail(ErrBindingMismatch, path+".requirementsHash", "does not match the canonical backup-target requirement")
	}
	bindingHash, err := resolvedplan.ComputeExternalBackupTargetBindingHash(resolvedplan.ExternalBackupTargetBinding(binding))
	if err != nil {
		return wrap(ErrInvalidPlan, path, "compute exact external backup-target binding hash", err)
	}
	if binding["bindingHash"] != bindingHash {
		return fail(ErrBindingMismatch, path+".bindingHash", "does not match the canonical external backup-target binding")
	}
	for _, field := range []string{"stackId", "siteRef", "capabilityRef", "contractOwnerRef", "capabilityContractHash", "requirementsHash", "specHash"} {
		if binding[field] != requirement[field] {
			return fail(ErrBindingMismatch, path+"."+field, "does not match the exact backup-target requirement")
		}
	}
	if binding["siteRef"] != siteRef || binding["capabilityRef"] != capabilityRef {
		return fail(ErrBindingMismatch, path, "map keys do not match the bound Site and capability")
	}
	for _, field := range []string{"bindingRef", "backupTargetRef", "custodyAttestationRef", "stackkitsVersion", "candidateDigest", "issuedAt", "validUntil", "bindingHash"} {
		if _, err := requiredString(binding, field, path+"."+field); err != nil {
			return err
		}
	}
	issuedAt, err := parseApplyAccessTimestamp(mustApplyAccessString(binding, "issuedAt"))
	if err != nil {
		return fail(ErrInvalidPlan, path+".issuedAt", "must be a canonical UTC timestamp")
	}
	validUntil, err := parseApplyAccessTimestamp(mustApplyAccessString(binding, "validUntil"))
	if err != nil {
		return fail(ErrInvalidPlan, path+".validUntil", "must be a canonical UTC timestamp")
	}
	if !issuedAt.Before(validUntil) || validUntil.Sub(issuedAt) > maxApplyBackupTargetBindingValidity {
		return fail(ErrInvalidPlan, path+".validUntil", "must be after issuedAt with validity no greater than %s", maxApplyBackupTargetBindingValidity)
	}
	return nil
}

type backupTargetRuntimeRequirement struct {
	id      string
	nodeRef string
}

func exactBackupTargetRuntimeRequirements(modules map[string]applyModule, contractOwnerRef, siteRef, capabilityRef string, targetNodeRefs []string) ([]backupTargetRuntimeRequirement, error) {
	matches := []backupTargetRuntimeRequirement{}
	for _, moduleID := range sortedApplyMapKeys(modules) {
		module := modules[moduleID]
		if module.providerRef != contractOwnerRef || !slices.Contains(module.provides, capabilityRef) || !slices.Contains(module.siteRefs, siteRef) {
			continue
		}
		for _, unit := range module.units {
			if !slices.Contains(unit.planInputRefs, "cloudOffsiteBackup") {
				continue
			}
			for _, instance := range unit.instances {
				if instance.siteRef != siteRef || !slices.Contains(targetNodeRefs, instance.nodeRef) {
					continue
				}
				matches = append(matches, backupTargetRuntimeRequirement{id: moduleID + "/" + unit.id + "/" + instance.id, nodeRef: instance.nodeRef})
			}
		}
	}
	if len(matches) != len(targetNodeRefs) {
		return nil, fail(ErrBindingMismatch, "resolvedPlan.backupTargetRequirements."+siteRef+"."+capabilityRef, "requires one node-local executor contract per target node; got %d for %d nodes", len(matches), len(targetNodeRefs))
	}
	slices.SortFunc(matches, func(left, right backupTargetRuntimeRequirement) int {
		if left.nodeRef < right.nodeRef {
			return -1
		}
		if left.nodeRef > right.nodeRef {
			return 1
		}
		return 0
	})
	for index, nodeRef := range targetNodeRefs {
		if matches[index].nodeRef != nodeRef {
			return nil, fail(ErrBindingMismatch, "resolvedPlan.backupTargetRequirements."+siteRef+"."+capabilityRef, "executor contracts do not exactly cover target node %q", nodeRef)
		}
	}
	return matches, nil
}

func validateApplyBackupTargetBindingClosure(requirements ApplyRequirements) error {
	runtimeByID := make(map[string]ApplyRuntimeRequirement, len(requirements.RuntimeInstances))
	for _, runtime := range requirements.RuntimeInstances {
		runtimeByID[runtime.ID] = runtime
	}
	bindingByID := make(map[string]ApplyBackupTargetBindingRequirement, len(requirements.BackupTargetBindings))
	for _, binding := range requirements.BackupTargetBindings {
		if _, duplicate := bindingByID[binding.ID]; duplicate {
			return fail(ErrInvalidPlan, "resolvedPlan.applyRequirements.backupTargetBindings", "duplicate backup-target binding %q", binding.ID)
		}
		bindingByID[binding.ID] = binding
		runtime, executable := runtimeByID[binding.RuntimeRequirementID]
		if !executable {
			if hasContractHandoffOwner(requirements.Artifacts, binding.RuntimeRequirementID) {
				continue
			}
			return fail(ErrInvalidPlan, "resolvedPlan.applyRequirements.backupTargetBindings."+binding.ID+".runtimeRequirementId", "has neither an executable runtime nor its exact contract-handoff owner")
		}
		if !slices.Contains(runtime.BackupTargetBindingRefs, binding.ID) || !slices.Contains(runtime.SiteRefs, binding.SiteRef) {
			return fail(ErrBindingMismatch, "resolvedPlan.applyRequirements.backupTargetBindings."+binding.ID, "is not owned by its exact executable runtime target")
		}
		for _, nodeRef := range binding.TargetNodeRefs {
			if !slices.Contains(runtime.NodeRefs, nodeRef) {
				return fail(ErrBindingMismatch, "resolvedPlan.applyRequirements.backupTargetBindings."+binding.ID+".targetNodeRefs", "contains a node outside its exact runtime target")
			}
		}
	}
	for _, runtime := range requirements.RuntimeInstances {
		if len(runtime.BackupTargetBindingRefs) == 0 {
			continue
		}
		if len(runtime.SiteRefs) != 1 {
			return fail(ErrBindingMismatch, "resolvedPlan.applyRequirements.runtimeInstances."+runtime.ID+".siteRefs", "backup-target-bound runtime must belong to exactly one Site")
		}
		coveredSites := map[string]struct{}{}
		coveredNodes := map[string]struct{}{}
		for _, bindingRef := range runtime.BackupTargetBindingRefs {
			binding, exists := bindingByID[bindingRef]
			if !exists || binding.RuntimeRequirementID != runtime.ID {
				return fail(ErrBindingMismatch, "resolvedPlan.applyRequirements.runtimeInstances."+runtime.ID+".backupTargetBindingRefs", "contains an absent or foreign backup-target binding")
			}
			coveredSites[binding.SiteRef] = struct{}{}
			for _, nodeRef := range binding.TargetNodeRefs {
				coveredNodes[nodeRef] = struct{}{}
			}
		}
		if !exactApplyAccessRefs(runtime.SiteRefs, coveredSites) || !exactApplyAccessRefs(runtime.NodeRefs, coveredNodes) {
			return fail(ErrBindingMismatch, "resolvedPlan.applyRequirements.runtimeInstances."+runtime.ID+".backupTargetBindingRefs", "must exactly cover the runtime Site and node sets")
		}
	}
	return nil
}
