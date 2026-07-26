package runtimeexecutorlocal

import (
	"errors"

	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
)

// exactOwnedArtifactWithPlanMetadata selects one target-owned artifact while
// allowing only the immutable resolved-plan metadata that both dispatcher
// layers deliberately retain for every child. This keeps executors fail-closed
// without mistaking authenticated plan context for another executable input.
func exactOwnedArtifactWithPlanMetadata(
	artifacts []runtimeexecutor.Artifact,
	expectedID string,
) (runtimeexecutor.Artifact, error) {
	var selected runtimeexecutor.Artifact
	matches := 0
	for _, candidate := range artifacts {
		if candidate.ID == expectedID {
			selected = candidate
			matches++
			continue
		}
		if candidate.OwnerKind != "plan" ||
			candidate.ExecutionClass != runtimeexecutor.ArtifactExecutionClassPlan ||
			candidate.Kind != "metadata" || candidate.Format != "json" {
			return runtimeexecutor.Artifact{}, errors.New("request contains an unrelated executable artifact")
		}
	}
	if matches != 1 {
		return runtimeexecutor.Artifact{}, errors.New("request does not contain exactly one governed artifact")
	}
	return selected, nil
}
