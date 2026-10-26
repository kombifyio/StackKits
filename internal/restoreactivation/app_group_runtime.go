package restoreactivation

import (
	"context"
	"errors"
	"github.com/kombifyio/stackkits/internal/backuplifecycle"
	"github.com/kombifyio/stackkits/internal/lifecyclemutation"
	"reflect"
	"strings"
)

// WithAppGroupControls preserves the existing copy/staging/safety/rollback
// runtime and replaces ONLY application power control. No Docker start/stop
// fallback exists. Capture must derive G0 from the current verified node graph;
// the concrete native driver requires separate source/image/helper qualification.
func WithAppGroupControls(base Runtime, authority Authority, capture func(context.Context) ([]backuplifecycle.AppGroupControl, error), driver func(backuplifecycle.AppGroupControl) (backuplifecycle.AppGroupControlDriver, error)) (Runtime, error) {
	if base == nil || capture == nil || driver == nil {
		return nil, errors.New("restore app-group owner bindings missing")
	}
	return &appGroupRuntime{Runtime: base, authority: authority, capture: capture, driver: driver}, nil
}

type appGroupRuntime struct {
	Runtime
	authority     Authority
	capture       func(context.Context) ([]backuplifecycle.AppGroupControl, error)
	driver        func(backuplifecycle.AppGroupControl) (backuplifecycle.AppGroupControlDriver, error)
	journal       *lifecyclemutation.Session
	sessionDriver func(context.Context, *lifecyclemutation.Session, backuplifecycle.AppGroupControl) (backuplifecycle.AppGroupControlDriver, error)
}

func (r *appGroupRuntime) BindControlJournal(s *lifecyclemutation.Session) error {
	if s == nil || s.Record().OperationID != r.authority.OperationID {
		return errors.New("restore group journal changed")
	}
	r.journal = s
	return nil
}
func (r *appGroupRuntime) groups() ([]backuplifecycle.AppGroupControl, error) {
	if r.journal == nil || r.journal.Record().RestoreActivation == nil {
		return nil, errors.New("signed restore group journal missing")
	}
	groups := r.journal.Record().RestoreActivation.AppGroups
	if len(groups) == 0 {
		return nil, errors.New("restore group G0 not yet captured")
	}
	return groups, backuplifecycle.ValidateAppGroups(groups)
}
func (r *appGroupRuntime) bindGroups(ctx context.Context) error {
	if r.journal == nil {
		return errors.New("restore group journal missing")
	}
	if len(r.journal.Record().RestoreActivation.AppGroups) > 0 {
		return nil
	}
	scoped, err := r.journal.WithAppGroupContext(ctx)
	if err != nil {
		return err
	}
	groups, err := r.capture(scoped)
	if err != nil {
		return err
	}
	if len(groups) != 1 {
		return errors.New("first qualified restore slice requires exactly one daemon app-group")
	}
	if err = backuplifecycle.ValidateAppGroups(groups); err != nil {
		return err
	}
	for _, g := range groups {
		if g.Binding.OwnerRef != r.authority.OwnerRef || g.Binding.JournalOperationID != r.authority.OperationID || len(g.Effects) != 0 {
			return errors.New("restore group is not fresh/current operation custody")
		}
		for _, v := range r.authority.VolumeDetails {
			found := false
			for _, actual := range g.Original.Volumes {
				if actual.Name == v.LiveName {
					found = true
				}
			}
			if !found {
				return errors.New("restore volume outside exact external group source")
			}
		}
	}
	return r.journal.RecordRestoreAppGroups(groups)
}
func (r *appGroupRuntime) persist(index int, g backuplifecycle.AppGroupControl) error {
	groups, err := r.groups()
	if err != nil {
		return err
	}
	groups[index] = g
	return r.journal.RecordRestoreAppGroups(groups)
}
func (r *appGroupRuntime) settle(ctx context.Context) error {
	groups, err := r.groups()
	if err != nil {
		return err
	}
	for i, g := range groups {
		d, err := r.boundDriver(ctx, g)
		if err != nil {
			return err
		}
		if _, err = backuplifecycle.SettleAppGroup(ctx, g, func(next backuplifecycle.AppGroupControl) error { return r.persist(i, next) }, d); err != nil {
			return err
		}
	}
	return nil
}
func (r *appGroupRuntime) Stop(ctx context.Context, a Authority) error {
	if !reflect.DeepEqual(a, r.authority) {
		return errors.New("restore app-group authority changed")
	}
	if err := r.bindGroups(ctx); err != nil {
		return err
	}
	// Failed original START must settle before rollback STOP or ANY data copy.
	if err := r.settle(ctx); err != nil {
		return err
	}
	if settler, ok := r.Runtime.(interface {
		SettleAppGroupCopyHelpers(context.Context, Authority) error
	}); ok {
		if err := settler.SettleAppGroupCopyHelpers(ctx, a); err != nil {
			return err
		}
	}
	groups, err := r.groups()
	if err != nil {
		return err
	}
	rollback := strings.HasPrefix(r.journal.Record().Phase, "rollback-")
	for i, g := range groups {
		d, err := r.boundDriver(ctx, g)
		if err != nil {
			return err
		}
		if err = d.Hold(ctx, g); err != nil {
			return err
		}
		if !g.InitialRunning() {
			if err = d.VerifyCurrent(ctx, g); err != nil {
				return err
			}
			continue
		}
		if backuplifecycle.AppGroupStopped(g) {
			if err = d.VerifyCurrent(ctx, g); err != nil {
				return err
			}
			continue
		}
		action := "stop"
		if rollback {
			action = "rollback-stop"
		}
		if _, err = backuplifecycle.AdvanceAppGroup(ctx, g, action, func(next backuplifecycle.AppGroupControl) error { return r.persist(i, next) }, d); err != nil {
			return err
		}
	}
	return nil
}
func (r *appGroupRuntime) Start(ctx context.Context, a Authority) error {
	if !reflect.DeepEqual(a, r.authority) {
		return errors.New("restore app-group authority changed")
	}
	if err := r.settle(ctx); err != nil {
		return err
	}
	groups, err := r.groups()
	if err != nil {
		return err
	}
	for i, g := range groups {
		d, err := r.boundDriver(ctx, g)
		if err != nil {
			return err
		}
		if !g.InitialRunning() {
			if err = d.VerifyCurrent(ctx, g); err != nil {
				return err
			}
			continue
		}
		action := "start"
		if strings.HasPrefix(r.journal.Record().Phase, "rollback-") {
			action = "rollback-start"
		}
		if _, err = backuplifecycle.AdvanceAppGroup(ctx, g, action, func(next backuplifecycle.AppGroupControl) error { return r.persist(i, next) }, d); err != nil {
			return err
		}
	}
	return nil
}
func (r *appGroupRuntime) requireStopped(ctx context.Context, a Authority) error {
	if !reflect.DeepEqual(a, r.authority) {
		return errors.New("restore copy authority changed")
	}
	if err := r.settle(ctx); err != nil {
		return err
	}
	groups, err := r.groups()
	if err != nil {
		return err
	}
	for _, g := range groups {
		if !backuplifecycle.AppGroupStopped(g) {
			return backuplifecycle.ErrControlUnresolved
		}
		d, err := r.boundDriver(ctx, g)
		if err != nil {
			return err
		}
		if err = d.VerifyCurrent(ctx, g); err != nil {
			return err
		}
	}
	return nil
}
func (r *appGroupRuntime) PrepareRollback(ctx context.Context, a Authority, v Volume) error {
	if err := r.requireStopped(ctx, a); err != nil {
		return err
	}
	return r.Runtime.PrepareRollback(ctx, a, v)
}
func (r *appGroupRuntime) ActivateVolume(ctx context.Context, a Authority, v Volume) error {
	if err := r.requireStopped(ctx, a); err != nil {
		return err
	}
	return r.Runtime.ActivateVolume(ctx, a, v)
}
func (r *appGroupRuntime) RestoreVolume(ctx context.Context, a Authority, v Volume) error {
	if err := r.requireStopped(ctx, a); err != nil {
		return err
	}
	return r.Runtime.RestoreVolume(ctx, a, v)
}

