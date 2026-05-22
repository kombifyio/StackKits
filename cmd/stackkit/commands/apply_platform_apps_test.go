package commands

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/kombifyio/stackkits/internal/platformdeploy"
	"github.com/kombifyio/stackkits/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPlatformAdapterForBundleUsesLocalComposeForExplicitFallback(t *testing.T) {
	adapter, configured, err := platformAdapterForBundle(platformdeploy.BundleManifest{
		Platform: models.PAASNone,
		Fallback: platformdeploy.FallbackManifest{
			Enabled: true,
			Mode:    models.PlatformFallbackStandaloneCompose,
		},
	}, "/opt/stackkit/deploy")

	require.NoError(t, err)
	assert.True(t, configured)
	assert.NotNil(t, adapter)
}

func TestPlatformAdapterForBundleRejectsImplicitLocalCompose(t *testing.T) {
	adapter, configured, err := platformAdapterForBundle(platformdeploy.BundleManifest{
		Platform: models.PAASNone,
	}, "/opt/stackkit/deploy")

	require.Error(t, err)
	assert.False(t, configured)
	assert.Nil(t, adapter)
	assert.Contains(t, err.Error(), "platformFallback.mode=standalone-compose")
}

func TestRecordPlatformAppStateOnlyRecordsSystemApps(t *testing.T) {
	state := &models.DeploymentState{}
	bundle := platformdeploy.BundleManifest{
		Platform: models.PAASDokploy,
		SystemApps: []platformdeploy.SystemAppManifest{{
			AppManifest: platformdeploy.AppManifest{
				Name:        "stackkit-hub",
				ComposePath: ".stackkit-hub-compose.yaml",
			},
			Role: "node-hub",
		}},
		Apps: []platformdeploy.AppManifest{{
			Name:        "immich",
			ComposePath: ".immich-compose.yaml",
			SetupPolicy: platformdeploy.SetupPolicyManual,
			SetupDrops: []platformdeploy.SetupDropManifest{{
				Name:        "immich-owner-bootstrap",
				Version:     "0.1.0",
				Runner:      "stackkit-script",
				Description: "Create the first Immich owner and mark onboarding complete.",
			}},
		}},
	}

	recordPlatformAppState(state, bundle, []platformdeploy.DeploymentRef{
		{Platform: models.PAASDokploy, AppName: "stackkit-hub", ExternalID: "hub-1"},
		{Platform: models.PAASDokploy, AppName: "immich", ExternalID: "immich-1"},
	})

	require.Len(t, state.PlatformSystemApps, 1)
	assert.Equal(t, "stackkit-hub", state.PlatformSystemApps[0].Name)
	assert.Equal(t, "node-hub", state.PlatformSystemApps[0].Role)
	assert.Equal(t, platformdeploy.AppManagementManaged, state.PlatformSystemApps[0].Management)
	assert.Equal(t, ".stackkit-hub-compose.yaml", state.PlatformSystemApps[0].ComposePath)
	assert.Empty(t, state.PlatformApps, "user app deployment refs must not be recorded by StackKit")
	assert.Empty(t, state.SetupRuns, "manual setup drops are metadata until explicitly requested")
}

