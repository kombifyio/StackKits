package nativehost

import (
	"context"
	"errors"
	"reflect"
	"time"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/backupexec"
	"github.com/kombifyio/stackkits/internal/docker"
	"github.com/kombifyio/stackkits/internal/localbackuppolicy"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/fallback/platformdeploy"
)

const runtipiBackupStopGrace = 20 * time.Second

// runtipiOwnedWorkload resolves the exact signed Runtipi app of one node
// workload. It reads node custody only; it neither logs in nor changes the app.
func runtipiOwnedWorkload(ctx context.Context, workspace string, deployment SelectedPaaSWorkloadDeployment) (*osRuntipiWorkloadOperations, platformdeploy.HostDeployment, standaloneComposeProject, error) {
	ops, err := NewOSRuntipiWorkloadOperations(workspace)
	if err != nil {
		return nil, platformdeploy.HostDeployment{}, standaloneComposeProject{}, err
	}
	o := ops.(*osRuntipiWorkloadOperations)
	name := "stackkit-" + deployment.WorkloadRef + "-" + deployment.NodeRef
	state, err := platformdeploy.LoadOwnerHostDeployment(workspace, name)
	if err != nil {
		return nil, state, standaloneComposeProject{}, err
	}
	if err := platformdeploy.RequireHostDeploymentManager(state, "runtipi"); err != nil {
		return nil, state, standaloneComposeProject{}, err
	}
	if deployment.RuntimeAdapter.ID != "runtipi" || state.WorkloadRef != deployment.WorkloadRef || state.InstanceRef != deployment.InstanceRef ||
		state.ArtifactDigest != deployment.ArtifactDigest || state.SiteRef != deployment.SiteRef || state.NodeRef != deployment.NodeRef ||
		state.Deployment.AppName != name || state.Deployment.ExternalID != name+":_user" {
		return nil, state, standaloneComposeProject{}, errors.New("Runtipi backup requires the exact signed node workload custody")
	}
	project, err := o.prepared.prepareWithIdentityMutation(ctx, deployment, false)
	if err != nil {
		return nil, state, project, err
	}
	if err := o.prepared.verifyPersisted(project); err != nil {
		return nil, state, project, err
	}
	project.runtimeProject = name + "__user"
	_, recipeDigest, err := runtipiNodeCompose(preparedWorkloadCompose(project), deployment.ArtifactDigest)
	if err != nil {
		return nil, state, project, err
	}
	// The containers must carry the authorized revision, also while stopped.
	if err := o.verifyRuntimeRevision(ctx, project, deployment.ArtifactDigest, recipeDigest); err != nil {
		return nil, state, project, err
	}
	return o, state, project, nil
}

// ObserveRuntipiContainerCustody returns the exact component containers of a
// signed Runtipi app. It works while writers are stopped.
func ObserveRuntipiContainerCustody(ctx context.Context, workspace string, deployment SelectedPaaSWorkloadDeployment) (map[string]string, error) {
	o, _, project, err := runtipiOwnedWorkload(ctx, workspace, deployment)
	if err != nil {
		return nil, err
	}
	raw, err := o.prepared.runner.Run(ctx, standaloneComposeArgs(project, "ps"), project.directory)
	if err != nil {
		return nil, err
	}
	statuses, err := parseStandaloneComposeStatuses(raw)
	if err != nil {
		return nil, err
	}
	return standaloneComposeContainerIdentities(project.bundle.Components, statuses, true)
}

