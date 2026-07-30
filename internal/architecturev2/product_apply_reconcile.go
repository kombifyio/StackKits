package architecturev2

import (
	"sort"
	"strings"

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
	const message = "product Apply requires reconciliation"
	if e == nil {
		return message
	}
	closed := make([]string, 0)
	for _, operation := range e.operations {
		for _, snapshot := range operation.Snapshot.Steps {
			if snapshot.State != runtimeapply.StepFailed || snapshot.StepID == "" || snapshot.FailureCode == "" {
				continue
			}
			closed = append(closed, productApplyClosedStepDiagnostic(operation.Operation, snapshot))
		}
	}
	if len(closed) == 0 {
		return message
	}
	sort.Strings(closed)
	if len(closed) > 4 {
		closed = closed[:4]
	}
	return message + " (" + strings.Join(closed, ",") + ")"
}

func productApplyClosedStepDiagnostic(operation runtimeapply.Operation, snapshot runtimeapply.StepSnapshot) string {
	label := "step"
	for _, step := range operation.Steps {
		if step.ID != snapshot.StepID {
			continue
		}
		parts := make([]string, 0, 1+len(step.Runtime)+len(step.Health))
		if step.Executor.ID != "" {
			parts = append(parts, "executor:"+step.Executor.ID)
		}
		for _, runtime := range step.Runtime {
			if runtime.InstanceRef != "" {
				parts = append(parts, "runtime:"+runtime.InstanceRef)
			}
		}
		for _, health := range step.Health {
			if health.TargetRef != "" {
				parts = append(parts, "health:"+health.TargetRef)
			}
		}
		if len(parts) != 0 {
			label = strings.Join(parts, "/")
		}
		break
	}
	return label + "=" + string(snapshot.FailureCode)
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
