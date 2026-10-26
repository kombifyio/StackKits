package coolify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const (
	drainFormat   = "kombify.io/coolify-relocation-drain/v1"
	undrainFormat = "kombify.io/coolify-relocation-undrain/v1"
)

// DrainRecordPath is where the source keeps its drain record between drain,
// send and the end of the move: the workspace's relocation state.
func DrainRecordPath(workspace, transferID string) string {
	if transferID == "" {
		return filepath.Join(workspace, ".stackkit", "relocation", "coolify-drain.json")
	}
	return filepath.Join(workspace, ".stackkit", "relocation", "drains", transferID+".json")
}

// Drain stops the applications and services of plan on this (source)
// server; databases keep running for a consistent final backup. The record
// is written to recordPath even for a partial drain, so Undrain can restart
// what was stopped. It returns the engine's drain record unchanged.
func (r *Runner) Drain(ctx context.Context, plan []byte, recordPath, transferID string) (json.RawMessage, error) {
	var head struct {
		Format string `json:"format"`
	}
	if err := json.Unmarshal(plan, &head); err != nil || head.Format != planFormat {
		return nil, fmt.Errorf("not a Coolify relocation plan (%s); make it with `stackkit relocate plan` on this server", planFormat)
	}
	if transferID != "" {
		legacy := filepath.Join(filepath.Dir(filepath.Dir(recordPath)), "coolify-drain.json")
		if _, err := os.Stat(legacy); err == nil {
			return nil, errors.New("a legacy standalone drain is retained; resolve it with relocate drain --undo --owner-approve before starting a managed move")
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	if _, err := os.Stat(recordPath); err == nil {
		return nil, fmt.Errorf("a drain is already active on this server (%s); finish it or run `stackkit relocate drain --undo` first", recordPath)
	}
	if err := os.MkdirAll(filepath.Dir(recordPath), 0o700); err != nil {
		return nil, err
	}
	planFile, err := os.CreateTemp(filepath.Dir(recordPath), "plan-*.json")
	if err != nil {
		return nil, err
	}
	defer os.Remove(planFile.Name())
	if _, err := planFile.Write(plan); err != nil {
		planFile.Close()
		return nil, err
	}
	if err := planFile.Close(); err != nil {
		return nil, err
	}
	out, stderr, code, err := r.run(ctx, nil, "drain", "--json", "--plan", planFile.Name(), "--output", recordPath, "--transfer-id", transferID)
	if err != nil {
		return nil, err
	}
	if code != 0 {
		if _, statErr := os.Stat(recordPath); statErr == nil {
			return nil, fmt.Errorf("%w; the drain record stays at %s for `stackkit relocate drain --undo`", engineFailure("drain", code, stderr), recordPath)
		}
		return nil, engineFailure("drain", code, stderr)
	}
	if err := expectFormat(out, drainFormat); err != nil {
		return nil, fmt.Errorf("coolify-mirror drain: %w", err)
	}
	return json.RawMessage(out), nil
}

// Undrain starts again on this server what the drain at recordPath stopped
// and removes the record once everything runs.
func (r *Runner) Undrain(ctx context.Context, recordPath, transferID string) (json.RawMessage, error) {
	if _, err := os.Stat(recordPath); errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("no drain is active on this server")
	}
	if transferID != "" {
		raw, err := os.ReadFile(recordPath)
		var record struct {
			TransferID string `json:"transfer_id"`
		}
		if err != nil || json.Unmarshal(raw, &record) != nil || record.TransferID != transferID {
			return nil, errors.New("source drain belongs to another relocation")
		}
	}
	out, stderr, code, err := r.run(ctx, nil, "undrain", "--json", "--record", recordPath)
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return nil, engineFailure("undrain", code, stderr)
	}
	if err := expectFormat(out, undrainFormat); err != nil {
		return nil, fmt.Errorf("coolify-mirror undrain: %w", err)
	}
	// Keep the transfer-keyed original policy journal for signed resumed readback.
	if transferID == "" {
		if err := os.Remove(recordPath); err != nil {
			return nil, err
		}
	}
	return json.RawMessage(out), nil
}

func expectFormat(doc []byte, format string) error {
	var head struct {
		Format string `json:"format"`
	}
	if err := json.Unmarshal(doc, &head); err != nil || head.Format != format {
		return fmt.Errorf("returned no %s document", format)
	}
	return nil
}
