package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kombifyio/stackkits/internal/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRuntimeActionEndpoints_AcceptTechStackServicecallWithoutAPIKey(t *testing.T) {
	srv, _ := testServer(t)
	srv.config.APIKey = "external-api-key"
	srv.config.ServiceAuthSecret = "shared-secret"
	srv.config.RuntimeActionMode = "dry-run"

	tofuDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(tofuDir, "main.tf"), []byte("terraform {}\n"), 0600))
	unifiedPath := filepath.Join(t.TempDir(), "stack-spec.json")
	require.NoError(t, os.WriteFile(unifiedPath, []byte(`{"stack_id":"stack-123"}`), 0600))

	cases := []struct {
		name   string
		path   string
		action string
	}{
		{"rollout", "/api/v1/internal/runtime-actions/stackkit-rollout", "stackkit_rollout"},
		{"verify", "/api/v1/internal/runtime-actions/stackkit-verify", "verify_rollout"},
		{"restore drill", "/api/v1/internal/runtime-actions/restore-drill", "restore_drill"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := postRuntimeAction(t, srv, tc.path, "shared-secret", map[string]any{
				"action":       tc.action,
				"stack_id":     "stack-123",
				"stack_name":   "Managed Base",
				"stackkit":     "base-kit",
				"tofu_dir":     tofuDir,
				"unified_path": unifiedPath,
			})

			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			resp := parseResponse(t, rec)
			var data runtimeActionResponse
			require.NoError(t, json.Unmarshal(resp["data"], &data))
			assert.Equal(t, tc.action, data.Action)
			assert.Equal(t, "stack-123", data.StackID)
			assert.Equal(t, "Managed Base", data.StackName)
			assert.Equal(t, "base-kit", data.StackKit)
			assert.Equal(t, tofuDir, data.TofuDir)
			assert.Equal(t, unifiedPath, data.UnifiedPath)
			assert.Equal(t, "dry-run", data.Mode)
			assert.NotEmpty(t, data.Checks)
		})
	}
}

func TestRuntimeActionEndpoints_RequireConfiguredServiceAuth(t *testing.T) {
	srv, _ := testServer(t)

	rec := postRuntimeAction(t, srv, "/api/v1/internal/runtime-actions/stackkit-rollout", "shared-secret", map[string]any{
		"action":   "stackkit_rollout",
		"stack_id": "stack-123",
	})

	require.Equal(t, http.StatusServiceUnavailable, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "service_auth_not_configured")
}

func TestRuntimeActionEndpoints_ReturnManagedStackKitOutputs(t *testing.T) {
	srv, _ := testServer(t)
	srv.config.ServiceAuthSecret = "shared-secret"
	srv.config.RuntimeActionMode = "dry-run"

	rec := postRuntimeAction(t, srv, "/api/v1/internal/runtime-actions/stackkit-rollout", "shared-secret", map[string]any{
		"action":     "stackkit_rollout",
		"stack_id":   "stack-123",
		"stack_name": "BaseKit IONOS",
		"stackkit":   "base-kit",
		"owner_spec_bootstrap": map[string]any{
			"endpoint":   "/api/v1/stacks/stack-123/owner-spec",
			"token":      "bootstrap-token",
			"expires_at": "2026-05-14T10:15:00Z",
			"scopes":     []string{"read:owner-spec"},
		},
	})

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	resp := parseResponse(t, rec)
	var data runtimeActionResponse
	require.NoError(t, json.Unmarshal(resp["data"], &data))
	require.NotNil(t, data.StackKitOutputs)
	assert.Equal(t, "owner", data.StackKitOutputs.Identity.Owner.Username)
	assert.Equal(t, "https://basekit-ionos.kombify.me", data.StackKitOutputs.LoginGateway.URL)
	assert.Equal(t, "techstack://recovery/stacks/stack-123", data.StackKitOutputs.Identity.Recovery.BundleRef)
	assert.NotContains(t, rec.Body.String(), "passphrase")
}

func TestRuntimeActionRemoteTargetWritesDockerHostTFVars(t *testing.T) {
	tofuDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(tofuDir, "terraform.tfvars.json"), []byte(`{"domain":"kombify.me"}`), 0600))

	target := normalizeRuntimeActionTarget(&runtimeActionTarget{
		Host: "203.0.113.10",
		User: "ubuntu",
		Port: 2222,
	})
	require.NotNil(t, target)
	dockerHost := runtimeTargetDockerHost(target)
	require.Equal(t, "ssh://ubuntu@203.0.113.10:2222", dockerHost)
	require.NoError(t, writeRuntimeTargetDockerHostTFVars(tofuDir, dockerHost))

	data, err := os.ReadFile(filepath.Join(tofuDir, "terraform.tfvars.json"))
	require.NoError(t, err)
	var values map[string]any
	require.NoError(t, json.Unmarshal(data, &values))
	assert.Equal(t, "kombify.me", values["domain"])
	assert.Equal(t, dockerHost, values["docker_host"])
}

func TestRuntimeActionRemoteTargetSSHCommandDoesNotIncludeHost(t *testing.T) {
	target := normalizeRuntimeActionTarget(&runtimeActionTarget{
		Host: "203.0.113.10",
		User: "root",
		Port: 22,
	})
	require.NotNil(t, target)

	command := runtimeTargetSSHCommand(target, "/tmp/id_runtime")
	assert.Contains(t, command, "-i /tmp/id_runtime")
	assert.NotContains(t, command, "root@203.0.113.10")

	args := runtimeTargetSSHArgs(target, "/tmp/id_runtime")
	assert.Equal(t, "root@203.0.113.10", args[len(args)-1])
}

