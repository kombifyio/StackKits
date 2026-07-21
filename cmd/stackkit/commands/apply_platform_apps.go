package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/platformdeploy"
	"github.com/kombifyio/stackkits/pkg/models"
)

var newLocalComposeAdapter = func(deployDir string) platformdeploy.Adapter {
	return platformdeploy.NewLocalComposeAdapter(deployDir)
}

const platformBoolEnabledTrueValue = "true"

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

		customerBundle := bundle
		customerBundle.SystemApps = nil
		customerBundle.Apps = customerOwnedApps(bundle.Apps)
		if len(customerBundle.Apps) > 0 {
			recordPlatformAppHandoffs(state, customerBundle)
			printInfo("Recorded %d customer app handoff(s) for %s. Deploy customer-owned apps through the PaaS/Admin surface; StackKit does not manage their lifecycle.", len(customerBundle.Apps), bundle.Platform)
		}

		for _, deployBundle := range platformDeploymentBundles(bundle) {
			stackKitOwnedAppCount := countStackKitOwnedApps(deployBundle.Apps)
			deployCount := len(deployBundle.SystemApps) + stackKitOwnedAppCount
			if deployCount == 0 {
				continue
			}
			fallback := explicitStandaloneFallback(deployBundle)
			if !models.IsSupportedPAAS(deployBundle.Platform) && !fallback {
				return fmt.Errorf("StackKit-owned platform app manifest targets %q without an explicit platformFallback standalone-compose contract", deployBundle.Platform)
			}

			adapter, configured, err := platformAdapterForBundle(deployBundle, deployDir)
			if err != nil {
				return err
			}
			if !configured {
				return fmt.Errorf("platform API config for %s is required for %d StackKit-owned app(s); self-managed rollouts must persist .stackkit/platform.json during PaaS bootstrap, Coolify requires endpoint/token values, Komodo requires endpoint/apiKey/apiSecret values, draft Dokploy uses endpoint/token values, or platformFallback.mode=standalone-compose must be enabled explicitly", deployBundle.Platform, deployCount)
			}

			if fallback {
				printWarning("Deploying %d StackKit-owned app(s) through explicit standalone Compose fallback...", deployCount)
			} else {
				printInfo("Deploying %d StackKit-owned app(s) through %s platform adapter...", deployCount, deployBundle.Platform)
			}
			refs, err := platformdeploy.ApplyBundle(ctx, adapter, deployBundle)
			if err != nil {
				return err
			}
			management := platformdeploy.AppManagementManaged
			if fallback {
				management = platformdeploy.AppManagementFallback
			}
			recordPlatformAppStateWithManagement(state, deployBundle, refs, management)
			recordAutomaticComposeProvisionerSetupRuns(state, deployBundle)
			if fallback {
				printSuccess("Standalone Compose fallback rollout complete")
			} else {
				printSuccess("PaaS app rollout complete: %s", deployBundle.Platform)
			}
		}
	}

	return nil
}

func customerOwnedApps(apps []platformdeploy.AppManifest) []platformdeploy.AppManifest {
	out := make([]platformdeploy.AppManifest, 0, len(apps))
	for _, app := range apps {
		if platformdeploy.IsStackKitOwnedApp(app) {
			continue
		}
		out = append(out, app)
	}
	return out
}

