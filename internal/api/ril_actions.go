package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/architecturev2"
	skerrors "github.com/kombifyio/stackkits/internal/errors"
	"github.com/kombifyio/stackkits/internal/rilactionv2"
)

const (
	rilActionPathPrefix      = "/api/v2/internal/ril-actions/"
	rilActionResolvePath     = rilActionPathPrefix + "resolve"
	rilActionExecutePath     = rilActionPathPrefix + "execute"
	rilActionTenantHeader    = "X-Kombify-Tenant-ID"
	maxRILCurrentResolutions = 4096
)

var rilActionTenantPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

func (s *Server) handleRILActionResolve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	tenantID, ok := trustedRILActionTenant(w, r)
	if !ok {
		return
	}
	if err := requireArchitectureV2JSONMediaType(r); err != nil {
		writeArchitectureV2RequestError(w, err)
		return
	}
	envelope, err := decodeArchitectureV2ResolveEnvelope(w, r)
	if err != nil {
		writeArchitectureV2RequestError(w, err)
		return
	}
	if !s.tryAcquireArchitectureV2Resolve() {
		w.Header().Set("Retry-After", architectureV2ResolveRetryAfterSeconds)
		writeStructuredError(w, r, http.StatusTooManyRequests, skerrors.NewValidationError(
			"architecture_v2_resolve_busy", "Architecture v2 resolver concurrency limit reached",
		))
		return
	}
	defer s.releaseArchitectureV2Resolve()

	service, err := s.architectureV2ResolveService()
	if err != nil {
		writeMappedArchitectureV2ResolveError(w, err)
		return
	}
	current, err := service.ResolveCurrentScoped(architecturev2.ResolveInput{
		StackSpec: envelope.StackSpec,
		Inventory: envelope.Inventory,
	}, tenantID)
	if err != nil {
		writeMappedArchitectureV2ResolveError(w, err)
		return
	}
	result, err := current.Result()
	if err != nil {
		writeMappedArchitectureV2ResolveError(w, err)
		return
	}
	stackID, _ := result.Plan["stackId"].(string)
	if stackID == "" {
		writeStructuredError(w, r, http.StatusInternalServerError, skerrors.NewValidationError(
			"ril_action_resolution_identity_missing", "governed resolution returned no stack identity",
		))
		return
	}
	if err := s.storeRILCurrentResolution(tenantID, stackID, current); err != nil {
		w.Header().Set("Retry-After", "1")
		writeStructuredError(w, r, http.StatusServiceUnavailable, skerrors.NewValidationError(
			"ril_action_resolution_capacity_reached", err.Error(),
		))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("ETag", `"`+result.PlanHash+`"`)
	w.Header().Set("X-StackKit-Plan-Hash", result.PlanHash)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(result.CanonicalPlan)
}

