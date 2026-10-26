package main

import (
	"encoding/json"
	"github.com/mtalavi/coolify-mirror/internal/engine"
	"os"
	"path/filepath"
)

// The initial record must never replace a preceding attempt's original
// policies. No source mutation starts before its bytes have reached disk.
func writeDrainIntent(path string, value any) error {
	return writeDrainRecord(path, value, true)
}

func writeDrainRecord(path string, value any, exclusive bool) error {
	if err := engine.EnsureRelocationDirectory(filepath.Dir(path)); err != nil {
		return err
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".drain-intent-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(raw); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if exclusive {
		err = os.Link(f.Name(), path)
	} else {
		err = os.Rename(f.Name(), path)
	}
	if err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
