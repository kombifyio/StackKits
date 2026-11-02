package platformdeploy

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"
)

// DokployComposeState is the exact readback of one recorded compose service:
// its physical Compose project (Dokploy's suffixed appName), the raw recipe it
// executes and its deployment history.
type DokployComposeState struct {
	AppName     string
	Status      string
	ComposeFile string
	Deployments []DokployDeployment
}

type DokployDeployment struct {
	ID     string
	Status string
}

func (a *DokployAdapter) ComposeState(ctx context.Context, ref DeploymentRef) (DokployComposeState, error) {
	if err := validateDokployRef(ref); err != nil {
		return DokployComposeState{}, err
	}
	var observed struct {
		ComposeID     string `json:"composeId"`
		Name          string `json:"name"`
		AppName       string `json:"appName"`
		EnvironmentID string `json:"environmentId"`
		ServerID      string `json:"serverId"`
		Status        string `json:"composeStatus"`
		SourceType    string `json:"sourceType"`
		ComposeType   string `json:"composeType"`
		ComposeFile   string `json:"composeFile"`
		Randomize     bool   `json:"randomize"`
		Isolated      bool   `json:"isolatedDeployment"`
		Command       string `json:"command"`
		Domains       []any  `json:"domains"`
		Mounts        []any  `json:"mounts"`
		Deployments   []struct {
			DeploymentID string `json:"deploymentId"`
			Status       string `json:"status"`
		} `json:"deployments"`
	}
	path := "/api/compose.one?" + url.Values{"composeId": {ref.ExternalID}}.Encode()
	if _, _, err := a.client.getJSON(ctx, path, &observed); err != nil {
		return DokployComposeState{}, fmt.Errorf("dokploy compose readback %q: %w", ref.AppName, err)
	}
	// Domains, mounts, randomization, isolation and custom commands would
	// change the executed recipe or its routing beside the Kit router.
	if observed.ComposeID != ref.ExternalID || observed.Name != ref.AppName || observed.EnvironmentID != a.cfg.EnvironmentID ||
		observed.ServerID != a.cfg.ServerID || observed.SourceType != "raw" || observed.ComposeType != "docker-compose" ||
		observed.Randomize || observed.Isolated || observed.Command != "" || len(observed.Domains) != 0 || len(observed.Mounts) != 0 ||
		!dokployIDPattern.MatchString(observed.AppName) {
		return DokployComposeState{}, errors.New("dokploy compose readback differs from the recorded raw workload placement")
	}
	state := DokployComposeState{AppName: observed.AppName, Status: observed.Status, ComposeFile: observed.ComposeFile}
	for _, deployment := range observed.Deployments {
		state.Deployments = append(state.Deployments, DokployDeployment{ID: deployment.DeploymentID, Status: deployment.Status})
	}
	return state, nil
}

// Redeploy rebuilds the recorded compose service from its stored recipe.
func (a *DokployAdapter) Redeploy(ctx context.Context, ref DeploymentRef) error {
	if err := validateDokployRef(ref); err != nil {
		return err
	}
	payload := map[string]any{"composeId": ref.ExternalID, "title": "StackKit redeploy", "description": "Triggered by StackKit platform adapter"}
	if _, _, err := a.client.postJSON(ctx, "/api/compose.redeploy", payload, nil); err != nil {
		return fmt.Errorf("dokploy compose redeploy %q: %w", ref.AppName, err)
	}
	return nil
}

// WaitNewDeployment waits for a deployment record absent from previous and
// returns its ID once it is done. Dokploy queues deployments in memory, so a
// done compose status alone may still belong to an earlier deployment.
func (a *DokployAdapter) WaitNewDeployment(ctx context.Context, ref DeploymentRef, previous map[string]bool, timeout time.Duration) (string, error) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		state, err := a.ComposeState(ctx, ref)
		if err != nil {
			return "", err
		}
		for _, deployment := range state.Deployments {
			if previous[deployment.ID] {
				continue
			}
			switch deployment.Status {
			case "done":
				return deployment.ID, nil
			case "error", "cancelled":
				return "", fmt.Errorf("dokploy deployment %s of %q ended %s", deployment.ID, ref.AppName, deployment.Status)
			}
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-deadline.C:
			return "", fmt.Errorf("dokploy deployment of %q did not finish within %s", ref.AppName, timeout)
		case <-time.After(2 * time.Second):
		}
	}
}

// ComposeAbsent reports whether Dokploy no longer holds the recorded service.
// A present service must still be the recorded one.
func (a *DokployAdapter) ComposeAbsent(ctx context.Context, ref DeploymentRef) (bool, error) {
	if err := validateDokployRef(ref); err != nil {
		return false, err
	}
	status, _, err := a.client.getJSON(ctx, "/api/compose.one?"+url.Values{"composeId": {ref.ExternalID}}.Encode(), nil)
	if status == 404 {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("dokploy compose presence %q: %w", ref.AppName, err)
	}
	if _, err := a.ComposeState(ctx, ref); err != nil {
		return false, err
	}
	return false, nil
}

// EnvironmentComposeIDs returns the compose IDs Dokploy holds under name in
// the configured environment. A create whose identity was lost leaves such a
// service behind; it must be inspected, never shadowed by a second create.
func (a *DokployAdapter) EnvironmentComposeIDs(ctx context.Context, name string) ([]string, error) {
	if a.cfg.EnvironmentID == "" {
		return nil, errors.New("dokploy environment readback requires an environment id")
	}
	var environment struct {
		EnvironmentID string `json:"environmentId"`
		Compose       []struct {
			ComposeID string `json:"composeId"`
			Name      string `json:"name"`
		} `json:"compose"`
	}
	path := "/api/environment.one?" + url.Values{"environmentId": {a.cfg.EnvironmentID}}.Encode()
	if _, _, err := a.client.getJSON(ctx, path, &environment); err != nil {
		return nil, fmt.Errorf("dokploy environment readback: %w", err)
	}
	if environment.EnvironmentID != a.cfg.EnvironmentID {
		return nil, errors.New("dokploy environment readback differs from the configured environment")
	}
	var ids []string
	for _, compose := range environment.Compose {
		if compose.Name == name {
			ids = append(ids, compose.ComposeID)
		}
	}
	return ids, nil
}
