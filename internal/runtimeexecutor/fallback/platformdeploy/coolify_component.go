package platformdeploy

import (
	"context"
	"errors"
	"net/http"
	"net/url"
)

// CoolifyApplicationComponent is a component inside one retained service.
type CoolifyApplicationComponent struct {
	UUID   string `json:"uuid"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

func (a *CoolifyAdapter) ApplicationComponents(ctx context.Context, ref DeploymentRef) ([]CoolifyApplicationComponent, error) {
	if ref.Platform != "coolify" || ref.ExternalID == "" || a.cfg.LegacyDockerComposeAPI {
		return nil, errors.New("Coolify component control requires a retained service identity")
	}
	var components []CoolifyApplicationComponent
	if _, _, err := a.client.getJSON(ctx, "/api/v1/services/"+url.PathEscape(ref.ExternalID)+"/applications", &components); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, component := range components {
		if component.UUID == "" || component.Name == "" || seen[component.UUID] {
			return nil, errors.New("Coolify component readback has ambiguous identity")
		}
		seen[component.UUID] = true
	}
	return components, nil
}

// ControlApplication uses the manager's component controls. Stop retains the
// container, and Start does not request forced recreation or an image update.
func (a *CoolifyAdapter) ControlApplication(ctx context.Context, ref DeploymentRef, componentUUID, action string) error {
	if action != "stop" && action != "start" {
		return errors.New("unsupported Coolify component control")
	}
	components, err := a.ApplicationComponents(ctx, ref)
	if err != nil {
		return err
	}
	owned := false
	for _, component := range components {
		if component.UUID == componentUUID {
			owned = true
		}
	}
	if !owned {
		return errors.New("Coolify component is not held by the retained service")
	}
	path := "/api/v1/services/" + url.PathEscape(ref.ExternalID) + "/applications/" + url.PathEscape(componentUUID) + "/" + action
	var response map[string]any
	_, _, err = a.client.doJSON(ctx, http.MethodPost, path, nil, &response)
	return err
}
