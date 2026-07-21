package generationartifact

import (
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

const maxApplyAccessBindingValidity = 24 * time.Hour

func appendApplyAccessBindingRequirements(plan resolvedplan.ResolvedPlan, modules map[string]applyModule, result *ApplyRequirements) error {
	requirementSites, err := requiredObject(plan, "homeAccessRequirements", "resolvedPlan.homeAccessRequirements")
	if err != nil {
		return err
	}
	bindingSites, err := requiredObject(plan, "externalHomeAccessBindings", "resolvedPlan.externalHomeAccessBindings")
	if err != nil {
		return err
	}
	for _, siteRef := range sortedApplyMapKeys(bindingSites) {
		capabilityBindings, err := requiredObject(bindingSites, siteRef, "resolvedPlan.externalHomeAccessBindings."+siteRef)
		if err != nil {
			return err
		}
		capabilityRequirements, err := requiredObject(requirementSites, siteRef, "resolvedPlan.homeAccessRequirements."+siteRef)
		if err != nil {
			return err
		}
		for _, capabilityRef := range sortedApplyMapKeys(capabilityBindings) {
			path := "resolvedPlan.externalHomeAccessBindings." + siteRef + "." + capabilityRef
			rawRequirement, exists := capabilityRequirements[capabilityRef]
			if !exists {
				return fail(ErrBindingMismatch, path, "binding has no exact Home access requirement")
			}
			requirement, ok := rawRequirement.(map[string]any)
			if !ok {
				return fail(ErrInvalidPlan, "resolvedPlan.homeAccessRequirements."+siteRef+"."+capabilityRef, "must be an object")
			}
			binding, ok := capabilityBindings[capabilityRef].(map[string]any)
			if !ok {
				return fail(ErrInvalidPlan, path, "must be an object")
			}
			if err := verifyApplyHomeAccessAuthority(requirement, binding, siteRef, capabilityRef, path); err != nil {
				return err
			}
			contractOwnerRef, err := requiredString(requirement, "contractOwnerRef", path+".contractOwnerRef")
			if err != nil {
				return err
			}
			runtimeRequirementID, err := exactHomeAccessRuntimeRequirementID(modules, contractOwnerRef, siteRef, capabilityRef)
			if err != nil {
				return err
			}
			targetNodeRefs, err := applyStringList(requirement, "targetNodeRefs", "resolvedPlan.homeAccessRequirements."+siteRef+"."+capabilityRef+".targetNodeRefs")
			if err != nil {
				return err
			}
			access := ApplyAccessBindingRequirement{
				ID: "home-access/" + siteRef + "/" + capabilityRef, RuntimeRequirementID: runtimeRequirementID,
				StackID: mustApplyAccessString(requirement, "stackId"), SiteRef: siteRef, CapabilityRef: capabilityRef,
				ContractOwnerRef: contractOwnerRef, CapabilityContractHash: mustApplyAccessString(requirement, "capabilityContractHash"),
				TargetNodeRefs: targetNodeRefs, RequirementsHash: mustApplyAccessString(requirement, "requirementsHash"),
				BindingRef: mustApplyAccessString(binding, "bindingRef"), BindingHash: mustApplyAccessString(binding, "bindingHash"),
				AccessFabricRef: mustApplyAccessString(binding, "accessFabricRef"), StackKitsVersion: mustApplyAccessString(binding, "stackkitsVersion"),
				CandidateDigest: mustApplyAccessString(binding, "candidateDigest"), SpecHash: mustApplyAccessString(binding, "specHash"),
				IssuedAt: mustApplyAccessString(binding, "issuedAt"), ValidUntil: mustApplyAccessString(binding, "validUntil"),
			}
			result.AccessBindings = append(result.AccessBindings, access)
			for index := range result.RuntimeInstances {
				if result.RuntimeInstances[index].ID == runtimeRequirementID {
					result.RuntimeInstances[index].AccessBindingRefs = append(result.RuntimeInstances[index].AccessBindingRefs, access.ID)
				}
			}
		}
	}
	return nil
}

func verifyApplyHomeAccessAuthority(requirement, binding map[string]any, siteRef, capabilityRef, path string) error {
	if !exactApplyAccessObjectKeys(requirement, []string{"apiVersion", "kind", "stackId", "siteRef", "capabilityRef", "contractOwnerRef", "capabilityContractHash", "targetNodeRefs", "policy", "specHash", "requirementsHash"}) {
		return fail(ErrInvalidPlan, "resolvedPlan.homeAccessRequirements."+siteRef+"."+capabilityRef, "must be the closed Home access requirement body")
	}
	if !exactApplyAccessObjectKeys(binding, []string{"apiVersion", "kind", "bindingRef", "stackId", "siteRef", "capabilityRef", "contractOwnerRef", "capabilityContractHash", "requirementsHash", "accessFabricRef", "stackkitsVersion", "candidateDigest", "specHash", "issuedAt", "validUntil", "bindingHash"}) {
		return fail(ErrInvalidPlan, path, "must be the closed external Home access binding body")
	}
	if requirement["apiVersion"] != "stackkit.home-access-requirement/v1" || requirement["kind"] != "HomeAccessRequirement" ||
		binding["apiVersion"] != "stackkit.external-home-access-binding/v1" || binding["kind"] != "ExternalHomeAccessBinding" {
		return fail(ErrInvalidPlan, path, "uses an unsupported Home access contract version or kind")
	}
	for _, field := range []string{"stackId", "siteRef", "capabilityRef", "contractOwnerRef", "capabilityContractHash", "specHash", "requirementsHash"} {
		if _, err := requiredString(requirement, field, "resolvedPlan.homeAccessRequirements."+siteRef+"."+capabilityRef+"."+field); err != nil {
			return err
		}
	}
	for _, field := range []string{"bindingRef", "stackId", "siteRef", "capabilityRef", "contractOwnerRef", "capabilityContractHash", "requirementsHash", "accessFabricRef", "stackkitsVersion", "candidateDigest", "specHash", "issuedAt", "validUntil", "bindingHash"} {
		if _, err := requiredString(binding, field, path+"."+field); err != nil {
			return err
		}
	}
	policy, err := requiredObject(requirement, "policy", "resolvedPlan.homeAccessRequirements."+siteRef+"."+capabilityRef+".policy")
	if err != nil {
		return err
	}
	if !exactApplyAccessObjectKeys(policy, []string{"defaultDeny", "initiation", "routeScope", "allowDefaultRoute", "allowBroadLAN", "identityMode", "credentialCustody", "fabricLifecycle"}) ||
		policy["defaultDeny"] != true || policy["initiation"] != "home-outbound" || policy["routeScope"] != "declared-services-only" ||
		policy["allowDefaultRoute"] != false || policy["allowBroadLAN"] != false || policy["credentialCustody"] != "external" || policy["fabricLifecycle"] != "external" {
		return fail(ErrInvalidPlan, "resolvedPlan.homeAccessRequirements."+siteRef+"."+capabilityRef+".policy", "widens the closed provider-free Home access policy")
	}
	wantIdentityMode := "device-bound"
	if capabilityRef == "public-publish-egress" {
		wantIdentityMode = "service-identity"
	} else if capabilityRef != "private-remote-access" {
		return fail(ErrInvalidPlan, path+".capabilityRef", "unsupported Home access capability %q", capabilityRef)
	}
	if policy["identityMode"] != wantIdentityMode {
		return fail(ErrInvalidPlan, "resolvedPlan.homeAccessRequirements."+siteRef+"."+capabilityRef+".policy.identityMode", "does not match the capability authority")
	}
	requirementHash, err := resolvedplan.ComputeHomeAccessRequirementHash(resolvedplan.HomeAccessRequirement(requirement))
	if err != nil {
		return wrap(ErrInvalidPlan, path, "compute exact Home access requirement hash", err)
	}
	if requirement["requirementsHash"] != requirementHash {
		return fail(ErrBindingMismatch, path+".requirementsHash", "does not match the canonical Home access requirement")
	}
	bindingHash, err := resolvedplan.ComputeExternalHomeAccessBindingHash(resolvedplan.ExternalHomeAccessBinding(binding))
	if err != nil {
		return wrap(ErrInvalidPlan, path, "compute exact external Home access binding hash", err)
	}
	if binding["bindingHash"] != bindingHash {
		return fail(ErrBindingMismatch, path+".bindingHash", "does not match the canonical external Home access binding")
	}
	for _, field := range []string{"stackId", "siteRef", "capabilityRef", "contractOwnerRef", "capabilityContractHash", "requirementsHash", "specHash"} {
		if binding[field] != requirement[field] {
			return fail(ErrBindingMismatch, path+"."+field, "does not match the exact Home access requirement")
		}
	}
	if binding["siteRef"] != siteRef || binding["capabilityRef"] != capabilityRef {
		return fail(ErrBindingMismatch, path, "map keys do not match the bound Site and capability")
	}
	issuedAt, err := parseApplyAccessTimestamp(mustApplyAccessString(binding, "issuedAt"))
	if err != nil {
		return fail(ErrInvalidPlan, path+".issuedAt", "must be a canonical UTC timestamp")
	}
	validUntil, err := parseApplyAccessTimestamp(mustApplyAccessString(binding, "validUntil"))
	if err != nil {
		return fail(ErrInvalidPlan, path+".validUntil", "must be a canonical UTC timestamp")
	}
	if !issuedAt.Before(validUntil) || validUntil.Sub(issuedAt) > maxApplyAccessBindingValidity {
		return fail(ErrInvalidPlan, path+".validUntil", "must be after issuedAt with validity no greater than %s", maxApplyAccessBindingValidity)
	}
	return nil
}

func exactHomeAccessRuntimeRequirementID(modules map[string]applyModule, contractOwnerRef, siteRef, capabilityRef string) (string, error) {
	matches := []string{}
	for _, moduleID := range sortedApplyMapKeys(modules) {
		module := modules[moduleID]
		if module.providerRef != contractOwnerRef || !slices.Contains(module.provides, capabilityRef) || !slices.Contains(module.siteRefs, siteRef) {
			continue
		}
		for _, unit := range module.units {
			if !slices.Contains(unit.planInputRefs, "homeAccessRequirements") || !slices.Contains(unit.planInputRefs, "externalHomeAccessBindings") {
				continue
			}
			for _, instance := range unit.instances {
				if instance.siteRef != "" && instance.siteRef != siteRef {
					continue
				}
				matches = append(matches, moduleID+"/"+unit.id+"/"+instance.id)
			}
		}
	}
	if len(matches) != 1 {
		return "", fail(ErrBindingMismatch, "resolvedPlan.homeAccessRequirements."+siteRef+"."+capabilityRef, "requires exactly one module-scoped executor contract; got %d", len(matches))
	}
	return matches[0], nil
}

func validateApplyAccessBindingClosure(requirements ApplyRequirements) error {
	runtimeByID := make(map[string]ApplyRuntimeRequirement, len(requirements.RuntimeInstances))
	for _, runtime := range requirements.RuntimeInstances {
		runtimeByID[runtime.ID] = runtime
	}
	bindingByID := make(map[string]ApplyAccessBindingRequirement, len(requirements.AccessBindings))
	for _, binding := range requirements.AccessBindings {
		if _, duplicate := bindingByID[binding.ID]; duplicate {
			return fail(ErrInvalidPlan, "resolvedPlan.applyRequirements.accessBindings", "duplicate access binding %q", binding.ID)
		}
		bindingByID[binding.ID] = binding
		if runtime, executable := runtimeByID[binding.RuntimeRequirementID]; executable {
			if !slices.Contains(runtime.AccessBindingRefs, binding.ID) || !slices.Contains(runtime.SiteRefs, binding.SiteRef) {
				return fail(ErrBindingMismatch, "resolvedPlan.applyRequirements.accessBindings."+binding.ID, "is not owned by its exact executable runtime target")
			}
			for _, nodeRef := range binding.TargetNodeRefs {
				if !slices.Contains(runtime.NodeRefs, nodeRef) {
					return fail(ErrBindingMismatch, "resolvedPlan.applyRequirements.accessBindings."+binding.ID+".targetNodeRefs", "contains a node outside its exact runtime target")
				}
			}
			continue
		}
		if !hasContractHandoffOwner(requirements.Artifacts, binding.RuntimeRequirementID) {
			return fail(ErrInvalidPlan, "resolvedPlan.applyRequirements.accessBindings."+binding.ID+".runtimeRequirementId", "has neither an executable runtime nor its exact contract-handoff owner")
		}
	}
	for _, runtime := range requirements.RuntimeInstances {
		coveredSites := map[string]struct{}{}
		coveredNodes := map[string]struct{}{}
		if len(runtime.AccessBindingRefs) != 0 && len(runtime.SiteRefs) != 1 {
			return fail(ErrBindingMismatch, "resolvedPlan.applyRequirements.runtimeInstances."+runtime.ID+".siteRefs", "access-bound runtime must belong to exactly one Site until the contract carries explicit node-to-Site pairs")
		}
		for _, bindingRef := range runtime.AccessBindingRefs {
			binding, exists := bindingByID[bindingRef]
			if !exists || binding.RuntimeRequirementID != runtime.ID {
				return fail(ErrBindingMismatch, "resolvedPlan.applyRequirements.runtimeInstances."+runtime.ID+".accessBindingRefs", "contains an absent or foreign access binding")
			}
			coveredSites[binding.SiteRef] = struct{}{}
			for _, nodeRef := range binding.TargetNodeRefs {
				coveredNodes[nodeRef] = struct{}{}
			}
		}
		if len(runtime.AccessBindingRefs) != 0 && (!exactApplyAccessRefs(runtime.SiteRefs, coveredSites) || !exactApplyAccessRefs(runtime.NodeRefs, coveredNodes)) {
			return fail(ErrBindingMismatch, "resolvedPlan.applyRequirements.runtimeInstances."+runtime.ID+".accessBindingRefs", "must exactly cover the runtime Site and node sets")
		}
	}
	return nil
}

func hasContractHandoffOwner(artifacts []ApplyArtifactRequirement, runtimeRequirementID string) bool {
	for _, artifact := range artifacts {
		if artifact.ExecutionClass == ApplyExecutionClassContractHandoff && artifact.ModuleRef+"/"+artifact.UnitRef+"/"+artifact.InstanceRef == runtimeRequirementID {
			return true
		}
	}
	return false
}

func exactApplyAccessRefs(values []string, covered map[string]struct{}) bool {
	if len(values) != len(covered) {
		return false
	}
	for _, value := range values {
		if _, exists := covered[value]; !exists {
			return false
		}
	}
	return true
}

func parseApplyAccessTimestamp(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || parsed.Location() != time.UTC || parsed.Format(time.RFC3339Nano) != value {
		return time.Time{}, fmt.Errorf("noncanonical UTC timestamp")
	}
	return parsed, nil
}

func mustApplyAccessString(object map[string]any, field string) string {
	value, _ := object[field].(string)
	return value
}

func exactApplyAccessObjectKeys(object map[string]any, expected []string) bool {
	actual := make([]string, 0, len(object))
	for key := range object {
		actual = append(actual, key)
	}
	sort.Strings(actual)
	expected = append([]string(nil), expected...)
	sort.Strings(expected)
	return slices.Equal(actual, expected)
}
