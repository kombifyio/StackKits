package commands

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kombifyio/stackkits/internal/registry"
	"github.com/kombifyio/stackkits/internal/snapshot"
)

func TestParseUpgradeTarget_DefaultsToChannelStable(t *testing.T) {
	semver, channel, err := parseUpgradeTarget("", "")
	require.NoError(t, err)
	assert.Equal(t, "", semver)
	assert.Equal(t, "stable", channel)
}

func TestParseUpgradeTarget_ChannelPrefix(t *testing.T) {
	for _, c := range []string{"edge", "beta", "stable"} {
		semver, channel, err := parseUpgradeTarget("channel:"+c, "")
		require.NoError(t, err)
		assert.Equal(t, "", semver)
		assert.Equal(t, c, channel)
	}
}

func TestParseUpgradeTarget_ExplicitSemver(t *testing.T) {
	semver, channel, err := parseUpgradeTarget("1.2.0", "")
	require.NoError(t, err)
	assert.Equal(t, "1.2.0", semver)
	assert.Equal(t, "stable", channel, "no explicit --kit-channel should default to stable")

	semver, channel, err = parseUpgradeTarget("1.2.0", "beta")
	require.NoError(t, err)
	assert.Equal(t, "1.2.0", semver)
	assert.Equal(t, "beta", channel)
}

func TestParseUpgradeTarget_RejectsConflictingChannel(t *testing.T) {
	_, _, err := parseUpgradeTarget("channel:stable", "beta")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "disagree")
}

func TestParseUpgradeTarget_RejectsInvalidChannel(t *testing.T) {
	_, _, err := parseUpgradeTarget("channel:alpha", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid channel")

	_, _, err = parseUpgradeTarget("1.0.0", "saas")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid --kit-channel")
}

func TestSummarizeMismatches_EmptyResolver(t *testing.T) {
	assert.Nil(t, summarizeMismatches(nil, "stable"))

	r := &registry.ResolveResult{Modules: nil}
	assert.Empty(t, summarizeMismatches(r, "stable"))
}

func TestSummarizeMismatches_OnlyFallbackOutsideDesired(t *testing.T) {
	r := &registry.ResolveResult{Modules: []registry.ResolvedModule{
		{ModuleSlug: "traefik", Channel: "stable", Reason: "matched"},
		{ModuleSlug: "vaultwarden", Channel: "beta", Reason: "fallback"},
		{ModuleSlug: "dokploy", Channel: "edge", Reason: "override"}, // ignored: override is operator-blessed
	}}
	mismatches := summarizeMismatches(r, "stable")
	require.Len(t, mismatches, 1)
	assert.Equal(t, "vaultwarden", mismatches[0].ModuleSlug)
}

func TestBuildChannelMap_ProjectsResolverFields(t *testing.T) {
	r := &registry.ResolveResult{Modules: []registry.ResolvedModule{
		{ModuleSlug: "traefik", ModuleSemver: "3.2.0", Channel: "stable", Reason: "matched"},
		{ModuleSlug: "vaultwarden", ModuleSemver: "1.30.0", Channel: "beta", Reason: "fallback"},
	}}
	got := buildChannelMap(r)
	require.Len(t, got, 2)
	assert.Equal(t, snapshot.ChannelMapEntry{
		ModuleSlug: "traefik", ModuleVersion: "3.2.0", Channel: "stable", Reason: "matched",
	}, got[0])
	assert.Equal(t, "fallback", got[1].Reason)
}

func TestBuildChannelMap_NilResolver(t *testing.T) {
	assert.Nil(t, buildChannelMap(nil))
}

func TestFirstKopiaID(t *testing.T) {
	assert.Equal(t, "", firstKopiaID(nil))

	m := &snapshot.SnapshotManifest{
		KopiaSnapshots: []snapshot.KopiaSnapshotRef{
			{Path: "/var/lib/postgres", SnapshotID: "snap-1"},
			{Path: "/var/lib/vaultwarden", SnapshotID: "snap-2"},
		},
	}
	assert.Equal(t, "snap-1", firstKopiaID(m))

	empty := &snapshot.SnapshotManifest{}
	assert.Equal(t, "", firstKopiaID(empty))
}

func TestResolveTargetVersion_PicksLatestInChannel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/sk/registry/stackkits/base-kit/versions", r.URL.Path)
		assert.Equal(t, "stable", r.URL.Query().Get("channel"))
		_ = json.NewEncoder(w).Encode([]kitVersionMeta{
			{ID: "v1", Semver: "1.0.0", Channel: "stable", ReleasedAt: parseTime(t, "2026-04-01T00:00:00Z")},
			{ID: "v2", Semver: "1.1.0", Channel: "stable", ReleasedAt: parseTime(t, "2026-05-01T00:00:00Z")},
		})
	}))
	defer srv.Close()

	v, err := resolveTargetVersion(context.Background(), srv.URL, "", "base-kit", "stable", "")
	require.NoError(t, err)
	assert.Equal(t, "v2", v.ID)
	assert.Equal(t, "1.1.0", v.Semver)
}

func TestResolveTargetVersion_ExplicitSemverHit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]kitVersionMeta{
			{ID: "v1", Semver: "1.0.0", Channel: "stable"},
			{ID: "v2", Semver: "1.1.0", Channel: "stable"},
		})
	}))
	defer srv.Close()

	v, err := resolveTargetVersion(context.Background(), srv.URL, "", "base-kit", "stable", "1.0.0")
	require.NoError(t, err)
	assert.Equal(t, "v1", v.ID)
}

func TestResolveTargetVersion_ExplicitSemverMiss(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]kitVersionMeta{
			{ID: "v1", Semver: "1.0.0", Channel: "stable"},
		})
	}))
	defer srv.Close()

	_, err := resolveTargetVersion(context.Background(), srv.URL, "", "base-kit", "stable", "9.9.9")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "9.9.9")
	assert.Contains(t, err.Error(), "not found")
}

func TestResolveTargetVersion_EmptyChannelList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]kitVersionMeta{})
	}))
	defer srv.Close()

	_, err := resolveTargetVersion(context.Background(), srv.URL, "", "base-kit", "stable", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no versions found")
}

func TestResolveTargetVersion_NoEndpoint(t *testing.T) {
	_, err := resolveTargetVersion(context.Background(), "", "", "base-kit", "stable", "")
	require.Error(t, err)
}

// parseTime is a tiny RFC3339 helper that fails the test if the
// timestamp is malformed. Used to seed kitVersionMeta.ReleasedAt.
func parseTime(t *testing.T, iso string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, iso)
	require.NoError(t, err)
	return parsed
}
