package runtimeexecutor

import (
	"fmt"
)

func validateArtifacts(artifacts []Artifact) error {
	seen := map[string]struct{}{}
	total := 0
	for i, artifact := range artifacts {
		prefix := fmt.Sprintf("artifacts[%d]", i)
		if err := validateArtifactOwner(prefix, artifact); err != nil {
			return err
		}
		if err := validateArtifactProvider(prefix, artifact); err != nil {
			return err
		}
		if err := validateArtifactModuleAuthority(prefix, artifact); err != nil {
			return err
		}
		if err := validateArtifactPlacement(prefix, artifact); err != nil {
			return err
		}
		if err := validateArtifactScope(prefix, artifact); err != nil {
			return err
		}
		if artifact.Content == nil || len(artifact.Content) > MaxArtifactBytes {
			return invalidRequest(prefix+".content", "must be present and no larger than %d bytes", MaxArtifactBytes)
		}
		total += len(artifact.Content)
		if total > MaxTotalArtifactBytes {
			return invalidRequest("artifacts", "total content exceeds %d bytes", MaxTotalArtifactBytes)
		}
		if hashBytes(artifact.Content) != artifact.Digest {
			return invalidRequest(prefix+".digest", "does not match content")
		}
		if _, exists := seen[artifact.ID]; exists {
			return invalidRequest(prefix+".id", "duplicate artifact")
		}
		seen[artifact.ID] = struct{}{}
	}
	return nil
}

func validateArtifactOwner(prefix string, artifact Artifact) error {
	for _, value := range []struct{ name, value string }{{"id", artifact.ID}, {"kind", artifact.Kind}, {"format", artifact.Format}, {"mode", artifact.Mode}} {
		if err := validateToken(prefix+"."+value.name, value.value); err != nil {
			return err
		}
	}
	if !validDigest(artifact.OwnerContractHash) {
		return invalidRequest(prefix+".owner_contract_hash", "must be a canonical SHA-256 digest")
	}
	if artifact.OwnerKind != artifactOwnerPlan && artifact.OwnerKind != artifactOwnerRenderInstance {
		return invalidRequest(prefix+".owner_kind", "must be plan or render-instance")
	}
	switch artifact.ExecutionClass {
	case ArtifactExecutionClassPlan:
		if artifact.OwnerKind != artifactOwnerPlan {
			return invalidRequest(prefix+".execution_class", "plan execution class requires plan ownership")
		}
	case ArtifactExecutionClassExecutable, ArtifactExecutionClassContractHandoff:
		if artifact.OwnerKind != artifactOwnerRenderInstance {
			return invalidRequest(prefix+".execution_class", "runtime execution class requires render-instance ownership")
		}
	default:
		return invalidRequest(prefix+".execution_class", "must be executable, contract-handoff, or plan")
	}
	if artifact.OwnerKind == artifactOwnerPlan {
		if !validDigest(artifact.OwnerRef) {
			return invalidRequest(prefix+".owner_ref", "plan owner ref must be a canonical SHA-256 plan digest")
		}
	} else if err := validateToken(prefix+".owner_ref", artifact.OwnerRef); err != nil {
		return err
	}
	return nil
}

func validateArtifactProvider(prefix string, artifact Artifact) error {
	if (artifact.ProviderRef == "") != (artifact.ProviderContractHash == "") {
		return invalidRequest(prefix, "provider ref and contract hash must be paired")
	}
	if artifact.ProviderRef != "" {
		if err := validateToken(prefix+".provider_ref", artifact.ProviderRef); err != nil {
			return err
		}
		if !validDigest(artifact.ProviderContractHash) {
			return invalidRequest(prefix+".provider_contract_hash", "must be a canonical SHA-256 digest")
		}
	}
	return nil
}