func TestRecordPlatformAppStateUpsertsExistingSystemApps(t *testing.T) {
	state := &models.DeploymentState{
		PlatformApps: []models.PlatformAppState{{
			Name:        "web",
			Platform:    models.PAASDokploy,
			ExternalID:  "old-web",
			ComposePath: "old-web.compose.yaml",
		}},
		PlatformSystemApps: []models.PlatformAppState{{
			Name:       "stackkit-hub",
			Role:       "node-hub",
			Platform:   models.PAASDokploy,
			ExternalID: "old-hub",
		}},
	}
	bundle := platformdeploy.BundleManifest{
		Platform: models.PAASDokploy,
		SystemApps: []platformdeploy.SystemAppManifest{{
			AppManifest: platformdeploy.AppManifest{Name: "stackkit-hub", ComposePath: ".hub.compose.yaml"},
			Role:        "node-hub",
		}},
		Apps: []platformdeploy.AppManifest{{
			Name:        "web",
			ComposePath: "platform-apps/web.compose.yaml",
		}},
	}

	recordPlatformAppState(state, bundle, []platformdeploy.DeploymentRef{
		{Platform: models.PAASDokploy, AppName: "stackkit-hub", ExternalID: "new-hub"},
		{Platform: models.PAASDokploy, AppName: "web", ExternalID: "new-web"},
	})

	require.Len(t, state.PlatformSystemApps, 1)
	assert.Equal(t, "new-hub", state.PlatformSystemApps[0].ExternalID)
	assert.Equal(t, ".hub.compose.yaml", state.PlatformSystemApps[0].ComposePath)
	require.Len(t, state.PlatformApps, 1)
	assert.Equal(t, "old-web", state.PlatformApps[0].ExternalID)
	assert.Equal(t, "old-web.compose.yaml", state.PlatformApps[0].ComposePath)
}

func TestRecordPlatformAppStateRecordsStackKitOwnedApps(t *testing.T) {
	state := &models.DeploymentState{}
	bundle := platformdeploy.BundleManifest{
		Platform: models.PAASCoolify,
		Apps: []platformdeploy.AppManifest{{
			Name:        "immich",
			Ownership:   platformdeploy.AppOwnershipStackKit,
			ComposePath: ".immich-compose.yaml",
			SetupPolicy: platformdeploy.SetupPolicyOnDemand,
			SetupDrops: []platformdeploy.SetupDropManifest{{
				Name:   "immich-owner-bootstrap",
				Runner: "stackkit-script",
			}},
		}},
	}

	recordPlatformAppState(state, bundle, []platformdeploy.DeploymentRef{
		{Platform: models.PAASCoolify, AppName: "immich", ExternalID: "coolify-immich", DeploymentID: "deploy-1"},
	})

	require.Len(t, state.PlatformApps, 1)
	assert.Equal(t, "immich", state.PlatformApps[0].Name)
	assert.Equal(t, models.PAASCoolify, state.PlatformApps[0].Platform)
	assert.Equal(t, platformdeploy.AppManagementManaged, state.PlatformApps[0].Management)
	assert.Equal(t, "coolify-immich", state.PlatformApps[0].ExternalID)
	assert.Equal(t, "deploy-1", state.PlatformApps[0].DeploymentID)
	assert.Equal(t, ".immich-compose.yaml", state.PlatformApps[0].ComposePath)
	assert.Equal(t, platformdeploy.SetupPolicyOnDemand, state.PlatformApps[0].SetupPolicy)
	require.Len(t, state.PlatformApps[0].SetupDrops, 1)
	assert.Equal(t, "immich-owner-bootstrap", state.PlatformApps[0].SetupDrops[0].Name)
}

func TestRecordPlatformAppHandoffsClearsLegacyDeploymentIDs(t *testing.T) {
	state := &models.DeploymentState{
		PlatformApps: []models.PlatformAppState{{
			Name:         "web",
			Platform:     models.PAASDokploy,
			ExternalID:   "legacy-web",
			DeploymentID: "legacy-deploy",
		}},
	}
	bundle := platformdeploy.BundleManifest{
		Platform: models.PAASDokploy,
		Apps: []platformdeploy.AppManifest{{
			Name:        "web",
			ManagedBy:   models.PAASDokploy,
			ComposePath: "platform-apps/web.compose.yaml",
			SetupPolicy: platformdeploy.SetupPolicyManual,
		}},
	}

	recordPlatformAppHandoffs(state, bundle)

	require.Len(t, state.PlatformApps, 1)
	assert.Equal(t, "web", state.PlatformApps[0].Name)
	assert.Equal(t, platformdeploy.AppManagementHandoff, state.PlatformApps[0].Management)
	assert.Empty(t, state.PlatformApps[0].ExternalID)
	assert.Empty(t, state.PlatformApps[0].DeploymentID)
	assert.Equal(t, "platform-apps/web.compose.yaml", state.PlatformApps[0].ComposePath)
}

