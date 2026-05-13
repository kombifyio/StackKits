package snapshot

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeKopia satisfies kopiaIface for tests without touching a real
// kopia binary. Each method returns canned responses or canned errors
// driven by the public fields.
type fakeKopia struct {
	statusErr     error
	statusCfg     bool
	createCalls   int
	createErr     error
	createdReturn []CreatedSnapshot
}

func (f *fakeKopia) Status(_ context.Context) (StatusResult, error) {
	if f.statusErr != nil {
		return StatusResult{}, f.statusErr
	}
	return StatusResult{Configured: f.statusCfg}, nil
}

func (f *fakeKopia) SnapshotCreate(_ context.Context, specs []CreateSpec) ([]CreatedSnapshot, error) {
	f.createCalls++
	if f.createErr != nil {
		return nil, f.createErr
	}
	if f.createdReturn != nil {
		return f.createdReturn, nil
	}
	out := make([]CreatedSnapshot, len(specs))
	for i, s := range specs {
		out[i] = CreatedSnapshot{
			ID:   "fake-id-" + s.Path,
			Path: s.Path,
		}
	}
	return out, nil
}

func newSnapshotter(t *testing.T, k kopiaIface) (*AtomicSnapshotter, string) {
	t.Helper()
	tmp := t.TempDir()
	return &AtomicSnapshotter{
		Kopia:        k,
		SnapshotsDir: filepath.Join(tmp, "snapshots"),
		NodeName:     "test-node-01",
		Now:          func() time.Time { return time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC) },
	}, tmp
}

// helper that writes a stub tfstate file the snapshotter expects
func writeFakeTfstate(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "terraform.tfstate")
	require.NoError(t, os.WriteFile(path, []byte(`{"version":4}`), 0o600))
	return path
}

func TestCreateBundle_HappyPath(t *testing.T) {
	a, tmp := newSnapshotter(t, &fakeKopia{statusCfg: true})
	tfsrc := writeFakeTfstate(t, tmp)

	dir, manifest, err := a.CreateBundle(context.Background(), BundleOptions{
		OldKitVersion: "1.0.0",
		NewKitVersion: "1.1.0",
		VolumePaths:   []string{"/var/lib/postgres", "/var/lib/vaultwarden"},
		TofuStateSrc:  tfsrc,
		ChannelMap: []ChannelMapEntry{
			{ModuleSlug: "traefik", ModuleVersion: "3.2.0", Channel: "stable", Reason: "matched"},
		},
	})

	require.NoError(t, err)
	assert.DirExists(t, dir)
	assert.NotNil(t, manifest)
	assert.Equal(t, ManifestVersion, manifest.Schema)
	assert.Equal(t, "1.0.0", manifest.OldKitVersion)
	assert.Equal(t, "1.1.0", manifest.NewKitVersion)
	assert.Equal(t, "test-node-01", manifest.NodeName)
	assert.Len(t, manifest.KopiaSnapshots, 2)
	assert.Equal(t, "/var/lib/postgres", manifest.KopiaSnapshots[0].Path)
	assert.Equal(t, "fake-id-/var/lib/postgres", manifest.KopiaSnapshots[0].SnapshotID)
	assert.FileExists(t, manifest.TofuStatePath)
	assert.FileExists(t, filepath.Join(dir, "manifest.yaml"))

	// Round-trip via LoadManifest.
	loaded, err := LoadManifest(dir)
	require.NoError(t, err)
	assert.Equal(t, manifest.OldKitVersion, loaded.OldKitVersion)
	assert.Equal(t, manifest.KopiaSnapshots, loaded.KopiaSnapshots)
	assert.Equal(t, manifest.ChannelMap, loaded.ChannelMap)
}

func TestCreateBundle_FailsWhenKopiaUnconfigured(t *testing.T) {
	a, tmp := newSnapshotter(t, &fakeKopia{statusCfg: false})
	tfsrc := writeFakeTfstate(t, tmp)

	_, _, err := a.CreateBundle(context.Background(), BundleOptions{
		OldKitVersion: "1.0.0",
		TofuStateSrc:  tfsrc,
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrKopiaNotConfigured), "expected ErrKopiaNotConfigured, got %v", err)
}

func TestCreateBundle_FailsWhenTfstateMissing(t *testing.T) {
	a, _ := newSnapshotter(t, &fakeKopia{statusCfg: true})

	_, _, err := a.CreateBundle(context.Background(), BundleOptions{
		OldKitVersion: "1.0.0",
		TofuStateSrc:  "/this/does/not/exist/terraform.tfstate",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tofu state")
}

func TestCreateBundle_FailsWhenKopiaErrors(t *testing.T) {
	a, tmp := newSnapshotter(t, &fakeKopia{
		statusCfg: true,
		createErr: errors.New("kopia died"),
	})
	tfsrc := writeFakeTfstate(t, tmp)

	_, _, err := a.CreateBundle(context.Background(), BundleOptions{
		OldKitVersion: "1.0.0",
		VolumePaths:   []string{"/var/lib/postgres"},
		TofuStateSrc:  tfsrc,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "step 1 kopia")
}

func TestCreateBundle_NilKopiaIsErrorNotPanic(t *testing.T) {
	a := &AtomicSnapshotter{
		Kopia:        nil,
		SnapshotsDir: t.TempDir(),
		NodeName:     "n",
	}
	_, _, err := a.CreateBundle(context.Background(), BundleOptions{
		OldKitVersion: "1.0.0",
		TofuStateSrc:  "/tmp/whatever",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Kopia is nil")
}

func TestVerify_HappyRoundtrip(t *testing.T) {
	a, tmp := newSnapshotter(t, &fakeKopia{statusCfg: true})
	tfsrc := writeFakeTfstate(t, tmp)

	dir, _, err := a.CreateBundle(context.Background(), BundleOptions{
		OldKitVersion: "1.0.0",
		VolumePaths:   []string{"/var/lib/postgres"},
		TofuStateSrc:  tfsrc,
	})
	require.NoError(t, err)
	require.NoError(t, Verify(dir))
}

func TestVerify_DetectsDeletedTfstate(t *testing.T) {
	a, tmp := newSnapshotter(t, &fakeKopia{statusCfg: true})
	tfsrc := writeFakeTfstate(t, tmp)

	dir, manifest, err := a.CreateBundle(context.Background(), BundleOptions{
		OldKitVersion: "1.0.0",
		VolumePaths:   []string{"/var/lib/postgres"},
		TofuStateSrc:  tfsrc,
	})
	require.NoError(t, err)

	// Operator deletes the tfstate copy out from under us.
	require.NoError(t, os.Remove(manifest.TofuStatePath))

	err = Verify(dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tfstate copy missing")
}

func TestSanitizeForPath_NoSlashesOrSpaces(t *testing.T) {
	assert.Equal(t, "1.0.0", sanitizeForPath("1.0.0"))
	assert.Equal(t, "1.0.0_beta", sanitizeForPath("1.0.0 beta"))
	assert.Equal(t, "feat_x_y", sanitizeForPath("feat/x/y"))
	assert.Equal(t, "win_path", sanitizeForPath(`win\path`))
	assert.Equal(t, "v1_2_3", sanitizeForPath("v1:2:3"))
}
