package api

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	skerrors "github.com/kombifyio/stackkits/internal/errors"
	stackaction "github.com/kombifyio/stackkits/internal/stackaction"
	"github.com/kombifyio/stackkits/internal/telemetry"
)

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