func TestPlatformAdapterConfigUsesPersistedConfigWhenEnvMissing(t *testing.T) {
	t.Setenv("STACKKIT_PLATFORM_ENDPOINT", "")
	t.Setenv("STACKKIT_PLATFORM_TOKEN", "")
	t.Setenv("DOKPLOY_API_URL", "")
	t.Setenv("DOKPLOY_API_KEY", "")

	wd := t.TempDir()
	deployDir := filepath.Join(wd, "deploy")
	require.NoError(t, os.MkdirAll(filepath.Join(wd, ".stackkit"), 0750))
	require.NoError(t, os.MkdirAll(deployDir, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(wd, ".stackkit", "platform.json"), []byte(`{
  "platform": "dokploy",
  "endpoint": "http://127.0.0.1:3000",
  "token": "persisted-token",
  "environmentId": "env-1",
  "serverId": "server-1"
}`), 0600))

	cfg, configured := platformHTTPConfigForBundle(platformdeploy.BundleManifest{Platform: models.PAASDokploy}, deployDir)

	require.True(t, configured)
	assert.Equal(t, "http://127.0.0.1:3000", cfg.BaseURL)
	assert.Equal(t, "persisted-token", cfg.Token)
	assert.Equal(t, "env-1", cfg.EnvironmentID)
	assert.Equal(t, "server-1", cfg.ServerID)
}

func TestPlatformAdapterConfigAcceptsDokployApiKeyAlias(t *testing.T) {
	t.Setenv("STACKKIT_PLATFORM_ENDPOINT", "")
	t.Setenv("STACKKIT_PLATFORM_TOKEN", "")
	t.Setenv("DOKPLOY_API_URL", "")
	t.Setenv("DOKPLOY_API_KEY", "")

	deployDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(deployDir, ".stackkit"), 0750))
	require.NoError(t, os.WriteFile(filepath.Join(deployDir, ".stackkit", "platform.json"), []byte(`{
  "platform": "dokploy",
  "endpoint": "http://127.0.0.1:3000",
  "apiKey": "persisted-api-key"
}`), 0600))

	cfg, configured := platformHTTPConfigForBundle(platformdeploy.BundleManifest{Platform: models.PAASDokploy}, deployDir)

	require.True(t, configured)
	assert.Equal(t, "http://127.0.0.1:3000", cfg.BaseURL)
	assert.Equal(t, "persisted-api-key", cfg.Token)
	assert.Equal(t, "persisted-api-key", cfg.APIKey)
}

func TestPlatformAdapterConfigUsesGeneratedDeployDirPlatformConfig(t *testing.T) {
	t.Setenv("STACKKIT_PLATFORM_ENDPOINT", "")
	t.Setenv("STACKKIT_PLATFORM_TOKEN", "")
	t.Setenv("COOLIFY_API_URL", "")
	t.Setenv("COOLIFY_API_TOKEN", "")

	deployDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(deployDir, ".stackkit"), 0750))
	require.NoError(t, os.WriteFile(filepath.Join(deployDir, ".stackkit", "platform.json"), []byte(`{
  "platform": "coolify",
  "endpoint": "http://127.0.0.1:8000",
  "token": "bootstrap-token",
  "projectUuid": "project-1",
  "environmentUuid": "environment-1",
  "environmentId": "production",
  "serverId": "server-1",
  "destinationUuid": "destination-1"
}`), 0600))

	cfg, configured := platformHTTPConfigForBundle(platformdeploy.BundleManifest{Platform: models.PAASCoolify}, deployDir)

	require.True(t, configured)
	assert.Equal(t, "http://127.0.0.1:8000", cfg.BaseURL)
	assert.Equal(t, "bootstrap-token", cfg.Token)
	assert.Equal(t, "project-1", cfg.ProjectUUID)
	assert.Equal(t, "environment-1", cfg.EnvironmentUUID)
	assert.Equal(t, "production", cfg.EnvironmentID)
	assert.Equal(t, "server-1", cfg.ServerID)
	assert.Equal(t, "destination-1", cfg.DestinationUUID)
}

