package coolify

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
)

func (r *Runner) Abort(ctx context.Context, plan []byte, transferID, receiptPath string) (json.RawMessage, error) {
	d, err := os.MkdirTemp("", "stackkit-relocation-abort-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(d)
	p := filepath.Join(d, "plan.json")
	if err = os.WriteFile(p, plan, 0600); err != nil {
		return nil, err
	}
	out, stderr, code, err := r.run(ctx, nil, "relocation-abort", "--plan", p, "--transfer-id", transferID, "--receipt", receiptPath)
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return nil, engineFailure("relocation-abort", code, stderr)
	}
	return json.RawMessage(out), nil
}

func (r *Runner) Resume(ctx context.Context, plan []byte, transferID, record string) error {
	d, err := os.MkdirTemp("", "stackkit-relocation-resume-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(d)
	p := filepath.Join(d, "plan.json")
	if err = os.WriteFile(p, plan, 0600); err != nil {
		return err
	}
	_, stderr, code, err := r.run(ctx, nil, "relocation-resume", "--plan", p, "--transfer-id", transferID, "--record", record)
	if err != nil {
		return err
	}
	if code != 0 {
		return engineFailure("relocation-resume", code, stderr)
	}
	return nil
}