// BindRuntipiBackupRuntime binds the compiler-owned external volumes of one
// Runtipi app. Writers are quiesced like every other adapter-owned workload:
// the exact held container is stopped and started again, so the app keeps
// its container generation and Runtipi keeps its app record.
func BindRuntipiBackupRuntime(ctx context.Context, workspace string, deployment SelectedPaaSWorkloadDeployment, graph localbackuppolicy.ApplicationRuntime, volumes []localbackuppolicy.ApplicationVolume) (backupexec.ApplicationRuntimeBinding, error) {
	if graph.RuntimeAdapterRef != "runtipi" || graph.WorkloadRef != deployment.WorkloadRef || graph.SiteRef != deployment.SiteRef || graph.NodeRef != deployment.NodeRef || graph.ComposeProject != "stackkit-"+deployment.WorkloadRef+"-"+deployment.NodeRef {
		return backupexec.ApplicationRuntimeBinding{}, errors.New("Runtipi source graph differs from the signed workload")
	}
	ids, err := ObserveRuntipiContainerCustody(ctx, workspace, deployment)
	if err != nil {
		return backupexec.ApplicationRuntimeBinding{}, err
	}
	physical := graph.ComposeProject + "__user"
	client := docker.NewLocalClient()
	names := map[string]string{}
	for _, volume := range volumes {
		if volume.ComposeProject != graph.ComposeProject || volume.SiteRef != graph.SiteRef || volume.NodeRef != graph.NodeRef || volume.WorkloadRef != graph.WorkloadRef || ids[volume.ComponentRef] == "" {
			return backupexec.ApplicationRuntimeBinding{}, errors.New("Runtipi source volume is outside the held component graph")
		}
		container, err := client.InspectContainer(ctx, ids[volume.ComponentRef])
		if err != nil {
			return backupexec.ApplicationRuntimeBinding{}, err
		}
		if container.Config.Labels["com.docker.compose.project"] != physical || container.Config.Labels["com.docker.compose.service"] != volume.ComponentRef {
			return backupexec.ApplicationRuntimeBinding{}, errors.New("Runtipi source container belongs to another app")
		}
		found := false
		for _, mount := range container.Mounts {
			if mount.Destination != volume.Target {
				continue
			}
			if found || mount.Type != "volume" || !mount.RW || mount.Name != volume.VolumeName {
				return backupexec.ApplicationRuntimeBinding{}, errors.New("Runtipi source mount differs from its held volume identity")
			}
			found = true
		}
		if !found {
			return backupexec.ApplicationRuntimeBinding{}, errors.New("Runtipi source has no exact writable volume")
		}
		names[volume.VolumeName] = volume.VolumeName
	}
	return backupexec.ApplicationRuntimeBinding{Runtime: graph, PhysicalProject: physical, VolumeNames: names, Control: func(ctx context.Context, component, action string) error {
		current, err := BindRuntipiBackupRuntime(ctx, workspace, deployment, graph, volumes)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(current.VolumeNames, names) {
			return errors.New("Runtipi source volume binding changed before writer control")
		}
		if now, err := ObserveRuntipiContainerCustody(ctx, workspace, deployment); err != nil || !reflect.DeepEqual(now, ids) {
			return errors.Join(errors.New("Runtipi source containers changed before writer control"), err)
		}
		return controlRuntipiBackupComponent(ctx, workspace, deployment, component, action)
	}}, nil
}

// Runtipi controls whole apps only and has no component control. A
// backup therefore stops and starts the exact held writer container; before a
// stop the installation login proves the node manager is still in custody.
func controlRuntipiBackupComponent(ctx context.Context, workspace string, deployment SelectedPaaSWorkloadDeployment, componentRef, action string) error {
	if action != "stop" && action != "start" {
		return errors.New("Runtipi backup permits only writer stop and start")
	}
	o, state, project, err := runtipiOwnedWorkload(ctx, workspace, deployment)
	if err != nil {
		return err
	}
	component, found := standaloneComposeComponent(project.bundle.Components, componentRef)
	if !found || component.Lifecycle != "daemon" {
		return errors.New("Runtipi backup requires a declared daemon component")
	}
	// A stop proves the manager first; a start must restore the writer even
	// when the Runtipi API is unavailable, so it relies on node custody alone.
	if action == "stop" {
		if _, installationID, err := o.configuration(ctx, deployment); err != nil {
			return err
		} else if installationID != state.ServerID {
			return errors.New("Runtipi backup control differs from the installed node instance")
		}
	}
	ids, err := ObserveRuntipiContainerCustody(ctx, workspace, deployment)
	if err != nil {
		return err
	}
	id := ids[componentRef]
	if id == "" {
		return errors.New("Runtipi backup component has no held container")
	}
	client := docker.NewLocalClient()
	if action == "stop" {
		err = client.StopContainerWithTimeout(ctx, id, runtipiBackupStopGrace)
	} else {
		err = client.StartContainer(ctx, id)
	}
	if err != nil {
		return err
	}
	return o.prepared.waitForComponentReadiness(ctx, project, func(statuses map[string]standaloneComposePS) (bool, error) {
		observed, exists := statuses[componentRef]
		if !exists || observed.ID != id {
			return false, errors.New("Runtipi backup control changed the held container identity")
		}
		if observed.Image != component.ImageRef+"@"+component.ImageDigest {
			return false, errors.New("Runtipi backup control changed the held image")
		}
		if action == "stop" {
			return observed.State == "exited", nil
		}
		if observed.State != "running" {
			return false, nil
		}
		return blockingStandaloneComposeReadiness([]architecturev2renderer.ApplicationDeliveryComponentDescriptor{component}, statuses)
	})
}