func TestPlatformAdapterConfigReadsKomodoAPIKeySecret(t *testing.T) {
	t.Setenv("STACKKIT_PLATFORM_ENDPOINT", "")
	t.Setenv("STACKKIT_PLATFORM_API_KEY", "")
	t.Setenv("STACKKIT_PLATFORM_API_SECRET", "")
	t.Setenv("KOMODO_API_URL", "")
	t.Setenv("KOMODO_API_KEY", "")
	t.Setenv("KOMODO_API_SECRET", "")

	deployDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(deployDir, ".stackkit"), 0750))
	require.NoError(t, os.WriteFile(filepath.Join(deployDir, ".stackkit", "platform.json"), []byte(`{
  "platform": "komodo",
  "endpoint": "http://127.0.0.1:9120",
  "apiKey": "komodo-key",
  "apiSecret": "komodo-secret",
  "serverId": "stackkit-local"
}`), 0600))

	cfg, configured := platformHTTPConfigForBundle(platformdeploy.BundleManifest{Platform: models.PAASKomodo}, deployDir)

	require.True(t, configured)
	assert.Equal(t, "http://127.0.0.1:9120", cfg.BaseURL)
	assert.Equal(t, "komodo-key", cfg.APIKey)
	assert.Equal(t, "komodo-secret", cfg.Secret)
	assert.Equal(t, "stackkit-local", cfg.ServerID)
}

func TestPlatformAdapterConfigReadsCoolifyLegacyFlag(t *testing.T) {
	t.Setenv("COOLIFY_API_URL", "https://coolify.example.test")
	t.Setenv("COOLIFY_API_TOKEN", "coolify-token")
	t.Setenv("COOLIFY_LEGACY_DOCKERCOMPOSE_API", "true")

	cfg, configured := platformHTTPConfigForBundle(platformdeploy.BundleManifest{Platform: models.PAASCoolify}, t.TempDir())

	require.True(t, configured)
	assert.True(t, cfg.LegacyDockerComposeAPI)
}

func TestPersistPlatformConfigFromSpecEnvironmentRejectsIncompleteConfig(t *testing.T) {
	tmp := t.TempDir()
	spec := &models.StackSpec{
		PAAS: models.PAASCoolify,
		Environment: map[string]string{
			"STACKKIT_PLATFORM_ENDPOINT": "https://coolify.example.test",
		},
	}

	err := persistPlatformConfigFromSpecEnvironment(spec, tmp)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "platform config is incomplete")
	if _, statErr := os.Stat(filepath.Join(tmp, ".stackkit", "platform.json")); !os.IsNotExist(statErr) {
		t.Fatalf("platform config should not be written for incomplete config, statErr=%v", statErr)
	}
}

func TestPersistPlatformConfigFromSpecEnvironmentDefaultsGenericConfigToCoolify(t *testing.T) {
	tmp := t.TempDir()
	spec := &models.StackSpec{
		PAAS: models.PAASNone,
		Environment: map[string]string{
			"STACKKIT_PLATFORM_ENDPOINT": "https://coolify.example.test",
			"STACKKIT_PLATFORM_TOKEN":    "platform-token",
		},
	}

	err := persistPlatformConfigFromSpecEnvironment(spec, tmp)

	require.NoError(t, err)
	data, err := os.ReadFile(filepath.Join(tmp, ".stackkit", "platform.json"))
	require.NoError(t, err)
	var cfg platformConfigFile
	require.NoError(t, json.Unmarshal(data, &cfg))
	assert.Equal(t, models.PAASCoolify, cfg.Platform)
	assert.Equal(t, "https://coolify.example.test", cfg.Endpoint)
	assert.Equal(t, "platform-token", cfg.Token)
	assert.NotContains(t, spec.Environment, "STACKKIT_PLATFORM_TOKEN")
}