func TestRuntimeActionEndpoints_RejectWrongCaller(t *testing.T) {
	srv, _ := testServer(t)
	srv.config.ServiceAuthSecret = "shared-secret"

	token, err := auth.SignServiceToken("simulate", "stackkits", "shared-secret", time.Minute)
	require.NoError(t, err)

	body, err := json.Marshal(map[string]any{
		"action":   "stackkit_rollout",
		"stack_id": "stack-123",
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/internal/runtime-actions/stackkit-rollout", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(auth.HeaderServiceAuth, token)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "invalid_service_auth")
}

func TestRuntimeActionEndpoints_RejectMismatchedAction(t *testing.T) {
	srv, _ := testServer(t)
	srv.config.ServiceAuthSecret = "shared-secret"

	rec := postRuntimeAction(t, srv, "/api/v1/internal/runtime-actions/stackkit-verify", "shared-secret", map[string]any{
		"action":   "restore_drill",
		"stack_id": "stack-123",
	})

	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "invalid_runtime_action")
}

func TestRuntimeActionEndpoints_RestoreVerifierCommandApplyMode(t *testing.T) {
	srv, _ := testServer(t)
	srv.config.ServiceAuthSecret = "shared-secret"
	srv.config.RuntimeActionMode = "apply"
	srv.config.RuntimeRestoreVerifierCommand = restoreVerifierHelperCommand(t)
	t.Setenv("STACKKITS_RESTORE_VERIFIER_HELPER", "1")
	t.Setenv("STACKKITS_RESTORE_VERIFIER_MODE", "success")

	tofuDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(tofuDir, "main.tf"), []byte("terraform {}\n"), 0600))
	unifiedPath := filepath.Join(t.TempDir(), "stack-spec.json")
	require.NoError(t, os.WriteFile(unifiedPath, []byte(`{"stack_id":"stack-123"}`), 0600))

	rec := postRuntimeAction(t, srv, "/api/v1/internal/runtime-actions/restore-drill", "shared-secret", map[string]any{
		"action":       "restore_drill",
		"stack_id":     "stack-123",
		"stackkit":     "base-kit",
		"tofu_dir":     tofuDir,
		"unified_path": unifiedPath,
	})

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	resp := parseResponse(t, rec)
	var data runtimeActionResponse
	require.NoError(t, json.Unmarshal(resp["data"], &data))
	assert.Equal(t, "verified", data.Status)
	assert.True(t, hasRuntimeActionCheck(data.Checks, "restore_drill_verifier", "ok"), "checks=%+v", data.Checks)
}

func TestRuntimeActionEndpoints_RestoreVerifierFailureIsStructured(t *testing.T) {
	srv, _ := testServer(t)
	srv.config.ServiceAuthSecret = "shared-secret"
	srv.config.RuntimeActionMode = "apply"
	srv.config.RuntimeRestoreVerifierCommand = restoreVerifierHelperCommand(t)
	t.Setenv("STACKKITS_RESTORE_VERIFIER_HELPER", "1")
	t.Setenv("STACKKITS_RESTORE_VERIFIER_MODE", "fail")

	tofuDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(tofuDir, "main.tf"), []byte("terraform {}\n"), 0600))

	rec := postRuntimeAction(t, srv, "/api/v1/internal/runtime-actions/restore-drill", "shared-secret", map[string]any{
		"action":   "restore_drill",
		"stack_id": "stack-123",
		"stackkit": "base-kit",
		"tofu_dir": tofuDir,
	})

	require.Equal(t, http.StatusBadGateway, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "restore_drill_failed")
	assert.Contains(t, rec.Body.String(), "restore failed")
}

func postRuntimeAction(t *testing.T, srv *Server, path, secret string, payload map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(payload)
	require.NoError(t, err)
	token, err := auth.SignServiceToken("techstack", "stackkits", secret, time.Minute)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(auth.HeaderServiceAuth, token)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func restoreVerifierHelperCommand(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	require.NoError(t, err)
	return exe + " -test.run=TestRestoreVerifierCommandHelper --"
}

func TestRestoreVerifierCommandHelper(t *testing.T) {
	if os.Getenv("STACKKITS_RESTORE_VERIFIER_HELPER") != "1" {
		return
	}
	if os.Getenv("STACKKIT_RUNTIME_ACTION") != "restore_drill" || os.Getenv("STACKKIT_STACK_ID") == "" {
		fmt.Fprintln(os.Stderr, "missing restore verifier environment")
		os.Exit(2)
	}
	if os.Getenv("STACKKITS_RESTORE_VERIFIER_MODE") == "fail" {
		fmt.Fprintln(os.Stderr, "restore failed")
		os.Exit(3)
	}
	fmt.Fprintf(os.Stdout, "restore verified for %s\n", os.Getenv("STACKKIT_STACK_ID"))
	os.Exit(0)
}

func hasRuntimeActionCheck(checks []runtimeActionCheck, name, status string) bool {
	for _, check := range checks {
		if check.Name == name && check.Status == status {
			return true
		}
	}
	return false
}
