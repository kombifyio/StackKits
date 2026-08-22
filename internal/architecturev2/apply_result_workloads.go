package architecturev2

import (
	"reflect"
	"sort"

	"github.com/kombifyio/stackkits/internal/generationartifact"
)

func projectAppliedWorkloadIdentities(requirements generationartifact.ApplyRequirements, artifactDigests map[string]string) ([]AppliedWorkloadIdentity, error) {
	result := make([]AppliedWorkloadIdentity, 0)
	for _, requirement := range requirements.RuntimeInstances {
		if requirement.WorkloadRef == "" {
			continue
		}
		runtimeOwnerRef := requirement.OwnerRef
		artifactRefs := append([]string{}, requirement.ArtifactRefs...)
		if requirement.RuntimeAdapter != nil {
			runtimeOwnerRef = requirement.RuntimeAdapter.ID
			artifactRefs = append(artifactRefs, requirement.RuntimeAdapter.ArtifactRefs...)
			for _, agent := range requirement.RuntimeAdapter.Agents {
				artifactRefs = append(artifactRefs, agent.ArtifactRefs...)
			}
		}
		seenArtifacts := make(map[string]struct{}, len(artifactRefs))
		artifacts := make([]AppliedArtifactIdentity, 0, len(artifactRefs))
		for _, ref := range artifactRefs {
			if _, seen := seenArtifacts[ref]; seen {
				continue
			}
			digest, ok := artifactDigests[ref]
			if !ok || !validApplySHA256(digest) {
				return nil, applyExecutorError(generationartifact.ErrArtifactMissing, "apply.result.appliedWorkloads", "workload %q references artifact %q without immutable digest custody", nil, requirement.WorkloadRef, ref)
			}
			seenArtifacts[ref] = struct{}{}
			artifacts = append(artifacts, AppliedArtifactIdentity{Ref: ref, Digest: digest})
		}
		sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].Ref < artifacts[j].Ref })
		placements, err := appliedWorkloadPlacements(requirement, requirements.Hosts)
		if err != nil {
			return nil, err
		}
		result = append(result, AppliedWorkloadIdentity{
			WorkloadRef: requirement.WorkloadRef, RequirementID: requirement.ID,
			InstanceRef: requirement.InstanceRef, RuntimeOwnerRef: runtimeOwnerRef,
			Placements: placements, Artifacts: artifacts,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].WorkloadRef != result[j].WorkloadRef {
			return result[i].WorkloadRef < result[j].WorkloadRef
		}
		if result[i].RequirementID != result[j].RequirementID {
			return result[i].RequirementID < result[j].RequirementID
		}
		return result[i].InstanceRef < result[j].InstanceRef
	})
	return result, nil
}

func appliedWorkloadPlacements(requirement generationartifact.ApplyRuntimeRequirement, hosts []generationartifact.ApplyHostRequirement) ([]AppliedWorkloadPlacement, error) {
	placements := make([]AppliedWorkloadPlacement, 0, len(requirement.NodeRefs))
	seenNodes := make(map[string]struct{}, len(requirement.NodeRefs))
	seenSites := make(map[string]struct{}, len(requirement.SiteRefs))
	for _, nodeRef := range requirement.NodeRefs {
		if _, duplicate := seenNodes[nodeRef]; duplicate {
			return nil, applyExecutorError(generationartifact.ErrBindingMismatch, "apply.result.appliedWorkloads", "workload %q repeats node placement %q", nil, requirement.WorkloadRef, nodeRef)
		}
		seenNodes[nodeRef] = struct{}{}
		var matched *generationartifact.ApplyHostRequirement
		for index := range hosts {
			if hosts[index].NodeRef != nodeRef {
				continue
			}
			if matched != nil {
				return nil, applyExecutorError(generationartifact.ErrBindingMismatch, "apply.result.appliedWorkloads", "workload %q has multiple host authorities for node %q", nil, requirement.WorkloadRef, nodeRef)
			}
			matched = &hosts[index]
		}
		if matched == nil || !containsApplyString(requirement.SiteRefs, matched.SiteRef) {
			return nil, applyExecutorError(generationartifact.ErrBindingMismatch, "apply.result.appliedWorkloads", "workload %q has no exact host placement for node %q", nil, requirement.WorkloadRef, nodeRef)
		}
		placements = append(placements, AppliedWorkloadPlacement{
			SiteRef: matched.SiteRef, NodeRef: matched.NodeRef, ExecutionChannelRef: matched.ExecutionChannelRef,
		})
		seenSites[matched.SiteRef] = struct{}{}
	}
	for _, siteRef := range requirement.SiteRefs {
		if _, represented := seenSites[siteRef]; !represented {
			return nil, applyExecutorError(generationartifact.ErrBindingMismatch, "apply.result.appliedWorkloads", "workload %q has Site %q without a node placement", nil, requirement.WorkloadRef, siteRef)
		}
	}
	sort.Slice(placements, func(i, j int) bool {
		if placements[i].SiteRef != placements[j].SiteRef {
			return placements[i].SiteRef < placements[j].SiteRef
		}
		return placements[i].NodeRef < placements[j].NodeRef
	})
	return placements, nil
}

func snapshotArtifactDigests(artifacts []applyArtifactSnapshot) map[string]string {
	result := make(map[string]string, len(artifacts))
	for _, artifact := range artifacts {
		result[artifact.ID] = artifact.SHA256
	}
	return result
}

func manifestArtifactDigests(manifest generationartifact.ArtifactManifest) map[string]string {
	result := make(map[string]string, len(manifest.Artifacts))
	for _, artifact := range manifest.Artifacts {
		result[artifact.ID] = artifact.SHA256
	}
	return result
}

func cloneAppliedWorkloadIdentities(input []AppliedWorkloadIdentity) []AppliedWorkloadIdentity {
	result := append([]AppliedWorkloadIdentity{}, input...)
	for index := range result {
		result[index].Placements = append([]AppliedWorkloadPlacement{}, input[index].Placements...)
		result[index].Artifacts = append([]AppliedArtifactIdentity{}, input[index].Artifacts...)
	}
	return result
}

func equalAppliedWorkloadIdentities(left, right []AppliedWorkloadIdentity) bool {
	return reflect.DeepEqual(left, right)
}
