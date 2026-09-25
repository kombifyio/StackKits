package nativehost

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
)

// ResolvedPlanArtifactID is the plan-owned metadata artifact that accompanies
// every runtime request: the canonical ResolvedPlan the request was derived
// from.
const ResolvedPlanArtifactID = "resolved-plan"

// GenerationTargetFromArtifacts returns the generation target of the resolved
// plan carried by a runtime request. A request without the plan artifact
// yields "" (the native executor path). A plan artifact whose bytes do not
// match its digest, or that cannot be decoded, fails closed.
func GenerationTargetFromArtifacts(artifacts []runtimeexecutor.Artifact) (string, error) {
	found := false
	target := ""
	for _, artifact := range artifacts {
		if artifact.ID != ResolvedPlanArtifactID {
			continue
		}
		if found {
			return "", errors.New("runtime request carries more than one resolved plan")
		}
		found = true
		if artifact.OwnerKind != "plan" || artifact.Kind != "metadata" || artifact.Format != "json" {
			return "", errors.New("runtime request resolved plan is not the plan-owned metadata artifact")
		}
		sum := sha256.Sum256(artifact.Content)
		if artifact.Digest != "sha256:"+hex.EncodeToString(sum[:]) {
			return "", errors.New("runtime request resolved plan digest does not match its content")
		}
		var document struct {
			Generation struct {
				Target string `json:"target"`
			} `json:"generation"`
		}
		if err := json.Unmarshal(artifact.Content, &document); err != nil {
			return "", fmt.Errorf("decode the runtime request resolved plan: %w", err)
		}
		target = document.Generation.Target
	}
	return target, nil
}

// GenerationTargetExecutesOpenTofu reports whether a generation target runs
// its runtime through OpenTofu roots (ADR-0045 Stage 1). Every other target,
// including compose and an absent plan, keeps the native executors.
func GenerationTargetExecutesOpenTofu(target string) bool {
	return target == "opentofu" || target == "terramate"
}
