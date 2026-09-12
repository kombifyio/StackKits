package commands

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/backupcustody"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/logging"
	"github.com/spf13/cobra"
)

const (
	supportBundleSchemaVersion = "stackkit.support-bundle/v1"
	maxSupportSourceBytes      = 1 << 20
)

type supportEvidenceFile struct {
	Ref     string            `json:"ref"`
	Records []json.RawMessage `json:"records"`
}

type supportNotice struct {
	Ref        string `json:"ref"`
	ReasonCode string `json:"reasonCode"`
	Message    string `json:"message"`
}

type supportReadResult struct {
	Records []json.RawMessage
	Found   bool
	Notices []supportNotice
}

type supportBundle struct {
	SchemaVersion string                `json:"schemaVersion"`
	RunID         string                `json:"runId"`
	Log           *supportEvidenceFile  `json:"log,omitempty"`
	Receipts      []supportEvidenceFile `json:"receipts"`
	Notices       []supportNotice       `json:"notices,omitempty"`
}

func newSupportCommand() *cobra.Command {
	var outputPath string
	command := &cobra.Command{
		Use:   "support",
		Short: "Export redacted local support evidence",
		Annotations: map[string]string{
			noDeployObservabilityAnnotation: "true",
		},
	}
	export := &cobra.Command{
		Use:   "export [run-id]",
		Short: "Export one local rollout log and its receipts",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			requested := "latest"
			if len(args) == 1 {
				requested = args[0]
			}
			result, err := buildSupportBundle(getWorkDir(), requested)
			if err != nil {
				return err
			}
			if strings.TrimSpace(outputPath) == "" {
				return errors.New("support export output path is required")
			}
			encoded, err := json.MarshalIndent(result, "", "  ")
			if err != nil {
				return fmt.Errorf("encode support bundle: %w", err)
			}
			encoded = append(encoded, '\n')
			if err := writeSupportBundle(outputPath, encoded); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Support evidence for %s written to %s\n", result.RunID, outputPath)
			return err
		},
	}
	export.Flags().StringVarP(&outputPath, "output", "o", "", "New local JSON file to create (required)")
	command.AddCommand(export)
	return command
}

func init() {
	rootCmd.AddCommand(newSupportCommand())
}

func buildSupportBundle(workspace, requested string) (supportBundle, error) {
	runID, err := resolveSupportRunID(workspace, requested)
	if err != nil {
		return supportBundle{}, err
	}
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return supportBundle{}, fmt.Errorf("open support evidence workspace: %w", err)
	}
	defer root.Close()
	view, err := root.View(".")
	if err != nil {
		return supportBundle{}, fmt.Errorf("open support evidence view: %w", err)
	}

	bundle := supportBundle{SchemaVersion: supportBundleSchemaVersion, RunID: runID, Receipts: []supportEvidenceFile{}}
	logRef := filepath.ToSlash(filepath.Join(".stackkit", "logs", runID+".jsonl"))
	if result, readErr := readSupportJSONL(view, logRef); readErr != nil {
		return supportBundle{}, readErr
	} else if result.Found {
		bundle.Log = &supportEvidenceFile{Ref: logRef, Records: result.Records}
		bundle.Notices = append(bundle.Notices, result.Notices...)
	}
	for _, name := range []string{"metadata.json", "events.jsonl", "summary.json"} {
		ref := filepath.ToSlash(filepath.Join(".stackkit", "runs", runID, name))
		result, readErr := readSupportEvidence(view, ref, strings.HasSuffix(name, ".jsonl"))
		if readErr != nil {
			return supportBundle{}, readErr
		}
		if result.Found {
			bundle.Receipts = append(bundle.Receipts, supportEvidenceFile{Ref: ref, Records: result.Records})
			bundle.Notices = append(bundle.Notices, result.Notices...)
		}
	}
	if bundle.Log == nil && len(bundle.Receipts) == 0 {
		return supportBundle{}, fmt.Errorf("no local log or run receipts found for %q", runID)
	}
	return bundle, nil
}

