package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/auth"
	skerrors "github.com/kombifyio/stackkits/internal/errors"
	"github.com/kombifyio/stackkits/internal/platformdeploy"
	stackaction "github.com/kombifyio/stackkits/internal/stackaction"
	"github.com/kombifyio/stackkits/internal/telemetry"
	"github.com/kombifyio/stackkits/internal/tofu"
	"github.com/kombifyio/stackkits/pkg/models"
)

const (
	stackActionModeDryRun = stackaction.ModeDryRun
	stackActionModeApply  = stackaction.ModeApply

	stackActionRollout = stackaction.ActionStackKitRollout
	stackActionVerify  = stackaction.ActionVerifyRollout
	stackActionRestore = stackaction.ActionRestoreDrill

	stackActionExecutionTimeout = 14*time.Minute + 30*time.Second
)

type stackActionRequest = stackaction.Request

type stackActionTarget = stackaction.RuntimeTarget

type stackActionResponse = stackaction.Response

type stackActionCheck = stackaction.Check
type stackActionStackKitOutputs = stackaction.StackKitOutputs
type stackActionIdentityOutputs = stackaction.IdentityOutputs
type stackActionOwnerOutput = stackaction.OwnerIdentity
type stackActionRecoveryOutput = stackaction.RecoveryOutput
type stackActionLoginGateway = stackaction.LoginGatewayOutput
type stackActionServiceLink = stackaction.ServiceOutput

type stackActionRuntimeMetrics = stackaction.RuntimeMetrics

func (s *Server) registerStackActionRoutes() {
	s.mux.Handle("POST "+stackaction.PathStackKitRollout,
		s.requireStackActionServiceAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			s.handleStackAction(w, r, stackActionRollout)
		})))
	s.mux.Handle("POST "+stackaction.PathStackKitVerify,
		s.requireStackActionServiceAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			s.handleStackAction(w, r, stackActionVerify)
		})))
	s.mux.Handle("POST "+stackaction.PathRestoreDrill,
		s.requireStackActionServiceAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			s.handleStackAction(w, r, stackActionRestore)
		})))
	s.mux.Handle("POST "+stackaction.PathBackupRun,
		s.requireStackActionServiceAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			s.handleStackAction(w, r, stackaction.ActionBackupRun)
		})))
	s.mux.Handle("POST "+stackaction.PathBackupStatus,
		s.requireStackActionServiceAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			s.handleStackAction(w, r, stackaction.ActionBackupStatus)
		})))
	s.mux.Handle("POST "+stackaction.PathBackupRestore,
		s.requireStackActionServiceAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			s.handleStackAction(w, r, stackaction.ActionBackupRestore)
		})))
	s.mux.Handle("POST "+stackaction.PathBackupWipe,
		s.requireStackActionServiceAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			s.handleStackAction(w, r, stackaction.ActionBackupWipe)
		})))
}

func (s *Server) requireStackActionServiceAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secrets := []string{s.config.ServiceAuthSecret, s.config.ServiceAuthSecretNext}
		if !hasAnySecretStackAction(secrets) {
			writeStructuredError(w, r, http.StatusServiceUnavailable, skerrors.NewAuthError(
				"service_auth_not_configured",
				"SERVICE_AUTH_SECRET is required for internal StackActions",
				skerrors.WithSuggestion("Set SERVICE_AUTH_SECRET in the StackKits runtime environment"),
			))
			return
		}

		token := strings.TrimSpace(r.Header.Get(auth.HeaderServiceAuth))
		if token == "" {
			writeStructuredError(w, r, http.StatusUnauthorized, skerrors.NewAuthError(
				"missing_service_auth",
				"missing X-Kombify-Service-Auth header",
				skerrors.WithSuggestion("Call this endpoint with a techstack service-auth token"),
			))
			return
		}

		if _, err := auth.VerifyServiceToken(token, auth.VerifyOptions{
			Target:         "stackkits",
			Secrets:        secrets,
			AllowedCallers: []string{"techstack"},
		}); err != nil {
			writeStructuredError(w, r, http.StatusForbidden, skerrors.NewAuthError(
				"invalid_service_auth",
				"invalid service-auth token",
				skerrors.WithField("reason", err.Error()),
			))
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleStackAction(w http.ResponseWriter, r *http.Request, expectedAction stackaction.Action) {
	var req stackActionRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeStructuredError(w, r, http.StatusBadRequest, skerrors.NewValidationError(
			"invalid_stack_action_payload",
			"StackAction payload must be valid JSON",
			skerrors.WithField("error", fmt.Sprint(err)),
		))
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeStructuredError(w, r, http.StatusBadRequest, skerrors.NewValidationError(
			"invalid_stack_action_payload",
			"StackAction payload must contain exactly one JSON object",
			skerrors.WithField("error", fmt.Sprint(err)),
		))
		return
	}

	req.Action = stackaction.NormalizeAction(string(req.Action))
	if req.Action != expectedAction {
		writeStructuredError(w, r, http.StatusBadRequest, skerrors.NewValidationError(
			"invalid_stack_action",
			"StackAction does not match endpoint",
			skerrors.WithField("expected", expectedAction),
			skerrors.WithField("actual", req.Action),
		))
		return
	}
	req.StackID = strings.TrimSpace(req.StackID)
	if req.StackID == "" {
		writeStructuredError(w, r, http.StatusBadRequest, skerrors.NewValidationError(
			"missing_stack_id",
			"stack_id is required",
		))
		return
	}

	resp, status, err := s.executeStackAction(r.Context(), req)
	if err != nil {
		writeStructuredError(w, r, status, err)
		return
	}
	writeSuccess(w, r, http.StatusOK, resp)
}

func (s *Server) executeStackAction(ctx context.Context, req stackActionRequest) (resp stackActionResponse, status int, stackErr *skerrors.StackKitError) {
	mode := s.stackActionMode()
	resp = newStackActionResponse(req, mode)
	target, enrollment, requestErr := prepareStackActionRequest(&resp, &req)
	if requestErr != nil {
		return resp, http.StatusBadRequest, requestErr
	}

	ctx, span := startStackActionSpan(ctx, resp)
	defer func() {
		finishStackActionSpan(span, resp, status, stackErr)
	}()

	includeStackKitOutputs := true
	if mode == stackActionModeDryRun {
		resp.Status = dryRunStatusStackAction(req.Action)
		if includeStackKitOutputs {
			resp.StackKitOutputs = stackKitOutputsFromOpenTofuStackAction(resp, nil)
		}
		return resp, http.StatusOK, nil
	}
	if requiresManagedRuntimeTargetStackAction(req.Action, enrollment, target) {
		resp.Status = stackaction.StatusFailed
		resp.Checks = append(resp.Checks, stackActionCheck{Name: "runtime_target", Status: stackaction.CheckStatusFailed, Detail: "managed runtime target is required"})
		return resp, http.StatusBadRequest, skerrors.NewValidationError(
			"managed_runtime_target_required",
			"TechStack-managed rollout and verify actions require runtime_target",
		)
	}

	return s.dispatchStackAction(ctx, resp, includeStackKitOutputs, req)
}

func newStackActionResponse(req stackActionRequest, mode stackaction.Mode) stackActionResponse {
	resp := stackActionResponse{
		Status:      stackaction.StatusAccepted,
		Action:      req.Action,
		StackID:     req.StackID,
		StackName:   strings.TrimSpace(req.StackName),
		StackKit:    strings.TrimSpace(req.StackKit),
		TofuDir:     strings.TrimSpace(req.TofuDir),
		UnifiedPath: strings.TrimSpace(req.UnifiedPath),
		Mode:        mode,
	}
	resp.Checks = append(resp.Checks, stackActionCheck{Name: "request", Status: stackaction.CheckStatusOK, Detail: "StackAction payload decoded"})
	resp.Checks = appendPathCheckStackAction(resp.Checks, "tofu_dir", resp.TofuDir, true)
	resp.Checks = appendPathCheckStackAction(resp.Checks, "unified_path", resp.UnifiedPath, false)
	if resp.StackKit == "" {
		resp.Checks = append(resp.Checks, stackActionCheck{Name: "stackkit", Status: stackaction.CheckStatusWarning, Detail: "stackkit name not provided"})
		return resp
	}
	resp.Checks = append(resp.Checks, stackActionCheck{Name: "stackkit", Status: stackaction.CheckStatusOK, Detail: resp.StackKit})
	return resp
}