func platformDeploymentBundles(bundle platformdeploy.BundleManifest) []platformdeploy.BundleManifest {
	groups := map[string]*platformdeploy.BundleManifest{}
	order := []string{}

	ensure := func(platform string) *platformdeploy.BundleManifest {
		platform = strings.TrimSpace(platform)
		if platform == "" {
			platform = strings.TrimSpace(bundle.Platform)
		}
		if platform == "" {
			platform = models.PAASNone
		}
		if _, ok := groups[platform]; !ok {
			groups[platform] = &platformdeploy.BundleManifest{
				Version:  bundle.Version,
				Platform: platform,
				Fallback: bundle.Fallback,
			}
			order = append(order, platform)
		}
		return groups[platform]
	}

	for _, app := range bundle.SystemApps {
		platform := firstNonEmpty(app.Platform, app.ManagedBy, bundle.Platform)
		group := ensure(platform)
		group.SystemApps = append(group.SystemApps, app)
	}
	for _, app := range bundle.Apps {
		if !platformdeploy.IsStackKitOwnedApp(app) {
			continue
		}
		platform := firstNonEmpty(app.Platform, app.ManagedBy, bundle.Platform)
		group := ensure(platform)
		group.Apps = append(group.Apps, app)
	}

	out := make([]platformdeploy.BundleManifest, 0, len(order))
	for _, platform := range order {
		group := groups[platform]
		if len(group.SystemApps) == 0 && len(group.Apps) == 0 {
			continue
		}
		out = append(out, *group)
	}
	return out
}

func countStackKitOwnedApps(apps []platformdeploy.AppManifest) int {
	count := 0
	for _, app := range apps {
		if platformdeploy.IsStackKitOwnedApp(app) {
			count++
		}
	}
	return count
}

func platformAdapterForBundle(bundle platformdeploy.BundleManifest, deployDir string) (platformdeploy.Adapter, bool, error) {
	switch bundle.Platform {
	case models.PAASNone:
		if !explicitStandaloneFallback(bundle) {
			return nil, false, fmt.Errorf("local compose adapter requires platformFallback.mode=standalone-compose")
		}
		return newLocalComposeAdapter(deployDir), true, nil
	case models.PAASDokploy:
		cfg, configured, err := platformHTTPConfigForBundle(bundle, deployDir)
		if err != nil {
			return nil, false, err
		}
		if !configured {
			return nil, false, nil
		}
		return platformdeploy.NewDokployAdapter(cfg), true, nil
	case models.PAASCoolify:
		cfg, configured, err := platformHTTPConfigForBundle(bundle, deployDir)
		if err != nil {
			return nil, false, err
		}
		if !configured {
			return nil, false, nil
		}
		return platformdeploy.NewCoolifyAdapter(cfg), true, nil
	case models.PAASKomodo:
		cfg, configured, err := platformHTTPConfigForBundle(bundle, deployDir)
		if err != nil {
			return nil, false, err
		}
		if !configured {
			return nil, false, nil
		}
		return platformdeploy.NewKomodoAdapter(cfg), true, nil
	default:
		return nil, false, fmt.Errorf("unsupported platform app adapter %q", bundle.Platform)
	}
}

func explicitStandaloneFallback(bundle platformdeploy.BundleManifest) bool {
	return bundle.Fallback.Enabled && bundle.Fallback.Mode == models.PlatformFallbackStandaloneCompose
}

type platformConfigFile struct {
	Platform                    string                           `json:"platform,omitempty"`
	Endpoint                    string                           `json:"endpoint,omitempty"`
	BaseURL                     string                           `json:"baseUrl,omitempty"`
	Token                       string                           `json:"token,omitempty"`
	APIKey                      string                           `json:"apiKey,omitempty"`
	APISecret                   string                           `json:"apiSecret,omitempty"`
	EnvironmentID               string                           `json:"environmentId,omitempty"`
	ServerID                    string                           `json:"serverId,omitempty"`
	ProjectUUID                 string                           `json:"projectUuid,omitempty"`
	EnvironmentUUID             string                           `json:"environmentUuid,omitempty"`
	DestinationUUID             string                           `json:"destinationUuid,omitempty"`
	LegacyDockerComposeAPI      bool                             `json:"legacyDockerComposeApi,omitempty"`
	DisableDockerRuntimeObserve bool                             `json:"disableDockerRuntimeObserve,omitempty"`
	BootstrapEvidence           platformdeploy.BootstrapEvidence `json:"bootstrapEvidence,omitempty"`
	found                       bool                             `json:"-"`
}

