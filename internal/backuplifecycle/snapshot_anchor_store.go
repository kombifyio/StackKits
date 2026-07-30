package backuplifecycle

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/kombifyio/stackkits/internal/confinedfs"
)

// LoadSnapshotAnchor reads and verifies the exact persisted content-addressed
// anchor consumed by restore. It has no repository or restore side effects.
func LoadSnapshotAnchor(workspaceRoot, anchorID string) (SnapshotAnchor, error) {
	root, err := confinedfs.Open(workspaceRoot)
	if err != nil {
		return SnapshotAnchor{}, fmt.Errorf("backuplifecycle: open snapshot anchor workspace: %w", err)
	}
	absolute := root.Name()
	if err := root.Close(); err != nil {
		return SnapshotAnchor{}, fmt.Errorf("backuplifecycle: close snapshot anchor workspace: %w", err)
	}
	return (&Service{workspaceRoot: absolute}).loadStoredSnapshotAnchor(anchorID)
}

// SnapshotAnchorEvidenceRef returns the owner-custody path derived solely from
// a verified content address. Callers never supply or redirect the path.
func SnapshotAnchorEvidenceRef(anchorID string) (string, error) {
	if !validDigest(anchorID) {
		return "", errors.New("backuplifecycle: snapshot anchor identity is invalid")
	}
	return filepath.ToSlash(filepath.Join(
		anchorDirectory, strings.TrimPrefix(anchorID, "sha256:")+".json",
	)), nil
}

// RestoreResultEvidenceRef returns the canonical owner-custody path shared by
// CLI, MCP, and the State Console lifecycle projection.
func RestoreResultEvidenceRef(resultID string) (string, error) {
	if !validDigest(resultID) {
		return "", errors.New("backuplifecycle: restore result identity is invalid")
	}
	return filepath.ToSlash(filepath.Join(
		restoreResultDirectory, strings.TrimPrefix(resultID, "sha256:")+".json",
	)), nil
}
