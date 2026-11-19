package generationartifact

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strconv"

	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

func appendApplyHealthRequirements(plan resolvedplan.ResolvedPlan, modules map[string]applyModule, result *ApplyRequirements) error {
	gates, err := requiredObject(plan, "gates", "resolvedPlan.gates")
	if err != nil {
		return err
	}
	rawHealth, ok := gates["health"].([]any)
	if !ok {
		return fail(ErrInvalidPlan, "resolvedPlan.gates.health", "must be an array")
	}
	for index, raw := range rawHealth {
		path := fmt.Sprintf("resolvedPlan.gates.health[%d]", index)
		gate, ok := raw.(map[string]any)
		if !ok {
			return fail(ErrInvalidPlan, path, "must be an object")
		}
		requirement := ApplyHealthRequirement{}
		var err error
		if requirement.ID, err = requiredString(gate, "id", path+".id"); err != nil {
			return err
		}
		if requirement.ContractHash, err = requiredString(gate, "contractHash", path+".contractHash"); err != nil {
			return err
		}
		if requirement.Phase, err = requiredString(gate, "phase", path+".phase"); err != nil {
			return err
		}
		if requirement.Kind, err = requiredString(gate, "kind", path+".kind"); err != nil {
			return err
		}
		if requirement.TargetKind, err = requiredString(gate, "targetKind", path+".targetKind"); err != nil {
			return err
		}
		sourceField := "sourceRef"
		if requirement.TargetKind == "route" {
			sourceField = "sourceHealthGateRef"
		}
		if requirement.SourceRef, err = requiredString(gate, sourceField, path+"."+sourceField); err != nil {
			return err
		}
		if requirement.TargetRef, err = requiredString(gate, "targetRef", path+".targetRef"); err != nil {
			return err
		}
		if requirement.TargetKind == "module" {
			module, exists := modules[requirement.TargetRef]
			if exists && module.executionClass == ApplyExecutionClassContractHandoff {
				continue
			}
		}
		if requirement.TargetKind == "provider" && !applyProviderHasOwner(plan, requirement.TargetRef) {
			// Provider contracts remain plan/evidence gates. Runtime health is
			// owned by concrete module postconditions; only an explicit
			// provider-owner runtime receives a provider health target.
			continue
		}
		requirement.RouteRef, _ = gate["routeRef"].(string)
		requirement.BackendPoolRef, _ = gate["backendPoolRef"].(string)
		if requirement.SiteRefs, err = applyStringList(gate, "siteRefs", path+".siteRefs"); err != nil {
			return err
		}
		if rawNodeRefs, exists := gate["nodeRefs"]; exists {
			if requirement.NodeRefs, err = applyStringList(map[string]any{"nodeRefs": rawNodeRefs}, "nodeRefs", path+".nodeRefs"); err != nil {
				return err
			}
		}
		if requirement.TargetKind == "route" {
			execution, err := requiredString(gate, "execution", path+".execution")
			if err != nil {
				return err
			}
			if execution == "probe" {
				probe, err := applyRouteHealthProbe(gate, path)
				if err != nil {
					return err
				}
				owners, err := applyRouteHealthRuntimeOwners(plan, requirement, result.RuntimeInstances, path)
				if err != nil {
					return err
				}
				for _, owner := range owners {
					owned := requirement
					owned.ID = requirement.ID + "/runtime/" + owner.ID
					owned.RuntimeRequirementID = owner.ID
					owned.Probe = cloneApplyHealthProbe(probe)
					owned.SiteRefs = append([]string(nil), owner.SiteRefs...)
					owned.NodeRefs = append([]string(nil), owner.NodeRefs...)
					result.HealthRequirements = append(result.HealthRequirements, owned)
				}
				continue
			}
		}
		owners := applyPlacementHealthRuntimeOwners(requirement, result.RuntimeInstances)
		if len(owners) > 1 || len(owners) == 1 && (len(requirement.SiteRefs) != 1 || len(requirement.NodeRefs) != 1) {
			for _, owner := range owners {
				owned := requirement
				owned.ID = requirement.ID + "/runtime/" + owner.ID
				owned.RuntimeRequirementID = owner.ID
				owned.SiteRefs = append([]string(nil), owner.SiteRefs...)
				owned.NodeRefs = append([]string(nil), owner.NodeRefs...)
				result.HealthRequirements = append(result.HealthRequirements, owned)
			}
			continue
		}
		result.HealthRequirements = append(result.HealthRequirements, requirement)
	}
	return nil
}

