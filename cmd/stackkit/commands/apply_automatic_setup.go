package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/config"
	"github.com/kombifyio/stackkits/internal/docker"
	"github.com/kombifyio/stackkits/internal/platformdeploy"
	"github.com/kombifyio/stackkits/pkg/models"
)

const (
	automaticNodeSetupTimeout       = 90 * time.Second
	automaticNodeSetupRetryInterval = 3 * time.Second
)

type automaticNodeSetupAction struct {
	ServiceKey string
	AppName    string
}

type automaticNodeSetupResult struct {
	Status  string                         `json:"status"`
	Message string                         `json:"message"`
	Drops   []automaticNodeSetupDropResult `json:"drops"`
}

type automaticNodeSetupDropResult struct {
	Name   string `json:"name"`
	RunID  string `json:"runId"`
	Status string `json:"status"`
	Phase  string `json:"phase"`
}

type automaticNodeSetupTarget struct {
	Endpoint   string
	HostHeader string
	Source     string
}

var detectLocalStackKitServerBaseURL = defaultLocalStackKitServerBaseURL

func runAutomaticNodeSetupActions(ctx context.Context, access *accessSummary, state *models.DeploymentState, loader *config.Loader, stateFile string) (*models.DeploymentState, error) {
	actions := automaticNodeSetupActions(state)
	if len(actions) == 0 {
		return state, nil
	}
	if access == nil || strings.TrimSpace(access.HubURL) == "" {
		return state, fmt.Errorf("automatic setup actions require a generated Base Hub URL")
	}
	if loader == nil {
		return state, fmt.Errorf("automatic setup actions require a deployment state loader")
	}
	if strings.TrimSpace(stateFile) == "" {
		return state, fmt.Errorf("automatic setup actions require a deployment state path")
	}

	if err := os.MkdirAll(filepath.Dir(stateFile), 0750); err != nil {
		return state, fmt.Errorf("prepare deployment state for automatic setup actions: %w", err)
	}
	if err := loader.SaveDeploymentState(state, stateFile); err != nil {
		return state, fmt.Errorf("save deployment state before automatic setup actions: %w", err)
	}

	for _, action := range actions {
		printInfo("Running automatic setup for %s...", action.ServiceKey)
		result, err := runAutomaticNodeSetupAction(ctx, access.HubURL, action.ServiceKey)
		if err != nil {
			return state, fmt.Errorf("%s automatic setup action failed: %w", action.ServiceKey, err)
		}
		if result.Status == models.SetupRunStatusWaiting {
			printInfo("Automatic setup waiting for Owner activation: %s", action.ServiceKey)
			continue
		}
		printSuccess("Automatic setup completed: %s", action.ServiceKey)
	}

	reloaded, err := loader.LoadDeploymentState(stateFile)
	if err != nil {
		return state, fmt.Errorf("reload deployment state after automatic setup actions: %w", err)
	}
	if reloaded != nil {
		return reloaded, nil
	}
	return state, nil
}

func automaticNodeSetupActions(state *models.DeploymentState) []automaticNodeSetupAction {
	if state == nil {
		return nil
	}
	actions := make([]automaticNodeSetupAction, 0)
	seen := map[string]bool{}
	for _, app := range state.PlatformApps {
		if app.SetupPolicy != platformdeploy.SetupPolicyAutomatic {
			continue
		}
		serviceKey := setupRunServiceKeyForAppName(app.Name)
		if serviceKey == "" || seen[serviceKey] {
			continue
		}
		if !hasPendingStackKitScriptDrop(state, app, serviceKey) {
			continue
		}
		seen[serviceKey] = true
		actions = append(actions, automaticNodeSetupAction{
			ServiceKey: serviceKey,
			AppName:    app.Name,
		})
	}
	return actions
}

func hasPendingStackKitScriptDrop(state *models.DeploymentState, app models.PlatformAppState, serviceKey string) bool {
	for _, drop := range app.SetupDrops {
		if drop.Runner != "stackkit-script" {
			continue
		}
		if idx := findDeploymentSetupRunIndex(state.SetupRuns, serviceKey, app.Name, drop.Name); idx >= 0 {
			run := state.SetupRuns[idx]
			if run.Status == models.SetupRunStatusCompleted {
				continue
			}
		}
		return true
	}
	return false
}

func setupRunServiceKeyForAppName(appName string) string {
	switch strings.ToLower(strings.TrimSpace(appName)) {
	case "uptime-kuma":
		return "kuma"
	case "vaultwarden":
		return "vault"
	case "immich":
		return "photos"
	case "cloudreve", "nextcloud":
		return "files"
	case "stackkit-hub":
		return "home"
	default:
		return strings.TrimSpace(appName)
	}
}