func platformHTTPConfigForBundle(bundle platformdeploy.BundleManifest, deployDir string) (platformdeploy.HTTPConfig, bool, error) {
	persisted, err := loadPlatformConfigFile(bundle.Platform, deployDir)
	if err != nil {
		return platformdeploy.HTTPConfig{}, false, err
	}
	cfg := platformdeploy.HTTPConfig{
		BaseURL:                     firstNonEmpty(firstPlatformEnv(bundle.Platform, "endpoint"), persisted.endpoint()),
		Token:                       firstNonEmpty(firstPlatformEnv(bundle.Platform, "token"), persisted.Token),
		APIKey:                      firstNonEmpty(firstPlatformEnv(bundle.Platform, "api_key"), persisted.APIKey, persisted.Token),
		Secret:                      firstNonEmpty(firstPlatformEnv(bundle.Platform, "api_secret"), persisted.APISecret),
		EnvironmentID:               firstNonEmpty(firstPlatformEnv(bundle.Platform, "environment_id"), persisted.EnvironmentID),
		ServerID:                    firstNonEmpty(firstPlatformEnv(bundle.Platform, "server_id"), persisted.ServerID),
		ProjectUUID:                 firstNonEmpty(firstPlatformEnv(bundle.Platform, "project_uuid"), persisted.ProjectUUID),
		EnvironmentUUID:             firstNonEmpty(firstPlatformEnv(bundle.Platform, "environment_uuid"), persisted.EnvironmentUUID),
		DestinationUUID:             firstNonEmpty(firstPlatformEnv(bundle.Platform, "destination_uuid"), persisted.DestinationUUID),
		LegacyDockerComposeAPI:      persisted.LegacyDockerComposeAPI,
		DisableDockerRuntimeObserve: persisted.DisableDockerRuntimeObserve,
	}
	if value, ok := firstPlatformBoolEnv(bundle.Platform, "legacy_docker_compose_api"); ok {
		cfg.LegacyDockerComposeAPI = value
	}
	if bundle.Platform == models.PAASDokploy && cfg.Token == "" && cfg.APIKey != "" {
		cfg.Token = cfg.APIKey
	}
	if persisted.found && platformdeploy.RequiresBootstrapEvidence(bundle.Platform) {
		if err := platformdeploy.ValidateBootstrapEvidence(bundle.Platform, persisted.BootstrapEvidence); err != nil {
			return cfg, false, err
		}
	}
	if bundle.Platform == models.PAASKomodo {
		return cfg, cfg.BaseURL != "" && cfg.APIKey != "" && cfg.Secret != "", nil
	}
	return cfg, cfg.BaseURL != "" && cfg.Token != "", nil
}

func (cfg platformConfigFile) endpoint() string {
	return firstNonEmpty(cfg.Endpoint, cfg.BaseURL)
}

