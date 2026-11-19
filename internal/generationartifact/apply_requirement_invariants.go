package generationartifact

import "sort"

func validateApplyRequirementIDs(requirements ApplyRequirements) error {
	checks := []struct {
		path string
		ids  []string
	}{
		{path: "workloads", ids: applyRequirementIDs(requirements.Workloads, func(value ApplyWorkloadRequirement) string { return value.ID })},
		{path: "secrets", ids: applyRequirementIDs(requirements.Secrets, func(value ApplySecretRequirement) string { return value.ID })},
		{path: "runtimeInstances", ids: applyRequirementIDs(requirements.RuntimeInstances, func(value ApplyRuntimeRequirement) string { return value.ID })},
		{path: "artifacts", ids: applyRequirementIDs(requirements.Artifacts, func(value ApplyArtifactRequirement) string { return value.ID })},
		{path: "hosts", ids: applyRequirementIDs(requirements.Hosts, func(value ApplyHostRequirement) string { return value.NodeRef })},
		{path: "providerOwners", ids: applyRequirementIDs(requirements.ProviderOwners, func(value ApplyProviderOwnerRequirement) string { return value.ID })},
		{path: "accessBindings", ids: applyRequirementIDs(requirements.AccessBindings, func(value ApplyAccessBindingRequirement) string { return value.ID })},
		{path: "backupTargetBindings", ids: applyRequirementIDs(requirements.BackupTargetBindings, func(value ApplyBackupTargetBindingRequirement) string { return value.ID })},
		{path: "evidence", ids: applyRequirementIDs(requirements.EvidenceRequirements, func(value ApplyEvidenceRequirement) string { return value.ID })},
		{path: "health", ids: applyRequirementIDs(requirements.HealthRequirements, func(value ApplyHealthRequirement) string { return value.ID })},
	}
	for _, check := range checks {
		seen := make(map[string]struct{}, len(check.ids))
		for _, id := range check.ids {
			if _, duplicate := seen[id]; duplicate {
				return fail(ErrInvalidPlan, "resolvedPlan.applyRequirements."+check.path, "duplicate requirement ID %q", id)
			}
			seen[id] = struct{}{}
		}
	}
	return nil
}

func applyRequirementIDs[T any](values []T, id func(T) string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, id(value))
	}
	return result
}

func sortApplyRequirements(result *ApplyRequirements) {
	for index := range result.Workloads {
		sort.Strings(result.Workloads[index].SiteRefs)
		sort.Strings(result.Workloads[index].NodeRefs)
		sort.Strings(result.Workloads[index].InstanceRefs)
		sort.Strings(result.Workloads[index].EvidenceRefs)
	}
	for index := range result.RuntimeInstances {
		sort.Strings(result.RuntimeInstances[index].SiteRefs)
		sort.Strings(result.RuntimeInstances[index].NodeRefs)
		sort.Strings(result.RuntimeInstances[index].HealthGateRefs)
		sort.Strings(result.RuntimeInstances[index].EvidenceGateRefs)
		sort.Slice(result.RuntimeInstances[index].DaemonBindings, func(i, j int) bool {
			return result.RuntimeInstances[index].DaemonBindings[i].InstanceRef < result.RuntimeInstances[index].DaemonBindings[j].InstanceRef
		})
		sort.Strings(result.RuntimeInstances[index].ArtifactRefs)
		if result.RuntimeInstances[index].RuntimeAdapter != nil {
			sort.Strings(result.RuntimeInstances[index].RuntimeAdapter.ArtifactRefs)
			sort.Strings(result.RuntimeInstances[index].RuntimeAdapter.Capabilities)
			for agentIndex := range result.RuntimeInstances[index].RuntimeAdapter.Agents {
				sort.Strings(result.RuntimeInstances[index].RuntimeAdapter.Agents[agentIndex].ArtifactRefs)
			}
			sort.Slice(result.RuntimeInstances[index].RuntimeAdapter.Agents, func(left, right int) bool {
				return result.RuntimeInstances[index].RuntimeAdapter.Agents[left].ID < result.RuntimeInstances[index].RuntimeAdapter.Agents[right].ID
			})
		}
		sort.Strings(result.RuntimeInstances[index].AccessBindingRefs)
		sort.Strings(result.RuntimeInstances[index].BackupTargetBindingRefs)
	}
	for index := range result.Artifacts {
		sort.Strings(result.Artifacts[index].SiteRefs)
		sort.Strings(result.Artifacts[index].NodeRefs)
	}
	for index := range result.Secrets {
		sort.Strings(result.Secrets[index].SiteRefs)
		sort.Strings(result.Secrets[index].NodeRefs)
	}
	for index := range result.ProviderOwners {
		sort.Strings(result.ProviderOwners[index].SiteRefs)
		sort.Strings(result.ProviderOwners[index].NodeRefs)
		sort.Strings(result.ProviderOwners[index].EvidenceRefs)
		sort.Strings(result.ProviderOwners[index].HealthGateRefs)
		sort.Strings(result.ProviderOwners[index].EvidenceGateRefs)
	}
	for index := range result.AccessBindings {
		sort.Strings(result.AccessBindings[index].TargetNodeRefs)
	}
	for index := range result.BackupTargetBindings {
		sort.Strings(result.BackupTargetBindings[index].TargetNodeRefs)
	}
	for index := range result.EvidenceRequirements {
		sort.Strings(result.EvidenceRequirements[index].HealthGateRefs)
		sort.Strings(result.EvidenceRequirements[index].ArtifactRefs)
	}
	for index := range result.HealthRequirements {
		sort.Strings(result.HealthRequirements[index].SiteRefs)
		sort.Strings(result.HealthRequirements[index].NodeRefs)
		if result.HealthRequirements[index].Probe != nil {
			sort.Ints(result.HealthRequirements[index].Probe.ExpectedStatuses)
		}
	}
	sort.Slice(result.Workloads, func(i, j int) bool { return result.Workloads[i].ID < result.Workloads[j].ID })
	sort.Slice(result.Secrets, func(i, j int) bool { return result.Secrets[i].ID < result.Secrets[j].ID })
	sort.Slice(result.RuntimeInstances, func(i, j int) bool { return result.RuntimeInstances[i].ID < result.RuntimeInstances[j].ID })
	sort.Slice(result.Artifacts, func(i, j int) bool { return result.Artifacts[i].ID < result.Artifacts[j].ID })
	sort.Slice(result.Hosts, func(i, j int) bool { return result.Hosts[i].NodeRef < result.Hosts[j].NodeRef })
	sort.Slice(result.ProviderOwners, func(i, j int) bool { return result.ProviderOwners[i].ID < result.ProviderOwners[j].ID })
	sort.Slice(result.AccessBindings, func(i, j int) bool { return result.AccessBindings[i].ID < result.AccessBindings[j].ID })
	sort.Slice(result.BackupTargetBindings, func(i, j int) bool { return result.BackupTargetBindings[i].ID < result.BackupTargetBindings[j].ID })
	sort.Slice(result.EvidenceRequirements, func(i, j int) bool { return result.EvidenceRequirements[i].ID < result.EvidenceRequirements[j].ID })
	sort.Slice(result.HealthRequirements, func(i, j int) bool { return result.HealthRequirements[i].ID < result.HealthRequirements[j].ID })
}

func sortedApplyMapKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
