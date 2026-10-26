package localbackupruntime

import (
	"context"
	"errors"
	"github.com/kombifyio/stackkits/internal/backupexec"
	"github.com/kombifyio/stackkits/internal/backuplifecycle"
	"time"
)

// AppGroupSnapshotBinding belongs to the existing node-custody adapter. Core
// is the existing exact-container quiescer after the selected app-group has
// been separated; Capture must derive one complete current daemon graph from
// the held Plan/Apply/Owner, never discovery or caller-selected container names.
// No binding is installed by default or merely from a runtipi catalog value.
type AppGroupSnapshotBinding struct {
	Core    backupexec.ContainerQuiescer
	Capture func(context.Context, string) (backuplifecycle.AppGroupControl, error)
	Driver  func(context.Context, backuplifecycle.AppGroupControl) (backuplifecycle.AppGroupControlDriver, error)
}

func (r *Runtime) BindAppGroupSnapshots(binding AppGroupSnapshotBinding) error {
	if r == nil || r.policy == nil || binding.Core == nil || binding.Capture == nil || binding.Driver == nil {
		return errors.New("qualified app-group snapshot owner incomplete")
	}
	// Native group policy/projection and concrete custody adapter must be supplied
	// together. Retained stock 4.10.2 has no such admission.
	selected := 0
	for _, graph := range r.sourceProjection().ApplicationRuntimes {
		if graph.RuntimeAdapterRef == "runtipi" {
			selected++
		}
	}
	if selected != 1 {
		return errors.New("first app-group snapshot requires one explicitly admitted Runtipi source graph")
	}
	r.appGroups = &binding
	r.quiescer = binding.Core
	return nil
}
func (r *Runtime) createAppGroupSnapshot(ctx context.Context, request backuplifecycle.RepositorySnapshotRequest, j backuplifecycle.SnapshotQuiescence, persist func(backuplifecycle.SnapshotQuiescence) error) (receipt backuplifecycle.RepositorySnapshotReceipt, resultErr error) {
	if persist == nil || r.appGroups == nil {
		return receipt, errors.New("signed app-group snapshot binding missing")
	}
	if err := r.ready(ctx); err != nil {
		return receipt, err
	}
	if j.Phase == "" {
		initial, err := r.appGroups.Capture(ctx, request.OperationID)
		if err != nil {
			return receipt, err
		}
		if err = backuplifecycle.ValidateAppGroups([]backuplifecycle.AppGroupControl{initial}); err != nil {
			return receipt, err
		}
		if initial.Binding.OwnerRef != request.OwnerRef || initial.Binding.JournalOperationID != request.OperationID || len(initial.Effects) != 0 {
			return receipt, errors.New("snapshot initial group authority changed")
		}
		j, err = r.prepareQuiescence(ctx)
		if err != nil {
			return receipt, err
		}
		j.ControlVersion = 2
		j.AppGroups = []backuplifecycle.AppGroupControl{initial}
		// Core controls retain their existing v1 exact-container semantics.
		j.Controls = nil
		for _, c := range j.Containers {
			j.Controls = append(j.Controls, backuplifecycle.RuntimeControl{ComponentRef: c.Name, ContainerID: c.ID, WasRunning: c.WasRunning})
		}
		j.Containers = append(j.Containers, initial.Original.Containers...)
		if err = persist(j); err != nil {
			return receipt, err
		}
	}
	if j.ControlVersion != 2 || len(j.AppGroups) != 1 {
		return receipt, errors.New("snapshot app-group journal protocol changed")
	}
	if err := r.validateQuiescenceGraph(j); err != nil {
		return receipt, err
	}
	groupPersist := func(next backuplifecycle.AppGroupControl) error {
		candidate := j
		candidate.AppGroups = backuplifecycle.CloneAppGroups(j.AppGroups)
		candidate.AppGroups[0] = next
		if err := persist(candidate); err != nil {
			return err
		}
		j = candidate
		return nil
	}
	g := j.AppGroups[0]
	d, err := r.appGroups.Driver(ctx, g)
	if err != nil {
		return receipt, err
	}
	// Preflight native capability/source and group hold before any Core stop/data.
	if err = d.Hold(ctx, g); err != nil {
		return receipt, err
	}
	g, err = backuplifecycle.SettleAppGroup(ctx, g, groupPersist, d)
	if err != nil {
		return receipt, err
	}
	// Existing Kopia settlement must precede writer resume on every error/retry.
	attempted := j.Phase == "stopped" || j.Phase == "snapshot-created" || j.Phase == "resuming"
	settledKopia := false
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Minute)
		defer cancel()
		if attempted && !settledKopia {
			if err := r.authorizeSnapshotSettlement(request); err != nil {
				resultErr = errors.Join(resultErr, err)
				return
			}
			if err := r.settleSnapshotRuntime(); err != nil {
				resultErr = errors.Join(resultErr, err)
				return
			}
		}
		current := j.AppGroups[0]
		driver, err := r.appGroups.Driver(cleanup, current)
		if err != nil {
			resultErr = errors.Join(resultErr, err)
			return
		}
		current, err = backuplifecycle.SettleAppGroup(cleanup, current, groupPersist, driver)
		if err != nil {
			resultErr = errors.Join(resultErr, err)
			return
		}
		j.Phase = "resuming"
		if err = persist(j); err != nil {
			resultErr = errors.Join(resultErr, err)
			return
		}
		// Settlement may have appended the predecessor receipt. Rebind the
		// driver before preparing the next native operation/action.
		driver, err = r.appGroups.Driver(cleanup, current)
		if err != nil {
			resultErr = errors.Join(resultErr, err)
			return
		}
		if current.InitialRunning() && !backuplifecycle.AppGroupRestored(current) {
			// A closed failed START may leave a partial graph. It is not an absence
			// witness: obtain a new governed rollback STOP before any rollback START.
			if !backuplifecycle.AppGroupStopped(current) {
				current, err = backuplifecycle.AdvanceAppGroup(cleanup, current, "rollback-stop", groupPersist, driver)
				if err == nil {
					driver, err = r.appGroups.Driver(cleanup, current)
				}
			}
			if err == nil {
				action := "start"
				for _, effect := range current.Effects {
					if effect.State == backuplifecycle.ControlFailed || effect.Action == "rollback-stop" {
						action = "rollback-start"
					}
				}
				current, err = backuplifecycle.AdvanceAppGroup(cleanup, current, action, groupPersist, driver)
			}
		} else {
			err = driver.VerifyCurrent(cleanup, current)
		}
		if err != nil {
			resultErr = errors.Join(resultErr, err)
			return
		}
		// Core resume is still its exact original journal; no group container is
		// passed through the legacy per-container control machinery.
		core := coreOnlyGroupJournal(j)
		if err = r.restoreControlledQuiescence(request.OperationID, &core, func(next backuplifecycle.SnapshotQuiescence) error {
			candidate := j
			candidate.Controls = next.Controls
			if err := persist(candidate); err != nil {
				return err
			}
			j = candidate
			return nil
		}); err != nil {
			resultErr = errors.Join(resultErr, err)
			return
		}
		j.Phase = "restored"
		if err = persist(j); err != nil {
			resultErr = errors.Join(resultErr, err)
			receipt = backuplifecycle.RepositorySnapshotReceipt{}
		}
		// Native hold remains open until owning service's signed anchor/operation
		// completion. No premature Close in a runtime cleanup/defer.
	}()
	if j.Phase == "resuming" || j.Phase == "restored" || len(g.Effects) > 1 {
		if err = r.authorizeSnapshotSettlement(request); err != nil {
			return receipt, err
		}
		if err = r.settleSnapshotRuntime(); err != nil {
			return receipt, err
		}
		settledKopia = true
		foundReceipt, found, err := r.LookupSnapshot(ctx, request)
		if err != nil {
			return receipt, err
		}
		if !found {
			return receipt, errors.New("original snapshot absent after writer resume; no re-actuation permitted")
		}
		return foundReceipt, nil
	}
	core := coreOnlyGroupJournal(j)
	if err = r.stopSnapshotWriters(ctx, request.OperationID, &core, func(next backuplifecycle.SnapshotQuiescence) error {
		candidate := j
		candidate.Controls = next.Controls
		if err := persist(candidate); err != nil {
			return err
		}
		j = candidate
		return nil
	}); err != nil {
		return receipt, err
	}
	g = j.AppGroups[0]
	d, err = r.appGroups.Driver(ctx, g)
	if err != nil {
		return receipt, err
	}
	if g.InitialRunning() {
		if _, err = backuplifecycle.AdvanceAppGroup(ctx, g, "stop", groupPersist, d); err != nil {
			return receipt, err
		}
	} else if err = d.VerifyCurrent(ctx, g); err != nil {
		return receipt, err
	}
	j.Phase = "stopped"
	if err = persist(j); err != nil {
		return receipt, err
	}
	attempted = true
	// The SAME policy, repository secret, Kopia engine and request are reused.
	// Existing CreateSnapshot includes exact source policy/secret admission.
	existing, found, lookupErr := r.LookupSnapshot(ctx, request)
	if lookupErr != nil {
		return receipt, lookupErr
	}
	if found {
		receipt = existing
	} else {
		receipt, err = r.CreateSnapshot(ctx, request)
	}
	if err != nil {
		return receipt, err
	}
	j.Phase = "snapshot-created"
	if err = persist(j); err != nil {
		return receipt, err
	}
	return receipt, nil
}
func coreOnlyGroupJournal(j backuplifecycle.SnapshotQuiescence) backuplifecycle.SnapshotQuiescence {
	j.ControlVersion = 1
	j.AppGroups = nil
	if j.Phase == "resuming" {
		j.Phase = "prepared"
	}
	var core []backuplifecycle.SnapshotQuiescedContainer
	ids := map[string]bool{}
	for _, c := range j.Controls {
		ids[c.ContainerID] = true
	}
	for _, c := range j.Containers {
		if ids[c.ID] {
			core = append(core, c)
		}
	}
	j.Containers = core
	return j
}