func prepareStackActionRequest(resp *stackActionResponse, req *stackActionRequest) (*stackActionTarget, *stackaction.TechStackEnrollment, *skerrors.StackKitError) {
	if req.OwnerSpecRef != nil {
		ownerRef, err := normalizeStackActionReference(req.OwnerSpecRef, stackActionScopeOwnerSpecRead, time.Now())
		if err != nil {
			resp.Status = stackaction.StatusFailed
			return nil, nil, skerrors.NewValidationError(
				"invalid_owner_spec_ref",
				"owner_spec_ref must be versioned, unexpired, and scoped for owner-spec read",
				skerrors.WithField("error", err.Error()),
			)
		}
		req.OwnerSpecRef = ownerRef
	}
	target, targetErr := normalizeStackActionRequestTarget(resp, req)
	if targetErr != nil {
		return nil, nil, targetErr
	}
	enrollment, enrollmentErr := normalizeStackActionRequestEnrollment(resp, req)
	if enrollmentErr != nil {
		return target, nil, enrollmentErr
	}
	if nodeErr := normalizeStackActionRequestPlatformNodes(resp, req); nodeErr != nil {
		return target, enrollment, nodeErr
	}
	return target, enrollment, nil
}

func normalizeStackActionRequestTarget(resp *stackActionResponse, req *stackActionRequest) (*stackActionTarget, *skerrors.StackKitError) {
	target := normalizeStackActionTarget(req.RuntimeTarget)
	if target == nil {
		if req.RuntimeTarget != nil && strings.TrimSpace(req.RuntimeTarget.DockerHost) != "" {
			resp.Status = stackaction.StatusFailed
			resp.Checks = append(resp.Checks, stackActionCheck{Name: "runtime_target", Status: stackaction.CheckStatusFailed, Detail: "runtime target host is required"})
			return nil, skerrors.NewValidationError(
				"invalid_runtime_target",
				"runtime_target host is required when docker_host is set",
			)
		}
		return nil, nil
	}
	if _, targetErr := runtimeTargetDockerHostStackAction(target); targetErr != nil {
		resp.Status = stackaction.StatusFailed
		resp.Checks = append(resp.Checks, stackActionCheck{Name: "runtime_target", Status: stackaction.CheckStatusFailed, Detail: targetErr.Error()})
		return nil, skerrors.NewValidationError(
			"invalid_runtime_target",
			"runtime_target must use the validated SSH Docker transport",
			skerrors.WithField("error", targetErr.Error()),
		)
	}
	accessRef, accessErr := normalizeStackActionReference(target.AccessProfileRef, stackActionScopeRuntimeSSH, time.Now())
	if accessErr != nil {
		resp.Status = stackaction.StatusFailed
		resp.Checks = append(resp.Checks, stackActionCheck{Name: "runtime_target", Status: stackaction.CheckStatusFailed, Detail: "invalid access_profile_ref"})
		return nil, skerrors.NewValidationError(
			"invalid_runtime_access_profile_ref",
			"runtime_target access_profile_ref must be versioned, unexpired, and scoped for SSH",
			skerrors.WithField("error", accessErr.Error()),
		)
	}
	target.AccessProfileRef = accessRef
	resp.Checks = append(resp.Checks, stackActionCheck{Name: "runtime_target", Status: "ok", Detail: target.Host})
	req.RuntimeTarget = target
	return target, nil
}

func normalizeStackActionRequestEnrollment(resp *stackActionResponse, req *stackActionRequest) (*stackaction.TechStackEnrollment, *skerrors.StackKitError) {
	enrollment, enrollmentErr := normalizeTechStackEnrollmentStackAction(req.TechStackEnrollment)
	if enrollmentErr != nil {
		resp.Status = stackaction.StatusFailed
		resp.Checks = append(resp.Checks, stackActionCheck{Name: "techstack_enrollment", Status: stackaction.CheckStatusFailed, Detail: enrollmentErr.Error()})
		return nil, skerrors.NewValidationError(
			"techstack_enrollment_incomplete",
			"techstack_enrollment requires lease_id, server_url, server_id, runtime_agent_id, enrollment_access_ref, and heartbeat_url or inventory_url",
			skerrors.WithField("error", enrollmentErr.Error()),
		)
	}
	if enrollment != nil {
		resp.Checks = append(resp.Checks, stackActionCheck{Name: "techstack_enrollment", Status: stackaction.CheckStatusOK, Detail: enrollment.RuntimeAgentID})
		req.TechStackEnrollment = enrollment
	}
	return enrollment, nil
}

func normalizeStackActionRequestPlatformNodes(resp *stackActionResponse, req *stackActionRequest) *skerrors.StackKitError {
	nodes, err := normalizeStackActionPlatformNodeReferences(req.PlatformNodes, time.Now())
	if err != nil {
		resp.Status = stackaction.StatusFailed
		resp.Checks = append(resp.Checks, stackActionCheck{Name: "platform_nodes", Status: stackaction.CheckStatusFailed, Detail: err.Error()})
		return skerrors.NewValidationError(
			"invalid_platform_node_reference",
			"platform_nodes must use observed platform IDs or valid opaque onboarding and access-profile references",
			skerrors.WithField("error", err.Error()),
		)
	}
	if len(nodes) > 0 {
		resp.Checks = append(resp.Checks, stackActionCheck{Name: "platform_nodes", Status: stackaction.CheckStatusOK, Detail: fmt.Sprintf("%d supplemental node(s) requested", len(nodes))})
		req.PlatformNodes = nodes
	}
	return nil
}

func requiresManagedRuntimeTargetStackAction(action stackaction.Action, enrollment *stackaction.TechStackEnrollment, target *stackActionTarget) bool {
	if enrollment == nil || target != nil {
		return false
	}
	return action == stackActionRollout || action == stackActionVerify
}

func (s *Server) dispatchStackAction(ctx context.Context, resp stackActionResponse, includeStackKitOutputs bool, req stackActionRequest) (stackActionResponse, int, *skerrors.StackKitError) {
	switch req.Action {
	case stackActionRollout:
		return runOpenTofuRolloutStackAction(ctx, resp, includeStackKitOutputs, req.RuntimeTarget, req.PlatformNodes, s.stackActionReferenceResolver)
	case stackActionVerify:
		return runOpenTofuVerifyStackAction(ctx, resp, includeStackKitOutputs, req.RuntimeTarget, s.stackActionReferenceResolver)
	case stackActionRestore:
		return runRestoreDrillVerifierStackAction(ctx, resp, s.config.StackActionRestoreVerifierCommand, req.RuntimeTarget, s.stackActionReferenceResolver)
	case stackaction.ActionBackupRun:
		return s.runBackupRunStackAction(ctx, resp, req)
	case stackaction.ActionBackupStatus:
		return s.runBackupStatusStackAction(ctx, resp)
	case stackaction.ActionBackupRestore:
		return s.runBackupRestoreStackAction(ctx, resp, req)
	case stackaction.ActionBackupWipe:
		return s.runBackupWipeStackAction(ctx, resp, req)
	default:
		return resp, http.StatusBadRequest, skerrors.NewValidationError("invalid_stack_action", "unsupported StackAction")
	}
}

func startStackActionSpan(ctx context.Context, resp stackActionResponse) (context.Context, telemetry.SpanHandle) {
	action := strings.TrimSpace(string(resp.Action))
	if action == "" {
		action = "unknown"
	}
	return telemetry.StartSpan(ctx, "stackkit.server.stack_action."+action, stackActionSpanAttributes(resp, 0, nil))
}

