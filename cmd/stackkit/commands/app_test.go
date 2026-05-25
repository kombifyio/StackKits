package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kombifyio/stackkits/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestAppAddSpecAddsSvelteKitHandoffAndForcesStandardPAAS(t *testing.T) {
	spec := &models.StackSpec{
		Name:     "homelab",
		StackKit: "base-kit",
		Context:  string(models.ContextLocal),
		Domain:   models.DomainHomeLab,
		Network:  models.NetworkSpec{Mode: "local"},
		PAAS:     models.PAASNone,
		Services: map[string]any{
			"coolify": map[string]any{"enabled": false},
		},
	}

	err := addAppToSpec(spec, appAddOptions{
		Name:       "web",
		Image:      "ghcr.io/kombifyio/stackkits-smoke-sveltekit:0.1.0",
		Port:       3000,
		Auth:       "login-gateway",
		HealthPath: "/health",
		Env: map[string]string{
			"PUBLIC_APP_NAME": "StackKit Smoke",
		},
		Secrets: map[string]string{
			"SESSION_SECRET": "env:SESSION_SECRET",
		},
	})

	require.NoError(t, err)
	assert.Equal(t, models.PAASCoolify, spec.PAAS)
	require.Contains(t, spec.Apps, "web")
	app := spec.Apps["web"]
	assert.Equal(t, "sveltekit", app.Kind)
	assert.Equal(t, "ghcr.io/kombifyio/stackkits-smoke-sveltekit:0.1.0", app.Image)
	assert.Equal(t, 3000, app.Port)
	assert.Equal(t, "login-gateway", app.Route.Auth)
	assert.Equal(t, "/health", app.Health.Path)
	assert.Equal(t, "StackKit Smoke", app.Env["PUBLIC_APP_NAME"])
	assert.Equal(t, "env:SESSION_SECRET", app.Secrets["SESSION_SECRET"])

	coolify, ok := spec.Services["coolify"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, true, coolify["enabled"])
}

func TestAppAddCommandWritesStackSpec(t *testing.T) {
	tmpDir := t.TempDir()
	specPath := filepath.Join(tmpDir, "stack-spec.yaml")
	require.NoError(t, os.WriteFile(specPath, []byte(`name: homelab
stackkit: base-kit
mode: simple
context: cloud
domain: kombify.me
network:
  mode: public
compute:
  tier: standard
`), 0600))

	_, err := executeCommand(
		"--no-log",
		"--chdir", tmpDir,
		"app", "add", "web",
		"--image", "ghcr.io/kombifyio/stackkits-smoke-sveltekit:0.1.0",
		"--port", "3000",
		"--auth", "public",
		"--host", "web.example.com",
		"--env", "PUBLIC_APP_NAME=StackKit Smoke",
		"--secret", "SESSION_SECRET=env:SESSION_SECRET",
	)

	require.NoError(t, err)

	data, err := os.ReadFile(specPath)
	require.NoError(t, err)
	var spec models.StackSpec
	require.NoError(t, yaml.Unmarshal(data, &spec))

	require.Contains(t, spec.Apps, "web")
	app := spec.Apps["web"]
	assert.Equal(t, "sveltekit", app.Kind)
	assert.Equal(t, "ghcr.io/kombifyio/stackkits-smoke-sveltekit:0.1.0", app.Image)
	assert.Equal(t, "public", app.Route.Auth)
	assert.Equal(t, "web.example.com", app.Route.Host)
	assert.Equal(t, "StackKit Smoke", app.Env["PUBLIC_APP_NAME"])
	assert.Equal(t, "env:SESSION_SECRET", app.Secrets["SESSION_SECRET"])
}

func TestAppAddRejectsMissingImage(t *testing.T) {
	err := addAppToSpec(&models.StackSpec{Name: "homelab", StackKit: "base-kit"}, appAddOptions{Name: "web"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "image is required")
}

func TestAppAddRejectsInvalidRouteHost(t *testing.T) {
	err := addAppToSpec(&models.StackSpec{Name: "homelab", StackKit: "base-kit"}, appAddOptions{
		Name:       "web",
		Image:      "ghcr.io/kombifyio/stackkits-smoke-sveltekit:0.1.0",
		Host:       "https://app.example.com/path",
		HealthPath: "/health",
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "route host")
}

func TestBaseInstallerDevGatesSvelteKitAppHandoffEnvironment(t *testing.T) {
	cwd, err := os.Getwd()
	require.NoError(t, err)
	repoRoot := filepath.Clean(filepath.Join(cwd, "..", "..", ".."))

	data, err := os.ReadFile(filepath.Join(repoRoot, "base-install.sh"))
	require.NoError(t, err)
	installer := string(data)

	for _, needle := range []string{
		"STACKKIT_ENABLE_DEV_APP_HANDOFF",
		"STACKKIT_DEV_APP_IMAGE",
		"STACKKIT_APP_IMAGE",
		"STACKKIT_APP_NAME",
		"STACKKIT_APP_AUTH",
		"app add \"$APP_NAME\"",
		"configure_dns_tls_provider",
		"STACKKIT_DNS_TOKEN",
		"CLOUDFLARE_API_TOKEN",
		"tls:",
		"provider: $DNS_PROVIDER",
		"persist_platform_config",
		"STACKKIT_PLATFORM_ENDPOINT",
		"STACKKIT_PLATFORM_TOKEN",
		".stackkit/platform.json",
	} {
		assert.True(t, strings.Contains(installer, needle), "base installer should contain %q", needle)
	}
	assert.Contains(t, installer, `if [ "${STACKKIT_ENABLE_DEV_APP_HANDOFF:-false}" = "true" ]`)
}

func TestStatusJSONOutputIncludesPlatformApps(t *testing.T) {
	output := buildStatusJSONOutput(
		&models.StackSpec{StackKit: "base-kit", Mode: "simple"},
		&models.DeploymentState{
			LastApplied: time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC),
			PlatformApps: []models.PlatformAppState{{
				Name:       "web",
				Platform:   models.PAASCoolify,
				ExternalID: "coolify-web-1",
			}},
		},
		nil,
		models.StatusRunning,
		nil,
	)

	apps, ok := output["platformApps"].([]models.PlatformAppState)
	require.True(t, ok)
	require.Len(t, apps, 1)
	assert.Equal(t, "web", apps[0].Name)
	assert.Equal(t, "coolify-web-1", apps[0].ExternalID)
}

func TestKombifyMeAppServiceRegistrations(t *testing.T) {
	services := kombifyMeAppServiceRegistrations(&models.StackSpec{
		Domain: models.DomainKombifyMe,
		Apps: map[string]models.AppSpec{
			"web": {
				Image: "ghcr.io/kombifyio/stackkits-smoke-sveltekit:0.1.0",
			},
			"studio": {
				Image: "ghcr.io/kombifyio/studio:1.0.0",
			},
		},
	})

	names := make([]string, 0, len(services))
	for _, svc := range services {
		names = append(names, svc.Name)
	}

	assert.ElementsMatch(t, []string{"web", "studio"}, names)
	for _, svc := range services {
		assert.True(t, svc.Primary)
		assert.Contains(t, svc.Description, "StackKit app")
	}
}
