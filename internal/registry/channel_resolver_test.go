package registry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolve_HappyPath(t *testing.T) {
	var gotPath, gotQuery, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(ResolveResult{
			KitVersionID: "kv-1",
			KitChannel:   "stable",
			Modules: []ResolvedModule{
				{ModuleSlug: "traefik", ModuleVersionID: "mv-traefik", ModuleSemver: "3.2.0", Channel: "stable", Reason: "matched"},
				{ModuleSlug: "vaultwarden", ModuleVersionID: "mv-vault", ModuleSemver: "1.30.0", Channel: "beta", Reason: "fallback"},
			},
		})
	}))
	defer srv.Close()

	res, err := NewChannelResolver(srv.URL, "abc-token").Resolve(context.Background(), ResolveRequest{
		KitSlug:      "base-kit",
		KitVersionID: "kv-1",
		KitChannel:   "stable",
	})
	require.NoError(t, err)
	require.NotNil(t, res)

	assert.Equal(t, "/api/v1/sk/compat/resolve", gotPath)
	assert.Contains(t, gotQuery, "kit_slug=base-kit")
	assert.Contains(t, gotQuery, "kit_channel=stable")
	assert.Equal(t, "Bearer abc-token", gotAuth)

	require.Len(t, res.Modules, 2)
	assert.Equal(t, "matched", res.Modules[0].Reason)
	assert.Equal(t, "fallback", res.Modules[1].Reason)

	summary := res.SummarizeReasons()
	assert.Equal(t, 1, summary["matched"])
	assert.Equal(t, 1, summary["fallback"])
	assert.Equal(t, 0, summary["override"])
}

func TestResolve_PassesModuleChannelOverride(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		_ = json.NewEncoder(w).Encode(ResolveResult{KitChannel: "stable"})
	}))
	defer srv.Close()

	_, err := NewChannelResolver(srv.URL, "").Resolve(context.Background(), ResolveRequest{
		KitSlug:       "base-kit",
		KitVersionID:  "kv-1",
		KitChannel:    "stable",
		ModuleChannel: "edge",
	})
	require.NoError(t, err)
	assert.Contains(t, gotQuery, "module_channel=edge")
}

func TestResolve_DefaultsKitChannelToStable(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		_ = json.NewEncoder(w).Encode(ResolveResult{KitChannel: "stable"})
	}))
	defer srv.Close()

	_, err := NewChannelResolver(srv.URL, "").Resolve(context.Background(), ResolveRequest{
		KitSlug:      "base-kit",
		KitVersionID: "kv-1",
		// KitChannel left empty
	})
	require.NoError(t, err)
	assert.Contains(t, gotQuery, "kit_channel=stable")
}

func TestResolve_RejectsInvalidChannel(t *testing.T) {
	r := NewChannelResolver("http://unused", "")
	_, err := r.Resolve(context.Background(), ResolveRequest{
		KitSlug:      "base-kit",
		KitVersionID: "kv-1",
		KitChannel:   "alpha",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid kit_channel")

	_, err = r.Resolve(context.Background(), ResolveRequest{
		KitSlug:       "base-kit",
		KitVersionID:  "kv-1",
		KitChannel:    "stable",
		ModuleChannel: "saas",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid module_channel")
}

func TestResolve_RejectsMissingRequired(t *testing.T) {
	r := NewChannelResolver("http://unused", "")
	_, err := r.Resolve(context.Background(), ResolveRequest{
		KitVersionID: "kv-1",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "kit_slug")

	_, err = r.Resolve(context.Background(), ResolveRequest{
		KitSlug: "base-kit",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "kit_version")
}

func TestResolve_404IsErrNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	_, err := NewChannelResolver(srv.URL, "").Resolve(context.Background(), ResolveRequest{
		KitSlug:      "ghost-kit",
		KitVersionID: "kv-x",
		KitChannel:   "stable",
	})
	require.Error(t, err)
}
