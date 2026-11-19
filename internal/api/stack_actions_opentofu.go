package api

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/kombifyio/stackkits/internal/applyledger"
	skerrors "github.com/kombifyio/stackkits/internal/errors"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/fallback/platformdeploy"
	sshpin "github.com/kombifyio/stackkits/internal/ssh"
	stackaction "github.com/kombifyio/stackkits/internal/stackaction"
	"github.com/kombifyio/stackkits/internal/tofu"
	"github.com/kombifyio/stackkits/pkg/models"
)

func runOpenTofuRolloutStackAction(ctx context.Context, resp stackActionResponse, includeStackKitOutputs bool, target *stackActionTarget, platformNodes []stackaction.PlatformNode, resolver StackActionReferenceResolver, workspace string) (stackActionResponse, int, *skerrors.StackKitError) {
	if err := requireLocalTofuDirStackAction(resp.TofuDir); err != nil {
		return resp, http.StatusBadRequest, err
	}
	remote, cleanup, remoteErr := prepareStackActionRemoteTarget(ctx, resp.TofuDir, target, resolver)
	if cleanup != nil {
		defer cleanup()
	}
	if remoteErr != nil {
		return resp, http.StatusBadGateway, runtimeTargetPrepareErrorStackAction("runtime_target_prepare_failed", "Runtime target preparation failed", remoteErr)
	}
	recordRuntimeTargetHostKeyStackAction(&resp, remote)
	opts := []tofu.ExecutorOption{tofu.WithWorkDir(resp.TofuDir), tofu.WithAutoApprove(true), tofu.WithTimeout(stackActionExecutionTimeout)}
	if remote != nil {
		opts = append(opts, tofu.WithEnv(remote.env...))
		resp.Checks = append(resp.Checks, stackActionCheck{Name: "remote_docker_host", Status: "ok", Detail: remote.dockerHost})
	}
	exec := tofu.NewExecutor(opts...)
	var result *tofu.Result
	var err error
	ctx, initSpan := startStackActionOperationSpan(ctx, resp, "tofu_init")
	result, err = exec.Init(ctx)
	initErr := stackActionOperationResultError("tofu_init", result, err)
	finishStackActionOperationSpan(initSpan, resp, "tofu_init", initErr)
	if initErr != nil {
		return resp, http.StatusBadGateway, tofuActionErrorStackAction("opentofu_init_failed", "OpenTofu init failed", err, resultStderrStackAction(result))
	}
	ctx, applySpan := startStackActionOperationSpan(ctx, resp, "tofu_apply")
	result, err = exec.Apply(ctx, "")
	applyErr := stackActionOperationResultError("tofu_apply", result, err)
	finishStackActionOperationSpan(applySpan, resp, "tofu_apply", applyErr)
	if applyErr != nil {
		return resp, http.StatusBadGateway, tofuActionErrorStackAction("opentofu_apply_failed", "OpenTofu apply failed", err, resultStderrStackAction(result))
	}
	resp.Status = stackaction.StatusApplied
	resp.Checks = append(resp.Checks, stackActionCheck{Name: "opentofu_apply", Status: stackaction.CheckStatusOK})
	if remote != nil {
		ctx, syncSpan := startStackActionOperationSpan(ctx, resp, "runtime_workspace_sync")
		syncErr := syncRuntimeTargetWorkspaceStackAction(ctx, remote, resp.TofuDir)
		finishStackActionOperationSpan(syncSpan, resp, "runtime_workspace_sync", syncErr)
		if syncErr != nil {
			return resp, http.StatusBadGateway, tofuActionErrorStackAction("runtime_target_sync_failed", "Runtime target workspace sync failed", syncErr, "")
		}
		resp.Checks = append(resp.Checks, stackActionCheck{Name: "runtime_workspace_sync", Status: stackaction.CheckStatusOK, Detail: remote.workspaceRoot})
	}
	if nodes := normalizeStackActionPlatformNodes(platformNodes); len(nodes) > 0 {
		ctx, nodeSpan := startStackActionOperationSpan(ctx, resp, "platform_nodes_prepare")
		nodeChecks, nodeErr := prepareRuntimePlatformNodesStackAction(ctx, resp.TofuDir, nodes, resolver)
		finishStackActionOperationSpan(nodeSpan, resp, "platform_nodes_prepare", nodeErr)
		resp.Checks = append(resp.Checks, nodeChecks...)
		if nodeErr != nil {
			return resp, http.StatusBadGateway, tofuActionErrorStackAction("platform_nodes_prepare_failed", "Supplemental platform node preparation failed", nodeErr, "")
		}
	}
	ctx, platformSpan := startStackActionOperationSpan(ctx, resp, "platform_apps_deploy")
	platformEvidence, platformChecks, platformErr := runRuntimePlatformAppDeploymentsStackAction(ctx, resp.TofuDir, runtimePlatformDeployOptionsStackAction{Remote: remote})
	finishStackActionOperationSpan(platformSpan, resp, "platform_apps_deploy", platformErr)
	resp.PlatformRefs = stackActionDeploymentRefs(platformEvidence.Refs)
	resp.PlatformSystemApps = stackActionPlatformAppStates(platformEvidence.SystemApps)
	resp.PlatformApps = stackActionPlatformAppStates(platformEvidence.Apps)
	resp.Checks = append(resp.Checks, platformChecks...)
	if platformErr != nil {
		return resp, http.StatusBadGateway, tofuActionErrorStackAction("platform_apps_deploy_failed", "Platform app deployment failed", platformErr, "")
	}
	if includeStackKitOutputs {
		ctx, outputSpan := startStackActionOperationSpan(ctx, resp, "stackkit_outputs")
		resp.StackKitOutputs = collectStackKitOutputsStackAction(ctx, exec, &resp)
		finishStackActionOperationSpan(outputSpan, resp, "stackkit_outputs", nil)
	}
	resp.Observation = collectRuntimeLiveObservationStackAction(ctx, resp, remote, platformEvidence.Refs)
	updated, status, observationErr := requireManagedRuntimeObservationStackAction(resp, remote != nil)
	if observationErr != nil {
		return updated, status, observationErr
	}
	if len(platformEvidence.Failures) > 0 {
		updated.Status = stackaction.StatusCompletedDegraded
	}
	// The per-unit account is what keeps the response from collapsing a partly
	// installed stack into one word. It only ever makes the aggregate worse:
	// a ledger that reports something short of applied downgrades the status,
	// and a ledger that reports success never overrides a failure observed
	// here.
	if units, overall := stackActionUnitOutcomes(applyledger.Latest(workspace)); len(units) > 0 {
		updated.Units = units
		updated.OverallOutcome = overall
		if overall != stackaction.OverallOutcomeApplied && updated.Status == stackaction.StatusApplied {
			updated.Status = stackaction.StatusCompletedDegraded
		}
	}
	return updated, http.StatusOK, nil
}