func applyPlacementHealthRuntimeOwners(health ApplyHealthRequirement, runtimes []ApplyRuntimeRequirement) []ApplyRuntimeRequirement {
	owners := make([]ApplyRuntimeRequirement, 0)
	for _, runtime := range runtimes {
		if len(runtime.SiteRefs) != 1 || len(runtime.NodeRefs) != 1 ||
			!slices.Contains(health.SiteRefs, runtime.SiteRefs[0]) ||
			!slices.Contains(health.NodeRefs, runtime.NodeRefs[0]) {
			continue
		}
		matches := health.TargetKind == "module" && health.TargetRef == runtime.ModuleRef ||
			health.TargetKind == "runtime" && health.TargetRef == runtime.InstanceRef
		if matches {
			owners = append(owners, runtime)
		}
	}
	sort.Slice(owners, func(i, j int) bool { return owners[i].ID < owners[j].ID })
	return owners
}

func applyProviderHasOwner(plan resolvedplan.ResolvedPlan, providerRef string) bool {
	rawProviders, _ := plan["providers"].([]any)
	for _, raw := range rawProviders {
		provider, ok := raw.(map[string]any)
		if !ok || provider["id"] != providerRef {
			continue
		}
		_, hasOwner := provider["owner"].(map[string]any)
		return hasOwner
	}
	return false
}

func applyRouteHealthProbe(gate map[string]any, path string) (*ApplyHealthProbe, error) {
	protocol, err := requiredString(gate, "protocol", path+".protocol")
	if err != nil {
		return nil, err
	}
	port, err := applyInt(gate, "port", path+".port")
	if err != nil {
		return nil, err
	}
	timeout, err := applyInt(gate, "timeoutSeconds", path+".timeoutSeconds")
	if err != nil {
		return nil, err
	}
	probe := &ApplyHealthProbe{Protocol: protocol, Port: port, TimeoutSeconds: timeout}
	kind, _ := gate["kind"].(string)
	if kind == "http" {
		if probe.Method, err = requiredString(gate, "method", path+".method"); err != nil {
			return nil, err
		}
		if probe.Path, err = requiredString(gate, "path", path+".path"); err != nil {
			return nil, err
		}
		follow, ok := gate["followRedirects"].(bool)
		if !ok {
			return nil, fail(ErrInvalidPlan, path+".followRedirects", "must be a boolean")
		}
		probe.FollowRedirects = follow
		rawStatuses, ok := gate["expectedStatuses"].([]any)
		if !ok || len(rawStatuses) == 0 {
			return nil, fail(ErrInvalidPlan, path+".expectedStatuses", "must be a non-empty array")
		}
		for index, raw := range rawStatuses {
			statusPath := fmt.Sprintf("%s.expectedStatuses[%d]", path, index)
			status, err := applyIntegerValue(raw, statusPath)
			if err != nil {
				return nil, err
			}
			probe.ExpectedStatuses = append(probe.ExpectedStatuses, status)
		}
	}
	return probe, nil
}

