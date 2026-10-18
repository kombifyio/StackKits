package nativehost

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/docker"
	"github.com/kombifyio/stackkits/internal/localbackuppolicy"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/fallback/platformdeploy"
)

// InspectMarketplaceStoreSelection is read-only discovery for an explicit
// service/component/volume choice. Its output becomes authority only after
// the operator selects it in the StackSpec and generates/applies that Plan.
func InspectMarketplaceStoreSelection(ctx context.Context, workspace string, d architecturev2renderer.MarketplaceCoolifyDocument, service, component, volumeName, dataClass string) (localbackuppolicy.MarketplaceService, error) {
	o, state, ids, installation, err := marketplaceStoreInstallation(ctx, workspace, d)
	if err != nil {
		return localbackuppolicy.MarketplaceService{}, err
	}
	endpoint, err := o.endpoint(ctx, filepath.Join(workspace, marketplaceDirectory))
	if err != nil {
		return localbackuppolicy.MarketplaceService{}, err
	}
	adapter := platformdeploy.NewCoolifyAdapter(platformdeploy.HTTPConfig{BaseURL: endpoint, Token: state.APIToken, ServerID: state.ServerID})
	components, err := adapter.ApplicationComponents(ctx, platformdeploy.DeploymentRef{Platform: "coolify", ExternalID: service})
	if err != nil {
		return localbackuppolicy.MarketplaceService{}, err
	}
	if len(components) != 1 || components[0].UUID != component {
		return localbackuppolicy.MarketplaceService{}, errors.New("Store backup requires one explicitly selected filesystem application")
	}
	name := components[0].Name
	command, err := marketplaceStoreCommand(marketplaceStoreQuery{Scope: "store", Server: state.ServerID, Service: service, Component: component, Name: name, Action: "inspect"})
	if err != nil {
		return localbackuppolicy.MarketplaceService{}, err
	}
	client := docker.NewLocalClient()
	result, err := client.Exec(ctx, ids["coolify"], command)
	if err != nil {
		return localbackuppolicy.MarketplaceService{}, errors.New("Store native membership inspection failed")
	}
	var member struct {
		Server    string `json:"server"`
		Service   string `json:"service"`
		Component string `json:"component"`
		Name      string `json:"name"`
	}
	if json.Unmarshal([]byte(result), &member) != nil || member.Server != state.ServerID || member.Service != service || member.Component != component || member.Name != name {
		return localbackuppolicy.MarketplaceService{}, errors.New("Store native service/component membership changed")
	}
	info, err := client.DaemonIdentity(ctx)
	if err != nil {
		return localbackuppolicy.MarketplaceService{}, err
	}
	volume, err := client.InspectVolume(ctx, volumeName)
	if err != nil {
		return localbackuppolicy.MarketplaceService{}, err
	}
	digest, err := resolvedplan.CanonicalSHA256(volume)
	if err != nil {
		return localbackuppolicy.MarketplaceService{}, err
	}
	listed, err := client.ListContainers(ctx, true)
	if err != nil {
		return localbackuppolicy.MarketplaceService{}, err
	}
	var selected *localbackuppolicy.MarketplaceService
	for _, entry := range listed {
		c, err := client.InspectContainer(ctx, entry.ID)
		if err != nil {
			return localbackuppolicy.MarketplaceService{}, err
		}
		if c.Config.Labels["coolify.serviceUuid"] != service || c.Config.Labels["coolify.service.subUuid"] != component {
			continue
		}
		for _, mount := range c.Mounts {
			if mount.Type != "volume" || mount.Name != volumeName || !mount.RW {
				continue
			}
			if selected != nil {
				return localbackuppolicy.MarketplaceService{}, errors.New("Store selection is ambiguous")
			}
			selected = &localbackuppolicy.MarketplaceService{SiteRef: state.SiteRef, NodeRef: state.NodeRef, InstallationDigest: installation, ServerUUID: state.ServerID, ServiceUUID: service, ComponentUUID: component, ComponentName: name, VolumeName: volumeName, ContainerTarget: mount.Destination, BackupDataClass: dataClass, ExpectedGeneration: localbackuppolicy.MarketplaceServiceGeneration{DaemonID: info.ID, ContainerID: c.ID, ImageID: c.Image, VolumeCreatedAt: volume.CreatedAt, VolumeMountpoint: volume.Mountpoint, VolumeMetadataDigest: digest}}
		}
	}
	if selected == nil {
		return localbackuppolicy.MarketplaceService{}, errors.New("Store selection has no retained volume writer")
	}
	if _, err := ObserveMarketplaceStore(ctx, workspace, d, *selected); err != nil {
		return localbackuppolicy.MarketplaceService{}, err
	}
	return *selected, nil
}
