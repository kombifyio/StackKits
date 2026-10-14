package nativehost

import (
	"context"
	"errors"

	"github.com/kombifyio/stackkits/internal/runtimeexecutor/fallback/platformdeploy"
)

// LoadOwnedCoolifyServiceConfig resolves only an existing signed node service.
func LoadOwnedCoolifyServiceConfig(ctx context.Context, workspace, project string) (platformdeploy.HTTPConfig, platformdeploy.DeploymentRef, error) {
	state, err := platformdeploy.LoadOwnerHostDeployment(workspace, project)
	if err != nil {
		return platformdeploy.HTTPConfig{}, platformdeploy.DeploymentRef{}, err
	}
	if err := platformdeploy.RequireHostDeploymentManager(state, "coolify"); err != nil {
		return platformdeploy.HTTPConfig{}, platformdeploy.DeploymentRef{}, err
	}
	ops, err := NewOSCoolifyWorkloadOperations(workspace)
	if err != nil {
		return platformdeploy.HTTPConfig{}, platformdeploy.DeploymentRef{}, err
	}
	cfg, _, err := ops.(*osCoolifyWorkloadOperations).configuration(ctx, SelectedPaaSWorkloadDeployment{WorkloadRef: state.WorkloadRef, InstanceRef: state.InstanceRef, ArtifactDigest: state.ArtifactDigest, SiteRef: state.SiteRef, NodeRef: state.NodeRef})
	if err != nil {
		return cfg, platformdeploy.DeploymentRef{}, err
	}
	if cfg.ServerID != state.ServerID {
		return cfg, platformdeploy.DeploymentRef{}, errors.New("Coolify service differs from the node server")
	}
	return cfg, state.Deployment, nil
}

func coolifyOwnedWorkload(ctx context.Context, workspace string, deployment SelectedPaaSWorkloadDeployment) (*osCoolifyWorkloadOperations, platformdeploy.HostDeployment, standaloneComposeProject, error) {
	ops, err := NewOSCoolifyWorkloadOperations(workspace)
	if err != nil {
		return nil, platformdeploy.HostDeployment{}, standaloneComposeProject{}, err
	}
	o := ops.(*osCoolifyWorkloadOperations)
	state, present, err := o.manager(deployment)
	if err != nil {
		return nil, state, standaloneComposeProject{}, err
	}
	if !present || state.ArtifactDigest != deployment.ArtifactDigest {
		return nil, state, standaloneComposeProject{}, errors.New("Coolify custody differs from the exact workload artifact")
	}
	project, err := o.prepared.prepareWithIdentityMutation(ctx, deployment, false)
	if err != nil {
		return nil, state, project, err
	}
	if err := o.prepared.verifyPersisted(project); err != nil {
		return nil, state, project, err
	}
	project.runtimeProject = state.Deployment.ExternalID
	return o, state, project, nil
}

// ObserveCoolifyContainerCustody verifies node-owned files and the signed
// service before observing exact component containers. It works while stopped.
func ObserveCoolifyContainerCustody(ctx context.Context, workspace string, deployment SelectedPaaSWorkloadDeployment) (map[string]string, error) {
	o, _, project, err := coolifyOwnedWorkload(ctx, workspace, deployment)
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
	if err := o.prepared.verifyPersisted(project); err != nil {
		return nil, err
	}
	return standaloneComposeContainerIdentities(project.bundle.Components, statuses, true)
}

// ControlCoolifyWorkloadComponent delegates quiescence to the retained manager.
// The caller supplies a declared component reference, never an API credential.
func ControlCoolifyWorkloadComponent(ctx context.Context, workspace string, deployment SelectedPaaSWorkloadDeployment, componentRef, action string) error {
	o, state, project, err := coolifyOwnedWorkload(ctx, workspace, deployment)
	if err != nil {
		return err
	}
	component, found := standaloneComposeComponent(project.bundle.Components, componentRef)
	if !found || component.Lifecycle != "daemon" {
		return errors.New("Coolify quiescence requires a declared daemon component")
	}
	cfg, _, err := o.configuration(ctx, deployment)
	if err != nil {
		return err
	}
	if cfg.ServerID != state.ServerID {
		return errors.New("Coolify component control server differs from node custody")
	}
	adapter := platformdeploy.NewCoolifyAdapter(cfg)
	components, err := adapter.ApplicationComponents(ctx, state.Deployment)
	if err != nil {
		return err
	}
	uuid := ""
	for _, candidate := range components {
		if candidate.Name == componentRef {
			if uuid != "" {
				return errors.New("Coolify component name is ambiguous")
			}
			uuid = candidate.UUID
		}
	}
	if uuid == "" {
		return errors.New("Coolify service has no exact declared component")
	}
	identities, err := ObserveCoolifyContainerCustody(ctx, workspace, deployment)
	if err != nil {
		return err
	}
	if err := adapter.ControlApplication(ctx, state.Deployment, uuid, action); err != nil {
		return err
	}
	return o.prepared.waitForComponentReadiness(ctx, project, func(statuses map[string]standaloneComposePS) (bool, error) {
		observed, exists := statuses[componentRef]
		if !exists || observed.ID != identities[componentRef] {
			return false, errors.New("Coolify component control changed the held container identity")
		}
		if observed.Image != component.ImageRef+"@"+component.ImageDigest {
			return false, errors.New("Coolify component control changed the held image")
		}
		if action == "stop" {
			return observed.State == "exited", nil
		}
		return observed.State == "running", nil
	})
}
