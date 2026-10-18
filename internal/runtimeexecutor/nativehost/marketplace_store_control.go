package nativehost

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/backuplifecycle"
	"github.com/kombifyio/stackkits/internal/docker"
	"github.com/kombifyio/stackkits/internal/localbackuppolicy"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/fallback/platformdeploy"
)

type marketplaceStoreControlDriver struct {
	workspace string
	document  architecturev2renderer.MarketplaceCoolifyDocument
	source    localbackuppolicy.MarketplaceService
	operation string
	client    *docker.ExecHandleClient
	resolve   func(context.Context, string) (string, []string, error)
	validate  func(context.Context, string) error
}

func NewMarketplaceStoreControlDriver(workspace string, d architecturev2renderer.MarketplaceCoolifyDocument, source localbackuppolicy.MarketplaceService, operation string) (backuplifecycle.RuntimeControlDriver, error) {
	if operation == "" {
		return nil, errors.New("Store control requires the owning backup/activation operation")
	}
	if err := localbackuppolicy.RequireMarketplaceServiceGeneration(source, source); err != nil {
		return nil, err
	}
	client, err := docker.NewLocalExecHandleClient()
	if err != nil {
		return nil, err
	}
	return &marketplaceStoreControlDriver{workspace: workspace, document: d, source: source, operation: operation, client: client}, nil
}

func (d *marketplaceStoreControlDriver) binding(ctx context.Context, action string) (string, []string, error) {
	if d.resolve != nil {
		return d.resolve(ctx, action)
	}
	if action != "stop" && action != "start" && action != "rollback-stop" && action != "rollback-start" {
		return "", nil, errors.New("Store control action is outside the owning lifecycle")
	}
	if _, err := ObserveMarketplaceStore(ctx, d.workspace, d.document, d.source); err != nil {
		return "", nil, err
	}
	_, _, ids, installation, err := marketplaceStoreInstallation(ctx, d.workspace, d.document)
	if err != nil {
		return "", nil, err
	}
	if installation != d.source.InstallationDigest {
		return "", nil, errors.New("Store control installation generation changed")
	}
	command, err := marketplaceStoreCommand(marketplaceStoreQuery{Scope: "store", Server: d.source.ServerUUID, Service: d.source.ServiceUUID, Component: d.source.ComponentUUID, Name: d.source.ComponentName, Action: strings.TrimPrefix(action, "rollback-"), Operation: d.operation + ":" + action})
	return ids["coolify"], command, err
}

func (d *marketplaceStoreControlDriver) Prepare(ctx context.Context, action string) (string, error) {
	container, command, err := d.binding(ctx, action)
	if err != nil {
		return "", err
	}
	return d.client.Prepare(ctx, container, command)
}

// Readiness is a read-only preflight before recording a preparation effect.
// Restarted control containers can be running before the private API is ready.
func (d *marketplaceStoreControlDriver) Validate(ctx context.Context, action string) error {
	if err := d.client.Qualify(ctx); err != nil {
		return err
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, _, err := d.binding(ctx, action); err != nil {
			return err
		}
		err := d.validateOnce(ctx, action)
		if err == nil {
			return nil
		}
		if errors.Is(err, errNativeComponentPreflight) {
			return err
		}
		select {
		case <-ctx.Done():
			return errors.Join(ctx.Err(), err)
		case <-ticker.C:
		}
	}
}
func (d *marketplaceStoreControlDriver) validateOnce(ctx context.Context, action string) error {
	if d.validate != nil {
		return d.validate(ctx, action)
	}
	o, state, _, _, err := marketplaceStoreInstallation(ctx, d.workspace, d.document)
	if err != nil {
		return err
	}
	endpoint, err := o.endpoint(ctx, filepath.Join(d.workspace, marketplaceDirectory))
	if err != nil {
		return err
	}
	adapter := platformdeploy.NewCoolifyAdapter(platformdeploy.HTTPConfig{BaseURL: endpoint, Token: state.APIToken, ServerID: state.ServerID})
	if err := adapter.ValidateApplicationControl(ctx, platformdeploy.DeploymentRef{Platform: "coolify", ExternalID: d.source.ServiceUUID}, d.source.ComponentUUID, strings.TrimPrefix(action, "rollback-")); err != nil {
		return err
	}
	_, _, ids, _, err := marketplaceStoreInstallation(ctx, d.workspace, d.document)
	if err != nil {
		return err
	}
	return inspectMarketplaceComponent(ctx, ids["coolify"], marketplaceStoreQuery{Scope: "store", Server: d.source.ServerUUID, Service: d.source.ServiceUUID, Component: d.source.ComponentUUID, Name: d.source.ComponentName, Action: "inspect"})
}

func (d *marketplaceStoreControlDriver) Dispatch(ctx context.Context, handle, action string) error {
	container, command, err := d.binding(ctx, action)
	if err != nil {
		return err
	}
	// Validate the dormant command/container before the one Start request.
	complete, err := d.client.Complete(ctx, handle, container, command, 73)
	if err != nil {
		return err
	}
	if complete {
		return errors.New("Store native handle already completed before dispatch")
	}
	return d.client.Start(ctx, handle)
}

func (d *marketplaceStoreControlDriver) Complete(ctx context.Context, handle, action string) (bool, error) {
	container, command, err := d.binding(ctx, action)
	if err != nil {
		return false, err
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		complete, err := d.client.Complete(ctx, handle, container, command, 73)
		if err != nil || complete {
			return complete, err
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-ticker.C:
		}
	}
}
