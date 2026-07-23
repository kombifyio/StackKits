package runtimeexecutor

// CloneExecutionRequest returns a deep copy safe to pass across an adapter
// boundary.
func CloneExecutionRequest(input ExecutionRequest) ExecutionRequest {
	result := input
	result.RuntimeTargets = append([]RuntimeTarget(nil), input.RuntimeTargets...)
	for i := range result.RuntimeTargets {
		result.RuntimeTargets[i].SiteRefs = append([]string(nil), input.RuntimeTargets[i].SiteRefs...)
		result.RuntimeTargets[i].NodeRefs = append([]string(nil), input.RuntimeTargets[i].NodeRefs...)
		result.RuntimeTargets[i].DaemonBindings = append([]DaemonTarget(nil), input.RuntimeTargets[i].DaemonBindings...)
		result.RuntimeTargets[i].ArtifactRefs = append([]string(nil), input.RuntimeTargets[i].ArtifactRefs...)
		if input.RuntimeTargets[i].RuntimeAdapter != nil {
			adapter := *input.RuntimeTargets[i].RuntimeAdapter
			adapter.ArtifactRefs = append([]string(nil), input.RuntimeTargets[i].RuntimeAdapter.ArtifactRefs...)
			adapter.Agents = append([]RuntimeAdapterAgentBinding(nil), input.RuntimeTargets[i].RuntimeAdapter.Agents...)
			for agentIndex := range adapter.Agents {
				adapter.Agents[agentIndex].ArtifactRefs = append([]string(nil), input.RuntimeTargets[i].RuntimeAdapter.Agents[agentIndex].ArtifactRefs...)
			}
			result.RuntimeTargets[i].RuntimeAdapter = &adapter
		}
		result.RuntimeTargets[i].AccessCapabilities = append([]AccessCapability(nil), input.RuntimeTargets[i].AccessCapabilities...)
		result.RuntimeTargets[i].AccessBindingRefs = append([]string(nil), input.RuntimeTargets[i].AccessBindingRefs...)
	}
	result.HealthTargets = append([]HealthTarget(nil), input.HealthTargets...)
	for i := range result.HealthTargets {
		result.HealthTargets[i].SiteRefs = append([]string(nil), input.HealthTargets[i].SiteRefs...)
		result.HealthTargets[i].NodeRefs = append([]string(nil), input.HealthTargets[i].NodeRefs...)
		if input.HealthTargets[i].Probe != nil {
			probe := *input.HealthTargets[i].Probe
			probe.ExpectedStatuses = append([]int(nil), input.HealthTargets[i].Probe.ExpectedStatuses...)
			result.HealthTargets[i].Probe = &probe
		}
	}
	result.AccessBindings = append([]AccessBinding(nil), input.AccessBindings...)
	for i := range result.AccessBindings {
		result.AccessBindings[i].TargetNodeRefs = append([]string(nil), input.AccessBindings[i].TargetNodeRefs...)
	}
	result.Artifacts = cloneArtifacts(input.Artifacts)
	return result
}

// CloneExecutionOutcome returns a deep copy of an adapter outcome.
func CloneExecutionOutcome(input ExecutionOutcome) ExecutionOutcome {
	input.Runtime = append([]RuntimeOutcome(nil), input.Runtime...)
	input.Health = append([]HealthOutcome(nil), input.Health...)
	return input
}

// CloneExecutionResult returns a deep copy of a verified result.
func CloneExecutionResult(input ExecutionResult) ExecutionResult {
	input.Runtime = append([]RuntimeOutcome(nil), input.Runtime...)
	input.Health = append([]HealthOutcome(nil), input.Health...)
	return input
}

func cloneArtifacts(input []Artifact) []Artifact {
	result := append([]Artifact(nil), input...)
	for i := range result {
		result[i].SiteRefs = append([]string(nil), input[i].SiteRefs...)
		result[i].NodeRefs = append([]string(nil), input[i].NodeRefs...)
		result[i].Content = append([]byte(nil), input[i].Content...)
	}
	return result
}