func TestPersistPlatformConfigFromSpecEnvironmentWritesKomodoConfig(t *testing.T) {
	tmp := t.TempDir()
	spec := &models.StackSpec{
		PAAS: models.PAASKomodo,
		Environment: map[string]string{
			"KOMODO_API_URL":    "http://127.0.0.1:9120",
			"KOMODO_API_KEY":    "komodo-key",
			"KOMODO_API_SECRET": "komodo-secret",
			"KOMODO_SERVER_ID":  "stackkit-local",
		},
	}

	err := persistPlatformConfigFromSpecEnvironment(spec, tmp)

	require.NoError(t, err)
	data, err := os.ReadFile(filepath.Join(tmp, ".stackkit", "platform.json"))
	require.NoError(t, err)
	var cfg platformConfigFile
	require.NoError(t, json.Unmarshal(data, &cfg))
	assert.Equal(t, models.PAASKomodo, cfg.Platform)
	assert.Equal(t, "http://127.0.0.1:9120", cfg.Endpoint)
	assert.Equal(t, "komodo-key", cfg.APIKey)
	assert.Equal(t, "komodo-secret", cfg.APISecret)
	assert.Equal(t, "stackkit-local", cfg.ServerID)
	assert.NotContains(t, spec.Environment, "KOMODO_API_SECRET")
}

func TestPersistPlatformConfigFromSpecEnvironmentRejectsUnsupportedPlatform(t *testing.T) {
	tmp := t.TempDir()
	spec := &models.StackSpec{
		PAAS: models.PAASCoolify,
		Environment: map[string]string{
			"STACKKIT_PLATFORM":          "fly",
			"STACKKIT_PLATFORM_ENDPOINT": "https://fly.example.test",
			"STACKKIT_PLATFORM_TOKEN":    "platform-token",
		},
	}

	err := persistPlatformConfigFromSpecEnvironment(spec, tmp)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported tenant spec platform config")
	if _, statErr := os.Stat(filepath.Join(tmp, ".stackkit", "platform.json")); !os.IsNotExist(statErr) {
		t.Fatalf("platform config should not be written for unsupported platform, statErr=%v", statErr)
	}
}

func TestRunPlatformAppDeploymentsFailsStackKitOwnedAppsWithoutPlatformConfig(t *testing.T) {
	t.Setenv("STACKKIT_PLATFORM_ENDPOINT", "")
	t.Setenv("STACKKIT_PLATFORM_TOKEN", "")
	t.Setenv("COOLIFY_API_URL", "")
	t.Setenv("COOLIFY_API_TOKEN", "")

	wd := t.TempDir()
	deployDir := filepath.Join(wd, "deploy")
	require.NoError(t, os.MkdirAll(deployDir, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(deployDir, ".platform-apps-manifest.json"), []byte(`{
  "version": "stackkit.platform-apps/v2",
  "platform": "coolify",
  "apps": [{
    "name": "immich",
    "ownership": "stackkit",
    "kind": "compose",
    "platform": "coolify",
    "managedBy": "coolify",
    "composePath": ".immich-compose.yaml",
    "composeYAML": "services:\n  immich:\n    image: ghcr.io/immich-app/immich-server:release\n"
  }]
}`), 0600))

	state := &models.DeploymentState{}
	err := runPlatformAppDeployments(context.Background(), deployDir, state)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "platform API config for coolify is required")
	assert.Contains(t, err.Error(), "must persist .stackkit/platform.json during PaaS bootstrap")
	assert.Contains(t, err.Error(), "Komodo requires endpoint/apiKey/apiSecret")
	assert.Empty(t, state.PlatformApps)
}

