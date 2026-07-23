package architecturev2

import (
	"sort"

	"github.com/kombifyio/stackkits/internal/runtimeapplyv2"
	"github.com/kombifyio/stackkits/internal/runtimeexecutordispatch"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
)

// ProductApplyReconcileOperation is one validated provider-free operation and
// its exact durable partial-failure state. The operation identifies every
// child runtime/Health authority; the snapshot contains only closed failure
// codes and verified results, never adapter payloads or provider handles.
type ProductApplyReconcileOperation struct {
	Operation runtimeapply.Operation
	Snapshot  runtimeapply.Snapshot
}

// ProductApplyReconcileRequiredError is the structured Product Apply outcome
// when at least one journaled channel or owner needs reconciliation.
type ProductApplyReconcileRequiredError struct {
	operations    []ProductApplyReconcileOperation
	requestDigest string
	cause         error
}

// RequestDigest returns the opaque, canonical recovery key for
// ReconcileProductApply. It contains no request bytes or provider authority.
func (e *ProductApplyReconcileRequiredError) RequestDigest() string {
	if e == nil {
		return ""
	}
	return e.requestDigest
}

func (e *ProductApplyReconcileRequiredError) Error() string {
	return "product Apply requires reconciliation"
}

func (e *ProductApplyReconcileRequiredError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

// Operations returns defensive copies sorted by operation identity.
func (e *ProductApplyReconcileRequiredError) Operations() []ProductApplyReconcileOperation {
	if e == nil {
		return nil
	}
	result := make([]ProductApplyReconcileOperation, len(e.operations))
	for index, operation := range e.operations {
		result[index] = cloneProductApplyReconcileOperation(operation)
	}
	return result
}

func newProductApplyReconcileRequiredError(err error, requestDigest ...string) *ProductApplyReconcileRequiredError {
	if err == nil {
		return nil
	}
	byID := map[string]ProductApplyReconcileOperation{}
	var visit func(error)
	visit = func(candidate error) {
		if candidate == nil {
			return
		}
		if reconcile, ok := candidate.(*runtimeexecutordispatch.ReconcileRequiredError); ok {
			operation := reconcile.Operation()
			snapshot := reconcile.Snapshot()
			if operation.OperationID != "" && snapshot.OperationID == operation.OperationID {
				byID[operation.OperationID] = ProductApplyReconcileOperation{Operation: operation, Snapshot: snapshot}
			}
		}
		switch unwrapped := candidate.(type) {
		case interface{ Unwrap() []error }:
			for _, child := range unwrapped.Unwrap() {
				visit(child)
			}
		case interface{ Unwrap() error }:
			visit(unwrapped.Unwrap())
		}
	}
	visit(err)
	if len(byID) == 0 {
		return nil
	}
	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	operations := make([]ProductApplyReconcileOperation, 0, len(ids))
	for _, id := range ids {
		operations = append(operations, cloneProductApplyReconcileOperation(byID[id]))
	}
	digest := ""
	if len(requestDigest) != 0 && validProductApplyDigest(requestDigest[0]) {
		digest = requestDigest[0]
	}
	return &ProductApplyReconcileRequiredError{operations: operations, requestDigest: digest, cause: err}
}

func cloneProductApplyReconcileOperation(input ProductApplyReconcileOperation) ProductApplyReconcileOperation {
	operation := input.Operation
	operation.Steps = append([]runtimeapply.Step(nil), input.Operation.Steps...)
	for index := range operation.Steps {
		operation.Steps[index].Runtime = append([]runtimeapply.RuntimeExpectation(nil), input.Operation.Steps[index].Runtime...)
		operation.Steps[index].Health = append([]runtimeapply.HealthExpectation(nil), input.Operation.Steps[index].Health...)
	}
	snapshot := input.Snapshot
	snapshot.Steps = append([]runtimeapply.StepSnapshot(nil), input.Snapshot.Steps...)
	for index := range snapshot.Steps {
		if input.Snapshot.Steps[index].Result == nil {
			continue
		}
		result := runtimeexecutor.CloneExecutionResult(*input.Snapshot.Steps[index].Result)
		snapshot.Steps[index].Result = &result
	}
	return ProductApplyReconcileOperation{Operation: operation, Snapshot: snapshot}
}

var _ error = (*ProductApplyReconcileRequiredError)(nil)
