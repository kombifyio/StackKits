package api

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kombifyio/stackkits/internal/platformdeploy"
)

type runtimePlatformConfigFile struct {
	Platform               string `json:"platform,omitempty"`
	Endpoint               string `json:"endpoint,omitempty"`
	BaseURL                string `json:"baseUrl,omitempty"`
	Token                  string `json:"token,omitempty"`
	APIKey                 string `json:"apiKey,omitempty"`
	APISecret              string `json:"apiSecret,omitempty"`
	EnvironmentID          string `json:"environmentId,omitempty"`
	ServerID               string `json:"serverId,omitempty"`
	ProjectUUID            string `json:"projectUuid,omitempty"`
	EnvironmentUUID        string `json:"environmentUuid,omitempty"`
	DestinationUUID        string `json:"destinationUuid,omitempty"`
	LegacyDockerComposeAPI bool   `json:"legacyDockerComposeApi,omitempty"`
}

func runRuntimePlatformAppDeployments(ctx context.Context, deployDir string) ([]platformdeploy.DeploymentRef, []runtimeActionCheck, error) {
	manifestPaths := []string{
		filepath.Join(deployDir, "platform-apps", "manifest.json"),
		filepath.Join(deployDir, ".platform-apps-manifest.json"),
	}

	var refs []platformdeploy.DeploymentRef
	var checks []runtimeActionCheck
	manifestSeen := false

	for _, manifestPath := range manifestPaths {
		if _, err := os.Stat(manifestPath); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return refs, checks, fmt.Errorf("inspect platform app manifest: %w", err)
		}
		manifestSeen = true

		bundle, err := platformdeploy.LoadBundleManifest(manifestPath)
		if err != nil {
			return refs, checks, err
		}

		for _, deployBundle := range runtimePlatformDeploymentBundles(bundle) {
			deployCount := len(deployBundle.SystemApps) + runtimeStackKitOwnedAppCount(deployBundle.Apps)
			if deployCount == 0 {
				continue
			}

			adapter, configured, err := runtimePlatformAdapterForBundle(deployBundle, deployDir)
			if err != nil {
				return refs, checks, err
			}
			if !configured {
				return refs, checks, fmt.Errorf("platform API config for %s is required for %d StackKit-owned app(s)", deployBundle.Platform, deployCount)
			}

			deployed, err := platformdeploy.ApplyBundle(ctx, adapter, deployBundle)
			if err != nil {
				return refs, checks, err
			}
			refs = append(refs, deployed...)
			checks = append(checks, runtimeActionCheck{
				Name:   "platform_apps",
				Status: "ok",
				Detail: fmt.Sprintf("%s deployed %d app(s)", deployBundle.Platform, len(deployed)),
			})
		}
	}

	if !manifestSeen {
		checks = append(checks, runtimeActionCheck{Name: "platform_apps", Status: "skipped", Detail: "manifest not present"})
	} else if len(refs) == 0 {
		checks = append(checks, runtimeActionCheck{Name: "platform_apps", Status: "skipped", Detail: "no StackKit-owned platform apps"})
	}

	return refs, checks, nil
}

