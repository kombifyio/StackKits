package registry

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestEmbeddedSnapshot_SchemaVersion(t *testing.T) {
	snap, err := EmbeddedSnapshot()
	if err != nil {
		t.Fatalf("EmbeddedSnapshot: %v", err)
	}
	if snap.SchemaVersion != SnapshotVersion {
		t.Errorf("schema_version = %d, want %d", snap.SchemaVersion, SnapshotVersion)
	}
	if snap.Source == "" {
		t.Error("source must not be empty")
	}
	if snap.GeneratedAt.IsZero() {
		t.Error("generated_at must not be zero")
	}
}

func TestEmbeddedSnapshot_IncludesCanonicalServices(t *testing.T) {
	snap, err := EmbeddedSnapshot()
	if err != nil {
		t.Fatalf("EmbeddedSnapshot: %v", err)
	}
	services := map[string]Service{}
	for _, svc := range snap.Services {
		services[svc.Key] = svc
	}
	base, ok := services["base"]
	if !ok {
		t.Fatalf("embedded snapshot missing base service; got %v", services)
	}
	if base.ToolName != "dashboard" || base.PublicSlug != "base" {
		t.Fatalf("base service = %#v, want dashboard tool with base public slug", base)
	}
	home, ok := services["home"]
	if !ok {
		t.Fatalf("embedded snapshot missing home service; got %v", services)
	}
	if home.ToolName != "homepage" || home.PublicSlug != "home" {
		t.Fatalf("home service = %#v, want homepage tool with home public slug", home)
	}
	auth, ok := services["auth"]
	if !ok {
		t.Fatalf("embedded snapshot missing auth service; got %v", services)
	}
	if auth.PublicSlug != "auth" {
		t.Fatalf("auth public slug = %q, want auth", auth.PublicSlug)
	}
}

func TestEmbeddedClient_Module_NotFound(t *testing.T) {
	c := NewEmbeddedClient()
	_, err := c.Module(context.Background(), "definitely-not-a-real-module")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestEmbeddedClient_Source(t *testing.T) {
	if got := NewEmbeddedClient().Source(); got != "embedded" {
		t.Errorf("Source() = %q, want %q", got, "embedded")
	}
}

func TestAutoClient_SelectsBackend(t *testing.T) {
	// With the env var set, we should get a RemoteClient.
	t.Setenv(EnvEndpoint, "https://admin.example.test")
	t.Setenv(EnvToken, "dummy")

	c := AutoClient()
	if got := c.Source(); got != "admin-api" {
		t.Errorf("with endpoint set: Source() = %q, want %q", got, "admin-api")
	}

	// With the env var unset, we should get an EmbeddedClient.
	if err := os.Unsetenv(EnvEndpoint); err != nil {
		t.Fatalf("unset env: %v", err)
	}
	c = AutoClient()
	if got := c.Source(); got != "embedded" {
		t.Errorf("without endpoint: Source() = %q, want %q", got, "embedded")
	}
}

func TestRemoteClient_TrimsTrailingSlash(t *testing.T) {
	c := NewRemoteClient("https://admin.example.test///", "tok")
	if c.baseURL != "https://admin.example.test" {
		t.Errorf("baseURL = %q, want trimmed", c.baseURL)
	}
}

func TestEmbeddedClientLookups(t *testing.T) {
	c := NewEmbeddedClient()
	ctx := context.Background()

	snap, err := c.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(snap.Tools) == 0 {
		if _, err := c.Tool(ctx, "missing-tool"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("empty tool snapshot should return ErrNotFound, got %v", err)
		}
	}
	if len(snap.Modules) == 0 {
		t.Fatal("embedded snapshot should include modules")
	}
	if len(snap.Services) == 0 {
		t.Fatal("embedded snapshot should include services")
	}

	if len(snap.Tools) > 0 {
		if got, err := c.Tool(ctx, snap.Tools[0].Slug); err != nil || got.Slug != snap.Tools[0].Slug {
			t.Fatalf("Tool lookup = %#v, %v", got, err)
		}
	}
	if got, err := c.Module(ctx, snap.Modules[0].Slug); err != nil || got.Slug != snap.Modules[0].Slug {
		t.Fatalf("Module lookup = %#v, %v", got, err)
	}
	if len(snap.StackKits) > 0 {
		if got, err := c.StackKit(ctx, snap.StackKits[0].Slug); err != nil || got.Slug != snap.StackKits[0].Slug {
			t.Fatalf("StackKit lookup = %#v, %v", got, err)
		}
	} else if _, err := c.StackKit(ctx, "missing-kit"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("empty stackkit snapshot should return ErrNotFound, got %v", err)
	}
}

func TestRemoteClientSnapshotAddsDefaultsAndAuthHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/sk/registry/snapshot" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer tok" {
			t.Fatalf("Authorization = %q", got)
		}
		_ = json.NewEncoder(w).Encode(Snapshot{SchemaVersion: SnapshotVersion})
	}))
	defer srv.Close()

	snap, err := NewRemoteClient(srv.URL, "tok").Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snap.Source != SourceAdminAPI {
		t.Fatalf("Source = %q, want %q", snap.Source, SourceAdminAPI)
	}
	if snap.AdminEndpoint != srv.URL {
		t.Fatalf("AdminEndpoint = %q, want %q", snap.AdminEndpoint, srv.URL)
	}
	if snap.GeneratedAt.IsZero() {
		t.Fatal("GeneratedAt should be filled when server omits it")
	}
}

