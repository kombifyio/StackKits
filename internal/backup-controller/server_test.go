package backupcontroller

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	store := NewMemoryStore()
	srv := &Server{
		Store:          store,
		Audit:          &AuditLog{Store: store},
		OperatorAPIKey: "test-key",
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return srv, ts
}

func operatorPOST(t *testing.T, ts *httptest.Server, path string, body interface{}) *http.Response {
	t.Helper()
	buf := &bytes.Buffer{}
	require.NoError(t, json.NewEncoder(buf).Encode(body))
	req, err := http.NewRequest(http.MethodPost, ts.URL+path, buf)
	require.NoError(t, err)
	req.Header.Set("X-API-Key", "test-key")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	return resp
}

func TestServer_TenantCRUD(t *testing.T) {
	_, ts := newTestServer(t)

	resp := operatorPOST(t, ts, "/api/v1/tenants", &Tenant{Name: "acme"})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var created Tenant
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
	resp.Body.Close()
	assert.NotEmpty(t, created.ID)
	assert.Equal(t, PlanFree, created.Plan, "default plan should be free")

	// List and verify.
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/tenants", nil)
	req.Header.Set("X-API-Key", "test-key")
	listResp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer listResp.Body.Close()
	require.Equal(t, http.StatusOK, listResp.StatusCode)
	var list []Tenant
	require.NoError(t, json.NewDecoder(listResp.Body).Decode(&list))
	assert.Len(t, list, 1)
}

func TestServer_RejectsMissingAPIKey(t *testing.T) {
	_, ts := newTestServer(t)
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/tenants", nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestServer_HealthzNoAuth(t *testing.T) {
	_, ts := newTestServer(t)
	resp, err := http.Get(ts.URL + "/healthz")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestServer_AgentHeartbeat(t *testing.T) {
	srv, ts := newTestServer(t)
	ctx := context.Background()

	// Set up a tenant → fleet → host with a known token.
	tenant := &Tenant{Name: "acme"}
	require.NoError(t, srv.Store.CreateTenant(ctx, tenant))
	fleet := &Fleet{TenantID: tenant.ID, Name: "default"}
	require.NoError(t, srv.Store.CreateFleet(ctx, fleet))
	host := &Host{FleetID: fleet.ID, Hostname: "alpha", AgentToken: "agent-secret", StackKitKind: HostKindBaseKit}
	require.NoError(t, srv.Store.CreateHost(ctx, host))

	// Heartbeat with the right token → 204.
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/agent/heartbeat", nil)
	req.Header.Set("X-Agent-Token", "agent-secret")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)

	// Heartbeat with a wrong token → 401.
	req, _ = http.NewRequest(http.MethodPost, ts.URL+"/api/v1/agent/heartbeat", nil)
	req.Header.Set("X-Agent-Token", "wrong")
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	// LastSeen should be populated.
	got, err := srv.Store.GetHost(ctx, host.ID)
	require.NoError(t, err)
	assert.False(t, got.LastSeen.IsZero())
}
