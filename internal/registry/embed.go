package registry

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

// embeddedSnapshotJSON holds the baked-in registry snapshot. The file is
// refreshed at release time (see `stackkit registry snapshot`) or when the
// CUE tree changes (see `stackkit registry bake-from-cue`). The file MUST
// exist for the package to compile -- a minimal placeholder is checked in.
//
//go:embed data/registry_snapshot.json
var embeddedSnapshotJSON []byte

// EmbeddedSnapshot returns a freshly decoded copy of the baked-in
// snapshot. The returned value is safe to mutate by the caller.
func EmbeddedSnapshot() (Snapshot, error) {
	var snap Snapshot
	if err := json.Unmarshal(embeddedSnapshotJSON, &snap); err != nil {
		return Snapshot{}, fmt.Errorf("decode embedded registry snapshot: %w", err)
	}
	if snap.SchemaVersion != SnapshotVersion {
		return Snapshot{}, fmt.Errorf(
			"embedded registry snapshot schema version mismatch: got %d, expected %d -- rebuild with `stackkit registry snapshot` or `stackkit registry bake-from-cue`",
			snap.SchemaVersion, SnapshotVersion,
		)
	}
	return snap, nil
}

// EmbeddedSnapshotBytes returns the raw JSON bytes of the baked-in
// snapshot. Useful for `registry info` which prints them verbatim.
func EmbeddedSnapshotBytes() []byte {
	out := make([]byte, len(embeddedSnapshotJSON))
	copy(out, embeddedSnapshotJSON)
	return out
}
