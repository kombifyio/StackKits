package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManagementStatusAndPlan(t *testing.T) {
	srv, tmpDir := testServer(t)
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "stack-spec.yaml"), []byte(`name: test
stackkit: base-kit
mode: simple
domain: home.localhost
adminEmail: admin@example.com
network:
  mode: local
compute:
  tier: standard
`), 0600))
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "deploy"), 0750))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "deploy", "main.tf"), []byte("# generated"), 0600))

	handler := srv.Handler()

	req := httptest.NewRequest("GET", "/api/v1/status", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	resp := parseResponse(t, rec)
	var status map[string]any
	require.NoError(t, json.Unmarshal(resp["data"], &status))
	assert.Equal(t, true, status["specLoaded"])
	assert.Equal(t, "base-kit", status["stackkit"])

	req = httptest.NewRequest("POST", "/api/v1/plan", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	resp = parseResponse(t, rec)
	var plan map[string]any
	require.NoError(t, json.Unmarshal(resp["data"], &plan))
	assert.Equal(t, true, plan["dryRun"])
	assert.Equal(t, false, plan["mutation"])
	assert.Equal(t, "ready", plan["status"])
}

func TestManagementDoctorAndVerifyAreReadOnly(t *testing.T) {
	srv, _ := testServer(t)
	handler := srv.Handler()

	req := httptest.NewRequest("POST", "/api/v1/doctor", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "checks")

	req = httptest.NewRequest("POST", "/api/v1/verify", strings.NewReader(`{"http":false,"strict":true}`))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "deployment-state")
}

func TestRunEvidence(t *testing.T) {
	srv, tmpDir := testServer(t)
	runDir := filepath.Join(tmpDir, ".stackkit", "runs", "20260517-120000")
	require.NoError(t, os.MkdirAll(runDir, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(runDir, "metadata.json"), []byte(`{"runId":"20260517-120000"}`), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(runDir, "events.jsonl"), []byte(`{"phase":"verify","status":"pass"}`+"\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(runDir, "summary.json"), []byte(`{"status":"pass"}`), 0600))

	req := httptest.NewRequest("GET", "/api/v1/runs/20260517-120000/evidence", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `"runId":"20260517-120000"`)
	assert.Contains(t, rec.Body.String(), `"events"`)

	req = httptest.NewRequest("GET", "/api/v1/runs/bad/evidence", nil)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}
