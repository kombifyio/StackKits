package commands

import (
	"context"
	"errors"
	"strings"

	"github.com/kombifyio/stackkits/internal/applyoutcome"
	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/hostconformance"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
)

// Only construction-owned diagnostics cross this boundary. Arbitrary wrapped
// error text can contain credentials and must never be printed here.
func localApplyDiagnostic(err error) string {
	var resolution *architecturev2.ResolveError
	if !errors.As(err, &resolution) || resolution.Code != architecturev2.ErrApplyAuthorization {
		return ""
	}
	var binding *generationartifact.Error
	if errors.As(resolution.Cause, &binding) && binding != nil && binding.Code == generationartifact.ErrBindingMismatch {
		// Paths are matched exactly; neither arbitrary paths nor nested error
		// messages may cross the CLI diagnostic boundary.
		switch binding.Path {
		case "apply.reconcile.executor":
			return "Recovery rejected: executor identity differs from the saved authority"
		case "apply.reconcile.plan":
			return "Recovery rejected: current plan or requirements differ from the saved authority"
		case "apply.reconcile.requirementsHash":
			return "Recovery rejected: requirements hash differs from the saved authority"
		case "apply.reconcile.outputRoot":
			return "Recovery rejected: output root differs from the saved authority"
		}
	}
	var probe *hostconformance.ProbeError
	if errors.As(resolution.Cause, &probe) {
		return probe.Diagnostic()
	}
	var observation *localevidence.DiagnosticError
	if errors.As(resolution.Cause, &observation) {
		return observation.Diagnostic()
	}
	if errors.Is(resolution.Cause, context.DeadlineExceeded) {
		return "Apply evidence context timed out"
	}
	if errors.Is(resolution.Cause, context.Canceled) {
		return "Apply evidence context canceled"
	}
	return ""
}

// diagnoseExecutorFailure inspects wrappers before unwrapping: shared executor
// errors hide their cause in Error(), while process wrappers carry useful
// context that disappears at their exit-status leaf. Only closed codes escape.
type executorFailureDiagnostic struct {
	Code          string
	BoundaryField string
}

func diagnoseExecutorFailure(err error) executorFailureDiagnostic {
	result := executorFailureDiagnostic{}
	cause := ""
	pending := []error{err}
	stage, boundary := "", ""
	for visited := 0; len(pending) > 0 && visited < 64; visited++ {
		current := pending[0]
		pending = pending[1:]
		if current == nil {
			continue
		}
		message := current.Error()
		if len(message) > 16384 {
			message = message[:16384]
		}
		if classified := applyoutcome.Classify(message); classified.Class != applyoutcome.ClassUnknown {
			if cause == "" {
				cause = string(classified.Class)
			}
		}
		for _, entry := range []struct{ marker, code string }{
			{"required Docker runtime is not installed", "docker_missing"},
			{"Docker daemon data root differs from the CUE-declared container data root", "docker_data_root_mismatch"},
			{"Docker runtime returned an invalid observation", "docker_observation_invalid"},
			{"Docker runtime returned an invalid version", "docker_observation_invalid"},
			{"Docker runtime is not ready:", "docker_not_ready"},
			{"Basement core artifact digest does not match immutable content", "basement_artifact_digest_mismatch"},
			{"artifact is not the exact CUE-owned Basement core Compose instance", "basement_artifact_contract_mismatch"},
			{"artifact is not the exact CUE-owned Basement core lite Compose instance", "basement_artifact_contract_mismatch"},
			{"runtime target is not the exact locally bound Basement core Compose contract", "basement_runtime_contract_mismatch"},
			{"runtime target is not the exact locally bound Basement core lite Compose contract", "basement_runtime_contract_mismatch"},
		} {
			if strings.Contains(message, entry.marker) {
				if cause == "" {
					cause = entry.code
				}
			}
		}
		if stage == "" {
			for _, entry := range []struct{ marker, code string }{
				{"verify local Basement runtime custody before Apply:", "basement_custody_failed"},
				{"local PocketID owner realization did not complete:", "basement_owner_realization_failed"},
				{"local TinyAuth PocketID binding did not complete:", "basement_oidc_reconcile_failed"},
				{"local Docker Compose Apply did not complete:", "basement_compose_up_failed"},
			} {
				if strings.Contains(message, entry.marker) {
					stage = entry.code
					break
				}
			}
		}
		if typed, ok := current.(*runtimeexecutor.Error); ok {
			switch typed.Code {
			case runtimeexecutor.ErrorInvalidRequest, runtimeexecutor.ErrorInvalidResult,
				runtimeexecutor.ErrorIdentityMismatch, runtimeexecutor.ErrorSetMismatch,
				runtimeexecutor.ErrorCancelled, runtimeexecutor.ErrorExecutorFailed, runtimeexecutor.ErrorExecutorPanic:
				boundary = "runtime_" + string(typed.Code)
				switch typed.Field {
				case "context", "executor", "executor.identity", "executor.execute", "executor.digest",
					"outcome", "api_version", "plan_hash", "manifest_hash", "generation_receipt_hash",
					"requirements_hash", "evidence_bundle_hash", "artifact_set_hash", "request_digest",
					"runtime_targets", "authorization_time", "request", "result", "result_digest",
					"access_binding", "access_bindings", "backup_target_binding", "backup_target_bindings",
					"artifacts", "runtime", "health":
					result.BoundaryField = typed.Field
				}

			}
		}
		if joined, ok := current.(interface{ Unwrap() []error }); ok {
			children := joined.Unwrap()
			remaining := max(0, 63-visited-len(pending))
			if len(children) > remaining {
				children = children[:remaining]
			}
			pending = append(pending, children...)
		} else if child := errors.Unwrap(current); child != nil {
			pending = append(pending, child)
		}
	}
	switch {
	case cause != "":
		result.Code = cause
	case stage != "":
		result.Code = stage
	case boundary != "":
		result.Code = boundary
	default:
		result.Code = "unknown_failure"
	}
	return result
}