func stackActionDeploymentRefs(refs []platformdeploy.DeploymentRef) []stackaction.DeploymentRef {
	out := make([]stackaction.DeploymentRef, 0, len(refs))
	for _, ref := range refs {
		out = append(out, stackaction.DeploymentRef{
			Platform:       ref.Platform,
			AppName:        ref.AppName,
			ExternalID:     ref.ExternalID,
			DeploymentID:   ref.DeploymentID,
			ObservedStatus: ref.ObservedStatus,
			ObservedAt:     ref.ObservedAt,
			LastDeployed:   ref.LastDeployed,
		})
	}
	return out
}

func stackActionPlatformAppStates(states []models.PlatformAppState) []stackaction.PlatformAppState {
	out := make([]stackaction.PlatformAppState, 0, len(states))
	for _, state := range states {
		setupDrops := make([]stackaction.SetupDrop, 0, len(state.SetupDrops))
		for _, drop := range state.SetupDrops {
			setupDrops = append(setupDrops, stackaction.SetupDrop{
				Name:          drop.Name,
				Version:       drop.Version,
				Runner:        drop.Runner,
				Description:   drop.Description,
				RollbackNotes: append([]string(nil), drop.RollbackNotes...),
			})
		}
		out = append(out, stackaction.PlatformAppState{
			Name:           state.Name,
			Role:           state.Role,
			Platform:       state.Platform,
			Management:     state.Management,
			ExternalID:     state.ExternalID,
			DeploymentID:   state.DeploymentID,
			ObservedStatus: state.ObservedStatus,
			ObservedAt:     state.ObservedAt,
			ComposePath:    state.ComposePath,
			SetupPolicy:    state.SetupPolicy,
			SetupDrops:     setupDrops,
			LastDeployed:   state.LastDeployed,
			FailureStage:   state.FailureStage,
			FailureMessage: state.FailureMessage,
			Retryable:      state.Retryable,
		})
	}
	return out
}

