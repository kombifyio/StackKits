package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testServerWithLogs(t *testing.T) (*Server, string) {
	t.Helper()
	srv, tmpDir := testServer(t)

	// Create log directory with sample log files
	logDir := filepath.Join(tmpDir, ".stackkit", "logs")
	require.NoError(t, os.MkdirAll(logDir, 0750))

	// Write a sample log file
	logContent := `{"time":"2026-03-11T10:45:39.123Z","level":"INFO","msg":"apply.start","stackkit":"base-kit","mode":"simple","elapsed_ms":42}
{"time":"2026-03-11T10:45:40.456Z","level":"WARN","msg":"prepare.docker","installed":true,"elapsed_ms":1300}
{"time":"2026-03-11T10:45:45.789Z","level":"ERROR","msg":"tofu.apply","attempt":0,"success":false,"elapsed_ms":6500}
{"time":"2026-03-11T10:46:15.000Z","level":"INFO","msg":"apply.success","elapsed_ms":36000}
`
	require.NoError(t, os.WriteFile(filepath.Join(logDir, "20260311-104539.jsonl"), []byte(logContent), 0600))

	// Write a second log file
	logContent2 := `{"time":"2026-03-11T11:00:00.000Z","level":"INFO","msg":"generate.complete","file_count":3,"elapsed_ms":500}
`
	require.NoError(t, os.WriteFile(filepath.Join(logDir, "20260311-110000.jsonl"), []byte(logContent2), 0600))

	// Update server config with log dir
	srv.config.LogDir = logDir

	return srv, tmpDir
}

// ── List Logs ─────────────────────────────────────────────────────

func TestHandleListLogs(t *testing.T) {
	srv, _ := testServerWithLogs(t)
	handler := srv.Handler()

	t.Run("lists log runs", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/logs", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)

		resp := parseResponse(t, rec)
		var data struct {
			Items  []logRunSummary `json:"items"`
			Total  int             `json:"total"`
			Limit  int             `json:"limit"`
			Offset int             `json:"offset"`
		}
		require.NoError(t, json.Unmarshal(resp["data"], &data))
		assert.Equal(t, 2, data.Total)
		assert.Len(t, data.Items, 2)
		assert.Equal(t, "20260311-104539", data.Items[0].RunID)
		assert.Equal(t, "20260311-110000", data.Items[1].RunID)
	})

	t.Run("pagination", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/logs?limit=1&offset=1", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		resp := parseResponse(t, rec)
		var data struct {
			Items []logRunSummary `json:"items"`
			Total int             `json:"total"`
		}
		require.NoError(t, json.Unmarshal(resp["data"], &data))
		assert.Len(t, data.Items, 1)
		assert.Equal(t, 2, data.Total)
		assert.Equal(t, "20260311-110000", data.Items[0].RunID)
	})
}

func TestHandleListLogs_NoLogDir(t *testing.T) {
	srv, _ := testServer(t)
	// LogDir is empty by default
	req := httptest.NewRequest("GET", "/api/v1/logs", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

// ── Get Latest Log ────────────────────────────────────────────────

func TestHandleGetLatestLog(t *testing.T) {
	srv, _ := testServerWithLogs(t)
	handler := srv.Handler()

	req := httptest.NewRequest("GET", "/api/v1/logs/latest", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	resp := parseResponse(t, rec)
	var data struct {
		RunID  string                   `json:"run_id"`
		Events []map[string]interface{} `json:"events"`
		Total  int                      `json:"total"`
	}
	require.NoError(t, json.Unmarshal(resp["data"], &data))
	assert.Equal(t, "20260311-110000", data.RunID)
	assert.Equal(t, 1, data.Total)
}

// ── Get Log by Run ID ─────────────────────────────────────────────

func TestHandleGetLog(t *testing.T) {
	srv, _ := testServerWithLogs(t)
	handler := srv.Handler()

	t.Run("found", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/logs/20260311-104539", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)

		resp := parseResponse(t, rec)
		var data struct {
			RunID  string                   `json:"run_id"`
			Events []map[string]interface{} `json:"events"`
			Total  int                      `json:"total"`
		}
		require.NoError(t, json.Unmarshal(resp["data"], &data))
		assert.Equal(t, "20260311-104539", data.RunID)
		assert.Equal(t, 4, data.Total)
	})

	t.Run("not found", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/logs/20260101-000000", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusNotFound, rec.Code)
	})

	t.Run("invalid run ID", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/logs/invalid", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("filter by level", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/logs/20260311-104539?level=error", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		resp := parseResponse(t, rec)
		var data struct {
			Events []map[string]interface{} `json:"events"`
			Total  int                      `json:"total"`
		}
		require.NoError(t, json.Unmarshal(resp["data"], &data))
		assert.Equal(t, 1, data.Total)
	})

	t.Run("filter by prefix", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/logs/20260311-104539?prefix=apply.", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		resp := parseResponse(t, rec)
		var data struct {
			Events []map[string]interface{} `json:"events"`
			Total  int                      `json:"total"`
		}
		require.NoError(t, json.Unmarshal(resp["data"], &data))
		assert.Equal(t, 2, data.Total) // apply.start + apply.success
	})
}

// ── Helpers ───────────────────────────────────────────────────────

func TestIsValidRunID(t *testing.T) {
	assert.True(t, isValidRunID("20260311-104539"))
	assert.True(t, isValidRunID("20260101-000000"))
	assert.False(t, isValidRunID("invalid"))
	assert.False(t, isValidRunID(""))
	assert.False(t, isValidRunID("2026-03-11T10:45"))
	assert.False(t, isValidRunID("../../../etc/passwd"))
}

func TestIsTerminalEvent(t *testing.T) {
	assert.True(t, isTerminalEvent("apply.success"))
	assert.True(t, isTerminalEvent("apply.failed"))
	assert.True(t, isTerminalEvent("generate.complete"))
	assert.True(t, isTerminalEvent("remove.complete"))
	assert.False(t, isTerminalEvent("apply.start"))
	assert.False(t, isTerminalEvent("tofu.init"))
}

func TestParseRunTimestamp(t *testing.T) {
	ts := parseRunTimestamp("20260311-104539")
	assert.Equal(t, "2026-03-11T10:45:39Z", ts)

	ts = parseRunTimestamp("invalid")
	assert.Equal(t, "", ts)
}
