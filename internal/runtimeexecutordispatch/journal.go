package runtimeexecutordispatch

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"time"

	"github.com/kombifyio/stackkits/internal/runtimeapplyv2"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
)

type preparedExecution struct {
	label        string
	executor     runtimeexecutor.Executor
	request      runtimeexecutor.ExecutionRequest
	compensation runtimeapply.CompensationMode
}

func normalizeCompensationMode(mode runtimeapply.CompensationMode) (runtimeapply.CompensationMode, error) {
	if mode == "" {
		return runtimeapply.CompensationNone, nil
	}
	if mode != runtimeapply.CompensationNone && mode != runtimeapply.CompensationExplicit {
		return "", errors.New("runtime route has unsupported compensation mode")
	}
	return mode, nil
}

func nilRuntimeApplyJournal(journal runtimeapply.Journal) bool {
	if journal == nil {
		return true
	}
	value := reflect.ValueOf(journal)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func executePrepared(
	ctx context.Context,
	parent runtimeexecutor.ExecutionRequest,
	prepared []preparedExecution,
	journal runtimeapply.Journal,
) (runtimeexecutor.ExecutionOutcome, error) {
	if len(prepared) == 0 {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("runtime dispatch requires at least one prepared child")
	}
	if nilRuntimeApplyJournal(journal) {
		return executePreparedDirect(ctx, prepared)
	}

	steps := make([]runtimeapply.Step, 0, len(prepared))
	for _, child := range prepared {
		step, err := runtimeapply.NewStep(child.request, child.compensation)
		if err != nil {
			return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("build journal step for %q: %w", child.label, err)
		}
		steps = append(steps, step)
	}
	operation, err := runtimeapply.NewOperation(parent, steps)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("build runtime Apply operation: %w", err)
	}
	stepByDigest := make(map[string]runtimeapply.Step, len(operation.Steps))
	for _, step := range operation.Steps {
		stepByDigest[step.RequestDigest] = step
	}

	reservation, err := safeJournalBegin(ctx, journal, operation)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("reserve runtime Apply operation: %w", err)
	}
	if err := runtimeapply.ValidateReservation(operation, reservation); err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("validate runtime Apply reservation: %w", err)
	}
	if reservation.Disposition == runtimeapply.DispositionInProgress {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("runtime Apply operation is already in progress")
	}
	if reservation.Disposition == runtimeapply.DispositionConflict {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("runtime Apply operation conflicts with durable journal authority")
	}
	if reservation.Disposition == runtimeapply.DispositionReplay {
		if reservation.Snapshot.State != runtimeapply.OperationCompleted {
			return runtimeexecutor.ExecutionOutcome{}, errors.New("compensated runtime Apply operation cannot be replayed as successful Apply")
		}
		return replayPrepared(prepared, operation, *reservation.Snapshot)
	}

	states := make(map[string]runtimeapply.StepSnapshot, len(operation.Steps))
	operationState := runtimeapply.OperationRunning
	if reservation.Disposition == runtimeapply.DispositionAcquired {
		for _, step := range operation.Steps {
			states[step.RequestDigest] = runtimeapply.StepSnapshot{
				StepID: step.ID, RequestDigest: step.RequestDigest, State: runtimeapply.StepPending,
			}
		}
	} else {
		operationState = reservation.Snapshot.State
		for _, state := range reservation.Snapshot.Steps {
			if state.State == runtimeapply.StepRunning {
				return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf(
					"runtime Apply step %q remains running; durable abandoned-operation reconciliation is required",
					state.StepID,
				)
			}
			states[state.RequestDigest] = state
		}
	}

	outcome := runtimeexecutor.ExecutionOutcome{}
	for _, child := range prepared {
		step, exists := stepByDigest[child.request.RequestDigest]
		if !exists {
			return runtimeexecutor.ExecutionOutcome{}, errors.New("prepared child is absent from runtime Apply operation")
		}
		state := states[step.RequestDigest]
		if state.State == runtimeapply.StepSucceeded {
			if state.Result == nil || runtimeapply.VerifyStepResult(step, *state.Result) != nil {
				return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("runtime Apply step %q has invalid replay evidence", child.label)
			}
			appendResult(&outcome, *state.Result)
			continue
		}
		if state.State != runtimeapply.StepPending && state.State != runtimeapply.StepFailed {
			return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("runtime Apply step %q has non-executable state %q", child.label, state.State)
		}

		running := runtimeapply.StepCommit{
			OperationID: operation.OperationID, FenceToken: reservation.FenceToken,
			StepID: step.ID, RequestDigest: step.RequestDigest,
			ExpectedState: state.State, State: runtimeapply.StepRunning,
		}
		if err := runtimeapply.ValidateStepCommit(operation, running); err != nil {
			return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("validate running journal transition for %q: %w", child.label, err)
		}
		snapshot, err := safeJournalCommit(ctx, journal, running)
		if err != nil {
			return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("commit running journal transition for %q: %w", child.label, err)
		}
		if err := validateCommittedSnapshot(operation, snapshot, states, running); err != nil {
			return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("validate running journal snapshot for %q: %w", child.label, err)
		}
		operationState = snapshot.State
		states = indexStepSnapshots(snapshot)

		result, invokeErr := invokePrepared(ctx, child)
		if invokeErr != nil {
			failed := runtimeapply.StepCommit{
				OperationID: operation.OperationID, FenceToken: reservation.FenceToken,
				StepID: step.ID, RequestDigest: step.RequestDigest,
				ExpectedState: runtimeapply.StepRunning, State: runtimeapply.StepFailed,
				FailureCode: classifyFailure(invokeErr),
			}
			if validationErr := runtimeapply.ValidateStepCommit(operation, failed); validationErr != nil {
				return runtimeexecutor.ExecutionOutcome{}, errors.Join(invokeErr, fmt.Errorf("validate failed journal transition: %w", validationErr))
			}
			failureSnapshot, commitErr := safeJournalCommit(ctx, journal, failed)
			if commitErr != nil {
				return runtimeexecutor.ExecutionOutcome{}, errors.Join(invokeErr, fmt.Errorf("commit failed journal transition: %w", commitErr))
			}
			if validationErr := validateCommittedSnapshot(operation, failureSnapshot, states, failed); validationErr != nil {
				return runtimeexecutor.ExecutionOutcome{}, errors.Join(invokeErr, fmt.Errorf("validate failed journal snapshot: %w", validationErr))
			}
			return runtimeexecutor.ExecutionOutcome{}, newReconcileRequiredError(operation, failureSnapshot, invokeErr)
		}

		succeeded := runtimeapply.StepCommit{
			OperationID: operation.OperationID, FenceToken: reservation.FenceToken,
			StepID: step.ID, RequestDigest: step.RequestDigest,
			ExpectedState: runtimeapply.StepRunning, State: runtimeapply.StepSucceeded,
			Result: &result,
		}
		if err := runtimeapply.ValidateStepCommit(operation, succeeded); err != nil {
			return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("validate succeeded journal transition for %q: %w", child.label, err)
		}
		snapshot, err = safeJournalCommit(ctx, journal, succeeded)
		if err != nil {
			return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("commit succeeded journal transition for %q: %w", child.label, err)
		}
		if err := validateCommittedSnapshot(operation, snapshot, states, succeeded); err != nil {
			return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("validate succeeded journal snapshot for %q: %w", child.label, err)
		}
		operationState = snapshot.State
		states = indexStepSnapshots(snapshot)
		appendResult(&outcome, result)
	}

	finalization := runtimeapply.Finalization{
		OperationID: operation.OperationID, FenceToken: reservation.FenceToken,
		ExpectedState: operationState, State: runtimeapply.OperationCompleted,
	}
	if err := runtimeapply.ValidateFinalization(operation, finalization); err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("validate runtime Apply finalization: %w", err)
	}
	finalSnapshot, err := safeJournalFinalize(ctx, journal, finalization)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("finalize runtime Apply operation: %w", err)
	}
	if err := runtimeapply.ValidateSnapshot(operation, finalSnapshot); err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("validate final runtime Apply snapshot: %w", err)
	}
	if finalSnapshot.State != runtimeapply.OperationCompleted {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("runtime Apply journal did not finalize the operation as completed")
	}
	if !snapshotStepsMatch(states, finalSnapshot) {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("runtime Apply finalization changed immutable step evidence")
	}
	sortExecutionOutcome(&outcome)
	return outcome, nil
}

