package nativehost

import (
	"context"
	"errors"
	"fmt"

	"github.com/kombifyio/stackkits/internal/docker"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/fallback/platformdeploy"
	"github.com/kombifyio/stackkits/internal/workloadremoval"
)

const (
	runtipiRemovalJournalFile    = "runtipi-removal.json"
	runtipiRemovalJournalVersion = "stackkit.runtipi-workload-removal/v1"
	runtipiAdapterModuleRef      = "stackkits-runtipi-runtime"
)

// RemoveRuntipiWorkload implements the canonical Owner-signed removal of one
// Runtipi custom app. Runtipi uninstalls the app and its recipe; the
// compiler-owned external volumes stay until the Owner asks to delete data.
func RemoveRuntipiWorkload(ctx context.Context, workspace string, request workloadremoval.Request) (workloadremoval.Result, error) {
	operations, err := NewOSRuntipiWorkloadOperations(workspace)
	if err != nil {
		return workloadremoval.Result{}, &RemovalProgressError{Err: err}
	}
	o := operations.(*osRuntipiWorkloadOperations)
	return removePaaSWorkload(ctx, workspace, request, paasRemovalOwner{
		label: "Runtipi", adapterRef: "runtipi", moduleRef: runtipiAdapterModuleRef,
		journalFile: runtipiRemovalJournalFile, journalVersion: runtipiRemovalJournalVersion,
		prepared: o.prepared,
		custody: func(deployment SelectedPaaSWorkloadDeployment) (platformdeploy.HostDeployment, bool, error) {
			name := "stackkit-" + deployment.WorkloadRef + "-" + deployment.NodeRef
			present, err := platformdeploy.HasOwnerHostDeployment(workspace, name)
			if err != nil || !present {
				return platformdeploy.HostDeployment{}, false, err
			}
			state, err := platformdeploy.LoadOwnerHostDeployment(workspace, name)
			if err != nil {
				return state, false, err
			}
			if err := platformdeploy.RequireHostDeploymentManager(state, "runtipi"); err != nil {
				return state, false, err
			}
			if state.WorkloadRef != deployment.WorkloadRef || state.InstanceRef != deployment.InstanceRef || state.SiteRef != deployment.SiteRef || state.NodeRef != deployment.NodeRef || state.Deployment.ExternalID != name+":_user" {
				return state, false, errors.New("Runtipi workload custody differs from the exact node placement")
			}
			return state, true, nil
		},
		runtimeProject: func(state platformdeploy.HostDeployment) string { return state.Deployment.AppName + "__user" },
		connect: func(ctx context.Context, deployment SelectedPaaSWorkloadDeployment) (paasRemovalManager, error) {
			adapter, installationID, err := o.configuration(ctx, deployment)
			if err != nil {
				return nil, err
			}
			return runtipiRemovalManager{serverID: installationID, adapter: adapter}, nil
		},
		guard: func(ctx context.Context, deployment SelectedPaaSWorkloadDeployment, manager paasRemovalManager, journal paasRemovalJournal) error {
			return guardRuntipiUninstall(ctx, o, deployment, manager.(runtipiRemovalManager).adapter, journal)
		},
	})
}

// Runtipi 4.10.2 uninstalls with `docker compose down -v`, which deletes every
// volume its installed recipe does not mark external. Before each uninstall
// call the installed recipe must therefore equal the authorized one (all data
// volumes external) and every mounted volume must be a journaled one.
func guardRuntipiUninstall(ctx context.Context, o *osRuntipiWorkloadOperations, deployment SelectedPaaSWorkloadDeployment, adapter *platformdeploy.RuntipiAdapter, journal paasRemovalJournal) error {
	project, err := o.prepared.prepareWithIdentityMutation(ctx, deployment, false)
	if err != nil {
		return err
	}
	compose, _, err := runtipiNodeCompose(preparedWorkloadCompose(project), deployment.ArtifactDigest)
	if err != nil {
		return err
	}
	manifest := platformdeploy.AppManifest{Name: project.name, ServiceKey: project.entry.ID, Port: project.entry.HealthPort, ComposeYAML: string(compose)}
	matches, err := adapter.ComposeMatches(ctx, journal.Deployment.Deployment, manifest)
	if err != nil {
		return err
	}
	if !matches {
		return errors.New("Runtipi installed recipe differs from the authorized recipe; run stackkit apply before removing it")
	}
	held := map[string]bool{}
	for _, volume := range journal.Volumes {
		held[volume.Name] = true
	}
	client := docker.NewLocalClient()
	for component, id := range journal.Containers {
		container, err := client.InspectContainer(ctx, id)
		if err != nil {
			absent, absentErr := (standaloneComposeRemover{ops: o.prepared}).containerIDAbsent(ctx, id)
			if absentErr == nil && absent {
				continue
			}
			return err
		}
		for _, mount := range container.Mounts {
			if mount.Type == "volume" && !held[mount.Name] {
				return fmt.Errorf("Runtipi component %q mounts volume %q outside the removal journal; its uninstall would delete it", component, mount.Name)
			}
		}
	}
	return nil
}

type runtipiRemovalManager struct {
	serverID string
	adapter  *platformdeploy.RuntipiAdapter
}

func (m runtipiRemovalManager) ServerID() string { return m.serverID }

func (m runtipiRemovalManager) AppAbsent(ctx context.Context, ref platformdeploy.DeploymentRef) (bool, error) {
	return m.adapter.AppAbsent(ctx, ref)
}

// Runtipi's own pre-update backups of the app follow the data disposition.
func (m runtipiRemovalManager) RemoveApp(ctx context.Context, ref platformdeploy.DeploymentRef, disposition string) error {
	return m.adapter.Uninstall(ctx, ref, disposition == workloadremoval.DataDispositionDelete)
}
