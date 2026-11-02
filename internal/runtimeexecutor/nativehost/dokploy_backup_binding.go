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

// dokployOwnedWorkload returns the exact signed Dokploy workload and its node
// project bound to the service's physical Compose project.
func dokployOwnedWorkload(ctx context.Context, workspace string, deployment SelectedPaaSWorkloadDeployment) (*osDokployWorkloadOperations, platformdeploy.HostDeployment, standaloneComposeProject, error) {
	ops, err := NewOSDokployWorkloadOperations(workspace)
	if err != nil {
		return nil, platformdeploy.HostDeployment{}, standaloneComposeProject{}, err
	}
	o := ops.(*osDokployWorkloadOperations)
	state, present, err := o.manager(deployment)
	if err != nil {
		return nil, state, standaloneComposeProject{}, err
	}
	if !present || state.Signature == nil || deployment.RuntimeAdapter.ID != "dokploy" || state.ArtifactDigest != deployment.ArtifactDigest {
		return nil, state, standaloneComposeProject{}, errors.New("Dokploy custody differs from the exact workload artifact")
	}
	project, err := o.prepared.prepareWithIdentityMutation(ctx, deployment, false)
	if err != nil {
		return nil, state, project, err
	}
	if err := o.prepared.verifyPersisted(project); err != nil {
		return nil, state, project, err
	}
	project.runtimeProject = state.RuntimeProject
	return o, state, project, nil
}

// ObserveDokployContainerCustody observes the exact component containers of
// the signed service. It works while writers are stopped.
func ObserveDokployContainerCustody(ctx context.Context, workspace string, deployment SelectedPaaSWorkloadDeployment) (map[string]string, error) {
	o, _, project, err := dokployOwnedWorkload(ctx, workspace, deployment)
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

// controlDokployWorkloadComponent stops or starts one declared daemon
// container. Dokploy keeps no runtime state of its own for a running
// compose service, so the Docker engine is the controller, as for Compose.
func controlDokployWorkloadComponent(ctx context.Context, workspace string, deployment SelectedPaaSWorkloadDeployment, componentRef, action string) error {
	if action != "stop" && action != "start" {
		return errors.New("Dokploy quiescence permits only writer stop and start")
	}
	o, state, project, err := dokployOwnedWorkload(ctx, workspace, deployment)
	if err != nil {
		return err
	}
	component, found := standaloneComposeComponent(project.bundle.Components, componentRef)
	if !found || component.Lifecycle != "daemon" {
		return errors.New("Dokploy quiescence requires a declared daemon component")
	}
	ids, err := ObserveDokployContainerCustody(ctx, workspace, deployment)
	if err != nil {
		return err
	}
	if _, err := o.prepared.runner.Run(ctx, []string{action, ids[componentRef]}, project.directory); err != nil {
		return err
	}
	if err := o.prepared.waitForComponentReadiness(ctx, project, func(statuses map[string]standaloneComposePS) (bool, error) {
		observed, exists := statuses[componentRef]
		if !exists || observed.ID != ids[componentRef] || observed.Image != component.ImageRef+"@"+component.ImageDigest {
			return false, errors.New("Dokploy control changed the held component identity")
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
	_, current, _, err := dokployOwnedWorkload(ctx, workspace, deployment)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(current, state) {
		return errors.New("Dokploy node custody changed during component control")
	}
	return nil
}

// BindDokployBackupRuntime binds the held graph to the service's physical
// Compose project. Data stays in node-named external volumes, so every volume
// keeps its compiler-owned name; Kopia keeps backup and restore custody.
func BindDokployBackupRuntime(ctx context.Context, workspace string, deployment SelectedPaaSWorkloadDeployment, graph localbackuppolicy.ApplicationRuntime, volumes []localbackuppolicy.ApplicationVolume) (backupexec.ApplicationRuntimeBinding, error) {
	if graph.RuntimeAdapterRef != "dokploy" || graph.WorkloadRef != deployment.WorkloadRef || graph.SiteRef != deployment.SiteRef || graph.NodeRef != deployment.NodeRef || graph.ComposeProject != "stackkit-"+deployment.WorkloadRef+"-"+deployment.NodeRef {
		return backupexec.ApplicationRuntimeBinding{}, errors.New("Dokploy source graph differs from the signed workload")
	}
	_, state, _, err := dokployOwnedWorkload(ctx, workspace, deployment)
	if err != nil {
		return backupexec.ApplicationRuntimeBinding{}, err
	}
	ids, err := ObserveDokployContainerCustody(ctx, workspace, deployment)
	if err != nil {
		return backupexec.ApplicationRuntimeBinding{}, err
	}
	client := docker.NewLocalClient()
	names := map[string]string{}
	for _, volume := range volumes {
		if volume.ComposeProject != graph.ComposeProject || volume.SiteRef != graph.SiteRef || volume.NodeRef != graph.NodeRef || volume.WorkloadRef != graph.WorkloadRef || ids[volume.ComponentRef] == "" {
			return backupexec.ApplicationRuntimeBinding{}, errors.New("Dokploy source volume is outside the held component graph")
		}
		container, err := client.InspectContainer(ctx, ids[volume.ComponentRef])
		if err != nil {
			return backupexec.ApplicationRuntimeBinding{}, err
		}
		if container.Config.Labels["com.docker.compose.project"] != state.RuntimeProject || container.Config.Labels["com.docker.compose.service"] != volume.ComponentRef {
			return backupexec.ApplicationRuntimeBinding{}, errors.New("Dokploy source container belongs to another runtime")
		}
		found := false
		for _, mount := range container.Mounts {
			if mount.Destination != volume.Target {
				continue
			}
			if found || mount.Type != "volume" || !mount.RW || mount.Name != volume.VolumeName {
				return backupexec.ApplicationRuntimeBinding{}, errors.New("Dokploy source mount differs from its held volume identity")
			}
			found = true
		}
		if !found {
			return backupexec.ApplicationRuntimeBinding{}, errors.New("Dokploy source has no exact writable volume")
		}
		names[volume.VolumeName] = volume.VolumeName
	}
	return backupexec.ApplicationRuntimeBinding{Runtime: graph, PhysicalProject: state.RuntimeProject, VolumeNames: names, Control: func(ctx context.Context, component, action string) error {
		if _, err := BindDokployBackupRuntime(ctx, workspace, deployment, graph, volumes); err != nil {
			return err
		}
		return controlDokployWorkloadComponent(ctx, workspace, deployment, component, action)
	}}, nil
}
