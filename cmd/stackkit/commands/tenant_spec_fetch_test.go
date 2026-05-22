package commands

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kombifyio/stackkits/internal/rollout"
)

func TestFetchTenantSpecWritesTraceableManagedSpec(t *testing.T) {
	resetTenantDeploymentTestGlobals(t)

	tmp := t.TempDir()
	t.Setenv("STACKKIT_BOOTSTRAP_TOKEN", "boot-token")

	var sawRequest bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawRequest = true
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/api/v1/sk/tenants/deployments/dep-123/spec" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer boot-token" {
			t.Fatalf("authorization = %q", got)
		}
		if got := r.Header.Get("User-Agent"); got != "stackkit-cli tenant-spec-fetch" {
			t.Fatalf("user-agent = %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"deployment": map[string]any{
				"id":              "dep-123",
				"tenantId":        "tenant-1",
				"tenantSlug":      "acme",
				"stackkitSlug":    "base-kit",
				"stackkitVersion": "1.2.3",
				"lifecycleState":  "provisioning",
			},
			"spec": map[string]any{
				"mode":        "simple",
				"domain":      "kombify.me",
				"environment": map[string]any{"EXISTING": "kept"},
			},
			"bindings": []map[string]any{
				{
					"moduleSlug":        "vaultwarden",
					"moduleVersion":     "1.0.0",
					"secretKey":         "VAULTWARDEN_DB",
					"dopplerSecretPath": "doppler://tenant-1/vaultwarden/db",
					"status":            "ready",
				},
			},
		})
	}))
	defer srv.Close()
	t.Setenv("STACKKIT_ADMIN_ENDPOINT", srv.URL)

	spec, err := fetchTenantSpec(context.Background(), "dep-123", tmp)
	if err != nil {
		t.Fatalf("fetchTenantSpec returned error: %v", err)
	}
	if !sawRequest {
		t.Fatal("server did not receive request")
	}
	if spec.Name != "acme-base-kit" || spec.StackKit != "base-kit" {
		t.Fatalf("unexpected spec identity: %+v", spec)
	}
	if spec.Environment["EXISTING"] != "kept" {
		t.Fatalf("existing environment was not preserved: %#v", spec.Environment)
	}
	if spec.Environment["STACKKIT_TENANT_DEPLOYMENT_ID"] != "dep-123" {
		t.Fatalf("missing deployment id env: %#v", spec.Environment)
	}
	if spec.Environment["STACKKIT_TENANT_ID"] != "tenant-1" {
		t.Fatalf("missing tenant id env: %#v", spec.Environment)
	}
	if spec.Environment["STACKKIT_BINDING_VAULTWARDEN_DB"] != "doppler://tenant-1/vaultwarden/db" {
		t.Fatalf("missing binding env: %#v", spec.Environment)
	}

	specData, err := os.ReadFile(filepath.Join(tmp, specFile))
	if err != nil {
		t.Fatalf("spec file was not written: %v", err)
	}
	if !strings.Contains(string(specData), "STACKKIT_TENANT_DEPLOYMENT_ID") {
		t.Fatalf("written spec does not include managed trace env:\n%s", string(specData))
	}

	bindingsData, err := os.ReadFile(filepath.Join(tmp, ".stackkit", "tenant-bindings.json"))
	if err != nil {
		t.Fatalf("tenant bindings were not written: %v", err)
	}
	if !strings.Contains(string(bindingsData), "doppler://tenant-1/vaultwarden/db") {
		t.Fatalf("tenant bindings did not persist doppler path:\n%s", string(bindingsData))
	}
}

