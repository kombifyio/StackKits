package commands

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveSnapshotDir_ExplicitFlagAsBasename(t *testing.T) {
	wd := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(wd, ".stackkit", "snapshots", "20260508T120000Z-1.0.0"), 0o755))

	dir, err := resolveSnapshotDir(wd, "20260508T120000Z-1.0.0", "")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(wd, ".stackkit", "snapshots", "20260508T120000Z-1.0.0"), dir)
}

func TestResolveSnapshotDir_ExplicitFlagAsAbsolutePath(t *testing.T) {
	wd := t.TempDir()
	abs := filepath.Join(wd, "alt", "snapshot")
	require.NoError(t, os.MkdirAll(abs, 0o755))

	dir, err := resolveSnapshotDir(wd, abs, "")
	require.NoError(t, err)
	assert.Equal(t, abs, dir)
}

func TestResolveSnapshotDir_FallsBackOnLastFromState(t *testing.T) {
	wd := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(wd, ".stackkit", "snapshots", "from-state"), 0o755))

	dir, err := resolveSnapshotDir(wd, "", "from-state")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(wd, ".stackkit", "snapshots", "from-state"), dir)
}

func TestResolveSnapshotDir_FallsBackOnNewestEntry(t *testing.T) {
	wd := t.TempDir()
	root := filepath.Join(wd, ".stackkit", "snapshots")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "20260101T000000Z-0.9.0"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "20260508T120000Z-1.0.0"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "20260301T120000Z-0.9.5"), 0o755))

	dir, err := resolveSnapshotDir(wd, "", "")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(root, "20260508T120000Z-1.0.0"), dir,
		"newest entry by lexicographic sort should win")
}

func TestResolveSnapshotDir_NoSnapshotsErrors(t *testing.T) {
	wd := t.TempDir()
	_, err := resolveSnapshotDir(wd, "", "")
	require.Error(t, err)
}

func TestResolveSnapshotDir_EmptySnapshotsDirErrors(t *testing.T) {
	wd := t.TempDir()
	root := filepath.Join(wd, ".stackkit", "snapshots")
	require.NoError(t, os.MkdirAll(root, 0o755))

	_, err := resolveSnapshotDir(wd, "", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no snapshots in")
}

func TestAtomicCopy_OverwritesDestinationIntact(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.tfstate")
	dst := filepath.Join(dir, "dst.tfstate")

	require.NoError(t, os.WriteFile(src, []byte("source-content"), 0o600))
	require.NoError(t, os.WriteFile(dst, []byte("old-destination"), 0o600))

	require.NoError(t, atomicCopy(src, dst))

	// Source preserved (rollback anchor stays usable).
	srcBytes, err := os.ReadFile(src)
	require.NoError(t, err)
	assert.Equal(t, "source-content", string(srcBytes))

	// Destination now matches source.
	dstBytes, err := os.ReadFile(dst)
	require.NoError(t, err)
	assert.Equal(t, "source-content", string(dstBytes))

	// Tmp file is gone — atomic semantics mean either rename succeeded
	// (no .tmp) or we cleaned up on failure.
	matches, _ := filepath.Glob(filepath.Join(dir, "*.rollback.tmp"))
	assert.Empty(t, matches)
}

func TestAtomicCopy_MissingSourceErrors(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "dst.tfstate")
	err := atomicCopy(filepath.Join(dir, "does-not-exist.tfstate"), dst)
	require.Error(t, err)
}

func TestRestoreTfstate_CreatesParentDirs(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.tfstate")
	require.NoError(t, os.WriteFile(src, []byte(`{"version":4}`), 0o600))

	dst := filepath.Join(dir, "deeply", "nested", "deploy", "terraform.tfstate")
	require.NoError(t, restoreTfstate(src, dst))

	got, err := os.ReadFile(dst)
	require.NoError(t, err)
	assert.Equal(t, `{"version":4}`, string(got))
}

func TestRestoreTfstate_MissingSourceErrors(t *testing.T) {
	dir := t.TempDir()
	err := restoreTfstate(filepath.Join(dir, "ghost"), filepath.Join(dir, "x.tfstate"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "snapshot tfstate")
}
