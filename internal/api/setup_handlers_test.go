package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSetupServiceRunAcceptsOnDemandSetup(t *testing.T) {
	srv, tmpDir := testServer(t)
	writeSetupManifest(t, tmpDir)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/setup/services/photos/run", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusAccepted, rec.Code)
	resp := parseResponse(t, rec)
	var data map[string]any
	require.NoError(t, json.Unmarshal(resp["data"], &data))
	require.Equal(t, "photos", data["serviceKey"])
	require.Equal(t, "immich", data["appName"])
	require.Equal(t, "on_demand", data["setupPolicy"])
	require.Equal(t, "dry-run", data["mode"])
	require.Equal(t, "ready", data["status"])
	require.Len(t, data["drops"], 1)
}

func TestSetupServiceRunRequiresManifestForOnDemandSetup(t *testing.T) {
	srv, _ := testServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/setup/services/photos/run", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusConflict, rec.Code)
}

func TestSetupServiceRunRejectsManualOnlySetup(t *testing.T) {
	srv, _ := testServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/setup/services/vault/run", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusConflict, rec.Code)
}

func TestSetupServiceRunReportsAutomaticPlatformSetup(t *testing.T) {
	srv, _ := testServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/setup/services/kuma/run", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	resp := parseResponse(t, rec)
	var data map[string]any
	require.NoError(t, json.Unmarshal(resp["data"], &data))
	require.Equal(t, "kuma", data["serviceKey"])
	require.Equal(t, "automatic", data["setupPolicy"])
	require.Equal(t, "already_automatic", data["status"])
}

func TestSetupServiceRunExecutesImmichOwnerBootstrap(t *testing.T) {
	srv, tmpDir := testServer(t)
	writeSetupManifest(t, tmpDir)

	var paths []string
	immich := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		if r.URL.Path != "/api/server/config" && r.Header.Get("Authorization") != "" {
			require.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "GET /api/server/config":
			_, _ = w.Write([]byte(`{"isInitialized":false}`))
		case "POST /api/auth/admin-sign-up":
			_, _ = w.Write([]byte(`{}`))
		case "POST /api/auth/login":
			_, _ = w.Write([]byte(`{"accessToken":"test-token"}`))
		case "PUT /api/users/me", "PUT /api/users/me/onboarding", "POST /api/system-metadata/admin-onboarding":
			_, _ = w.Write([]byte(`{}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer immich.Close()

	srv.config.SetupActionMode = setupActionModeApply
	srv.config.SetupAdminEmail = "admin@example.test"
	srv.config.SetupAdminPassword = "secret"
	srv.config.SetupImmichURL = immich.URL

	req := httptest.NewRequest(http.MethodPost, "/api/v1/setup/services/photos/run", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	resp := parseResponse(t, rec)
	var data map[string]any
	require.NoError(t, json.Unmarshal(resp["data"], &data))
	require.Equal(t, "completed", data["status"])
	require.Equal(t, "apply", data["mode"])
	require.Equal(t, []string{
		"GET /api/server/config",
		"POST /api/auth/admin-sign-up",
		"POST /api/auth/login",
		"PUT /api/users/me",
		"PUT /api/users/me/onboarding",
		"POST /api/system-metadata/admin-onboarding",
	}, paths)
}

func TestBaseHubProtectionStatusReadsBootstrapState(t *testing.T) {
	srv, tmpDir := testServer(t)
	writeBaseHubTFVars(t, tmpDir, `{"enable_tinyauth":true,"protect_base_hub":false,"network_mode":"bridge"}`)
	require.NoError(t, writeBaseHubProtectionDynamicConfig(filepath.Join(tmpDir, "deploy", "traefik-dynamic", "stackkit.yml"), "bridge", false))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/setup/base-hub/protection", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	resp := parseResponse(t, rec)
	var data map[string]any
	require.NoError(t, json.Unmarshal(resp["data"], &data))
	require.Equal(t, "bootstrap_open", data["status"])
	require.Equal(t, false, data["protected"])
}

func TestBaseHubProtectionAppliesDynamicMiddlewareAndPersistsTFVars(t *testing.T) {
	srv, tmpDir := testServer(t)
	writeBaseHubTFVars(t, tmpDir, `{"enable_tinyauth":true,"protect_base_hub":false,"network_mode":"host"}`)
	srv.config.SetupActionMode = setupActionModeApply

	req := httptest.NewRequest(http.MethodPost, "/api/v1/setup/base-hub/protection", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	resp := parseResponse(t, rec)
	var data map[string]any
	require.NoError(t, json.Unmarshal(resp["data"], &data))
	require.Equal(t, "completed", data["status"])
	require.Equal(t, true, data["protected"])

	tfvarsData, err := os.ReadFile(filepath.Join(tmpDir, "deploy", "terraform.tfvars.json"))
	require.NoError(t, err)
	var tfvars map[string]any
	require.NoError(t, json.Unmarshal(tfvarsData, &tfvars))
	require.Equal(t, true, tfvars["protect_base_hub"])

	dynamicData, err := os.ReadFile(filepath.Join(tmpDir, "deploy", "traefik-dynamic", "stackkit.yml"))
	require.NoError(t, err)
	dynamic := string(dynamicData)
	require.Contains(t, dynamic, "forwardAuth:")
	require.Contains(t, dynamic, "http://127.0.0.1:3004/api/auth/traefik")
	require.NotContains(t, dynamic, "protect_base_hub")
}

func TestBaseHubProtectionRejectsWhenTinyAuthDisabled(t *testing.T) {
	srv, tmpDir := testServer(t)
	writeBaseHubTFVars(t, tmpDir, `{"enable_tinyauth":false,"protect_base_hub":false}`)
	srv.config.SetupActionMode = setupActionModeApply

	req := httptest.NewRequest(http.MethodPost, "/api/v1/setup/base-hub/protection", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusConflict, rec.Code)
	require.Contains(t, rec.Body.String(), "base_hub_protection_requires_tinyauth")
}

func writeSetupManifest(t *testing.T, baseDir string) {
	t.Helper()
	manifest := `{
  "version": "stackkit.platform-apps/v2",
  "platform": "coolify",
  "apps": [
    {
      "name": "immich",
      "kind": "compose",
      "platform": "coolify",
      "managedBy": "coolify",
      "setupPolicy": "on_demand",
      "setupDrops": [
        {
          "name": "immich-owner-bootstrap",
          "version": "0.1.0",
          "runner": "stackkit-script",
          "description": "Create the first Immich owner and mark onboarding complete when explicitly requested."
        }
      ]
    }
  ]
}`
	require.NoError(t, os.WriteFile(filepath.Join(baseDir, ".platform-apps-manifest.json"), []byte(manifest), 0600))
}

func writeBaseHubTFVars(t *testing.T, baseDir, content string) {
	t.Helper()
	deployDir := filepath.Join(baseDir, "deploy")
	require.NoError(t, os.MkdirAll(deployDir, 0750))
	content = strings.TrimSpace(content) + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(deployDir, "terraform.tfvars.json"), []byte(content), 0600))
}
