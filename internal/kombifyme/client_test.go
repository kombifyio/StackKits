package kombifyme

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	c := NewClient("test-api-key")
	c.baseURL = server.URL
	return c
}

func TestAutoRegister(t *testing.T) {
	t.Run("successful registration", func(t *testing.T) {
		c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "POST", r.Method)
			assert.Equal(t, "/subdomains/auto-register", r.URL.Path)
			assert.Equal(t, "test-api-key", r.Header.Get(apiKeyHeader))
			assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

			var body map[string]string
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			assert.Equal(t, "mylab", body["homelab_name"])
			assert.Equal(t, "abc123", body["device_fingerprint"])

			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(Subdomain{
				ID:   "sub-1",
				Name: "sh-mylab-abc123",
				FQDN: "sh-mylab-abc123.kombify.me",
			})
		})

		sub, err := c.AutoRegister("mylab", "abc123", "test")
		require.NoError(t, err)
		assert.Equal(t, "sub-1", sub.ID)
		assert.Equal(t, "sh-mylab-abc123", sub.Name)
	})

	t.Run("API error returns error", func(t *testing.T) {
		c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid name"}`))
		})

		_, err := c.AutoRegister("", "", "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "API error 400")
	})
}

func TestRegisterService(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "/subdomains/auto-register/service", r.URL.Path)

		var body map[string]string
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "sh-mylab-abc123", body["base_subdomain_name"])
		assert.Equal(t, "base", body["service_name"])

		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(Subdomain{
			ID:       "svc-1",
			Name:     "sh-mylab-abc123-base",
			ParentID: "sub-1",
		})
	})

	sub, err := c.RegisterService("sh-mylab-abc123", "base", "http://localhost:80", "Dashboard")
	require.NoError(t, err)
	assert.Equal(t, "svc-1", sub.ID)
	assert.Equal(t, "sub-1", sub.ParentID)
}

func TestExposeService(t *testing.T) {
	t.Run("successful expose", func(t *testing.T) {
		c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "PUT", r.Method)
			assert.Equal(t, "/subdomains/sub-1/services/svc-1/expose", r.URL.Path)

			var body map[string]bool
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			assert.True(t, body["exposed"])

			w.WriteHeader(http.StatusOK)
		})

		err := c.ExposeService("sub-1", "svc-1", true)
		require.NoError(t, err)
	})

	t.Run("API error", func(t *testing.T) {
		c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte("not found"))
		})

		err := c.ExposeService("bad-id", "bad-svc", true)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "API error 404")
	})
}

func TestListServices(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "GET", r.Method)
		assert.Equal(t, "/subdomains/sub-1/services", r.URL.Path)
		assert.Equal(t, "test-api-key", r.Header.Get(apiKeyHeader))

		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode([]Subdomain{
			{ID: "svc-1", Name: "base"},
			{ID: "svc-2", Name: "kuma"},
		})
	})

	subs, err := c.ListServices("sub-1")
	require.NoError(t, err)
	assert.Len(t, subs, 2)
	assert.Equal(t, "base", subs[0].Name)
	assert.Equal(t, "kuma", subs[1].Name)
}

func TestBaseKitServices(t *testing.T) {
	t.Run("standard tier includes dokploy and all L3 apps", func(t *testing.T) {
		services := BaseKitServices("standard")
		names := serviceNames(services)
		assert.Contains(t, names, "dokploy")
		assert.NotContains(t, names, "dockge")
		assert.Contains(t, names, "vault")
		assert.Contains(t, names, "media")
		assert.Contains(t, names, "photos")
	})

	t.Run("high tier includes dokploy and all L3 apps", func(t *testing.T) {
		services := BaseKitServices("high")
		names := serviceNames(services)
		assert.Contains(t, names, "dokploy")
		assert.Contains(t, names, "vault")
		assert.Contains(t, names, "media")
		assert.Contains(t, names, "photos")
	})

	t.Run("low tier includes dockge and vault only", func(t *testing.T) {
		services := BaseKitServices("low")
		names := serviceNames(services)
		assert.Contains(t, names, "dockge")
		assert.NotContains(t, names, "dokploy")
		assert.Contains(t, names, "vault")
		assert.NotContains(t, names, "media")
		assert.NotContains(t, names, "photos")
	})

	t.Run("all tiers have core services", func(t *testing.T) {
		for _, tier := range []string{"low", "standard", "high"} {
			services := BaseKitServices(tier)
			names := serviceNames(services)
			assert.Contains(t, names, "traefik", "tier=%s", tier)
			assert.Contains(t, names, "auth", "tier=%s", tier)
			assert.Contains(t, names, "id", "tier=%s", tier)
			assert.Contains(t, names, "base", "tier=%s", tier)
			assert.Contains(t, names, "dash", "tier=%s", tier)
			assert.Contains(t, names, "tinyauth", "tier=%s", tier)
			assert.Contains(t, names, "kuma", "tier=%s", tier)
			assert.Contains(t, names, "vault", "tier=%s", tier)
		}
	})
}

func TestProxyTargetAddrUsesPublicHTTPOrigin(t *testing.T) {
	assert.Equal(t, "http://203.0.113.10:80", proxyTargetAddr("203.0.113.10"))
	assert.Equal(t, "http://localhost:80", proxyTargetAddr(""))
}

func TestDeviceFingerprint(t *testing.T) {
	fp := DeviceFingerprint()
	assert.Len(t, fp, 6, "fingerprint should be 6 hex chars")
	// Should be deterministic for the same machine
	assert.Equal(t, fp, DeviceFingerprint())
}

func TestDeviceFingerprintUsesExplicitEnvironmentOverride(t *testing.T) {
	t.Setenv("KOMBIFY_DEVICE_FINGERPRINT", "abc123")

	assert.Equal(t, "abc123", DeviceFingerprint())
}

func serviceNames(defs []ServiceDef) []string {
	names := make([]string, len(defs))
	for i, d := range defs {
		names[i] = d.Name
	}
	return names
}
