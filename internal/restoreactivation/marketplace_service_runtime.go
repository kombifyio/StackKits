package restoreactivation

import (
	"context"
	"errors"
	"reflect"
	"strings"

	"github.com/kombifyio/stackkits/internal/backupexec"
	"github.com/kombifyio/stackkits/internal/backuplifecycle"
	"github.com/kombifyio/stackkits/internal/lifecyclemutation"
	"github.com/kombifyio/stackkits/internal/localbackuppolicy"
)

// Store data remains upstream-owned. This adapter supplies observation and
// native manager control to the existing activation/copy/rollback lifecycle.
func WithMarketplaceService(base Runtime, a Authority, s localbackuppolicy.MarketplaceService, observe func(context.Context) (backupexec.QuiesceContainer, error), driver backuplifecycle.RuntimeControlDriver) (Runtime, error) {
	if base == nil || observe == nil || driver == nil || len(a.VolumeDetails) != 1 {
		return nil, errors.New("Store activation requires one selected filesystem source")
	}
	v := a.VolumeDetails[0]
	if v.MarketplaceService != s.ServiceUUID || v.MarketplaceComponent != s.ComponentUUID || v.LiveName != s.VolumeName || v.ComposeProject != "" {
		return nil, errors.New("Store activation volume differs from the sealed selection")
	}
	return &marketplaceServiceRuntime{Runtime: base, authority: a, source: s, observe: observe, driver: driver}, nil
}

type marketplaceServiceRuntime struct {
	Runtime
	authority Authority
	source    localbackuppolicy.MarketplaceService
	observe   func(context.Context) (backupexec.QuiesceContainer, error)
	driver    backuplifecycle.RuntimeControlDriver
	journal   *lifecyclemutation.Session
}

func (r *marketplaceServiceRuntime) BindControlJournal(s *lifecyclemutation.Session) error {
	if s == nil || s.Record().OperationID != r.authority.OperationID || s.Record().RestoreActivation == nil {
		return errors.New("Store activation journal changed")
	}
	if err := bindJournalAuthority(r.authority, s.Record().RestoreActivation.Authority); err != nil {
		return err
	}
	r.journal = s
	return nil
}
func (r *marketplaceServiceRuntime) verify(ctx context.Context, a Authority) error {
	if !reflect.DeepEqual(a, r.authority) {
		return errors.New("Store activation authority changed")
	}
	c, err := r.observe(ctx)
	if err != nil {
		return err
	}
	if c.ID != r.source.ExpectedGeneration.ContainerID || c.Paused || c.Restarting {
		return errors.New("Store activation generation or power is unresolved")
	}
	return nil
}
func (r *marketplaceServiceRuntime) Inspect(ctx context.Context, a Authority) error {
	if err := r.verify(ctx, a); err != nil {
		return err
	}
	a.VolumeDetails = nil
	return r.Runtime.Inspect(ctx, a)
}
func (r *marketplaceServiceRuntime) Stop(ctx context.Context, a Authority) error {
	if !reflect.DeepEqual(a, r.authority) || r.journal == nil {
		return errors.New("Store recovery requires authenticated original authority")
	}
	record := r.journal.Record()
	if record.RestoreActivation == nil {
		return errors.New("Store recovery journal is missing")
	}
	if err := bindJournalAuthority(a, record.RestoreActivation.Authority); err != nil {
		return err
	}
	settler, ok := r.Runtime.(interface {
		settleJournalCopyHelpers(context.Context, Authority) error
	})
	if !ok {
		return errors.New("Store recovery helper settlement is unavailable")
	}
	if err := settler.settleJournalCopyHelpers(ctx, a); err != nil {
		return err
	}
	if err := r.verify(ctx, a); err != nil {
		return err
	}
	record = r.journal.Record()
	controls := record.RestoreActivation.Controls
	if len(controls) == 0 {
		if record.Phase != lifecyclemutation.PhaseQuiesceStarted {
			return errors.New("Store recovery has no authenticated initial power state")
		}
		c, err := r.observe(ctx)
		if err != nil {
			return err
		}
		controls = []backuplifecycle.RuntimeControl{{ComponentRef: r.source.ComponentUUID, ContainerID: c.ID, WasRunning: c.Running}}
		if err := r.journal.RecordRestoreControls(controls); err != nil {
			return err
		}
	}

	action := "stop"
	if strings.HasPrefix(record.Phase, "rollback-") {
		action = "rollback-stop"
	}
	return r.advance(ctx, action)
}
func (r *marketplaceServiceRuntime) Start(ctx context.Context, a Authority) error {
	if err := r.verify(ctx, a); err != nil {
		return err
	}
	if r.journal == nil {
		return errors.New("Store activation journal is unavailable")
	}
	controls := r.journal.Record().RestoreActivation.Controls
	if len(controls) != 1 {
		return errors.New("Store activation initial power is unavailable")
	}
	action := "start"
	if controls[0].RollbackStop.State != "" {
		action = "rollback-start"
	}
	return r.advance(ctx, action)
}
func (r *marketplaceServiceRuntime) advance(ctx context.Context, action string) error {
	controls := r.journal.Record().RestoreActivation.Controls
	if len(controls) != 1 {
		return errors.New("Store control set changed")
	}
	_, err := backuplifecycle.AdvanceRuntimeControl(ctx, controls[0], action, func(c backuplifecycle.RuntimeControl) error {
		return r.journal.RecordRestoreControls([]backuplifecycle.RuntimeControl{c})
	}, func(ctx context.Context) (string, bool, error) { c, err := r.observe(ctx); return c.ID, c.Running, err }, r.driver)
	return err
}
func (r *marketplaceServiceRuntime) PrepareRollback(ctx context.Context, a Authority, v Volume) error {
	if err := r.verifyStopped(ctx, a, v); err != nil {
		return err
	}
	return r.Runtime.PrepareRollback(ctx, a, v)
}
func (r *marketplaceServiceRuntime) ActivateVolume(ctx context.Context, a Authority, v Volume) error {
	if err := r.verifyStopped(ctx, a, v); err != nil {
		return err
	}
	return r.Runtime.ActivateVolume(ctx, a, v)
}
func (r *marketplaceServiceRuntime) RestoreVolume(ctx context.Context, a Authority, v Volume) error {
	if err := r.verifyStopped(ctx, a, v); err != nil {
		return err
	}
	return r.Runtime.RestoreVolume(ctx, a, v)
}
func (r *marketplaceServiceRuntime) verifyStopped(ctx context.Context, a Authority, v Volume) error {
	if err := r.verify(ctx, a); err != nil {
		return err
	}
	if err := requireAuthorityVolume(a, v); err != nil {
		return err
	}
	c, err := r.observe(ctx)
	if err != nil {
		return err
	}
	if c.Running {
		return errors.New("Store writer is running before copy")
	}
	if r.journal == nil {
		return errors.New("Store copy has no control journal")
	}
	controls := r.journal.Record().RestoreActivation.Controls
	if len(controls) != 1 {
		return errors.New("Store copy has no initial state")
	}
	x := controls[0]
	for _, e := range []backuplifecycle.RuntimeControlEffect{x.Stop, x.Start, x.RollbackStop, x.RollbackStart} {
		if e.State == backuplifecycle.ControlPreparing || e.State == backuplifecycle.ControlPrepared || e.State == backuplifecycle.ControlDispatched {
			return backuplifecycle.ErrControlUnresolved
		}
	}
	if x.WasRunning && x.Stop.State != backuplifecycle.ControlObserved {
		return backuplifecycle.ErrControlUnresolved
	}
	return nil
}
