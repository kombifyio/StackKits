package backuplifecycle

import (
	"context"
	"errors"
	"strings"
)

// RuntimeControl lives in the owning signed snapshot/activation journal.
// It retains initial power and exact manager handles across process restarts.
type RuntimeControl struct {
	ComponentRef  string               `json:"componentRef"`
	ContainerID   string               `json:"containerId"`
	WasRunning    bool                 `json:"wasRunning"`
	Stop          RuntimeControlEffect `json:"stop"`
	Start         RuntimeControlEffect `json:"start"`
	RollbackStop  RuntimeControlEffect `json:"rollbackStop"`
	RollbackStart RuntimeControlEffect `json:"rollbackStart"`
}
type RuntimeControlEffect struct {
	Handle string `json:"handle,omitempty"`
	State  string `json:"state,omitempty"`
}

const (
	ControlPreparing  = "preparing"
	ControlPrepared   = "prepared"
	ControlDispatched = "dispatched"
	ControlObserved   = "observed"
)

var ErrControlUnresolved = errors.New("runtime control is unresolved; observe the original manager handle before any further mutation")

// RuntimeControlDriver prepares a dormant, command-bound manager handle.
// Complete requires positive terminal actuation proof. HTTP acceptance and
// unchanged power are not completion. Dispatch is never replayed.
type RuntimeControlDriver interface {
	Prepare(context.Context, string) (string, error)
	Dispatch(context.Context, string, string) error
	Complete(context.Context, string, string) (bool, error)
}

func runtimeControlEffect(c *RuntimeControl, action string) *RuntimeControlEffect {
	switch action {
	case "stop":
		return &c.Stop
	case "start":
		return &c.Start
	case "rollback-stop":
		return &c.RollbackStop
	case "rollback-start":
		return &c.RollbackStart
	}
	return nil
}
func ValidateRuntimeControls(controls []RuntimeControl) error {
	seen := map[string]bool{}
	for _, c := range controls {
		if c.ComponentRef == "" || c.ContainerID == "" || seen[c.ContainerID] {
			return errors.New("runtime control has missing or duplicate identity")
		}
		seen[c.ContainerID] = true
		for _, action := range []string{"stop", "start", "rollback-stop", "rollback-start"} {
			e := runtimeControlEffect(&c, action)
			if e.State != "" && e.State != ControlPreparing && e.State != ControlPrepared && e.State != ControlDispatched && e.State != ControlObserved {
				return errors.New("invalid runtime control effect state")
			}
			if (e.State == "" || e.State == ControlPreparing) && e.Handle != "" || e.State != "" && e.State != ControlPreparing && e.Handle == "" {
				return errors.New("runtime control handle differs from effect state")
			}
			if !c.WasRunning && e.State != "" {
				return errors.New("runtime control changes an initially stopped component")
			}
		}
		if c.Start.State != "" && c.Stop.State != ControlObserved || c.RollbackStart.State != "" && c.RollbackStop.State != ControlObserved {
			return errors.New("runtime control starts before its verified stop")
		}
	}
	return nil
}

// AdvanceRuntimeControl dispatches each exact prepared handle once. A lost
// dispatch response admits only inspection of that handle. persist must
// atomically retain the next value in the existing owning signed journal.
func AdvanceRuntimeControl(ctx context.Context, original RuntimeControl, action string,
	persist func(RuntimeControl) error, observe func(context.Context) (string, bool, error),
	driver RuntimeControlDriver,
) (RuntimeControl, error) {
	c := original
	if ctx == nil || persist == nil || observe == nil || driver == nil || runtimeControlEffect(&c, action) == nil {
		return c, errors.New("runtime control requires a bound journal, observer and manager driver")
	}
	if err := ValidateRuntimeControls([]RuntimeControl{c}); err != nil {
		return c, err
	}
	if !c.WasRunning {
		id, running, err := observe(ctx)
		if err != nil {
			return c, err
		}
		if id != c.ContainerID || running {
			return c, errors.New("initially stopped component generation or power changed")
		}
		return c, nil
	}
	var err error
	c, err = settleRuntimeControl(ctx, c, action, persist, observe, driver)
	if err != nil {
		return c, err
	}
	// Rollback before activation start already holds this operation's verified
	// stop. Preserve that receipt instead of dispatching another stop.
	if action == "rollback-stop" && c.Start.State == "" && c.Stop.State == ControlObserved {
		return c, nil
	}
	if action == "start" && c.Stop.State == "" {
		return c, nil
	}
	if action == "start" && c.Stop.State != ControlObserved || action == "rollback-start" && c.RollbackStop.State != ControlObserved {
		return c, ErrControlUnresolved
	}
	e := runtimeControlEffect(&c, action)
	id, running, err := observe(ctx)
	if err != nil {
		return c, err
	}
	if id != c.ContainerID {
		return c, errors.New("runtime control container generation changed")
	}
	desired := controlStarts(action)
	if e.State == ControlObserved {
		if running != desired {
			return c, errors.New("power changed after verified runtime control")
		}
		return c, nil
	}
	if e.State == "" || e.State == ControlPrepared {
		if validator, ok := driver.(interface {
			Validate(context.Context, string) error
		}); ok {
			if err := validator.Validate(ctx, action); err != nil {
				return c, err
			}
		}
	}
	if e.State == "" {
		if running == desired {
			return c, errors.New("power changed before runtime control dispatch")
		}
		e.State = ControlPreparing
		if err := persist(c); err != nil {
			return c, err
		}
		handle, err := driver.Prepare(ctx, action)
		if err != nil {
			return c, errors.Join(ErrControlUnresolved, err)
		}
		if handle == "" {
			return c, ErrControlUnresolved
		}
		e.Handle, e.State = handle, ControlPrepared
		if err := persist(c); err != nil {
			return c, err
		}
	}
	e.State = ControlDispatched
	if err := persist(c); err != nil {
		return c, err
	}
	if err := driver.Dispatch(ctx, e.Handle, action); err != nil {
		return c, errors.Join(ErrControlUnresolved, err)
	}
	complete, err := driver.Complete(ctx, e.Handle, action)
	if err != nil {
		return c, errors.Join(ErrControlUnresolved, err)
	}
	if !complete {
		return c, ErrControlUnresolved
	}
	id, running, err = observe(ctx)
	if err != nil {
		return c, err
	}
	if id != c.ContainerID || running != desired {
		return c, errors.New("runtime control effect has changed identity or power")
	}
	e.State = ControlObserved
	if err := persist(c); err != nil {
		return c, err
	}
	return c, nil
}
func controlStarts(action string) bool { return action == "start" || action == "rollback-start" }