func TestFetchTenantSpecPersistsManagedIdentityBootstrapEnvelope(t *testing.T) {
	resetTenantDeploymentTestGlobals(t)

	tmp := t.TempDir()
	t.Setenv("STACKKIT_BOOTSTRAP_TOKEN", "boot-token")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"deployment": map[string]any{
				"id":           "dep-123",
				"tenantId":     "tenant-1",
				"tenantSlug":   "acme",
				"stackkitSlug": "base-kit",
			},
			"spec": map[string]any{
				"stackkit": "base-kit",
				"owner": map[string]any{
					"bootstrapMode":       "auto",
					"source":              "cloud",
					"recoveryMaterialRef": "techstack://identity-bootstrap/deployments/dep-123/recovery",
				},
			},
			"identityBootstrap": map[string]any{
				"owner": map[string]any{
					"bootstrapMode": "custom",
					"source":        "local",
					"email":         "owner@example.com",
					"username":      "owner",
					"displayName":   "Owner Example",
				},
				"adminEmail":              "owner@example.com",
				"adminUsername":           "owner",
				"adminCredentialRef":      "techstack://identity-bootstrap/deployments/dep-123/owner-admin-bootstrap",
				"recoveryMaterialRef":     "techstack://identity-bootstrap/deployments/dep-123/recovery",
				"recoveryPassphrasePlain": "this is one-time material",
				"breakGlass": map[string]any{
					"enabled":        true,
					"scope":          "full-emergency-admin",
					"pocketidAdmin":  true,
					"tinyauthStatic": true,
					"serverRecovery": true,
				},
			},
		})
	}))
	defer srv.Close()
	t.Setenv("STACKKIT_ADMIN_ENDPOINT", srv.URL)

	_, err := fetchTenantSpec(context.Background(), "dep-123", tmp)
	if err != nil {
		t.Fatalf("fetchTenantSpec returned error: %v", err)
	}

	data, err := os.ReadFile(identityBootstrapEnvelopePath(tmp))
	if err != nil {
		t.Fatalf("identity bootstrap envelope was not written: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, `"email": "owner@example.com"`) {
		t.Fatalf("identity bootstrap envelope missing owner email:\n%s", content)
	}
	if !strings.Contains(content, `"recoveryPassphrasePlain": "this is one-time material"`) {
		t.Fatalf("identity bootstrap envelope missing one-time recovery material:\n%s", content)
	}
	if strings.Contains(content, "@kombify.io") {
		t.Fatalf("identity bootstrap envelope must not invent kombify.io users:\n%s", content)
	}
}

func TestFetchTenantSpecFailsManagedAutoWithoutIdentityBootstrap(t *testing.T) {
	resetTenantDeploymentTestGlobals(t)

	tmp := t.TempDir()
	t.Setenv("STACKKIT_BOOTSTRAP_TOKEN", "boot-token")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"deployment": map[string]any{
				"id":           "dep-123",
				"tenantId":     "tenant-1",
				"stackkitSlug": "base-kit",
			},
			"spec": map[string]any{
				"stackkit": "base-kit",
				"owner": map[string]any{
					"bootstrapMode":       "auto",
					"source":              "cloud",
					"recoveryMaterialRef": "techstack://identity-bootstrap/deployments/dep-123/recovery",
				},
			},
		})
	}))
	defer srv.Close()
	t.Setenv("STACKKIT_ADMIN_ENDPOINT", srv.URL)

	_, err := fetchTenantSpec(context.Background(), "dep-123", tmp)
	if err == nil || !strings.Contains(err.Error(), "identityBootstrap") {
		t.Fatalf("expected missing identityBootstrap error, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(tmp, specFile)); !os.IsNotExist(statErr) {
		t.Fatalf("spec file should not be written after missing identity bootstrap, statErr=%v", statErr)
	}
}

func TestFetchTenantSpecRejectsMismatchedDeploymentEnvelope(t *testing.T) {
	resetTenantDeploymentTestGlobals(t)

	tmp := t.TempDir()
	t.Setenv("STACKKIT_BOOTSTRAP_TOKEN", "boot-token")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"deployment": map[string]any{
				"id":           "dep-other",
				"tenantId":     "tenant-1",
				"stackkitSlug": "base-kit",
			},
			"spec": map[string]any{"stackkit": "base-kit"},
		})
	}))
	defer srv.Close()
	t.Setenv("STACKKIT_ADMIN_ENDPOINT", srv.URL)

	_, err := fetchTenantSpec(context.Background(), "dep-123", tmp)
	if err == nil || !strings.Contains(err.Error(), "expected dep-123") {
		t.Fatalf("expected deployment id mismatch error, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(tmp, specFile)); !os.IsNotExist(statErr) {
		t.Fatalf("spec file should not be written after envelope mismatch, statErr=%v", statErr)
	}
}

