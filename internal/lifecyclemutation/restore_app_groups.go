package lifecyclemutation

import (
	"errors"
	"github.com/kombifyio/stackkits/internal/backuplifecycle"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"reflect"
)

// RecordRestoreAppGroups uses the same signed Session CAS and phase authority
// as existing restore copies. Empty fields preserve original v1 wire bytes.
func (s *Session) RecordRestoreAppGroups(groups []backuplifecycle.AppGroupControl) error {
	if s == nil || s.record.RestoreActivation == nil {
		return errors.New("restore app-group journal unavailable")
	}
	for _, g := range groups {
		for _, c := range g.Original.Containers {
			for _, legacy := range s.record.RestoreActivation.Controls {
				if legacy.ContainerID == c.ID {
					return errors.New("restore group overlaps v1 container power control")
				}
			}
		}
	}
	next := *s.record.RestoreActivation
	next.AppGroups = backuplifecycle.CloneAppGroups(groups)
	return s.transition(s.record.Phase, s.record.Phase, nil, &next)
}
func validRestoreAppGroupTransition(r Record, expected, next string, state *RestoreActivationState) bool {
	if r.Kind != KindRestoreActivation || expected != next || state == nil || r.RestoreActivation == nil {
		return false
	}
	old, candidate := *r.RestoreActivation, *state
	before, after := old.AppGroups, candidate.AppGroups
	old.AppGroups = nil
	candidate.AppGroups = nil
	if !reflect.DeepEqual(old, candidate) || backuplifecycle.ValidateAppGroups(after) != nil {
		return false
	}
	if len(before) == 0 {
		if expected != PhaseQuiesceStarted || len(after) == 0 {
			return false
		}
		for _, g := range after {
			if len(g.Effects) != 0 || g.Closed || g.Binding.JournalOperationID != r.OperationID || g.Binding.OwnerRef != r.OwnerRef {
				return false
			}
		}
		return true
	}
	if !backuplifecycle.ValidAppGroupTransition(before, after) {
		return false
	}
	for i, g := range after {
		prior := before[i]
		if len(g.Effects) > len(prior.Effects) {
			a := g.Effects[len(g.Effects)-1].Action
			if a == "stop" && expected != PhaseQuiesceStarted || a == "start" && expected != PhaseRuntimeStartStarted || a == "rollback-stop" && expected != PhaseRollbackStarted || a == "rollback-start" && expected != PhaseRollbackRuntimeStarted {
				return false
			}
		}
		if g.Closed && !prior.Closed && expected != PhaseCommitSucceeded && expected != PhaseRollbackSucceeded {
			return false
		}
	}
	return true
}
func validateRestoreAppGroupsForPhase(phase string, groups []backuplifecycle.AppGroupControl) error {
	if err := backuplifecycle.ValidateAppGroups(groups); err != nil {
		return err
	}
	for _, g := range groups {
		switch phase {
		case PhasePrepared, PhaseQuiesceStarted, PhaseRuntimeStartStarted, PhaseRollbackStarted, PhaseRollbackRuntimeStarted:
			continue
		case PhaseQuiesced, PhaseRollbackCopyStarted, PhaseRollbackCopySucceeded, PhaseRollbackReady, PhaseActivationCopyStarted, PhaseActivationCopySucceeded, PhaseActivationSucceeded, PhaseRollbackVolumeStarted, PhaseRollbackVolumeSucceeded:
			if backuplifecycle.AppGroupStopped(g) {
				continue
			}
		case PhaseRuntimeStartSucceeded, PhaseVerifyStarted, PhaseVerifySucceeded, PhaseCommitStarted, PhaseCommitSucceeded, PhaseRollbackRuntimeSucceeded, PhaseRollbackActivationVerifyStarted, PhaseRollbackActivationVerifyDone, PhaseRollbackSucceeded:
			if backuplifecycle.AppGroupRestored(g) {
				continue
			}
		}
		return errors.New("restore phase has unresolved app-group control/adoption obligations")
	}
	return nil
}

// VerifyRestoreAppGroup checks the actual signed Session CAS/readback. A native
// adapter cannot grant itself journal authority by repeating a binding digest.
func (s *Session) VerifyRestoreAppGroup(g backuplifecycle.AppGroupControl, completed bool) error {
	if s == nil || s.transaction == nil {
		return errors.New("native group requires owning restore session")
	}
	actual, digest, exists, err := loadRecord(s.workspace, s.transaction)
	if err != nil {
		return err
	}
	if !exists || digest != s.recordDigest || actual.OperationID != g.Binding.JournalOperationID || actual.OwnerRef != g.Binding.OwnerRef || actual.RestoreActivation == nil || completed && (actual.Status != StatusSucceeded && actual.Status != StatusRecovered) {
		return errors.New("native group restore journal authority changed")
	}
	for _, held := range actual.RestoreActivation.AppGroups {
		if reflect.DeepEqual(held, g) {
			return nil
		}
	}
	return errors.New("native group differs from authenticated restore session")
}

// ReadRestoreAppGroupRecord reads existing immutable signed history without
// acquiring a mutation Session, repairing a pointer or writing recovery state.
// It is intended for real-product journey assertions and diagnostic readback.
func ReadRestoreAppGroupRecord(workspace, operation string) (Record, error) {
	record, err := ReadRestoreActivationRecord(workspace, operation)
	if err != nil {
		return record, err
	}
	if len(record.RestoreActivation.AppGroups) == 0 {
		return Record{}, errors.New("restore history lacks signed app-group controls")
	}
	return record, nil
}

// ReadRestoreActivationRecord also reads ordinary activation histories without
// manufacturing app-group membership for unrelated workload verification.
func ReadRestoreActivationRecord(workspace, operation string) (Record, error) {
	if !operationPattern.MatchString(operation) {
		return Record{}, errors.New("invalid restore operation")
	}
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return Record{}, err
	}
	defer root.Close()
	tx, err := root.BeginTransaction()
	if err != nil {
		return Record{}, err
	}
	defer tx.Close()
	record, _, err := latestHistoryRecord(workspace, tx, operation)
	if err != nil {
		return Record{}, err
	}
	if record.Kind != KindRestoreActivation || record.RestoreActivation == nil {
		return Record{}, errors.New("restore history lacks signed app-group controls")
	}
	return record, nil
}
