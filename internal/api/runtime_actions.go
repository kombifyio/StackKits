package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/auth"
	skerrors "github.com/kombifyio/stackkits/internal/errors"
	"github.com/kombifyio/stackkits/internal/tofu"
)

const (
	runtimeActionModeDryRun = "dry-run"
	runtimeActionModeApply  = "apply"

	runtimeActionRollout = "stackkit_rollout"
	runtimeActionVerify  = "verify_rollout"
	runtimeActionRestore = "restore_drill"
)

type runtimeActionRequest struct {
	Action      string `json:"action"`
	StackID     string `json:"stack_id"`
	StackName   string `json:"stack_name,omitempty"`
	StackKit    string `json:"stackkit,omitempty"`
	TofuDir     string `json:"tofu_dir,omitempty"`
	UnifiedPath string `json:"unified_path,omitempty"`
}

type runtimeActionResponse struct {
	Status      string               `json:"status"`
	Action      string               `json:"action"`
	StackID     string               `json:"stack_id"`
	StackName   string               `json:"stack_name,omitempty"`
	StackKit    string               `json:"stackkit,omitempty"`
	TofuDir     string               `json:"tofu_dir,omitempty"`
	UnifiedPath string               `json:"unified_path,omitempty"`
	Mode        string               `json:"mode"`
	Checks      []runtimeActionCheck `json:"checks,omitempty"`
}

type runtimeActionCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
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

