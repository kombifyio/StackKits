package nativehost

import (
	"context"
	"errors"
	"reflect"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/backupexec"
	"github.com/kombifyio/stackkits/internal/docker"
	"github.com/kombifyio/stackkits/internal/localbackuppolicy"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/fallback/platformdeploy"
)

func komodoOwnedWorkload(ctx context.Context, workspace string, deployment SelectedPaaSWorkloadDeployment) (*osKomodoWorkloadOperations, platformdeploy.HostDeployment, standaloneComposeProject, error) {
	ops, err := NewOSKomodoWorkloadOperations(workspace)
	if err != nil {
		return nil, platformdeploy.HostDeployment{}, standaloneComposeProject{}, err
	}
	o := ops.(*osKomodoWorkloadOperations)
	name := "stackkit-" + deployment.WorkloadRef + "-" + deployment.NodeRef
	state, err := platformdeploy.LoadOwnerHostDeployment(workspace, name)
	if err != nil {
		return nil, state, standaloneComposeProject{}, err
	}
	if err := platformdeploy.RequireHostDeploymentManager(state, "komodo"); err != nil {
		return nil, state, standaloneComposeProject{}, err
	}
	if state.Signature == nil || state.Manager != "komodo" || deployment.RuntimeAdapter.ID != "komodo" ||
		state.WorkloadRef != deployment.WorkloadRef || state.InstanceRef != deployment.InstanceRef ||
		state.ArtifactDigest != deployment.ArtifactDigest || state.SiteRef != deployment.SiteRef || state.NodeRef != deployment.NodeRef || state.Deployment.AppName != name {
		return nil, state, standaloneComposeProject{}, errors.New("Komodo backup requires the exact signed node workload custody")
	}
	cfg, err := platformdeploy.LoadOwnerKomodoConfigForServer(ctx, workspace, state.ServerID)
	if err != nil {
		return nil, state, standaloneComposeProject{}, err
	}
	if cfg.ServerID != state.ServerID {
		return nil, state, standaloneComposeProject{}, errors.New("Komodo backup controller differs from node custody")
	}
	project, err := o.prepared.prepareWithIdentityMutation(ctx, deployment, false)
	if err != nil {
		return nil, state, project, err
	}
	if err := o.prepared.verifyPersisted(project); err != nil {
		return nil, state, project, err
	}
	// Attachment and recipe identity remain observable while writers are stopped.
	// A running-only health observation would prevent safe resume after quiescence.
	server, err := platformdeploy.NewKomodoAdapter(cfg).HostStackAttachment(ctx, state.Deployment, project.directory)
	if err != nil {
		return nil, state, project, err
	}
	if server != state.ServerID {
		return nil, state, project, errors.New("Komodo backup stack is detached or attached to another node")
	}
	return o, state, project, nil
}

// ObserveKomodoContainerCustody accepts stopped writers without modifying the
// node's identity configuration or transferring control from Komodo.
func ObserveKomodoContainerCustody(ctx context.Context, workspace string, deployment SelectedPaaSWorkloadDeployment) (map[string]string, error) {
	o, _, _, err := komodoOwnedWorkload(ctx, workspace, deployment)
	if err != nil {
		return nil, err
	}
	return o.prepared.observeContainerCustody(ctx, deployment)
}

