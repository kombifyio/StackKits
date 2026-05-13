// Package logging provides structured deploy logging for StackKits CLI.
// It writes JSON-Lines log files to .stackkit/logs/ that capture every
// decision, phase timing, and error during generate/apply/remove operations.
package logging

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// DeployLogger writes structured JSON-Lines logs for a single CLI run.
type DeployLogger struct {
	logger  *slog.Logger
	file    *os.File
	runID   string
	startAt time.Time
	logPath string
}

// New creates a DeployLogger writing to logDir/{timestamp}.jsonl.
// Returns nil (not an error) if the log directory cannot be created,
// so callers can always use deployLog.Event() without nil checks.
func New(logDir string) *DeployLogger {
	if err := os.MkdirAll(logDir, 0750); err != nil {
		return nil
	}

	runID := time.Now().Format("20060102-150405")
	logPath := filepath.Join(logDir, runID+".jsonl")

	f, err := os.Create(logPath)
	if err != nil {
		return nil
	}

	handler := slog.NewJSONHandler(f, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})

	dl := &DeployLogger{
		logger:  slog.New(handler),
		file:    f,
		runID:   runID,
		startAt: time.Now(),
		logPath: logPath,
	}

	// Rotate old logs (keep last 10)
	rotateLogFiles(logDir, 10)

	return dl
}

// RunID returns the unique identifier for this log run.
func (dl *DeployLogger) RunID() string {
	if dl == nil {
		return ""
	}
	return dl.runID
}

// LogPath returns the path to the log file.
func (dl *DeployLogger) LogPath() string {
	if dl == nil {
		return ""
	}
	return dl.logPath
}

// Event logs a structured event. Safe to call on nil receiver.
func (dl *DeployLogger) Event(msg string, attrs ...slog.Attr) {
	if dl == nil {
		return
	}
	// Add elapsed time
	elapsed := time.Since(dl.startAt).Milliseconds()
	allAttrs := make([]slog.Attr, 0, len(attrs)+1)
	allAttrs = append(allAttrs, slog.Int64("elapsed_ms", elapsed))
	allAttrs = append(allAttrs, attrs...)

	args := make([]any, len(allAttrs))
	for i, a := range allAttrs {
		args[i] = a
	}
	dl.logger.Info(msg, args...)
}

// Warn logs a warning event. Safe to call on nil receiver.
func (dl *DeployLogger) Warn(msg string, attrs ...slog.Attr) {
	if dl == nil {
		return
	}
	elapsed := time.Since(dl.startAt).Milliseconds()
	allAttrs := make([]slog.Attr, 0, len(attrs)+1)
	allAttrs = append(allAttrs, slog.Int64("elapsed_ms", elapsed))
	allAttrs = append(allAttrs, attrs...)

	args := make([]any, len(allAttrs))
	for i, a := range allAttrs {
		args[i] = a
	}
	dl.logger.Warn(msg, args...)
}

// Error logs an error event. Safe to call on nil receiver.
func (dl *DeployLogger) Error(msg string, attrs ...slog.Attr) {
	if dl == nil {
		return
	}
	elapsed := time.Since(dl.startAt).Milliseconds()
	allAttrs := make([]slog.Attr, 0, len(attrs)+1)
	allAttrs = append(allAttrs, slog.Int64("elapsed_ms", elapsed))
	allAttrs = append(allAttrs, attrs...)

	args := make([]any, len(allAttrs))
	for i, a := range allAttrs {
		args[i] = a
	}
	dl.logger.Error(msg, args...)
}

// Close flushes and closes the log file.
func (dl *DeployLogger) Close() {
	if dl == nil || dl.file == nil {
		return
	}
	_ = dl.file.Close()
}

// rotateLogFiles keeps only the most recent maxFiles log files.
func rotateLogFiles(logDir string, maxFiles int) {
	entries, err := os.ReadDir(logDir)
	if err != nil {
		return
	}

	var logFiles []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".jsonl") {
			logFiles = append(logFiles, e.Name())
		}
	}

	if len(logFiles) <= maxFiles {
		return
	}

	sort.Strings(logFiles)
	// Remove oldest files
	for i := 0; i < len(logFiles)-maxFiles; i++ {
		_ = os.Remove(filepath.Join(logDir, logFiles[i]))
	}
}

// LogEntry represents a single parsed log line for display/filtering.
type LogEntry struct {
	Time    string                 `json:"time"`
	Level   string                 `json:"level"`
	Msg     string                 `json:"msg"`
	Fields  map[string]interface{} `json:"-"`
	RawJSON []byte                 `json:"-"`
}

// ReadLogFile parses a JSONL log file into entries.
func ReadLogFile(path string) ([]LogEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var entries []LogEntry
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var entry LogEntry
		entry.RawJSON = []byte(line)

		var raw map[string]interface{}
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			continue
		}

		if t, ok := raw["time"].(string); ok {
			entry.Time = t
		}
		if l, ok := raw["level"].(string); ok {
			entry.Level = l
		}
		if m, ok := raw["msg"].(string); ok {
			entry.Msg = m
		}
		entry.Fields = raw
		entries = append(entries, entry)
	}
	return entries, nil
}

// ListLogFiles returns available log files sorted by name (newest last).
func ListLogFiles(logDir string) ([]string, error) {
	entries, err := os.ReadDir(logDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".jsonl") {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)
	return files, nil
}

// LatestLogFile returns the path to the most recent log file.
func LatestLogFile(logDir string) (string, error) {
	files, err := ListLogFiles(logDir)
	if err != nil {
		return "", err
	}
	if len(files) == 0 {
		return "", fmt.Errorf("no log files found in %s", logDir)
	}
	return filepath.Join(logDir, files[len(files)-1]), nil
}

// FormatEntryHuman formats a log entry for human-readable display.
func FormatEntryHuman(w io.Writer, entry LogEntry) {
	// Parse time for display
	t, err := time.Parse(time.RFC3339Nano, entry.Time)
	timeStr := entry.Time
	if err == nil {
		timeStr = t.Format("15:04:05")
	}

	// Level indicator
	levelIndicator := " "
	switch entry.Level {
	case "ERROR":
		levelIndicator = "E"
	case "WARN":
		levelIndicator = "W"
	case "DEBUG":
		levelIndicator = "D"
	}

	// Collect interesting fields (skip time, level, msg, elapsed_ms)
	var details []string
	for k, v := range entry.Fields {
		switch k {
		case "time", "level", "msg", "elapsed_ms":
			continue
		default:
			details = append(details, fmt.Sprintf("%s=%v", k, v))
		}
	}
	sort.Strings(details)

	detailStr := ""
	if len(details) > 0 {
		detailStr = "  " + strings.Join(details, " ")
	}

	_, _ = fmt.Fprintf(w, "%s %s %-30s%s\n", timeStr, levelIndicator, entry.Msg, detailStr)
}

// MaskSecrets replaces sensitive values in a map with "***".
func MaskSecrets(attrs map[string]interface{}) map[string]interface{} {
	sensitiveKeys := []string{"password", "token", "secret", "key", "hash", "credential", "apikey", "bearer", "auth", "authorization"}
	masked := make(map[string]interface{}, len(attrs))
	for k, v := range attrs {
		lower := strings.ToLower(k)
		isSensitive := false
		for _, sk := range sensitiveKeys {
			if strings.Contains(lower, sk) {
				isSensitive = true
				break
			}
		}
		if isSensitive {
			masked[k] = "***"
		} else {
			masked[k] = v
		}
	}
	return masked
}
