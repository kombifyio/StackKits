package commands

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/actionableerror"
	"github.com/kombifyio/stackkits/internal/logging"
	"github.com/kombifyio/stackkits/internal/runtimeobservation"
	"github.com/spf13/cobra"
)

const (
	logListSchemaVersion    = "stackkit.log-list/v1"
	logRunSchemaVersion     = "stackkit.log-run/v1"
	logCursorSchemaVersion  = "stackkit.log-cursor/v1"
	defaultLogReadMaxEvents = 200
	defaultLogReadMaxBytes  = 256 << 10
	maxLogReadEvents        = 2000
	maxLogReadBytes         = 1 << 20
)

var canonicalLogRunID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

type structuredLogRunSummary struct {
	RunID         string                            `json:"runId"`
	EventCount    int                               `json:"eventCount"`
	SizeBytes     int64                             `json:"sizeBytes"`
	ModifiedAt    time.Time                         `json:"modifiedAt"`
	FirstEventAt  string                            `json:"firstEventAt,omitempty"`
	LastEventAt   string                            `json:"lastEventAt,omitempty"`
	HighestLevel  string                            `json:"highestLevel,omitempty"`
	EvidenceLinks []runtimeobservation.EvidenceLink `json:"evidenceLinks"`
}

type structuredLogList struct {
	SchemaVersion string                    `json:"schemaVersion"`
	LatestRunID   string                    `json:"latestRunId,omitempty"`
	Runs          []structuredLogRunSummary `json:"runs"`
}

type logReadOptions struct {
	Decisions bool
	Errors    bool
	Cursor    string
	MaxEvents int
	MaxBytes  int64
}

type structuredLogRun struct {
	SchemaVersion string                            `json:"schemaVersion"`
	RunID         string                            `json:"runId"`
	EventCount    int                               `json:"eventCount"`
	Events        []json.RawMessage                 `json:"events"`
	Cursor        string                            `json:"cursor"`
	NextCursor    string                            `json:"next_cursor,omitempty"`
	Truncated     bool                              `json:"truncated"`
	ScannedBytes  int64                             `json:"scannedBytes"`
	EvidenceLinks []runtimeobservation.EvidenceLink `json:"evidenceLinks"`
}

type structuredLogCursor struct {
	SchemaVersion string `json:"schemaVersion"`
	RunID         string `json:"runId"`
	Digest        string `json:"digest"`
	Offset        int64  `json:"offset"`
}

func buildStructuredLogList(logDir string) (structuredLogList, error) {
	files, err := logging.ListLogFiles(logDir)
	if err != nil {
		return structuredLogList{}, fmt.Errorf("list structured rollout logs: %w", err)
	}
	result := structuredLogList{SchemaVersion: logListSchemaVersion, Runs: make([]structuredLogRunSummary, 0, len(files))}
	for _, file := range files {
		runID := strings.TrimSuffix(file, ".jsonl")
		path := filepath.Join(logDir, file)
		entries, err := logging.ReadLogFile(path)
		if err != nil {
			return structuredLogList{}, fmt.Errorf("read structured rollout log %q: %w", runID, err)
		}
		info, err := os.Stat(path)
		if err != nil {
			return structuredLogList{}, fmt.Errorf("inspect structured rollout log %q: %w", runID, err)
		}
		digest, err := fileSHA256(path)
		if err != nil {
			return structuredLogList{}, err
		}
		summary := structuredLogRunSummary{
			RunID: runID, EventCount: len(entries), SizeBytes: info.Size(), ModifiedAt: info.ModTime().UTC(),
			HighestLevel:  highestLogLevel(entries),
			EvidenceLinks: []runtimeobservation.EvidenceLink{{Kind: "rollout-log", Ref: logEvidenceRef(runID), Digest: digest}},
		}
		if len(entries) > 0 {
			summary.FirstEventAt = entries[0].Time
			summary.LastEventAt = entries[len(entries)-1].Time
		}
		result.Runs = append(result.Runs, summary)
	}
	if len(result.Runs) > 0 {
		result.LatestRunID = result.Runs[len(result.Runs)-1].RunID
	}
	return result, nil
}