func finishStackActionSpan(span telemetry.SpanHandle, resp stackActionResponse, status int, stackErr *skerrors.StackKitError) {
	if status == 0 {
		status = http.StatusInternalServerError
	}
	span.SetAttributes(stackActionSpanAttributes(resp, status, stackErr))
	if stackErr != nil {
		span.RecordError(stackErr)
		span.SetRolloutStatus("failed", stackErr.Message)
		span.End()
		return
	}
	if status >= http.StatusBadRequest || resp.Status == stackaction.StatusFailed {
		span.SetRolloutStatus("failed", string(resp.Status))
	} else {
		span.SetRolloutStatus("succeeded", "")
	}
	span.End()
}

func stackActionSpanAttributes(resp stackActionResponse, status int, stackErr *skerrors.StackKitError) map[string]string {
	attrs := map[string]string{
		"stackkit.stack_action":        string(resp.Action),
		"stackkit.stack_action.status": string(resp.Status),
		"stackkit.stack_action.mode":   string(resp.Mode),
	}
	if status > 0 {
		attrs["http.status_code"] = strconv.Itoa(status)
	}
	if strings.TrimSpace(resp.StackID) != "" {
		attrs["stackkit.stack_id"] = resp.StackID
	}
	if strings.TrimSpace(resp.StackName) != "" {
		attrs["stackkit.stack_name"] = resp.StackName
	}
	if strings.TrimSpace(resp.StackKit) != "" {
		attrs["stackkit.stackkit"] = resp.StackKit
	}
	if stackErr != nil {
		attrs["stackkit.failure_class"] = stackErr.Code
		attrs["stackkit.error_category"] = string(stackErr.Category)
	}
	return attrs
}

func startStackActionOperationSpan(ctx context.Context, resp stackActionResponse, operation string) (context.Context, telemetry.SpanHandle) {
	attrs := stackActionSpanAttributes(resp, 0, nil)
	attrs["stackkit.stack_action.operation"] = operation
	return telemetry.StartSpan(ctx, "stackkit.server.stack_action."+operation, attrs)
}

func finishStackActionOperationSpan(span telemetry.SpanHandle, resp stackActionResponse, operation string, err error) {
	attrs := stackActionSpanAttributes(resp, 0, nil)
	attrs["stackkit.stack_action.operation"] = operation
	if err != nil {
		attrs["stackkit.stack_action.operation_status"] = "failed"
		span.SetAttributes(attrs)
		span.RecordError(err)
		span.SetRolloutStatus("failed", err.Error())
		span.End()
		return
	}
	attrs["stackkit.stack_action.operation_status"] = "succeeded"
	span.SetAttributes(attrs)
	span.SetRolloutStatus("succeeded", "")
	span.End()
}

func stackActionOperationResultError(operation string, result *tofu.Result, err error) error {
	if err != nil {
		return err
	}
	if result == nil {
		return fmt.Errorf("%s did not return a result", operation)
	}
	if !result.Success {
		detail := resultStderrStackAction(result)
		if strings.TrimSpace(detail) == "" {
			detail = fmt.Sprintf("%s exited with code %d", operation, result.ExitCode)
		}
		return fmt.Errorf("%s failed: %s", operation, detail)
	}
	return nil
}

func runOpenTofuRolloutStackAction(ctx context.Context, resp stackActionResponse, includeStackKitOutputs bool, target *stackActionTarget, platformNodes []stackaction.PlatformNode, resolver StackActionReferenceResolver) (stackActionResponse, int, *skerrors.StackKitError) {
	if err := requireLocalTofuDirStackAction(resp.TofuDir); err != nil {
		return resp, http.StatusBadRequest, err
	}
	remote, cleanup, remoteErr := prepareStackActionRemoteTarget(ctx, resp.TofuDir, target, resolver)
	if cleanup != nil {
		defer cleanup()
	}
	if remoteErr != nil {
		return resp, http.StatusBadGateway, tofuActionErrorStackAction("runtime_target_prepare_failed", "Runtime target preparation failed", remoteErr, "")
	}
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
	if updated, status, observationErr := requireManagedRuntimeObservationStackAction(resp, remote != nil); observationErr != nil {
		return updated, status, observationErr
	}
	return resp, http.StatusOK, nil
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
		})
	}
	return out
}

type preparedRuntimeTargetStackAction struct {
	dockerHost    string
	env           []string
	target        *stackActionTarget
	keyPath       string
	workspaceRoot string
}

func prepareStackActionRemoteTarget(ctx context.Context, tofuDir string, target *stackActionTarget, resolver StackActionReferenceResolver) (*preparedRuntimeTargetStackAction, func(), error) {
	target = normalizeStackActionTarget(target)
	if target == nil {
		return nil, nil, nil
	}
	dockerHost, err := runtimeTargetDockerHostStackAction(target)
	if err != nil {
		return nil, nil, err
	}
	keyPath, homeDir, cleanup, err := materializeRuntimeTargetSSHKeyStackAction(ctx, target, resolver)
	if err != nil {
		return nil, cleanup, err
	}
	if err := bootstrapRuntimeTargetDockerStackAction(ctx, target, keyPath); err != nil {
		return nil, cleanup, err
	}
	workspaceRoot := runtimeTargetWorkspaceRootStackAction(tofuDir)
	if err := writeRuntimeTargetDockerHostTFVarsStackAction(tofuDir, dockerHost, workspaceRoot); err != nil {
		return nil, cleanup, err
	}
	env := []string{"DOCKER_HOST=" + dockerHost}
	if homeDir != "" {
		env = append(env, "HOME="+homeDir)
	}
	if keyPath != "" {
		env = append(env, "DOCKER_SSH_COMMAND="+runtimeTargetSSHCommandStackAction(target, keyPath))
	}
	return &preparedRuntimeTargetStackAction{dockerHost: dockerHost, env: env, target: target, keyPath: keyPath, workspaceRoot: workspaceRoot}, cleanup, nil
}

func normalizeStackActionTarget(target *stackActionTarget) *stackActionTarget {
	if target == nil {
		return nil
	}
	normalized := *target
	normalized.Host = firstRuntimeOutputStackAction(map[string]string{
		"host":       normalized.Host,
		"public_ip":  normalized.PublicIP,
		"private_ip": normalized.PrivateIP,
	}, "host", "public_ip", "private_ip")
	normalized.User = strings.TrimSpace(normalized.User)
	if normalized.User == "" {
		normalized.User = "root"
	}
	if normalized.Port <= 0 {
		normalized.Port = 22
	}
	normalized.DockerHost = strings.TrimSpace(normalized.DockerHost)
	if normalized.Host == "" {
		return nil
	}
	return &normalized
}

func normalizeTechStackEnrollmentStackAction(enrollment *stackaction.TechStackEnrollment) (*stackaction.TechStackEnrollment, error) {
	if enrollment == nil {
		return nil, nil
	}
	normalized := *enrollment
	normalized.TenantID = strings.TrimSpace(normalized.TenantID)
	normalized.OwnerID = strings.TrimSpace(normalized.OwnerID)
	normalized.StackID = strings.TrimSpace(normalized.StackID)
	normalized.LeaseID = strings.TrimSpace(normalized.LeaseID)
	normalized.ServerURL = strings.TrimRight(strings.TrimSpace(normalized.ServerURL), "/")
	normalized.ServerID = strings.TrimSpace(normalized.ServerID)
	normalized.RuntimeAgentID = strings.TrimSpace(normalized.RuntimeAgentID)
	normalized.HeartbeatURL = strings.TrimSpace(normalized.HeartbeatURL)
	normalized.InventoryURL = strings.TrimSpace(normalized.InventoryURL)
	missing := []string{}
	for name, value := range map[string]string{
		"lease_id":         normalized.LeaseID,
		"server_url":       normalized.ServerURL,
		"server_id":        normalized.ServerID,
		"runtime_agent_id": normalized.RuntimeAgentID,
	} {
		if value == "" {
			missing = append(missing, name)
		}
	}
	if normalized.HeartbeatURL == "" && normalized.InventoryURL == "" {
		missing = append(missing, "heartbeat_url or inventory_url")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing %s", strings.Join(missing, ", "))
	}
	enrollmentRef, err := normalizeStackActionReference(normalized.EnrollmentAccessRef, stackActionScopeRuntimeEnroll, time.Now())
	if err != nil {
		return nil, fmt.Errorf("invalid enrollment_access_ref: %w", err)
	}
	normalized.EnrollmentAccessRef = enrollmentRef
	return &normalized, nil
}

