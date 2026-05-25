package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

func TestRuntimeActionExecutionTimeout_StaysInsideBudget(t *testing.T) {
	require.Equal(t, 14*time.Minute+30*time.Second, runtimeActionExecutionTimeout)
	require.LessOrEqual(t, runtimeActionExecutionTimeout, 15*time.Minute)
}

func TestCompactRuntimeActionStderrKeepsTail(t *testing.T) {
	var input bytes.Buffer
	for i := 0; i < 7000; i++ {
		input.WriteByte('x')
	}
	input.WriteString("\nreal diagnostic")

	got := compactRuntimeActionStderr(input.String())

	require.Contains(t, got, "[stderr truncated; showing last 6000 runes]")
	require.Contains(t, got, "real diagnostic")
	require.NotContains(t, got, strings.Repeat("x", 6500))
}

func TestRuntimePlatformAppDeployments_DeploysCoolifyManifest(t *testing.T) {
	deployDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(deployDir, ".stackkit"), 0700))

	var created, started int
	coolify := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/services":
			created++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"uuid":"service-123"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/services/service-123/start":
			started++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"deployments":[{"deployment_uuid":"deploy-123"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(coolify.Close)

	platformConfig := fmt.Sprintf(`{"platform":"coolify","endpoint":%q,"token":"test-token"}`, coolify.URL)
	require.NoError(t, os.WriteFile(filepath.Join(deployDir, ".stackkit", "platform.json"), []byte(platformConfig), 0600))
	manifest := `{
		"version":"stackkit.platform-apps/v2",
		"platform":"coolify",
		"systemApps":[{
			"name":"stackkit-hub",
			"kind":"compose",
			"managedBy":"coolify",
			"composeYAML":"services:\n  hub:\n    image: nginx:alpine\n"
		}],
		"apps":[]
	}`
	require.NoError(t, os.WriteFile(filepath.Join(deployDir, ".platform-apps-manifest.json"), []byte(manifest), 0600))

	refs, checks, err := runRuntimePlatformAppDeployments(context.Background(), deployDir)

	require.NoError(t, err)
	require.Len(t, refs, 1)
	assert.Equal(t, "service-123", refs[0].ExternalID)
	assert.Equal(t, "deploy-123", refs[0].DeploymentID)
	assert.Equal(t, 1, created)
	assert.Equal(t, 1, started)
	assert.Contains(t, fmt.Sprint(checks), "platform_apps")
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

func TestStackKitOutputsFromOpenTofu_ProjectsBaseKitServiceLinks(t *testing.T) {
	resp := runtimeActionResponse{StackID: "stack-123", StackName: "Local Base"}
	outputs := stackKitOutputsFromOpenTofu(resp, map[string]string{
		"dashboard_url":       "https://base.local",
		"homepage_url":        "https://home.local",
		"tinyauth_login_url":  "https://auth.local",
		"pocketid_url":        "https://id.local",
		"coolify_url":         "https://coolify.local",
		"kuma_url":            "https://kuma.local",
		"whoami_url":          "https://whoami.local",
		"vaultwarden_url":     "https://vault.local",
		"immich_url":          "https://photos.local",
		"coolify_admin_email": "admin@example.com",
	})

	require.NotNil(t, outputs)
	assert.Equal(t, "admin@example.com", outputs.Identity.Owner.Email)
	links := map[string]string{}
	for _, link := range outputs.ServiceLinks {
		links[link.Name] = link.URL
	}
	assert.Equal(t, "https://base.local", links["base"])
	assert.Equal(t, "https://home.local", links["homepage"])
	assert.Equal(t, "https://auth.local", links["auth"])
	assert.Equal(t, "https://id.local", links["pocketid"])
	assert.Equal(t, "https://coolify.local", links["coolify"])
	assert.Equal(t, "https://kuma.local", links["monitoring"])
	assert.Equal(t, "https://whoami.local", links["whoami"])
	assert.Equal(t, "https://vault.local", links["vaultwarden"])
	assert.Equal(t, "https://photos.local", links["immich"])
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

func TestRuntimeHostMetricsFromOutput(t *testing.T) {
	updatedAt := time.Date(2026, 5, 25, 8, 15, 0, 0, time.UTC)

	metrics, err := runtimeHostMetricsFromOutput(`cpu_percent=12.5
memory_percent=34.5
disk_percent=56.5
uptime_seconds=789
`, updatedAt)

	require.NoError(t, err)
	require.NotNil(t, metrics)
	assert.Equal(t, 12.5, metrics.CPUPercent)
	assert.Equal(t, 34.5, metrics.MemoryPercent)
	assert.Equal(t, 56.5, metrics.DiskPercent)
	assert.Equal(t, float64(789), metrics.UptimeSeconds)
	assert.Equal(t, "2026-05-25T08:15:00Z", metrics.UpdatedAt)
}

func TestRuntimeHostMetricsJSONKeepsZeroCPU(t *testing.T) {
	metrics := runtimeActionRuntimeMetrics{
		CPUPercent:    0,
		MemoryPercent: 34.5,
		DiskPercent:   56.5,
		UptimeSeconds: 789,
		UpdatedAt:     "2026-05-25T08:15:00Z",
	}

	body, err := json.Marshal(metrics)
	require.NoError(t, err)

	assert.Contains(t, string(body), `"cpu_percent":0`)
	assert.Contains(t, string(body), `"memory_percent":34.5`)
	assert.Contains(t, string(body), `"disk_percent":56.5`)
	assert.Contains(t, string(body), `"uptime_seconds":789`)
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

func TestRuntimeActionEndpoints_BuiltInRestoreVerifierApplyMode(t *testing.T) {
	srv, _ := testServer(t)
	srv.config.ServiceAuthSecret = "shared-secret"
	srv.config.RuntimeActionMode = "apply"

	tofuDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(tofuDir, "main.tf"), []byte("terraform {}\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(tofuDir, "terraform.tfstate"), []byte(`{"version":4}`+"\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(tofuDir, "terraform.tfvars.json"), []byte(`{"enable_coolify":true}`+"\n"), 0600))
	require.NoError(t, os.MkdirAll(filepath.Join(tofuDir, ".stackkit"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(tofuDir, ".stackkit", "platform.json"), []byte(`{"platform":"coolify"}`+"\n"), 0600))
	fakeBin := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(fakeBin, "docker"), []byte("#!/bin/sh\nif [ \"$1\" = \"ps\" ]; then printf 'coolify\\tUp 1 minute\\ncoolify-db\\tUp 1 minute\\n'; exit 0; fi\nexit 0\n"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(fakeBin, "docker.cmd"), []byte("@echo off\r\nif \"%1\"==\"ps\" (\r\n  echo coolify\tUp 1 minute\r\n  echo coolify-db\tUp 1 minute\r\n  exit /b 0\r\n)\r\nexit /b 0\r\n"), 0700))
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))

	rec := postRuntimeAction(t, srv, "/api/v1/internal/runtime-actions/restore-drill", "shared-secret", map[string]any{
		"action":   "restore_drill",
		"stack_id": "stack-123",
		"stackkit": "base-kit",
		"tofu_dir": tofuDir,
	})

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	resp := parseResponse(t, rec)
	var data runtimeActionResponse
	require.NoError(t, json.Unmarshal(resp["data"], &data))
	assert.Equal(t, "verified", data.Status)
	assert.True(t, hasRuntimeActionCheck(data.Checks, "restore_drill_adapter", "ok"), "checks=%+v", data.Checks)
	assert.True(t, hasRuntimeActionCheck(data.Checks, "docker_runtime", "ok"), "checks=%+v", data.Checks)
	assert.True(t, hasRuntimeActionCheck(data.Checks, "coolify_runtime", "ok"), "checks=%+v", data.Checks)
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
