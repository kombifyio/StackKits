package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/kombifyio/stackkits/internal/architecturev2"
	skerrors "github.com/kombifyio/stackkits/internal/errors"
	"github.com/kombifyio/stackkits/internal/runtimeaction"
	sharedruntimeaction "github.com/kombifyio/stackkits/internal/runtimeactionv2"
)

const runtimeActionBodyLimit int64 = 1 << 20

type runtimeActionEnvelopeError struct {
	code    string
	message string
}

func (e *runtimeActionEnvelopeError) Error() string {
	if e == nil {
		return "invalid runtime action envelope"
	}
	return e.message
}

func readRuntimeActionBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, runtimeActionBodyLimit))
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, errors.New("runtime action payload is empty or exceeds the size limit")
	}
	return body, nil
}

func classifyRuntimeActionBodyError(err error) (int, string, string) {
	var maxBytesErr *http.MaxBytesError
	if errors.As(err, &maxBytesErr) {
		return http.StatusRequestEntityTooLarge, "runtime_action_payload_too_large", "runtime action payload exceeds the 1 MiB limit"
	}
	return http.StatusBadRequest, "invalid_runtime_action_payload", "runtime action payload must be valid JSON"
}

// handleArchitectureV2RuntimeAction is registered only on /api/v2. The legacy
// /api/v1 decoder therefore never observes, buffers, or dispatches a V2 body.
func (s *Server) handleArchitectureV2RuntimeAction(w http.ResponseWriter, r *http.Request, expectedAction runtimeaction.Action) {
	body, readErr := readRuntimeActionBody(w, r)
	if readErr != nil {
		status, code, message := classifyRuntimeActionBodyError(readErr)
		writeStructuredError(w, r, status, skerrors.NewValidationError(code, message))
		return
	}
	s.admitArchitectureV2RuntimeAction(w, r, body, expectedAction)
}

func (s *Server) admitArchitectureV2RuntimeAction(w http.ResponseWriter, r *http.Request, body []byte, expectedAction runtimeaction.Action) {
	w.Header().Set("Cache-Control", "no-store")
	if err := requireArchitectureV2JSONMediaType(r); err != nil {
		writeStructuredError(w, r, http.StatusUnsupportedMediaType, skerrors.NewValidationError(
			"unsupported_runtime_action_media_type",
			"Architecture v2 runtime actions require Content-Type application/json",
		))
		return
	}
	request, err := sharedruntimeaction.DecodeArchitectureV2Request(body)
	if err != nil {
		writeStructuredError(w, r, http.StatusBadRequest, skerrors.NewValidationError(
			"invalid_architecture_v2_runtime_action",
			"Architecture v2 runtime action envelope is invalid",
			skerrors.WithField("reason", err.Error()),
		))
		return
	}

	wantOperation, ok := architectureV2OperationForEndpoint(expectedAction)
	if !ok || request.Action != wantOperation {
		writeStructuredError(w, r, http.StatusBadRequest, skerrors.NewValidationError(
			"invalid_runtime_action",
			"runtime action does not match endpoint",
		))
		return
	}

	if !s.tryAcquireArchitectureV2Resolve() {
		w.Header().Set("Retry-After", architectureV2ResolveRetryAfterSeconds)
		writeStructuredError(w, r, http.StatusTooManyRequests, skerrors.NewValidationError(
			"architecture_v2_resolve_busy",
			"Architecture v2 resolver concurrency limit reached",
		))
		return
	}
	defer s.releaseArchitectureV2Resolve()

	service, err := s.architectureV2ResolveService()
	if err != nil {
		writeStructuredError(w, r, http.StatusServiceUnavailable, skerrors.NewValidationError(
			"architecture_v2_authority_unavailable",
			"Architecture v2 authority is unavailable",
		))
		return
	}
	result, err := service.Resolve(architecturev2.ResolveInput{
		StackSpec: request.StackSpec,
		Inventory: request.Inventory,
	})
	if err != nil {
		writeArchitectureV2RuntimeActionResolveError(w, r, err)
		return
	}
	resolvedStackID, _ := result.Plan["stackId"].(string)
	if resolvedStackID == "" || request.StackID != resolvedStackID {
		writeStructuredError(w, r, http.StatusConflict, skerrors.NewValidationError(
			"architecture_v2_stack_identity_mismatch",
			"runtime action stack_id does not match the resolved plan",
		))
		return
	}
	if request.ExpectedPlanHash != result.PlanHash {
		writeStructuredError(w, r, http.StatusConflict, skerrors.NewValidationError(
			"architecture_v2_plan_hash_mismatch",
			"expected_plan_hash does not match the current governed resolution",
		))
		return
	}

	writeStructuredError(w, r, http.StatusNotImplemented, skerrors.NewValidationError(
		"architecture_v2_runtime_action_not_implemented",
		"Architecture v2 runtime execution is admitted but its governed renderer and executor are not implemented",
	))
}