func readStructuredLogRun(logDir, requested string, options logReadOptions) (structuredLogRun, error) {
	options, err := normalizeLogReadOptions(options)
	if err != nil {
		return structuredLogRun{}, err
	}
	runID := strings.TrimSuffix(strings.TrimSpace(requested), ".jsonl")
	if runID == "latest" || runID == "" {
		latest, err := logging.LatestLogFile(logDir)
		if err != nil {
			return structuredLogRun{}, fmt.Errorf("resolve latest rollout log: %w", err)
		}
		runID = strings.TrimSuffix(filepath.Base(latest), ".jsonl")
	}
	if !canonicalLogRunID.MatchString(runID) || runID == "." || runID == ".." || !logging.IsValidRunID(runID) {
		return structuredLogRun{}, errors.New("log run ID must be the exact basename returned by logs list")
	}
	cleanLogDir := filepath.Clean(logDir)
	path := filepath.Join(cleanLogDir, runID+".jsonl")
	if filepath.Dir(path) != cleanLogDir {
		return structuredLogRun{}, errors.New("log run ID escaped the local log directory")
	}
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return structuredLogRun{}, fmt.Errorf("rollout log %q was not found", runID)
		}
		return structuredLogRun{}, fmt.Errorf("read rollout log %q: %w", runID, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return structuredLogRun{}, fmt.Errorf("inspect rollout log %q: %w", runID, err)
	}
	digest, err := readerSHA256(io.NewSectionReader(file, 0, info.Size()))
	if err != nil {
		return structuredLogRun{}, fmt.Errorf("content-address rollout log: %w", err)
	}
	startOffset := int64(0)
	if strings.TrimSpace(options.Cursor) != "" {
		cursor, err := decodeStructuredLogCursor(options.Cursor)
		if err != nil {
			return structuredLogRun{}, err
		}
		if cursor.RunID != runID {
			return structuredLogRun{}, errors.New("log cursor runId does not match the requested run")
		}
		if cursor.Digest != digest {
			return structuredLogRun{}, errors.New("log cursor digest no longer matches the requested run")
		}
		if cursor.Offset < 0 || cursor.Offset > info.Size() {
			return structuredLogRun{}, errors.New("log cursor offset is outside the digest-bound run")
		}
		if cursor.Offset > 0 && cursor.Offset < info.Size() {
			var prior [1]byte
			if _, err := file.ReadAt(prior[:], cursor.Offset-1); err != nil || prior[0] != '\n' {
				return structuredLogRun{}, errors.New("log cursor offset is not an event boundary")
			}
		}
		startOffset = cursor.Offset
	}
	currentCursor, err := encodeStructuredLogCursor(structuredLogCursor{
		SchemaVersion: logCursorSchemaVersion, RunID: runID, Digest: digest, Offset: startOffset,
	})
	if err != nil {
		return structuredLogRun{}, err
	}
	if _, err := file.Seek(startOffset, io.SeekStart); err != nil {
		return structuredLogRun{}, fmt.Errorf("seek digest-bound rollout log cursor: %w", err)
	}
	reader := bufio.NewReaderSize(io.LimitReader(file, info.Size()-startOffset), 64<<10)
	events := make([]json.RawMessage, 0, options.MaxEvents)
	position := startOffset
	var scannedBytes int64
	for position < info.Size() && len(events) < options.MaxEvents && scannedBytes < options.MaxBytes {
		lineOffset := position
		line, consumed, readErr := readBoundedJSONLLine(reader, maxLogReadBytes)
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return structuredLogRun{}, fmt.Errorf("read bounded rollout event at byte %d: %w", lineOffset, readErr)
		}
		if consumed == 0 && errors.Is(readErr, io.EOF) {
			break
		}
		if scannedBytes+int64(consumed) > options.MaxBytes {
			if scannedBytes == 0 {
				return structuredLogRun{}, fmt.Errorf("rollout event at byte %d exceeds maxBytes=%d", lineOffset, options.MaxBytes)
			}
			break
		}
		position += int64(consumed)
		scannedBytes += int64(consumed)
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		entry, ok := logging.ParseLogLine(line)
		if !ok || !logEntrySelected(entry, options) {
			continue
		}
		events = append(events, append(json.RawMessage(nil), entry.RawJSON...))
	}
	truncated := position < info.Size()
	nextCursor := ""
	if truncated {
		nextCursor, err = encodeStructuredLogCursor(structuredLogCursor{
			SchemaVersion: logCursorSchemaVersion, RunID: runID, Digest: digest, Offset: position,
		})
		if err != nil {
			return structuredLogRun{}, err
		}
	}
	after, err := file.Stat()
	if err != nil || after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) {
		return structuredLogRun{}, errors.New("rollout log changed while the digest-bound page was read; restart without a cursor")
	}
	return structuredLogRun{
		SchemaVersion: logRunSchemaVersion, RunID: runID, EventCount: len(events), Events: events,
		Cursor: currentCursor, NextCursor: nextCursor, Truncated: truncated, ScannedBytes: scannedBytes,
		EvidenceLinks: []runtimeobservation.EvidenceLink{{Kind: "rollout-log", Ref: logEvidenceRef(runID), Digest: digest}},
	}, nil
}

