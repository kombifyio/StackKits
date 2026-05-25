package cue

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/kombifyio/stackkits/internal/platformdeploy"
	"github.com/kombifyio/stackkits/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateAppsTF_SvelteKitContract(t *testing.T) {
	outputDir := t.TempDir()
	spec := &models.StackSpec{
		Domain: "home.lab",
		PAAS:   models.PAASDokploy,
		Apps: map[string]models.AppSpec{
			"web": {
				Kind:  "sveltekit",
				Image: "ghcr.io/kombify/example-sveltekit:latest",
				Port:  3000,
				Route: models.AppRouteSpec{
					Host: "app.home.lab",
					Auth: "login-gateway",
				},
				Health: models.AppHealthSpec{Path: "/health"},
				Env: map[string]string{
					"PUBLIC_APP_NAME": "My App",
				},
				Secrets: map[string]string{
					"SESSION_SECRET": "env:SESSION_SECRET",
				},
			},
		},
	}

	require.NoError(t, GenerateAppsTF(spec, outputDir))

	data, err := os.ReadFile(filepath.Join(outputDir, "apps.tf"))
	require.NoError(t, err)
	content := string(data)

	assert.NotContains(t, content, `resource "docker_image"`)
	assert.NotContains(t, content, `resource "docker_container"`)
	assert.NotContains(t, content, `docker compose`)
	assert.Contains(t, content, `resource "local_file" "platform_app_web_compose"`)
	assert.Contains(t, content, `resource "local_file" "platform_apps_manifest"`)
	assert.Contains(t, content, `platform-apps/web.compose.yaml`)
	assert.Contains(t, content, `platform-apps/manifest.json`)
	assert.Contains(t, content, `variable "app_web_secret_session_secret"`)

	composeData, err := os.ReadFile(filepath.Join(outputDir, "platform-apps", "web.compose.yaml"))
	require.NoError(t, err)
	compose := string(composeData)
	assert.Contains(t, compose, `ghcr.io/kombify/example-sveltekit:latest`)
	assert.Contains(t, compose, `container_name: app-web`)
	assert.Contains(t, compose, `PUBLIC_APP_NAME: "My App"`)
	assert.Contains(t, compose, `SESSION_SECRET: "${APP_WEB_SECRET_SESSION_SECRET}"`)
	assert.Contains(t, compose, `stackkit.managed-by=dokploy`)
	assert.Contains(t, compose, `traefik.docker.network=dokploy-network`)
	assert.Contains(t, compose, `traefik.http.routers.app-web.rule=Host(`+"`"+`app.home.lab`+"`"+`)`)
	assert.Contains(t, compose, `traefik.http.routers.app-web.entrypoints=web`)
	assert.Contains(t, compose, `traefik.http.routers.app-web.middlewares=tinyauth@docker`)
	assert.NotContains(t, compose, `coolify.traefik.middlewares`)
	assert.Contains(t, compose, `name: dokploy-network`)

	manifestData, err := os.ReadFile(filepath.Join(outputDir, "platform-apps", "manifest.json"))
	require.NoError(t, err)
	var bundle platformdeploy.BundleManifest
	require.NoError(t, json.Unmarshal(manifestData, &bundle))
	assert.Equal(t, "stackkit.platform-apps/v2", bundle.Version)
	require.Equal(t, models.PAASDokploy, bundle.Platform)
	require.Len(t, bundle.Apps, 1)
	assert.Equal(t, "web", bundle.Apps[0].Name)
	assert.Equal(t, "platform-apps/web.compose.yaml", bundle.Apps[0].ComposePath)
	assert.Equal(t, models.PAASDokploy, bundle.Apps[0].ManagedBy)
	assert.Equal(t, platformdeploy.SetupPolicyManual, bundle.Apps[0].SetupPolicy)
	assert.Empty(t, bundle.Apps[0].SetupDrops)
}

