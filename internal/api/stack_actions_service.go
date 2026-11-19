package api

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strings"

	skerrors "github.com/kombifyio/stackkits/internal/errors"
	"github.com/kombifyio/stackkits/internal/servicecontrol"
	stackaction "github.com/kombifyio/stackkits/internal/stackaction"
)

func (s *Server) runServiceStackAction(ctx context.Context, resp stackActionResponse, req stackActionRequest) (stackActionResponse, int, *skerrors.StackKitError) {
	if req.Service == nil || strings.TrimSpace(req.Service.ServiceKey) == "" {
		return resp, http.StatusBadRequest, skerrors.NewValidationError("service_request_required", "service action requires a service_key")
	}
	workspace := strings.TrimSpace(s.config.BaseDir)
	authority, err := s.architectureV2ResolveService()
	if err != nil {
		return resp, http.StatusServiceUnavailable, skerrors.NewInfrastructureError(
			"service_runtime_unavailable", "StackKits service authority is unavailable",
			skerrors.WithCause(err),
		)
	}
	planPath := filepath.Join(workspace, "deploy", ".stackkit", "resolved-plan.json")
	plan, err := authority.ReadCanonicalPlan(planPath)
	if err != nil {
		return resp, http.StatusServiceUnavailable, skerrors.NewInfrastructureError(
			"service_runtime_unavailable", "StackKits service plan is unavailable",
			skerrors.WithCause(err),
		)
	}
	controller, err := servicecontrol.NewOSController(workspace, planPath, plan)
	if err != nil {
		return resp, http.StatusServiceUnavailable, skerrors.NewInfrastructureError(
			"service_runtime_unavailable", "StackKits service runtime is unavailable",
			skerrors.WithCause(err),
		)
	}
	serviceResult := &stackaction.ServiceActionResult{ServiceKey: strings.TrimSpace(req.Service.ServiceKey)}
	resp.Service = serviceResult

	if req.Action == stackaction.ActionServiceLogs {
		tail := req.Service.Tail
		if tail == 0 {
			tail = 100
		}
		logs, logsErr := controller.Logs(ctx, serviceResult.ServiceKey, tail, strings.TrimSpace(req.Service.Cursor))
		if logsErr != nil {
			return serviceStackActionError(resp, logsErr)
		}
		serviceResult.LogEntries = make([]stackaction.ServiceLogEntry, 0, len(logs.Entries))
		for _, entry := range logs.Entries {
			serviceResult.LogEntries = append(serviceResult.LogEntries, stackaction.ServiceLogEntry{Timestamp: entry.Timestamp, Message: entry.Message})
		}
		serviceResult.NextCursor = logs.NextCursor
		serviceResult.Truncated = logs.Truncated
		serviceResult.EvidenceRef = logs.EvidenceRef
		resp.Status = stackaction.StatusVerified
		return resp, http.StatusOK, nil
	}

	action := map[stackaction.Action]string{
		stackaction.ActionServiceStart:   servicecontrol.ActionStart,
		stackaction.ActionServiceStop:    servicecontrol.ActionStop,
		stackaction.ActionServiceRestart: servicecontrol.ActionRestart,
	}[req.Action]
	result, mutateErr := controller.Mutate(ctx, action, serviceResult.ServiceKey, req.Service.OwnerApproved)
	if mutateErr != nil {
		return serviceStackActionError(resp, mutateErr)
	}
	serviceResult.DesiredState = result.DesiredState
	serviceResult.ObservedState = result.ObservedState
	serviceResult.Revision = int64(result.Revision)
	serviceResult.ReasonCode = result.ReasonCode
	serviceResult.Retryable = result.Retryable
	serviceResult.EvidenceRef = result.EvidenceRef
	serviceResult.Checks = make([]stackaction.Check, 0, len(result.Checks))
	for _, check := range result.Checks {
		serviceResult.Checks = append(serviceResult.Checks, stackaction.Check{Name: check.Name, Status: stackaction.CheckStatus(check.Status), Detail: check.Detail})
	}
	resp.Status = stackaction.StatusApplied
	return resp, http.StatusOK, nil
}

func serviceStackActionError(resp stackActionResponse, err error) (stackActionResponse, int, *skerrors.StackKitError) {
	status := http.StatusBadRequest
	reason, retryable := "service_action_failed", false
	var serviceErr *servicecontrol.Error
	if errors.As(err, &serviceErr) {
		reason, retryable = serviceErr.ReasonCode, serviceErr.Retryable
		switch serviceErr.ReasonCode {
		case servicecontrol.ReasonCriticalControlPlane:
			status = http.StatusConflict
		case servicecontrol.ReasonRuntimeUnavailable:
			status = http.StatusServiceUnavailable
		}
	}
	if resp.Service == nil {
		resp.Service = &stackaction.ServiceActionResult{}
	}
	resp.Service.ReasonCode = reason
	resp.Service.Retryable = retryable
	resp.Status = stackaction.StatusFailed
	return resp, status, skerrors.NewDeploymentError(reason, "StackKits service action failed", skerrors.WithCause(err))
}