func runAutomaticNodeSetupAction(ctx context.Context, hubURL, serviceKey string) (automaticNodeSetupResult, error) {
	actionCtx, cancel := context.WithTimeout(ctx, automaticNodeSetupTimeout)
	defer cancel()

	client := &http.Client{Timeout: 20 * time.Second}
	var last string
	for {
		target, targetErr := automaticNodeSetupTargetForService(actionCtx, hubURL, serviceKey)
		if targetErr != nil {
			return automaticNodeSetupResult{}, targetErr
		}
		status, body, err := postAutomaticNodeSetupTarget(actionCtx, client, target)
		if err == nil && status >= 200 && status < 300 {
			result, parseErr := parseAutomaticNodeSetupResponse(body)
			if parseErr != nil {
				return automaticNodeSetupResult{}, parseErr
			}
			return result, nil
		}
		last = automaticSetupAttemptMessage(target.Source, status, body, err)
		if !automaticSetupRetryable(status, err) {
			return automaticNodeSetupResult{}, fmt.Errorf("%s", last)
		}

		select {
		case <-actionCtx.Done():
			return automaticNodeSetupResult{}, fmt.Errorf("%s after %s: %w", last, automaticNodeSetupTimeout, actionCtx.Err())
		case <-time.After(automaticNodeSetupRetryInterval):
		}
	}
}

func parseAutomaticNodeSetupResponse(body string) (automaticNodeSetupResult, error) {
	var envelope struct {
		Data automaticNodeSetupResult `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		return automaticNodeSetupResult{}, fmt.Errorf("parse automatic setup response: %w", err)
	}
	if strings.TrimSpace(envelope.Data.Status) == "" {
		return automaticNodeSetupResult{}, fmt.Errorf("automatic setup response missing status")
	}
	return envelope.Data, nil
}

func postAutomaticNodeSetupTarget(ctx context.Context, client *http.Client, target automaticNodeSetupTarget) (int, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.Endpoint, nil)
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Accept", "application/json")
	if target.HostHeader != "" {
		req.Host = target.HostHeader
	}

	resp, err := client.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	data, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if readErr != nil {
		return resp.StatusCode, "", readErr
	}
	return resp.StatusCode, strings.TrimSpace(string(data)), nil
}

func automaticNodeSetupTargetForService(ctx context.Context, hubURL, serviceKey string) (automaticNodeSetupTarget, error) {
	endpoint, hostHeader, err := setupActionRequestTarget(hubURL, serviceKey)
	if err != nil {
		return automaticNodeSetupTarget{}, err
	}
	publicTarget := automaticNodeSetupTarget{
		Endpoint:   endpoint,
		HostHeader: hostHeader,
		Source:     "public-hub",
	}
	parsed, err := url.Parse(strings.TrimSpace(hubURL))
	if err != nil || parsed.Hostname() == "" || isLocalhostName(parsed.Hostname()) {
		return publicTarget, nil
	}

	if baseURL := detectLocalStackKitServerBaseURL(ctx); baseURL != "" {
		localEndpoint, _, localErr := setupActionRequestTarget(baseURL, serviceKey)
		if localErr == nil {
			return automaticNodeSetupTarget{
				Endpoint: localEndpoint,
				Source:   "local-stackkit-server",
			}, nil
		}
	}
	return publicTarget, nil
}

func defaultLocalStackKitServerBaseURL(ctx context.Context) string {
	client := docker.NewClient(docker.WithTimeout(5 * time.Second))
	ip, err := client.ContainerIPAddress(ctx, "stackkit-server")
	if err != nil {
		return ""
	}
	if strings.TrimSpace(ip) == "" {
		return "http://127.0.0.1:8082"
	}
	return "http://" + net.JoinHostPort(strings.TrimSpace(ip), "8082")
}

func setupActionRequestTarget(hubURL, serviceKey string) (endpoint string, hostHeader string, err error) {
	parsed, err := url.Parse(strings.TrimSpace(hubURL))
	if err != nil {
		return "", "", err
	}
	if parsed.Scheme == "" || parsed.Hostname() == "" {
		return "", "", fmt.Errorf("invalid Base Hub URL %q", hubURL)
	}

	hostHeader = ""
	if isLocalhostName(parsed.Hostname()) {
		hostHeader = parsed.Host
		if port := parsed.Port(); port != "" {
			parsed.Host = net.JoinHostPort("127.0.0.1", port)
		} else {
			parsed.Host = "127.0.0.1"
		}
	}
	parsed.Path = "/api/v1/setup/services/" + url.PathEscape(serviceKey) + "/run"
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), hostHeader, nil
}

func isLocalhostName(hostname string) bool {
	name := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(hostname), "."))
	return name == "localhost" || strings.HasSuffix(name, ".localhost")
}

func automaticSetupRetryable(status int, err error) bool {
	if err != nil {
		return true
	}
	switch status {
	case http.StatusRequestTimeout, http.StatusTooEarly, http.StatusTooManyRequests,
		http.StatusNotFound, http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func automaticSetupAttemptMessage(source string, status int, body string, err error) string {
	source = strings.TrimSpace(source)
	if source == "" {
		source = "setup endpoint"
	}
	if err != nil {
		return fmt.Sprintf("%s request failed: %s", source, err.Error())
	}
	body = string(bytes.TrimSpace([]byte(body)))
	if body == "" {
		return fmt.Sprintf("%s returned HTTP %d", source, status)
	}
	if len(body) > 4000 {
		body = body[:4000] + "...(truncated)"
	}
	return fmt.Sprintf("%s returned HTTP %d: %s", source, status, body)
}
