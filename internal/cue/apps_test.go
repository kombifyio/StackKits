package cue

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kombifyio/stackkits/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateAppsTF_SvelteKitContract(t *testing.T) {
	outputDir := t.TempDir()
	spec := &models.StackSpec{
		Domain: "home.lab",
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

	assert.Contains(t, content, `resource "docker_image" "app_web"`)
	assert.Contains(t, content, `name = "ghcr.io/kombify/example-sveltekit:latest"`)
	assert.Contains(t, content, `resource "docker_container" "app_web"`)
	assert.Contains(t, content, `name     = "app-web"`)
	assert.Contains(t, content, `"PUBLIC_APP_NAME=My App"`)
	assert.Contains(t, content, `variable "app_web_secret_session_secret"`)
	assert.Contains(t, content, `"SESSION_SECRET=${var.app_web_secret_session_secret}"`)
	assert.Contains(t, content, `value = "Host(`+"`"+`app.home.lab`+"`"+`)"`)
	assert.Contains(t, content, `value = "tinyauth@docker"`)
	assert.Contains(t, content, `fetch('http://127.0.0.1:3000/health')`)
}

func TestGenerateAppsTF_NoAppsNoFile(t *testing.T) {
	outputDir := t.TempDir()
	require.NoError(t, GenerateAppsTF(&models.StackSpec{}, outputDir))

	_, err := os.Stat(filepath.Join(outputDir, "apps.tf"))
	assert.True(t, os.IsNotExist(err))
}
