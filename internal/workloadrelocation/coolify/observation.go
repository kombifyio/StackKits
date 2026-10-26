package coolify

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

func (r *Runner) Observe(ctx context.Context, receipt []byte, phase, workspace string) (time.Time, error) {
	if phase == "sent" || phase == "prepared" {
		var source struct {
			ArchiveName   string `json:"archive_name"`
			ArchiveSHA256 string `json:"archive_sha256"`
		}
		if json.Unmarshal(receipt, &source) != nil || source.ArchiveName == "" || filepath.Base(source.ArchiveName) != source.ArchiveName {
			return time.Time{}, errors.New("source archive identity is unavailable")
		}
		f, err := os.Open(filepath.Join(DefaultBackupDir, source.ArchiveName))
		if err != nil {
			return time.Time{}, err
		}
		h := sha256.New()
		_, err = io.Copy(h, f)
		closeErr := f.Close()
		if err != nil {
			return time.Time{}, err
		}
		if closeErr != nil {
			return time.Time{}, closeErr
		}
		if fmt.Sprintf("sha256:%x", h.Sum(nil)) != source.ArchiveSHA256 {
			return time.Time{}, errors.New("source retained archive no longer matches the transferred bytes")
		}
	}
	dir, err := os.MkdirTemp("", "stackkit-relocation-observe-")
	if err != nil {
		return time.Time{}, err
	}
	defer os.RemoveAll(dir)
	file := filepath.Join(dir, "receipt.json")
	if err := os.WriteFile(file, receipt, 0o600); err != nil {
		return time.Time{}, err
	}
	args := []string{"relocation-status", "--receipt", file, "--phase", phase}
	if phase == "aborted" {
		args = append(args, "--intent", filepath.Join(workspace, ".stackkit", "relocation", "engine-receipts", sourceTransferID(receipt)+".json.intent"))
	}
	if phase == "resumed" {
		args = append(args, "--drain-record", DrainRecordPath(workspace, sourceTransferID(receipt)))
	}
	if phase == "sent" || phase == "prepared" {
		args = append(args, "--drain-record", DrainRecordPath(workspace, sourceTransferID(receipt)))
	}
	out, stderr, code, err := r.run(ctx, nil, args...)
	if err != nil {
		return time.Time{}, err
	}
	if code != 0 {
		return time.Time{}, engineFailure("relocation-status", code, stderr)
	}
	var result struct {
		Verified   bool      `json:"verified"`
		ObservedAt time.Time `json:"observed_at"`
	}
	if json.Unmarshal(out, &result) != nil || !result.Verified || result.ObservedAt.IsZero() {
		return time.Time{}, errors.New("relocation observation did not prove current runtime")
	}
	return result.ObservedAt, nil
}

func sourceTransferID(raw []byte) string {
	var v struct {
		TransferID string `json:"transfer_id"`
	}
	_ = json.Unmarshal(raw, &v)
	return v.TransferID
}
