package nativehost

import (
	"context"
	"errors"
	"reflect"

	"github.com/kombifyio/stackkits/internal/backupexec"
	"github.com/kombifyio/stackkits/internal/docker"
	"github.com/kombifyio/stackkits/internal/localbackuppolicy"
)

// BindCoolifyBackupRuntime resolves only the mounted data of the exact signed
// node service. Callers already hold the verified Plan/Apply source selection.
func BindCoolifyBackupRuntime(ctx context.Context, workspace string, deployment SelectedPaaSWorkloadDeployment, graph localbackuppolicy.ApplicationRuntime, volumes []localbackuppolicy.ApplicationVolume) (backupexec.ApplicationRuntimeBinding, error) {
	_, state, _, err := coolifyOwnedWorkload(ctx, workspace, deployment)
	if err != nil {
		return backupexec.ApplicationRuntimeBinding{}, err
	}
	if graph.RuntimeAdapterRef != "coolify" || graph.WorkloadRef != deployment.WorkloadRef || graph.SiteRef != deployment.SiteRef || graph.NodeRef != deployment.NodeRef || graph.ComposeProject != "stackkit-"+deployment.WorkloadRef+"-"+deployment.NodeRef {
		return backupexec.ApplicationRuntimeBinding{}, errors.New("Coolify source graph differs from the exact node workload")
	}
	ids, err := ObserveCoolifyContainerCustody(ctx, workspace, deployment)
	if err != nil {
		return backupexec.ApplicationRuntimeBinding{}, err
	}
	client := docker.NewLocalClient()
	names := map[string]string{}
	for _, volume := range volumes {
		if volume.ComposeProject != graph.ComposeProject || volume.SiteRef != graph.SiteRef || volume.NodeRef != graph.NodeRef || volume.WorkloadRef != graph.WorkloadRef {
			return backupexec.ApplicationRuntimeBinding{}, errors.New("Coolify source volume is outside the held graph")
		}
		id := ids[volume.ComponentRef]
		if id == "" {
			return backupexec.ApplicationRuntimeBinding{}, errors.New("Coolify source component has no held container")
		}
		container, err := client.InspectContainer(ctx, id)
		if err != nil {
			return backupexec.ApplicationRuntimeBinding{}, err
		}
		if container.Config.Labels["com.docker.compose.project"] != state.Deployment.ExternalID || container.Config.Labels["com.docker.compose.service"] != volume.ComponentRef {
			return backupexec.ApplicationRuntimeBinding{}, errors.New("Coolify source container belongs to another runtime")
		}
		found := ""
		for _, mount := range container.Mounts {
			if mount.Type != "volume" || mount.Destination != volume.Target || !mount.RW {
				continue
			}
			if mount.Name != volume.VolumeName && mount.Name != state.Deployment.ExternalID+"_"+volume.LogicalName {
				return backupexec.ApplicationRuntimeBinding{}, errors.New("Coolify source mount is outside its node-owned volume names")
			}
			if found != "" {
				return backupexec.ApplicationRuntimeBinding{}, errors.New("Coolify source mount is ambiguous")
			}
			found = mount.Name
		}
		if found == "" {
			return backupexec.ApplicationRuntimeBinding{}, errors.New("Coolify source has no exact writable mounted volume")
		}
		names[volume.VolumeName] = found
	}
	return backupexec.ApplicationRuntimeBinding{Runtime: graph, PhysicalProject: state.Deployment.ExternalID, VolumeNames: names, Control: func(ctx context.Context, component, action string) error {
		current, err := BindCoolifyBackupRuntimeObservation(ctx, workspace, deployment, graph, volumes)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(current, names) {
			return errors.New("Coolify source volume binding changed before manager control")
		}
		return ControlCoolifyWorkloadComponent(ctx, workspace, deployment, component, action)
	}}, nil
}

// The observation is the same source admission without calling its controls.
func BindCoolifyBackupRuntimeObservation(ctx context.Context, workspace string, deployment SelectedPaaSWorkloadDeployment, graph localbackuppolicy.ApplicationRuntime, volumes []localbackuppolicy.ApplicationVolume) (map[string]string, error) {
	binding, err := BindCoolifyBackupRuntime(ctx, workspace, deployment, graph, volumes)
	return binding.VolumeNames, err
}
