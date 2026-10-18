package localbackupruntime

import (
	"context"
	"errors"
	"time"

	"github.com/kombifyio/stackkits/internal/backupexec"
	"github.com/kombifyio/stackkits/internal/backuplifecycle"
	"github.com/kombifyio/stackkits/internal/localbackuppolicy"
)

// BindMarketplaceControl binds only the immutable selection already held by
// the Plan policy. Discovery cannot widen the snapshot source.
func (r *Runtime) BindMarketplaceControl(observe func(context.Context, localbackuppolicy.MarketplaceService) (backupexec.QuiesceContainer, error), control func(localbackuppolicy.MarketplaceService, string) (backuplifecycle.RuntimeControlDriver, error), componentControl func(backuplifecycle.SnapshotQuiescedContainer, string) (backuplifecycle.RuntimeControlDriver, error)) error {
	if r == nil || control == nil {
		return errors.New("Store backup requires native manager control")
	}
	q, err := backupexec.BindMarketplaceSources(r.quiescer, r.sourceProjection().MarketplaceServices, observe)
	if err != nil {
		return err
	}
	r.quiescer = q
	r.storeControl = control
	r.componentControl = componentControl
	return nil
}

func (r *Runtime) snapshotControl(expected backuplifecycle.SnapshotQuiescedContainer, operation string) (backuplifecycle.RuntimeControlDriver, error) {
	if expected.MarketplaceService != "" {
		for _, s := range r.sourceProjection().MarketplaceServices {
			if s.ServiceUUID == expected.MarketplaceService && s.ComponentUUID == expected.MarketplaceComponent && s.ExpectedGeneration.ContainerID == expected.ID && r.storeControl != nil {
				return r.storeControl(s, operation)
			}
		}
		return nil, errors.New("Store writer has no exact manager driver")
	}
	// Queued third-party manager HTTP replies are not terminal actuation proof.
	for _, app := range r.sourceProjection().ApplicationRuntimes {
		if app.WorkloadRef == expected.WorkloadRef && app.RuntimeAdapterRef == "coolify" {
			if r.componentControl != nil {
				return r.componentControl(expected, operation)
			}
			return nil, errors.New("selected curated manager needs its own terminal control receipt before Store snapshot")
		}
	}
	return backuplifecycle.NewSynchronousRuntimeControl(expected.ID, func(ctx context.Context, action string) error {
		if action == "stop" {
			return r.quiescer.StopContainer(ctx, expected.ID)
		}
		return r.quiescer.StartContainer(ctx, expected.ID)
	}), nil
}

func (r *Runtime) advanceSnapshotControl(ctx context.Context, operation string, j *backuplifecycle.SnapshotQuiescence, expected backuplifecycle.SnapshotQuiescedContainer, action string, persist func(backuplifecycle.SnapshotQuiescence) error) error {
	for i := range j.Controls {
		if j.Controls[i].ContainerID == expected.ID {
			driver, err := r.snapshotControl(expected, operation)
			if err != nil {
				return err
			}
			_, err = backuplifecycle.AdvanceRuntimeControl(ctx, j.Controls[i], action, func(c backuplifecycle.RuntimeControl) error {
				candidate := *j
				candidate.Controls = append([]backuplifecycle.RuntimeControl(nil), j.Controls...)
				candidate.Controls[i] = c
				if err := persist(candidate); err != nil {
					return err
				}
				*j = candidate
				return nil
			}, func(ctx context.Context) (string, bool, error) {
				c, err := r.quiescer.InspectContainer(ctx, expected.ID)
				if err == nil {
					err = exactQuiescedContainer(expected, c)
				}
				return c.ID, c.Running, err
			}, driver)
			return err
		}
	}
	return errors.New("snapshot writer has no persisted control identity")
}
func (r *Runtime) stopSnapshotWriters(ctx context.Context, operation string, j *backuplifecycle.SnapshotQuiescence, persist func(backuplifecycle.SnapshotQuiescence) error) error {
	if j.ControlVersion == 0 {
		return r.stopQuiescence(ctx, *j)
	}
	if err := r.validateQuiescence(ctx, *j); err != nil {
		return err
	}
	// Resolve every manager seam before any writer is stopped.
	for _, c := range j.Containers {
		if _, err := r.snapshotControl(c, operation); err != nil {
			return err
		}
	}
	for _, c := range quiescenceStopOrder(*j) {
		if err := r.advanceSnapshotControl(ctx, operation, j, c, "stop", persist); err != nil {
			return err
		}
	}
	managed, err := r.quiescer.ManagedContainers(ctx)
	if err != nil {
		return err
	}
	for _, c := range managed {
		if c.Running || c.Paused || c.Restarting {
			return errors.New("snapshot writer did not remain stopped")
		}
		wasRunning := false
		for _, control := range j.Controls {
			if control.ContainerID == c.ID {
				wasRunning = control.WasRunning
			}
		}
		if wasRunning && c.MarketplaceService != "" {
			if err := backupexec.ValidateCleanDockerStop(c); err != nil {
				return err
			}
		}
	}
	return nil
}
func (r *Runtime) restoreControlledQuiescence(operation string, j *backuplifecycle.SnapshotQuiescence, persist func(backuplifecycle.SnapshotQuiescence) error) error {
	if err := r.validateQuiescenceGraph(*j); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute*5)
	defer cancel()
	// Resolve every retained dispatch before any writer is resumed.
	for i, c := range j.Controls {
		var expected backuplifecycle.SnapshotQuiescedContainer
		for _, x := range j.Containers {
			if x.ID == c.ContainerID {
				expected = x
			}
		}
		driver, err := r.snapshotControl(expected, operation)
		if err != nil {
			return err
		}
		_, err = backuplifecycle.SettleRuntimeControl(ctx, c, func(next backuplifecycle.RuntimeControl) error {
			candidate := *j
			candidate.Controls = append([]backuplifecycle.RuntimeControl(nil), j.Controls...)
			candidate.Controls[i] = next
			if err := persist(candidate); err != nil {
				return err
			}
			*j = candidate
			return nil
		}, func(ctx context.Context) (string, bool, error) {
			x, err := r.quiescer.InspectContainer(ctx, expected.ID)
			if err == nil {
				err = exactQuiescedContainer(expected, x)
			}
			return x.ID, x.Running, err
		}, driver)
		if err != nil {
			return err
		}
	}
	for _, c := range quiescenceRestoreOrder(*j) {
		if err := r.advanceSnapshotControl(ctx, operation, j, c, "start", persist); err != nil {
			return err
		}
	}
	j.Phase = "restored"
	return persist(*j)
}
