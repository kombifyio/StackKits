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
		if len(bundle.Apps) > 0 {
			recordPlatformAppHandoffs(state, bundle)
			printInfo("Recorded %d user app handoff(s) for %s. Deploy user apps through the PaaS; StackKit does not manage their lifecycle.", len(bundle.Apps), bundle.Platform)
		}
		if len(bundle.SystemApps) == 0 {
			continue
		}

		adapter, configured, err := platformAdapterForBundle(bundle, deployDir)
		if err != nil {
			return err
		}
		if !configured {
			printWarning("Platform system app manifests were generated for %s, but API endpoint/token settings are missing; skipping adapter rollout", bundle.Platform)
			printWarning("Set STACKKIT_PLATFORM_ENDPOINT and STACKKIT_PLATFORM_TOKEN, or provider-specific variables, then rerun stackkit apply")
			continue
		}

		printInfo("Deploying %d StackKit system app(s) through %s platform adapter...", len(bundle.SystemApps), bundle.Platform)
		systemBundle := bundle
		systemBundle.Apps = nil
		refs, err := platformdeploy.ApplyBundle(ctx, adapter, systemBundle)
		if err != nil {
			return err
		}
		recordPlatformAppState(state, bundle, refs)
		printSuccess("Platform system app rollout complete: %s", bundle.Platform)
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
	Platform        string `json:"platform,omitempty"`
	Endpoint        string `json:"endpoint,omitempty"`
	BaseURL         string `json:"baseUrl,omitempty"`
	Token           string `json:"token,omitempty"`
	EnvironmentID   string `json:"environmentId,omitempty"`
	ServerID        string `json:"serverId,omitempty"`
	ProjectUUID     string `json:"projectUuid,omitempty"`
	EnvironmentUUID string `json:"environmentUuid,omitempty"`
	DestinationUUID string `json:"destinationUuid,omitempty"`
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

func writePlatformConfigFile(wd string, cfg platformConfigFile) error {
	if cfg.Platform == "" {
		cfg.Platform = models.PAASCoolify
	}
	path := filepath.Join(wd, ".stackkit", "platform.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create platform config directory: %w", err)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal platform config: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0600); err != nil {
		return fmt.Errorf("write platform config: %w", err)
	}
	return nil
}

func persistPlatformConfigFromSpecEnvironment(spec *models.StackSpec, wd string) error {
	if spec == nil || len(spec.Environment) == 0 {
		return nil
	}
	cfg, anyConfigured := platformConfigFromValueMap(spec.PAAS, spec.Environment)
	if !anyConfigured {
		return nil
	}
	if err := validatePlatformConfigPlatform(cfg.Platform); err != nil {
		return err
	}
	if cfg.endpoint() == "" || cfg.Token == "" {
		return fmt.Errorf("tenant spec platform config is incomplete; provide STACKKIT_PLATFORM_ENDPOINT and STACKKIT_PLATFORM_TOKEN or provider-specific endpoint/token values")
	}
	if err := writePlatformConfigFile(wd, cfg); err != nil {
		return err
	}
	redactPlatformConfigEnvironment(spec.Environment)
	return nil
}

func platformConfigFromValueMap(defaultPlatform string, values map[string]string) (platformConfigFile, bool) {
	platform, platformSeen := platformFromValueMap(defaultPlatform, values)
	cfg := platformConfigFile{
		Platform:        platform,
		Endpoint:        firstPlatformValue(platform, values, "endpoint"),
		Token:           firstPlatformValue(platform, values, "token"),
		EnvironmentID:   firstPlatformValue(platform, values, "environment_id"),
		ServerID:        firstPlatformValue(platform, values, "server_id"),
		ProjectUUID:     firstPlatformValue(platform, values, "project_uuid"),
		EnvironmentUUID: firstPlatformValue(platform, values, "environment_uuid"),
		DestinationUUID: firstPlatformValue(platform, values, "destination_uuid"),
	}
	return cfg, platformSeen || cfg.endpoint() != "" || cfg.Token != "" ||
		cfg.EnvironmentID != "" || cfg.ServerID != "" || cfg.ProjectUUID != "" ||
		cfg.EnvironmentUUID != "" || cfg.DestinationUUID != ""
}

func platformFromValueMap(defaultPlatform string, values map[string]string) (string, bool) {
	for _, key := range []string{"STACKKIT_PLATFORM", "STACKKIT_PAAS"} {
		if value := strings.TrimSpace(values[key]); value != "" {
			return strings.ToLower(value), true
		}
	}
	if values["COOLIFY_API_URL"] != "" || values["COOLIFY_API_TOKEN"] != "" {
		return models.PAASCoolify, true
	}
	if values["DOKPLOY_API_URL"] != "" || values["DOKPLOY_API_KEY"] != "" {
		return models.PAASDokploy, true
	}
	defaultPlatform = strings.ToLower(strings.TrimSpace(defaultPlatform))
	if defaultPlatform == models.PAASCoolify || defaultPlatform == models.PAASDokploy {
		return defaultPlatform, false
	}
	if genericPlatformConfigPresent(values) {
		return models.PAASCoolify, false
	}
	if defaultPlatform != "" {
		return defaultPlatform, false
	}
	return models.PAASCoolify, false
}

func validatePlatformConfigPlatform(platform string) error {
	switch platform {
	case models.PAASCoolify, models.PAASDokploy:
		return nil
	default:
		return fmt.Errorf("unsupported tenant spec platform config %q; expected coolify or dokploy", platform)
	}
}

