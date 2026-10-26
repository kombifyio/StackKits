package restoreactivation

import (
	"context"
	"errors"

	"github.com/kombifyio/stackkits/internal/backupexec"
)

// WithAppGroupVolumeBinding reuses the existing signed source translation and
// Docker copy owner. Group power is deliberately unavailable here; the owning
// Session must wrap this runtime with WithAppGroupSessionControls.
func WithAppGroupVolumeBinding(base Runtime, authority Authority, binding backupexec.ApplicationRuntimeBinding, current func(context.Context) (backupexec.ApplicationRuntimeBinding, error)) (Runtime, error) {
	if base == nil || current == nil || binding.Runtime.RuntimeAdapterRef != "runtipi" || binding.Control != nil || binding.PhysicalProject != binding.Runtime.ComposeProject+"__user" || len(authority.VolumeDetails) == 0 {
		return nil, errors.New("restoreactivation: native group volume binding is incomplete")
	}
	for _, volume := range authority.VolumeDetails {
		if volume.ComposeProject != binding.Runtime.ComposeProject || binding.VolumeNames[volume.LiveName] != volume.LiveName {
			return nil, errors.New("restoreactivation: native group changes an exact selected external volume")
		}
	}
	return &appGroupVolumeRuntime{&applicationRuntime{Runtime: base, authority: authority, binding: binding, current: current}}, nil
}

type appGroupVolumeRuntime struct{ *applicationRuntime }

func (*appGroupVolumeRuntime) Stop(context.Context, Authority) error {
	return errors.New("restoreactivation: native group requires signed Session power control")
}

func (*appGroupVolumeRuntime) Start(context.Context, Authority) error {
	return errors.New("restoreactivation: native group requires signed Session power control")
}

func (r *appGroupVolumeRuntime) SettleAppGroupCopyHelpers(ctx context.Context, a Authority) error {
	if err := r.verify(ctx, a); err != nil {
		return err
	}
	base, ok := r.Runtime.(*dockerRuntime)
	if !ok {
		return errors.New("restoreactivation: native group has no existing Docker copy owner")
	}
	physical, _, err := r.physical(ctx, a, a.VolumeDetails[0])
	if err != nil {
		return err
	}
	return base.stopCopyHelpers(ctx, physical)
}