func TestRunPlatformAppDeploymentsUsesBootstrappedCoolifyConfig(t *testing.T) {
	t.Setenv("STACKKIT_PLATFORM_ENDPOINT", "")
	t.Setenv("STACKKIT_PLATFORM_TOKEN", "")
	t.Setenv("COOLIFY_API_URL", "")
	t.Setenv("COOLIFY_API_TOKEN", "")

	var servicePayload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/services":
			require.Equal(t, http.MethodPost, r.Method)
			require.Equal(t, "Bearer bootstrap-token", r.Header.Get("Authorization"))
			require.NoError(t, json.NewDecoder(r.Body).Decode(&servicePayload))
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"uuid":"coolify-stackkit-hub"}`))
		case "/api/v1/services/coolify-stackkit-hub/start":
			require.Equal(t, http.MethodPost, r.Method)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"deployments":[{"deployment_uuid":"deploy-1"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	deployDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(deployDir, ".stackkit"), 0750))
	require.NoError(t, os.WriteFile(filepath.Join(deployDir, ".stackkit", "platform.json"), []byte(`{
  "platform": "coolify",
  "endpoint": "`+server.URL+`",
  "token": "bootstrap-token",
  "projectUuid": "project-1",
  "environmentUuid": "environment-1",
  "environmentId": "production",
  "serverId": "server-1",
  "destinationUuid": "destination-1"
}`), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(deployDir, ".platform-apps-manifest.json"), []byte(`{
  "version": "stackkit.platform-apps/v2",
  "platform": "coolify",
  "systemApps": [{
    "name": "stackkit-hub",
    "role": "node-hub",
    "kind": "compose",
    "platform": "coolify",
    "managedBy": "coolify",
    "host": "base.home.localhost",
    "url": "http://base.home.localhost",
    "composePath": ".stackkit-hub-compose.yaml",
    "composeYAML": "services:\n  stackkit-hub:\n    image: nginx:alpine\n"
  }]
}`), 0600))

	state := &models.DeploymentState{}
	err := runPlatformAppDeployments(context.Background(), deployDir, state)

	require.NoError(t, err)
	require.Len(t, state.PlatformSystemApps, 1)
	assert.Equal(t, "stackkit-hub", state.PlatformSystemApps[0].Name)
	assert.Equal(t, platformdeploy.AppManagementManaged, state.PlatformSystemApps[0].Management)
	assert.Equal(t, "coolify-stackkit-hub", state.PlatformSystemApps[0].ExternalID)
	assert.Equal(t, "deploy-1", state.PlatformSystemApps[0].DeploymentID)
	assert.Equal(t, "project-1", servicePayload["project_uuid"])
	assert.Equal(t, "environment-1", servicePayload["environment_uuid"])
	assert.Equal(t, "production", servicePayload["environment_name"])
	assert.Equal(t, "server-1", servicePayload["server_uuid"])
	assert.Equal(t, "destination-1", servicePayload["destination_uuid"])
	assert.NotContains(t, servicePayload["docker_compose_raw"], "services:")
}

func TestRunPlatformAppDeploymentsFailsStackKitOwnedAppsForNonstandardPlatformWithoutFallback(t *testing.T) {
	wd := t.TempDir()
	deployDir := filepath.Join(wd, "deploy")
	require.NoError(t, os.MkdirAll(deployDir, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(deployDir, ".platform-apps-manifest.json"), []byte(`{
  "version": "stackkit.platform-apps/v2",
  "platform": "none",
  "apps": [{
    "name": "photos",
    "ownership": "stackkit",
    "kind": "compose",
    "platform": "none",
    "managedBy": "none",
    "composePath": ".photos-compose.yaml",
    "composeYAML": "services:\n  photos:\n    image: example/photos:latest\n"
  }]
}`), 0600))

	state := &models.DeploymentState{}
	err := runPlatformAppDeployments(context.Background(), deployDir, state)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "without an explicit platformFallback standalone-compose contract")
	assert.Empty(t, state.PlatformApps)
}

func TestRunPlatformAppDeploymentsUsesExplicitStandaloneFallback(t *testing.T) {
	wd := t.TempDir()
	deployDir := filepath.Join(wd, "deploy")
	require.NoError(t, os.MkdirAll(deployDir, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(deployDir, ".platform-apps-manifest.json"), []byte(`{
  "version": "stackkit.platform-apps/v2",
  "platform": "none",
  "fallback": {
    "enabled": true,
    "mode": "standalone-compose"
  },
  "apps": [{
    "name": "photos",
    "ownership": "stackkit",
    "kind": "compose",
    "platform": "none",
    "managedBy": "none",
    "composePath": ".photos-compose.yaml",
    "composeYAML": "services:\n  photos:\n    image: example/photos:latest\n"
  }]
}`), 0600))

	oldFactory := newLocalComposeAdapter
	t.Cleanup(func() { newLocalComposeAdapter = oldFactory })
	var applied []string
	newLocalComposeAdapter = func(gotDeployDir string) platformdeploy.Adapter {
		assert.Equal(t, deployDir, gotDeployDir)
		return platformAdapterFunc(func(_ context.Context, app platformdeploy.AppManifest) (platformdeploy.DeploymentRef, error) {
			applied = append(applied, app.Name)
			return platformdeploy.DeploymentRef{
				Platform:   app.ManagedBy,
				AppName:    app.Name,
				ExternalID: "local-compose:" + app.Name,
			}, nil
		})
	}

	state := &models.DeploymentState{}
	err := runPlatformAppDeployments(context.Background(), deployDir, state)

	require.NoError(t, err)
	assert.Equal(t, []string{"photos"}, applied)
	require.Len(t, state.PlatformApps, 1)
	assert.Equal(t, "photos", state.PlatformApps[0].Name)
	assert.Equal(t, models.PAASNone, state.PlatformApps[0].Platform)
	assert.Equal(t, platformdeploy.AppManagementFallback, state.PlatformApps[0].Management)
	assert.Equal(t, "local-compose:photos", state.PlatformApps[0].ExternalID)
}

func TestRunPlatformAppDeploymentsRecordsUserAppHandoffWithoutPlatformConfig(t *testing.T) {
	t.Setenv("STACKKIT_PLATFORM_ENDPOINT", "")
	t.Setenv("STACKKIT_PLATFORM_TOKEN", "")
	t.Setenv("DOKPLOY_API_URL", "")
	t.Setenv("DOKPLOY_API_KEY", "")

	wd := t.TempDir()
	deployDir := filepath.Join(wd, "deploy")
	require.NoError(t, os.MkdirAll(filepath.Join(deployDir, "platform-apps"), 0750))
	require.NoError(t, os.WriteFile(filepath.Join(deployDir, "platform-apps", "web.compose.yaml"), []byte("services:\n  web:\n    image: ghcr.io/kombifyio/stackkits-smoke-sveltekit:0.1.0\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(deployDir, "platform-apps", "manifest.json"), []byte(`{
  "version": "stackkit.platform-apps/v2",
  "platform": "dokploy",
  "apps": [{
    "name": "web",
    "kind": "sveltekit",
    "managedBy": "dokploy",
    "composePath": "platform-apps/web.compose.yaml"
  }]
}`), 0600))

	state := &models.DeploymentState{}
	err := runPlatformAppDeployments(context.Background(), deployDir, state)

	require.NoError(t, err)
	require.Len(t, state.PlatformApps, 1)
	assert.Equal(t, "web", state.PlatformApps[0].Name)
	assert.Equal(t, models.PAASDokploy, state.PlatformApps[0].Platform)
	assert.Equal(t, platformdeploy.AppManagementHandoff, state.PlatformApps[0].Management)
	assert.Equal(t, "platform-apps/web.compose.yaml", state.PlatformApps[0].ComposePath)
	assert.Empty(t, state.PlatformApps[0].ExternalID)
	assert.Empty(t, state.PlatformApps[0].DeploymentID)
}

type platformAdapterFunc func(context.Context, platformdeploy.AppManifest) (platformdeploy.DeploymentRef, error)

func (f platformAdapterFunc) ApplyCompose(ctx context.Context, app platformdeploy.AppManifest) (platformdeploy.DeploymentRef, error) {
	return f(ctx, app)
}
