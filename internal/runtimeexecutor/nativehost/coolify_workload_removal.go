package nativehost

import (
	"context"
	"errors"

	"github.com/kombifyio/stackkits/internal/runtimeexecutor/fallback/platformdeploy"
	"github.com/kombifyio/stackkits/internal/workloadremoval"
)

const (
	coolifyRemovalJournalFile    = "coolify-removal.json"
	coolifyRemovalJournalVersion = "stackkit.coolify-workload-removal/v1"
	coolifyAdapterRef            = "coolify"
	coolifyAdapterModuleRef      = "stackkits-coolify-runtime"
)

// RemoveCoolifyWorkload implements the canonical Owner-signed removal of one
// Coolify-managed application. Coolify deletes the service and its stored
// configuration but never its data; the curated Coolify project stays.
func RemoveCoolifyWorkload(ctx context.Context, workspace string, request workloadremoval.Request) (workloadremoval.Result, error) {
	operations, err := NewOSCoolifyWorkloadOperations(workspace)
	if err != nil {
		return workloadremoval.Result{}, &RemovalProgressError{Err: err}
	}
	o := operations.(*osCoolifyWorkloadOperations)
	return removePaaSWorkload(ctx, workspace, request, paasRemovalOwner{
		label: "Coolify", adapterRef: coolifyAdapterRef, moduleRef: coolifyAdapterModuleRef,
		journalFile: coolifyRemovalJournalFile, journalVersion: coolifyRemovalJournalVersion,
		prepared: o.prepared,
		custody:  o.manager,
		// The opt-in Coolify provider owns its service through an OpenTofu root
		// and encrypted state; deleting the service here would leave it behind.
		refuse: func(state platformdeploy.HostDeployment) error {
			if state.RecipeDigest != "" {
				return errors.New("Coolify workload removal does not admit a provider-owned service; its OpenTofu provider root owns it")
			}
			return nil
		},
		runtimeProject: func(state platformdeploy.HostDeployment) string { return state.Deployment.ExternalID },
		connect: func(ctx context.Context, deployment SelectedPaaSWorkloadDeployment) (paasRemovalManager, error) {
			cfg, _, err := o.configuration(ctx, deployment)
			if err != nil {
				return nil, err
			}
			return coolifyRemovalManager{serverID: cfg.ServerID, adapter: platformdeploy.NewCoolifyAdapter(cfg)}, nil
		},
	})
}

type coolifyRemovalManager struct {
	serverID string
	adapter  *platformdeploy.CoolifyAdapter
}

func (m coolifyRemovalManager) ServerID() string { return m.serverID }

func (m coolifyRemovalManager) AppAbsent(ctx context.Context, ref platformdeploy.DeploymentRef) (bool, error) {
	return m.adapter.ServiceAbsent(ctx, ref)
}

func (m coolifyRemovalManager) RemoveApp(ctx context.Context, ref platformdeploy.DeploymentRef, _ string) error {
	return m.adapter.DeleteServiceKeepingData(ctx, ref)
}
