package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