func loadPlatformConfigFile(platform, deployDir string) (platformConfigFile, error) {
	wd := filepath.Dir(deployDir)
	bundleExists, err := tenantFetchManifestExists(wd)
	if err != nil {
		return platformConfigFile{}, fmt.Errorf("inspect tenant platform bundle: %w", err)
	}
	if applyTenantDeployment != "" && !bundleExists {
		return platformConfigFile{}, fmt.Errorf("managed platform configuration requires a verified tenant-fetch bundle for deployment %s", applyTenantDeployment)
	}
	if bundleExists {
		if applyTenantDeployment == "" {
			return platformConfigFile{}, fmt.Errorf("tenant platform bundle exists without --tenant-deployment binding")
		}
		data, declared, readErr := readVerifiedTenantSidecar(wd, specFile, applyTenantDeployment, "platform.json")
		if readErr != nil {
			return platformConfigFile{}, fmt.Errorf("verify tenant platform config: %w", readErr)
		}
		if !declared {
			return platformConfigFile{}, nil
		}
		var cfg platformConfigFile
		if err := json.Unmarshal(data, &cfg); err != nil {
			return platformConfigFile{}, fmt.Errorf("decode tenant platform config: %w", err)
		}
		if cfg.Platform != "" && !strings.EqualFold(cfg.Platform, platform) {
			return platformConfigFile{}, fmt.Errorf("tenant platform config %q does not match requested platform %q", cfg.Platform, platform)
		}
		cfg.found = true
		return cfg, nil
	}
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
			cfg.found = true
			return cfg, nil
		}
	}
	return platformConfigFile{}, nil
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
	data, err := json.MarshalIndent(cfg, "", "  ") // #nosec G117 -- platform credentials are persisted to an operator-owned 0600 config file.
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
	if cfg.Platform == models.PAASKomodo {
		if cfg.endpoint() == "" || cfg.APIKey == "" || cfg.APISecret == "" {
			return fmt.Errorf("tenant spec Komodo platform config is incomplete; provide STACKKIT_PLATFORM_ENDPOINT plus STACKKIT_PLATFORM_API_KEY/STACKKIT_PLATFORM_API_SECRET or KOMODO_API_URL plus KOMODO_API_KEY/KOMODO_API_SECRET")
		}
	} else if cfg.endpoint() == "" || cfg.Token == "" {
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
		Platform:                    platform,
		Endpoint:                    firstPlatformValue(platform, values, "endpoint"),
		Token:                       firstPlatformValue(platform, values, "token"),
		APIKey:                      firstPlatformValue(platform, values, "api_key"),
		APISecret:                   firstPlatformValue(platform, values, "api_secret"),
		EnvironmentID:               firstPlatformValue(platform, values, "environment_id"),
		ServerID:                    firstPlatformValue(platform, values, "server_id"),
		ProjectUUID:                 firstPlatformValue(platform, values, "project_uuid"),
		EnvironmentUUID:             firstPlatformValue(platform, values, "environment_uuid"),
		DestinationUUID:             firstPlatformValue(platform, values, "destination_uuid"),
		LegacyDockerComposeAPI:      platformBoolValue(firstPlatformValue(platform, values, "legacy_docker_compose_api")),
		DisableDockerRuntimeObserve: platformBoolValue(firstPlatformValue(platform, values, "disable_docker_runtime_observe")),
	}
	return cfg, platformSeen || cfg.endpoint() != "" || cfg.Token != "" ||
		cfg.APIKey != "" || cfg.APISecret != "" ||
		cfg.EnvironmentID != "" || cfg.ServerID != "" || cfg.ProjectUUID != "" ||
		cfg.EnvironmentUUID != "" || cfg.DestinationUUID != "" || cfg.LegacyDockerComposeAPI ||
		cfg.DisableDockerRuntimeObserve
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
	if values["KOMODO_API_URL"] != "" || values["KOMODO_API_KEY"] != "" || values["KOMODO_API_SECRET"] != "" {
		return models.PAASKomodo, true
	}
	defaultPlatform = strings.ToLower(strings.TrimSpace(defaultPlatform))
	if defaultPlatform == models.PAASCoolify || defaultPlatform == models.PAASDokploy || defaultPlatform == models.PAASKomodo {
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
	case models.PAASCoolify, models.PAASDokploy, models.PAASKomodo:
		return nil
	default:
		return fmt.Errorf("unsupported tenant spec platform config %q; expected coolify, komodo, or dokploy", platform)
	}
}