func normalizeLogReadOptions(options logReadOptions) (logReadOptions, error) {
	if options.MaxEvents == 0 {
		options.MaxEvents = defaultLogReadMaxEvents
	}
	if options.MaxBytes == 0 {
		options.MaxBytes = defaultLogReadMaxBytes
	}
	if options.MaxEvents < 1 || options.MaxEvents > maxLogReadEvents {
		return logReadOptions{}, fmt.Errorf("maxEvents must be between 1 and %d", maxLogReadEvents)
	}
	if options.MaxBytes < 1 || options.MaxBytes > maxLogReadBytes {
		return logReadOptions{}, fmt.Errorf("maxBytes must be between 1 and %d", maxLogReadBytes)
	}
	return options, nil
}

func logEntrySelected(entry logging.LogEntry, options logReadOptions) bool {
	if options.Decisions {
		return len(filterByPrefix([]logging.LogEntry{entry}, "decision.", "spec.loaded", "init.choices", "init.spec_created")) == 1
	}
	if options.Errors {
		return entry.Level == "ERROR" || entry.Level == "WARN"
	}
	return true
}

func readBoundedJSONLLine(reader *bufio.Reader, maxBytes int64) ([]byte, int, error) {
	line := make([]byte, 0, 4096)
	consumed := 0
	for {
		fragment, err := reader.ReadSlice('\n')
		consumed += len(fragment)
		if int64(len(line)+len(fragment)) > maxBytes {
			return nil, consumed, fmt.Errorf("single rollout event exceeds %d bytes", maxBytes)
		}
		line = append(line, fragment...)
		switch {
		case err == nil:
			return line, consumed, nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF):
			return line, consumed, io.EOF
		default:
			return nil, consumed, err
		}
	}
}

func encodeStructuredLogCursor(cursor structuredLogCursor) (string, error) {
	raw, err := json.Marshal(cursor)
	if err != nil {
		return "", fmt.Errorf("encode digest-bound log cursor: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func decodeStructuredLogCursor(value string) (structuredLogCursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil {
		return structuredLogCursor{}, errors.New("log cursor is not valid base64url")
	}
	var cursor structuredLogCursor
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cursor); err != nil {
		return structuredLogCursor{}, fmt.Errorf("decode digest-bound log cursor: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return structuredLogCursor{}, errors.New("log cursor contains multiple JSON values")
	}
	if cursor.SchemaVersion != logCursorSchemaVersion || cursor.RunID == "" || cursor.Digest == "" {
		return structuredLogCursor{}, errors.New("log cursor has an unsupported or incomplete contract")
	}
	return cursor, nil
}

func actionableErrorFor(reason string, err error, guidance ...string) actionableerror.Contract {
	message := "operation failed"
	if err != nil {
		message = logging.RedactText(err.Error())
	}
	return actionableerror.New("stackkit_command_failed", reason, message, guidance, false)
}

func writeActionableCommandFailure(cmd *cobra.Command, reason string, err error, guidance ...string) error {
	status := machineCommandFailureStatus(err)
	if writeErr := writeCommandResultStatus(cmd, cmd.CommandPath(), status, actionableErrorFor(reason, err, guidance...)); writeErr != nil {
		return errors.Join(err, writeErr)
	}
	return err
}

func logEvidenceRef(runID string) string {
	return filepath.ToSlash(filepath.Join(".stackkit", "logs", runID+".jsonl"))
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("content-address rollout log: %w", err)
	}
	defer file.Close()
	digest, err := readerSHA256(file)
	if err != nil {
		return "", fmt.Errorf("content-address rollout log: %w", err)
	}
	return digest, nil
}

func readerSHA256(reader io.Reader) (string, error) {
	digest := sha256.New()
	if _, err := io.Copy(digest, reader); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(digest.Sum(nil)), nil
}

func highestLogLevel(entries []logging.LogEntry) string {
	rank := map[string]int{"DEBUG": 1, "INFO": 2, "WARN": 3, "ERROR": 4}
	highest := ""
	for _, entry := range entries {
		if rank[entry.Level] > rank[highest] {
			highest = entry.Level
		}
	}
	return highest
}