func TestFetchTenantSpecPersistsPlatformConfigAndRedactsSpecEnv(t *testing.T) {
	resetTenantDeploymentTestGlobals(t)

	tmp := t.TempDir()
	t.Setenv("STACKKIT_BOOTSTRAP_TOKEN", "boot-token")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"deployment": map[string]any{
				"id":           "dep-123",
				"tenantId":     "tenant-1",
				"tenantSlug":   "acme",
				"stackkitSlug": "base-kit",
			},
			"spec": map[string]any{
				"stackkit": "base-kit",
				"paas":     "coolify",
				"environment": map[string]any{
					"PUBLIC_VALUE":                         "keep-me",
					"STACKKIT_PLATFORM_ENDPOINT":           "https://coolify.example.test",
					"STACKKIT_PLATFORM_TOKEN":              "platform-token",
					"STACKKIT_PLATFORM_PROJECT_UUID":       "project-1",
					"STACKKIT_PLATFORM_ENVIRONMENT_UUID":   "env-uuid-1",
					"STACKKIT_PLATFORM_DESTINATION_UUID":   "dest-1",
					"STACKKIT_PLATFORM_ENVIRONMENT_NAME":   "production",
					"STACKKIT_PLATFORM_SERVER_UUID":        "server-uuid-1",
					"STACKKIT_PLATFORM_SERVER_ID":          "server-generic-ignored-for-coolify",
					"STACKKIT_PLATFORM_ENVIRONMENT_ID":     "env-generic-ignored-for-coolify",
					"STACKKIT_PLATFORM_SHOULD_NOT_BE_USED": "ignored",
				},
			},
		})
	}))
	defer srv.Close()
	t.Setenv("STACKKIT_ADMIN_ENDPOINT", srv.URL)

	spec, err := fetchTenantSpec(context.Background(), "dep-123", tmp)
	if err != nil {
		t.Fatalf("fetchTenantSpec returned error: %v", err)
	}
	if spec.Environment["PUBLIC_VALUE"] != "keep-me" {
		t.Fatalf("non-platform env was not preserved: %#v", spec.Environment)
	}
	if _, ok := spec.Environment["STACKKIT_PLATFORM_TOKEN"]; ok {
		t.Fatalf("platform token should be redacted from spec env: %#v", spec.Environment)
	}

	data, err := os.ReadFile(filepath.Join(tmp, ".stackkit", "platform.json"))
	if err != nil {
		t.Fatalf("platform config was not written: %v", err)
	}
	var cfg platformConfigFile
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("platform config is invalid json: %v\n%s", err, string(data))
	}
	if cfg.Platform != "coolify" || cfg.Endpoint != "https://coolify.example.test" || cfg.Token != "platform-token" {
		t.Fatalf("unexpected platform config: %+v", cfg)
	}
	if cfg.ProjectUUID != "project-1" || cfg.EnvironmentUUID != "env-uuid-1" || cfg.DestinationUUID != "dest-1" {
		t.Fatalf("missing coolify placement config: %+v", cfg)
	}

	specData, err := os.ReadFile(filepath.Join(tmp, specFile))
	if err != nil {
		t.Fatalf("spec file was not written: %v", err)
	}
	if strings.Contains(string(specData), "platform-token") {
		t.Fatalf("written spec leaked platform token:\n%s", string(specData))
	}
	if !strings.Contains(string(specData), "PUBLIC_VALUE") {
		t.Fatalf("written spec lost non-platform env:\n%s", string(specData))
	}
}

