package platformdeploy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// KomodoAdapter deploys generated compose bundles through Komodo's Stack API.
// Komodo authenticates with x-api-key and x-api-secret headers.
type KomodoAdapter struct {
	client apiClient
	cfg    HTTPConfig
}

var (
	komodoDeployRetryDelay  = 5 * time.Second
	komodoDeployPollDelay   = 5 * time.Second
	komodoDeployPollAttempt = 72
)

func NewKomodoAdapter(cfg HTTPConfig) *KomodoAdapter {
	return &KomodoAdapter{
		client: apiClient{cfg: cfg, authMode: authAPIKeySecret},
		cfg:    cfg,
	}
}

func (a *KomodoAdapter) ApplyCompose(ctx context.Context, manifest AppManifest) (DeploymentRef, error) {
	ref, err := a.UpsertStack(ctx, manifest)
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

func (a *KomodoAdapter) UpsertStack(ctx context.Context, manifest AppManifest) (DeploymentRef, error) {
	if err := a.validateConfig(); err != nil {
		return DeploymentRef{}, err
	}
	payload := map[string]any{
		"name":   manifest.Name,
		"config": komodoStackConfig(manifest, a.cfg),
	}

	var created map[string]any
	status, body, err := a.client.postJSON(ctx, "/write/CreateStack", payload, &created)
	if err != nil {
		if status != http.StatusConflict && status != http.StatusBadRequest {
			return DeploymentRef{}, fmt.Errorf("komodo stack create %q: %w", manifest.Name, err)
		}
		stackID, resolveErr := a.resolveStackID(ctx, manifest.Name, body)
		if resolveErr != nil {
			return DeploymentRef{}, fmt.Errorf("komodo stack resolve %q after create conflict: %w", manifest.Name, resolveErr)
		}
		updatePayload := map[string]any{
			"id":     stackID,
			"config": komodoStackConfig(manifest, a.cfg),
		}
		var updated map[string]any
		if _, _, updateErr := a.client.postJSON(ctx, "/write/UpdateStack", updatePayload, &updated); updateErr != nil {
			return DeploymentRef{}, fmt.Errorf("komodo stack update %q: %w", manifest.Name, updateErr)
		}
		if id := firstKomodoID(updated); id != "" {
			stackID = id
		}
		return DeploymentRef{Platform: manifest.ManagedBy, AppName: manifest.Name, ExternalID: stackID}, nil
	}

	stackID := firstKomodoID(created)
	if stackID == "" {
		resolvedID, resolveErr := a.resolveStackID(ctx, manifest.Name, body)
		if resolveErr != nil {
			return DeploymentRef{}, fmt.Errorf("komodo stack resolve %q after create: %w", manifest.Name, resolveErr)
		}
		stackID = resolvedID
	}
	return DeploymentRef{Platform: manifest.ManagedBy, AppName: manifest.Name, ExternalID: stackID}, nil
}

func (a *KomodoAdapter) resolveStackID(ctx context.Context, stackName string, responseBody []byte) (string, error) {
	if stackID := idFromBody(responseBody); stackID != "" {
		return stackID, nil
	}
	var stack map[string]any
	if _, _, err := a.client.postJSON(ctx, "/read/GetStack", map[string]any{"stack": stackName}, &stack); err != nil {
		return "", err
	}
	if stackID := firstKomodoID(stack); stackID != "" {
		return stackID, nil
	}
	return "", fmt.Errorf("komodo GetStack returned no stack id")
}

func (a *KomodoAdapter) Deploy(ctx context.Context, ref DeploymentRef) (string, error) {
	if err := a.validateConfig(); err != nil {
		return "", err
	}
	if ref.ExternalID == "" {
		return "", fmt.Errorf("komodo stack deploy requires external id")
	}
	payload := map[string]any{
		"stack":     ref.ExternalID,
		"services":  []string{},
		"stop_time": nil,
	}

	var lastErr error
	for attempt := 0; attempt < 24; attempt++ {
		var deployed map[string]any
		if _, _, err := a.client.postJSON(ctx, "/execute/DeployStack", payload, &deployed); err != nil {
			lastErr = err
		} else if rejected := komodoOperationError(deployed); rejected != nil {
			lastErr = rejected
		} else {
			deploymentID := firstKomodoID(deployed)
			if isKomodoOperationPending(deployed) && deploymentID != "" {
				if err := a.waitForUpdateComplete(ctx, ref.AppName, deploymentID); err != nil {
					return "", err
				}
			}
			return deploymentID, nil
		}
		if !isRetryableKomodoDeployError(lastErr) {
			return "", fmt.Errorf("komodo stack deploy %q: %w", ref.AppName, lastErr)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(komodoDeployRetryDelay):
		}
	}
	return "", fmt.Errorf("komodo stack deploy %q: %w", ref.AppName, lastErr)
}

func (a *KomodoAdapter) waitForUpdateComplete(ctx context.Context, appName, updateID string) error {
	payload := map[string]any{"id": updateID}
	var lastStatus string
	for attempt := 0; attempt < komodoDeployPollAttempt; attempt++ {
		var update map[string]any
		if _, _, err := a.client.postJSON(ctx, "/read/GetUpdate", payload, &update); err != nil {
			return fmt.Errorf("komodo stack deploy %q update %s: %w", appName, updateID, err)
		}
		if rejected := komodoOperationError(update); rejected != nil {
			return fmt.Errorf("komodo stack deploy %q update %s: %w", appName, updateID, rejected)
		}
		lastStatus = komodoOperationStatus(update)
		if isKomodoOperationComplete(update) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(komodoDeployPollDelay):
		}
	}
	if lastStatus == "" {
		lastStatus = "unknown"
	}
	return fmt.Errorf("komodo stack deploy %q update %s did not complete after %d polls (last status: %s)", appName, updateID, komodoDeployPollAttempt, lastStatus)
}