func materializeRuntimeTargetSSHKeyStackAction(ctx context.Context, target *stackActionTarget, resolver StackActionReferenceResolver) (string, string, func(), error) {
	if target == nil {
		return "", "", nil, fmt.Errorf("runtime target is required")
	}
	accessRef, err := normalizeStackActionReference(target.AccessProfileRef, stackActionScopeRuntimeSSH, time.Now())
	if err != nil {
		return "", "", nil, fmt.Errorf("invalid runtime access_profile_ref: %w", err)
	}
	if resolver == nil {
		return "", "", nil, fmt.Errorf("StackAction reference resolver is not configured")
	}
	material, err := resolver.ResolveAccessProfile(ctx, *accessRef)
	if err != nil {
		return "", "", nil, fmt.Errorf("resolve runtime access profile: %w", err)
	}
	key := append([]byte(nil), material.PrivateKey...)
	defer clear(key)
	if len(bytes.TrimSpace(key)) == 0 {
		return "", "", nil, fmt.Errorf("resolved runtime access profile has no SSH private key")
	}
	dir, err := os.MkdirTemp("", "stackkits-runtime-ssh-")
	if err != nil {
		return "", "", nil, fmt.Errorf("create runtime SSH key dir: %w", err)
	}
	sshDir := filepath.Join(dir, ".ssh")
	if err := os.MkdirAll(sshDir, 0700); err != nil {
		_ = os.RemoveAll(dir)
		return "", "", nil, fmt.Errorf("create runtime SSH config dir: %w", err)
	}
	keyPath := filepath.Join(sshDir, "id_runtime")
	keyData := append(bytes.TrimSpace(key), '\n')
	if err := os.WriteFile(keyPath, keyData, 0600); err != nil {
		_ = os.RemoveAll(dir)
		return "", "", nil, fmt.Errorf("write runtime SSH key: %w", err)
	}
	config := runtimeSSHConfigStackAction(target, keyPath)
	if err := os.WriteFile(filepath.Join(sshDir, "config"), []byte(config), 0600); err != nil {
		_ = os.RemoveAll(dir)
		return "", "", nil, fmt.Errorf("write runtime SSH config: %w", err)
	}
	restoreUserConfig, err := installRuntimeUserSSHConfigStackAction(target, keyPath)
	if err != nil {
		_ = os.RemoveAll(dir)
		return "", "", nil, err
	}
	cleanup := func() {
		if restoreUserConfig != nil {
			restoreUserConfig()
		}
		_ = os.RemoveAll(dir)
	}
	return keyPath, dir, cleanup, nil
}

func runtimeSSHConfigStackAction(target *stackActionTarget, keyPath string) string {
	return fmt.Sprintf(`Host %s
  HostName %s
  User %s
  IdentityFile %s
  IdentitiesOnly yes
  StrictHostKeyChecking no
  UserKnownHostsFile /dev/null
  LogLevel ERROR
  Port %d
`, target.Host, target.Host, target.User, keyPath, target.Port)
}

func installRuntimeUserSSHConfigStackAction(target *stackActionTarget, keyPath string) (func(), error) {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return nil, nil
	}
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0700); err != nil {
		return nil, fmt.Errorf("create user SSH config dir: %w", err)
	}
	configPath := filepath.Join(sshDir, "config")
	previous, readErr := os.ReadFile(configPath)
	existed := readErr == nil
	if readErr != nil && !os.IsNotExist(readErr) {
		return nil, fmt.Errorf("read user SSH config: %w", readErr)
	}
	next := append([]byte(runtimeSSHConfigStackAction(target, keyPath)+"\n"), previous...)
	// #nosec G703 -- configPath is fixed to $HOME/.ssh/config for the local operator user.
	if err := os.WriteFile(configPath, next, 0600); err != nil {
		return nil, fmt.Errorf("write user SSH config: %w", err)
	}
	return func() {
		if existed {
			// #nosec G703 -- configPath is fixed to $HOME/.ssh/config for the local operator user.
			_ = os.WriteFile(configPath, previous, 0600)
		} else {
			_ = os.Remove(configPath)
		}
	}, nil
}

func bootstrapRuntimeTargetDockerStackAction(ctx context.Context, target *stackActionTarget, keyPath string) error {
	if keyPath == "" {
		return fmt.Errorf("runtime target SSH key path is required")
	}
	args := runtimeTargetSSHArgsStackAction(target, keyPath)
	script := `set -eu
if command -v sudo >/dev/null 2>&1 && [ "$(id -u)" != "0" ]; then SUDO="sudo -n"; else SUDO=""; fi
if command -v cloud-init >/dev/null 2>&1; then
  timeout 300 cloud-init status --wait >/dev/null 2>&1 || true
fi
apt_busy() {
  pgrep -x apt >/dev/null 2>&1 ||
  pgrep -x apt-get >/dev/null 2>&1 ||
  pgrep -x dpkg >/dev/null 2>&1 ||
  pgrep -f unattended-upgrade >/dev/null 2>&1
}
wait_for_apt() {
  for i in $(seq 1 90); do
    if ! apt_busy; then return 0; fi
    sleep 2
  done
  return 1
}
apt_get() {
  wait_for_apt
  $SUDO env DEBIAN_FRONTEND=noninteractive apt-get "$@"
}
install_docker() {
  for i in $(seq 1 12); do
    wait_for_apt || true
    if curl -fsSL https://get.docker.com | $SUDO sh; then return 0; fi
    sleep 10
  done
  curl -fsSL https://get.docker.com | $SUDO sh
}
if ! command -v docker >/dev/null 2>&1; then
  if ! command -v curl >/dev/null 2>&1; then
    apt_get update
    apt_get install -y ca-certificates curl
  fi
  install_docker
fi
if [ "$(id -u)" != "0" ]; then
  $SUDO usermod -aG docker "$(id -un)" || true
fi
$SUDO systemctl enable --now docker >/dev/null 2>&1 || true
for i in $(seq 1 60); do
  if $SUDO docker info >/dev/null 2>&1; then exit 0; fi
  sleep 2
done
$SUDO docker info >/dev/null`
	args = append(args, "sh", "-c", script)
	runCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	var lastOutput string
	var lastErr error
	for attempt := 1; attempt <= 8; attempt++ {
		cmd := exec.CommandContext(runCtx, "ssh", args...) // #nosec G204 -- command args are assembled without shell interpolation.
		output, err := cmd.CombinedOutput()
		if err == nil {
			return nil
		}
		lastErr = err
		lastOutput = strings.TrimSpace(string(output))
		if runCtx.Err() != nil {
			break
		}
		select {
		case <-runCtx.Done():
			return fmt.Errorf("bootstrap remote Docker over SSH: %w: %s", lastErr, lastOutput)
		case <-time.After(time.Duration(attempt) * 10 * time.Second):
		}
	}
	return fmt.Errorf("bootstrap remote Docker over SSH: %w: %s", lastErr, lastOutput)
}