func genericPlatformConfigPresent(values map[string]string) bool {
	for _, key := range []string{
		"STACKKIT_PLATFORM_ENDPOINT",
		"STACKKIT_PLATFORM_TOKEN",
		"STACKKIT_PLATFORM_ENVIRONMENT_ID",
		"STACKKIT_PLATFORM_ENVIRONMENT_NAME",
		"STACKKIT_PLATFORM_SERVER_ID",
		"STACKKIT_PLATFORM_SERVER_UUID",
		"STACKKIT_PLATFORM_PROJECT_UUID",
		"STACKKIT_PLATFORM_ENVIRONMENT_UUID",
		"STACKKIT_PLATFORM_DESTINATION_UUID",
	} {
		if strings.TrimSpace(values[key]) != "" {
			return true
		}
	}
	return false
}

func firstPlatformValue(platform string, values map[string]string, field string) string {
	for _, key := range platformConfigEnvKeys(platform, field) {
		if value := strings.TrimSpace(values[key]); value != "" {
			return value
		}
	}
	return ""
}

func redactPlatformConfigEnvironment(values map[string]string) {
	for _, key := range allPlatformConfigEnvKeys() {
		delete(values, key)
	}
}

func allPlatformConfigEnvKeys() []string {
	seen := map[string]bool{}
	var keys []string
	for _, platform := range []string{models.PAASCoolify, models.PAASDokploy} {
		for _, field := range []string{"endpoint", "token", "environment_id", "server_id", "project_uuid", "environment_uuid", "destination_uuid"} {
			for _, key := range platformConfigEnvKeys(platform, field) {
				if !seen[key] {
					seen[key] = true
					keys = append(keys, key)
				}
			}
		}
	}
	for _, key := range []string{"STACKKIT_PLATFORM", "STACKKIT_PAAS"} {
		if !seen[key] {
			keys = append(keys, key)
		}
	}
	return keys
}

func platformConfigEnvKeys(platform, field string) []string {
	switch field {
	case "endpoint":
		if platform == models.PAASDokploy {
			return []string{"DOKPLOY_API_URL", "STACKKIT_PLATFORM_ENDPOINT"}
		}
		if platform == models.PAASCoolify {
			return []string{"COOLIFY_API_URL", "STACKKIT_PLATFORM_ENDPOINT"}
		}
	case "token":
		if platform == models.PAASDokploy {
			return []string{"DOKPLOY_API_KEY", "STACKKIT_PLATFORM_TOKEN"}
		}
		if platform == models.PAASCoolify {
			return []string{"COOLIFY_API_TOKEN", "STACKKIT_PLATFORM_TOKEN"}
		}
	case "environment_id":
		if platform == models.PAASDokploy {
			return []string{"DOKPLOY_ENVIRONMENT_ID", "STACKKIT_PLATFORM_ENVIRONMENT_ID"}
		}
		if platform == models.PAASCoolify {
			return []string{"COOLIFY_ENVIRONMENT_NAME", "STACKKIT_PLATFORM_ENVIRONMENT_NAME"}
		}
	case "server_id":
		if platform == models.PAASDokploy {
			return []string{"DOKPLOY_SERVER_ID", "STACKKIT_PLATFORM_SERVER_ID"}
		}
		if platform == models.PAASCoolify {
			return []string{"COOLIFY_SERVER_UUID", "STACKKIT_PLATFORM_SERVER_UUID", "STACKKIT_PLATFORM_SERVER_ID"}
		}
	case "project_uuid":
		return []string{"COOLIFY_PROJECT_UUID", "STACKKIT_PLATFORM_PROJECT_UUID"}
	case "environment_uuid":
		return []string{"COOLIFY_ENVIRONMENT_UUID", "STACKKIT_PLATFORM_ENVIRONMENT_UUID"}
	case "destination_uuid":
		return []string{"COOLIFY_DESTINATION_UUID", "STACKKIT_PLATFORM_DESTINATION_UUID"}
	}
	return nil
}

func firstPlatformEnv(platform, field string) string {
	return firstEnv(platformConfigEnvKeys(platform, field)...)
}

func recordPlatformAppState(state *models.DeploymentState, bundle platformdeploy.BundleManifest, refs []platformdeploy.DeploymentRef) {
	if state == nil {
		return
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
		role, ok := systemRoles[ref.AppName]
		if !ok {
			continue
		}
		appState := models.PlatformAppState{
			Name:         ref.AppName,
			Platform:     ref.Platform,
			ExternalID:   ref.ExternalID,
			DeploymentID: ref.DeploymentID,
			LastDeployed: ref.LastDeployed,
			Role:         role,
			ComposePath:  systemComposePaths[ref.AppName],
			SetupPolicy:  systemSetupPolicies[ref.AppName],
			SetupDrops:   setupDropsToState(systemSetupDrops[ref.AppName]),
		}
		state.PlatformSystemApps = upsertPlatformAppState(state.PlatformSystemApps, appState)
	}
}

func recordPlatformAppHandoffs(state *models.DeploymentState, bundle platformdeploy.BundleManifest) {
	if state == nil {
		return
	}
	for _, app := range bundle.Apps {
		state.PlatformApps = upsertPlatformAppState(state.PlatformApps, models.PlatformAppState{
			Name:        app.Name,
			Platform:    firstNonEmpty(app.ManagedBy, app.Platform, bundle.Platform),
			ComposePath: app.ComposePath,
			SetupPolicy: app.SetupPolicy,
			SetupDrops:  setupDropsToState(app.SetupDrops),
		})
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
