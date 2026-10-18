package nativehost

import (
	"context"
	"errors"
	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/backuplifecycle"
	"github.com/kombifyio/stackkits/internal/docker"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/fallback/platformdeploy"
	"strings"
)

// NewCoolifyBackupControlDriver reuses the native terminal completion seam for
// an existing curated service. Its signed HostDeployment remains its custody.
func NewCoolifyBackupControlDriver(workspace string, document architecturev2renderer.MarketplaceCoolifyDocument, deployment SelectedPaaSWorkloadDeployment, component, containerID, operation string) (backuplifecycle.RuntimeControlDriver, error) {
	if component == "" || containerID == "" || operation == "" {
		return nil, errors.New("curated control identity is incomplete")
	}
	client, err := docker.NewLocalExecHandleClient()
	if err != nil {
		return nil, err
	}
	resolve := func(ctx context.Context, action string) (string, []string, error) {
		if action != "stop" && action != "start" && action != "rollback-stop" && action != "rollback-start" {
			return "", nil, errors.New("unsupported curated control")
		}
		_, state, _, err := coolifyOwnedWorkload(ctx, workspace, deployment)
		if err != nil {
			return "", nil, err
		}
		ids, err := ObserveCoolifyContainerCustody(ctx, workspace, deployment)
		if err != nil {
			return "", nil, err
		}
		if ids[component] != containerID {
			return "", nil, errors.New("curated control container changed")
		}
		_, installation, managerIDs, _, err := marketplaceStoreInstallation(ctx, workspace, document)
		if err != nil {
			return "", nil, err
		}
		c, err := docker.NewLocalClient().InspectContainer(ctx, containerID)
		if err != nil {
			return "", nil, err
		}
		uuid := c.Config.Labels["coolify.service.subUuid"]
		if uuid == "" || c.Config.Labels["coolify.serviceUuid"] != state.Deployment.ExternalID || state.ServerID != installation.ServerID {
			return "", nil, errors.New("curated component differs from its native service/server")
		}
		command, err := marketplaceStoreCommand(marketplaceStoreQuery{Scope: "curated", Server: state.ServerID, Service: state.Deployment.ExternalID, Component: uuid, Name: component, Action: strings.TrimPrefix(action, "rollback-"), Operation: operation + ":" + action})
		return managerIDs["coolify"], command, err
	}
	validate := func(ctx context.Context, action string) error {
		o, state, _, err := coolifyOwnedWorkload(ctx, workspace, deployment)
		if err != nil {
			return err
		}
		cfg, _, err := o.configuration(ctx, deployment)
		if err != nil {
			return err
		}
		adapter := platformdeploy.NewCoolifyAdapter(cfg)
		components, err := adapter.ServiceComponents(ctx, state.Deployment)
		if err != nil {
			return err
		}
		selected, err := platformdeploy.SelectCoolifyServiceComponent(components, component)
		if err != nil {
			return err
		}
		uuid := selected.UUID
		_, _, ids, _, err := marketplaceStoreInstallation(ctx, workspace, document)
		if err != nil {
			return err
		}
		return inspectMarketplaceComponent(ctx, ids["coolify"], marketplaceStoreQuery{Scope: "curated", Server: state.ServerID, Service: state.Deployment.ExternalID, Component: uuid, Name: component, Action: "inspect"})
	}
	return &marketplaceStoreControlDriver{client: client, resolve: resolve, validate: validate}, nil
}