func controlKomodoBackupComponent(ctx context.Context, workspace string, deployment SelectedPaaSWorkloadDeployment, componentRef, action string) error {
	if action != "stop" && action != "start" {
		return errors.New("Komodo backup permits only writer stop and start")
	}
	o, state, project, err := komodoOwnedWorkload(ctx, workspace, deployment)
	if err != nil {
		return err
	}
	component, found := standaloneComposeComponent(project.bundle.Components, componentRef)
	if !found || component.Lifecycle != "daemon" {
		return errors.New("Komodo backup requires a declared daemon component")
	}
	ids, err := o.prepared.observeContainerCustody(ctx, deployment)
	if err != nil {
		return err
	}
	cfg, err := platformdeploy.LoadOwnerKomodoConfigForServer(ctx, workspace, state.ServerID)
	if err != nil {
		return err
	}
	if _, err := platformdeploy.NewKomodoAdapter(cfg).ControlStack(ctx, state.Deployment, action, []string{componentRef}); err != nil {
		return err
	}
	if err := o.prepared.waitForComponentReadiness(ctx, project, func(statuses map[string]standaloneComposePS) (bool, error) {
		observed, exists := statuses[componentRef]
		if !exists || observed.ID != ids[componentRef] || observed.Image != component.ImageRef+"@"+component.ImageDigest {
			return false, errors.New("Komodo backup control changed the held component identity")
		}
		if action == "stop" {
			return observed.State == "exited", nil
		}
		if observed.State != "running" {
			return false, nil
		}
		return blockingStandaloneComposeReadiness([]architecturev2renderer.ApplicationDeliveryComponentDescriptor{component}, statuses)
	}); err != nil {
		return err
	}
	_, current, _, err := komodoOwnedWorkload(ctx, workspace, deployment)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(current, state) {
		return errors.New("Komodo node custody changed during backup control")
	}
	return nil
}

// BindKomodoBackupRuntime keeps the compiler-owned project and volume names.
// The binding delegates all writer controls to the signed Komodo controller;
// Kopia and the existing node lifecycle retain backup and restore custody.
func BindKomodoBackupRuntime(ctx context.Context, workspace string, deployment SelectedPaaSWorkloadDeployment, graph localbackuppolicy.ApplicationRuntime, volumes []localbackuppolicy.ApplicationVolume) (backupexec.ApplicationRuntimeBinding, error) {
	if graph.RuntimeAdapterRef != "komodo" || graph.WorkloadRef != deployment.WorkloadRef || graph.SiteRef != deployment.SiteRef || graph.NodeRef != deployment.NodeRef || graph.ComposeProject != "stackkit-"+deployment.WorkloadRef+"-"+deployment.NodeRef {
		return backupexec.ApplicationRuntimeBinding{}, errors.New("Komodo source graph differs from the signed workload")
	}
	ids, err := ObserveKomodoContainerCustody(ctx, workspace, deployment)
	if err != nil {
		return backupexec.ApplicationRuntimeBinding{}, err
	}
	client := docker.NewLocalClient()
	names := map[string]string{}
	for _, volume := range volumes {
		if volume.ComposeProject != graph.ComposeProject || volume.SiteRef != graph.SiteRef || volume.NodeRef != graph.NodeRef || volume.WorkloadRef != graph.WorkloadRef || ids[volume.ComponentRef] == "" {
			return backupexec.ApplicationRuntimeBinding{}, errors.New("Komodo source volume is outside the held component graph")
		}
		container, err := client.InspectContainer(ctx, ids[volume.ComponentRef])
		if err != nil {
			return backupexec.ApplicationRuntimeBinding{}, err
		}
		if container.Config.Labels["com.docker.compose.project"] != graph.ComposeProject || container.Config.Labels["com.docker.compose.service"] != volume.ComponentRef {
			return backupexec.ApplicationRuntimeBinding{}, errors.New("Komodo source container belongs to another workload")
		}
		found := false
		for _, mount := range container.Mounts {
			if mount.Destination != volume.Target {
				continue
			}
			if found || mount.Type != "volume" || !mount.RW || mount.Name != volume.VolumeName {
				return backupexec.ApplicationRuntimeBinding{}, errors.New("Komodo source mount differs from its held volume identity")
			}
			found = true
		}
		if !found {
			return backupexec.ApplicationRuntimeBinding{}, errors.New("Komodo source has no exact writable volume")
		}
		names[volume.VolumeName] = volume.VolumeName
	}
	return backupexec.ApplicationRuntimeBinding{Runtime: graph, PhysicalProject: graph.ComposeProject, VolumeNames: names, Control: func(ctx context.Context, component, action string) error {
		if _, err := BindKomodoBackupRuntime(ctx, workspace, deployment, graph, volumes); err != nil {
			return err
		}
		return controlKomodoBackupComponent(ctx, workspace, deployment, component, action)
	}}, nil
}