func (r *Runtime) CloseSnapshotAppGroupHolds(ctx context.Context, anchor backuplifecycle.SnapshotAnchor) error {
	if r.appGroups == nil || anchor.Quiescence == nil || anchor.Quiescence.ControlVersion != 2 {
		return errors.New("snapshot group closure requires original qualified adapter")
	}
	if err := backuplifecycle.VerifySnapshotAnchor(r.workspaceRoot, anchor); err != nil {
		return err
	}
	for _, g := range anchor.Quiescence.AppGroups {
		if err := backuplifecycle.VerifySnapshotAppGroup(r.workspaceRoot, g, true); err != nil {
			return err
		}
		d, err := r.appGroups.Driver(ctx, g)
		if err != nil {
			return err
		}
		if err = d.Close(ctx, g); err != nil {
			return err
		}
	}
	return nil
}

// Failed snapshot closure has no anchor or success receipt. The existing signed
// operation must first record restored/quiescent negative completion.
func (r *Runtime) CloseFailedSnapshotAppGroupHolds(ctx context.Context, groups []backuplifecycle.AppGroupControl) error {
	if r.appGroups == nil {
		return errors.New("failed group closure adapter absent")
	}
	for _, g := range groups {
		if err := backuplifecycle.VerifySnapshotAppGroup(r.workspaceRoot, g, true); err != nil {
			return err
		}
		d, err := r.appGroups.Driver(ctx, g)
		if err != nil {
			return err
		}
		if err = d.Close(ctx, g); err != nil {
			return err
		}
	}
	return nil
}
