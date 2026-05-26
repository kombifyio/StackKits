package platformdeploy

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
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
	if a.cfg.LegacyDockerComposeAPI {
		return a.upsertLegacyDockerCompose(ctx, manifest)
	}

	payload := coolifyServicePayload(manifest, a.cfg)

	var created map[string]any
	status, body, err := a.client.postJSON(ctx, "/api/v1/services", payload, &created)
	if err != nil {
		if status != http.StatusConflict {
			return DeploymentRef{}, fmt.Errorf("coolify service create %q: %w", manifest.Name, err)
		}
		return a.updateConflictingService(ctx, manifest, payload, body, "service")
	}
	uuid := firstString(created, "uuid", "id")
	if uuid == "" {
		uuid = manifest.Name
	}
	return DeploymentRef{Platform: manifest.ManagedBy, AppName: manifest.Name, ExternalID: uuid}, nil
}

func (a *CoolifyAdapter) upsertLegacyDockerCompose(ctx context.Context, manifest AppManifest) (DeploymentRef, error) {
	payload := map[string]any{
		"name":               manifest.Name,
		"description":        "Managed by StackKit",
		"docker_compose_raw": normalizeComposeYAML(manifest.ComposeYAML),
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
		return a.updateConflictingService(ctx, manifest, payload, body, "compose app")
	}
	uuid := firstString(created, "uuid", "id")
	if uuid == "" {
		uuid = manifest.Name
	}
	return DeploymentRef{Platform: manifest.ManagedBy, AppName: manifest.Name, ExternalID: uuid}, nil
}

func (a *CoolifyAdapter) updateConflictingService(ctx context.Context, manifest AppManifest, payload map[string]any, body []byte, label string) (DeploymentRef, error) {
	uuid := idFromBody(body)
	if uuid == "" {
		return DeploymentRef{}, fmt.Errorf("coolify %s create conflict %q did not include uuid", label, manifest.Name)
	}
	var updated map[string]any
	if _, _, updateErr := a.client.doJSON(ctx, http.MethodPatch, "/api/v1/services/"+url.PathEscape(uuid), payload, &updated); updateErr != nil {
		return DeploymentRef{}, fmt.Errorf("coolify %s update %q: %w", label, manifest.Name, updateErr)
	}
	if id := firstString(updated, "uuid", "id"); id != "" {
		uuid = id
	}
	return DeploymentRef{Platform: manifest.ManagedBy, AppName: manifest.Name, ExternalID: uuid}, nil
}

func (a *CoolifyAdapter) Deploy(ctx context.Context, ref DeploymentRef) (string, error) {
	if ref.ExternalID == "" {
		return "", fmt.Errorf("coolify deploy requires external id")
	}
	var deployed map[string]any
	if a.cfg.LegacyDockerComposeAPI {
		path := "/api/v1/deploy?uuid=" + url.QueryEscape(ref.ExternalID)
		if _, _, err := a.client.getJSON(ctx, path, &deployed); err != nil {
			return "", fmt.Errorf("coolify deploy %q: %w", ref.AppName, err)
		}
		return firstDeploymentID(deployed), nil
	}
	path := "/api/v1/services/" + url.PathEscape(ref.ExternalID) + "/start"
	if _, _, err := a.client.doJSON(ctx, http.MethodPost, path, nil, &deployed); err != nil {
		return "", fmt.Errorf("coolify deploy %q: %w", ref.AppName, err)
	}
	return firstDeploymentID(deployed), nil
}

func (a *CoolifyAdapter) Status(ctx context.Context, ref DeploymentRef) error {
	if ref.ExternalID == "" {
		return fmt.Errorf("coolify status requires external id")
	}
	path := "/api/v1/services/" + url.PathEscape(ref.ExternalID)
	if a.cfg.LegacyDockerComposeAPI {
		path = "/api/v1/applications/" + url.PathEscape(ref.ExternalID)
	}
	if _, _, err := a.client.getJSON(ctx, path, nil); err != nil {
		return fmt.Errorf("coolify status %q: %w", ref.AppName, err)
	}
	return nil
}

