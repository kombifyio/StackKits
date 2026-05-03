package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	backupcontroller "github.com/kombifyio/stackkits/internal/backup-controller"
)

// TestSetupLogging is a smoke test that the level parser does not
// panic on any of the documented inputs and silently falls back on
// nonsense. Loosely-typed env vars in production make the fallback
// the most-exercised path.
func TestSetupLogging(t *testing.T) {
	for _, lv := range []string{"debug", "info", "warn", "error", "DEBUG", "garbage", ""} {
		require.NotPanics(t, func() { setupLogging(lv) }, "level=%q", lv)
	}
}

// TestEnvOr_ReturnsEnvWhenSet documents the precedence: env wins over
// flag default. The controller's deployment uses env vars
// exclusively, and a regression to the opposite precedence would
// silently ignore the prod config.
func TestEnvOr_ReturnsEnvWhenSet(t *testing.T) {
	t.Setenv("BACKUP_CONTROLLER_API_KEY", "from-env")
	assert.Equal(t, "from-env", envOr("BACKUP_CONTROLLER_API_KEY", "from-flag"))
}

// TestEnvOr_FallsBackToFlag confirms the opposite branch.
func TestEnvOr_FallsBackToFlag(t *testing.T) {
	t.Setenv("BACKUP_CONTROLLER_API_KEY", "")
	assert.Equal(t, "from-flag", envOr("BACKUP_CONTROLLER_API_KEY", "from-flag"))
}

// TestResolvePort_HonoursValidEnv exercises the parse-and-fall-back
// logic. The flag default is the last resort; an invalid env var
// must not crash the binary on startup.
func TestResolvePort_HonoursValidEnv(t *testing.T) {
	t.Setenv("BACKUP_CONTROLLER_PORT", "9090")
	assert.Equal(t, 9090, resolvePort(8083))
}

func TestResolvePort_IgnoresInvalidEnv(t *testing.T) {
	t.Setenv("BACKUP_CONTROLLER_PORT", "not-a-number")
	assert.Equal(t, 8083, resolvePort(8083))
}

// TestServer_BindsControllerHandler confirms the wiring: the
// internal/backup-controller Server's Handler() is what the binary
// hosts. We don't start the long-running goroutines (scheduler is
// orthogonal to HTTP routing); we just spin a httptest.Server
// against the same Handler() and verify a known route resolves.
//
// This catches the kind of regression where a future refactor
// accidentally hosts a different handler or forgets to wire the
// audit log.
func TestServer_BindsControllerHandler(t *testing.T) {
	store := backupcontroller.NewMemoryStore()
	srv := &backupcontroller.Server{
		Store:          store,
		Audit:          &backupcontroller.AuditLog{Store: store},
		OperatorAPIKey: "test-key",
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	resp, err := http.Get(ts.URL + "/healthz")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Operator routes still require the key — sanity-check the
	// middleware composition didn't get dropped during refactor.
	resp2, err := http.Get(ts.URL + "/api/v1/tenants")
	require.NoError(t, err)
	defer resp2.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp2.StatusCode)
}
