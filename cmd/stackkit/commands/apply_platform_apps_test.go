package commands

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/kombifyio/stackkits/internal/platformdeploy"
	"github.com/kombifyio/stackkits/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPlatformAdapterForBundleUsesLocalComposeForNone(t *testing.T) {
	adapter, configured, err := platformAdapterForBundle(platformdeploy.BundleManifest{
		Platform: models.PAASNone,
	}, "/opt/stackkit/deploy")

	require.NoError(t, err)
	assert.True(t, configured)
	assert.NotNil(t, adapter)
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
	assert.Equal(t, "platform-apps/web.compose.yaml", state.PlatformApps[0].ComposePath)
	assert.Empty(t, state.PlatformApps[0].ExternalID)
	assert.Empty(t, state.PlatformApps[0].DeploymentID)
}
