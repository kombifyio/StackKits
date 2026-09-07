package commands

import (
	"context"
	"errors"

	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/hostconformance"
	"github.com/kombifyio/stackkits/internal/localevidence"
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