func (s *Server) handleRILActionExecute(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	tenantID, ok := trustedRILActionTenant(w, r)
	if !ok {
		return
	}
	if err := requireArchitectureV2JSONMediaType(r); err != nil {
		writeStructuredError(w, r, http.StatusUnsupportedMediaType, skerrors.NewValidationError(
			"unsupported_ril_action_media_type", "RIL action execution requires Content-Type application/json",
		))
		return
	}
	body, err := readBoundedRILActionBody(w, r)
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		status := http.StatusBadRequest
		code := "invalid_ril_action_payload"
		if errors.As(err, &maxBytesErr) {
			status = http.StatusRequestEntityTooLarge
			code = "ril_action_payload_too_large"
		}
		writeStructuredError(w, r, status, skerrors.NewValidationError(code, "RIL action payload is invalid"))
		return
	}
	evaluatedAt := time.Now().UTC()
	request, err := rilaction.DecodeRequestAt(body, evaluatedAt)
	if err != nil {
		writeStructuredError(w, r, http.StatusBadRequest, skerrors.NewValidationError(
			"invalid_ril_action_request", "RIL action request is invalid",
			skerrors.WithField("reason", err.Error()),
		))
		return
	}
	if request.TenantID != tenantID {
		writeStructuredError(w, r, http.StatusForbidden, skerrors.NewAuthError(
			"ril_action_tenant_mismatch", "RIL action tenant does not match the authenticated transport context",
		))
		return
	}
	current, exists := s.loadRILCurrentResolution(tenantID, request.StackID)
	if !exists {
		writeStructuredError(w, r, http.StatusConflict, skerrors.NewValidationError(
			"ril_action_current_resolution_required",
			"resolve the exact StackSpec and Inventory through the tenant-bound RIL resolve endpoint before execution",
		))
		return
	}
	service, err := s.architectureV2ResolveService()
	if err != nil {
		writeStructuredError(w, r, http.StatusServiceUnavailable, skerrors.NewValidationError(
			"architecture_v2_authority_unavailable", "Architecture v2 authority is unavailable",
		))
		return
	}
	evidence, executionErr := service.ExecuteRILAction(r.Context(), architecturev2.RILActionAdmissionInput{
		Current: current, TrustedTenantID: tenantID, Envelope: body, EvaluatedAt: evaluatedAt,
	})
	if evidence.APIVersion != "" {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-StackKits-RIL-Execution-Status", evidence.Status)
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(evidence)
		return
	}
	writeRILActionExecutionError(w, r, executionErr)
}

func trustedRILActionTenant(w http.ResponseWriter, r *http.Request) (string, bool) {
	tenantID := r.Header.Get(rilActionTenantHeader)
	if tenantID == "" || tenantID != strings.TrimSpace(tenantID) || !rilActionTenantPattern.MatchString(tenantID) || strings.Contains(tenantID, "://") {
		writeStructuredError(w, r, http.StatusBadRequest, skerrors.NewValidationError(
			"ril_action_tenant_required", fmt.Sprintf("%s must contain the exact trusted tenant identity", rilActionTenantHeader),
		))
		return "", false
	}
	return tenantID, true
}

func readBoundedRILActionBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	if r.ContentLength > rilaction.MaxRequestBytes {
		return nil, &http.MaxBytesError{Limit: rilaction.MaxRequestBytes}
	}
	reader := http.MaxBytesReader(w, r.Body, int64(rilaction.MaxRequestBytes))
	body, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, io.ErrUnexpectedEOF
	}
	return body, nil
}

func (s *Server) storeRILCurrentResolution(tenantID, stackID string, current architecturev2.CurrentResolution) error {
	key := tenantID + "\x00" + stackID
	s.architectureV2.rilMu.Lock()
	defer s.architectureV2.rilMu.Unlock()
	if _, exists := s.architectureV2.rilCurrent[key]; !exists && len(s.architectureV2.rilCurrent) >= maxRILCurrentResolutions {
		return fmt.Errorf("tenant-bound RIL resolution registry is full")
	}
	s.architectureV2.rilCurrent[key] = current
	return nil
}

func (s *Server) loadRILCurrentResolution(tenantID, stackID string) (architecturev2.CurrentResolution, bool) {
	key := tenantID + "\x00" + stackID
	s.architectureV2.rilMu.RLock()
	defer s.architectureV2.rilMu.RUnlock()
	current, exists := s.architectureV2.rilCurrent[key]
	return current, exists
}

func writeRILActionExecutionError(w http.ResponseWriter, r *http.Request, err error) {
	status := http.StatusUnprocessableEntity
	code := "ril_action_execution_rejected"
	message := "RIL action execution was rejected"
	var resolveErr *architecturev2.ResolveError
	if errors.As(err, &resolveErr) {
		switch resolveErr.Code {
		case architecturev2.ErrRILActionBusy, architecturev2.ErrRILActionReplay:
			status = http.StatusConflict
		case architecturev2.ErrRILActionUnavailable:
			status = http.StatusNotImplemented
		case architecturev2.ErrAuthorityLoad:
			status = http.StatusServiceUnavailable
		}
	}
	writeStructuredError(w, r, status, skerrors.NewValidationError(code, message))
}
