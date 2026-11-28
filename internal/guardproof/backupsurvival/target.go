package backupsurvival

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// RestoreTarget is the isolated, initially empty place a clean-room restore
// writes to. It never shares a path with the backup source.
type RestoreTarget interface {
	// Path is the absolute directory, reported in the receipt.
	Path() string
	// Write stores one restored file and returns the SHA-256 of what was
	// actually written, read back from the target.
	Write(name string, data []byte) (string, error)
}

// DirTarget is a RestoreTarget on a local directory.
type DirTarget struct {
	path string
	root *os.Root
}

// NewDirTarget requires an absolute path that is absent or an empty real
// directory, so a restore can neither overwrite live data nor hide behind a
// symbolic link.
func NewDirTarget(path string) (*DirTarget, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, errors.New("backupsurvival: restore target must be a clean absolute path")
	}
	info, err := os.Lstat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if err := os.Mkdir(path, 0o700); err != nil {
			return nil, fmt.Errorf("backupsurvival: create restore target: %w", err)
		}
	case err != nil:
		return nil, fmt.Errorf("backupsurvival: inspect restore target: %w", err)
	case !info.IsDir():
		return nil, errors.New("backupsurvival: restore target must be a real directory")
	default:
		entries, err := os.ReadDir(path)
		if err != nil {
			return nil, fmt.Errorf("backupsurvival: read restore target: %w", err)
		}
		if len(entries) != 0 {
			return nil, errors.New("backupsurvival: restore target must be empty")
		}
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, fmt.Errorf("backupsurvival: open restore target: %w", err)
	}
	return &DirTarget{path: path, root: root}, nil
}

// Path implements RestoreTarget.
func (t *DirTarget) Path() string { return t.path }

// Close releases the directory handle.
func (t *DirTarget) Close() error { return t.root.Close() }

// Write implements RestoreTarget. The name must be a single path element.
func (t *DirTarget) Write(name string, data []byte) (string, error) {
	if name == "" || name != filepath.Base(name) || name == "." || name == ".." {
		return "", errors.New("backupsurvival: restore file name must be one path element")
	}
	file, err := t.root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", fmt.Errorf("backupsurvival: create restored file: %w", err)
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return "", fmt.Errorf("backupsurvival: write restored file: %w", err)
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("backupsurvival: close restored file: %w", err)
	}
	back, err := t.root.Open(name)
	if err != nil {
		return "", fmt.Errorf("backupsurvival: reopen restored file: %w", err)
	}
	defer back.Close()
	h := sha256.New()
	if _, err := io.Copy(h, back); err != nil {
		return "", fmt.Errorf("backupsurvival: read back restored file: %w", err)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}
