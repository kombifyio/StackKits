package platformdeploy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
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
		return dokployApplyFailure(ref, err)
	}
	if err := a.Deploy(ctx, ref); err != nil {
		return dokployApplyFailure(ref, err)
	}
	ref.LastDeployed = time.Now().UTC()
	return ref, nil
}

func dokployApplyFailure(ref DeploymentRef, err error) (DeploymentRef, error) {
	if ref.ExternalID != "" {
		// Retrying ApplyCompose creates another randomized appName. Keep the
		// known identity and require reconciliation through its recorded ID.
		err = &DeploymentIdentityUncertainError{Err: fmt.Errorf("%w; retain composeId %q and reconcile with UpdateCompose instead of retrying ApplyCompose", err, ref.ExternalID)}
	}
	return ref, err
}

// UpsertCompose retains the historical entry point for a new deployment.
// Reconciliation of an existing deployment must use UpdateCompose with custody's
// composeId: Dokploy randomizes appName, so repeated creation is not an upsert.
func (a *DokployAdapter) UpsertCompose(ctx context.Context, manifest AppManifest) (DeploymentRef, error) {
	if manifest.ManagedBy != "dokploy" || strings.TrimSpace(manifest.Name) == "" {
		return DeploymentRef{}, fmt.Errorf("dokploy compose create requires a named Dokploy-managed workload")
	}
	if strings.TrimSpace(a.cfg.EnvironmentID) == "" {
		return DeploymentRef{}, fmt.Errorf("dokploy compose create requires an environment id")
	}
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
	status, _, err := a.client.postJSON(ctx, "/api/compose.create", payload, &created)
	if err != nil {
		cause := fmt.Errorf("dokploy compose create %q: %w", manifest.Name, err)
		// A conflict identifies an existing owner, not its composeId. A lost
		// response, decode failure or server error can follow a committed create.
		if status == 0 || status == http.StatusConflict || status >= 500 || (status >= 200 && status < 300) {
			return DeploymentRef{}, &DeploymentIdentityUncertainError{Err: fmt.Errorf("%w; reconcile the platform identity before retrying; use UpdateCompose with the recorded composeId", cause)}
		}
		return DeploymentRef{}, cause
	}

	composeID, _ := created["composeId"].(string)
	if strings.TrimSpace(composeID) == "" || strings.TrimSpace(composeID) != composeID {
		return DeploymentRef{}, &DeploymentIdentityUncertainError{Err: fmt.Errorf("dokploy compose create %q returned no valid composeId; reconcile the platform identity before retrying", manifest.Name)}
	}
	ref := DeploymentRef{Platform: manifest.ManagedBy, AppName: manifest.Name, ExternalID: composeID}
	// Earlier Dokploy APIs omit sourceType from compose.create's accepted
	// fields. Set raw explicitly while preserving the identity on any failure.
	return a.UpdateCompose(ctx, ref, manifest)
}

// UpdateCompose changes only an explicitly recorded deployment. A matching
// display name or a create conflict never authorizes adopting another service.
func (a *DokployAdapter) UpdateCompose(ctx context.Context, ref DeploymentRef, manifest AppManifest) (DeploymentRef, error) {
	if err := validateDokployRef(ref); err != nil {
		return ref, err
	}
	if ref.AppName != manifest.Name || manifest.ManagedBy != ref.Platform {
		return ref, fmt.Errorf("dokploy compose update differs from the recorded workload identity")
	}
	if _, err := a.readCompose(ctx, ref); err != nil {
		return ref, err
	}
	payload := map[string]any{"composeId": ref.ExternalID, "composeFile": manifest.ComposeYAML, "sourceType": "raw"}
	var updated struct {
		ComposeID string `json:"composeId"`
	}
	if _, _, err := a.client.postJSON(ctx, "/api/compose.update", payload, &updated); err != nil {
		return ref, fmt.Errorf("dokploy compose update %q: %w", ref.AppName, err)
	}
	if updated.ComposeID != ref.ExternalID {
		return ref, fmt.Errorf("dokploy compose update returned a different or missing composeId; reconcile before retrying")
	}
	ref.ObservedStatus, ref.ObservedAt = "", time.Time{}
	return ref, nil
}

func (a *DokployAdapter) Deploy(ctx context.Context, ref DeploymentRef) error {
	if err := validateDokployRef(ref); err != nil {
		return err
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

// Status validates placement and the last recorded deployment state only.
// A newly queued Deploy can retain a previous done state until its worker starts;
// nil does not certify the latest queued apply, application health or data access.
func (a *DokployAdapter) Status(ctx context.Context, ref DeploymentRef) error {
	status, err := a.readCompose(ctx, ref)
	if err != nil {
		return err
	}
	// Dokploy's running state means a deployment command is executing.
	if status != "done" {
		return fmt.Errorf("dokploy compose %q last recorded deployment status is %q, want done", ref.AppName, status)
	}
	return nil
}

func (a *DokployAdapter) Delete(ctx context.Context, ref DeploymentRef, deleteVolumes bool) error {
	if err := validateDokployRef(ref); err != nil {
		return err
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

func validateDokployRef(ref DeploymentRef) error {
	if ref.Platform != "dokploy" || strings.TrimSpace(ref.ExternalID) == "" || strings.TrimSpace(ref.ExternalID) != ref.ExternalID {
		return fmt.Errorf("dokploy operation requires a recorded Dokploy composeId")
	}
	return nil
}

func (a *DokployAdapter) readCompose(ctx context.Context, ref DeploymentRef) (string, error) {
	if err := validateDokployRef(ref); err != nil {
		return "", err
	}
	var observed struct {
		ComposeID     string `json:"composeId"`
		Name          string `json:"name"`
		EnvironmentID string `json:"environmentId"`
		ServerID      string `json:"serverId"`
		Status        string `json:"composeStatus"`
	}
	path := "/api/compose.one?" + url.Values{"composeId": {ref.ExternalID}}.Encode()
	if _, _, err := a.client.getJSON(ctx, path, &observed); err != nil {
		return "", fmt.Errorf("dokploy compose status %q: %w", ref.AppName, err)
	}
	// Dokploy adds a random suffix to appName. Name is our display name;
	// composeId and placement, never that title, identify the owned service.
	if observed.ComposeID != ref.ExternalID || observed.Name != ref.AppName ||
		(a.cfg.EnvironmentID != "" && observed.EnvironmentID != a.cfg.EnvironmentID) || observed.ServerID != a.cfg.ServerID {
		return "", fmt.Errorf("dokploy compose readback differs from the recorded workload placement")
	}
	switch observed.Status {
	case "idle", "running", "done", "error":
		return observed.Status, nil
	default:
		return "", fmt.Errorf("dokploy compose readback has a missing or unknown deployment status")
	}
}

func idFromBody(body []byte) string {
	var payload map[string]any
	if len(body) == 0 || json.Unmarshal(body, &payload) != nil {
		return ""
	}
	return firstString(payload, "id", "composeId", "uuid")
}