func writeRuntimeTargetDockerHostTFVarsStackAction(tofuDir, dockerHost string, workspaceRoot ...string) error {
	tfvarsPath := filepath.Join(filepath.Clean(tofuDir), "terraform.tfvars.json")
	values := map[string]any{}
	if data, err := os.ReadFile(tfvarsPath); err == nil && len(strings.TrimSpace(string(data))) > 0 {
		if err := json.Unmarshal(data, &values); err != nil {
			return fmt.Errorf("parse terraform.tfvars.json: %w", err)
		}
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read terraform.tfvars.json: %w", err)
	}
	values["docker_host"] = dockerHost
	if len(workspaceRoot) > 0 && strings.TrimSpace(workspaceRoot[0]) != "" {
		values["workspace_root"] = strings.TrimSpace(workspaceRoot[0])
	}
	data, err := json.MarshalIndent(values, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal terraform.tfvars.json: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(tfvarsPath, data, 0600); err != nil {
		return fmt.Errorf("write terraform.tfvars.json: %w", err)
	}
	return nil
}

func runtimeTargetWorkspaceRootStackAction(tofuDir string) string {
	workspaceDir := filepath.Clean(filepath.Join(filepath.Clean(tofuDir), ".."))
	sum := sha256.Sum256([]byte(workspaceDir))
	hash := fmt.Sprintf("%x", sum[:])
	return "/opt/stackkits/runtime-workspaces/" + hash[:16]
}

// runtimeTargetDockerHost accepts only the Docker-over-SSH transport derived
// from the validated runtime target. A caller must not redirect a managed
// rollout/observation at arbitrary tcp:// or unix:// Docker endpoints.
func runtimeTargetDockerHostStackAction(target *stackActionTarget) (string, error) {
	host, user, port, err := validatedRuntimeTargetDockerEndpointStackAction(target)
	if err != nil {
		return "", err
	}
	if dockerHost := strings.TrimSpace(target.DockerHost); dockerHost != "" {
		if err := validateRuntimeTargetDockerHostOverrideStackAction(dockerHost, user, host, port); err != nil {
			return "", err
		}
	}
	return runtimeTargetDockerSSHURLStackAction(user, host, port), nil
}

func validatedRuntimeTargetDockerEndpointStackAction(target *stackActionTarget) (host, user string, port int, err error) {
	if target == nil {
		return "", "", 0, fmt.Errorf("runtime target is required for Docker transport")
	}
	host, err = validateRuntimeTargetHostStackAction(target.Host)
	if err != nil {
		return "", "", 0, err
	}
	user, err = validateRuntimeTargetSSHUserStackAction(target.User)
	if err != nil {
		return "", "", 0, err
	}
	port, err = runtimeTargetSSHPortStackAction(target.Port)
	if err != nil {
		return "", "", 0, err
	}
	return host, user, port, nil
}

func runtimeTargetSSHPortStackAction(port int) (int, error) {
	if port <= 0 {
		return 22, nil
	}
	if port > 65535 {
		return 0, fmt.Errorf("runtime target SSH port %d is invalid", port)
	}
	return port, nil
}

func validateRuntimeTargetDockerHostOverrideStackAction(value, user, host string, port int) error {
	parsed, err := parseRuntimeTargetDockerSSHURLStackAction(value)
	if err != nil {
		return err
	}
	if err := validateRuntimeTargetDockerHostUserStackAction(parsed, user); err != nil {
		return err
	}
	if err := validateRuntimeTargetDockerHostPathStackAction(parsed); err != nil {
		return err
	}
	if err := validateRuntimeTargetDockerHostHostStackAction(parsed, host); err != nil {
		return err
	}
	return validateRuntimeTargetDockerHostPortStackAction(parsed, port)
}

func parseRuntimeTargetDockerSSHURLStackAction(value string) (*url.URL, error) {
	parsed, err := url.Parse(value)
	if err != nil {
		return nil, fmt.Errorf("runtime target docker_host must be a validated ssh:// URL")
	}
	if parsed.Scheme != "ssh" || parsed.Opaque != "" {
		return nil, fmt.Errorf("runtime target docker_host must be a validated ssh:// URL")
	}
	return parsed, nil
}

func validateRuntimeTargetDockerHostUserStackAction(parsed *url.URL, user string) error {
	if parsed.User == nil || parsed.User.Username() != user {
		return fmt.Errorf("runtime target docker_host user must match runtime target user")
	}
	if _, hasPassword := parsed.User.Password(); hasPassword {
		return fmt.Errorf("runtime target docker_host must not contain credentials")
	}
	return nil
}

func validateRuntimeTargetDockerHostPathStackAction(parsed *url.URL) error {
	if parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("runtime target docker_host must not contain a path, query, or fragment")
	}
	return nil
}

func validateRuntimeTargetDockerHostHostStackAction(parsed *url.URL, host string) error {
	overrideHost, err := validateRuntimeTargetHostStackAction(parsed.Hostname())
	if err != nil {
		return fmt.Errorf("runtime target docker_host must match the validated runtime target host")
	}
	if !runtimeTargetHostsEqualStackAction(host, overrideHost) {
		return fmt.Errorf("runtime target docker_host must match the validated runtime target host")
	}
	return nil
}

func validateRuntimeTargetDockerHostPortStackAction(parsed *url.URL, port int) error {
	overridePort, err := runtimeTargetDockerHostOverridePortStackAction(parsed)
	if err != nil {
		return err
	}
	if overridePort != port {
		return fmt.Errorf("runtime target docker_host port must match runtime target port")
	}
	return nil
}

func runtimeTargetDockerHostOverridePortStackAction(parsed *url.URL) (int, error) {
	rawPort := parsed.Port()
	if rawPort == "" {
		return 22, nil
	}
	port, err := strconv.Atoi(rawPort)
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("runtime target docker_host SSH port is invalid")
	}
	return port, nil
}

func runtimeTargetDockerSSHURLStackAction(user, host string, port int) string {
	authority := host
	if port != 22 {
		authority = net.JoinHostPort(host, strconv.Itoa(port))
	} else if addr, err := netip.ParseAddr(host); err == nil && addr.Is6() {
		authority = "[" + addr.String() + "]"
	}
	return (&url.URL{Scheme: "ssh", User: url.User(user), Host: authority}).String()
}

func validateRuntimeTargetHostStackAction(value string) (string, error) {
	host := strings.TrimSpace(value)
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	}
	if host == "" || strings.ContainsAny(host, " \t\r\n/@?#\\") {
		return "", fmt.Errorf("runtime target host is invalid")
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		return addr.String(), nil
	}
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if host == "" || len(host) > 253 {
		return "", fmt.Errorf("runtime target host is invalid")
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", fmt.Errorf("runtime target host is invalid")
		}
		for _, char := range label {
			if !(char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '-') {
				return "", fmt.Errorf("runtime target host is invalid")
			}
		}
	}
	return host, nil
}

func validateRuntimeTargetSSHUserStackAction(value string) (string, error) {
	user := strings.TrimSpace(value)
	if user == "" || strings.ContainsAny(user, " \t\r\n@:/?#\\") {
		return "", fmt.Errorf("runtime target SSH user is invalid")
	}
	return user, nil
}

func runtimeTargetHostsEqualStackAction(left, right string) bool {
	if left == "" || right == "" {
		return false
	}
	leftAddr, leftIsIP := netip.ParseAddr(left)
	rightAddr, rightIsIP := netip.ParseAddr(right)
	if leftIsIP == nil || rightIsIP == nil {
		return leftIsIP == nil && rightIsIP == nil && leftAddr == rightAddr
	}
	return strings.EqualFold(left, right)
}

func runtimeTargetSSHCommandStackAction(target *stackActionTarget, keyPath string) string {
	return strings.Join(runtimeTargetSSHBaseArgsStackAction(target, keyPath), " ")
}

func runtimeTargetSSHArgsStackAction(target *stackActionTarget, keyPath string) []string {
	return append(runtimeTargetSSHBaseArgsStackAction(target, keyPath), target.User+"@"+target.Host)
}

