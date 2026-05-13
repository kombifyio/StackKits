package platformdeploy

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// CoolifyAdapter deploys generated compose bundles through Coolify's API.
// Coolify authenticates with bearer tokens.
type CoolifyAdapter struct {
	client apiClient
	cfg    HTTPConfig
}

func NewCoolifyAdapter(cfg HTTPConfig) *CoolifyAdapter {
	return &CoolifyAdapter{
		client: apiClient{cfg: cfg, authMode: authBearer},
		cfg:    cfg,
	}
}

func (a *CoolifyAdapter) ApplyCompose(ctx context.Context, manifest AppManifest) (DeploymentRef, error) {
	ref, err := a.UpsertCompose(ctx, manifest)
	if err != nil {
		return DeploymentRef{}, err
	}
	deploymentID, err := a.Deploy(ctx, ref)
	if err != nil {
		return DeploymentRef{}, err
	}
	ref.DeploymentID = deploymentID
	ref.LastDeployed = time.Now().UTC()
	return ref, nil
}

func (a *CoolifyAdapter) UpsertCompose(ctx context.Context, manifest AppManifest) (DeploymentRef, error) {
	payload := map[string]any{
		"name":               manifest.Name,
		"description":        "Managed by StackKit",
		"docker_compose_raw": manifest.ComposeYAML,
		"instant_deploy":     false,
	}
	if a.cfg.ProjectUUID != "" {
		payload["project_uuid"] = a.cfg.ProjectUUID
	}
	if a.cfg.ServerID != "" {
		payload["server_uuid"] = a.cfg.ServerID
	}
	if a.cfg.EnvironmentUUID != "" {
		payload["environment_uuid"] = a.cfg.EnvironmentUUID
	}
	if a.cfg.EnvironmentID != "" {
		payload["environment_name"] = a.cfg.EnvironmentID
	}
	if a.cfg.DestinationUUID != "" {
		payload["destination_uuid"] = a.cfg.DestinationUUID
	}

	var created map[string]any
	status, body, err := a.client.postJSON(ctx, "/api/v1/applications/dockercompose", payload, &created)
	if err != nil {
		if status != http.StatusConflict {
			return DeploymentRef{}, fmt.Errorf("coolify compose app create %q: %w", manifest.Name, err)
		}
		uuid := idFromBody(body)
		if uuid == "" {
			return DeploymentRef{}, fmt.Errorf("coolify compose app create conflict %q did not include uuid", manifest.Name)
		}
		var updated map[string]any
		if _, _, updateErr := a.client.doJSON(ctx, http.MethodPatch, "/api/v1/applications/"+url.PathEscape(uuid), payload, &updated); updateErr != nil {
			return DeploymentRef{}, fmt.Errorf("coolify compose app update %q: %w", manifest.Name, updateErr)
		}
		if id := firstString(updated, "uuid", "id"); id != "" {
			uuid = id
		}
		return DeploymentRef{Platform: manifest.ManagedBy, AppName: manifest.Name, ExternalID: uuid}, nil
	}
	uuid := firstString(created, "uuid", "id")
	if uuid == "" {
		uuid = manifest.Name
	}
	return DeploymentRef{Platform: manifest.ManagedBy, AppName: manifest.Name, ExternalID: uuid}, nil
}

func (a *CoolifyAdapter) Deploy(ctx context.Context, ref DeploymentRef) (string, error) {
	if ref.ExternalID == "" {
		return "", fmt.Errorf("coolify deploy requires external id")
	}
	var deployed map[string]any
	path := "/api/v1/deploy?uuid=" + url.QueryEscape(ref.ExternalID)
	if _, _, err := a.client.getJSON(ctx, path, &deployed); err != nil {
		return "", fmt.Errorf("coolify deploy %q: %w", ref.AppName, err)
	}
	return firstDeploymentID(deployed), nil
}

func (a *CoolifyAdapter) Status(ctx context.Context, ref DeploymentRef) error {
	if ref.ExternalID == "" {
		return fmt.Errorf("coolify status requires external id")
	}
	path := "/api/v1/applications/" + url.PathEscape(ref.ExternalID)
	if _, _, err := a.client.getJSON(ctx, path, nil); err != nil {
		return fmt.Errorf("coolify status %q: %w", ref.AppName, err)
	}
	return nil
}

func (a *CoolifyAdapter) Delete(ctx context.Context, ref DeploymentRef) error {
	if ref.ExternalID == "" {
		return fmt.Errorf("coolify delete requires external id")
	}
	path := "/api/v1/applications/" + url.PathEscape(ref.ExternalID)
	if _, _, err := a.client.doJSON(ctx, "DELETE", path, nil, nil); err != nil {
		return fmt.Errorf("coolify delete %q: %w", ref.AppName, err)
	}
	return nil
}

func firstDeploymentID(payload map[string]any) string {
	items, _ := payload["deployments"].([]any)
	if len(items) == 0 {
		return ""
	}
	first, _ := items[0].(map[string]any)
	return firstString(first, "deployment_uuid", "uuid", "id")
}
