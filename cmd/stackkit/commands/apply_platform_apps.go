package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kombifyio/stackkits/internal/platformdeploy"
	"github.com/kombifyio/stackkits/pkg/models"
)

func runPlatformAppDeployments(ctx context.Context, deployDir string, state *models.DeploymentState) error {
	manifestPaths := []string{
		filepath.Join(deployDir, "platform-apps", "manifest.json"),
		filepath.Join(deployDir, ".platform-apps-manifest.json"),
	}

	for _, manifestPath := range manifestPaths {
		if _, err := os.Stat(manifestPath); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return fmt.Errorf("inspect platform app manifest: %w", err)
		}

		bundle, err := platformdeploy.LoadBundleManifest(manifestPath)
		if err != nil {
			return err
		}
		if len(bundle.SystemApps) == 0 && len(bundle.Apps) == 0 {
			continue
		}

		adapter, configured, err := platformAdapterForBundle(bundle, deployDir)
		if err != nil {
			return err
		}
		if !configured {
			if len(bundle.Apps) > 0 {
				return fmt.Errorf("platform app rollout for %s requires API endpoint and token when user apps are present; set STACKKIT_PLATFORM_ENDPOINT and STACKKIT_PLATFORM_TOKEN, or provider-specific variables, then rerun stackkit apply", bundle.Platform)
			}
			printWarning("Platform app manifests were generated for %s, but API endpoint/token settings are missing; skipping adapter rollout", bundle.Platform)
			printWarning("Set STACKKIT_PLATFORM_ENDPOINT and STACKKIT_PLATFORM_TOKEN, or provider-specific variables, then rerun stackkit apply")
			continue
		}

		printInfo("Deploying %d system app(s) and %d user app(s) through %s platform adapter...", len(bundle.SystemApps), len(bundle.Apps), bundle.Platform)
		refs, err := platformdeploy.ApplyBundle(ctx, adapter, bundle)
		if err != nil {
			return err
		}
		recordPlatformAppState(state, bundle, refs)
		printSuccess("Platform app rollout complete: %s", bundle.Platform)
	}

	return nil
}

func platformAdapterForBundle(bundle platformdeploy.BundleManifest, deployDir string) (platformdeploy.Adapter, bool, error) {
	switch bundle.Platform {
	case models.PAASNone:
		return platformdeploy.NewLocalComposeAdapter(deployDir), true, nil
	case models.PAASDokploy:
		cfg, configured := platformHTTPConfigForBundle(bundle, deployDir)
		if !configured {
			return nil, false, nil
		}
		return platformdeploy.NewDokployAdapter(cfg), true, nil
	case models.PAASCoolify:
		cfg, configured := platformHTTPConfigForBundle(bundle, deployDir)
		if !configured {
			return nil, false, nil
		}
		return platformdeploy.NewCoolifyAdapter(cfg), true, nil
	default:
		return nil, false, fmt.Errorf("unsupported platform app adapter %q", bundle.Platform)
	}
}

type platformConfigFile struct {
	Platform        string `json:"platform"`
	Endpoint        string `json:"endpoint"`
	BaseURL         string `json:"baseUrl"`
	Token           string `json:"token"`
	EnvironmentID   string `json:"environmentId"`
	ServerID        string `json:"serverId"`
	ProjectUUID     string `json:"projectUuid"`
	EnvironmentUUID string `json:"environmentUuid"`
	DestinationUUID string `json:"destinationUuid"`
}

func platformHTTPConfigForBundle(bundle platformdeploy.BundleManifest, deployDir string) (platformdeploy.HTTPConfig, bool) {
	persisted := loadPlatformConfigFile(bundle.Platform, deployDir)
	cfg := platformdeploy.HTTPConfig{
		BaseURL:         firstNonEmpty(firstPlatformEnv(bundle.Platform, "endpoint"), persisted.endpoint()),
		Token:           firstNonEmpty(firstPlatformEnv(bundle.Platform, "token"), persisted.Token),
		EnvironmentID:   firstNonEmpty(firstPlatformEnv(bundle.Platform, "environment_id"), persisted.EnvironmentID),
		ServerID:        firstNonEmpty(firstPlatformEnv(bundle.Platform, "server_id"), persisted.ServerID),
		ProjectUUID:     firstNonEmpty(firstPlatformEnv(bundle.Platform, "project_uuid"), persisted.ProjectUUID),
		EnvironmentUUID: firstNonEmpty(firstPlatformEnv(bundle.Platform, "environment_uuid"), persisted.EnvironmentUUID),
		DestinationUUID: firstNonEmpty(firstPlatformEnv(bundle.Platform, "destination_uuid"), persisted.DestinationUUID),
	}
	return cfg, cfg.BaseURL != "" && cfg.Token != ""
}

func (cfg platformConfigFile) endpoint() string {
	return firstNonEmpty(cfg.Endpoint, cfg.BaseURL)
}

func loadPlatformConfigFile(platform, deployDir string) platformConfigFile {
	for _, path := range platformConfigCandidates(deployDir) {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var cfg platformConfigFile
		if err := json.Unmarshal(data, &cfg); err != nil {
			continue
		}
		if cfg.Platform == "" || strings.EqualFold(cfg.Platform, platform) {
			return cfg
		}
	}
	return platformConfigFile{}
}

func platformConfigCandidates(deployDir string) []string {
	return []string{
		filepath.Join(filepath.Dir(deployDir), ".stackkit", "platform.json"),
		filepath.Join(deployDir, ".stackkit", "platform.json"),
		filepath.Join(deployDir, "platform.json"),
	}
}

