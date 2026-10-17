package nativehost

import (
	"context"
	"errors"
	"net/http"
	"slices"
)

// platformSetupActions are the owner actions whose whole effect is the local
// application API session; actions that also need standalone-only host
// operations (media import, connectors, model pulls) stay unsupported.
var platformSetupActions = []string{"immich-owner-bootstrap", "cloudreve-owner-bootstrap", "vault-owner-invite"}

// platformApplicationSetupAdapter gives a Komodo- or Coolify-managed workload
// the same native setup as standalone Compose: both managers run the exact
// generated bundle, so the loopback session and its per-request identity
// checks are unchanged. The verified manager custody selects only the physical
// Compose project; no manager API or credential takes part in a request.
type platformApplicationSetupAdapter struct{ adapterRef string }

func (platformApplicationSetupAdapter) SupportsNativeAction(action string) bool {
	return slices.Contains(platformSetupActions, action)
}

func (a platformApplicationSetupAdapter) WithHTTP(ctx context.Context, workspace string, deployment SelectedPaaSWorkloadDeployment, run func(*http.Client, string) error) error {
	if deployment.RuntimeAdapter.ID != a.adapterRef {
		return errors.New("application setup adapter differs from the selected workload manager")
	}
	switch a.adapterRef {
	case "komodo":
		// Komodo keeps the node project name for its stack.
		o, _, _, err := komodoOwnedWorkload(ctx, workspace, deployment)
		if err != nil {
			return err
		}
		return o.prepared.withRuntimeProjectHTTP(ctx, deployment, run, true, "")
	case "coolify":
		o, state, _, err := coolifyOwnedWorkload(ctx, workspace, deployment)
		if err != nil {
			return err
		}
		if state.Deployment.ExternalID == "" {
			return errors.New("Coolify setup requires the signed service identity")
		}
		return o.prepared.withRuntimeProjectHTTP(ctx, deployment, run, true, state.Deployment.ExternalID)
	}
	return errors.New("runtime adapter has no executable application setup transport")
}