func genericPlatformConfigPresent(values map[string]string) bool {
	for _, key := range []string{
		"STACKKIT_PLATFORM_ENDPOINT",
		"STACKKIT_PLATFORM_TOKEN",
		"STACKKIT_PLATFORM_API_KEY",
		"STACKKIT_PLATFORM_API_SECRET",
		"STACKKIT_PLATFORM_ENVIRONMENT_ID",
		"STACKKIT_PLATFORM_ENVIRONMENT_NAME",
		"STACKKIT_PLATFORM_SERVER_ID",
		"STACKKIT_PLATFORM_SERVER_UUID",
		"STACKKIT_PLATFORM_PROJECT_UUID",
		"STACKKIT_PLATFORM_ENVIRONMENT_UUID",
		"STACKKIT_PLATFORM_DESTINATION_UUID",
		"STACKKIT_PLATFORM_LEGACY_DOCKERCOMPOSE_API",
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
	for _, platform := range []string{models.PAASCoolify, models.PAASDokploy, models.PAASKomodo} {
		for _, field := range []string{"endpoint", "token", "api_key", "api_secret", "environment_id", "server_id", "project_uuid", "environment_uuid", "destination_uuid", "legacy_docker_compose_api"} {
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

var platformSpecificConfigEnvKeys = map[string]map[string][]string{
	models.PAASCoolify: {
		"endpoint":                  {"COOLIFY_API_URL", "STACKKIT_PLATFORM_ENDPOINT"},
		"token":                     {"COOLIFY_API_TOKEN", "STACKKIT_PLATFORM_TOKEN"},
		"environment_id":            {"COOLIFY_ENVIRONMENT_NAME", "STACKKIT_PLATFORM_ENVIRONMENT_NAME"},
		"server_id":                 {"COOLIFY_SERVER_UUID", "STACKKIT_PLATFORM_SERVER_UUID", "STACKKIT_PLATFORM_SERVER_ID"},
		"legacy_docker_compose_api": {"COOLIFY_LEGACY_DOCKERCOMPOSE_API", "STACKKIT_PLATFORM_LEGACY_DOCKERCOMPOSE_API"},
	},
	models.PAASDokploy: {
		"endpoint":       {"DOKPLOY_API_URL", "STACKKIT_PLATFORM_ENDPOINT"},
		"token":          {"DOKPLOY_API_KEY", "STACKKIT_PLATFORM_TOKEN"},
		"environment_id": {"DOKPLOY_ENVIRONMENT_ID", "STACKKIT_PLATFORM_ENVIRONMENT_ID"},
		"server_id":      {"DOKPLOY_SERVER_ID", "STACKKIT_PLATFORM_SERVER_ID"},
	},
	models.PAASKomodo: {
		"endpoint":   {"KOMODO_API_URL", "STACKKIT_PLATFORM_ENDPOINT"},
		"api_key":    {"KOMODO_API_KEY", "STACKKIT_PLATFORM_API_KEY"},
		"api_secret": {"KOMODO_API_SECRET", "STACKKIT_PLATFORM_API_SECRET"},
		"server_id":  {"KOMODO_SERVER_ID", "STACKKIT_PLATFORM_SERVER_ID"},
	},
}

var sharedPlatformConfigEnvKeys = map[string][]string{
	"project_uuid":     {"COOLIFY_PROJECT_UUID", "STACKKIT_PLATFORM_PROJECT_UUID"},
	"environment_uuid": {"COOLIFY_ENVIRONMENT_UUID", "STACKKIT_PLATFORM_ENVIRONMENT_UUID"},
	"destination_uuid": {"COOLIFY_DESTINATION_UUID", "STACKKIT_PLATFORM_DESTINATION_UUID"},
}

func platformConfigEnvKeys(platform, field string) []string {
	if fields := platformSpecificConfigEnvKeys[platform]; fields != nil {
		if keys := fields[field]; len(keys) > 0 {
			return keys
		}
	}
	return sharedPlatformConfigEnvKeys[field]
}

func firstPlatformEnv(platform, field string) string {
	return firstEnv(platformConfigEnvKeys(platform, field)...)
}

func firstPlatformBoolEnv(platform, field string) (bool, bool) {
	for _, key := range platformConfigEnvKeys(platform, field) {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return platformBoolValue(value), true
		}
	}
	return false, false
}

func platformBoolValue(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", platformBoolEnabledTrueValue, "yes", "y", "on", "enabled":
		return true
	default:
		return false
	}
}

func recordPlatformAppState(state *models.DeploymentState, bundle platformdeploy.BundleManifest, refs []platformdeploy.DeploymentRef) {
	recordPlatformAppStateWithManagement(state, bundle, refs, platformdeploy.AppManagementManaged)
}

func recordPlatformAppStateWithManagement(state *models.DeploymentState, bundle platformdeploy.BundleManifest, refs []platformdeploy.DeploymentRef, management string) {
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
	appComposePaths := make(map[string]string, len(bundle.Apps))
	appSetupPolicies := make(map[string]string, len(bundle.Apps))
	appSetupDrops := make(map[string][]platformdeploy.SetupDropManifest, len(bundle.Apps))
	for _, app := range bundle.Apps {
		if !platformdeploy.IsStackKitOwnedApp(app) {
			continue
		}
		appComposePaths[app.Name] = app.ComposePath
		appSetupPolicies[app.Name] = app.SetupPolicy
		appSetupDrops[app.Name] = app.SetupDrops
	}
	for _, ref := range refs {
		role, ok := systemRoles[ref.AppName]
		if ok {
			appState := models.PlatformAppState{
				Name:           ref.AppName,
				Platform:       ref.Platform,
				Management:     management,
				ExternalID:     ref.ExternalID,
				DeploymentID:   ref.DeploymentID,
				ObservedStatus: ref.ObservedStatus,
				ObservedAt:     ref.ObservedAt,
				LastDeployed:   ref.LastDeployed,
				Role:           role,
				ComposePath:    systemComposePaths[ref.AppName],
				SetupPolicy:    systemSetupPolicies[ref.AppName],
				SetupDrops:     setupDropsToState(systemSetupDrops[ref.AppName]),
			}
			state.PlatformSystemApps = upsertPlatformAppState(state.PlatformSystemApps, appState)
			continue
		}
		if _, ok := appComposePaths[ref.AppName]; !ok {
			continue
		}
		appState := models.PlatformAppState{
			Name:           ref.AppName,
			Platform:       ref.Platform,
			Management:     management,
			ExternalID:     ref.ExternalID,
			DeploymentID:   ref.DeploymentID,
			ObservedStatus: ref.ObservedStatus,
			ObservedAt:     ref.ObservedAt,
			LastDeployed:   ref.LastDeployed,
			ComposePath:    appComposePaths[ref.AppName],
			SetupPolicy:    appSetupPolicies[ref.AppName],
			SetupDrops:     setupDropsToState(appSetupDrops[ref.AppName]),
		}
		state.PlatformApps = upsertPlatformAppState(state.PlatformApps, appState)
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
			Management:  platformdeploy.AppManagementHandoff,
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
			Name:          drop.Name,
			Version:       drop.Version,
			Runner:        drop.Runner,
			Description:   drop.Description,
			RollbackNotes: append([]string(nil), drop.RollbackNotes...),
			Command:       append([]string(nil), drop.Command...),
			Env:           drop.Env,
			Secrets:       drop.Secrets,
		})
	}
	return stateDrops
}

