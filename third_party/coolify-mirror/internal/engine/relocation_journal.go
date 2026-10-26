package engine

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type RelocationIntent struct {
	TransferID           string `json:"transfer_id"`
	PlanSHA256           string `json:"plan_sha256"`
	State                string `json:"state"`
	NoDestinationEffects bool   `json:"no_destination_effects"`
}

func RelocationIntentPath(receipt string) string { return receipt + ".intent" }

// Caller holds Lock, shared with every restore Apply. An absent journal is safe
// only because every checked Apply must durably claim effects_started first.
func RecordRelocationIntent(path, transfer string, plan []byte, state string) (RelocationIntent, error) {
	v := RelocationIntent{TransferID: transfer, PlanSHA256: fmt.Sprintf("sha256:%x", sha256.Sum256(plan)), State: state, NoDestinationEffects: state == "aborted"}
	if transfer == "" || path == "" {
		return v, errors.New("relocation intent requires exact identity")
	}
	prior, err := os.ReadFile(path)
	if err == nil {
		var old RelocationIntent
		if json.Unmarshal(prior, &old) != nil || old.TransferID != v.TransferID || old.PlanSHA256 != v.PlanSHA256 {
			return v, errors.New("relocation journal identity differs")
		}
		if old.State == "aborted" && state == "aborted" {
			return old, nil
		}
		return v, errors.New("relocation already aborted or destination effects may have begun; retain source fence")
	}
	if !errors.Is(err, os.ErrNotExist) {
		return v, err
	}
	if err = EnsureRelocationDirectory(filepath.Dir(path)); err != nil {
		return v, err
	}
	raw, _ := json.Marshal(v)
	f, err := os.CreateTemp(filepath.Dir(path), ".relocation-intent-*")
	if err != nil {
		return v, err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(raw); err == nil {
		err = f.Sync()
	}
	cerr := f.Close()
	if err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Link(f.Name(), path)
	}
	if err != nil {
		return v, err
	}
	d, err := os.Open(filepath.Dir(path))
	if err != nil {
		return v, err
	}
	defer d.Close()
	return v, d.Sync()
}
func VerifyRelocationAborted(path, transfer string, plan []byte) (RelocationIntent, error) {
	v := RelocationIntent{}
	raw, err := os.ReadFile(path)
	if err != nil {
		return v, err
	}
	if json.Unmarshal(raw, &v) != nil || v.TransferID != transfer || v.PlanSHA256 != fmt.Sprintf("sha256:%x", sha256.Sum256(plan)) || v.State != "aborted" || !v.NoDestinationEffects {
		return v, errors.New("no durable no-effects destination abort")
	}
	return v, nil
}
