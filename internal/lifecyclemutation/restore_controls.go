package lifecyclemutation

import (
	"errors"
	"reflect"

	"github.com/kombifyio/stackkits/internal/backuplifecycle"
)

// RecordRestoreControls writes per-effect progress into the current signed
// activation phase, with the same lock, CAS and journal custody as copy steps.
func (s *Session) RecordRestoreControls(controls []backuplifecycle.RuntimeControl) error {
	if s == nil || s.record.RestoreActivation == nil {
		return errors.New("restore control journal is unavailable")
	}
	next := *s.record.RestoreActivation
	next.Controls = append([]backuplifecycle.RuntimeControl(nil), controls...)
	return s.transition(s.record.Phase, s.record.Phase, nil, &next)
}

func validateRestoreControlsForPhase(phase string, controls []backuplifecycle.RuntimeControl) error {
	if err := backuplifecycle.ValidateRuntimeControls(controls); err != nil {
		return err
	}
	for _, c := range controls {
		if !c.WasRunning {
			continue
		}
		pending := false
		for _, e := range []backuplifecycle.RuntimeControlEffect{c.Stop, c.Start, c.RollbackStop, c.RollbackStart} {
			if e.State != "" && e.State != backuplifecycle.ControlObserved {
				pending = true
			}
		}
		switch phase {
		case PhasePrepared, PhaseQuiesceStarted, PhaseRuntimeStartStarted, PhaseRollbackStarted, PhaseRollbackRuntimeStarted:
			// These are the durable pre-dispatch/settlement boundaries.
			continue
		case PhaseQuiesced, PhaseRollbackCopyStarted, PhaseRollbackCopySucceeded, PhaseRollbackReady, PhaseActivationCopyStarted, PhaseActivationCopySucceeded, PhaseActivationSucceeded:
			if !pending && c.Stop.State == backuplifecycle.ControlObserved && c.Start.State == "" {
				continue
			}
		case PhaseRuntimeStartSucceeded, PhaseVerifyStarted, PhaseVerifySucceeded, PhaseCommitStarted, PhaseCommitSucceeded:
			if !pending && c.Start.State == backuplifecycle.ControlObserved {
				continue
			}
		case PhaseRollbackVolumeStarted, PhaseRollbackVolumeSucceeded:
			if !pending && (c.RollbackStop.State == backuplifecycle.ControlObserved || c.Stop.State == backuplifecycle.ControlObserved && c.Start.State == "") {
				continue
			}
		case PhaseRollbackRuntimeSucceeded, PhaseRollbackActivationVerifyStarted, PhaseRollbackActivationVerifyDone, PhaseRollbackSucceeded:
			if !pending && (c.RollbackStop.State == "" && c.Start.State == backuplifecycle.ControlObserved || c.RollbackStart.State == backuplifecycle.ControlObserved) {
				continue
			}
		}
		return errors.New("restore activation phase has unresolved writer control obligations")
	}
	return nil
}
func validRestoreControlTransition(r Record, expected, next string, state *RestoreActivationState) bool {
	if r.Kind != KindRestoreActivation || expected != next || state == nil || r.RestoreActivation == nil {
		return false
	}
	old := *r.RestoreActivation
	candidate := *state
	before, after := old.Controls, candidate.Controls
	old.Controls = nil
	candidate.Controls = nil
	if !reflect.DeepEqual(old, candidate) || backuplifecycle.ValidateRuntimeControls(after) != nil {
		return false
	}
	if len(before) == 0 {
		return expected == PhaseQuiesceStarted && len(after) > 0 && initialControls(after)
	}
	if len(before) != len(after) {
		return false
	}
	for i, a := range before {
		b := after[i]
		if a.ContainerID != b.ContainerID || a.ComponentRef != b.ComponentRef || a.WasRunning != b.WasRunning {
			return false
		}
		for j, x := range []backuplifecycle.RuntimeControlEffect{a.Stop, a.Start, a.RollbackStop, a.RollbackStart} {
			y := []backuplifecycle.RuntimeControlEffect{b.Stop, b.Start, b.RollbackStop, b.RollbackStart}[j]
			if x == y {
				continue
			}
			rank := map[string]int{"": 0, backuplifecycle.ControlPreparing: 1, backuplifecycle.ControlPrepared: 2, backuplifecycle.ControlDispatched: 3, backuplifecycle.ControlObserved: 4}
			if rank[y.State] != rank[x.State]+1 || x.Handle != "" && x.Handle != y.Handle {
				return false
			}
			if x.State == backuplifecycle.ControlDispatched && y.State == backuplifecycle.ControlObserved {
				continue
			}
			if j == 0 && expected != PhaseQuiesceStarted || j == 1 && expected != PhaseRuntimeStartStarted && expected != PhaseRollbackRuntimeStarted || j == 2 && expected != PhaseRollbackStarted && expected != PhaseRollbackVolumeStarted && expected != PhaseRollbackVolumeSucceeded || j == 3 && expected != PhaseRollbackRuntimeStarted {
				return false
			}
		}
	}
	return true
}
func initialControls(c []backuplifecycle.RuntimeControl) bool {
	for _, x := range c {
		if x.Stop.State != "" || x.Start.State != "" || x.RollbackStop.State != "" || x.RollbackStart.State != "" {
			return false
		}
	}
	return true
}