func recordAutomaticComposeProvisionerSetupRuns(state *models.DeploymentState, bundle platformdeploy.BundleManifest) {
	if state == nil {
		return
	}
	for _, systemApp := range bundle.SystemApps {
		recordAutomaticComposeProvisionerSetupRunsForApp(state, systemApp.AppManifest)
	}
	for _, app := range bundle.Apps {
		if !platformdeploy.IsStackKitOwnedApp(app) {
			continue
		}
		recordAutomaticComposeProvisionerSetupRunsForApp(state, app)
	}
}

func recordAutomaticComposeProvisionerSetupRunsForApp(state *models.DeploymentState, app platformdeploy.AppManifest) {
	if app.SetupPolicy != platformdeploy.SetupPolicyAutomatic {
		return
	}
	serviceKey := setupRunServiceKey(app)
	for _, drop := range app.SetupDrops {
		if drop.Runner != "compose-provisioner" {
			continue
		}
		upsertCompletedAutomaticSetupRun(state, serviceKey, app.Name, app.SetupPolicy, drop)
	}
}

func upsertCompletedAutomaticSetupRun(state *models.DeploymentState, serviceKey, appName, policy string, drop platformdeploy.SetupDropManifest) {
	now := time.Now().UTC()
	idx := findDeploymentSetupRunIndex(state.SetupRuns, serviceKey, appName, drop.Name)
	run := models.SetupRunState{
		RunID:         automaticSetupRunID(serviceKey, appName, drop.Name),
		ServiceKey:    serviceKey,
		AppName:       appName,
		DropName:      drop.Name,
		Policy:        policy,
		Status:        models.SetupRunStatusCompleted,
		Phase:         models.BootstrapPhaseVerified,
		Attempts:      1,
		Message:       "Automatic compose-provisioner setup completed during platform app rollout.",
		RollbackNotes: append([]string(nil), drop.RollbackNotes...),
		Evidence:      automaticComposeProvisionerEvidence(serviceKey, appName, drop.Name),
		LastRequested: now,
		LastStarted:   now,
		LastFinished:  now,
		Logs: []models.SetupRunLogEntry{{
			Timestamp: now,
			Phase:     models.BootstrapPhaseVerified,
			Level:     "info",
			Message:   "Compose provisioner completed as part of platform app rollout.",
		}},
	}
	if idx >= 0 {
		existing := state.SetupRuns[idx]
		if existing.RunID != "" {
			run.RunID = existing.RunID
		}
		run.Attempts = existing.Attempts + 1
		if run.Attempts < 1 {
			run.Attempts = 1
		}
		run.Logs = append(existing.Logs, run.Logs...)
		state.SetupRuns[idx] = run
		return
	}
	state.SetupRuns = append(state.SetupRuns, run)
}