func runOpenTofuVerifyStackAction(ctx context.Context, resp stackActionResponse, includeStackKitOutputs bool, target *stackActionTarget, resolver StackActionReferenceResolver) (stackActionResponse, int, *skerrors.StackKitError) {
	if err := requireLocalTofuDirStackAction(resp.TofuDir); err != nil {
		return resp, http.StatusBadRequest, err
	}
	exec := tofu.NewExecutor(tofu.WithWorkDir(resp.TofuDir), tofu.WithTimeout(5*time.Minute))
	ctx, stateSpan := startStackActionOperationSpan(ctx, resp, "tofu_state")
	result, err := exec.State(ctx)
	stateErr := stackActionOperationResultError("tofu_state", result, err)
	finishStackActionOperationSpan(stateSpan, resp, "tofu_state", stateErr)
	if stateErr != nil {
		return resp, http.StatusBadGateway, tofuActionErrorStackAction("opentofu_state_failed", "OpenTofu state verification failed", err, resultStderrStackAction(result))
	}
	resp.Status = stackaction.StatusVerified
	resp.Checks = append(resp.Checks, stackActionCheck{Name: "opentofu_state", Status: stackaction.CheckStatusOK})
	if includeStackKitOutputs {
		ctx, outputSpan := startStackActionOperationSpan(ctx, resp, "stackkit_outputs")
		resp.StackKitOutputs = collectStackKitOutputsStackAction(ctx, exec, &resp)
		finishStackActionOperationSpan(outputSpan, resp, "stackkit_outputs", nil)
	}
	remote, cleanup, remoteErr := prepareRuntimeObservationTargetStackAction(ctx, target, resolver)
	if cleanup != nil {
		defer cleanup()
	}
	if remoteErr != nil {
		failureClass := "runtime_target_unreachable"
		if reason := sshpin.HostKeyReason(remoteErr); reason != "" {
			failureClass = reason
			resp.Checks = append(resp.Checks, stackActionCheck{Name: "ssh_host_key", Status: stackaction.CheckStatusFailed, Detail: remoteErr.Error()})
		}
		resp.Observation = runtimeObservationTargetFailureStackAction(target, failureClass)
	} else {
		recordRuntimeTargetHostKeyStackAction(&resp, remote)
		resp.Observation = collectRuntimeLiveObservationStackAction(ctx, resp, remote, nil)
	}
	if updated, status, observationErr := requireManagedRuntimeObservationStackAction(resp, normalizeStackActionTarget(target) != nil); observationErr != nil {
		return updated, status, observationErr
	}
	return resp, http.StatusOK, nil
}

// requireManagedRuntimeObservation prevents a managed rollout or verify from
// reporting success without an actual Docker-over-SSH measurement. The action
// result and StackKit outputs are deployment evidence only; they are never a
// substitute for a reachable runtime observation.
func requireManagedRuntimeObservationStackAction(resp stackActionResponse, managed bool) (stackActionResponse, int, *skerrors.StackKitError) {
	if !managed {
		return resp, 0, nil
	}
	failureClass := managedRuntimeObservationFailureClassStackAction(resp.Observation)
	if failureClass == "" {
		return resp, 0, nil
	}
	resp.Status = stackaction.StatusFailed
	resp.Checks = append(resp.Checks, stackActionCheck{
		Name:   "runtime_observation",
		Status: stackaction.CheckStatusFailed,
		Detail: failureClass,
	})
	return resp, http.StatusBadGateway, tofuActionErrorStackAction(
		"runtime_observation_failed",
		"Managed runtime observation failed",
		fmt.Errorf("%s", failureClass),
		"",
	)
}

func managedRuntimeObservationFailureClassStackAction(observation *stackaction.LiveObservation) string {
	if observation == nil {
		return "runtime_observation_missing"
	}
	if observation.Host.Reachable && observation.Host.DockerReachable {
		return ""
	}
	return runtimeFirstNonEmptyStackAction(observation.FailureClass, observation.Host.FailureClass, "runtime_observation_unreachable")
}
