package upgradelifecycle

import (
	"errors"
	"fmt"
	"path"

	"github.com/kombifyio/stackkits/internal/restoreactivation"
)

const runtimeRecoveryGraphArtifactID = "runtime-recovery-graph"

// currentStateRuntimeRecoveryGraph captures the already CUE-verified runtime
// graph alongside the exact runtime files. The existing snapshot signature
// retains that acceptance; graph data alone is never a fresh CUE authority.
func currentStateRuntimeRecoveryGraph(input CurrentStateAuthorityInput) (ExecutorStateBlobInput, error) {
	graph, err := restoreactivation.DeriveRuntimeRecoveryGraph(
		input.WorkspaceRoot, input.Plan, input.Manifest, input.Capture.OperationID,
	)
	if err != nil {
		return ExecutorStateBlobInput{}, fmt.Errorf("current state authority: derive runtime recovery graph: %w", err)
	}
	paths := make(map[string]string, len(input.Capture.Artifacts))
	manifestPaths := make(map[string]string, len(input.Manifest.Artifacts))
	for _, artifact := range input.Manifest.Artifacts {
		manifestPaths[artifact.ID] = artifact.Path
	}
	for _, artifact := range input.Capture.Artifacts {
		target := artifact.Path
		if generatedPath, ok := manifestPaths[artifact.ID]; ok {
			// Core Compose recovery historically stores its output-relative
			// path; the generation manifest owns its workspace-relative path.
			target = generatedPath
		}
		if _, duplicate := paths[target]; duplicate {
			return ExecutorStateBlobInput{}, errors.New("current state authority: runtime recovery paths collide")
		}
		paths[target] = executorStateDigest(artifact.Data)
	}
	// Under an OpenTofu target the runtime Compose files, .env files and
	// states the graph binds are the captured OpenTofu root files.
	for _, blob := range executorStateOpenTofuBlobInputs(input.Capture.RuntimeOpenTofu) {
		if _, duplicate := paths[blob.Path]; duplicate {
			return ExecutorStateBlobInput{}, errors.New("current state authority: runtime recovery paths collide")
		}
		paths[blob.Path] = executorStateDigest(blob.Data)
	}
	if graph.RenderTarget != "" && graph.RenderTarget != input.Capture.GenerationTarget ||
		graph.RenderTarget == "" && input.Capture.GenerationTarget != executorStateTargetCompose {
		return ExecutorStateBlobInput{}, errors.New("current state authority: recovery graph render target differs from the capture")
	}
	if paths[graph.CorePolicyPath] != graph.CorePolicyDigest {
		return ExecutorStateBlobInput{}, errors.New("current state authority: recovery graph policy differs from captured bytes")
	}
	for _, runtime := range graph.ComposeRuntimes {
		if paths[runtime.Path] != runtime.Digest ||
			(runtime.EnvironmentPath != "" && paths[runtime.EnvironmentPath] != runtime.EnvironmentDigest) ||
			(runtime.StatePath != "" && paths[runtime.StatePath] != runtime.StateDigest) {
			return ExecutorStateBlobInput{}, errors.New("current state authority: recovery graph differs from captured runtime files")
		}
	}
	canonical, err := graph.MarshalCanonical()
	if err != nil {
		return ExecutorStateBlobInput{}, err
	}
	return ExecutorStateBlobInput{
		ID: runtimeRecoveryGraphArtifactID, Mode: "0600", Data: canonical,
		Path: path.Join(executorStateRoot, "runtime-graphs", input.Capture.OperationID+".json"),
	}, nil
}