func resolveSupportRunID(workspace, requested string) (string, error) {
	runID := strings.TrimSuffix(strings.TrimSpace(requested), ".jsonl")
	if runID != "" && runID != "latest" {
		if !logging.IsValidRunID(runID) {
			return "", errors.New("support run ID must be an exact local rollout run ID")
		}
		return runID, nil
	}
	logFiles, err := logging.ListLogFiles(filepath.Join(workspace, ".stackkit", "logs"))
	if err != nil {
		return "", fmt.Errorf("list local support logs: %w", err)
	}
	latest := ""
	var latestAt time.Time
	consider := func(candidate string) {
		candidateAt, parseErr := supportRunTime(candidate)
		if parseErr == nil && (latest == "" || candidateAt.After(latestAt) || (candidateAt.Equal(latestAt) && candidate > latest)) {
			latest, latestAt = candidate, candidateAt
		}
	}
	for _, file := range logFiles {
		consider(strings.TrimSuffix(file, ".jsonl"))
	}
	runEntries, err := os.ReadDir(filepath.Join(workspace, ".stackkit", "runs"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("list local support receipts: %w", err)
	}
	for _, entry := range runEntries {
		if entry.IsDir() && logging.IsValidRunID(entry.Name()) {
			consider(entry.Name())
		}
	}
	if latest == "" {
		return "", errors.New("no local rollout logs or receipts found")
	}
	return latest, nil
}

func supportRunTime(runID string) (time.Time, error) {
	if !logging.IsValidRunID(runID) {
		return time.Time{}, errors.New("invalid support run ID")
	}
	layout, value := "20060102-150405", runID
	if len(runID) >= len("20060102-150405.000000000") && runID[len("20060102-150405")] == '.' {
		layout = "20060102-150405.000000000"
		value = runID[:len(layout)]
	}
	return time.ParseInLocation(layout, value, time.UTC)
}

func readSupportEvidence(view confinedfs.View, ref string, jsonLines bool) (supportReadResult, error) {
	if jsonLines {
		return readSupportJSONL(view, ref)
	}
	raw, found, oversized, err := readBoundedSupportFile(view, ref)
	if err != nil || !found {
		return supportReadResult{Found: found}, err
	}
	if oversized {
		return supportReadResult{Found: true, Records: []json.RawMessage{}, Notices: []supportNotice{{
			Ref: ref, ReasonCode: "source_too_large", Message: "JSON receipt omitted because it exceeds the local support export limit",
		}}}, nil
	}
	redacted, err := redactSupportJSON(raw)
	if err != nil {
		return supportReadResult{Found: true, Records: []json.RawMessage{}, Notices: []supportNotice{{
			Ref: ref, ReasonCode: "malformed_json", Message: "JSON receipt omitted because it is not a complete JSON value",
		}}}, nil
	}
	return supportReadResult{Found: true, Records: []json.RawMessage{redacted}}, nil
}

func readSupportJSONL(view confinedfs.View, ref string) (supportReadResult, error) {
	file, err := view.Open(ref)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return supportReadResult{}, nil
		}
		return supportReadResult{}, fmt.Errorf("open support evidence %s: %w", ref, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return supportReadResult{}, fmt.Errorf("inspect support evidence %s: %w", ref, err)
	}
	offset := int64(0)
	result := supportReadResult{Found: true, Records: []json.RawMessage{}}
	if info.Size() > maxSupportSourceBytes {
		offset = info.Size() - maxSupportSourceBytes
		result.Notices = append(result.Notices, supportNotice{
			Ref: ref, ReasonCode: "jsonl_tail_truncated", Message: "Earlier JSONL records were omitted to fit the local support export limit",
		})
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return supportReadResult{}, fmt.Errorf("seek support evidence %s: %w", ref, err)
	}
	raw, err := io.ReadAll(io.LimitReader(file, maxSupportSourceBytes))
	if err != nil {
		return supportReadResult{}, fmt.Errorf("read support evidence %s: %w", ref, err)
	}
	if offset > 0 {
		if boundary := bytes.IndexByte(raw, '\n'); boundary >= 0 {
			raw = raw[boundary+1:]
		} else {
			raw = nil
		}
	}
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 4096), maxSupportSourceBytes)
	unreadable := 0
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		redacted, decodeErr := redactSupportJSON(line)
		if decodeErr != nil {
			unreadable++
			continue
		}
		result.Records = append(result.Records, redacted)
	}
	if err := scanner.Err(); err != nil {
		unreadable++
	}
	if unreadable > 0 {
		result.Notices = append(result.Notices, supportNotice{
			Ref: ref, ReasonCode: "malformed_jsonl_record",
			Message: fmt.Sprintf("%d incomplete or unreadable JSONL record(s) were omitted", unreadable),
		})
	}
	return result, nil
}

func readBoundedSupportFile(view confinedfs.View, ref string) ([]byte, bool, bool, error) {
	file, err := view.Open(ref)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, false, nil
		}
		return nil, false, false, fmt.Errorf("open support evidence %s: %w", ref, err)
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, maxSupportSourceBytes+1))
	if err != nil {
		return nil, true, false, fmt.Errorf("read support evidence %s: %w", ref, err)
	}
	if len(raw) > maxSupportSourceBytes {
		return nil, true, true, nil
	}
	return raw, true, false, nil
}

func redactSupportJSON(raw []byte) (json.RawMessage, error) {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("multiple JSON values")
	}
	redacted, err := json.Marshal(logging.RedactValue(value))
	return json.RawMessage(redacted), err
}

func writeSupportBundle(outputPath string, data []byte) error {
	target, err := filepath.Abs(outputPath)
	if err != nil {
		return fmt.Errorf("resolve support output path: %w", err)
	}
	parentRoot, err := confinedfs.Open(filepath.Dir(target))
	if err != nil {
		return fmt.Errorf("open support output directory: %w", err)
	}
	defer parentRoot.Close()
	view, err := parentRoot.View(".")
	if err != nil {
		return fmt.Errorf("open support output view: %w", err)
	}
	result, err := view.WriteAtomic0600NoReplace(filepath.Base(target), data)
	if err != nil {
		return fmt.Errorf("write new support output: %w", err)
	}
	if !result.Installed || !result.FileSynced {
		return errors.New("support output was not securely installed")
	}
	if err := backupcustody.ProtectPrivatePath(target, false); err != nil {
		return fmt.Errorf("protect support output for the local owner: %w", err)
	}
	if err := parentRoot.VerifyPathIdentity(); err != nil {
		return fmt.Errorf("verify support output directory identity: %w", err)
	}
	return nil
}
