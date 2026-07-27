package backuplifecycle

import (
	"fmt"

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