// SettleRuntimeControl observes only retained handles, never dispatches. It is
// used across the whole writer set before admitting any opposite action.
func SettleRuntimeControl(ctx context.Context, c RuntimeControl, persist func(RuntimeControl) error, observe func(context.Context) (string, bool, error), driver RuntimeControlDriver) (RuntimeControl, error) {
	if persist == nil || observe == nil || driver == nil {
		return c, errors.New("runtime settlement requires its owning bindings")
	}
	if err := ValidateRuntimeControls([]RuntimeControl{c}); err != nil {
		return c, err
	}
	return settleRuntimeControl(ctx, c, "", persist, observe, driver)
}
func settleRuntimeControl(ctx context.Context, c RuntimeControl, requestedAction string, persist func(RuntimeControl) error, observe func(context.Context) (string, bool, error), driver RuntimeControlDriver) (RuntimeControl, error) {
	// Settle any earlier dispatch before another or opposite action.
	for _, previous := range []string{"stop", "start", "rollback-stop", "rollback-start"} {
		e := runtimeControlEffect(&c, previous)
		if e.State == ControlPreparing || e.State == ControlPrepared && previous != requestedAction {
			return c, ErrControlUnresolved
		}
		if e.State != ControlDispatched {
			continue
		}
		complete, err := driver.Complete(ctx, e.Handle, previous)
		if err != nil {
			return c, errors.Join(ErrControlUnresolved, err)
		}
		if !complete {
			return c, ErrControlUnresolved
		}
		id, running, err := observe(ctx)
		if err != nil {
			return c, err
		}
		if id != c.ContainerID || running != controlStarts(previous) {
			return c, errors.New("completed runtime control has changed identity or power")
		}
		e.State = ControlObserved
		if err := persist(c); err != nil {
			return c, err
		}
	}
	return c, nil
}

// NewSynchronousRuntimeControl retains positive terminal replies in-process.
// A lost reply remains unresolved across restart; power is not a receipt.
func NewSynchronousRuntimeControl(id string, call func(context.Context, string) error) RuntimeControlDriver {
	return &synchronousRuntimeControl{id: id, call: call, complete: map[string]bool{}}
}

type synchronousRuntimeControl struct {
	id       string
	call     func(context.Context, string) error
	complete map[string]bool
}

func (d *synchronousRuntimeControl) Prepare(_ context.Context, action string) (string, error) {
	if d.id == "" || d.call == nil || runtimeControlEffect(&RuntimeControl{}, action) == nil {
		return "", errors.New("synchronous control binding is incomplete")
	}
	return d.id + ":" + action, nil
}
func (d *synchronousRuntimeControl) Dispatch(ctx context.Context, handle, action string) error {
	if handle != d.id+":"+action || runtimeControlEffect(&RuntimeControl{}, action) == nil {
		return errors.New("synchronous control handle changed")
	}
	err := d.call(ctx, strings.TrimPrefix(action, "rollback-"))
	if err == nil {
		d.complete[handle] = true
	}
	return err
}
func (d *synchronousRuntimeControl) Complete(_ context.Context, handle, action string) (bool, error) {
	if handle != d.id+":"+action {
		return false, errors.New("synchronous control handle changed")
	}
	return d.complete[handle], nil
}