func applyRouteHealthRuntimeOwners(plan resolvedplan.ResolvedPlan, health ApplyHealthRequirement, runtimes []ApplyRuntimeRequirement, path string) ([]ApplyRuntimeRequirement, error) {
	network, err := requiredObject(plan, "network", "resolvedPlan.network")
	if err != nil {
		return nil, err
	}
	rawPools, ok := network["backendPools"].([]any)
	if !ok {
		return nil, fail(ErrInvalidPlan, "resolvedPlan.network.backendPools", "must be an array")
	}
	var pool map[string]any
	for _, raw := range rawPools {
		candidate, ok := raw.(map[string]any)
		if ok && candidate["id"] == health.BackendPoolRef {
			if pool != nil {
				return nil, fail(ErrInvalidPlan, path+".backendPoolRef", "matches more than one backend pool")
			}
			pool = candidate
		}
	}
	if pool == nil {
		return nil, fail(ErrInvalidPlan, path+".backendPoolRef", "does not resolve one backend pool")
	}
	rawMembers, ok := pool["members"].([]any)
	if !ok || len(rawMembers) == 0 {
		return nil, fail(ErrInvalidPlan, path+".backendPoolRef", "backend pool has no exact members")
	}
	owners := make([]ApplyRuntimeRequirement, 0, len(rawMembers))
	for index, raw := range rawMembers {
		member, ok := raw.(map[string]any)
		if !ok {
			return nil, fail(ErrInvalidPlan, fmt.Sprintf("%s.members[%d]", path, index), "must be an object")
		}
		instanceRef, err := requiredString(member, "instanceRef", fmt.Sprintf("%s.members[%d].instanceRef", path, index))
		if err != nil {
			return nil, err
		}
		siteRef, err := requiredString(member, "siteRef", fmt.Sprintf("%s.members[%d].siteRef", path, index))
		if err != nil {
			return nil, err
		}
		nodeRef, err := requiredString(member, "nodeRef", fmt.Sprintf("%s.members[%d].nodeRef", path, index))
		if err != nil {
			return nil, err
		}
		matches := make([]ApplyRuntimeRequirement, 0, 1)
		for _, runtime := range runtimes {
			if runtime.InstanceRef == instanceRef && len(runtime.SiteRefs) == 1 && runtime.SiteRefs[0] == siteRef && len(runtime.NodeRefs) == 1 && runtime.NodeRefs[0] == nodeRef {
				matches = append(matches, runtime)
			}
		}
		if len(matches) != 1 {
			return nil, fail(ErrInvalidPlan, fmt.Sprintf("%s.members[%d]", path, index), "must bind exactly one Apply runtime owner; got %d", len(matches))
		}
		owners = append(owners, matches[0])
	}
	sort.Slice(owners, func(i, j int) bool { return owners[i].ID < owners[j].ID })
	return owners, nil
}

func applyInt(object map[string]any, field, path string) (int, error) {
	value, ok := object[field]
	if !ok {
		return 0, fail(ErrInvalidPlan, path, "must be an integer")
	}
	return applyIntegerValue(value, path)
}

func applyIntegerValue(value any, path string) (int, error) {
	switch number := value.(type) {
	case int:
		return number, nil
	case json.Number:
		parsed, err := strconv.Atoi(number.String())
		if err == nil {
			return parsed, nil
		}
	}
	return 0, fail(ErrInvalidPlan, path, "must be an integer")
}

func cloneApplyHealthProbe(source *ApplyHealthProbe) *ApplyHealthProbe {
	if source == nil {
		return nil
	}
	result := *source
	result.ExpectedStatuses = append([]int(nil), source.ExpectedStatuses...)
	return &result
}

func bindApplyWorkloadHealthRequirements(result *ApplyRequirements) error {
	for _, workload := range result.Workloads {
		matches := make([]string, 0, 1)
		for _, health := range result.HealthRequirements {
			if health.SourceRef == workload.HealthRef && health.TargetKind == "module" && health.TargetRef == workload.ModuleRef {
				matches = append(matches, health.ID)
			}
		}
		if len(matches) == 0 {
			return fail(ErrInvalidPlan, "resolvedPlan.workloads."+workload.ID+".alternative.route.healthRef", "must bind at least one exact materialized module health gate for logical source %q", workload.HealthRef)
		}
		sort.Strings(matches)
		result.EvidenceRequirements = append(result.EvidenceRequirements, ApplyEvidenceRequirement{
			ID: "workload/" + workload.ID + "/health/" + matches[0], OwnerKind: "workload", OwnerRef: workload.ID,
			Ref: workload.HealthRef, HealthGateRefs: matches,
		})
	}
	return nil
}
