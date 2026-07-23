package runtimeexecutordispatch

import (
	"github.com/kombifyio/stackkits/internal/runtimeapplyv2"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
)

// ReconcileRequiredError is the provider-free structured partial-failure
// result. It exposes only the exact validated operation and durable snapshot;
// adapter payloads, logs, endpoints, credentials, and provider handles are not
// part of either shared contract.
type ReconcileRequiredError struct {
	operation runtimeapply.Operation
	snapshot  runtimeapply.Snapshot
	cause     error
}

func newReconcileRequiredError(operation runtimeapply.Operation, snapshot runtimeapply.Snapshot, cause error) error {
	return &ReconcileRequiredError{
		operation: cloneRuntimeApplyOperation(operation),
		snapshot:  cloneRuntimeApplySnapshot(snapshot),
		cause:     cause,
	}
}

func (e *ReconcileRequiredError) Error() string {
	return "runtime Apply operation requires reconciliation"
}

func (e *ReconcileRequiredError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

// Operation returns a defensive copy of the exact registered child authority.
func (e *ReconcileRequiredError) Operation() runtimeapply.Operation {
	if e == nil {
		return runtimeapply.Operation{}
	}
	return cloneRuntimeApplyOperation(e.operation)
}

// Snapshot returns a defensive copy of the validated reconcile-required state.
func (e *ReconcileRequiredError) Snapshot() runtimeapply.Snapshot {
	if e == nil {
		return runtimeapply.Snapshot{}
	}
	return cloneRuntimeApplySnapshot(e.snapshot)
}

func cloneRuntimeApplyOperation(input runtimeapply.Operation) runtimeapply.Operation {
	result := input
	result.Steps = append([]runtimeapply.Step(nil), input.Steps...)
	for index := range result.Steps {
		result.Steps[index].Runtime = append([]runtimeapply.RuntimeExpectation(nil), input.Steps[index].Runtime...)
		result.Steps[index].Health = append([]runtimeapply.HealthExpectation(nil), input.Steps[index].Health...)
	}
	return result
}

func cloneRuntimeApplySnapshot(input runtimeapply.Snapshot) runtimeapply.Snapshot {
	result := input
	result.Steps = append([]runtimeapply.StepSnapshot(nil), input.Steps...)
	for index := range result.Steps {
		if input.Steps[index].Result == nil {
			continue
		}
		cloned := runtimeexecutor.CloneExecutionResult(*input.Steps[index].Result)
		result.Steps[index].Result = &cloned
	}
	return result
}

var _ error = (*ReconcileRequiredError)(nil)
var _ interface{ Unwrap() error } = (*ReconcileRequiredError)(nil)
