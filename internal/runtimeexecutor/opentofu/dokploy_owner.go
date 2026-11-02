package opentofu

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"
)

// DokployWorkloadOwner routes a selected Dokploy workload. The node API owner
// deploys by default; the existing held-ID provider path runs only under its
// explicit opt-in, and neither takes over a workload the other owns.
type DokployWorkloadOwner struct {
	runtime   Runtime
	api       nativehost.SelectedPaaSWorkloadOperations
	validator nativehost.SelectedPaaSWorkloadObservationValidator
	provider  *DokployWorkloadOperations
}

func NewDokployWorkloadOwner(runtime Runtime) (*DokployWorkloadOwner, error) {
	api, err := nativehost.NewOSDokployWorkloadOperations(runtime.WorkspaceRoot)
	if err != nil {
		return nil, err
	}
	validator, ok := api.(nativehost.SelectedPaaSWorkloadObservationValidator)
	if !ok {
		return nil, errors.New("Dokploy requires the native semantic observation validator")
	}
	provider, err := NewDokployWorkloadOperations(runtime)
	if err != nil {
		return nil, err
	}
	return &DokployWorkloadOwner{runtime: runtime, api: api, validator: validator, provider: provider}, nil
}

func dokployProviderSelected() bool {
	return strings.TrimSpace(os.Getenv(DokployProviderModulesEnv)) != ""
}

// requireAPIOwnership refuses API ownership of a workload whose OpenTofu root
// belongs to the provider path.
func (o *DokployWorkloadOwner) requireAPIOwnership(d nativehost.SelectedPaaSWorkloadDeployment) error {
	relative, err := WorkloadRootRelativePath("stackkit-" + d.WorkloadRef + "-" + d.NodeRef)
	if err != nil {
		return err
	}
	root := filepath.Join(o.runtime.WorkspaceRoot, filepath.FromSlash(relative))
	if _, err := os.Lstat(filepath.Join(root, MarkerFile)); err == nil {
		marker, err := ReadRootMarker(root)
		if err != nil {
			return err
		}
		if marker.Execution == DokployProviderExecution {
			return errors.New("Dokploy provider owns this workload; retain its opt-in or explicitly transition it before API Apply")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (o *DokployWorkloadOwner) ApplyWorkload(ctx context.Context, d nativehost.SelectedPaaSWorkloadDeployment) (nativehost.SelectedPaaSApplyReceipt, error) {
	if dokployProviderSelected() {
		return o.provider.ApplyWorkload(ctx, d)
	}
	if err := o.requireAPIOwnership(d); err != nil {
		return nativehost.SelectedPaaSApplyReceipt{}, err
	}
	return o.api.ApplyWorkload(ctx, d)
}

func (o *DokployWorkloadOwner) ObserveWorkload(ctx context.Context, d nativehost.SelectedPaaSWorkloadDeployment) (nativehost.SelectedPaaSWorkloadObservation, error) {
	if dokployProviderSelected() {
		return o.provider.ObserveWorkload(ctx, d)
	}
	if err := o.requireAPIOwnership(d); err != nil {
		return nativehost.SelectedPaaSWorkloadObservation{}, err
	}
	return o.api.ObserveWorkload(ctx, d)
}

func (o *DokployWorkloadOwner) ValidateWorkloadObservation(d nativehost.SelectedPaaSWorkloadDeployment, v nativehost.SelectedPaaSWorkloadObservation) error {
	if dokployProviderSelected() {
		return o.provider.ValidateWorkloadObservation(d, v)
	}
	return o.validator.ValidateWorkloadObservation(d, v)
}