func syncRuntimeTargetWorkspaceStackAction(ctx context.Context, remote *preparedRuntimeTargetStackAction, tofuDir string) error {
	if remote == nil || remote.target == nil || strings.TrimSpace(remote.keyPath) == "" || strings.TrimSpace(remote.workspaceRoot) == "" {
		return nil
	}
	workspaceDir := filepath.Clean(filepath.Join(filepath.Clean(tofuDir), ".."))
	info, err := os.Stat(workspaceDir)
	if err != nil {
		return fmt.Errorf("stat local runtime workspace: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("local runtime workspace %q is not a directory", workspaceDir)
	}

	syncCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	tarCmd := exec.CommandContext(syncCtx, "tar", runtimeTargetWorkspaceArchiveArgsStackAction(workspaceDir)...) // #nosec G204 -- workspaceDir is a local path, passed as an argv value.
	pipe, err := tarCmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("prepare runtime workspace archive: %w", err)
	}

	script := runtimeTargetWorkspaceSyncScriptStackAction(remote.workspaceRoot)
	sshArgs := append(runtimeTargetSSHArgsStackAction(remote.target, remote.keyPath), "sh", "-c", shellQuoteStackAction(script))
	sshCmd := exec.CommandContext(syncCtx, "ssh", sshArgs...) // #nosec G204 -- command args are assembled without shell interpolation except a quoted constant path.
	sshCmd.Stdin = pipe

	var tarErr bytes.Buffer
	var sshErr bytes.Buffer
	tarCmd.Stderr = &tarErr
	sshCmd.Stderr = &sshErr

	if err := sshCmd.Start(); err != nil {
		return fmt.Errorf("start runtime workspace sync target: %w", err)
	}
	if err := tarCmd.Start(); err != nil {
		_ = sshCmd.Process.Kill()
		return fmt.Errorf("start runtime workspace archive: %w", err)
	}
	tarWaitErr := tarCmd.Wait()
	sshWaitErr := sshCmd.Wait()
	if tarWaitErr != nil {
		return fmt.Errorf("archive runtime workspace: %w: %s", tarWaitErr, strings.TrimSpace(tarErr.String()))
	}
	if sshWaitErr != nil {
		return fmt.Errorf("copy runtime workspace to target: %w: %s", sshWaitErr, strings.TrimSpace(sshErr.String()))
	}
	return nil
}

func runtimeTargetWorkspaceArchiveArgsStackAction(workspaceDir string) []string {
	excludes := []string{
		".git",
		"*/.git",
		".terraform",
		"*/.terraform",
		"terraform.tfstate",
		"terraform.tfstate.backup",
		"*.tfstate",
		"*.tfstate.backup",
		"node_modules",
		"*/node_modules",
		".stackkit/logs",
		"*/.stackkit/logs",
		"artifacts",
		"*/artifacts",
		"coverage",
		"*/coverage",
		"dist",
		"*/dist",
		"build",
		"*/build",
	}
	args := []string{"-C", workspaceDir}
	for _, exclude := range excludes {
		args = append(args, "--exclude="+exclude)
	}
	return append(args, "-cf", "-", ".")
}

func runtimeTargetWorkspaceSyncScriptStackAction(workspaceRoot string) string {
	remotePath := shellQuoteStackAction(workspaceRoot)
	return "rm -rf -- " + remotePath + " && mkdir -p " + remotePath + " && tar -C " + remotePath + " -xf -"
}

