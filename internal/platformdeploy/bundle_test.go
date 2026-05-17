package platformdeploy

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/kombifyio/stackkits/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadBundleManifestReadsComposeFiles(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, "platform-apps"), 0750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "platform-apps", "web.compose.yaml"), []byte("services:\n  web:\n    image: nginx\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "platform-apps", "manifest.json"), []byte(`{
  "version": "stackkit.platform-apps/v1",
  "platform": "dokploy",
  "apps": [
    {
      "name": "web",
      "platform": "dokploy",
      "managedBy": "dokploy",
      "composePath": "platform-apps/web.compose.yaml"
    }
  ]
}`), 0600))

	bundle, err := LoadBundleManifest(filepath.Join(dir, "platform-apps", "manifest.json"))

	require.NoError(t, err)
	require.Len(t, bundle.Apps, 1)
	assert.Equal(t, "services:\n  web:\n    image: nginx\n", bundle.Apps[0].ComposeYAML)
}

func TestLoadBundleManifestV2ReadsSystemAppsBeforeApps(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, "platform-apps"), 0750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "platform-apps", "hub.compose.yaml"), []byte("services:\n  hub:\n    image: nginx\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "platform-apps", "web.compose.yaml"), []byte("services:\n  web:\n    image: nginx\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "platform-apps", "manifest.json"), []byte(`{
  "version": "stackkit.platform-apps/v2",
  "platform": "dokploy",
  "systemApps": [
    {
      "name": "stackkit-hub",
      "role": "node-hub",
      "composePath": "platform-apps/hub.compose.yaml"
    }
  ],
  "apps": [
    {
      "name": "web",
      "composePath": "platform-apps/web.compose.yaml"
    }
  ]
}`), 0600))

	bundle, err := LoadBundleManifest(filepath.Join(dir, "platform-apps", "manifest.json"))

	require.NoError(t, err)
	require.Len(t, bundle.SystemApps, 1)
	assert.Equal(t, "stackkit-hub", bundle.SystemApps[0].Name)
	assert.Equal(t, "node-hub", bundle.SystemApps[0].Role)
	assert.Equal(t, models.PAASDokploy, bundle.SystemApps[0].Platform)
	assert.Equal(t, models.PAASDokploy, bundle.SystemApps[0].ManagedBy)
	assert.Equal(t, "services:\n  hub:\n    image: nginx\n", bundle.SystemApps[0].ComposeYAML)
	require.Len(t, bundle.Apps, 1)
	assert.Equal(t, "services:\n  web:\n    image: nginx\n", bundle.Apps[0].ComposeYAML)
}

func TestLoadBundleManifestPreservesSetupDrops(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, "platform-apps"), 0750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "platform-apps", "immich.compose.yaml"), []byte("services:\n  immich:\n    image: immich\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "platform-apps", "manifest.json"), []byte(`{
  "version": "stackkit.platform-apps/v2",
  "platform": "dokploy",
  "apps": [
    {
      "name": "immich",
      "composePath": "platform-apps/immich.compose.yaml",
      "setupPolicy": "manual",
      "setupDrops": [
        {
          "name": "immich-owner-bootstrap",
          "version": "0.1.0",
          "runner": "stackkit-script",
          "description": "Create the first Immich owner and mark onboarding complete."
        }
      ]
    }
  ]
}`), 0600))

	bundle, err := LoadBundleManifest(filepath.Join(dir, "platform-apps", "manifest.json"))

	require.NoError(t, err)
	require.Len(t, bundle.Apps, 1)
	assert.Equal(t, SetupPolicyManual, bundle.Apps[0].SetupPolicy)
	require.Len(t, bundle.Apps[0].SetupDrops, 1)
	assert.Equal(t, "immich-owner-bootstrap", bundle.Apps[0].SetupDrops[0].Name)
	assert.Equal(t, "stackkit-script", bundle.Apps[0].SetupDrops[0].Runner)
	assert.Equal(t, "services:\n  immich:\n    image: immich\n", bundle.Apps[0].ComposeYAML)
}

func TestApplyBundleIgnoresUserApps(t *testing.T) {
	adapter := &recordingAdapter{}

	refs, err := ApplyBundle(context.Background(), adapter, BundleManifest{
		Platform: models.PAASDokploy,
		Apps: []AppManifest{{
			Name:        "web",
			Platform:    models.PAASDokploy,
			ManagedBy:   models.PAASDokploy,
			ComposeYAML: "services: {}\n",
		}},
	})

	require.NoError(t, err)
	assert.Empty(t, refs)
	assert.Empty(t, adapter.names)
}

func TestApplyBundleDeploysOnlySystemApps(t *testing.T) {
	adapter := &recordingAdapter{}

	refs, err := ApplyBundle(context.Background(), adapter, BundleManifest{
		Platform: models.PAASDokploy,
		SystemApps: []SystemAppManifest{{
			AppManifest: AppManifest{
				Name:        "stackkit-hub",
				ComposeYAML: "services: {}\n",
			},
			Role: "node-hub",
		}},
		Apps: []AppManifest{{
			Name:        "web",
			ComposeYAML: "services: {}\n",
		}},
	})

	require.NoError(t, err)
	assert.Equal(t, []string{"stackkit-hub"}, adapter.names)
	require.Len(t, refs, 1)
	assert.Equal(t, "external-stackkit-hub", refs[0].ExternalID)
}

type recordingAdapter struct {
	names []string
}

func (a *recordingAdapter) ApplyCompose(_ context.Context, manifest AppManifest) (DeploymentRef, error) {
	a.names = append(a.names, manifest.Name)
	return DeploymentRef{
		Platform:   manifest.ManagedBy,
		AppName:    manifest.Name,
		ExternalID: "external-" + manifest.Name,
	}, nil
}