func TestReportTenantDeploymentStateUsesBootstrapToken(t *testing.T) {
	resetTenantDeploymentTestGlobals(t)

	t.Setenv("STACKKIT_BOOTSTRAP_TOKEN", "boot-token")
	t.Setenv("STACKKIT_ADMIN_TOKEN", "admin-token")

	var sawRequest bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawRequest = true
		if r.Method != http.MethodPatch {
			t.Fatalf("method = %s, want PATCH", r.Method)
		}
		if r.URL.Path != "/api/v1/sk/tenants/deployments/dep-123" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer boot-token" {
			t.Fatalf("authorization = %q", got)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		if payload["lifecycleState"] != "healthy" || payload["actor"] != "stackkit-cli" {
			t.Fatalf("unexpected payload: %#v", payload)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	applyReportingEndpoint = srv.URL

	if err := reportTenantDeploymentState("dep-123", "healthy", "", "apply succeeded"); err != nil {
		t.Fatalf("reportTenantDeploymentState returned error: %v", err)
	}
	if !sawRequest {
		t.Fatal("server did not receive report request")
	}
}

func TestReportTenantDeploymentStateRequiresToken(t *testing.T) {
	resetTenantDeploymentTestGlobals(t)

	applyReportingEndpoint = "http://127.0.0.1"

	err := reportTenantDeploymentState("dep-123", "failed", "boom", "stackkit apply failed")
	if err == nil || !strings.Contains(err.Error(), "STACKKIT_BOOTSTRAP_TOKEN") {
		t.Fatalf("expected missing token error, got %v", err)
	}
}

func TestTenantDeploymentEventRequiresToken(t *testing.T) {
	resetTenantDeploymentTestGlobals(t)

	applyReportingEndpoint = "http://127.0.0.1"

	err := reportTenantDeploymentEvent("dep-123", tenantDeploymentEvent{
		RunID:  "20260515-120000",
		Phase:  "tofu.apply",
		Status: "failed",
	})
	if err == nil || !strings.Contains(err.Error(), "STACKKIT_BOOTSTRAP_TOKEN") {
		t.Fatalf("expected missing token error, got %v", err)
	}
}

func TestTenantDeploymentEventPostsProgressPayload(t *testing.T) {
	resetTenantDeploymentTestGlobals(t)

	t.Setenv("STACKKIT_BOOTSTRAP_TOKEN", "boot-token")
	rolloutRecorder = nil
	deployLog = nil

	var sawRequest bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawRequest = true
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/v1/sk/tenants/deployments/dep-123/events" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer boot-token" {
			t.Fatalf("authorization = %q", got)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		if payload["runId"] != "20260515-120000" ||
			payload["phase"] != "tofu.apply" ||
			payload["status"] != "failed" ||
			payload["failureClass"] != "tofu_apply_failed" ||
			payload["actor"] != "stackkit-cli" {
			t.Fatalf("unexpected payload: %#v", payload)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	applyReportingEndpoint = srv.URL

	err := reportTenantDeploymentEvent("dep-123", tenantDeploymentEvent{
		RunID:        "20260515-120000",
		Phase:        "tofu.apply",
		Status:       "failed",
		FailureClass: "tofu_apply_failed",
	})
	if err != nil {
		t.Fatalf("reportTenantDeploymentEvent returned error: %v", err)
	}
	if !sawRequest {
		t.Fatal("server did not receive report request")
	}
}

func TestTenantDeploymentEventIgnoresUnsupportedEndpoint(t *testing.T) {
	resetTenantDeploymentTestGlobals(t)

	t.Setenv("STACKKIT_BOOTSTRAP_TOKEN", "boot-token")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()
	applyReportingEndpoint = srv.URL

	err := reportTenantDeploymentEvent("dep-123", tenantDeploymentEvent{
		RunID:  "20260515-120000",
		Phase:  "tofu.apply",
		Status: "running",
	})
	if err != nil {
		t.Fatalf("404 should be treated as unsupported and ignored, got %v", err)
	}
}

func TestTenantDeploymentEventFailureDoesNotRequireDeployLog(t *testing.T) {
	resetTenantDeploymentTestGlobals(t)

	t.Setenv("STACKKIT_BOOTSTRAP_TOKEN", "boot-token")
	rec, err := rollout.NewRecorder(filepath.Join(t.TempDir(), ".stackkit"), rollout.Metadata{RunID: "20260515-120000"})
	if err != nil {
		t.Fatalf("new recorder: %v", err)
	}
	t.Cleanup(func() {
		_ = rec.Close(rollout.Summary{Status: "test"})
	})
	rolloutRecorder = rec
	deployLog = nil

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	applyReportingEndpoint = srv.URL

	recordTenantDeploymentEvent("dep-123", "tofu.apply", "failed", "apply failed", "tofu_apply_failed")
}

func TestRolloutFailureWritesFailureClass(t *testing.T) {
	resetTenantDeploymentTestGlobals(t)

	rec, err := rollout.NewRecorder(filepath.Join(t.TempDir(), ".stackkit"), rollout.Metadata{RunID: "20260515-120000"})
	if err != nil {
		t.Fatalf("new recorder: %v", err)
	}
	rolloutRecorder = rec

	rolloutFailure("tofu.apply", errors.New("OpenTofu apply failed token=super-secret"))
	if err := rec.Close(rollout.Summary{Status: "test"}); err != nil {
		t.Fatalf("close recorder: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(rec.Root(), "events.jsonl"))
	if err != nil {
		t.Fatalf("read events: %v", err)
	}
	if !strings.Contains(string(data), `"failureClass":"tofu_apply_failed"`) {
		t.Fatalf("events missing failure class: %s", data)
	}
	if strings.Contains(string(data), "super-secret") {
		t.Fatalf("events were not redacted: %s", data)
	}
}

func resetTenantDeploymentTestGlobals(t *testing.T) {
	t.Helper()
	prevEndpoint := applyReportingEndpoint
	prevToken := applyReportingToken
	prevTenantDeployment := applyTenantDeployment
	prevSpecFile := specFile
	t.Cleanup(func() {
		applyReportingEndpoint = prevEndpoint
		applyReportingToken = prevToken
		applyTenantDeployment = prevTenantDeployment
		specFile = prevSpecFile
		rolloutRecorder = nil
		rolloutRecorderClosed = false
	})

	applyReportingEndpoint = ""
	applyReportingToken = ""
	applyTenantDeployment = ""
	specFile = "stack-spec.yaml"

	t.Setenv("STACKKIT_ADMIN_ENDPOINT", "")
	t.Setenv("STACKKIT_ADMIN_URL", "")
	t.Setenv("STACKKIT_BOOTSTRAP_TOKEN", "")
	t.Setenv("STACKKIT_ADMIN_TOKEN", "")
}
