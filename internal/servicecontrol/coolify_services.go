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
	if action != ActionStop && action != ActionStart && action != ActionRestart {
		return runtimeCommandOutput{}, errors.New("Coolify service control requires start, stop or restart")
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
	phases := []string{action}
	if action == ActionRestart {
		phases = []string{ActionStop, ActionStart}
	}
	for _, phase := range phases {
		for _, uuid := range selected {
			if err := adapter.ControlApplication(ctx, ref, uuid, phase); err != nil {
				return runtimeCommandOutput{}, err
			}
		}
		if err := waitCoolifyServicePhase(ctx, adapter, ref, selected, phase); err != nil {
			return runtimeCommandOutput{}, err
		}
	}
	state := DesiredRunning
	if action == ActionStop {
		state = DesiredStopped
	}
	return runtimeCommandOutput{ObservedState: state}, nil
}

func waitCoolifyServicePhase(ctx context.Context, adapter *platformdeploy.CoolifyAdapter, ref platformdeploy.DeploymentRef, selected map[string]string, phase string) error {
	for {
		components, err := adapter.ApplicationComponents(ctx, ref)
		if err != nil {
			return err
		}
		ready := true
		for name, uuid := range selected {
			found := false
			for _, component := range components {
				if component.UUID != uuid || component.Name != name {
					continue
				}
				found = true
				if phase == ActionStop {
					ready = ready && strings.HasPrefix(component.Status, "exited")
				} else {
					ready = ready && strings.HasPrefix(component.Status, "running")
				}
			}
			if !found {
				return errors.New("Coolify service component identity changed")
			}
		}
		if ready {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}
