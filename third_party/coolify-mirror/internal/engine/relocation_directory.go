package engine

import (
	"os"
	"path/filepath"
)

// EnsureRelocationDirectory also persists newly created directory entries in
// their parents before a journal can authorize a runtime effect.
func EnsureRelocationDirectory(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	current, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	for {
		dir, err := os.Open(current)
		if err != nil {
			return err
		}
		err = dir.Sync()
		closeErr := dir.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		parent := filepath.Dir(current)
		if parent == current {
			return nil
		}
		current = parent
	}
}
