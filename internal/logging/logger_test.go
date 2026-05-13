package logging

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeployLoggerWritesReadableJSONLEvents(t *testing.T) {
	logDir := t.TempDir()

	dl := New(logDir)
	if dl == nil {
		t.Fatal("New returned nil")
	}
	if dl.RunID() == "" {
		t.Fatal("RunID should not be empty")
	}
	if dl.LogPath() == "" {
		t.Fatal("LogPath should not be empty")
	}

	dl.Event("generate started", slog.String("phase", "generate"))
	dl.Warn("slow step", slog.Int("seconds", 3))
	dl.Error("apply failed", slog.String("token", "secret-value"))
	dl.Close()

	entries, err := ReadLogFile(dl.LogPath())
	if err != nil {
		t.Fatalf("ReadLogFile: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("len(entries) = %d, want 3", len(entries))
	}
	if entries[0].Msg != "generate started" {
		t.Fatalf("first msg = %q", entries[0].Msg)
	}
	if entries[0].Level != "INFO" {
		t.Fatalf("first level = %q", entries[0].Level)
	}
	if entries[0].Fields["phase"] != "generate" {
		t.Fatalf("phase field = %#v", entries[0].Fields["phase"])
	}

	var raw map[string]any
	if err := json.Unmarshal(entries[0].RawJSON, &raw); err != nil {
		t.Fatalf("raw json should parse: %v", err)
	}
	if _, ok := raw["elapsed_ms"]; !ok {
		t.Fatal("elapsed_ms should be present")
	}
}

func TestDeployLoggerNilReceiverIsSafe(t *testing.T) {
	var dl *DeployLogger

	if got := dl.RunID(); got != "" {
		t.Fatalf("nil RunID = %q, want empty", got)
	}
	if got := dl.LogPath(); got != "" {
		t.Fatalf("nil LogPath = %q, want empty", got)
	}
	dl.Event("ignored")
	dl.Warn("ignored")
	dl.Error("ignored")
	dl.Close()
}

func TestReadLogFileSkipsInvalidAndBlankLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log.jsonl")
	data := strings.Join([]string{
		`{"time":"2026-05-07T10:00:00Z","level":"INFO","msg":"ok","service":"base"}`,
		`not-json`,
		``,
		`{"time":"2026-05-07T10:00:01Z","level":"WARN","msg":"warn"}`,
	}, "\n")
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatalf("write log file: %v", err)
	}

	entries, err := ReadLogFile(path)
	if err != nil {
		t.Fatalf("ReadLogFile: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("len(entries) = %d, want 2", len(entries))
	}
	if entries[0].Msg != "ok" || entries[1].Msg != "warn" {
		t.Fatalf("unexpected entries: %#v", entries)
	}
}

func TestListAndLatestLogFilesSortJSONLOnly(t *testing.T) {
	logDir := t.TempDir()
	for _, name := range []string{"20260507-100001.jsonl", "ignore.txt", "20260507-100000.jsonl"} {
		if err := os.WriteFile(filepath.Join(logDir, name), []byte("{}\n"), 0600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	files, err := ListLogFiles(logDir)
	if err != nil {
		t.Fatalf("ListLogFiles: %v", err)
	}
	want := []string{"20260507-100000.jsonl", "20260507-100001.jsonl"}
	if strings.Join(files, ",") != strings.Join(want, ",") {
		t.Fatalf("files = %v, want %v", files, want)
	}

	latest, err := LatestLogFile(logDir)
	if err != nil {
		t.Fatalf("LatestLogFile: %v", err)
	}
	if filepath.Base(latest) != "20260507-100001.jsonl" {
		t.Fatalf("latest = %s", latest)
	}

	emptyLatest, err := LatestLogFile(filepath.Join(logDir, "missing"))
	if err == nil {
		t.Fatalf("LatestLogFile on missing dir returned nil error and %q", emptyLatest)
	}
}

func TestFormatEntryHumanIncludesSortedFields(t *testing.T) {
	entry := LogEntry{
		Time:  "2026-05-07T10:11:12Z",
		Level: "WARN",
		Msg:   "apply delayed",
		Fields: map[string]any{
			"time":       "2026-05-07T10:11:12Z",
			"level":      "WARN",
			"msg":        "apply delayed",
			"elapsed_ms": float64(12),
			"service":    "base",
			"phase":      "apply",
		},
	}

	var buf bytes.Buffer
	FormatEntryHuman(&buf, entry)

	got := buf.String()
	if !strings.Contains(got, "10:11:12 W apply delayed") {
		t.Fatalf("formatted entry missing time/level/msg: %q", got)
	}
	if !strings.Contains(got, "phase=apply service=base") {
		t.Fatalf("formatted entry should include sorted detail fields: %q", got)
	}
}

func TestMaskSecretsMasksSensitiveKeys(t *testing.T) {
	masked := MaskSecrets(map[string]any{
		"apiKey":        "abc",
		"Authorization": "Bearer token",
		"password_hash": "hash",
		"service":       "base",
	})

	for _, key := range []string{"apiKey", "Authorization", "password_hash"} {
		if masked[key] != "***" {
			t.Fatalf("%s = %#v, want masked", key, masked[key])
		}
	}
	if masked["service"] != "base" {
		t.Fatalf("service = %#v, want unmasked", masked["service"])
	}
}

func TestRotateLogFilesKeepsNewestFiles(t *testing.T) {
	logDir := t.TempDir()
	for _, name := range []string{
		"20260507-100000.jsonl",
		"20260507-100001.jsonl",
		"20260507-100002.jsonl",
		"note.txt",
	} {
		if err := os.WriteFile(filepath.Join(logDir, name), []byte("{}\n"), 0600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	rotateLogFiles(logDir, 2)

	files, err := ListLogFiles(logDir)
	if err != nil {
		t.Fatalf("ListLogFiles: %v", err)
	}
	want := []string{"20260507-100001.jsonl", "20260507-100002.jsonl"}
	if strings.Join(files, ",") != strings.Join(want, ",") {
		t.Fatalf("files = %v, want %v", files, want)
	}
	if _, err := os.Stat(filepath.Join(logDir, "note.txt")); err != nil {
		t.Fatalf("non-jsonl file should remain: %v", err)
	}
}