func (s *Server) handleRuntimeAction(w http.ResponseWriter, r *http.Request, expectedAction string) {
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

	req.Action = normalizeRuntimeAction(req.Action)
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

func (s *Server) executeRuntimeAction(ctx context.Context, req runtimeActionRequest) (runtimeActionResponse, int, *skerrors.StackKitError) {
	mode := s.runtimeActionMode()
	resp := runtimeActionResponse{
		Status:      "accepted",
		Action:      req.Action,
		StackID:     req.StackID,
		StackName:   strings.TrimSpace(req.StackName),
		StackKit:    strings.TrimSpace(req.StackKit),
		TofuDir:     strings.TrimSpace(req.TofuDir),
		UnifiedPath: strings.TrimSpace(req.UnifiedPath),
		Mode:        mode,
	}
	resp.Checks = append(resp.Checks, runtimeActionCheck{Name: "request", Status: "ok", Detail: "runtime action payload decoded"})
	resp.Checks = appendPathCheck(resp.Checks, "tofu_dir", resp.TofuDir, true)
	resp.Checks = appendPathCheck(resp.Checks, "unified_path", resp.UnifiedPath, false)
	if resp.StackKit == "" {
		resp.Checks = append(resp.Checks, runtimeActionCheck{Name: "stackkit", Status: "warning", Detail: "stackkit name not provided"})
	} else {
		resp.Checks = append(resp.Checks, runtimeActionCheck{Name: "stackkit", Status: "ok", Detail: resp.StackKit})
	}

	if mode == runtimeActionModeDryRun {
		resp.Status = dryRunStatus(req.Action)
		return resp, http.StatusOK, nil
	}

	switch req.Action {
	case runtimeActionRollout:
		return runOpenTofuRollout(ctx, resp)
	case runtimeActionVerify:
		return runOpenTofuVerify(ctx, resp)
	case runtimeActionRestore:
		return runRestoreDrillVerifier(ctx, resp, s.config.RuntimeRestoreVerifierCommand)
	default:
		return resp, http.StatusBadRequest, skerrors.NewValidationError("invalid_runtime_action", "unsupported runtime action")
	}
}

func runOpenTofuRollout(ctx context.Context, resp runtimeActionResponse) (runtimeActionResponse, int, *skerrors.StackKitError) {
	if err := requireLocalTofuDir(resp.TofuDir); err != nil {
		return resp, http.StatusBadRequest, err
	}
	exec := tofu.NewExecutor(tofu.WithWorkDir(resp.TofuDir), tofu.WithAutoApprove(true), tofu.WithTimeout(30*time.Minute))
	if result, err := exec.Init(ctx); err != nil || !result.Success {
		return resp, http.StatusBadGateway, tofuActionError("opentofu_init_failed", "OpenTofu init failed", err, resultStderr(result))
	}
	if result, err := exec.Apply(ctx, ""); err != nil || !result.Success {
		return resp, http.StatusBadGateway, tofuActionError("opentofu_apply_failed", "OpenTofu apply failed", err, resultStderr(result))
	}
	resp.Status = "applied"
	resp.Checks = append(resp.Checks, runtimeActionCheck{Name: "opentofu_apply", Status: "ok"})
	return resp, http.StatusOK, nil
}

func runOpenTofuVerify(ctx context.Context, resp runtimeActionResponse) (runtimeActionResponse, int, *skerrors.StackKitError) {
	if err := requireLocalTofuDir(resp.TofuDir); err != nil {
		return resp, http.StatusBadRequest, err
	}
	exec := tofu.NewExecutor(tofu.WithWorkDir(resp.TofuDir), tofu.WithTimeout(5*time.Minute))
	if result, err := exec.State(ctx); err != nil || !result.Success {
		return resp, http.StatusBadGateway, tofuActionError("opentofu_state_failed", "OpenTofu state verification failed", err, resultStderr(result))
	}
	resp.Status = "verified"
	resp.Checks = append(resp.Checks, runtimeActionCheck{Name: "opentofu_state", Status: "ok"})
	return resp, http.StatusOK, nil
}

func runRestoreDrillVerifier(ctx context.Context, resp runtimeActionResponse, command string) (runtimeActionResponse, int, *skerrors.StackKitError) {
	command = strings.TrimSpace(command)
	if command == "" {
		resp.Status = "skipped"
		resp.Checks = append(resp.Checks, runtimeActionCheck{
			Name:   "restore_drill_adapter",
			Status: "skipped",
			Detail: "set STACKKITS_RESTORE_DRILL_COMMAND to run a restore verifier in apply mode",
		})
		return resp, http.StatusOK, nil
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
		"STACKKIT_RUNTIME_ACTION="+resp.Action,
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
		resp.Status = "failed"
		resp.Checks = append(resp.Checks, runtimeActionCheck{Name: "restore_drill_verifier", Status: "failed", Detail: detail})
		return resp, http.StatusBadGateway, tofuActionError("restore_drill_failed", "Restore drill verifier failed", err, detail)
	}

	resp.Status = "verified"
	resp.Checks = append(resp.Checks, runtimeActionCheck{Name: "restore_drill_verifier", Status: "ok", Detail: detail})
	return resp, http.StatusOK, nil
}

func (s *Server) runtimeActionMode() string {
	switch strings.ToLower(strings.TrimSpace(s.config.RuntimeActionMode)) {
	case runtimeActionModeApply:
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

func normalizeRuntimeAction(action string) string {
	action = strings.ToLower(strings.TrimSpace(action))
	action = strings.ReplaceAll(action, "-", "_")
	return action
}

func dryRunStatus(action string) string {
	switch action {
	case runtimeActionRollout:
		return "ready"
	case runtimeActionVerify:
		return "verified"
	case runtimeActionRestore:
		return "skipped"
	default:
		return "accepted"
	}
}

func appendPathCheck(checks []runtimeActionCheck, name, path string, wantDir bool) []runtimeActionCheck {
	path = strings.TrimSpace(path)
	if path == "" {
		return append(checks, runtimeActionCheck{Name: name, Status: "missing"})
	}
	info, err := os.Stat(path)
	if err != nil {
		return append(checks, runtimeActionCheck{Name: name, Status: "reference", Detail: path})
	}
	if wantDir && !info.IsDir() {
		return append(checks, runtimeActionCheck{Name: name, Status: "warning", Detail: "path is not a directory"})
	}
	if !wantDir && info.IsDir() {
		return append(checks, runtimeActionCheck{Name: name, Status: "warning", Detail: "path is a directory"})
	}
	return append(checks, runtimeActionCheck{Name: name, Status: "ok", Detail: path})
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
	return result.Stderr
}
