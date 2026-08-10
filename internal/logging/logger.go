// Package logging provides structured deploy logging for StackKits CLI.
// It writes JSON-Lines log files to .stackkit/logs/ that capture every
// decision, phase timing, and error during generate/apply/remove operations.
package logging

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/rollout"
)

const maxRunIDCreateAttempts = 16

var (
	legacyRunIDPattern = regexp.MustCompile(`^[0-9]{8}-[0-9]{6}$`)
	uniqueRunIDPattern = regexp.MustCompile(`^[0-9]{8}-[0-9]{6}\.[0-9]{9}(?:-[A-Za-z0-9][A-Za-z0-9._-]{0,63})?-[a-f0-9]{16}$`)
	correlationPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
)

// DeployLogger writes structured JSON-Lines logs for a single CLI run.
type DeployLogger struct {
	logger        *slog.Logger
	file          *os.File
	runID         string
	correlationID string
	startAt       time.Time
	logPath       string
}

// New creates a DeployLogger writing to one collision-resistant, exclusively
// created logDir/{run-id}.jsonl path.
// Returns nil (not an error) if the log directory cannot be created,
// so callers can always use deployLog.Event() without nil checks.
func New(logDir string) *DeployLogger {
	logger, _ := NewWithCorrelation(logDir, "")
	return logger
}

// NewWithCorrelation creates an exclusively owned log file and, when
// supplied, binds every event to one validated caller correlation ID.
func NewWithCorrelation(logDir, correlationID string) (*DeployLogger, error) {
	correlationID = strings.TrimSpace(correlationID)
	if err := ValidateCorrelationID(correlationID); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(logDir, 0750); err != nil {
		return nil, err
	}

	var (
		runID   string
		logPath string
		f       *os.File
		err     error
	)
	for attempt := 0; attempt < maxRunIDCreateAttempts; attempt++ {
		runID, err = newRunID(time.Now().UTC(), correlationID)
		if err != nil {
			return nil, err
		}
		logPath = filepath.Join(logDir, runID+".jsonl")
		f, err = os.OpenFile(logPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			break
		}
		if !os.IsExist(err) {
			return nil, err
		}
	}
	if f == nil {
		return nil, fmt.Errorf("create exclusive rollout log after %d attempts", maxRunIDCreateAttempts)
	}

	handler := slog.NewJSONHandler(f, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})

	dl := &DeployLogger{
		logger:        slog.New(handler),
		file:          f,
		runID:         runID,
		correlationID: correlationID,
		startAt:       time.Now(),
		logPath:       logPath,
	}

	// Rotate old logs (keep last 10)
	rotateLogFiles(logDir, 10)

	return dl, nil
}

func newRunID(now time.Time, correlationID string) (string, error) {
	nonce := make([]byte, 8)
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("generate rollout log nonce: %w", err)
	}
	prefix := now.UTC().Format("20060102-150405.000000000")
	if correlationID != "" {
		prefix += "-" + correlationID
	}
	return fmt.Sprintf("%s-%x", prefix, nonce), nil
}

// IsValidRunID accepts both the immutable legacy timestamp shape and the
// collision-resistant current shape used as an exact log basename.
func IsValidRunID(value string) bool {
	value = strings.TrimSpace(value)
	return legacyRunIDPattern.MatchString(value) || uniqueRunIDPattern.MatchString(value)
}

// ValidateCorrelationID rejects implicit, path-like, or unbounded correlation
// values before they can influence evidence names or event identity.
func ValidateCorrelationID(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if !correlationPattern.MatchString(value) {
		return errors.New("correlation ID must match [A-Za-z0-9][A-Za-z0-9._-]{0,63}")
	}
	return nil
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
	dl.log(slog.LevelInfo, msg, attrs...)
}

// Warn logs a warning event. Safe to call on nil receiver.
func (dl *DeployLogger) Warn(msg string, attrs ...slog.Attr) {
	dl.log(slog.LevelWarn, msg, attrs...)
}

// Error logs an error event. Safe to call on nil receiver.
func (dl *DeployLogger) Error(msg string, attrs ...slog.Attr) {
	dl.log(slog.LevelError, msg, attrs...)
}