func architectureV2OperationForEndpoint(action runtimeaction.Action) (sharedruntimeaction.ArchitectureV2Operation, bool) {
	switch action {
	case runtimeActionRollout:
		return sharedruntimeaction.ArchitectureV2OperationRollout, true
	case runtimeActionVerify:
		return sharedruntimeaction.ArchitectureV2OperationVerify, true
	default:
		return "", false
	}
}

func validateLegacyRuntimeActionEnvelope(apiVersion json.RawMessage, stackSpec json.RawMessage) error {
	if len(stackSpec) > 0 {
		return invalidRuntimeActionEnvelope("runtime_action_version_conflict", "runtime-action v1 does not admit stack_spec")
	}
	if len(apiVersion) == 0 {
		return nil
	}
	var version string
	if err := json.Unmarshal(apiVersion, &version); err != nil || version != string(sharedruntimeaction.RuntimeActionAPIVersionV1) {
		return invalidRuntimeActionEnvelope("unsupported_runtime_action_api_version", "runtime-action v1 does not admit this api_version")
	}
	return nil
}

func invalidRuntimeActionEnvelope(code, message string) error {
	return &runtimeActionEnvelopeError{code: code, message: message}
}

func writeRuntimeActionEnvelopeError(w http.ResponseWriter, r *http.Request, err error) {
	var envelopeErr *runtimeActionEnvelopeError
	if !errors.As(err, &envelopeErr) {
		envelopeErr = &runtimeActionEnvelopeError{code: "invalid_runtime_action_payload", message: "runtime action payload is invalid"}
	}
	writeStructuredError(w, r, http.StatusBadRequest, skerrors.NewValidationError(envelopeErr.code, envelopeErr.message))
}

func writeArchitectureV2RuntimeActionResolveError(w http.ResponseWriter, r *http.Request, err error) {
	status := http.StatusInternalServerError
	code := "architecture_v2_runtime_action_resolution_failed"
	message := "Architecture v2 runtime action resolution failed"
	var resolveErr *architecturev2.ResolveError
	if errors.As(err, &resolveErr) {
		switch resolveErr.Code {
		case architecturev2.ErrInvalidStackSpec, architecturev2.ErrInvalidInventory:
			status = http.StatusBadRequest
			code = "architecture_v2_runtime_action_invalid"
			message = "StackSpec or Inventory is invalid"
		case architecturev2.ErrMigrationRequired, architecturev2.ErrMigrationBlocked, architecturev2.ErrResolveFailed:
			status = http.StatusUnprocessableEntity
			code = "architecture_v2_runtime_action_unresolvable"
			message = "StackSpec and Inventory cannot be resolved by the governed Architecture v2 authority"
		case architecturev2.ErrAuthorityLoad:
			status = http.StatusServiceUnavailable
			code = "architecture_v2_authority_unavailable"
			message = "Architecture v2 authority is unavailable"
		}
	}
	writeStructuredError(w, r, status, skerrors.NewValidationError(code, message))
}
