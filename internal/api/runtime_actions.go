package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/auth"
	skerrors "github.com/kombifyio/stackkits/internal/errors"
	"github.com/kombifyio/stackkits/internal/platformdeploy"
	"github.com/kombifyio/stackkits/internal/runtimeaction"
	"github.com/kombifyio/stackkits/internal/telemetry"
	"github.com/kombifyio/stackkits/internal/tofu"
	"github.com/kombifyio/stackkits/pkg/models"
)

const (
	runtimeActionModeDryRun = runtimeaction.ModeDryRun
	runtimeActionModeApply  = runtimeaction.ModeApply

	runtimeActionRollout = runtimeaction.ActionStackKitRollout
	runtimeActionVerify  = runtimeaction.ActionVerifyRollout
	runtimeActionRestore = runtimeaction.ActionRestoreDrill

	runtimeActionExecutionTimeout = 14*time.Minute + 30*time.Second
)

type runtimeActionRequest struct {
	Action             runtimeaction.Action              `json:"action"`
	StackID            string                            `json:"stack_id"`
	StackName          string                            `json:"stack_name,omitempty"`
	StackKit           string                            `json:"stackkit,omitempty"`
	TofuDir            string                            `json:"tofu_dir,omitempty"`
	UnifiedPath        string                            `json:"unified_path,omitempty"`
	OwnerSpecBootstrap *runtimeaction.OwnerSpecBootstrap `json:"owner_spec_bootstrap,omitempty"`
	RuntimeTarget      *runtimeActionTarget              `json:"runtime_target,omitempty"`
}

type runtimeActionTarget struct {
	Host             string `json:"host,omitempty"`
	PublicIP         string `json:"public_ip,omitempty"`
	PrivateIP        string `json:"private_ip,omitempty"`
	User             string `json:"user,omitempty"`
	Port             int    `json:"port,omitempty"`
	DockerHost       string `json:"docker_host,omitempty"`
	KeyPath          string `json:"key_path,omitempty"`
	PrivateKey       string `json:"private_key,omitempty"`
	ClientPrivateKey string `json:"client_private_key,omitempty"`
	Password         string `json:"password,omitempty"`
}

type runtimeActionResponse struct {
	Status             runtimeaction.Status           `json:"status"`
	Action             runtimeaction.Action           `json:"action"`
	StackID            string                         `json:"stack_id"`
	StackName          string                         `json:"stack_name,omitempty"`
	StackKit           string                         `json:"stackkit,omitempty"`
	TofuDir            string                         `json:"tofu_dir,omitempty"`
	UnifiedPath        string                         `json:"unified_path,omitempty"`
	Mode               runtimeaction.Mode             `json:"mode"`
	Checks             []runtimeActionCheck           `json:"checks,omitempty"`
	StackKitOutputs    *runtimeActionStackKitOutputs  `json:"stackkit_outputs,omitempty"`
	RuntimeMetrics     *runtimeActionRuntimeMetrics   `json:"runtime_metrics,omitempty"`
	PlatformRefs       []platformdeploy.DeploymentRef `json:"platform_refs,omitempty"`
	PlatformSystemApps []models.PlatformAppState      `json:"platform_system_apps,omitempty"`
	PlatformApps       []models.PlatformAppState      `json:"platform_apps,omitempty"`
}

type runtimeActionCheck = runtimeaction.Check
type runtimeActionStackKitOutputs = runtimeaction.StackKitOutputs
type runtimeActionIdentityOutputs = runtimeaction.IdentityOutputs
type runtimeActionOwnerOutput = runtimeaction.OwnerIdentity
type runtimeActionRecoveryOutput = runtimeaction.RecoveryOutput
type runtimeActionLoginGateway = runtimeaction.LoginGatewayOutput
type runtimeActionServiceLink = runtimeaction.ServiceOutput

type runtimeActionRuntimeMetrics struct {
	CPUPercent    float64 `json:"cpu_percent"`
	MemoryPercent float64 `json:"memory_percent"`
	DiskPercent   float64 `json:"disk_percent"`
	UptimeSeconds float64 `json:"uptime_seconds"`
	UpdatedAt     string  `json:"updated_at,omitempty"`
}

func (s *Server) registerRuntimeActionRoutes() {
	s.mux.Handle("POST /api/v1/internal/runtime-actions/stackkit-rollout",
		s.requireRuntimeActionServiceAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			s.handleRuntimeAction(w, r, runtimeActionRollout)
		})))
	s.mux.Handle("POST /api/v1/internal/runtime-actions/stackkit-verify",
		s.requireRuntimeActionServiceAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			s.handleRuntimeAction(w, r, runtimeActionVerify)
		})))
	s.mux.Handle("POST /api/v1/internal/runtime-actions/restore-drill",
		s.requireRuntimeActionServiceAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			s.handleRuntimeAction(w, r, runtimeActionRestore)
		})))
}

func (s *Server) requireRuntimeActionServiceAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secrets := []string{s.config.ServiceAuthSecret, s.config.ServiceAuthSecretNext}
		if !hasAnySecret(secrets) {
			writeStructuredError(w, r, http.StatusServiceUnavailable, skerrors.NewAuthError(
				"service_auth_not_configured",
				"SERVICE_AUTH_SECRET is required for internal runtime actions",
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

func (s *Server) handleRuntimeAction(w http.ResponseWriter, r *http.Request, expectedAction runtimeaction.Action) {
	var req runtimeActionRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := decoder.Decode(&req); err != nil {
		writeStructuredError(w, r, http.StatusBadRequest, skerrors.NewValidationError(
			"invalid_runtime_action_payload",
			"runtime action payload must be valid JSON",
			skerrors.WithField("error", err.Error()),
		))
		return
	}

	req.Action = runtimeaction.NormalizeAction(string(req.Action))
	if req.Action == "" {
		req.Action = expectedAction
	}
	if req.Action != expectedAction {
		writeStructuredError(w, r, http.StatusBadRequest, skerrors.NewValidationError(
			"invalid_runtime_action",
			"runtime action does not match endpoint",
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

	resp, status, err := s.executeRuntimeAction(r.Context(), req)
	if err != nil {
		writeStructuredError(w, r, status, err)
		return
	}
	writeSuccess(w, r, http.StatusOK, resp)
}

func (s *Server) executeRuntimeAction(ctx context.Context, req runtimeActionRequest) (resp runtimeActionResponse, status int, stackErr *skerrors.StackKitError) {
	mode := s.runtimeActionMode()
	resp = runtimeActionResponse{
		Status:      runtimeaction.StatusAccepted,
		Action:      req.Action,
		StackID:     req.StackID,
		StackName:   strings.TrimSpace(req.StackName),
		StackKit:    strings.TrimSpace(req.StackKit),
		TofuDir:     strings.TrimSpace(req.TofuDir),
		UnifiedPath: strings.TrimSpace(req.UnifiedPath),
		Mode:        mode,
	}
	resp.Checks = append(resp.Checks, runtimeActionCheck{Name: "request", Status: runtimeaction.CheckStatusOK, Detail: "runtime action payload decoded"})
	resp.Checks = appendPathCheck(resp.Checks, "tofu_dir", resp.TofuDir, true)
	resp.Checks = appendPathCheck(resp.Checks, "unified_path", resp.UnifiedPath, false)
	if resp.StackKit == "" {
		resp.Checks = append(resp.Checks, runtimeActionCheck{Name: "stackkit", Status: runtimeaction.CheckStatusWarning, Detail: "stackkit name not provided"})
	} else {
		resp.Checks = append(resp.Checks, runtimeActionCheck{Name: "stackkit", Status: runtimeaction.CheckStatusOK, Detail: resp.StackKit})
	}
	if target := normalizeRuntimeActionTarget(req.RuntimeTarget); target != nil {
		resp.Checks = append(resp.Checks, runtimeActionCheck{Name: "runtime_target", Status: "ok", Detail: target.Host})
	}

	ctx, span := startRuntimeActionSpan(ctx, resp)
	defer func() {
		finishRuntimeActionSpan(span, resp, status, stackErr)
	}()

	includeStackKitOutputs := true
	if mode == runtimeActionModeDryRun {
		resp.Status = dryRunStatus(req.Action)
		if includeStackKitOutputs {
			resp.StackKitOutputs = stackKitOutputsFromOpenTofu(resp, nil)
		}
		return resp, http.StatusOK, nil
	}

	switch req.Action {
	case runtimeActionRollout:
		return runOpenTofuRollout(ctx, resp, includeStackKitOutputs, req.RuntimeTarget)
	case runtimeActionVerify:
		return runOpenTofuVerify(ctx, resp, includeStackKitOutputs)
	case runtimeActionRestore:
		return runRestoreDrillVerifier(ctx, resp, s.config.RuntimeRestoreVerifierCommand, req.RuntimeTarget)
	default:
		return resp, http.StatusBadRequest, skerrors.NewValidationError("invalid_runtime_action", "unsupported runtime action")
	}
}

func startRuntimeActionSpan(ctx context.Context, resp runtimeActionResponse) (context.Context, telemetry.SpanHandle) {
	action := strings.TrimSpace(string(resp.Action))
	if action == "" {
		action = "unknown"
	}
	return telemetry.StartSpan(ctx, "stackkit.server.runtime_action."+action, runtimeActionSpanAttributes(resp, 0, nil))
}

func finishRuntimeActionSpan(span telemetry.SpanHandle, resp runtimeActionResponse, status int, stackErr *skerrors.StackKitError) {
	if status == 0 {
		status = http.StatusInternalServerError
	}
	span.SetAttributes(runtimeActionSpanAttributes(resp, status, stackErr))
	if stackErr != nil {
		span.RecordError(stackErr)
		span.SetRolloutStatus("failed", stackErr.Message)
		span.End()
		return
	}
	if status >= http.StatusBadRequest || resp.Status == runtimeaction.StatusFailed {
		span.SetRolloutStatus("failed", string(resp.Status))
	} else {
		span.SetRolloutStatus("succeeded", "")
	}
	span.End()
}

func runtimeActionSpanAttributes(resp runtimeActionResponse, status int, stackErr *skerrors.StackKitError) map[string]string {
	attrs := map[string]string{
		"stackkit.runtime_action":        string(resp.Action),
		"stackkit.runtime_action.status": string(resp.Status),
		"stackkit.runtime_action.mode":   string(resp.Mode),
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

func startRuntimeOperationSpan(ctx context.Context, resp runtimeActionResponse, operation string) (context.Context, telemetry.SpanHandle) {
	attrs := runtimeActionSpanAttributes(resp, 0, nil)
	attrs["stackkit.runtime_action.operation"] = operation
	return telemetry.StartSpan(ctx, "stackkit.server.runtime_action."+operation, attrs)
}

func finishRuntimeOperationSpan(span telemetry.SpanHandle, resp runtimeActionResponse, operation string, err error) {
	attrs := runtimeActionSpanAttributes(resp, 0, nil)
	attrs["stackkit.runtime_action.operation"] = operation
	if err != nil {
		attrs["stackkit.runtime_action.operation_status"] = "failed"
		span.SetAttributes(attrs)
		span.RecordError(err)
		span.SetRolloutStatus("failed", err.Error())
		span.End()
		return
	}
	attrs["stackkit.runtime_action.operation_status"] = "succeeded"
	span.SetAttributes(attrs)
	span.SetRolloutStatus("succeeded", "")
	span.End()
}

func runtimeOperationResultError(operation string, result *tofu.Result, err error) error {
	if err != nil {
		return err
	}
	if result == nil {
		return fmt.Errorf("%s did not return a result", operation)
	}
	if !result.Success {
		detail := resultStderr(result)
		if strings.TrimSpace(detail) == "" {
			detail = fmt.Sprintf("%s exited with code %d", operation, result.ExitCode)
		}
		return fmt.Errorf("%s failed: %s", operation, detail)
	}
	return nil
}

func runOpenTofuRollout(ctx context.Context, resp runtimeActionResponse, includeStackKitOutputs bool, target *runtimeActionTarget) (runtimeActionResponse, int, *skerrors.StackKitError) {
	if err := requireLocalTofuDir(resp.TofuDir); err != nil {
		return resp, http.StatusBadRequest, err
	}
	remote, cleanup, remoteErr := prepareRuntimeActionRemoteTarget(ctx, resp.TofuDir, target)
	if cleanup != nil {
		defer cleanup()
	}
	if remoteErr != nil {
		return resp, http.StatusBadGateway, tofuActionError("runtime_target_prepare_failed", "Runtime target preparation failed", remoteErr, "")
	}
	opts := []tofu.ExecutorOption{tofu.WithWorkDir(resp.TofuDir), tofu.WithAutoApprove(true), tofu.WithTimeout(runtimeActionExecutionTimeout)}
	if remote != nil {
		opts = append(opts, tofu.WithEnv(remote.env...))
		resp.Checks = append(resp.Checks, runtimeActionCheck{Name: "remote_docker_host", Status: "ok", Detail: remote.dockerHost})
	}
	exec := tofu.NewExecutor(opts...)
	var result *tofu.Result
	var err error
	ctx, initSpan := startRuntimeOperationSpan(ctx, resp, "tofu_init")
	result, err = exec.Init(ctx)
	initErr := runtimeOperationResultError("tofu_init", result, err)
	finishRuntimeOperationSpan(initSpan, resp, "tofu_init", initErr)
	if initErr != nil {
		return resp, http.StatusBadGateway, tofuActionError("opentofu_init_failed", "OpenTofu init failed", err, resultStderr(result))
	}
	ctx, applySpan := startRuntimeOperationSpan(ctx, resp, "tofu_apply")
	result, err = exec.Apply(ctx, "")
	applyErr := runtimeOperationResultError("tofu_apply", result, err)
	finishRuntimeOperationSpan(applySpan, resp, "tofu_apply", applyErr)
	if applyErr != nil {
		return resp, http.StatusBadGateway, tofuActionError("opentofu_apply_failed", "OpenTofu apply failed", err, resultStderr(result))
	}
	resp.Status = runtimeaction.StatusApplied
	resp.Checks = append(resp.Checks, runtimeActionCheck{Name: "opentofu_apply", Status: runtimeaction.CheckStatusOK})
	if remote != nil {
		ctx, syncSpan := startRuntimeOperationSpan(ctx, resp, "runtime_workspace_sync")
		syncErr := syncRuntimeTargetWorkspace(ctx, remote, resp.TofuDir)
		finishRuntimeOperationSpan(syncSpan, resp, "runtime_workspace_sync", syncErr)
		if syncErr != nil {
			return resp, http.StatusBadGateway, tofuActionError("runtime_target_sync_failed", "Runtime target workspace sync failed", syncErr, "")
		}
		resp.Checks = append(resp.Checks, runtimeActionCheck{Name: "runtime_workspace_sync", Status: runtimeaction.CheckStatusOK, Detail: remote.workspaceRoot})
	}
	ctx, platformSpan := startRuntimeOperationSpan(ctx, resp, "platform_apps_deploy")
	platformEvidence, platformChecks, platformErr := runRuntimePlatformAppDeployments(ctx, resp.TofuDir, runtimePlatformDeployOptions{Remote: remote})
	finishRuntimeOperationSpan(platformSpan, resp, "platform_apps_deploy", platformErr)
	resp.PlatformRefs = platformEvidence.Refs
	resp.PlatformSystemApps = platformEvidence.SystemApps
	resp.PlatformApps = platformEvidence.Apps
	resp.Checks = append(resp.Checks, platformChecks...)
	if platformErr != nil {
		return resp, http.StatusBadGateway, tofuActionError("platform_apps_deploy_failed", "Platform app deployment failed", platformErr, "")
	}
	if includeStackKitOutputs {
		ctx, outputSpan := startRuntimeOperationSpan(ctx, resp, "stackkit_outputs")
		resp.StackKitOutputs = collectStackKitOutputs(ctx, exec, &resp)
		finishRuntimeOperationSpan(outputSpan, resp, "stackkit_outputs", nil)
	}
	return resp, http.StatusOK, nil
}

type preparedRuntimeTarget struct {
	dockerHost    string
	env           []string
	target        *runtimeActionTarget
	keyPath       string
	workspaceRoot string
}

func prepareRuntimeActionRemoteTarget(ctx context.Context, tofuDir string, target *runtimeActionTarget) (*preparedRuntimeTarget, func(), error) {
	target = normalizeRuntimeActionTarget(target)
	if target == nil {
		return nil, nil, nil
	}
	keyPath, homeDir, cleanup, err := materializeRuntimeTargetSSHKey(target)
	if err != nil {
		return nil, cleanup, err
	}
	if err := bootstrapRuntimeTargetDocker(ctx, target, keyPath); err != nil {
		return nil, cleanup, err
	}
	dockerHost := runtimeTargetDockerHost(target)
	workspaceRoot := runtimeTargetWorkspaceRoot(tofuDir)
	if err := writeRuntimeTargetDockerHostTFVars(tofuDir, dockerHost, workspaceRoot); err != nil {
		return nil, cleanup, err
	}
	env := []string{"DOCKER_HOST=" + dockerHost}
	if homeDir != "" {
		env = append(env, "HOME="+homeDir)
	}
	if keyPath != "" {
		env = append(env, "DOCKER_SSH_COMMAND="+runtimeTargetSSHCommand(target, keyPath))
	}
	return &preparedRuntimeTarget{dockerHost: dockerHost, env: env, target: target, keyPath: keyPath, workspaceRoot: workspaceRoot}, cleanup, nil
}

func normalizeRuntimeActionTarget(target *runtimeActionTarget) *runtimeActionTarget {
	if target == nil {
		return nil
	}
	normalized := *target
	normalized.Host = firstRuntimeOutput(map[string]string{
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
	normalized.KeyPath = strings.TrimSpace(normalized.KeyPath)
	normalized.PrivateKey = strings.TrimSpace(normalized.PrivateKey)
	normalized.ClientPrivateKey = strings.TrimSpace(normalized.ClientPrivateKey)
	normalized.Password = strings.TrimSpace(normalized.Password)
	if normalized.Host == "" {
		return nil
	}
	return &normalized
}

func materializeRuntimeTargetSSHKey(target *runtimeActionTarget) (string, string, func(), error) {
	key := firstRuntimeOutput(map[string]string{
		"client_private_key": target.ClientPrivateKey,
		"private_key":        target.PrivateKey,
	}, "client_private_key", "private_key")
	if key == "" {
		if target.KeyPath != "" {
			return target.KeyPath, "", nil, nil
		}
		return "", "", nil, fmt.Errorf("runtime target SSH private key is required for remote Docker access")
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
	if err := os.WriteFile(keyPath, []byte(strings.TrimSpace(key)+"\n"), 0600); err != nil {
		_ = os.RemoveAll(dir)
		return "", "", nil, fmt.Errorf("write runtime SSH key: %w", err)
	}
	config := runtimeSSHConfig(target, keyPath)
	if err := os.WriteFile(filepath.Join(sshDir, "config"), []byte(config), 0600); err != nil {
		_ = os.RemoveAll(dir)
		return "", "", nil, fmt.Errorf("write runtime SSH config: %w", err)
	}
	restoreUserConfig, err := installRuntimeUserSSHConfig(target, keyPath)
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

func runtimeSSHConfig(target *runtimeActionTarget, keyPath string) string {
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

func installRuntimeUserSSHConfig(target *runtimeActionTarget, keyPath string) (func(), error) {
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
	next := append([]byte(runtimeSSHConfig(target, keyPath)+"\n"), previous...)
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

func bootstrapRuntimeTargetDocker(ctx context.Context, target *runtimeActionTarget, keyPath string) error {
	if keyPath == "" {
		return fmt.Errorf("runtime target SSH key path is required")
	}
	args := runtimeTargetSSHArgs(target, keyPath)
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

func writeRuntimeTargetDockerHostTFVars(tofuDir, dockerHost string, workspaceRoot ...string) error {
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

func runtimeTargetWorkspaceRoot(tofuDir string) string {
	workspaceDir := filepath.Clean(filepath.Join(filepath.Clean(tofuDir), ".."))
	sum := sha256.Sum256([]byte(workspaceDir))
	hash := fmt.Sprintf("%x", sum[:])
	return "/opt/stackkits/runtime-workspaces/" + hash[:16]
}

func runtimeTargetDockerHost(target *runtimeActionTarget) string {
	if target.DockerHost != "" {
		return target.DockerHost
	}
	host := target.Host
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		host = "[" + host + "]"
	}
	if target.Port > 0 && target.Port != 22 {
		host = host + ":" + strconv.Itoa(target.Port)
	}
	return "ssh://" + target.User + "@" + host
}

func runtimeTargetSSHCommand(target *runtimeActionTarget, keyPath string) string {
	return strings.Join(runtimeTargetSSHBaseArgs(target, keyPath), " ")
}

func runtimeTargetSSHArgs(target *runtimeActionTarget, keyPath string) []string {
	return append(runtimeTargetSSHBaseArgs(target, keyPath), target.User+"@"+target.Host)
}

func syncRuntimeTargetWorkspace(ctx context.Context, remote *preparedRuntimeTarget, tofuDir string) error {
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

	tarCmd := exec.CommandContext(syncCtx, "tar", runtimeTargetWorkspaceArchiveArgs(workspaceDir)...) // #nosec G204 -- workspaceDir is a local path, passed as an argv value.
	pipe, err := tarCmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("prepare runtime workspace archive: %w", err)
	}

	script := runtimeTargetWorkspaceSyncScript(remote.workspaceRoot)
	sshArgs := append(runtimeTargetSSHArgs(remote.target, remote.keyPath), "sh", "-c", shellQuote(script))
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

func runtimeTargetWorkspaceArchiveArgs(workspaceDir string) []string {
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

func runtimeTargetWorkspaceSyncScript(workspaceRoot string) string {
	remotePath := shellQuote(workspaceRoot)
	return "rm -rf -- " + remotePath + " && mkdir -p " + remotePath + " && tar -C " + remotePath + " -xf -"
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func runtimeTargetSSHBaseArgs(target *runtimeActionTarget, keyPath string) []string {
	return []string{
		"-i", keyPath,
		"-p", strconv.Itoa(target.Port),
		"-o", "IdentitiesOnly=yes",
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "ConnectTimeout=20",
	}
}

func runOpenTofuVerify(ctx context.Context, resp runtimeActionResponse, includeStackKitOutputs bool) (runtimeActionResponse, int, *skerrors.StackKitError) {
	if err := requireLocalTofuDir(resp.TofuDir); err != nil {
		return resp, http.StatusBadRequest, err
	}
	exec := tofu.NewExecutor(tofu.WithWorkDir(resp.TofuDir), tofu.WithTimeout(5*time.Minute))
	ctx, stateSpan := startRuntimeOperationSpan(ctx, resp, "tofu_state")
	result, err := exec.State(ctx)
	stateErr := runtimeOperationResultError("tofu_state", result, err)
	finishRuntimeOperationSpan(stateSpan, resp, "tofu_state", stateErr)
	if stateErr != nil {
		return resp, http.StatusBadGateway, tofuActionError("opentofu_state_failed", "OpenTofu state verification failed", err, resultStderr(result))
	}
	resp.Status = runtimeaction.StatusVerified
	resp.Checks = append(resp.Checks, runtimeActionCheck{Name: "opentofu_state", Status: runtimeaction.CheckStatusOK})
	if includeStackKitOutputs {
		ctx, outputSpan := startRuntimeOperationSpan(ctx, resp, "stackkit_outputs")
		resp.StackKitOutputs = collectStackKitOutputs(ctx, exec, &resp)
		finishRuntimeOperationSpan(outputSpan, resp, "stackkit_outputs", nil)
	}
	return resp, http.StatusOK, nil
}

func collectStackKitOutputs(ctx context.Context, exec *tofu.Executor, resp *runtimeActionResponse) *runtimeActionStackKitOutputs {
	result, err := exec.Output(ctx)
	if err != nil || result == nil || !result.Success {
		detail := "tofu output unavailable"
		if result != nil && strings.TrimSpace(result.Stderr) != "" {
			detail = strings.TrimSpace(result.Stderr)
		} else if err != nil {
			detail = err.Error()
		}
		resp.Checks = append(resp.Checks, runtimeActionCheck{Name: "stackkit_outputs", Status: "warning", Detail: detail})
		return stackKitOutputsFromOpenTofu(*resp, nil)
	}

	values := parseOpenTofuOutputValues(result.Stdout)
	resp.Checks = append(resp.Checks, runtimeActionCheck{Name: "stackkit_outputs", Status: "ok"})
	return stackKitOutputsFromOpenTofu(*resp, values)
}

func parseOpenTofuOutputValues(raw string) map[string]string {
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
			values[key] = strings.TrimSpace(jsonNumber(v))
		}
	}
	return values
}

func stackKitOutputsFromOpenTofu(resp runtimeActionResponse, values map[string]string) *runtimeActionStackKitOutputs {
	loginURL := firstRuntimeOutput(values, "tinyauth_login_url", "paas_url", "coolify_url", "dashboard_url", "homepage_url")
	if loginURL == "" {
		loginURL = "https://" + stackSlug(resp.StackName, resp.StackID) + ".kombify.me"
	}
	ownerEmail := firstRuntimeOutput(values, "coolify_admin_email", "admin_email")
	ownerUsername := ownerEmail
	if ownerUsername == "" {
		ownerUsername = "owner"
	}
	links := make([]runtimeActionServiceLink, 0, 10)
	for _, candidate := range []struct {
		name string
		keys []string
	}{
		{name: "base", keys: []string{"dashboard_url"}},
		{name: "homepage", keys: []string{"homepage_url"}},
		{name: "auth", keys: []string{"tinyauth_login_url", "auth_url"}},
		{name: "pocketid", keys: []string{"pocketid_url"}},
		{name: "coolify", keys: []string{"coolify_url"}},
		{name: "monitoring", keys: []string{"kuma_url"}},
		{name: "whoami", keys: []string{"whoami_url"}},
		{name: "vaultwarden", keys: []string{"vaultwarden_url"}},
		{name: "immich", keys: []string{"immich_url"}},
		{name: "files", keys: []string{"files_url"}},
	} {
		if url := firstRuntimeOutput(values, candidate.keys...); url != "" {
			links = append(links, runtimeActionServiceLink{Name: candidate.name, URL: url})
		}
	}
	return &runtimeActionStackKitOutputs{
		Identity: &runtimeActionIdentityOutputs{
			Owner: &runtimeActionOwnerOutput{
				Username:    ownerUsername,
				Email:       ownerEmail,
				DisplayName: "Owner",
			},
			Recovery: &runtimeActionRecoveryOutput{
				BundleRef: "techstack://recovery/stacks/" + resp.StackID,
			},
		},
		LoginGateway: &runtimeActionLoginGateway{
			URL:   loginURL,
			Label: "Open first login",
		},
		Recovery: &runtimeActionRecoveryOutput{
			BundleRef: "techstack://recovery/stacks/" + resp.StackID,
		},
		Services: links,
	}
}

func firstRuntimeOutput(values map[string]string, keys ...string) string {
	for _, key := range keys {
		if values != nil && strings.TrimSpace(values[key]) != "" {
			return strings.TrimSpace(values[key])
		}
	}
	return ""
}

func stackSlug(values ...string) string {
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

func jsonNumber(value float64) string {
	if value == float64(int64(value)) {
		return strconv.FormatInt(int64(value), 10)
	}
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func runRestoreDrillVerifier(ctx context.Context, resp runtimeActionResponse, command string, target *runtimeActionTarget) (runtimeActionResponse, int, *skerrors.StackKitError) {
	command = strings.TrimSpace(command)
	if command == "" {
		return runBuiltInRestoreDrillVerifier(ctx, resp, target)
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
		"STACKKIT_RUNTIME_ACTION="+string(resp.Action),
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
		resp.Status = runtimeaction.StatusFailed
		resp.Checks = append(resp.Checks, runtimeActionCheck{Name: "restore_drill_verifier", Status: runtimeaction.CheckStatusFailed, Detail: detail})
		return resp, http.StatusBadGateway, tofuActionError("restore_drill_failed", "Restore drill verifier failed", err, detail)
	}

	resp.Status = runtimeaction.StatusVerified
	resp.Checks = append(resp.Checks, runtimeActionCheck{Name: "restore_drill_verifier", Status: runtimeaction.CheckStatusOK, Detail: detail})
	return resp, http.StatusOK, nil
}

func runBuiltInRestoreDrillVerifier(ctx context.Context, resp runtimeActionResponse, target *runtimeActionTarget) (runtimeActionResponse, int, *skerrors.StackKitError) {
	if err := requireLocalTofuDir(resp.TofuDir); err != nil {
		return resp, http.StatusBadRequest, err
	}
	resp.Checks = append(resp.Checks, runtimeActionCheck{
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
		resp.Checks = append(resp.Checks, runtimeActionCheck{Name: "opentofu_state", Status: "failed", Detail: detail})
		return resp, http.StatusBadGateway, tofuActionError("restore_drill_failed", "Restore drill verifier failed", fmt.Errorf("%s", detail), detail)
	}
	resp.Checks = append(resp.Checks, runtimeActionCheck{Name: "opentofu_state", Status: "ok", Detail: statePath})

	remote, cleanup, remoteErr := prepareRuntimeActionRemoteTarget(ctx, resp.TofuDir, target)
	if cleanup != nil {
		defer cleanup()
	}
	if remoteErr != nil {
		return resp, http.StatusBadGateway, tofuActionError("restore_drill_failed", "Restore drill verifier failed", remoteErr, "runtime target preparation failed")
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
		resp.Checks = append(resp.Checks, runtimeActionCheck{Name: "docker_runtime", Status: "failed", Detail: detail})
		return resp, http.StatusBadGateway, tofuActionError("restore_drill_failed", "Restore drill verifier failed", err, detail)
	}

	running := runtimeDockerPSLines(detail)
	if len(running) == 0 {
		resp.Status = "failed"
		resp.Checks = append(resp.Checks, runtimeActionCheck{Name: "docker_runtime", Status: "failed", Detail: "no running Docker containers"})
		return resp, http.StatusBadGateway, tofuActionError("restore_drill_failed", "Restore drill verifier failed", fmt.Errorf("no running Docker containers"), detail)
	}
	resp.Checks = append(resp.Checks, runtimeActionCheck{Name: "docker_runtime", Status: "ok", Detail: fmt.Sprintf("%d running containers", len(running))})

	if baseKitCoolifyEnabled(resp.TofuDir) {
		if !runtimeDockerPSHasContainer(running, "coolify") {
			resp.Status = "failed"
			resp.Checks = append(resp.Checks, runtimeActionCheck{Name: "coolify_runtime", Status: "failed", Detail: detail})
			return resp, http.StatusBadGateway, tofuActionError("restore_drill_failed", "Restore drill verifier failed", fmt.Errorf("coolify container is not running"), detail)
		}
		platformPath := filepath.Join(filepath.Clean(resp.TofuDir), ".stackkit", "platform.json")
		if info, err := os.Stat(platformPath); err != nil || info.Size() == 0 {
			detail := fmt.Sprintf("Coolify platform config missing or empty at %s", platformPath)
			if err != nil {
				detail = detail + ": " + err.Error()
			}
			resp.Status = "failed"
			resp.Checks = append(resp.Checks, runtimeActionCheck{Name: "coolify_platform_config", Status: "failed", Detail: detail})
			return resp, http.StatusBadGateway, tofuActionError("restore_drill_failed", "Restore drill verifier failed", fmt.Errorf("%s", detail), detail)
		}
		resp.Checks = append(resp.Checks, runtimeActionCheck{Name: "coolify_runtime", Status: "ok", Detail: "coolify container running"})
		resp.Checks = append(resp.Checks, runtimeActionCheck{Name: "coolify_platform_config", Status: "ok", Detail: platformPath})
	}

	if metrics, err := collectRuntimeHostMetrics(ctx, remote); err != nil {
		resp.Checks = append(resp.Checks, runtimeActionCheck{Name: "runtime_metrics", Status: "warning", Detail: err.Error()})
	} else if metrics != nil {
		resp.RuntimeMetrics = metrics
		resp.Checks = append(resp.Checks, runtimeActionCheck{Name: "runtime_metrics", Status: "ok", Detail: "host resource metrics collected"})
	}

	resp.Status = "verified"
	resp.Checks = append(resp.Checks, runtimeActionCheck{Name: "restore_drill_verifier", Status: "ok", Detail: "built-in runtime smoke restore verifier completed"})
	return resp, http.StatusOK, nil
}

func collectRuntimeHostMetrics(ctx context.Context, remote *preparedRuntimeTarget) (*runtimeActionRuntimeMetrics, error) {
	if remote == nil || remote.target == nil || strings.TrimSpace(remote.keyPath) == "" {
		return nil, nil
	}
	args := append(runtimeTargetSSHArgs(remote.target, remote.keyPath), "sh", "-s")
	runCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(runCtx, "ssh", args...) // #nosec G204 -- command args are assembled without shell interpolation.
	cmd.Stdin = strings.NewReader(runtimeHostMetricsScript())
	output, err := cmd.CombinedOutput()
	detail := strings.TrimSpace(string(output))
	if err != nil {
		if detail != "" {
			return nil, fmt.Errorf("collect runtime host metrics: %w: %s", err, detail)
		}
		return nil, fmt.Errorf("collect runtime host metrics: %w", err)
	}
	return runtimeHostMetricsFromOutput(detail, time.Now().UTC())
}

func runtimeHostMetricsScript() string {
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

func runtimeHostMetricsFromOutput(raw string, updatedAt time.Time) (*runtimeActionRuntimeMetrics, error) {
	metrics := &runtimeActionRuntimeMetrics{}
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

func runtimeDockerPSLines(output string) []string {
	lines := []string{}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func runtimeDockerPSHasContainer(lines []string, name string) bool {
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) > 0 && fields[0] == name {
			return true
		}
	}
	return false
}

func baseKitCoolifyEnabled(tofuDir string) bool {
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

func (s *Server) runtimeActionMode() runtimeaction.Mode {
	switch strings.ToLower(strings.TrimSpace(s.config.RuntimeActionMode)) {
	case string(runtimeActionModeApply):
		return runtimeActionModeApply
	default:
		return runtimeActionModeDryRun
	}
}

func hasAnySecret(secrets []string) bool {
	for _, secret := range secrets {
		if strings.TrimSpace(secret) != "" {
			return true
		}
	}
	return false
}

func dryRunStatus(action runtimeaction.Action) runtimeaction.Status {
	switch action {
	case runtimeActionRollout:
		return runtimeaction.StatusReady
	case runtimeActionVerify:
		return runtimeaction.StatusVerified
	case runtimeActionRestore:
		return runtimeaction.StatusSkipped
	default:
		return runtimeaction.StatusAccepted
	}
}

func appendPathCheck(checks []runtimeActionCheck, name, path string, wantDir bool) []runtimeActionCheck {
	path = strings.TrimSpace(path)
	if path == "" {
		return append(checks, runtimeActionCheck{Name: name, Status: runtimeaction.CheckStatusMissing})
	}
	info, err := os.Stat(path)
	if err != nil {
		return append(checks, runtimeActionCheck{Name: name, Status: runtimeaction.CheckStatusReference, Detail: path})
	}
	if wantDir && !info.IsDir() {
		return append(checks, runtimeActionCheck{Name: name, Status: runtimeaction.CheckStatusWarning, Detail: "path is not a directory"})
	}
	if !wantDir && info.IsDir() {
		return append(checks, runtimeActionCheck{Name: name, Status: runtimeaction.CheckStatusWarning, Detail: "path is a directory"})
	}
	return append(checks, runtimeActionCheck{Name: name, Status: runtimeaction.CheckStatusOK, Detail: path})
}

func requireLocalTofuDir(dir string) *skerrors.StackKitError {
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

func tofuActionError(code, message string, err error, stderr string) *skerrors.StackKitError {
	fields := []skerrors.ErrorOption{}
	if err != nil {
		fields = append(fields, skerrors.WithField("error", err.Error()))
	}
	if strings.TrimSpace(stderr) != "" {
		fields = append(fields, skerrors.WithField("stderr", strings.TrimSpace(stderr)))
	}
	return skerrors.NewDeploymentError(code, message, fields...)
}

func resultStderr(result *tofu.Result) string {
	if result == nil {
		return ""
	}
	return compactRuntimeActionStderr(result.Stderr)
}

func compactRuntimeActionStderr(stderr string) string {
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
