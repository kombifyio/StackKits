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

	"github.com/kombifyio/stackkits/pkg/models"
)

func TestAppendUpdateChecks_NoStateYieldsWarn(t *testing.T) {
	r := &doctorReport{Status: "pass"}
	appendUpdateChecks(context.Background(), r, nil, nil)

	require.Len(t, r.Checks, 1)
	assert.Equal(t, "updates", r.Checks[0].Name)
	assert.Equal(t, "warn", r.Checks[0].Status)
	assert.Contains(t, r.Checks[0].Message, "no .stackkit/state.yaml")
	assert.Equal(t, "warn", r.Status)
}

func TestAppendUpdateChecks_StateMissingVersionMetaYieldsWarn(t *testing.T) {
	r := &doctorReport{Status: "pass"}
	state := &models.DeploymentState{StackKit: "base-kit"} // no KitVersionID
	appendUpdateChecks(context.Background(), r, state, nil)

	require.Len(t, r.Checks, 1)
	assert.Equal(t, "warn", r.Checks[0].Status)
	assert.Contains(t, r.Checks[0].Message, "KitVersionID/KitChannel")
}

func TestAppendUpdateChecks_NoEndpointYieldsWarn(t *testing.T) {
	t.Setenv("STACKKIT_ADMIN_ENDPOINT", "")
	t.Setenv("ADMIN_PUBLIC_API_URL", "")
	t.Setenv("ADMIN_API_URL", "")

	r := &doctorReport{Status: "pass"}
	state := &models.DeploymentState{
		StackKit:     "base-kit",
		KitVersionID: "v1",
		KitSemver:    "1.0.0",
		KitChannel:   "stable",
	}
	appendUpdateChecks(context.Background(), r, state, nil)
	require.Len(t, r.Checks, 1)
	assert.Equal(t, "warn", r.Checks[0].Status)
	assert.Contains(t, r.Checks[0].Message, "STACKKIT_ADMIN_ENDPOINT not set")
}

func TestAppendUpdateChecks_NoUpgradesYieldsPass(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]kitVersionMeta{
			{ID: "v1", Semver: "1.0.0", Channel: "stable", ReleasedAt: time.Now().Add(-72 * time.Hour)},
		})
	}))
	defer srv.Close()

	t.Setenv("STACKKIT_ADMIN_ENDPOINT", srv.URL)
	t.Setenv("STACKKIT_ADMIN_TOKEN", "tok")

	r := &doctorReport{Status: "pass"}
	state := &models.DeploymentState{
		StackKit:     "base-kit",
		KitVersionID: "v1",
		KitSemver:    "1.0.0",
		KitChannel:   "stable",
	}
	appendUpdateChecks(context.Background(), r, state, nil)

	require.Len(t, r.Checks, 1)
	assert.Equal(t, "pass", r.Checks[0].Status)
	assert.Contains(t, r.Checks[0].Message, "is at latest")
	assert.Equal(t, "pass", r.Status)
}

func TestAppendUpdateChecks_NewerVersionYieldsWarnPlusCTA(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]kitVersionMeta{
			{ID: "v1", Semver: "1.0.0", Channel: "stable", ReleasedAt: time.Now().Add(-72 * time.Hour)},
			{ID: "v2", Semver: "1.1.0", Channel: "stable", ReleasedAt: time.Now().Add(-24 * time.Hour)},
		})
	}))
	defer srv.Close()

	t.Setenv("STACKKIT_ADMIN_ENDPOINT", srv.URL)
	t.Setenv("STACKKIT_ADMIN_TOKEN", "")

	r := &doctorReport{Status: "pass"}
	state := &models.DeploymentState{
		StackKit:     "base-kit",
		KitVersionID: "v1",
		KitSemver:    "1.0.0",
		KitChannel:   "stable",
	}
	appendUpdateChecks(context.Background(), r, state, nil)

	// Expect 2 checks: one per available upgrade + the CTA line.
	require.Len(t, r.Checks, 2)
	assert.Equal(t, "updates", r.Checks[0].Name)
	assert.Contains(t, r.Checks[0].Message, "1.1.0 available")
	assert.Equal(t, "updates-cta", r.Checks[1].Name)
	assert.Contains(t, r.Checks[1].Message, "stackkit kit upgrade")
	assert.Equal(t, "warn", r.Status)
}

func TestAppendUpdateChecks_AdminFailureYieldsWarnNotFail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	t.Setenv("STACKKIT_ADMIN_ENDPOINT", srv.URL)

	r := &doctorReport{Status: "pass"}
	state := &models.DeploymentState{
		StackKit:     "base-kit",
		KitVersionID: "v1",
		KitSemver:    "1.0.0",
		KitChannel:   "stable",
	}
	appendUpdateChecks(context.Background(), r, state, nil)

	require.Len(t, r.Checks, 1)
	assert.Equal(t, "warn", r.Checks[0].Status, "network/admin failure should be warn, not fail")
	assert.Contains(t, r.Checks[0].Message, "admin query failed")
}

func TestFormatDate(t *testing.T) {
	assert.Equal(t, "unknown", formatDate(time.Time{}))
	specific := time.Date(2026, 5, 8, 12, 30, 45, 0, time.UTC)
	assert.Equal(t, "2026-05-08", formatDate(specific))
}