func runtimePlatformDeploymentBundles(bundle platformdeploy.BundleManifest) []platformdeploy.BundleManifest {
	groups := map[string]*platformdeploy.BundleManifest{}
	order := []string{}

	ensure := func(platform string) *platformdeploy.BundleManifest {
		platform = strings.TrimSpace(platform)
		if platform == "" {
			platform = strings.TrimSpace(bundle.Platform)
		}
		if platform == "" {
			platform = "none"
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
		platform := runtimeFirstNonEmpty(app.Platform, app.ManagedBy, bundle.Platform)
		group := ensure(platform)
		group.SystemApps = append(group.SystemApps, app)
	}
	for _, app := range bundle.Apps {
		if !platformdeploy.IsStackKitOwnedApp(app) {
			continue
		}
		platform := runtimeFirstNonEmpty(app.Platform, app.ManagedBy, bundle.Platform)
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

func runtimeStackKitOwnedAppCount(apps []platformdeploy.AppManifest) int {
	count := 0
	for _, app := range apps {
		if platformdeploy.IsStackKitOwnedApp(app) {
			count++
		}
	}
	return count
}

func runtimePlatformAdapterForBundle(bundle platformdeploy.BundleManifest, deployDir string) (platformdeploy.Adapter, bool, error) {
	switch strings.ToLower(strings.TrimSpace(bundle.Platform)) {
	case "", "none":
		if bundle.Fallback.Enabled && bundle.Fallback.Mode == "standalone-compose" {
			return platformdeploy.NewLocalComposeAdapter(deployDir), true, nil
		}
		return nil, false, fmt.Errorf("local compose adapter requires platformFallback.mode=standalone-compose")
	case "coolify":
		cfg, configured := runtimePlatformHTTPConfigForBundle(bundle, deployDir)
		if !configured {
			return nil, false, nil
		}
		return platformdeploy.NewCoolifyAdapter(cfg), true, nil
	case "dokploy":
		cfg, configured := runtimePlatformHTTPConfigForBundle(bundle, deployDir)
		if !configured {
			return nil, false, nil
		}
		return platformdeploy.NewDokployAdapter(cfg), true, nil
	case "komodo":
		cfg, configured := runtimePlatformHTTPConfigForBundle(bundle, deployDir)
		if !configured {
			return nil, false, nil
		}
		return platformdeploy.NewKomodoAdapter(cfg), true, nil
	default:
		return nil, false, fmt.Errorf("unsupported platform app adapter %q", bundle.Platform)
	}
}

func runtimePlatformHTTPConfigForBundle(bundle platformdeploy.BundleManifest, deployDir string) (platformdeploy.HTTPConfig, bool) {
	persisted := runtimeLoadPlatformConfigFile(deployDir)
	cfg := platformdeploy.HTTPConfig{
		BaseURL:                runtimeFirstNonEmpty(runtimeFirstPlatformEnv(bundle.Platform, "endpoint"), persisted.endpoint()),
		Token:                  runtimeFirstNonEmpty(runtimeFirstPlatformEnv(bundle.Platform, "token"), persisted.Token),
		APIKey:                 runtimeFirstNonEmpty(runtimeFirstPlatformEnv(bundle.Platform, "api_key"), persisted.APIKey, persisted.Token),
		Secret:                 runtimeFirstNonEmpty(runtimeFirstPlatformEnv(bundle.Platform, "api_secret"), persisted.APISecret),
		EnvironmentID:          runtimeFirstNonEmpty(runtimeFirstPlatformEnv(bundle.Platform, "environment_id"), persisted.EnvironmentID),
		ServerID:               runtimeFirstNonEmpty(runtimeFirstPlatformEnv(bundle.Platform, "server_id"), persisted.ServerID),
		ProjectUUID:            runtimeFirstNonEmpty(runtimeFirstPlatformEnv(bundle.Platform, "project_uuid"), persisted.ProjectUUID),
		EnvironmentUUID:        runtimeFirstNonEmpty(runtimeFirstPlatformEnv(bundle.Platform, "environment_uuid"), persisted.EnvironmentUUID),
		DestinationUUID:        runtimeFirstNonEmpty(runtimeFirstPlatformEnv(bundle.Platform, "destination_uuid"), persisted.DestinationUUID),
		LegacyDockerComposeAPI: persisted.LegacyDockerComposeAPI,
	}
	if bundle.Platform == "komodo" {
		return cfg, cfg.BaseURL != "" && cfg.APIKey != "" && cfg.Secret != ""
	}
	return cfg, cfg.BaseURL != "" && cfg.Token != ""
}

func runtimeLoadPlatformConfigFile(deployDir string) runtimePlatformConfigFile {
	path := filepath.Join(deployDir, ".stackkit", "platform.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return runtimePlatformConfigFile{}
	}
	var cfg runtimePlatformConfigFile
	if err := json.Unmarshal(data, &cfg); err != nil {
		return runtimePlatformConfigFile{}
	}
	return cfg
}

func (cfg runtimePlatformConfigFile) endpoint() string {
	return runtimeFirstNonEmpty(cfg.Endpoint, cfg.BaseURL)
}

func runtimeFirstPlatformEnv(platform, suffix string) string {
	keyPlatform := strings.ToUpper(strings.NewReplacer("-", "_", ".", "_").Replace(strings.TrimSpace(platform)))
	keySuffix := strings.ToUpper(strings.NewReplacer("-", "_", ".", "_").Replace(strings.TrimSpace(suffix)))
	if keyPlatform == "" || keySuffix == "" {
		return ""
	}
	return os.Getenv("STACKKIT_" + keyPlatform + "_" + keySuffix)
}

func runtimeFirstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