func (a *KomodoAdapter) Status(ctx context.Context, ref DeploymentRef) error {
	if err := a.validateConfig(); err != nil {
		return err
	}
	if ref.ExternalID == "" {
		return fmt.Errorf("komodo stack status requires external id")
	}
	if _, _, err := a.client.postJSON(ctx, "/read/GetStack", map[string]any{"stack": ref.ExternalID}, nil); err != nil {
		return fmt.Errorf("komodo stack status %q: %w", ref.AppName, err)
	}
	return nil
}

func (a *KomodoAdapter) validateConfig() error {
	if strings.TrimSpace(a.cfg.BaseURL) == "" {
		return fmt.Errorf("komodo adapter requires base URL")
	}
	if strings.TrimSpace(a.cfg.APIKey) == "" || strings.TrimSpace(a.cfg.Secret) == "" {
		return fmt.Errorf("komodo adapter requires api key and api secret")
	}
	return nil
}

func komodoStackConfig(manifest AppManifest, cfg HTTPConfig) map[string]any {
	config := map[string]any{
		"file_contents":         normalizeComposeYAML(manifest.ComposeYAML),
		"project_name":          manifest.Name,
		"auto_pull":             false,
		"destroy_before_deploy": false,
		"poll_for_updates":      false,
		"auto_update":           false,
		"send_alerts":           true,
	}
	if cfg.ServerID != "" {
		config["server_id"] = cfg.ServerID
	}
	if manifest.URL != "" {
		config["links"] = []string{manifest.URL}
	}
	return config
}

func firstKomodoID(values map[string]any) string {
	if id := firstString(values, "id", "_id", "uuid"); id != "" {
		return id
	}
	if raw, ok := values["_id"]; ok {
		if id := mongoID(raw); id != "" {
			return id
		}
	}
	return ""
}

func mongoID(value any) string {
	switch typed := value.(type) {
	case map[string]any:
		return firstString(typed, "$oid")
	case string:
		return typed
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			return ""
		}
		var payload map[string]any
		if json.Unmarshal(data, &payload) != nil {
			return ""
		}
		return firstString(payload, "$oid")
	}
}

func komodoOperationError(values map[string]any) error {
	if values == nil {
		return nil
	}
	status := komodoOperationStatus(values)
	if success, ok := boolValue(values["success"]); ok && !success {
		if status == "" || strings.EqualFold(status, "complete") {
			return fmt.Errorf("%s", komodoOperationMessage(values))
		}
	}
	normalized := strings.ToLower(status)
	if strings.Contains(normalized, "fail") || strings.Contains(normalized, "error") {
		return fmt.Errorf("%s", komodoOperationMessage(values))
	}
	return nil
}

func komodoOperationStatus(values map[string]any) string {
	if values == nil {
		return ""
	}
	return strings.TrimSpace(firstString(values, "status", "state"))
}

func isKomodoOperationPending(values map[string]any) bool {
	switch strings.ToLower(komodoOperationStatus(values)) {
	case "queued", "inprogress", "in_progress", "deploying":
		return true
	default:
		return false
	}
}

func isKomodoOperationComplete(values map[string]any) bool {
	switch strings.ToLower(komodoOperationStatus(values)) {
	case "complete", "completed":
		return true
	default:
		return false
	}
}

func boolValue(value any) (bool, bool) {
	switch typed := value.(type) {
	case bool:
		return typed, true
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "true":
			return true, true
		case "false":
			return false, true
		}
	}
	return false, false
}

func komodoOperationMessage(values map[string]any) string {
	for _, key := range []string{"error", "message", "logs", "status", "state"} {
		if text := komodoText(values[key]); text != "" {
			return text
		}
	}
	return "komodo operation failed"
}

func komodoText(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			if text := komodoText(item); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	case map[string]any:
		for _, key := range []string{"message", "msg", "stdout", "stderr", "stage"} {
			if text := komodoText(typed[key]); text != "" {
				return text
			}
		}
	}
	return ""
}

func isRetryableKomodoDeployError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "server is unreachable or disabled") ||
		strings.Contains(msg, "failed to get swarm or server") ||
		strings.Contains(msg, "server is unreachable")
}

// GenerateKomodoStackResource creates the bounded PoC artifact for Komodo
// Resource Sync. It intentionally does not make Komodo the default adapter.
func GenerateKomodoStackResource(manifest AppManifest) ([]byte, error) {
	if strings.TrimSpace(manifest.Name) == "" {
		return nil, fmt.Errorf("komodo stack resource requires app name")
	}
	if strings.TrimSpace(manifest.ComposeYAML) == "" {
		return nil, fmt.Errorf("komodo stack resource requires compose yaml")
	}

	var sb strings.Builder
	sb.WriteString("type: Stack\n")
	fmt.Fprintf(&sb, "name: %s\n", manifest.Name)
	sb.WriteString("tags:\n")
	sb.WriteString("  - stackkit\n")
	sb.WriteString("  - komodo-spike\n")
	sb.WriteString("config:\n")
	sb.WriteString("  file_contents: |\n")
	for _, line := range strings.Split(strings.TrimRight(manifest.ComposeYAML, "\n"), "\n") {
		sb.WriteString("    ")
		sb.WriteString(line)
		sb.WriteByte('\n')
	}
	return []byte(sb.String()), nil
}
