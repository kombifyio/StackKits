package registry

import (
	"context"
	"errors"
	"os"
	"testing"
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
