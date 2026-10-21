package nativehost

import (
	"context"
	"errors"
	"net/http"

	"github.com/kombifyio/stackkits/internal/runtimeexecutor/fallback/platformdeploy"
)

// PreparedRuntipiApplicationSetupAdapter supplies the existing Files owner
// bootstrap over its own node session. It is deliberately not registered in
// applicationSetupAdapterRegistrations before current-pin qualification.
func PreparedRuntipiApplicationSetupAdapter() ApplicationSetupAdapter {
	return runtipiApplicationSetupAdapter{}
}

type runtipiApplicationSetupAdapter struct{}

func (runtipiApplicationSetupAdapter) SupportsNativeAction(action string) bool {
	return action == "cloudreve-owner-bootstrap"
}

func (runtipiApplicationSetupAdapter) WithHTTP(ctx context.Context, workspace string, deployment SelectedPaaSWorkloadDeployment, run func(*http.Client, string) error) error {
	if deployment.RuntimeAdapter.ID != "runtipi" || deployment.RuntimeAdapter.ProviderRef != "stackkits-runtipi" || deployment.RuntimeAdapter.ModuleRef != "stackkits-runtipi-runtime" || deployment.WorkloadRef != "files" {
		return errors.New("prepared Runtipi setup requires its exact Files adapter")
	}
	operations, err := NewOSRuntipiWorkloadOperations(workspace)
	if err != nil {
		return err
	}
	owner := operations.(*osRuntipiWorkloadOperations)
	state, err := platformdeploy.LoadOwnerHostDeployment(owner.workspace, "stackkit-"+deployment.WorkloadRef+"-"+deployment.NodeRef)
	if err != nil {
		return err
	}
	if err := platformdeploy.RequireHostDeploymentManager(state, "runtipi"); err != nil {
		return err
	}
	if state.InstanceRef != deployment.InstanceRef || state.SiteRef != deployment.SiteRef || state.NodeRef != deployment.NodeRef || state.ArtifactDigest != deployment.ArtifactDigest {
		return errors.New("Runtipi setup differs from its signed node workload")
	}
	adapter, installationID, err := owner.configuration(ctx, deployment)
	if err != nil {
		return err
	}
	if state.ServerID != installationID {
		return errors.New("Runtipi setup differs from its installed node instance")
	}
	if _, err := adapter.ObserveDeployment(ctx, state.Deployment); err != nil {
		return err
	}
	return owner.prepared.withRuntimeProjectHTTP(ctx, deployment, run, true, state.Deployment.AppName+"__user")
}