func firstPlatformEnv(platform, field string) string {
	switch field {
	case "endpoint":
		if platform == models.PAASDokploy {
			return firstEnv("DOKPLOY_API_URL", "STACKKIT_PLATFORM_ENDPOINT")
		}
		if platform == models.PAASCoolify {
			return firstEnv("COOLIFY_API_URL", "STACKKIT_PLATFORM_ENDPOINT")
		}
	case "token":
		if platform == models.PAASDokploy {
			return firstEnv("DOKPLOY_API_KEY", "STACKKIT_PLATFORM_TOKEN")
		}
		if platform == models.PAASCoolify {
			return firstEnv("COOLIFY_API_TOKEN", "STACKKIT_PLATFORM_TOKEN")
		}
	case "environment_id":
		if platform == models.PAASDokploy {
			return firstEnv("DOKPLOY_ENVIRONMENT_ID", "STACKKIT_PLATFORM_ENVIRONMENT_ID")
		}
		if platform == models.PAASCoolify {
			return firstEnv("COOLIFY_ENVIRONMENT_NAME", "STACKKIT_PLATFORM_ENVIRONMENT_NAME")
		}
	case "server_id":
		if platform == models.PAASDokploy {
			return firstEnv("DOKPLOY_SERVER_ID", "STACKKIT_PLATFORM_SERVER_ID")
		}
		if platform == models.PAASCoolify {
			return firstEnv("COOLIFY_SERVER_UUID", "STACKKIT_PLATFORM_SERVER_UUID", "STACKKIT_PLATFORM_SERVER_ID")
		}
	case "project_uuid":
		return firstEnv("COOLIFY_PROJECT_UUID", "STACKKIT_PLATFORM_PROJECT_UUID")
	case "environment_uuid":
		return firstEnv("COOLIFY_ENVIRONMENT_UUID", "STACKKIT_PLATFORM_ENVIRONMENT_UUID")
	case "destination_uuid":
		return firstEnv("COOLIFY_DESTINATION_UUID", "STACKKIT_PLATFORM_DESTINATION_UUID")
	}
	return ""
}

func recordPlatformAppState(state *models.DeploymentState, bundle platformdeploy.BundleManifest, refs []platformdeploy.DeploymentRef) {
	if state == nil {
		return
	}
	composePaths := make(map[string]string, len(bundle.Apps))
	setupPolicies := make(map[string]string, len(bundle.Apps))
	setupDrops := make(map[string][]platformdeploy.SetupDropManifest, len(bundle.Apps))
	for _, app := range bundle.Apps {
		composePaths[app.Name] = app.ComposePath
		setupPolicies[app.Name] = app.SetupPolicy
		setupDrops[app.Name] = app.SetupDrops
	}
	systemComposePaths := make(map[string]string, len(bundle.SystemApps))
	systemRoles := make(map[string]string, len(bundle.SystemApps))
	systemSetupPolicies := make(map[string]string, len(bundle.SystemApps))
	systemSetupDrops := make(map[string][]platformdeploy.SetupDropManifest, len(bundle.SystemApps))
	for _, app := range bundle.SystemApps {
		systemComposePaths[app.Name] = app.ComposePath
		systemRoles[app.Name] = app.Role
		systemSetupPolicies[app.Name] = app.SetupPolicy
		systemSetupDrops[app.Name] = app.SetupDrops
	}
	for _, ref := range refs {
		appState := models.PlatformAppState{
			Name:         ref.AppName,
			Platform:     ref.Platform,
			ExternalID:   ref.ExternalID,
			DeploymentID: ref.DeploymentID,
			LastDeployed: ref.LastDeployed,
		}
		if role, ok := systemRoles[ref.AppName]; ok {
			appState.Role = role
			appState.ComposePath = systemComposePaths[ref.AppName]
			appState.SetupPolicy = systemSetupPolicies[ref.AppName]
			appState.SetupDrops = setupDropsToState(systemSetupDrops[ref.AppName])
			state.PlatformSystemApps = upsertPlatformAppState(state.PlatformSystemApps, appState)
			continue
		}
		appState.ComposePath = composePaths[ref.AppName]
		appState.SetupPolicy = setupPolicies[ref.AppName]
		appState.SetupDrops = setupDropsToState(setupDrops[ref.AppName])
		state.PlatformApps = upsertPlatformAppState(state.PlatformApps, appState)
	}
}

func upsertPlatformAppState(states []models.PlatformAppState, next models.PlatformAppState) []models.PlatformAppState {
	for i, existing := range states {
		if existing.Name == next.Name && existing.Platform == next.Platform && existing.Role == next.Role {
			states[i] = next
			return states
		}
	}
	return append(states, next)
}

func setupDropsToState(drops []platformdeploy.SetupDropManifest) []models.SetupDropSpec {
	if len(drops) == 0 {
		return nil
	}
	stateDrops := make([]models.SetupDropSpec, 0, len(drops))
	for _, drop := range drops {
		stateDrops = append(stateDrops, models.SetupDropSpec{
			Name:        drop.Name,
			Version:     drop.Version,
			Runner:      drop.Runner,
			Description: drop.Description,
			Command:     append([]string(nil), drop.Command...),
			Env:         drop.Env,
			Secrets:     drop.Secrets,
		})
	}
	return stateDrops
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func firstEnv(keys ...string) string {
	for _, key := range keys {
		if value := os.Getenv(key); value != "" {
			return value
		}
	}
	return ""
}
