package restoreactivation

import (
	"context"
	"errors"
	"reflect"

	"github.com/kombifyio/stackkits/internal/backupexec"
	"github.com/kombifyio/stackkits/internal/backuplifecycle"
	"github.com/kombifyio/stackkits/internal/lifecyclemutation"
)

// WithApplicationBinding delegates service control to the retained manager
// while reusing the native activation copy, rollback and readback machinery.
// The callback must re-read signed current custody, including observed mounts.
func WithApplicationBinding(base Runtime, authority Authority, binding backupexec.ApplicationRuntimeBinding, current func(context.Context) (backupexec.ApplicationRuntimeBinding, error)) (Runtime, error) {
	if base == nil || current == nil || binding.Control == nil || len(authority.VolumeDetails) == 0 {
		return nil, errors.New("restoreactivation: application runtime binding is incomplete")
	}
	for _, volume := range authority.VolumeDetails {
		if volume.ComposeProject != binding.Runtime.ComposeProject || binding.VolumeNames[volume.LiveName] == "" {
			return nil, errors.New("restoreactivation: selected volume is outside manager custody")
		}
	}
	order := []string{}
	seen := map[string]int{}
	var visit func(string) error
	visit = func(ref string) error {
		if seen[ref] == 2 {
			return nil
		}
		if seen[ref] == 1 {
			return errors.New("restoreactivation: cyclic application graph")
		}
		seen[ref] = 1
		found, daemon := false, false
		for _, component := range binding.Runtime.Components {
			if component.ComponentRef == ref {
				found, daemon = true, component.Lifecycle == "daemon"
				for _, dep := range component.DependsOn {
					if err := visit(dep); err != nil {
						return err
					}
				}
				break
			}
		}
		if !found {
			return errors.New("restoreactivation: unknown application dependency")
		}
		seen[ref] = 2
		// Managers control only daemons; restore never reruns a one-shot
		// initializer such as Immich's database init.
		if daemon {
			order = append(order, ref)
		}
		return nil
	}
	for _, component := range binding.Runtime.Components {
		if err := visit(component.ComponentRef); err != nil {
			return nil, err
		}
	}
	return &applicationRuntime{Runtime: base, authority: authority, binding: binding, current: current, order: order}, nil
}

type applicationRuntime struct {
	Runtime
	authority Authority
	binding   backupexec.ApplicationRuntimeBinding
	current   func(context.Context) (backupexec.ApplicationRuntimeBinding, error)
	order     []string
	journal   *lifecyclemutation.Session
	observe   func(context.Context, string) (backupexec.QuiesceContainer, error)
	driver    func(string, string, string) (backuplifecycle.RuntimeControlDriver, error)
}

func (r *applicationRuntime) verify(ctx context.Context, a Authority) error {
	if !reflect.DeepEqual(a, r.authority) {
		return errors.New("restoreactivation: application authority changed")
	}
	b, err := r.current(ctx)
	if err != nil {
		return err
	}
	if b.PhysicalProject != r.binding.PhysicalProject || !reflect.DeepEqual(b.Runtime, r.binding.Runtime) || !reflect.DeepEqual(b.VolumeNames, r.binding.VolumeNames) {
		return errors.New("restoreactivation: signed manager custody changed")
	}
	return nil
}

func (r *applicationRuntime) physical(ctx context.Context, a Authority, v Volume) (Authority, Volume, error) {
	if err := r.verify(ctx, a); err != nil {
		return Authority{}, Volume{}, err
	}
	if err := requireAuthorityVolume(a, v); err != nil {
		return Authority{}, Volume{}, err
	}
	a.VolumeDetails = append([]Volume(nil), a.VolumeDetails...)
	for i := range a.VolumeDetails {
		a.VolumeDetails[i].LiveName = r.binding.VolumeNames[a.VolumeDetails[i].LiveName]
		a.VolumeDetails[i].ComposeProject = r.binding.PhysicalProject
	}
	v.LiveName = r.binding.VolumeNames[v.LiveName]
	v.ComposeProject = r.binding.PhysicalProject
	return a, v, nil
}

func (r *applicationRuntime) Inspect(ctx context.Context, a Authority) error {
	if err := r.verify(ctx, a); err != nil {
		return err
	}
	// The base runtime checks the held Core helper and staging custody only;
	// selected application mounts were observed by current manager custody.
	a.VolumeDetails = nil
	return r.Runtime.Inspect(ctx, a)
}
func (r *applicationRuntime) ValidateStaging(ctx context.Context, a Authority) error {
	if err := r.verify(ctx, a); err != nil {
		return err
	}
	return r.Runtime.ValidateStaging(ctx, a)
}
func (r *applicationRuntime) Stop(ctx context.Context, a Authority) error {
	if err := r.verify(ctx, a); err != nil {
		return err
	}
	if base, ok := r.Runtime.(*dockerRuntime); ok {
		physical, _, err := r.physical(ctx, a, a.VolumeDetails[0])
		if err != nil {
			return err
		}
		if err = base.stopCopyHelpers(ctx, physical); err != nil {
			return err
		}
	}
	if err := r.captureControls(ctx); err != nil {
		return err
	}
	if err := r.settleControls(ctx); err != nil {
		return err
	}
	for i := len(r.order) - 1; i >= 0; i-- {
		if err := r.advanceControl(ctx, r.order[i], false); err != nil {
			return err
		}
	}

	return nil
}
func (r *applicationRuntime) Start(ctx context.Context, a Authority) error {
	if err := r.verify(ctx, a); err != nil {
		return err
	}
	if err := r.settleControls(ctx); err != nil {
		return err
	}
	for _, ref := range r.order {
		if err := r.advanceControl(ctx, ref, true); err != nil {
			return err
		}
	}

	return nil
}
func (r *applicationRuntime) PrepareRollback(ctx context.Context, a Authority, v Volume) error {
	a, v, err := r.physical(ctx, a, v)
	if err != nil {
		return err
	}
	return r.Runtime.PrepareRollback(ctx, a, v)
}
func (r *applicationRuntime) ActivateVolume(ctx context.Context, a Authority, v Volume) error {
	a, v, err := r.physical(ctx, a, v)
	if err != nil {
		return err
	}
	return r.Runtime.ActivateVolume(ctx, a, v)
}
func (r *applicationRuntime) RestoreVolume(ctx context.Context, a Authority, v Volume) error {
	a, v, err := r.physical(ctx, a, v)
	if err != nil {
		return err
	}
	return r.Runtime.RestoreVolume(ctx, a, v)
}
func (r *applicationRuntime) CleanupRollback(ctx context.Context, a Authority, v Volume) error {
	a, v, err := r.physical(ctx, a, v)
	if err != nil {
		return err
	}
	return r.Runtime.CleanupRollback(ctx, a, v)
}
func (r *applicationRuntime) DigestVolume(ctx context.Context, a Authority, v Volume, staged bool) (backuplifecycle.VolumeReadback, error) {
	logical := v.LiveName
	a, v, err := r.physical(ctx, a, v)
	if err != nil {
		return backuplifecycle.VolumeReadback{}, err
	}
	out, err := r.Runtime.DigestVolume(ctx, a, v, staged)
	out.LiveName = logical
	return out, err
}
