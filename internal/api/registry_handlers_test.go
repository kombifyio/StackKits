package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kombifyio/stackkits/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegistryRoutes(t *testing.T) {
	srv, _ := testServer(t)
	handler := srv.Handler()

	reg := models.InstanceRegistration{
		InstanceID:  "test-instance",
		EndpointURL: "https://test-api.kombify.me",
		StackKit:    "base-kit",
		Services: []models.ServiceInfo{
			{Name: "base", Status: "running"},
		},
		Status:  "running",
		APIPort: 8082,
	}
	body, err := json.Marshal(reg)
	require.NoError(t, err)

	req := httptest.NewRequest("POST", "/api/v1/registry/instances", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusCreated, rec.Code)
	resp := parseResponse(t, rec)
	var data registryResponse
	require.NoError(t, json.Unmarshal(resp["data"], &data))
	assert.Equal(t, "test-instance", data.InstanceID)
	assert.Equal(t, "registered", data.Status)

	hb := httptest.NewRequest("PUT", "/api/v1/registry/instances/test-instance/heartbeat", strings.NewReader(`{"instance_id":"test-instance","status":"degraded"}`))
	hb.Header.Set("Content-Type", "application/json")
	hbRec := httptest.NewRecorder()
	handler.ServeHTTP(hbRec, hb)
	assert.Equal(t, http.StatusNoContent, hbRec.Code)

	del := httptest.NewRequest("DELETE", "/api/v1/registry/instances/test-instance", nil)
	delRec := httptest.NewRecorder()
	handler.ServeHTTP(delRec, del)
	assert.Equal(t, http.StatusNoContent, delRec.Code)

	missing := httptest.NewRequest("DELETE", "/api/v1/registry/instances/test-instance", nil)
	missingRec := httptest.NewRecorder()
	handler.ServeHTTP(missingRec, missing)
	assert.Equal(t, http.StatusNotFound, missingRec.Code)
}

func TestRegistryRegisterRequiresInstanceID(t *testing.T) {
	srv, _ := testServer(t)
	req := httptest.NewRequest("POST", "/api/v1/registry/instances", strings.NewReader(`{"endpoint_url":"https://example.test","stackkit":"base-kit","status":"running"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	resp := parseResponse(t, rec)
	assert.Contains(t, string(resp["error"]), "instance_id")
}

func TestRegistryHeartbeatRequiresMatchingInstanceID(t *testing.T) {
	srv, _ := testServer(t)
	handler := srv.Handler()

	body := `{"instance_id":"test-instance","endpoint_url":"https://example.test","stackkit":"base-kit","services":[],"status":"running"}`
	req := httptest.NewRequest("POST", "/api/v1/registry/instances", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)

	hb := httptest.NewRequest("PUT", "/api/v1/registry/instances/test-instance/heartbeat", strings.NewReader(`{"instance_id":"other","status":"running"}`))
	hb.Header.Set("Content-Type", "application/json")
	hbRec := httptest.NewRecorder()
	handler.ServeHTTP(hbRec, hb)

	assert.Equal(t, http.StatusBadRequest, hbRec.Code)
	resp := parseResponse(t, hbRec)
	assert.Contains(t, string(resp["error"]), "instance_id")
}