func shellQuoteStackAction(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func runtimeTargetSSHBaseArgsStackAction(target *stackActionTarget, keyPath string) []string {
	return []string{
		"-i", keyPath,
		"-p", strconv.Itoa(target.Port),
		"-o", "IdentitiesOnly=yes",
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "ConnectTimeout=20",
	}
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
		resp.Observation = runtimeObservationTargetFailureStackAction(target, "runtime_target_unreachable")
	} else {
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

func collectStackKitOutputsStackAction(ctx context.Context, exec *tofu.Executor, resp *stackActionResponse) *stackActionStackKitOutputs {
	result, err := exec.Output(ctx)
	if err != nil || result == nil || !result.Success {
		detail := "tofu output unavailable"
		if result != nil && strings.TrimSpace(result.Stderr) != "" {
			detail = strings.TrimSpace(result.Stderr)
		} else if err != nil {
			detail = err.Error()
		}
		resp.Checks = append(resp.Checks, stackActionCheck{Name: "stackkit_outputs", Status: "warning", Detail: detail})
		return stackKitOutputsFromOpenTofuStackAction(*resp, nil)
	}

	values := parseOpenTofuOutputValuesStackAction(result.Stdout)
	resp.Checks = append(resp.Checks, stackActionCheck{Name: "stackkit_outputs", Status: "ok"})
	return stackKitOutputsFromOpenTofuStackAction(*resp, values)
}

func parseOpenTofuOutputValuesStackAction(raw string) map[string]string {
	var document map[string]struct {
		Value interface{} `json:"value"`
	}
	if err := json.Unmarshal([]byte(raw), &document); err != nil {
		return nil
	}
	values := make(map[string]string, len(document))
	for key, output := range document {
		switch v := output.Value.(type) {
		case string:
			values[key] = strings.TrimSpace(v)
		case float64:
			values[key] = strings.TrimSpace(jsonNumberStackAction(v))
		}
	}
	return values
}

func stackKitOutputsFromOpenTofuStackAction(resp stackActionResponse, values map[string]string) *stackActionStackKitOutputs {
	loginURL := firstRuntimeOutputStackAction(values, "tinyauth_login_url", "paas_url", "coolify_url", "dashboard_url", "homepage_url")
	if loginURL == "" {
		loginURL = "https://" + stackSlugStackAction(resp.StackName, resp.StackID) + ".kombify.me"
	}
	ownerEmail := firstRuntimeOutputStackAction(values, "coolify_admin_email", "admin_email")
	ownerUsername := ownerEmail
	if ownerUsername == "" {
		ownerUsername = "owner"
	}
	links := make([]stackActionServiceLink, 0, 15)
	for _, candidate := range []struct {
		name string
		keys []string
	}{
		{name: "base", keys: []string{"dashboard_url"}},
		{name: "homepage", keys: []string{"homepage_url"}},
		{name: "auth", keys: []string{"tinyauth_login_url", "auth_url"}},
		{name: "pocketid", keys: []string{"pocketid_url"}},
		{name: "traefik", keys: []string{"traefik_url"}},
		{name: "coolify", keys: []string{"coolify_url"}},
		{name: "komodo", keys: []string{"komodo_url"}},
		{name: "dokploy", keys: []string{"dokploy_url"}},
		{name: "dockge", keys: []string{"dockge_url"}},
		{name: "monitoring", keys: []string{"kuma_url"}},
		{name: "whoami", keys: []string{"whoami_url"}},
		{name: "vaultwarden", keys: []string{"vaultwarden_url"}},
		{name: "jellyfin", keys: []string{"jellyfin_url"}},
		{name: "immich", keys: []string{"immich_url"}},
		{name: "files", keys: []string{"files_url"}},
	} {
		if url := firstRuntimeOutputStackAction(values, candidate.keys...); url != "" {
			links = append(links, stackActionServiceLink{Name: candidate.name, URL: url})
		}
	}
	return &stackActionStackKitOutputs{
		Identity: &stackActionIdentityOutputs{
			Owner: &stackActionOwnerOutput{
				Username:    ownerUsername,
				Email:       ownerEmail,
				DisplayName: "Owner",
			},
			Recovery: &stackActionRecoveryOutput{
				BundleRef: "techstack://recovery/stacks/" + resp.StackID,
			},
		},
		LoginGateway: &stackActionLoginGateway{
			URL:   loginURL,
			Label: "Open first login",
		},
		Recovery: &stackActionRecoveryOutput{
			BundleRef: "techstack://recovery/stacks/" + resp.StackID,
		},
		Services: links,
	}
}

func firstRuntimeOutputStackAction(values map[string]string, keys ...string) string {
	for _, key := range keys {
		if values != nil && strings.TrimSpace(values[key]) != "" {
			return strings.TrimSpace(values[key])
		}
	}
	return ""
}

func stackSlugStackAction(values ...string) string {
	for _, value := range values {
		slug := strings.ToLower(strings.TrimSpace(value))
		slug = strings.Map(func(r rune) rune {
			switch {
			case r >= 'a' && r <= 'z':
				return r
			case r >= '0' && r <= '9':
				return r
			case r == '-':
				return r
			default:
				return '-'
			}
		}, slug)
		slug = strings.Trim(slug, "-")
		for strings.Contains(slug, "--") {
			slug = strings.ReplaceAll(slug, "--", "-")
		}
		if slug != "" {
			return slug
		}
	}
	return "techstack"
}

func jsonNumberStackAction(value float64) string {
	if value == float64(int64(value)) {
		return strconv.FormatInt(int64(value), 10)
	}
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func runRestoreDrillVerifierStackAction(ctx context.Context, resp stackActionResponse, command string, target *stackActionTarget, resolver StackActionReferenceResolver) (stackActionResponse, int, *skerrors.StackKitError) {
	command = strings.TrimSpace(command)
	if command == "" {
		return runBuiltInRestoreDrillVerifierStackAction(ctx, resp, target, resolver)
	}
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return resp, http.StatusBadRequest, skerrors.NewValidationError("missing_restore_drill_command", "restore drill verifier command is empty")
	}

	runCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(runCtx, fields[0], fields[1:]...)
	if strings.TrimSpace(resp.TofuDir) != "" {
		cmd.Dir = filepath.Clean(resp.TofuDir)
	}
	cmd.Env = append(os.Environ(),
		"STACKKIT_STACK_ACTION="+string(resp.Action),
		"STACKKIT_STACK_ID="+resp.StackID,
		"STACKKIT_STACK_NAME="+resp.StackName,
		"STACKKIT_STACKKIT="+resp.StackKit,
		"STACKKIT_TOFU_DIR="+resp.TofuDir,
		"STACKKIT_UNIFIED_PATH="+resp.UnifiedPath,
	)
	output, err := cmd.CombinedOutput()
	detail := strings.TrimSpace(string(output))
	if detail == "" {
		detail = "restore verifier completed"
	}
	if err != nil {
		resp.Status = stackaction.StatusFailed
		resp.Checks = append(resp.Checks, stackActionCheck{Name: "restore_drill_verifier", Status: stackaction.CheckStatusFailed, Detail: detail})
		return resp, http.StatusBadGateway, tofuActionErrorStackAction("restore_drill_failed", "Restore drill verifier failed", err, detail)
	}

	resp.Status = stackaction.StatusVerified
	resp.Checks = append(resp.Checks, stackActionCheck{Name: "restore_drill_verifier", Status: stackaction.CheckStatusOK, Detail: detail})
	return resp, http.StatusOK, nil
}

func runBuiltInRestoreDrillVerifierStackAction(ctx context.Context, resp stackActionResponse, target *stackActionTarget, resolver StackActionReferenceResolver) (stackActionResponse, int, *skerrors.StackKitError) {
	if err := requireLocalTofuDirStackAction(resp.TofuDir); err != nil {
		return resp, http.StatusBadRequest, err
	}
	resp.Checks = append(resp.Checks, stackActionCheck{
		Name:   "restore_drill_adapter",
		Status: "ok",
		Detail: "using built-in runtime smoke restore verifier",
	})

	statePath := filepath.Join(filepath.Clean(resp.TofuDir), "terraform.tfstate")
	info, err := os.Stat(statePath)
	if err != nil || info.Size() == 0 {
		detail := fmt.Sprintf("OpenTofu state missing or empty at %s", statePath)
		if err != nil {
			detail = detail + ": " + err.Error()
		}
		resp.Status = "failed"
		resp.Checks = append(resp.Checks, stackActionCheck{Name: "opentofu_state", Status: "failed", Detail: detail})
		return resp, http.StatusBadGateway, tofuActionErrorStackAction("restore_drill_failed", "Restore drill verifier failed", fmt.Errorf("%s", detail), detail)
	}
	resp.Checks = append(resp.Checks, stackActionCheck{Name: "opentofu_state", Status: "ok", Detail: statePath})

	remote, cleanup, remoteErr := prepareStackActionRemoteTarget(ctx, resp.TofuDir, target, resolver)
	if cleanup != nil {
		defer cleanup()
	}
	if remoteErr != nil {
		return resp, http.StatusBadGateway, tofuActionErrorStackAction("restore_drill_failed", "Restore drill verifier failed", remoteErr, "runtime target preparation failed")
	}

	runCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(runCtx, "docker", "ps", "--format", "{{.Names}}\t{{.Status}}") // #nosec G204 -- static docker command.
	cmd.Dir = filepath.Clean(resp.TofuDir)
	cmd.Env = os.Environ()
	if remote != nil {
		cmd.Env = append(cmd.Env, remote.env...)
	}
	output, err := cmd.CombinedOutput()
	detail := strings.TrimSpace(string(output))
	if err != nil {
		if detail == "" {
			detail = err.Error()
		} else {
			detail = detail + ": " + err.Error()
		}
		resp.Status = "failed"
		resp.Checks = append(resp.Checks, stackActionCheck{Name: "docker_runtime", Status: "failed", Detail: detail})
		return resp, http.StatusBadGateway, tofuActionErrorStackAction("restore_drill_failed", "Restore drill verifier failed", err, detail)
	}

	running := runtimeDockerPSLinesStackAction(detail)
	if len(running) == 0 {
		resp.Status = "failed"
		resp.Checks = append(resp.Checks, stackActionCheck{Name: "docker_runtime", Status: "failed", Detail: "no running Docker containers"})
		return resp, http.StatusBadGateway, tofuActionErrorStackAction("restore_drill_failed", "Restore drill verifier failed", fmt.Errorf("no running Docker containers"), detail)
	}
	resp.Checks = append(resp.Checks, stackActionCheck{Name: "docker_runtime", Status: "ok", Detail: fmt.Sprintf("%d running containers", len(running))})

	if baseKitCoolifyEnabledStackAction(resp.TofuDir) {
		if !runtimeDockerPSHasContainerStackAction(running, "coolify") {
			resp.Status = "failed"
			resp.Checks = append(resp.Checks, stackActionCheck{Name: "coolify_runtime", Status: "failed", Detail: detail})
			return resp, http.StatusBadGateway, tofuActionErrorStackAction("restore_drill_failed", "Restore drill verifier failed", fmt.Errorf("coolify container is not running"), detail)
		}
		platformPath := filepath.Join(filepath.Clean(resp.TofuDir), ".stackkit", "platform.json")
		if info, err := os.Stat(platformPath); err != nil || info.Size() == 0 {
			detail := fmt.Sprintf("Coolify platform config missing or empty at %s", platformPath)
			if err != nil {
				detail = detail + ": " + err.Error()
			}
			resp.Status = "failed"
			resp.Checks = append(resp.Checks, stackActionCheck{Name: "coolify_platform_config", Status: "failed", Detail: detail})
			return resp, http.StatusBadGateway, tofuActionErrorStackAction("restore_drill_failed", "Restore drill verifier failed", fmt.Errorf("%s", detail), detail)
		}
		resp.Checks = append(resp.Checks, stackActionCheck{Name: "coolify_runtime", Status: "ok", Detail: "coolify container running"})
		resp.Checks = append(resp.Checks, stackActionCheck{Name: "coolify_platform_config", Status: "ok", Detail: platformPath})
	}

	if metrics, err := collectRuntimeHostMetricsStackAction(ctx, remote); err != nil {
		resp.Checks = append(resp.Checks, stackActionCheck{Name: "runtime_metrics", Status: "warning", Detail: err.Error()})
	} else if metrics != nil {
		resp.RuntimeMetrics = metrics
		resp.Checks = append(resp.Checks, stackActionCheck{Name: "runtime_metrics", Status: "ok", Detail: "host resource metrics collected"})
	}

	resp.Status = "verified"
	resp.Checks = append(resp.Checks, stackActionCheck{Name: "restore_drill_verifier", Status: "ok", Detail: "built-in runtime smoke restore verifier completed"})
	return resp, http.StatusOK, nil
}

func collectRuntimeHostMetricsStackAction(ctx context.Context, remote *preparedRuntimeTargetStackAction) (*stackActionRuntimeMetrics, error) {
	if remote == nil || remote.target == nil || strings.TrimSpace(remote.keyPath) == "" {
		return nil, nil
	}
	args := append(runtimeTargetSSHArgsStackAction(remote.target, remote.keyPath), "sh", "-s")
	runCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(runCtx, "ssh", args...) // #nosec G204 -- command args are assembled without shell interpolation.
	cmd.Stdin = strings.NewReader(runtimeHostMetricsScriptStackAction())
	output, err := cmd.CombinedOutput()
	detail := strings.TrimSpace(string(output))
	if err != nil {
		if detail != "" {
			return nil, fmt.Errorf("collect runtime host metrics: %w: %s", err, detail)
		}
		return nil, fmt.Errorf("collect runtime host metrics: %w", err)
	}
	return runtimeHostMetricsFromOutputStackAction(detail, time.Now().UTC())
}

func runtimeHostMetricsScriptStackAction() string {
	return `set -eu
read -r _ user nice system idle iowait irq softirq steal _ < /proc/stat
total1=$((user + nice + system + idle + iowait + irq + softirq + steal))
idle1=$((idle + iowait))
sleep 1
read -r _ user nice system idle iowait irq softirq steal _ < /proc/stat
total2=$((user + nice + system + idle + iowait + irq + softirq + steal))
idle2=$((idle + iowait))
cpu=$(awk -v t1="$total1" -v i1="$idle1" -v t2="$total2" -v i2="$idle2" 'BEGIN { dt=t2-t1; di=i2-i1; if (dt<=0) printf "0"; else printf "%.1f", (dt-di)*100/dt }')
mem=$(awk '/MemTotal:/ {t=$2} /MemAvailable:/ {a=$2} END { if (t>0) printf "%.1f", (t-a)*100/t; else printf "0" }' /proc/meminfo)
disk=$(df -P / | awk 'NR==2 { gsub("%","",$5); printf "%.1f", $5 }')
uptime=$(awk '{ printf "%.0f", $1 }' /proc/uptime)
printf 'cpu_percent=%s\nmemory_percent=%s\ndisk_percent=%s\nuptime_seconds=%s\n' "$cpu" "$mem" "$disk" "$uptime"
`
}

func runtimeHostMetricsFromOutputStackAction(raw string, updatedAt time.Time) (*stackActionRuntimeMetrics, error) {
	metrics := &stackActionRuntimeMetrics{}
	parsed := 0
	for _, line := range strings.Split(raw, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		number, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err != nil {
			return nil, fmt.Errorf("parse runtime host metric %s: %w", key, err)
		}
		switch strings.TrimSpace(key) {
		case "cpu_percent":
			metrics.CPUPercent = number
			parsed++
		case "memory_percent":
			metrics.MemoryPercent = number
			parsed++
		case "disk_percent":
			metrics.DiskPercent = number
			parsed++
		case "uptime_seconds":
			metrics.UptimeSeconds = number
			parsed++
		}
	}
	if parsed == 0 {
		return nil, fmt.Errorf("runtime host metrics output did not contain metrics")
	}
	metrics.UpdatedAt = updatedAt.Format(time.RFC3339)
	return metrics, nil
}

func runtimeDockerPSLinesStackAction(output string) []string {
	lines := []string{}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func runtimeDockerPSHasContainerStackAction(lines []string, name string) bool {
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) > 0 && fields[0] == name {
			return true
		}
	}
	return false
}

func baseKitCoolifyEnabledStackAction(tofuDir string) bool {
	data, err := os.ReadFile(filepath.Join(filepath.Clean(tofuDir), "terraform.tfvars.json"))
	if err != nil {
		return false
	}
	values := map[string]any{}
	if err := json.Unmarshal(data, &values); err != nil {
		return false
	}
	enabled, ok := values["enable_coolify"].(bool)
	return ok && enabled
}

func (s *Server) stackActionMode() stackaction.Mode {
	switch strings.ToLower(strings.TrimSpace(s.config.StackActionMode)) {
	case string(stackActionModeApply):
		return stackActionModeApply
	default:
		return stackActionModeDryRun
	}
}

func hasAnySecretStackAction(secrets []string) bool {
	for _, secret := range secrets {
		if strings.TrimSpace(secret) != "" {
			return true
		}
	}
	return false
}

func dryRunStatusStackAction(action stackaction.Action) stackaction.Status {
	switch action {
	case stackActionRollout:
		return stackaction.StatusReady
	case stackActionVerify:
		return stackaction.StatusVerified
	case stackaction.ActionBackupRun:
		return stackaction.StatusReady
	case stackaction.ActionBackupStatus:
		return stackaction.StatusVerified
	case stackActionRestore, stackaction.ActionBackupRestore, stackaction.ActionBackupWipe:
		return stackaction.StatusSkipped
	default:
		return stackaction.StatusAccepted
	}
}

func appendPathCheckStackAction(checks []stackActionCheck, name, path string, wantDir bool) []stackActionCheck {
	path = strings.TrimSpace(path)
	if path == "" {
		return append(checks, stackActionCheck{Name: name, Status: stackaction.CheckStatusMissing})
	}
	info, err := os.Stat(path)
	if err != nil {
		return append(checks, stackActionCheck{Name: name, Status: stackaction.CheckStatusReference, Detail: path})
	}
	if wantDir && !info.IsDir() {
		return append(checks, stackActionCheck{Name: name, Status: stackaction.CheckStatusWarning, Detail: "path is not a directory"})
	}
	if !wantDir && info.IsDir() {
		return append(checks, stackActionCheck{Name: name, Status: stackaction.CheckStatusWarning, Detail: "path is a directory"})
	}
	return append(checks, stackActionCheck{Name: name, Status: stackaction.CheckStatusOK, Detail: path})
}

func requireLocalTofuDirStackAction(dir string) *skerrors.StackKitError {
	if strings.TrimSpace(dir) == "" {
		return skerrors.NewValidationError("missing_tofu_dir", "tofu_dir is required in apply mode")
	}
	info, err := os.Stat(dir)
	if err != nil {
		return skerrors.NewValidationError("invalid_tofu_dir", "tofu_dir must be readable in apply mode", skerrors.WithField("path", dir), skerrors.WithField("error", err.Error()))
	}
	if !info.IsDir() {
		return skerrors.NewValidationError("invalid_tofu_dir", "tofu_dir must be a directory in apply mode", skerrors.WithField("path", dir))
	}
	hasTF, err := tofu.HasTerraformFiles(filepath.Clean(dir))
	if err != nil {
		return skerrors.NewValidationError("invalid_tofu_dir", "failed to inspect tofu_dir", skerrors.WithField("path", dir), skerrors.WithField("error", err.Error()))
	}
	if !hasTF {
		return skerrors.NewValidationError("missing_tofu_files", "tofu_dir must contain .tf files in apply mode", skerrors.WithField("path", dir))
	}
	return nil
}

func tofuActionErrorStackAction(code, message string, err error, stderr string) *skerrors.StackKitError {
	fields := []skerrors.ErrorOption{}
	if err != nil {
		fields = append(fields, skerrors.WithField("error", err.Error()))
	}
	if strings.TrimSpace(stderr) != "" {
		fields = append(fields, skerrors.WithField("stderr", strings.TrimSpace(stderr)))
	}
	return skerrors.NewDeploymentError(code, message, fields...)
}

func resultStderrStackAction(result *tofu.Result) string {
	if result == nil {
		return ""
	}
	return compactStackActionStderr(result.Stderr)
}

func compactStackActionStderr(stderr string) string {
	const maxRunes = 6000
	trimmed := strings.TrimSpace(stderr)
	if trimmed == "" {
		return ""
	}
	runes := []rune(trimmed)
	if len(runes) <= maxRunes {
		return trimmed
	}
	return fmt.Sprintf("[stderr truncated; showing last %d runes]\n%s", maxRunes, string(runes[len(runes)-maxRunes:]))
}