func TestRemoteClientMaps404ToErrNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	_, err := NewRemoteClient(srv.URL, "").Tool(context.Background(), "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Tool error = %v, want ErrNotFound", err)
	}
}

func TestRemoteClientReturnsBodyOnServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "database down", http.StatusBadGateway)
	}))
	defer srv.Close()

	_, err := NewRemoteClient(srv.URL, "").Module(context.Background(), "traefik")
	if err == nil {
		t.Fatal("Module should return error")
	}
	if !strings.Contains(err.Error(), "status=502") || !strings.Contains(err.Error(), "database down") {
		t.Fatalf("error should include status and body, got %v", err)
	}
}

func TestRemoteClientFetchesToolModuleAndStackKit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/sk/registry/tools/traefik":
			_ = json.NewEncoder(w).Encode(Tool{Slug: "traefik", DisplayName: "Traefik"})
		case "/api/v1/sk/registry/modules/traefik":
			if r.URL.Query().Get("latest") != "true" {
				t.Fatalf("latest query = %q", r.URL.RawQuery)
			}
			_ = json.NewEncoder(w).Encode(Module{Slug: "traefik", Version: "1.0.0"})
		case "/api/v1/sk/registry/stackkits/base-kit":
			_ = json.NewEncoder(w).Encode(StackKit{Slug: "base-kit", DisplayName: "Base Kit"})
		default:
			t.Fatalf("unexpected path: %s", r.URL.String())
		}
	}))
	defer srv.Close()

	c := NewRemoteClient(srv.URL, "")
	tool, err := c.Tool(context.Background(), "traefik")
	if err != nil {
		t.Fatalf("Tool: %v", err)
	}
	if tool.Slug != "traefik" {
		t.Fatalf("tool = %#v", tool)
	}
	module, err := c.Module(context.Background(), "traefik")
	if err != nil {
		t.Fatalf("Module: %v", err)
	}
	if module.Version != "1.0.0" {
		t.Fatalf("module = %#v", module)
	}
	stackkit, err := c.StackKit(context.Background(), "base-kit")
	if err != nil {
		t.Fatalf("StackKit: %v", err)
	}
	if stackkit.DisplayName != "Base Kit" {
		t.Fatalf("stackkit = %#v", stackkit)
	}
}

func TestRemoteClientSnapshotPreservesServerGeneratedAtAndSource(t *testing.T) {
	generatedAt := time.Date(2026, 5, 7, 10, 0, 0, 0, time.UTC)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(Snapshot{
			SchemaVersion: SnapshotVersion,
			Source:        SourceCUE,
			GeneratedAt:   generatedAt,
		})
	}))
	defer srv.Close()

	snap, err := NewRemoteClient(srv.URL, "").Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snap.Source != SourceCUE {
		t.Fatalf("Source = %q, want server source", snap.Source)
	}
	if !snap.GeneratedAt.Equal(generatedAt) {
		t.Fatalf("GeneratedAt = %s, want %s", snap.GeneratedAt, generatedAt)
	}
}
