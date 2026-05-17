package commands

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

type tenantDeploymentEvent struct {
	RunID        string   `json:"runId,omitempty"`
	Phase        string   `json:"phase"`
	Status       string   `json:"status"`
	FailureClass string   `json:"failureClass,omitempty"`
	Actor        string   `json:"actor"`
	Message      string   `json:"message,omitempty"`
	ArtifactRefs []string `json:"artifactRefs,omitempty"`
}

func recordTenantDeploymentEvent(deploymentID, phase, status, message, failureClass string) {
	if strings.TrimSpace(deploymentID) == "" {
		return
	}
	if rolloutRecorder == nil && deployLog == nil {
		return
	}
	event := tenantDeploymentEvent{
		RunID:        currentRolloutRunID(),
		Phase:        phase,
		Status:       status,
		FailureClass: failureClass,
		Message:      message,
		ArtifactRefs: currentRolloutArtifactRefs(),
	}
	if err := reportTenantDeploymentEvent(deploymentID, event); err != nil {
		rolloutEvent("admin.report", "failed", err.Error(), map[string]string{
			"phase":  phase,
			"status": status,
		})
		if deployLog != nil {
			deployLog.Warn("admin.report",
				slog.String("phase", phase),
				slog.String("status", status),
				slog.String("error", err.Error()),
			)
		}
		return
	}
	rolloutEvent("admin.report", "succeeded", "tenant deployment progress event posted", map[string]string{
		"phase":  phase,
		"status": status,
	})
}

func reportTenantDeploymentEvent(deploymentID string, event tenantDeploymentEvent) error {
	endpoint := resolveAdminEndpoint()
	if endpoint == "" {
		return fmt.Errorf("no --admin-endpoint, STACKKIT_ADMIN_ENDPOINT, or STACKKIT_ADMIN_URL configured")
	}
	token := resolveTenantDeploymentToken()
	if token == "" {
		return fmt.Errorf("no STACKKIT_BOOTSTRAP_TOKEN, --admin-token, or STACKKIT_ADMIN_TOKEN configured")
	}
	if strings.TrimSpace(event.Actor) == "" {
		event.Actor = "stackkit-cli"
	}
	if strings.TrimSpace(event.RunID) == "" {
		event.RunID = currentRolloutRunID()
	}

	body, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal deployment event: %w", err)
	}

	url := fmt.Sprintf("%s/api/v1/sk/tenants/deployments/%s/events", strings.TrimRight(endpoint, "/"), deploymentID)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body)) // #nosec G107 G704 -- endpoint is an operator-supplied CLI flag.
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req) // #nosec G107 G704 -- request URL is operator-supplied CLI endpoint.
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		rolloutEvent("admin.report", "skipped", "tenant deployment event endpoint unsupported", map[string]string{
			"phase": event.Phase,
		})
		return nil
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("admin event endpoint returned %d", resp.StatusCode)
	}
	return nil
}

func currentRolloutRunID() string {
	if rolloutRecorder != nil && rolloutRecorder.RunID() != "" {
		return rolloutRecorder.RunID()
	}
	if deployLog != nil && deployLog.RunID() != "" {
		return deployLog.RunID()
	}
	return ""
}

func currentRolloutArtifactRefs() []string {
	refs := []string{}
	if rolloutRecorder != nil && rolloutRecorder.Root() != "" {
		refs = append(refs, rolloutRecorder.Root())
	}
	if deployLog != nil && deployLog.LogPath() != "" {
		refs = append(refs, filepath.Clean(deployLog.LogPath()))
	}
	return refs
}
