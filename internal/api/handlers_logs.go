package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	skerrors "github.com/kombifyio/stackkits/internal/errors"
	"github.com/kombifyio/stackkits/internal/logging"
)

// ── Logs: List ────────────────────────────────────────────────────

type logRunSummary struct {
	RunID     string `json:"run_id"`
	Timestamp string `json:"timestamp,omitempty"`
	File      string `json:"file"`
}

func (s *Server) handleListLogs(w http.ResponseWriter, r *http.Request) {
	logDir := s.config.LogDir
	if logDir == "" {
		writeStructuredError(w, r, http.StatusServiceUnavailable, skerrors.NewResourceError(
			"logs_not_configured", "log directory not configured",
			skerrors.WithSuggestion("Set --log-dir when starting stackkit-server"),
			skerrors.WithSuggestion("Or set STACKKITS_LOG_DIR environment variable"),
		))
		return
	}

	files, err := logging.ListLogFiles(logDir)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "failed to list log files")
		return
	}

	runs := make([]logRunSummary, 0, len(files))
	for _, f := range files {
		runID := strings.TrimSuffix(f, ".jsonl")
		ts := parseRunTimestamp(runID)
		runs = append(runs, logRunSummary{
			RunID:     runID,
			Timestamp: ts,
			File:      f,
		})
	}

	// Pagination
	total := len(runs)
	limit, offset := parsePagination(r, total)
	end := offset + limit
	if end > total {
		end = total
	}
	paged := runs[offset:end]

	writeSuccess(w, r, http.StatusOK, map[string]interface{}{
		"items":  paged,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

// ── Logs: Latest ──────────────────────────────────────────────────

func (s *Server) handleGetLatestLog(w http.ResponseWriter, r *http.Request) {
	logDir := s.config.LogDir
	if logDir == "" {
		writeStructuredError(w, r, http.StatusServiceUnavailable, skerrors.NewResourceError(
			"logs_not_configured", "log directory not configured",
		))
		return
	}

	latestPath, err := logging.LatestLogFile(logDir)
	if err != nil {
		writeStructuredError(w, r, http.StatusNotFound, skerrors.NewResourceError(
			"no_logs", "no log files found",
			skerrors.WithSuggestion("Run a stackkit command first to generate logs"),
		))
		return
	}

	serveLogFile(w, r, latestPath)
}

// ── Logs: Get by Run ID ───────────────────────────────────────────

func (s *Server) handleGetLog(w http.ResponseWriter, r *http.Request) {
	logDir := s.config.LogDir
	if logDir == "" {
		writeStructuredError(w, r, http.StatusServiceUnavailable, skerrors.NewResourceError(
			"logs_not_configured", "log directory not configured",
		))
		return
	}

	runID := r.PathValue("runID")
	if runID == "" {
		writeError(w, r, http.StatusBadRequest, "run ID is required")
		return
	}

	// Sanitize: only allow alphanumeric + dash (matches timestamp format YYYYMMDD-HHMMSS)
	if !isValidRunID(runID) {
		writeStructuredError(w, r, http.StatusBadRequest, skerrors.NewValidationError(
			"invalid_run_id", "invalid run ID format: "+runID,
			skerrors.WithSuggestion("Run ID format: YYYYMMDD-HHMMSS (e.g., 20260311-104539)"),
			skerrors.WithSuggestion("List available runs: GET /api/v1/logs"),
		))
		return
	}

	logPath := filepath.Join(logDir, runID+".jsonl")
	serveLogFile(w, r, logPath)
}

// ── Logs: Stream (SSE) ────────────────────────────────────────────

func (s *Server) handleStreamLog(w http.ResponseWriter, r *http.Request) {
	logDir := s.config.LogDir
	if logDir == "" {
		writeStructuredError(w, r, http.StatusServiceUnavailable, skerrors.NewResourceError(
			"logs_not_configured", "log directory not configured",
		))
		return
	}

	runID := r.PathValue("runID")
	if !isValidRunID(runID) {
		writeStructuredError(w, r, http.StatusBadRequest, skerrors.NewValidationError(
			"invalid_run_id", "invalid run ID format",
		))
		return
	}

	logPath := filepath.Join(logDir, runID+".jsonl")

	// Check if the client supports SSE
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, r, http.StatusInternalServerError, "streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	// Track how many entries we've already sent
	sentCount := 0
	ctx := r.Context()

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	// Maximum stream duration to prevent resource leaks
	maxDuration := 30 * time.Minute
	deadline := time.After(maxDuration)

	// Send existing entries first, then poll for new ones
	for {
		select {
		case <-ctx.Done():
			return
		case <-deadline:
			_, _ = fmt.Fprintf(w, "event: timeout\ndata: {\"reason\":\"max stream duration exceeded\"}\n\n")
			flusher.Flush()
			return
		case <-ticker.C:
			entries, err := logging.ReadLogFile(logPath)
			if err != nil {
				// File doesn't exist yet — keep waiting
				continue
			}

			// Send any new entries
			for i := sentCount; i < len(entries); i++ {
				data, jsonErr := json.Marshal(entries[i].Fields)
				if jsonErr != nil {
					continue
				}
				_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
				flusher.Flush()
			}
			sentCount = len(entries)

			// Check if the run is complete (look for terminal events)
			if sentCount > 0 {
				lastMsg := entries[sentCount-1].Msg
				if isTerminalEvent(lastMsg) {
					_, _ = fmt.Fprintf(w, "event: done\ndata: {\"run_id\":%q,\"final_event\":%q}\n\n", runID, lastMsg)
					flusher.Flush()
					return
				}
			}
		}
	}
}

// ── Helpers ───────────────────────────────────────────────────────

func serveLogFile(w http.ResponseWriter, r *http.Request, logPath string) {
	entries, err := logging.ReadLogFile(logPath)
	if err != nil {
		writeStructuredError(w, r, http.StatusNotFound, skerrors.NewResourceError(
			"log_not_found", "log file not found",
			skerrors.WithSuggestion("List available runs: GET /api/v1/logs"),
		))
		return
	}

	// Apply query filters
	level := r.URL.Query().Get("level")
	prefix := r.URL.Query().Get("prefix")

	filtered := entries
	if level != "" {
		filtered = filterEntriesByLevel(filtered, strings.ToUpper(level))
	}
	if prefix != "" {
		filtered = filterEntriesByPrefix(filtered, prefix)
	}

	// Build response
	items := make([]map[string]interface{}, 0, len(filtered))
	for _, e := range filtered {
		items = append(items, e.Fields)
	}

	// Extract run ID from path
	base := filepath.Base(logPath)
	runID := strings.TrimSuffix(base, ".jsonl")

	writeSuccess(w, r, http.StatusOK, map[string]interface{}{
		"run_id": runID,
		"events": items,
		"total":  len(items),
	})
}

func filterEntriesByLevel(entries []logging.LogEntry, level string) []logging.LogEntry {
	var result []logging.LogEntry
	for _, e := range entries {
		if e.Level == level {
			result = append(result, e)
		}
	}
	return result
}

func filterEntriesByPrefix(entries []logging.LogEntry, prefix string) []logging.LogEntry {
	var result []logging.LogEntry
	for _, e := range entries {
		if strings.HasPrefix(e.Msg, prefix) {
			result = append(result, e)
		}
	}
	return result
}

func isValidRunID(id string) bool {
	if len(id) != 15 { // YYYYMMDD-HHMMSS
		return false
	}
	for _, c := range id {
		if c != '-' && (c < '0' || c > '9') {
			return false
		}
	}
	return id[8] == '-'
}

func isTerminalEvent(msg string) bool {
	terminals := []string{
		"apply.success", "apply.failed",
		"generate.complete",
		"remove.complete",
		"prepare.complete", "prepare.failed",
	}
	for _, t := range terminals {
		if msg == t {
			return true
		}
	}
	return false
}

func parseRunTimestamp(runID string) string {
	t, err := time.Parse("20060102-150405", runID)
	if err != nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func parsePagination(r *http.Request, total int) (limit, offset int) {
	limit = total
	offset = 0
	if l := r.URL.Query().Get("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 {
			limit = v
		}
	}
	if o := r.URL.Query().Get("offset"); o != "" {
		if v, err := strconv.Atoi(o); err == nil && v >= 0 {
			offset = v
		}
	}
	if offset > total {
		offset = total
	}
	return limit, offset
}
