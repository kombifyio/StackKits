package restoreactivation

import (
	"context"
	"errors"
	"github.com/kombifyio/stackkits/internal/backupexec"
	"github.com/kombifyio/stackkits/internal/backuplifecycle"
	"github.com/kombifyio/stackkits/internal/lifecyclemutation"
	"strings"
)

func BindApplicationControls(base Runtime, observe func(context.Context, string) (backupexec.QuiesceContainer, error), driver func(string, string, string) (backuplifecycle.RuntimeControlDriver, error)) error {
	r, ok := base.(*applicationRuntime)
	if !ok || observe == nil {
		return errors.New("application restore requires retained component observation")
	}
	r.observe = observe
	r.driver = driver
	return nil
}
func (r *applicationRuntime) BindControlJournal(s *lifecyclemutation.Session) error {
	if s == nil || s.Record().OperationID != r.authority.OperationID {
		return errors.New("application control journal changed")
	}
	r.journal = s
	return nil
}
func (r *applicationRuntime) captureControls(ctx context.Context) error {
	if r.journal == nil || r.observe == nil {
		return errors.New("application restore requires initial power custody")
	}
	record := r.journal.Record()
	if len(record.RestoreActivation.Controls) > 0 {
		return nil
	}
	if record.Phase != lifecyclemutation.PhaseQuiesceStarted {
		return errors.New("application recovery has no authenticated initial power state")
	}
	var controls []backuplifecycle.RuntimeControl
	for _, ref := range r.order {
		c, err := r.observeComponent(ctx, ref)
		if err != nil {
			return err
		}
		if c.Paused || c.Restarting {
			return errors.New("application component power is unresolved")
		}
		controls = append(controls, backuplifecycle.RuntimeControl{ComponentRef: ref, ContainerID: c.ID, WasRunning: c.Running})
	}
	return r.journal.RecordRestoreControls(controls)
}
func (r *applicationRuntime) advanceControl(ctx context.Context, ref string, start bool) error {
	if r.journal == nil || r.observe == nil || r.driver == nil {
		return errors.New("application restore control binding is unavailable")
	}
	record := r.journal.Record()
	controls := record.RestoreActivation.Controls
	for i, c := range controls {
		if c.ComponentRef != ref {
			continue
		}
		action := "stop"
		if strings.HasPrefix(record.Phase, "rollback-") {
			action = "rollback-stop"
		}
		if start {
			action = "start"
			if c.RollbackStop.State != "" {
				action = "rollback-start"
			}
		}
		driver, err := r.driver(r.authority.OperationID, ref, c.ContainerID)
		if err != nil {
			return err
		}
		_, err = backuplifecycle.AdvanceRuntimeControl(ctx, c, action, func(next backuplifecycle.RuntimeControl) error {
			candidate := append([]backuplifecycle.RuntimeControl(nil), controls...)
			candidate[i] = next
			return r.journal.RecordRestoreControls(candidate)
		}, func(ctx context.Context) (string, bool, error) {
			current, err := r.observeComponent(ctx, ref)
			return current.ID, current.Running, err
		}, driver)
		return err
	}
	return errors.New("application component is outside initial power custody")
}

func (r *applicationRuntime) settleControls(ctx context.Context) error {
	if r.journal == nil || r.observe == nil || r.driver == nil {
		return errors.New("application control settlement is unavailable")
	}
	controls := r.journal.Record().RestoreActivation.Controls
	for i, c := range controls {
		driver, err := r.driver(r.authority.OperationID, c.ComponentRef, c.ContainerID)
		if err != nil {
			return err
		}
		_, err = backuplifecycle.SettleRuntimeControl(ctx, c, func(next backuplifecycle.RuntimeControl) error {
			candidate := append([]backuplifecycle.RuntimeControl(nil), r.journal.Record().RestoreActivation.Controls...)
			candidate[i] = next
			return r.journal.RecordRestoreControls(candidate)
		}, func(ctx context.Context) (string, bool, error) {
			current, err := r.observeComponent(ctx, c.ComponentRef)
			return current.ID, current.Running, err
		}, driver)
		if err != nil {
			return err
		}
	}
	return nil
}
