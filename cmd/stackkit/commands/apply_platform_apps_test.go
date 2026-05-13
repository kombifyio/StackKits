package commands

import (
	"context"
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

func TestRecordPlatformAppStateSeparatesSystemApps(t *testing.T) {
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
	require.Len(t, state.PlatformApps, 1)
	assert.Equal(t, "immich", state.PlatformApps[0].Name)
	assert.Empty(t, state.PlatformApps[0].Role)
	assert.Equal(t, platformdeploy.SetupPolicyManual, state.PlatformApps[0].SetupPolicy)
	require.Len(t, state.PlatformApps[0].SetupDrops, 1)
	assert.Equal(t, "immich-owner-bootstrap", state.PlatformApps[0].SetupDrops[0].Name)
	assert.Empty(t, state.SetupRuns, "manual setup drops are metadata until explicitly requested")
}

func TestRecordPlatformAppStateUpsertsExistingApps(t *testing.T) {
	state := &models.DeploymentState{
		PlatformApps: []models.PlatformAppState{{
			Name:       "web",
			Platform:   models.PAASDokploy,
			ExternalID: "old-web",
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
	assert.Equal(t, "new-web", state.PlatformApps[0].ExternalID)
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

func TestRunPlatformAppDeploymentsFailsWhenUserAppsNeedUnconfiguredPlatform(t *testing.T) {
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

	err := runPlatformAppDeployments(context.Background(), deployDir, &models.DeploymentState{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "platform app rollout for dokploy requires API endpoint and token")
	assert.Contains(t, err.Error(), "STACKKIT_PLATFORM_ENDPOINT")
}
