package runtimeexecutor

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Invoke validates a sealed request, revalidates the selected executor
// identity, calls it with a defensive request copy, rejects cancellation and
// panics fail-closed, verifies the exact result set, and returns a digest-bound
// result. It performs no retry, persistence, provider selection, or I/O itself.
func Invoke(ctx context.Context, executor Executor, request ExecutionRequest) (ExecutionResult, error) {
	return InvokeAt(ctx, executor, request, time.Now().UTC())
}

// InvokeAt is the deterministic clock-injected form of Invoke. Production
// callers use Invoke; tests and replay verification may supply an exact UTC
// instant. Freshness is checked immediately before the adapter call.
func InvokeAt(ctx context.Context, executor Executor, request ExecutionRequest, at time.Time) (ExecutionResult, error) {
	if ctx == nil {
		return ExecutionResult{}, wrapError(ErrorInvalidRequest, "context", "non-nil context required", nil)
	}
	if executor == nil {
		return ExecutionResult{}, wrapError(ErrorIdentityMismatch, "executor", "executor required", nil)
	}
	if err := request.Validate(); err != nil {
		return ExecutionResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return ExecutionResult{}, wrapError(ErrorCancelled, "context", "cancelled before execution", err)
	}
	identity, err := safeIdentity(executor)
	if err != nil {
		return ExecutionResult{}, err
	}
	if identity != request.Executor {
		return ExecutionResult{}, wrapError(ErrorIdentityMismatch, "executor", "identity differs from sealed request", nil)
	}
	if err := validateExternalBindingFreshness(request.AccessBindings, request.BackupTargetBindings, request.AuthorizationTime, at); err != nil {
		return ExecutionResult{}, err
	}
	outcome, err := safeExecute(ctx, executor, CloneExecutionRequest(request))
	if err != nil {
		return ExecutionResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return ExecutionResult{}, wrapError(ErrorCancelled, "context", "cancelled during execution", err)
	}
	if err := validateExactOutcome(request, outcome); err != nil {
		return ExecutionResult{}, err
	}
	result := ExecutionResult{
		APIVersion: APIVersion, Executor: request.Executor, PlanHash: request.PlanHash,
		ManifestHash: request.ManifestHash, GenerationReceiptHash: request.GenerationReceiptHash,
		RequirementsHash: request.RequirementsHash, EvidenceBundleHash: request.EvidenceBundleHash,
		ArtifactSetHash: request.ArtifactSetHash, RequestDigest: request.RequestDigest,
		Runtime: append([]RuntimeOutcome(nil), outcome.Runtime...),
		Health:  append([]HealthOutcome(nil), outcome.Health...),
	}
	digest, err := computeResultDigest(result)
	if err != nil {
		return ExecutionResult{}, err
	}
	result.ResultDigest = digest
	if err := result.Validate(); err != nil {
		return ExecutionResult{}, err
	}
	return CloneExecutionResult(result), nil
}

func validateExactOutcome(request ExecutionRequest, outcome ExecutionOutcome) error {
	if err := validateOutcomeShape(outcome.Runtime, outcome.Health); err != nil {
		return err
	}
	if len(outcome.Runtime) != len(request.RuntimeTargets) || len(outcome.Health) != len(request.HealthTargets) {
		return wrapError(ErrorSetMismatch, "outcome", "missing or extra runtime/health outcomes", nil)
	}
	for i, target := range request.RuntimeTargets {
		actual := outcome.Runtime[i]
		if actual.RequirementID != target.RequirementID || actual.InstanceRef != target.InstanceRef {
			return wrapError(ErrorSetMismatch, fmt.Sprintf("runtime[%d]", i), "outcome does not match exact governed target", nil)
		}
	}
	for i, target := range request.HealthTargets {
		actual := outcome.Health[i]
		if actual.RequirementID != target.RequirementID || actual.TargetRef != target.TargetRef {
			return wrapError(ErrorSetMismatch, fmt.Sprintf("health[%d]", i), "outcome does not match exact health target", nil)
		}
	}
	return nil
}

func safeIdentity(executor Executor) (identity ExecutorIdentity, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = wrapError(ErrorExecutorPanic, "executor.identity", "executor panicked", errors.New("executor panic"))
		}
	}()
	return executor.Identity(), nil
}

func safeExecute(ctx context.Context, executor Executor, request ExecutionRequest) (outcome ExecutionOutcome, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = wrapError(ErrorExecutorPanic, "executor.execute", "executor panicked", errors.New("executor panic"))
		}
	}()
	outcome, err = executor.Execute(ctx, request)
	if err != nil {
		return ExecutionOutcome{}, wrapError(ErrorExecutorFailed, "executor.execute", "executor returned an error", err)
	}
	return CloneExecutionOutcome(outcome), nil
}