func (a *CoolifyAdapter) Delete(ctx context.Context, ref DeploymentRef) error {
	if ref.ExternalID == "" {
		return fmt.Errorf("coolify delete requires external id")
	}
	path := "/api/v1/services/" + url.PathEscape(ref.ExternalID)
	if a.cfg.LegacyDockerComposeAPI {
		path = "/api/v1/applications/" + url.PathEscape(ref.ExternalID)
	}
	if _, _, err := a.client.doJSON(ctx, "DELETE", path, nil, nil); err != nil {
		return fmt.Errorf("coolify delete %q: %w", ref.AppName, err)
	}
	return nil
}

func coolifyServicePayload(manifest AppManifest, cfg HTTPConfig) map[string]any {
	compose := normalizeComposeYAML(manifest.ComposeYAML)
	resourceName := coolifyServiceResourceName(manifest.Name, compose)
	payload := map[string]any{
		"name":                              resourceName,
		"description":                       "Managed by StackKit",
		"docker_compose_raw":                base64.StdEncoding.EncodeToString([]byte(compose)),
		"instant_deploy":                    false,
		"is_container_label_escape_enabled": true,
	}
	if cfg.ProjectUUID != "" {
		payload["project_uuid"] = cfg.ProjectUUID
	}
	if cfg.ServerID != "" {
		payload["server_uuid"] = cfg.ServerID
	}
	if cfg.EnvironmentUUID != "" {
		payload["environment_uuid"] = cfg.EnvironmentUUID
	}
	if cfg.EnvironmentID != "" {
		payload["environment_name"] = cfg.EnvironmentID
	}
	if cfg.DestinationUUID != "" {
		payload["destination_uuid"] = cfg.DestinationUUID
	}
	if route := coolifyServiceRoute(manifest); route != "" {
		payload["urls"] = []map[string]string{{
			"name": resourceName,
			"url":  route,
		}}
	}
	return payload
}

func coolifyServiceResourceName(appName, compose string) string {
	firstService, appNameExists := firstComposeServiceName(compose, appName)
	if appNameExists || firstService == "" {
		return appName
	}
	return firstService
}

func firstComposeServiceName(compose, appName string) (string, bool) {
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(compose), &root); err != nil || len(root.Content) == 0 {
		return "", false
	}
	doc := root.Content[0]
	if doc.Kind != yaml.MappingNode {
		return "", false
	}
	for i := 0; i+1 < len(doc.Content); i += 2 {
		if doc.Content[i].Value != "services" {
			continue
		}
		services := doc.Content[i+1]
		if services.Kind != yaml.MappingNode || len(services.Content) == 0 {
			return "", false
		}
		first := ""
		for j := 0; j+1 < len(services.Content); j += 2 {
			name := services.Content[j].Value
			if first == "" {
				first = name
			}
			if name == appName {
				return first, true
			}
		}
		return first, false
	}
	return "", false
}

func coolifyServiceRoute(manifest AppManifest) string {
	route := manifest.URL
	if route == "" {
		route = manifest.Host
	}
	if route == "" {
		return ""
	}
	if strings.HasPrefix(route, "http://") || strings.HasPrefix(route, "https://") {
		return route
	}
	return "https://" + route
}

func normalizeComposeYAML(compose string) string {
	compose = strings.ReplaceAll(compose, "\r\n", "\n")
	compose = strings.Trim(compose, "\n")
	lines := strings.Split(compose, "\n")
	minIndent := -1
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		indent := 0
		for indent < len(line) && line[indent] == ' ' {
			indent++
		}
		if minIndent == -1 || indent < minIndent {
			minIndent = indent
		}
	}
	if minIndent > 0 {
		for i, line := range lines {
			if strings.TrimSpace(line) == "" {
				continue
			}
			if len(line) >= minIndent {
				lines[i] = line[minIndent:]
			}
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n")) + "\n"
}

func firstDeploymentID(payload map[string]any) string {
	items, _ := payload["deployments"].([]any)
	if len(items) == 0 {
		return ""
	}
	first, _ := items[0].(map[string]any)
	return firstString(first, "deployment_uuid", "uuid", "id")
}
