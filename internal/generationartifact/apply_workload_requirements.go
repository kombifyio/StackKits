package generationartifact

import (
	"fmt"
	"sort"

	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

func parseApplyWorkloads(plan resolvedplan.ResolvedPlan, modules map[string]applyModule, result *ApplyRequirements) (map[string]string, map[string]*ApplyRuntimeAdapterRequirement, error) {
	rawWorkloads, ok := plan["workloads"].([]any)
	if !ok {
		return nil, nil, fail(ErrInvalidPlan, "resolvedPlan.workloads", "must be an array")
	}
	workloadByModule := make(map[string]string, len(rawWorkloads))
	runtimeAdapterByModule := make(map[string]*ApplyRuntimeAdapterRequirement, len(rawWorkloads))
	for index, raw := range rawWorkloads {
		path := fmt.Sprintf("resolvedPlan.workloads[%d]", index)
		workload, ok := raw.(map[string]any)
		if !ok {
			return nil, nil, fail(ErrInvalidPlan, path, "must be an object")
		}
		id, err := requiredString(workload, "id", path+".id")
		if err != nil {
			return nil, nil, err
		}
		contractHash, err := requiredString(workload, "contractHash", path+".contractHash")
		if err != nil {
			return nil, nil, err
		}
		alternative, err := requiredObject(workload, "alternative", path+".alternative")
		if err != nil {
			return nil, nil, err
		}
		moduleRef, err := requiredString(alternative, "moduleRef", path+".alternative.moduleRef")
		if err != nil {
			return nil, nil, err
		}
		module, exists := modules[moduleRef]
		if !exists {
			return nil, nil, fail(ErrInvalidPlan, path+".alternative.moduleRef", "module %q is absent", moduleRef)
		}
		runtimeContract, err := requiredObject(alternative, "runtime", path+".alternative.runtime")
		if err != nil {
			return nil, nil, err
		}
		route, err := requiredObject(alternative, "route", path+".alternative.route")
		if err != nil {
			return nil, nil, err
		}
		requirement := ApplyWorkloadRequirement{ID: id, ContractHash: contractHash, ModuleRef: moduleRef, EvidenceRefs: append([]string(nil), module.evidenceRefs...)}
		if requirement.ProviderRef, err = requiredString(alternative, "providerRef", path+".alternative.providerRef"); err != nil {
			return nil, nil, err
		}
		if requirement.RuntimeKind, err = requiredString(runtimeContract, "kind", path+".alternative.runtime.kind"); err != nil {
			return nil, nil, err
		}
		if requirement.RuntimeDelivery, err = requiredString(runtimeContract, "delivery", path+".alternative.runtime.delivery"); err != nil {
			return nil, nil, err
		}
		adapter, err := parseApplyRuntimeAdapter(runtimeContract, path+".alternative.runtime", modules)
		if err != nil {
			return nil, nil, err
		}
		if requirement.RuntimeDelivery == applicationAdapterRuntimeDelivery && adapter == nil {
			return nil, nil, fail(ErrInvalidPlan, path+".alternative.runtime.adapter", "application-adapter workload requires one exact runtime adapter")
		}
		if requirement.RuntimeDelivery != applicationAdapterRuntimeDelivery && adapter != nil {
			return nil, nil, fail(ErrInvalidPlan, path+".alternative.runtime.adapter", "runtime adapter is valid only for application-adapter delivery")
		}
		if requirement.ServiceRef, err = requiredString(route, "serviceRef", path+".alternative.route.serviceRef"); err != nil {
			return nil, nil, err
		}
		if requirement.HealthRef, err = requiredString(route, "healthRef", path+".alternative.route.healthRef"); err != nil {
			return nil, nil, err
		}
		if requirement.SiteRefs, err = applyStringList(workload, "siteRefs", path+".siteRefs"); err != nil {
			return nil, nil, err
		}
		if requirement.NodeRefs, err = applyStringList(workload, "nodeRefs", path+".nodeRefs"); err != nil {
			return nil, nil, err
		}
		for _, unit := range module.units {
			for _, instance := range unit.instances {
				requirement.InstanceRefs = append(requirement.InstanceRefs, instance.id)
			}
		}
		if _, duplicate := workloadByModule[moduleRef]; duplicate {
			return nil, nil, fail(ErrInvalidPlan, path+".alternative.moduleRef", "workload module %q is selected more than once", moduleRef)
		}
		workloadByModule[moduleRef] = id
		runtimeAdapterByModule[moduleRef] = adapter
		result.Workloads = append(result.Workloads, requirement)
		for _, evidenceRef := range requirement.EvidenceRefs {
			result.EvidenceRequirements = append(result.EvidenceRequirements, ApplyEvidenceRequirement{
				ID: "workload/" + id + "/" + evidenceRef, OwnerKind: "workload", OwnerRef: id, Ref: evidenceRef,
			})
		}
	}
	return workloadByModule, runtimeAdapterByModule, nil
}

func parseApplyRuntimeAdapter(runtime map[string]any, path string, modules map[string]applyModule) (*ApplyRuntimeAdapterRequirement, error) {
	raw, exists := runtime["adapter"]
	if !exists {
		return nil, nil
	}
	adapter, ok := raw.(map[string]any)
	if !ok {
		return nil, fail(ErrInvalidPlan, path+".adapter", "must be an object")
	}
	result := &ApplyRuntimeAdapterRequirement{}
	var err error
	if result.ID, err = requiredString(adapter, "id", path+".adapter.id"); err != nil {
		return nil, err
	}
	if result.ProviderRef, err = requiredString(adapter, "providerRef", path+".adapter.providerRef"); err != nil {
		return nil, err
	}
	if result.ProviderVersion, err = requiredString(adapter, "providerVersion", path+".adapter.providerVersion"); err != nil {
		return nil, err
	}
	if result.ProviderContractHash, err = requiredString(adapter, "providerContractHash", path+".adapter.providerContractHash"); err != nil {
		return nil, err
	}
	if result.ModuleRef, err = requiredString(adapter, "moduleRef", path+".adapter.moduleRef"); err != nil {
		return nil, err
	}
	if result.ModuleVersion, err = requiredString(adapter, "moduleVersion", path+".adapter.moduleVersion"); err != nil {
		return nil, err
	}
	if result.ModuleContractHash, err = requiredString(adapter, "moduleContractHash", path+".adapter.moduleContractHash"); err != nil {
		return nil, err
	}
	core, exists := modules[result.ModuleRef]
	if !exists || core.runtimeAdapterID != result.ID || core.providerRef != result.ProviderRef || core.providerVersion != result.ProviderVersion || core.version != result.ModuleVersion ||
		core.providerContractHash != result.ProviderContractHash || core.contractHash != result.ModuleContractHash || core.executionClass != ApplyExecutionClassContractHandoff {
		return nil, fail(ErrInvalidPlan, path+".adapter", "does not match one exact contract-handoff adapter module")
	}
	result.ArtifactRefs = applyModuleArtifactRefs(core)
	if len(result.ArtifactRefs) == 0 {
		return nil, fail(ErrInvalidPlan, path+".adapter.moduleRef", "adapter module has no contract-handoff artifact")
	}
	result.Capabilities = append([]string(nil), core.runtimeAdapterCapabilities...)
	for _, agentID := range core.runtimeAdapterAgentRefs {
		var matched *applyModule
		for _, moduleID := range sortedApplyMapKeys(modules) {
			candidate := modules[moduleID]
			if candidate.runtimeAdapterAgentID != agentID || candidate.runtimeAdapterAgentRef != result.ID || candidate.providerRef != result.ProviderRef {
				continue
			}
			if matched != nil {
				return nil, fail(ErrInvalidPlan, path+".adapter", "agent %q resolves to multiple modules", agentID)
			}
			copy := candidate
			matched = &copy
		}
		if matched == nil || matched.executionClass != ApplyExecutionClassContractHandoff || matched.providerContractHash != result.ProviderContractHash {
			return nil, fail(ErrInvalidPlan, path+".adapter", "agent %q has no exact contract-handoff module", agentID)
		}
		artifactRefs := applyModuleArtifactRefs(*matched)
		if len(artifactRefs) == 0 {
			return nil, fail(ErrInvalidPlan, path+".adapter", "agent %q has no contract-handoff artifact", agentID)
		}
		result.Agents = append(result.Agents, ApplyRuntimeAdapterAgentRequirement{
			ID: agentID, ModuleRef: matched.id, ModuleVersion: matched.version, ModuleContractHash: matched.contractHash, ArtifactRefs: artifactRefs,
		})
	}
	return result, nil
}

func applyModuleArtifactRefs(module applyModule) []string {
	refs := []string{}
	for _, unit := range module.units {
		for _, instance := range unit.instances {
			refs = append(refs, applyInstanceArtifactRefs(instance.outputs)...)
		}
	}
	sort.Strings(refs)
	return refs
}

func appendApplyModuleRequirements(modules map[string]applyModule, workloadByModule map[string]string, runtimeAdapterByWorkloadModule map[string]*ApplyRuntimeAdapterRequirement, targetNodes map[string]struct{}, result *ApplyRequirements) error {
	for _, moduleID := range sortedApplyMapKeys(modules) {
		module := modules[moduleID]
		for _, nodeRef := range module.nodeRefs {
			targetNodes[nodeRef] = struct{}{}
		}
		if module.executionClass == ApplyExecutionClassExecutable {
			for _, evidenceRef := range module.evidenceRefs {
				result.EvidenceRequirements = append(result.EvidenceRequirements, ApplyEvidenceRequirement{ID: "module/" + moduleID + "/" + evidenceRef, OwnerKind: "module", OwnerRef: moduleID, Ref: evidenceRef})
			}
		}
		for _, unit := range module.units {
			for _, instance := range unit.instances {
				defaultSecretSourceKind, defaultSecretSourceRef := "module", moduleID
				if workloadRef := workloadByModule[moduleID]; workloadRef != "" {
					defaultSecretSourceKind, defaultSecretSourceRef = "workload", workloadRef
				}
				siteRefs := append([]string(nil), module.siteRefs...)
				nodeRefs := append([]string(nil), module.nodeRefs...)
				if instance.siteRef != "" {
					siteRefs = []string{instance.siteRef}
				}
				if instance.nodeRef != "" {
					nodeRefs = []string{instance.nodeRef}
				}
				if module.executionClass == ApplyExecutionClassExecutable && unit.applyMode != ApplyExecutionClassArtifactOnly {
					runtimeRequirement := ApplyRuntimeRequirement{
						ID: moduleID + "/" + unit.id + "/" + instance.id, OwnerKind: "module", OwnerRef: moduleID, OwnerContractHash: module.contractHash,
						ProviderRef: module.providerRef, ProviderContractHash: module.providerContractHash, ModuleRef: moduleID, ModuleContractHash: module.contractHash,
						UnitRef: unit.id, UnitContractHash: unit.contractHash, InstanceRef: instance.id, WorkloadRef: workloadByModule[moduleID], RuntimeKind: module.runtimeKind,
						RuntimeDelivery: module.runtimeDelivery, RuntimeEngine: module.runtimeEngine, ImageRef: module.imageRef, ImageDigest: module.imageDigest,
						SiteRefs: siteRefs, NodeRefs: nodeRefs, DaemonBindings: append([]ApplyDaemonRequirement(nil), unit.daemonBindings[instance.nodeRef]...),
						ArtifactRefs:   applyInstanceArtifactRefs(instance.outputs),
						RuntimeAdapter: cloneApplyRuntimeAdapterRequirement(runtimeAdapterByWorkloadModule[moduleID]),
					}
					result.RuntimeInstances = append(result.RuntimeInstances, runtimeRequirement)
				}
				if instance.nodeRef != "" {
					targetNodes[instance.nodeRef] = struct{}{}
				}
				for inputRef, secretRef := range unit.secretRefs {
					secretSourceKind, secretSourceRef, secretSourceInputRef := defaultSecretSourceKind, defaultSecretSourceRef, inputRef
					if binding, bound := unit.secretBindings[inputRef]; bound {
						secretSourceKind, secretSourceRef, secretSourceInputRef = "capability", binding.capabilityRef, binding.key
					}
					result.Secrets = append(result.Secrets, ApplySecretRequirement{
						ID: moduleID + "/" + unit.id + "/" + instance.id + "/" + inputRef, OwnerKind: "render-instance", OwnerRef: instance.id,
						SourceKind: secretSourceKind, SourceRef: secretSourceRef, SourceInputRef: secretSourceInputRef,
						ModuleRef: moduleID, UnitRef: unit.id, InstanceRef: instance.id, InputRef: inputRef, SecretRef: secretRef,
						SiteRefs: append([]string(nil), siteRefs...), NodeRefs: append([]string(nil), nodeRefs...),
					})
				}
			}
		}
	}
	return nil
}

func applyInstanceArtifactRefs(outputs []applyInstanceOutput) []string {
	refs := make([]string, 0, len(outputs))
	for _, output := range outputs {
		refs = append(refs, output.artifactRef)
	}
	return refs
}
