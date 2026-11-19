package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/auth"
	skerrors "github.com/kombifyio/stackkits/internal/errors"
	stackaction "github.com/kombifyio/stackkits/internal/stackaction"
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
	for path, action := range map[string]stackaction.Action{
		stackaction.PathServiceStart:   stackaction.ActionServiceStart,
		stackaction.PathServiceStop:    stackaction.ActionServiceStop,
		stackaction.PathServiceRestart: stackaction.ActionServiceRestart,
		stackaction.PathServiceLogs:    stackaction.ActionServiceLogs,
	} {
		path, action := path, action
		s.mux.Handle("POST "+path,
			s.requireStackActionServiceAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				s.handleStackAction(w, r, action)
			})))
	}
}

func (s *Server) requireStackActionServiceAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secrets := []string{s.config.ServiceAuthSecret, s.config.ServiceAuthSecretNext}
		if !hasAnySecretStackAction(secrets) && len(s.config.ServiceAuthCallerKeys) == 0 {
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
				skerrors.WithSuggestion("Call this endpoint with a homelab service-auth token"),
			))
			return
		}

		if _, err := auth.VerifyServiceToken(token, auth.VerifyOptions{
			Target:           "stackkits",
			Secrets:          secrets,
			AllowedCallers:   []string{"homelab"},
			CallerPublicKeys: s.config.ServiceAuthCallerKeys,
			RequireEd25519:   s.config.ServiceAuthRequireEd25519,
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
	if stackaction.IsBackupAction(req.Action) && strings.TrimSpace(req.Mode) != "" && req.Mode != string(mode) {
		resp.Status = stackaction.StatusFailed
		return resp, http.StatusBadRequest, skerrors.NewValidationError(
			"backup_execution_mode_mismatch", "backup request mode must match the node execution mode")
	}
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
		if stackaction.IsBackupAction(req.Action) {
			resp.Checks = append(resp.Checks, stackActionCheck{Name: "backup_execution", Status: stackaction.CheckStatusSkipped, Detail: "dry-run: backup was not executed or verified"})
		}
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
			"Homelab-managed rollout and verify actions require runtime_target",
		)
	}

	return s.dispatchStackAction(ctx, resp, includeStackKitOutputs, req)
}

func (s *Server) dispatchStackAction(ctx context.Context, resp stackActionResponse, includeStackKitOutputs bool, req stackActionRequest) (stackActionResponse, int, *skerrors.StackKitError) {
	switch req.Action {
	case stackActionRollout:
		return runOpenTofuRolloutStackAction(ctx, resp, includeStackKitOutputs, req.RuntimeTarget, req.PlatformNodes, s.stackActionReferenceResolver, s.config.BaseDir)
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
	case stackaction.ActionServiceStart, stackaction.ActionServiceStop, stackaction.ActionServiceRestart, stackaction.ActionServiceLogs:
		return s.runServiceStackAction(ctx, resp, req)
	default:
		return resp, http.StatusBadRequest, skerrors.NewValidationError("invalid_stack_action", "unsupported StackAction")
	}
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
	case stackaction.ActionBackupRun, stackaction.ActionBackupStatus:
		return stackaction.StatusSkipped
	case stackActionRestore, stackaction.ActionBackupRestore, stackaction.ActionBackupWipe:
		return stackaction.StatusSkipped
	default:
		return stackaction.StatusAccepted
	}
}