func automaticComposeProvisionerEvidence(serviceKey, appName, dropName string) map[string]string {
	switch {
	case serviceKey == "files" && appName == "cloudreve" && dropName == "cloudreve-owner-bootstrap":
		return map[string]string{
			"credentialRole":          "technical-admin-bootstrap",
			"appLocalAccount":         "stackkit-admin-created",
			"demoData":                "seeded-when-enabled",
			"outerAuthBoundary":       "tinyauth-pocketid",
			"ownerLogin":              "pocketid-passkey",
			"identityBridge":          "stackkit-cloudreve-local-session",
			"appLocalSessionHandoff":  "stackkit-session-bridge-prepared",
			"readyToUseContentStatus": "pending-browser-evidence",
		}
	case serviceKey == "kuma" && appName == "uptime-kuma" && dropName == "kuma-platform-bootstrap":
		return map[string]string{
			"credentialRole":    "technical-admin-bootstrap",
			"outerAuthBoundary": "tinyauth-pocketid",
			"monitoring":        "default-service-monitors-prepared",
		}
	default:
		return nil
	}
}

func findDeploymentSetupRunIndex(runs []models.SetupRunState, serviceKey, appName, dropName string) int {
	for i, run := range runs {
		if run.ServiceKey == serviceKey && run.AppName == appName && run.DropName == dropName {
			return i
		}
	}
	return -1
}

func setupRunServiceKey(app platformdeploy.AppManifest) string {
	if strings.TrimSpace(app.ServiceKey) != "" {
		return app.ServiceKey
	}
	switch app.Name {
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
		return app.Name
	}
}

func automaticSetupRunID(parts ...string) string {
	normalized := make([]string, 0, len(parts)+1)
	normalized = append(normalized, "automatic")
	for _, part := range parts {
		part = strings.ToLower(strings.TrimSpace(part))
		part = strings.Map(func(r rune) rune {
			switch {
			case r >= 'a' && r <= 'z':
				return r
			case r >= '0' && r <= '9':
				return r
			case r == '-' || r == '_':
				return r
			default:
				return '-'
			}
		}, part)
		part = strings.Trim(part, "-")
		if part != "" {
			normalized = append(normalized, part)
		}
	}
	return strings.Join(normalized, "-")
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
