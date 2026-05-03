package kombifyme

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kombifyio/stackkits/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegisterInstance(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "/registry/instances", r.URL.Path)
		assert.Equal(t, "test-key", r.Header.Get(apiKeyHeader))

		var reg models.InstanceRegistration
		require.NoError(t, json.NewDecoder(r.Body).Decode(&reg))
		assert.Equal(t, "test-instance", reg.InstanceID)
		assert.Equal(t, "https://test-api.kombify.me", reg.EndpointURL)
		assert.Equal(t, "base-kit", reg.StackKit)
		assert.Len(t, reg.Services, 1)

		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(RegistryResponse{
			InstanceID: "test-instance",
			Status:     "registered",
		})
	}))
	defer server.Close()

	client := NewClient("test-key")
	client.baseURL = server.URL

	resp, err := client.RegisterInstance(&models.InstanceRegistration{
		InstanceID:  "test-instance",
		EndpointURL: "https://test-api.kombify.me",
		StackKit:    "base-kit",
		Services: []models.ServiceInfo{
			{Name: "traefik", Status: "running"},
		},
		Status:  "running",
		APIPort: 8082,
	})

	require.NoError(t, err)
	assert.Equal(t, "test-instance", resp.InstanceID)
	assert.Equal(t, "registered", resp.Status)
}

func TestHeartbeat(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "PUT", r.Method)
		assert.Equal(t, "/registry/instances/test-instance/heartbeat", r.URL.Path)

		var body map[string]string
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "test-instance", body["instance_id"])
		assert.Equal(t, "running", body["status"])

		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := NewClient("test-key")
	client.baseURL = server.URL

	err := client.Heartbeat("test-instance", "running")
	require.NoError(t, err)
}

func TestDeregisterInstance(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "DELETE", r.Method)
		assert.Equal(t, "/registry/instances/test-instance", r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := NewClient("test-key")
	client.baseURL = server.URL

	err := client.DeregisterInstance("test-instance")
	require.NoError(t, err)
}

func TestRegisterInstance_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error": "invalid instance_id"}`))
	}))
	defer server.Close()

	client := NewClient("test-key")
	client.baseURL = server.URL

	_, err := client.RegisterInstance(&models.InstanceRegistration{
		InstanceID:  "",
		EndpointURL: "https://test-api.kombify.me",
		StackKit:    "base-kit",
		Services:    []models.ServiceInfo{},
		Status:      "running",
		LastSeen:    time.Now(),
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "register instance")
}