func TestGenerateAppsTF_CoolifyDefaultUsesCoolifyRoutingContract(t *testing.T) {
	outputDir := t.TempDir()
	spec := &models.StackSpec{
		Domain:  "home.localhost",
		Context: string(models.ContextLocal),
		Apps: map[string]models.AppSpec{
			"web": {
				Image: "ghcr.io/kombify/example-sveltekit:latest",
				Port:  3000,
			},
		},
	}

	require.NoError(t, GenerateAppsTF(spec, outputDir))

	composeData, err := os.ReadFile(filepath.Join(outputDir, "platform-apps", "web.compose.yaml"))
	require.NoError(t, err)
	compose := string(composeData)
	assert.Contains(t, compose, `stackkit.managed-by=coolify`)
	assert.Contains(t, compose, `coolify.traefik.middlewares=tinyauth@file`)
	assert.Contains(t, compose, `traefik.docker.network=coolify`)
	assert.Contains(t, compose, `traefik.http.routers.app-web.entrypoints=http`)
	assert.Contains(t, compose, `traefik.http.routers.app-web.middlewares=tinyauth@file`)
	assert.NotContains(t, compose, `traefik.http.routers.app-web.middlewares=tinyauth@docker`)
	assert.NotContains(t, compose, `traefik.http.routers.app-web.entrypoints=web`)
	assert.Contains(t, compose, `name: coolify`)

	manifestData, err := os.ReadFile(filepath.Join(outputDir, "platform-apps", "manifest.json"))
	require.NoError(t, err)
	var bundle platformdeploy.BundleManifest
	require.NoError(t, json.Unmarshal(manifestData, &bundle))
	assert.Equal(t, models.PAASCoolify, bundle.Platform)
	require.Len(t, bundle.Apps, 1)
	assert.Equal(t, models.PAASCoolify, bundle.Apps[0].ManagedBy)
}

func TestGenerateAppsTF_NoAppsNoFile(t *testing.T) {
	outputDir := t.TempDir()
	require.NoError(t, GenerateAppsTF(&models.StackSpec{}, outputDir))

	_, err := os.Stat(filepath.Join(outputDir, "apps.tf"))
	assert.True(t, os.IsNotExist(err))
}

func TestGenerateAppsTF_KombifyMeDefaultsAppHostToRegisteredPrefix(t *testing.T) {
	outputDir := t.TempDir()
	spec := &models.StackSpec{
		Domain:          models.DomainKombifyMe,
		SubdomainPrefix: "sh-demo-abc123",
		Context:         string(models.ContextCloud),
		Apps: map[string]models.AppSpec{
			"web": {
				Image: "ghcr.io/kombifyio/stackkits-smoke-sveltekit:0.1.0",
			},
		},
	}

	require.NoError(t, GenerateAppsTF(spec, outputDir))

	composeData, err := os.ReadFile(filepath.Join(outputDir, "platform-apps", "web.compose.yaml"))
	require.NoError(t, err)
	assert.Contains(t, string(composeData), "Host(`sh-demo-abc123-web.kombify.me`)")

	manifestData, err := os.ReadFile(filepath.Join(outputDir, "platform-apps", "manifest.json"))
	require.NoError(t, err)
	var bundle platformdeploy.BundleManifest
	require.NoError(t, json.Unmarshal(manifestData, &bundle))
	require.Len(t, bundle.Apps, 1)
	assert.Equal(t, "sh-demo-abc123-web.kombify.me", bundle.Apps[0].Host)
}

func TestGenerateAppsTF_PreservesSetupDropContract(t *testing.T) {
	outputDir := t.TempDir()
	spec := &models.StackSpec{
		Domain: "home.lab",
		PAAS:   models.PAASDokploy,
		Apps: map[string]models.AppSpec{
			"photos": {
				Image: "ghcr.io/example/photos:latest",
				Setup: models.AppSetupSpec{
					Policy: platformdeploy.SetupPolicyOnDemand,
					Drops: []models.SetupDropSpec{{
						Name:        "photos-owner-bootstrap",
						Version:     "0.1.0",
						Runner:      "stackkit-script",
						Description: "Create the first owner only when requested.",
					}},
				},
			},
		},
	}

	require.NoError(t, GenerateAppsTF(spec, outputDir))

	manifestData, err := os.ReadFile(filepath.Join(outputDir, "platform-apps", "manifest.json"))
	require.NoError(t, err)
	var bundle platformdeploy.BundleManifest
	require.NoError(t, json.Unmarshal(manifestData, &bundle))

	require.Len(t, bundle.Apps, 1)
	assert.Equal(t, platformdeploy.SetupPolicyOnDemand, bundle.Apps[0].SetupPolicy)
	require.Len(t, bundle.Apps[0].SetupDrops, 1)
	assert.Equal(t, "photos-owner-bootstrap", bundle.Apps[0].SetupDrops[0].Name)
	assert.Equal(t, "stackkit-script", bundle.Apps[0].SetupDrops[0].Runner)
}