func validateArtifactModuleAuthority(prefix string, artifact Artifact) error {
	if (artifact.ModuleRef == "") != (artifact.ModuleContractHash == "") || (artifact.UnitRef == "") != (artifact.UnitContractHash == "") {
		return invalidRequest(prefix, "module/unit refs and contract hashes must be paired")
	}
	if artifact.ModuleRef != "" {
		if err := validateToken(prefix+".module_ref", artifact.ModuleRef); err != nil {
			return err
		}
		if !validDigest(artifact.ModuleContractHash) {
			return invalidRequest(prefix+".module_contract_hash", "must be a canonical SHA-256 digest")
		}
	}
	if artifact.UnitRef != "" {
		if artifact.ModuleRef == "" {
			return invalidRequest(prefix, "unit authority requires module authority")
		}
		if err := validateToken(prefix+".unit_ref", artifact.UnitRef); err != nil {
			return err
		}
		if !validDigest(artifact.UnitContractHash) {
			return invalidRequest(prefix+".unit_contract_hash", "must be a canonical SHA-256 digest")
		}
	}
	if artifact.InstanceRef != "" {
		if artifact.UnitRef == "" {
			return invalidRequest(prefix, "instance authority requires unit authority")
		}
		if err := validateToken(prefix+".instance_ref", artifact.InstanceRef); err != nil {
			return err
		}
	}
	return nil
}

func validateArtifactPlacement(prefix string, artifact Artifact) error {
	minimumPlacement := 0
	if artifact.OwnerKind == artifactOwnerRenderInstance {
		minimumPlacement = 1
	}
	if err := validateRefList(prefix+".site_refs", artifact.SiteRefs, minimumPlacement, MaxSiteRefsPerTarget); err != nil {
		return err
	}
	if err := validateRefList(prefix+".node_refs", artifact.NodeRefs, minimumPlacement, MaxNodeRefsPerTarget); err != nil {
		return err
	}
	return nil
}

func validateArtifactScope(prefix string, artifact Artifact) error {
	if artifact.OwnerKind == artifactOwnerPlan {
		return validatePlanArtifactScope(prefix, artifact)
	}
	return validateRuntimeArtifactScope(prefix, artifact)
}

func validatePlanArtifactScope(prefix string, artifact Artifact) error {
	if artifact.ProviderRef != "" || artifact.ProviderContractHash != "" || artifact.ModuleRef != "" || artifact.ModuleContractHash != "" || artifact.UnitRef != "" || artifact.UnitContractHash != "" || artifact.InstanceRef != "" || artifact.OutputRef != "" || len(artifact.SiteRefs) != 0 || len(artifact.NodeRefs) != 0 {
		return invalidRequest(prefix, "plan-owned artifact must not carry runtime owner, placement, or output authority")
	}
	return nil
}

func validateRuntimeArtifactScope(prefix string, artifact Artifact) error {
	if artifact.ProviderRef == "" || artifact.ModuleRef == "" || artifact.UnitRef == "" || artifact.InstanceRef == "" || artifact.OutputRef == "" {
		return invalidRequest(prefix, "render-instance artifact requires full provider/module/unit/instance/output authority")
	}
	if err := validateToken(prefix+".output_ref", artifact.OutputRef); err != nil {
		return err
	}
	return nil
}

func validateArtifactClosure(targets []RuntimeTarget, artifacts []Artifact, planHash string) error {
	available := make(map[string]Artifact, len(artifacts))
	runtimeReferenceCount := make(map[string]int, len(artifacts))
	adapterReferenceCount := make(map[string]int, len(artifacts))
	for _, artifact := range artifacts {
		available[artifact.ID] = artifact
		if artifact.OwnerKind == artifactOwnerPlan && (artifact.OwnerRef != planHash || artifact.OwnerContractHash != planHash) {
			return invalidRequest("artifacts."+artifact.ID, "plan-owned artifact owner ref and contract hash must both bind the exact plan hash")
		}
	}
	for targetIndex, target := range targets {
		for refIndex, ref := range target.ArtifactRefs {
			artifact, exists := available[ref]
			if !exists {
				return invalidRequest(fmt.Sprintf("runtime_targets[%d].artifact_refs[%d]", targetIndex, refIndex), "references absent artifact %q", ref)
			}
			if artifact.ExecutionClass != ArtifactExecutionClassExecutable {
				return invalidRequest(fmt.Sprintf("runtime_targets[%d].artifact_refs[%d]", targetIndex, refIndex), "artifact %q must be executable runtime material", ref)
			}
			if !artifactMatchesRuntimeTarget(artifact, target) {
				return invalidRequest(fmt.Sprintf("runtime_targets[%d].artifact_refs[%d]", targetIndex, refIndex), "render-instance artifact %q identity does not exactly match runtime target", ref)
			}
			runtimeReferenceCount[ref]++
		}
		if err := validateTargetAdapterArtifacts(targetIndex, target, available, adapterReferenceCount); err != nil {
			return err
		}

	}
	return validateArtifactReferenceCounts(artifacts, runtimeReferenceCount, adapterReferenceCount)
}

