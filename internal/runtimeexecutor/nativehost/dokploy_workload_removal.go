package nativehost

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/kombifyio/stackkits/internal/runtimeexecutor/fallback/platformdeploy"
	"github.com/kombifyio/stackkits/internal/workloadremoval"
)

// RemoveDokployWorkload implements the canonical Owner-signed removal of one
// Dokploy-managed application. Dokploy deletes the compose service without
// its volumes; StackKits applies the requested data disposition. Dokploy, its
// project and other services stay untouched.
func RemoveDokployWorkload(ctx context.Context, workspace string, request workloadremoval.Request) (workloadremoval.Result, error) {
	operations, err := NewOSDokployWorkloadOperations(workspace)
	if err != nil {
		return workloadremoval.Result{}, &RemovalProgressError{Err: err}
	}
	o := operations.(*osDokployWorkloadOperations)
	return removePaaSWorkload(ctx, workspace, request, paasRemovalOwner{
		label: "Dokploy", adapterRef: "dokploy", moduleRef: "stackkits-dokploy-runtime",
		journalFile: "dokploy-removal.json", journalVersion: "stackkit.dokploy-workload-removal/v1",
		prepared: o.prepared,
		custody:  o.manager,
		// The held-ID provider path owns its service through an OpenTofu root
		// and encrypted state; deleting the service here would leave it behind.
		refuse: func(state platformdeploy.HostDeployment) error {
			marker := filepath.Join(o.workspace, ".stackkit", "runtime", "applications", "stackkit-"+state.WorkloadRef+"-"+state.NodeRef, "opentofu", "stackkit-root.json")
			if _, err := os.Lstat(marker); err == nil {
				return errors.New("Dokploy workload removal does not admit a provider-owned service; its OpenTofu provider root owns it")
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
			return nil
		},
		runtimeProject: func(state platformdeploy.HostDeployment) string { return state.RuntimeProject },
		// compose.delete removes the suffixed physical project; the service must
		// still be the journaled one before every call.
		guard: func(ctx context.Context, _ SelectedPaaSWorkloadDeployment, manager paasRemovalManager, journal paasRemovalJournal) error {
			observed, err := manager.(dokployRemovalManager).adapter.ComposeState(ctx, journal.Deployment.Deployment)
			if err != nil {
				return err
			}
			if observed.AppName != journal.RuntimeProject {
				return errors.New("Dokploy service differs from the journaled physical project")
			}
			return nil
		},
		connect: func(ctx context.Context, deployment SelectedPaaSWorkloadDeployment) (paasRemovalManager, error) {
			cfg, err := o.configuration(ctx, deployment)
			if err != nil {
				return nil, err
			}
			state, present, err := o.manager(deployment)
			if err != nil {
				return nil, err
			}
			if present && state.EnvironmentID != cfg.EnvironmentID {
				return nil, errors.New("Dokploy removal environment differs from node custody")
			}
			return dokployRemovalManager{adapter: platformdeploy.NewDokployAdapter(cfg)}, nil
		},
	})
}

// dokployRemovalManager deletes on the node's own Dokploy server, whose
// placement custody records an empty remote server ID.
type dokployRemovalManager struct {
	adapter *platformdeploy.DokployAdapter
}

func (dokployRemovalManager) ServerID() string { return "" }

func (m dokployRemovalManager) AppAbsent(ctx context.Context, ref platformdeploy.DeploymentRef) (bool, error) {
	return m.adapter.ComposeAbsent(ctx, ref)
}

func (m dokployRemovalManager) RemoveApp(ctx context.Context, ref platformdeploy.DeploymentRef, _ string) error {
	return m.adapter.Delete(ctx, ref, false)
}
