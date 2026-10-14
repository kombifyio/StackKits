package servicecontrol

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/runtimeexecutor/fallback/platformdeploy"
)

func runCoolifyService(ctx context.Context, request runtimeCommandRequest) (runtimeCommandOutput, error) {
	action := request.Args[0]
	if action != ActionStop && action != ActionStart {
		return runtimeCommandOutput{}, errors.New("Coolify service control requires start or stop")
	}
	ref := platformdeploy.DeploymentRef{Platform: "coolify", AppName: "stackkit-" + request.RuntimeRef, ExternalID: request.ExternalID}
	adapter := platformdeploy.NewCoolifyAdapter(request.HTTPConfig)
	components, err := adapter.ApplicationComponents(ctx, ref)
	if err != nil {
		return runtimeCommandOutput{}, err
	}
	selected := map[string]string{}
	for _, name := range request.ComponentRefs {
		for _, component := range components {
			if component.Name == name {
				if selected[name] != "" {
					return runtimeCommandOutput{}, errors.New("Coolify service component is ambiguous")
				}
				selected[name] = component.UUID
			}
		}
		if selected[name] == "" {
			return runtimeCommandOutput{}, errors.New("Coolify service has no declared component")
		}
	}
	for _, uuid := range selected {
		if err := adapter.ControlApplication(ctx, ref, uuid, action); err != nil {
			return runtimeCommandOutput{}, err
		}
	}
	for {
		components, err := adapter.ApplicationComponents(ctx, ref)
		if err != nil {
			return runtimeCommandOutput{}, err
		}
		ready := true
		for name, uuid := range selected {
			found := false
			for _, component := range components {
				if component.UUID != uuid || component.Name != name {
					continue
				}
				found = true
				if action == ActionStop {
					ready = ready && strings.HasPrefix(component.Status, "exited")
				} else {
					ready = ready && strings.HasPrefix(component.Status, "running")
				}
			}
			if !found {
				return runtimeCommandOutput{}, errors.New("Coolify service component identity changed")
			}
		}
		if ready {
			state := DesiredRunning
			if action == ActionStop {
				state = DesiredStopped
			}
			return runtimeCommandOutput{ObservedState: state}, nil
		}
		select {
		case <-ctx.Done():
			return runtimeCommandOutput{}, ctx.Err()
		case <-time.After(time.Second):
		}
	}
}