func validateTargetAdapterArtifacts(targetIndex int, target RuntimeTarget, available map[string]Artifact, adapterReferenceCount map[string]int) error {
	if target.RuntimeAdapter != nil {
		adapter := *target.RuntimeAdapter
		if err := validateAdapterArtifactRefs(targetIndex, "runtime_adapter", adapter.ProviderRef, adapter.ProviderContractHash, adapter.ModuleRef, adapter.ModuleContractHash, adapter.ArtifactRefs, available, adapterReferenceCount); err != nil {
			return err
		}
		for agentIndex, agent := range adapter.Agents {
			field := fmt.Sprintf("runtime_adapter.agents[%d]", agentIndex)
			if err := validateAdapterArtifactRefs(targetIndex, field, adapter.ProviderRef, adapter.ProviderContractHash, agent.ModuleRef, agent.ModuleContractHash, agent.ArtifactRefs, available, adapterReferenceCount); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateArtifactReferenceCounts(artifacts []Artifact, runtimeReferenceCount, adapterReferenceCount map[string]int) error {
	for _, artifact := range artifacts {
		switch artifact.ExecutionClass {
		case ArtifactExecutionClassExecutable:
			if runtimeReferenceCount[artifact.ID] != 1 || adapterReferenceCount[artifact.ID] != 0 {
				return invalidRequest("artifacts."+artifact.ID, "executable artifact must be referenced by exactly one runtime target and no adapter; got runtime=%d adapter=%d", runtimeReferenceCount[artifact.ID], adapterReferenceCount[artifact.ID])
			}
		case ArtifactExecutionClassContractHandoff:
			if runtimeReferenceCount[artifact.ID] != 0 || adapterReferenceCount[artifact.ID] == 0 {
				return invalidRequest("artifacts."+artifact.ID, "contract-handoff artifact must be referenced by at least one adapter binding and no runtime target")
			}
		case ArtifactExecutionClassPlan:
			if runtimeReferenceCount[artifact.ID] != 0 || adapterReferenceCount[artifact.ID] != 0 {
				return invalidRequest("artifacts."+artifact.ID, "plan-owned artifact must not be runtime- or adapter-referenced")
			}
		}
	}
	return nil
}

func validateAdapterArtifactRefs(targetIndex int, field, providerRef, providerHash, moduleRef, moduleHash string, refs []string, available map[string]Artifact, referenceCount map[string]int) error {
	for refIndex, ref := range refs {
		prefix := fmt.Sprintf("runtime_targets[%d].%s.artifact_refs[%d]", targetIndex, field, refIndex)
		artifact, exists := available[ref]
		if !exists {
			return invalidRequest(prefix, "references absent adapter artifact %q", ref)
		}
		if artifact.ExecutionClass != ArtifactExecutionClassContractHandoff {
			return invalidRequest(prefix, "artifact %q must be contract-handoff material", ref)
		}
		if artifact.ProviderRef != providerRef || artifact.ProviderContractHash != providerHash || artifact.ModuleRef != moduleRef || artifact.ModuleContractHash != moduleHash {
			return invalidRequest(prefix, "artifact %q identity does not match the exact adapter module authority", ref)
		}
		referenceCount[ref]++
	}
	return nil
}

func artifactMatchesRuntimeTarget(artifact Artifact, target RuntimeTarget) bool {
	return artifact.OwnerKind == artifactOwnerRenderInstance && artifact.OwnerRef == target.InstanceRef && artifact.OwnerContractHash == target.UnitContractHash &&
		artifact.ProviderRef == target.ProviderRef && artifact.ProviderContractHash == target.ProviderContractHash &&
		artifact.ModuleRef == target.ModuleRef && artifact.ModuleContractHash == target.ModuleContractHash &&
		artifact.UnitRef == target.UnitRef && artifact.UnitContractHash == target.UnitContractHash && artifact.InstanceRef == target.InstanceRef &&
		equalStrings(artifact.SiteRefs, target.SiteRefs) && equalStrings(artifact.NodeRefs, target.NodeRefs)
}