// CloseAppGroupHolds is deliberately separate from Start/verification. The
// owning activation service calls it ONLY after signed terminal completion.
func (r *appGroupRuntime) CloseAppGroupHolds(ctx context.Context) error {
	if r.journal == nil {
		return errors.New("restore group journal missing")
	}
	record := r.journal.Record()
	if record.Status != lifecyclemutation.StatusSucceeded && record.Status != lifecyclemutation.StatusRecovered {
		return errors.New("restore hold cannot close before terminal signed journal")
	}
	groups, err := r.groups()
	if err != nil {
		return err
	}
	for _, g := range groups {
		if r.journal.RestoreAppGroupClosed(g) {
			continue
		}
		if !backuplifecycle.AppGroupRestored(g) {
			return backuplifecycle.ErrControlUnresolved
		}
		d, err := r.boundDriver(ctx, g)
		if err != nil {
			return err
		}
		if err = d.Close(ctx, g); err != nil {
			return err
		}
		if err = r.journal.AcknowledgeRestoreAppGroupClose(g); err != nil {
			return err
		}
	}
	return nil
}

func WithAppGroupSessionControls(base Runtime, authority Authority, capture func(context.Context) ([]backuplifecycle.AppGroupControl, error), driver func(context.Context, *lifecyclemutation.Session, backuplifecycle.AppGroupControl) (backuplifecycle.AppGroupControlDriver, error)) (Runtime, error) {
	if base == nil || capture == nil || driver == nil {
		return nil, errors.New("restore group session factory missing")
	}
	return &appGroupRuntime{Runtime: base, authority: authority, capture: capture, sessionDriver: driver}, nil
}
func (r *appGroupRuntime) boundDriver(ctx context.Context, g backuplifecycle.AppGroupControl) (backuplifecycle.AppGroupControlDriver, error) {
	if r.journal == nil {
		return nil, errors.New("restore control Session not bound")
	}
	scoped, err := r.journal.WithAppGroupContext(ctx)
	if err != nil {
		return nil, err
	}
	if r.sessionDriver != nil {
		return r.sessionDriver(scoped, r.journal, g)
	}
	return r.driver(g)
}