func executePreparedDirect(ctx context.Context, prepared []preparedExecution) (runtimeexecutor.ExecutionOutcome, error) {
	outcome := runtimeexecutor.ExecutionOutcome{}
	for _, child := range prepared {
		result, err := invokePrepared(ctx, child)
		if err != nil {
			return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("execute runtime owner for %q: %w", child.label, err)
		}
		appendResult(&outcome, result)
	}
	sortExecutionOutcome(&outcome)
	return outcome, nil
}

func replayPrepared(
	prepared []preparedExecution,
	operation runtimeapply.Operation,
	snapshot runtimeapply.Snapshot,
) (runtimeexecutor.ExecutionOutcome, error) {
	steps := make(map[string]runtimeapply.Step, len(operation.Steps))
	states := indexStepSnapshots(snapshot)
	for _, step := range operation.Steps {
		steps[step.RequestDigest] = step
	}
	outcome := runtimeexecutor.ExecutionOutcome{}
	for _, child := range prepared {
		step, stepExists := steps[child.request.RequestDigest]
		state, stateExists := states[child.request.RequestDigest]
		if !stepExists || !stateExists || state.State != runtimeapply.StepSucceeded || state.Result == nil {
			return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("runtime Apply replay for %q is incomplete", child.label)
		}
		if err := runtimeapply.VerifyStepResult(step, *state.Result); err != nil {
			return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("verify runtime Apply replay for %q: %w", child.label, err)
		}
		appendResult(&outcome, *state.Result)
	}
	sortExecutionOutcome(&outcome)
	return outcome, nil
}

