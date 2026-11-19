package generationartifact

import (
	"fmt"
	"slices"

	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

func appendApplyArtifactRequirements(plan resolvedplan.ResolvedPlan, binding PlanBinding, modules map[string]applyModule, result *ApplyRequirements) error {
	generation, err := requiredObject(plan, "generation", "resolvedPlan.generation")
	if err != nil {
		return err
	}
	rawArtifacts, ok := generation["artifacts"].([]any)
	if !ok {
		return fail(ErrInvalidPlan, "resolvedPlan.generation.artifacts", "must be an array")
	}
	for index, raw := range rawArtifacts {
		path := fmt.Sprintf("resolvedPlan.generation.artifacts[%d]", index)
		artifact, ok := raw.(map[string]any)
		if !ok {
			return fail(ErrInvalidPlan, path, "must be an object")
		}
		requirement := ApplyArtifactRequirement{ExecutionClass: ApplyExecutionClassPlan, SiteRefs: []string{}, NodeRefs: []string{}}
		if requirement.ID, err = requiredString(artifact, "id", path+".id"); err != nil {
			return err
		}
		if requirement.Kind, err = requiredString(artifact, "kind", path+".kind"); err != nil {
			return err
		}
		if requirement.Format, err = requiredString(artifact, "format", path+".format"); err != nil {
			return err
		}
		if requirement.Mode, err = requiredString(artifact, "mode", path+".mode"); err != nil {
			return err
		}
		owner, err := requiredObject(artifact, "owner", path+".owner")
		if err != nil {
			return err
		}
		if requirement.OwnerKind, err = requiredString(owner, "kind", path+".owner.kind"); err != nil {
			return err
		}
		switch requirement.OwnerKind {
		case "plan":
			requirement.OwnerRef = binding.PlanHash
			requirement.OwnerContractHash = binding.PlanHash
		case "render-instance":
			if requirement.ModuleRef, err = requiredString(owner, "moduleRef", path+".owner.moduleRef"); err != nil {
				return err
			}
			module, exists := modules[requirement.ModuleRef]
			if !exists {
				return fail(ErrInvalidPlan, path+".owner.moduleRef", "module %q is absent", requirement.ModuleRef)
			}
			requirement.ExecutionClass = module.executionClass
			requirement.ProviderRef = module.providerRef
			requirement.ProviderContractHash = module.providerContractHash
			requirement.ModuleContractHash = module.contractHash
			if requirement.UnitRef, err = requiredString(owner, "unitRef", path+".owner.unitRef"); err != nil {
				return err
			}
			if requirement.InstanceRef, err = requiredString(owner, "instanceRef", path+".owner.instanceRef"); err != nil {
				return err
			}
			requirement.OwnerRef = requirement.InstanceRef
			if requirement.OutputRef, err = requiredString(owner, "outputRef", path+".owner.outputRef"); err != nil {
				return err
			}
			unit, instance, output, found := findApplyArtifactOwner(module, requirement.UnitRef, requirement.InstanceRef, requirement.ID)
			if !found || output.outputRef != requirement.OutputRef {
				return fail(ErrInvalidPlan, path+".owner", "artifact owner does not match an exact module/unit/instance output")
			}
			requirement.UnitContractHash = unit.contractHash
			if unit.applyMode == ApplyExecutionClassArtifactOnly {
				requirement.ExecutionClass = ApplyExecutionClassArtifactOnly
			}
			requirement.OwnerContractHash = unit.contractHash
			requirement.SiteRefs = append([]string(nil), module.siteRefs...)
			requirement.NodeRefs = append([]string(nil), module.nodeRefs...)
			if instance.siteRef != "" {
				requirement.SiteRefs = []string{instance.siteRef}
			}
			if instance.nodeRef != "" {
				requirement.NodeRefs = []string{instance.nodeRef}
			}
		default:
			return fail(ErrInvalidPlan, path+".owner.kind", "unsupported artifact owner kind %q", requirement.OwnerKind)
		}
		result.Artifacts = append(result.Artifacts, requirement)
	}
	return nil
}

func findApplyArtifactOwner(module applyModule, unitRef, instanceRef, artifactRef string) (applyUnit, applyInstance, applyInstanceOutput, bool) {
	for _, unit := range module.units {
		if unit.id != unitRef {
			continue
		}
		for _, instance := range unit.instances {
			if instance.id != instanceRef {
				continue
			}
			for _, output := range instance.outputs {
				if output.artifactRef == artifactRef {
					return unit, instance, output, true
				}
			}
		}
	}
	return applyUnit{}, applyInstance{}, applyInstanceOutput{}, false
}

func validateApplyArtifactRuntimeClosure(requirements ApplyRequirements) error {
	artifacts := make(map[string]ApplyArtifactRequirement, len(requirements.Artifacts))
	for _, artifact := range requirements.Artifacts {
		if _, duplicate := artifacts[artifact.ID]; duplicate {
			return fail(ErrInvalidPlan, "resolvedPlan.applyRequirements.artifacts", "duplicate artifact requirement %q", artifact.ID)
		}
		artifacts[artifact.ID] = artifact
	}
	runtimeReferenced := make(map[string]int, len(artifacts))
	adapterReferenced := make(map[string]int, len(artifacts))
	for _, runtime := range requirements.RuntimeInstances {
		seen := make(map[string]struct{}, len(runtime.ArtifactRefs))
		for _, artifactRef := range runtime.ArtifactRefs {
			if _, duplicate := seen[artifactRef]; duplicate {
				return fail(ErrInvalidPlan, "resolvedPlan.applyRequirements.runtimeInstances."+runtime.ID+".artifactRefs", "duplicate artifact reference %q", artifactRef)
			}
			seen[artifactRef] = struct{}{}
			artifact, exists := artifacts[artifactRef]
			if !exists {
				return fail(ErrInvalidPlan, "resolvedPlan.applyRequirements.runtimeInstances."+runtime.ID+".artifactRefs", "artifact %q is absent", artifactRef)
			}
			if artifact.OwnerKind != "render-instance" || artifact.OwnerRef != runtime.InstanceRef || artifact.ProviderRef != runtime.ProviderRef ||
				artifact.ProviderContractHash != runtime.ProviderContractHash || artifact.ModuleRef != runtime.ModuleRef || artifact.ModuleContractHash != runtime.ModuleContractHash ||
				artifact.UnitRef != runtime.UnitRef || artifact.UnitContractHash != runtime.UnitContractHash || artifact.OwnerContractHash != runtime.UnitContractHash ||
				artifact.InstanceRef != runtime.InstanceRef || !slices.Equal(artifact.SiteRefs, runtime.SiteRefs) || !slices.Equal(artifact.NodeRefs, runtime.NodeRefs) {
				return fail(ErrInvalidPlan, "resolvedPlan.applyRequirements.runtimeInstances."+runtime.ID+".artifactRefs", "artifact %q is not owned by the exact runtime instance", artifactRef)
			}
			runtimeReferenced[artifactRef]++
		}
		if runtime.RuntimeDelivery == applicationAdapterRuntimeDelivery && runtime.RuntimeAdapter == nil {
			return fail(ErrInvalidPlan, "resolvedPlan.applyRequirements.runtimeInstances."+runtime.ID+".runtimeAdapter", "application-adapter runtime requires one exact adapter")
		}
		if runtime.RuntimeAdapter != nil {
			if runtime.RuntimeDelivery != applicationAdapterRuntimeDelivery || runtime.WorkloadRef == "" {
				return fail(ErrInvalidPlan, "resolvedPlan.applyRequirements.runtimeInstances."+runtime.ID+".runtimeAdapter", "is valid only for an application-adapter workload")
			}
			adapter := runtime.RuntimeAdapter
			if err := validateApplyRuntimeAdapterArtifacts(runtime.ID, "runtimeAdapter", adapter.ProviderRef, adapter.ProviderContractHash, adapter.ModuleRef, adapter.ModuleContractHash, adapter.ArtifactRefs, artifacts, adapterReferenced); err != nil {
				return err
			}
			seenAgents := map[string]struct{}{}
			for index, agent := range adapter.Agents {
				if _, duplicate := seenAgents[agent.ID]; duplicate {
					return fail(ErrInvalidPlan, "resolvedPlan.applyRequirements.runtimeInstances."+runtime.ID+".runtimeAdapter.agents", "duplicate agent %q", agent.ID)
				}
				seenAgents[agent.ID] = struct{}{}
				field := fmt.Sprintf("runtimeAdapter.agents[%d]", index)
				if err := validateApplyRuntimeAdapterArtifacts(runtime.ID, field, adapter.ProviderRef, adapter.ProviderContractHash, agent.ModuleRef, agent.ModuleContractHash, agent.ArtifactRefs, artifacts, adapterReferenced); err != nil {
					return err
				}
			}
		}
	}
	for _, artifact := range requirements.Artifacts {
		switch artifact.OwnerKind {
		case "plan":
			if artifact.ExecutionClass != ApplyExecutionClassPlan {
				return fail(ErrInvalidPlan, "resolvedPlan.applyRequirements.artifacts."+artifact.ID+".executionClass", "plan-owned metadata must use the plan execution class")
			}
			if runtimeReferenced[artifact.ID] != 0 || adapterReferenced[artifact.ID] != 0 {
				return fail(ErrInvalidPlan, "resolvedPlan.applyRequirements.artifacts."+artifact.ID, "plan-owned metadata must not be assigned to a runtime instance")
			}
		case "render-instance":
			switch artifact.ExecutionClass {
			case ApplyExecutionClassExecutable:
				if runtimeReferenced[artifact.ID] != 1 || adapterReferenced[artifact.ID] != 0 {
					return fail(ErrInvalidPlan, "resolvedPlan.applyRequirements.artifacts."+artifact.ID, "executable render-instance artifact must be owned by exactly one runtime and no adapter; got runtime=%d adapter=%d", runtimeReferenced[artifact.ID], adapterReferenced[artifact.ID])
				}
			case ApplyExecutionClassContractHandoff:
				if runtimeReferenced[artifact.ID] != 0 {
					return fail(ErrInvalidPlan, "resolvedPlan.applyRequirements.artifacts."+artifact.ID, "contract-handoff artifact must not be assigned to an executable runtime")
				}
			case ApplyExecutionClassArtifactOnly:
				if runtimeReferenced[artifact.ID] != 0 || adapterReferenced[artifact.ID] != 0 {
					return fail(ErrInvalidPlan, "resolvedPlan.applyRequirements.artifacts."+artifact.ID, "artifact-only render-instance must not be assigned to a runtime or adapter")
				}
			default:
				return fail(ErrInvalidPlan, "resolvedPlan.applyRequirements.artifacts."+artifact.ID+".executionClass", "render-instance artifact requires an executable, contract-handoff, or artifact-only execution class")
			}
		default:
			return fail(ErrInvalidPlan, "resolvedPlan.applyRequirements.artifacts."+artifact.ID+".ownerKind", "unsupported owner kind %q", artifact.OwnerKind)
		}
	}
	return nil
}

func validateApplyRuntimeAdapterArtifacts(runtimeID, field, providerRef, providerHash, moduleRef, moduleHash string, refs []string, artifacts map[string]ApplyArtifactRequirement, referenced map[string]int) error {
	if providerRef == "" || moduleRef == "" || !validSHA256(providerHash) || !validSHA256(moduleHash) || len(refs) == 0 {
		return fail(ErrInvalidPlan, "resolvedPlan.applyRequirements.runtimeInstances."+runtimeID+"."+field, "adapter authority and artifact refs must be complete")
	}
	seen := map[string]struct{}{}
	for _, ref := range refs {
		if _, duplicate := seen[ref]; duplicate {
			return fail(ErrInvalidPlan, "resolvedPlan.applyRequirements.runtimeInstances."+runtimeID+"."+field+".artifactRefs", "duplicate artifact %q", ref)
		}
		seen[ref] = struct{}{}
		artifact, exists := artifacts[ref]
		if !exists || artifact.ExecutionClass != ApplyExecutionClassContractHandoff || artifact.ProviderRef != providerRef || artifact.ProviderContractHash != providerHash || artifact.ModuleRef != moduleRef || artifact.ModuleContractHash != moduleHash {
			return fail(ErrInvalidPlan, "resolvedPlan.applyRequirements.runtimeInstances."+runtimeID+"."+field+".artifactRefs", "artifact %q does not match the exact contract-handoff module", ref)
		}
		referenced[ref]++
	}
	return nil
}
