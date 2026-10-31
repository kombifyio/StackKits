package runtimeapply

import (
	"errors"

	"github.com/kombifyio/stackkits/contracts/runtimeexecutor"
)

func validateResumableReservation(operation Operation, reservation Reservation) error {
	if !tokenPattern.MatchString(reservation.FenceToken) || reservation.Snapshot == nil {
		return errors.New("resume runtime Apply reservation requires a fence token and snapshot")
	}
	if err := ValidateSnapshot(operation, *reservation.Snapshot); err != nil {
		return err
	}
	if reservation.Snapshot.State != OperationRunning && reservation.Snapshot.State != OperationReconcileRequired {
		return errors.New("resume runtime Apply reservation has no resumable state")
	}
	return nil
}

func validateReplayReservation(operation Operation, reservation Reservation) error {
	if reservation.FenceToken != "" || reservation.Snapshot == nil {
		return errors.New("replay runtime Apply reservation requires only a final snapshot")
	}
	if err := ValidateSnapshot(operation, *reservation.Snapshot); err != nil {
		return err
	}
	if reservation.Snapshot.State != OperationCompleted && reservation.Snapshot.State != OperationCompensated {
		return errors.New("replay runtime Apply reservation is not final")
	}
	return nil
}

// snapshotStepCounts summarizes only steps whose exact identity and terminal
// evidence have already been validated, before the operation state is checked.
type snapshotStepCounts struct {
	failed, pending, running, succeeded, compensated int
}

func validateSnapshotSteps(operation Operation, snapshot Snapshot) (snapshotStepCounts, error) {
	counts := snapshotStepCounts{}
	for index, state := range snapshot.Steps {
		step := operation.Steps[index]
		if state.StepID != step.ID || state.RequestDigest != step.RequestDigest {
			return counts, errors.New("runtime Apply snapshot step is reordered or substituted")
		}
		if err := validateStepState(step, state.State, state.Result, state.FailureCode, state.CompensationReceiptDigest); err != nil {
			return counts, err
		}
		switch state.State {
		case StepPending:
			counts.pending++
		case StepRunning:
			counts.running++
		case StepSucceeded:
			counts.succeeded++
		case StepFailed:
			counts.failed++
		case StepCompensated:
			counts.compensated++
		}
	}
	return counts, nil
}

func (counts snapshotStepCounts) validate(state OperationState, steps int) error {
	switch state {
	case OperationRunning:
		return counts.validateRunning(steps)
	case OperationReconcileRequired:
		if counts.failed == 0 || counts.running != 0 {
			return errors.New("reconcile-required runtime Apply snapshot requires a failed step and no running step")
		}
	case OperationCompleted:
		if counts.succeeded != steps {
			return errors.New("completed runtime Apply snapshot requires every step to succeed")
		}
	case OperationCompensated:
		return counts.validateCompensated(steps)
	default:
		return errors.New("runtime Apply snapshot has unsupported operation state")
	}
	return nil
}

func (counts snapshotStepCounts) validateRunning(steps int) error {
	// Independent steps may continue after a failure. Settled failed work must
	// be reconcile-required, but all-succeeded work may await finalization.
	if counts.compensated != 0 || counts.pending+counts.running+counts.succeeded+counts.failed != steps {
		return errors.New("running runtime Apply snapshot has impossible step states")
	}
	if counts.failed != 0 && counts.pending+counts.running == 0 {
		return errors.New("running runtime Apply snapshot has settled failed work and must be reconcile-required")
	}
	return nil
}

func (counts snapshotStepCounts) validateCompensated(steps int) error {
	if counts.compensated == 0 || counts.pending != 0 || counts.running != 0 || counts.succeeded != 0 || counts.compensated+counts.failed != steps {
		return errors.New("compensated runtime Apply snapshot has uncompensated successful work")
	}
	return nil
}

func validateStepResultState(step Step, state StepState, result *runtimeexecutor.ExecutionResult, failure FailureCode, compensationDigest string) error {
	if state == StepSucceeded {
		if result == nil || failure != "" || compensationDigest != "" {
			return errors.New("succeeded runtime Apply step requires only an exact result")
		}
	} else if step.Compensation != CompensationExplicit || result == nil || failure != "" || !digestPattern.MatchString(compensationDigest) {
		return errors.New("compensated runtime Apply step requires explicit capability, original result, and receipt digest")
	}
	return VerifyStepResult(step, *result)
}
