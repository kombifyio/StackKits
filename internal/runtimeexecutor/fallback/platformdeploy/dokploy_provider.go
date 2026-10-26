package platformdeploy

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
)

var dokployIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// DokployProviderCompose retains the API's physical identity. AppName is the
// randomized Docker project name, not the display name in DeploymentRef.
type DokployProviderCompose struct {
	ComposeID                 string            `json:"composeId"`
	Name                      string            `json:"name"`
	AppName                   string            `json:"appName"`
	EnvironmentID             string            `json:"environmentId"`
	ServerID                  string            `json:"serverId"`
	Status                    string            `json:"composeStatus"`
	ComposeType               string            `json:"composeType"`
	SourceType                string            `json:"sourceType"`
	ComposeFile               string            `json:"composeFile"`
	Description               string            `json:"description"`
	Env                       string            `json:"env"`
	Command                   string            `json:"command"`
	AutoDeploy                bool              `json:"autoDeploy"`
	Randomize                 bool              `json:"randomize"`
	IsolatedDeployment        bool              `json:"isolatedDeployment"`
	IsolatedDeploymentsVolume bool              `json:"isolatedDeploymentsVolume"`
	Domains                   []json.RawMessage `json:"domains"`
	Mounts                    []json.RawMessage `json:"mounts"`
	ServiceNetworks           []json.RawMessage `json:"serviceNetworks"`
}

type DokployProviderDeployment struct {
	DeploymentID string `json:"deploymentId"`
	Status       string `json:"status"`
}

// LatestProviderDeployment deliberately fails closed. The provider's waiter
// tolerates an unavailable history endpoint and can accept an older done
// status, so independent acceptance must retain the actual successful run ID.
func (a *DokployAdapter) LatestProviderDeployment(ctx context.Context, ref DeploymentRef) (DokployProviderDeployment, error) {
	var empty DokployProviderDeployment
	if validateDokployRef(ref) != nil {
		return empty, errors.New("Dokploy deployment history requires the held Compose identity")
	}
	var history []DokployProviderDeployment
	path := "/api/deployment.allByType?" + url.Values{"id": {ref.ExternalID}, "type": {"compose"}}.Encode()
	if _, _, err := a.client.getJSON(ctx, path, &history); err != nil || len(history) == 0 ||
		!dokployIDPattern.MatchString(history[0].DeploymentID) || history[0].Status != "done" {
		return empty, errors.New("Dokploy requires an independently observed successful deployment-history record")
	}
	return history[0], nil
}

// RequireProviderEnvironment authenticates the bootstrap key and checks the
// exact parent relationship before importing any workload. Names never select
// a project or environment. An empty serverId means Dokploy's own host.
func (a *DokployAdapter) RequireProviderEnvironment(ctx context.Context, projectID string) error {
	if !dokployIDPattern.MatchString(projectID) || !dokployIDPattern.MatchString(a.cfg.EnvironmentID) {
		return errors.New("Dokploy provider requires the bootstrap's exact projectId and environmentId")
	}
	var project struct {
		ProjectID    string `json:"projectId"`
		Environments []struct {
			EnvironmentID string `json:"environmentId"`
			ProjectID     string `json:"projectId"`
		} `json:"environments"`
	}
	path := "/api/project.one?" + url.Values{"projectId": {projectID}}.Encode()
	if _, _, err := a.client.getJSON(ctx, path, &project); err != nil {
		// Platform response bodies can contain credentials. Keep this public
		// preparation boundary independent of the legacy error snippets.
		return errors.New("Dokploy project readback failed; check the existing bootstrap API key and private endpoint")
	}
	if project.ProjectID != projectID {
		return errors.New("Dokploy project readback differs from bootstrap custody")
	}
	matches := 0
	for _, environment := range project.Environments {
		if environment.EnvironmentID == a.cfg.EnvironmentID && environment.ProjectID == projectID {
			matches++
		}
	}
	if matches != 1 {
		return errors.New("Dokploy environment is absent, ambiguous or outside the custodied project")
	}
	return nil
}

// RequireProviderCompose is read-only. It never creates, adopts by name,
// deploys or removes a service. The existing adapter remains the API owner.
func (a *DokployAdapter) RequireProviderCompose(ctx context.Context, ref DeploymentRef) (DokployProviderCompose, error) {
	var observed DokployProviderCompose
	if !dokployIDPattern.MatchString(ref.ExternalID) || validateDokployRef(ref) != nil {
		return observed, errors.New("Dokploy provider requires an explicit recorded composeId")
	}
	path := "/api/compose.one?" + url.Values{"composeId": {ref.ExternalID}}.Encode()
	if _, _, err := a.client.getJSON(ctx, path, &observed); err != nil {
		return DokployProviderCompose{}, errors.New("Dokploy Compose readback failed; preserve its held composeId and reconcile before retrying")
	}
	if observed.ComposeID != ref.ExternalID || observed.Name != ref.AppName ||
		observed.EnvironmentID != a.cfg.EnvironmentID || observed.ServerID != a.cfg.ServerID {
		return DokployProviderCompose{}, errors.New("Dokploy Compose identity or placement differs from node custody")
	}
	if observed.Status != "done" || observed.ComposeType != "docker-compose" || observed.SourceType != "raw" || observed.ComposeFile == "" {
		return DokployProviderCompose{}, errors.New("Dokploy provider preparation requires a completed raw docker-compose deployment; this is not an application health assertion")
	}
	if observed.Env != "" {
		return DokployProviderCompose{}, errors.New("Dokploy service has platform-held environment values; deliver secrets through node files before provider preparation")
	}
	if observed.Command != "" || observed.AutoDeploy || observed.Randomize || observed.IsolatedDeployment || observed.IsolatedDeploymentsVolume ||
		len(observed.Domains) != 0 || len(observed.Mounts) != 0 || len(observed.ServiceNetworks) != 0 {
		return DokployProviderCompose{}, errors.New("Dokploy node recipe refuses platform domains, mounts, network injection, automatic deployment, custom commands or isolation/randomization; preserve existing routing and volume ownership")
	}
	return observed, nil
}
