package platformdeploy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// DokployAdapter deploys generated compose bundles through Dokploy's Compose
// API. Dokploy authenticates with the x-api-key header.
type DokployAdapter struct {
	client apiClient
	cfg    HTTPConfig
}

func NewDokployAdapter(cfg HTTPConfig) *DokployAdapter {
	return &DokployAdapter{
		client: apiClient{cfg: cfg, authMode: authAPIKey},
		cfg:    cfg,
	}
}

func (a *DokployAdapter) BootstrapProviderName() string {
	return "dokploy-draft"
}

func (a *DokployAdapter) BootstrapCapabilities() []BootstrapCapability {
	return []BootstrapCapability{
		BootstrapCapabilityAPIAccess,
		BootstrapCapabilityServiceHandoff,
	}
}

func (a *DokployAdapter) ApplyCompose(ctx context.Context, manifest AppManifest) (DeploymentRef, error) {
	ref, err := a.UpsertCompose(ctx, manifest)
	if err != nil {
		return DeploymentRef{}, err
	}
	if err := a.Deploy(ctx, ref); err != nil {
		return DeploymentRef{}, err
	}
	ref.LastDeployed = time.Now().UTC()
	return ref, nil
}

func (a *DokployAdapter) UpsertCompose(ctx context.Context, manifest AppManifest) (DeploymentRef, error) {
	payload := map[string]any{
		"name":        manifest.Name,
		"appName":     manifest.Name,
		"description": "Managed by StackKit",
		"composeType": "docker-compose",
		"sourceType":  "raw",
		"composeFile": manifest.ComposeYAML,
	}
	if a.cfg.EnvironmentID != "" {
		payload["environmentId"] = a.cfg.EnvironmentID
	}
	if a.cfg.ServerID != "" {
		payload["serverId"] = a.cfg.ServerID
	}

	var created map[string]any
	status, body, err := a.client.postJSON(ctx, "/api/compose.create", payload, &created)
	if err != nil {
		if status != http.StatusConflict {
			return DeploymentRef{}, fmt.Errorf("dokploy compose create %q: %w", manifest.Name, err)
		}
		composeID := idFromBody(body)
		if composeID == "" {
			composeID = manifest.Name
		}
		payload["composeId"] = composeID

		var updated map[string]any
		if _, _, updateErr := a.client.postJSON(ctx, "/api/compose.update", payload, &updated); updateErr != nil {
			return DeploymentRef{}, fmt.Errorf("dokploy compose update %q: %w", manifest.Name, updateErr)
		}
		if id := firstString(updated, "id", "composeId"); id != "" {
			composeID = id
		}
		return DeploymentRef{Platform: manifest.ManagedBy, AppName: manifest.Name, ExternalID: composeID}, nil
	}

	composeID := firstString(created, "id", "composeId")
	if composeID == "" {
		composeID = manifest.Name
	}
	payload["composeId"] = composeID
	var updated map[string]any
	if _, _, updateErr := a.client.postJSON(ctx, "/api/compose.update", payload, &updated); updateErr != nil {
		return DeploymentRef{}, fmt.Errorf("dokploy compose update %q after create: %w", manifest.Name, updateErr)
	}
	if id := firstString(updated, "id", "composeId"); id != "" {
		composeID = id
	}
	return DeploymentRef{Platform: manifest.ManagedBy, AppName: manifest.Name, ExternalID: composeID}, nil
}

func (a *DokployAdapter) Deploy(ctx context.Context, ref DeploymentRef) error {
	if ref.ExternalID == "" {
		return fmt.Errorf("dokploy compose deploy requires external id")
	}
	payload := map[string]any{
		"composeId":   ref.ExternalID,
		"title":       "StackKit deploy",
		"description": "Triggered by StackKit platform adapter",
	}
	if _, _, err := a.client.postJSON(ctx, "/api/compose.deploy", payload, nil); err != nil {
		return fmt.Errorf("dokploy compose deploy %q: %w", ref.AppName, err)
	}
	return nil
}

func (a *DokployAdapter) Status(ctx context.Context, ref DeploymentRef) error {
	if ref.ExternalID == "" {
		return fmt.Errorf("dokploy compose status requires external id")
	}
	path := "/api/compose.one?composeId=" + ref.ExternalID
	if _, _, err := a.client.getJSON(ctx, path, nil); err != nil {
		return fmt.Errorf("dokploy compose status %q: %w", ref.AppName, err)
	}
	return nil
}

func (a *DokployAdapter) Delete(ctx context.Context, ref DeploymentRef, deleteVolumes bool) error {
	if ref.ExternalID == "" {
		return fmt.Errorf("dokploy compose delete requires external id")
	}
	payload := map[string]any{
		"composeId":     ref.ExternalID,
		"deleteVolumes": deleteVolumes,
	}
	if _, _, err := a.client.postJSON(ctx, "/api/compose.delete", payload, nil); err != nil {
		return fmt.Errorf("dokploy compose delete %q: %w", ref.AppName, err)
	}
	return nil
}

func idFromBody(body []byte) string {
	var payload map[string]any
	if len(body) == 0 || json.Unmarshal(body, &payload) != nil {
		return ""
	}
	return firstString(payload, "id", "composeId", "uuid")
}