func invokePrepared(ctx context.Context, child preparedExecution) (runtimeexecutor.ExecutionResult, error) {
	if len(child.request.AccessBindings) == 0 {
		return runtimeexecutor.Invoke(ctx, child.executor, child.request)
	}
	authorizationTime, err := time.Parse(time.RFC3339Nano, child.request.AuthorizationTime)
	if err != nil {
		return runtimeexecutor.ExecutionResult{}, fmt.Errorf("parse runtime authorization time: %w", err)
	}
	return runtimeexecutor.InvokeAt(ctx, child.executor, child.request, authorizationTime)
}

func appendResult(outcome *runtimeexecutor.ExecutionOutcome, result runtimeexecutor.ExecutionResult) {
	outcome.Runtime = append(outcome.Runtime, result.Runtime...)
	outcome.Health = append(outcome.Health, result.Health...)
}

func sortExecutionOutcome(outcome *runtimeexecutor.ExecutionOutcome) {
	sort.Slice(outcome.Runtime, func(i, j int) bool {
		left := outcome.Runtime[i].RequirementID + "\x00" + outcome.Runtime[i].InstanceRef
		right := outcome.Runtime[j].RequirementID + "\x00" + outcome.Runtime[j].InstanceRef
		return left < right
	})
	sort.Slice(outcome.Health, func(i, j int) bool {
		return outcome.Health[i].RequirementID < outcome.Health[j].RequirementID
	})
}

func indexStepSnapshots(snapshot runtimeapply.Snapshot) map[string]runtimeapply.StepSnapshot {
	states := make(map[string]runtimeapply.StepSnapshot, len(snapshot.Steps))
	for _, state := range snapshot.Steps {
		states[state.RequestDigest] = state
	}
	return states
}

func validateCommittedSnapshot(
	operation runtimeapply.Operation,
	snapshot runtimeapply.Snapshot,
	previous map[string]runtimeapply.StepSnapshot,
	commit runtimeapply.StepCommit,
) error {
	if err := runtimeapply.ValidateSnapshot(operation, snapshot); err != nil {
		return err
	}
	wantOperationState := runtimeapply.OperationRunning
	if commit.State == runtimeapply.StepFailed {
		wantOperationState = runtimeapply.OperationReconcileRequired
	}
	if snapshot.State != wantOperationState {
		return errors.New("runtime Apply journal snapshot has an unexpected operation state after step commit")
	}
	for _, state := range snapshot.Steps {
		if state.StepID != commit.StepID {
			before, exists := previous[state.RequestDigest]
			if !exists || !reflect.DeepEqual(state, before) {
				return errors.New("runtime Apply journal step commit changed unrelated step evidence")
			}
			continue
		}
		if state.RequestDigest != commit.RequestDigest || state.State != commit.State ||
			state.FailureCode != commit.FailureCode ||
			state.CompensationReceiptDigest != commit.CompensationReceiptDigest ||
			!reflect.DeepEqual(state.Result, commit.Result) {
			return errors.New("runtime Apply journal snapshot does not reflect the exact step commit")
		}
		return nil
	}
	return errors.New("runtime Apply journal snapshot omits the committed step")
}

func snapshotStepsMatch(expected map[string]runtimeapply.StepSnapshot, snapshot runtimeapply.Snapshot) bool {
	if len(expected) != len(snapshot.Steps) {
		return false
	}
	for _, state := range snapshot.Steps {
		if !reflect.DeepEqual(expected[state.RequestDigest], state) {
			return false
		}
	}
	return true
}

func classifyFailure(err error) runtimeapply.FailureCode {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return runtimeapply.FailureCancelled
	}
	var executionErr *runtimeexecutor.Error
	if errors.As(err, &executionErr) {
		switch executionErr.Code {
		case runtimeexecutor.ErrorCancelled:
			return runtimeapply.FailureCancelled
		case runtimeexecutor.ErrorExecutorFailed, runtimeexecutor.ErrorExecutorPanic:
			return runtimeapply.FailureExecutorFailed
		default:
			return runtimeapply.FailureVerificationFailed
		}
	}
	return runtimeapply.FailureExecutorFailed
}

func safeJournalBegin(ctx context.Context, journal runtimeapply.Journal, operation runtimeapply.Operation) (reservation runtimeapply.Reservation, err error) {
	defer func() {
		if recover() != nil {
			err = errors.New("runtime Apply journal Begin panicked")
		}
	}()
	return journal.Begin(ctx, operation)
}

func safeJournalCommit(ctx context.Context, journal runtimeapply.Journal, commit runtimeapply.StepCommit) (snapshot runtimeapply.Snapshot, err error) {
	defer func() {
		if recover() != nil {
			err = errors.New("runtime Apply journal CommitStep panicked")
		}
	}()
	return journal.CommitStep(ctx, commit)
}

func safeJournalFinalize(ctx context.Context, journal runtimeapply.Journal, finalization runtimeapply.Finalization) (snapshot runtimeapply.Snapshot, err error) {
	defer func() {
		if recover() != nil {
			err = errors.New("runtime Apply journal Finalize panicked")
		}
	}()
	return journal.Finalize(ctx, finalization)
}