func (dl *DeployLogger) log(level slog.Level, msg string, attrs ...slog.Attr) {
	if dl == nil {
		return
	}
	elapsed := time.Since(dl.startAt).Milliseconds()
	allAttrs := make([]slog.Attr, 0, len(attrs)+2)
	allAttrs = append(allAttrs, slog.Int64("elapsed_ms", elapsed))
	if dl.correlationID != "" {
		allAttrs = append(allAttrs, slog.String("correlation_id", dl.correlationID))
	}
	for _, attr := range attrs {
		allAttrs = append(allAttrs, redactAttr(attr))
	}

	args := make([]any, len(allAttrs))
	for i, a := range allAttrs {
		args[i] = a
	}
	dl.logger.Log(context.Background(), level, RedactText(msg), args...)
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
		entry, ok := ParseLogLine([]byte(line))
		if !ok {
			continue
		}
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
		runID := strings.TrimSuffix(e.Name(), ".jsonl")
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".jsonl") && IsValidRunID(runID) {
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

// ParseLogLine parses and defensively redacts one legacy or current JSONL event.
func ParseLogLine(line []byte) (LogEntry, bool) {
	var raw map[string]interface{}
	if err := json.Unmarshal(line, &raw); err != nil {
		return LogEntry{}, false
	}
	raw = MaskSecrets(raw)
	redacted, err := json.Marshal(raw)
	if err != nil {
		return LogEntry{}, false
	}
	entry := LogEntry{Fields: raw, RawJSON: redacted}
	entry.Time, _ = raw["time"].(string)
	entry.Level, _ = raw["level"].(string)
	entry.Msg, _ = raw["msg"].(string)
	return entry, true
}

// RedactText removes common inline credential shapes from untrusted text.
func RedactText(value string) string {
	return rollout.Redact(value)
}

// RedactValue recursively removes sensitive fields and inline secret strings.
func RedactValue(value interface{}) interface{} {
	switch typed := value.(type) {
	case string:
		return RedactText(typed)
	case map[string]interface{}:
		return MaskSecrets(typed)
	case map[string]string:
		result := make(map[string]string, len(typed))
		for key, item := range typed {
			if sensitiveKey(key) {
				result[key] = "***"
			} else {
				result[key] = RedactText(item)
			}
		}
		return result
	case []interface{}:
		result := make([]interface{}, len(typed))
		for index, item := range typed {
			result[index] = RedactValue(item)
		}
		return result
	case []string:
		result := make([]string, len(typed))
		for index, item := range typed {
			result[index] = RedactText(item)
		}
		return result
	case json.RawMessage:
		var decoded interface{}
		if json.Unmarshal(typed, &decoded) == nil {
			return RedactValue(decoded)
		}
		return json.RawMessage([]byte(RedactText(string(typed))))
	default:
		raw, err := json.Marshal(value)
		if err != nil || len(raw) == 0 || (raw[0] != '{' && raw[0] != '[') {
			return value
		}
		var decoded interface{}
		if json.Unmarshal(raw, &decoded) != nil {
			return value
		}
		return RedactValue(decoded)
	}
}

func redactAttr(attr slog.Attr) slog.Attr {
	if sensitiveKey(attr.Key) {
		return slog.String(attr.Key, "***")
	}
	value := attr.Value.Resolve()
	if value.Kind() == slog.KindGroup {
		group := value.Group()
		redacted := make([]slog.Attr, 0, len(group))
		for _, child := range group {
			redacted = append(redacted, redactAttr(child))
		}
		return slog.Group(attr.Key, attrsToAny(redacted)...)
	}
	if value.Kind() == slog.KindString {
		return slog.String(attr.Key, RedactText(value.String()))
	}
	if value.Kind() == slog.KindAny {
		return slog.Any(attr.Key, RedactValue(value.Any()))
	}
	return slog.Attr{Key: attr.Key, Value: value}
}

func attrsToAny(attrs []slog.Attr) []any {
	result := make([]any, len(attrs))
	for index, attr := range attrs {
		result[index] = attr
	}
	return result
}

func sensitiveKey(key string) bool {
	normalized := strings.ToLower(strings.NewReplacer("_", "", "-", "", ".", "").Replace(key))
	for _, marker := range []string{
		"password", "passwd", "token", "secret", "credential", "apikey",
		"authorization", "bearer", "privatekey", "encryptionkey", "signingkey",
	} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

// MaskSecrets replaces sensitive values recursively with "***" and redacts
// inline credential shapes from all retained string values.
func MaskSecrets(attrs map[string]interface{}) map[string]interface{} {
	masked := make(map[string]interface{}, len(attrs))
	for k, v := range attrs {
		if sensitiveKey(k) {
			masked[k] = "***"
		} else {
			masked[k] = RedactValue(v)
		}
	}
	return masked
}
