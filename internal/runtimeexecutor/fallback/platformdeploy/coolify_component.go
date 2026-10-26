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

// ServiceComponents includes database components for an existing signed
// curated graph. Store-only filesystem admission belongs to its own selector.
func (a *CoolifyAdapter) ServiceComponents(ctx context.Context, ref DeploymentRef) ([]CoolifyApplicationComponent, error) {
	if ref.Platform != "coolify" || ref.ExternalID == "" || a.cfg.LegacyDockerComposeAPI {
		return nil, errors.New("Coolify component control requires retained service custody")
	}
	var service struct {
		UUID         string                        `json:"uuid"`
		Applications []CoolifyApplicationComponent `json:"applications"`
		Databases    []CoolifyApplicationComponent `json:"databases"`
	}
	if _, _, err := a.client.getJSON(ctx, "/api/v1/services/"+url.PathEscape(ref.ExternalID), &service); err != nil {
		return nil, err
	}
	if service.UUID != ref.ExternalID {
		return nil, errors.New("Coolify component service identity changed")
	}
	components := append(service.Applications, service.Databases...)
	seen := map[string]bool{}
	for _, c := range components {
		if c.UUID == "" || c.Name == "" || seen[c.UUID] {
			return nil, errors.New("Coolify component readback is ambiguous")
		}
		seen[c.UUID] = true
	}
	return components, nil
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
	if err := a.ValidateApplicationControl(ctx, ref, componentUUID, action); err != nil {
		return err
	}
	path := "/api/v1/services/" + url.PathEscape(ref.ExternalID) + "/applications/" + url.PathEscape(componentUUID) + "/" + action
	var response map[string]any
	_, _, err := a.client.doJSON(ctx, http.MethodPost, path, nil, &response)
	return err
}

// ValidateApplicationControl is shared by API control and the node-private
// completion-bound transport. Neither may act on another service's component.
func (a *CoolifyAdapter) ValidateApplicationControl(ctx context.Context, ref DeploymentRef, componentUUID, action string) error {
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
	return nil
}

func SelectCoolifyServiceComponent(components []CoolifyApplicationComponent, name string) (CoolifyApplicationComponent, error) {
	var selected CoolifyApplicationComponent
	for _, c := range components {
		if c.Name == name {
			if selected.UUID != "" {
				return selected, errors.New("Coolify component name is ambiguous")
			}
			selected = c
		}
	}
	if selected.UUID == "" {
		return selected, errors.New("Coolify graph component is missing from its retained service")
	}
	return selected, nil
}

// ControlServiceComponent stops or starts one component of the retained
// service through the manager. Coolify lists database containers (for example
// Immich's postgres) under the service's databases, not its applications, so
// the control path follows the component's actual kind.
func (a *CoolifyAdapter) ControlServiceComponent(ctx context.Context, ref DeploymentRef, componentUUID, action string) error {
	if action != "stop" && action != "start" {
		return errors.New("unsupported Coolify component control")
	}
	if ref.Platform != "coolify" || ref.ExternalID == "" || a.cfg.LegacyDockerComposeAPI || componentUUID == "" {
		return errors.New("Coolify component control requires retained service custody")
	}
	var service struct {
		UUID         string                        `json:"uuid"`
		Applications []CoolifyApplicationComponent `json:"applications"`
		Databases    []CoolifyApplicationComponent `json:"databases"`
	}
	if _, _, err := a.client.getJSON(ctx, "/api/v1/services/"+url.PathEscape(ref.ExternalID), &service); err != nil {
		return err
	}
	if service.UUID != ref.ExternalID {
		return errors.New("Coolify component service identity changed")
	}
	kind := ""
	for _, group := range []struct {
		name       string
		components []CoolifyApplicationComponent
	}{{"applications", service.Applications}, {"databases", service.Databases}} {
		for _, component := range group.components {
			if component.UUID == componentUUID {
				if kind != "" {
					return errors.New("Coolify component readback is ambiguous")
				}
				kind = group.name
			}
		}
	}
	if kind == "" {
		return errors.New("Coolify component is not held by the retained service")
	}
	path := "/api/v1/services/" + url.PathEscape(ref.ExternalID) + "/" + kind + "/" + url.PathEscape(componentUUID) + "/" + action
	var response map[string]any
	_, _, err := a.client.doJSON(ctx, http.MethodPost, path, nil, &response)
	return err
}
