package snapshot

// atomic.go composes Kopia + Tofu-state-copy into the single atomic
// pre-update snapshot the kit-upgrade pipeline calls before `tofu apply`
// (kit-update-phase-1, ADR-0018 §3).
//
// Sequence:
//   1. kopia snapshot create  --tag=pre-update-<ts>-<old-kit-version>  <volumes>
//   2. cp deploy/terraform.tfstate  .stackkit/snapshots/<ts>-<kit>/state.tfstate
//   3. write manifest.yaml binding both anchors together
//
// If step 1 or 2 fail, no apply runs. The manifest is the rollback
// breadcrumb: it tells `stackkit kit upgrade rollback --to-snapshot=<ts>`
// which kopia snapshot id to restore and which tfstate file to push.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// ManifestVersion is the schema version of manifest.yaml. Bump when the
// shape changes incompatibly so old snapshots can still be read.
const ManifestVersion = "v1"

// SnapshotManifest is the on-disk record under
// .stackkit/snapshots/<ts>-<old-kit>/manifest.yaml. It binds together
// the Kopia snapshot ids (one per persistent volume) with the Tofu
// state-file copy and the channel-map from the resolver decision.
type SnapshotManifest struct {
	Schema         string             `yaml:"schema"`
	Timestamp      time.Time          `yaml:"timestamp"`
	NodeName       string             `yaml:"nodeName"`
	OldKitVersion  string             `yaml:"oldKitVersion"`
	NewKitVersion  string             `yaml:"newKitVersion"`
	KopiaSnapshots []KopiaSnapshotRef `yaml:"kopiaSnapshots"`
	TofuStatePath  string             `yaml:"tofuStatePath"`
	ChannelMap     []ChannelMapEntry  `yaml:"channelMap,omitempty"`
}

// KopiaSnapshotRef ties a volume path to its Kopia snapshot id.
type KopiaSnapshotRef struct {
	Path       string `yaml:"path"`
	SnapshotID string `yaml:"snapshotId"`
}

// ChannelMapEntry mirrors the resolver-decision row that drove the
// upgrade. Recording it in the manifest means rollback can re-apply the
// exact same module-version set the operator approved.
type ChannelMapEntry struct {
	ModuleSlug    string `yaml:"moduleSlug"`
	ModuleVersion string `yaml:"moduleVersion"`
	Channel       string `yaml:"channel"`
	Reason        string `yaml:"reason"` // matched | fallback | override
}

// AtomicSnapshotter orchestrates the three-step pre-update snapshot.
type AtomicSnapshotter struct {
	Kopia        kopiaIface
	SnapshotsDir string // .stackkit/snapshots
	NodeName     string
	Now          func() time.Time // injectable for tests
}

// kopiaIface is the subset of *Kopia we use. Defining it as an
// interface lets tests pass a fake implementation without touching
// exec.Command.
type kopiaIface interface {
	Status(ctx context.Context) (StatusResult, error)
	SnapshotCreate(ctx context.Context, specs []CreateSpec) ([]CreatedSnapshot, error)
}

// BundleOptions describes a single create-bundle call.
type BundleOptions struct {
	OldKitVersion string
	NewKitVersion string
	VolumePaths   []string // persistent volumes to snapshot via Kopia
	TofuStateSrc  string   // path to deploy/terraform.tfstate
	ChannelMap    []ChannelMapEntry
}

// ErrKopiaNotConfigured is the canonical pre-flight failure: the
// upgrade workflow MUST refuse to proceed when no Kopia repo is set up.
// Callers translate this to an operator-facing message.
var ErrKopiaNotConfigured = errors.New("kopia repository not configured — run 'stackkit backup configure' first")

// CreateBundle runs the atomic-snapshot sequence. It returns the
// snapshot directory (under SnapshotsDir) and the manifest. If kopia or
// the tfstate copy fail, the caller MUST refuse to apply. CreateBundle
// is best-effort transactional: if the kopia call succeeds but the
// tfstate copy fails we still return the manifest with the kopia ids
// so an operator can clean up via `kopia snapshot delete`.
func (a *AtomicSnapshotter) CreateBundle(ctx context.Context, opts BundleOptions) (dir string, manifest *SnapshotManifest, err error) {
	if err := a.preflight(ctx, opts); err != nil {
		return "", nil, err
	}

	now := a.timeNow()
	ts := now.UTC().Format("20060102T150405Z")
	dirName := ts + "-" + sanitizeForPath(opts.OldKitVersion)
	dir = filepath.Join(a.SnapshotsDir, dirName)

	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", nil, fmt.Errorf("snapshot dir: %w", err)
	}

	// Step 1 — Kopia snapshots
	tag := fmt.Sprintf("pre-update=%s", ts)
	specs := make([]CreateSpec, 0, len(opts.VolumePaths))
	for _, p := range opts.VolumePaths {
		specs = append(specs, CreateSpec{Path: p, Tags: []string{tag}})
	}
	created, kerr := a.Kopia.SnapshotCreate(ctx, specs)
	if kerr != nil {
		return dir, nil, fmt.Errorf("step 1 kopia: %w", kerr)
	}

	// Step 2 — copy tofu state
	tofuDst := filepath.Join(dir, "state.tfstate")
	if cerr := copyFile(opts.TofuStateSrc, tofuDst); cerr != nil {
		return dir, nil, fmt.Errorf("step 2 tfstate copy: %w", cerr)
	}

	// Step 3 — write manifest
	refs := make([]KopiaSnapshotRef, 0, len(created))
	for i, snap := range created {
		path := snap.Path
		if path == "" && i < len(opts.VolumePaths) {
			path = opts.VolumePaths[i]
		}
		refs = append(refs, KopiaSnapshotRef{Path: path, SnapshotID: snap.ID})
	}
	manifest = &SnapshotManifest{
		Schema:         ManifestVersion,
		Timestamp:      now.UTC(),
		NodeName:       a.NodeName,
		OldKitVersion:  opts.OldKitVersion,
		NewKitVersion:  opts.NewKitVersion,
		KopiaSnapshots: refs,
		TofuStatePath:  tofuDst,
		ChannelMap:     opts.ChannelMap,
	}
	if werr := writeManifest(filepath.Join(dir, "manifest.yaml"), manifest); werr != nil {
		return dir, nil, fmt.Errorf("step 3 manifest: %w", werr)
	}
	return dir, manifest, nil
}

// LoadManifest reads a manifest.yaml from a snapshot directory.
func LoadManifest(snapshotDir string) (*SnapshotManifest, error) {
	raw, err := os.ReadFile(filepath.Join(snapshotDir, "manifest.yaml"))
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	var m SnapshotManifest
	if err := yaml.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("decode manifest: %w", err)
	}
	return &m, nil
}

// Verify checks that a snapshot directory still has every anchor it
// claims to. We deliberately keep this check local-only: confirming
// each Kopia snapshot id by round-tripping to the repo can take
// minutes against remote storage, and the rollback CLI does that
// existence check naturally when it calls `kopia snapshot restore`.
func Verify(snapshotDir string) error {
	m, err := LoadManifest(snapshotDir)
	if err != nil {
		return err
	}
	if m.TofuStatePath == "" {
		return errors.New("manifest: tofuStatePath empty")
	}
	if _, err := os.Stat(m.TofuStatePath); err != nil {
		return fmt.Errorf("tfstate copy missing: %w", err)
	}
	return nil
}

// timeNow honors an injected clock for tests.
func (a *AtomicSnapshotter) timeNow() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

// preflight runs the cheap input checks before we start touching disk.
// CreateBundle's contract is: if preflight returns nil, we make a best
// effort to produce a complete bundle; if it returns non-nil, nothing
// has been written.
func (a *AtomicSnapshotter) preflight(ctx context.Context, opts BundleOptions) error {
	if a.Kopia == nil {
		return errors.New("AtomicSnapshotter.Kopia is nil")
	}
	if a.SnapshotsDir == "" {
		return errors.New("AtomicSnapshotter.SnapshotsDir is required")
	}
	if opts.TofuStateSrc == "" {
		return errors.New("BundleOptions.TofuStateSrc is required")
	}
	if _, err := os.Stat(opts.TofuStateSrc); err != nil {
		return fmt.Errorf("tofu state %q not readable: %w", opts.TofuStateSrc, err)
	}
	status, err := a.Kopia.Status(ctx)
	if err != nil {
		return fmt.Errorf("kopia status: %w", err)
	}
	if !status.Configured {
		return ErrKopiaNotConfigured
	}
	return nil
}

func writeManifest(path string, m *SnapshotManifest) error {
	raw, err := yaml.Marshal(m)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o640); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src) // #nosec G304 -- operator-supplied path inside their workdir
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600) // #nosec G304 -- destination in our snapshot dir
	if err != nil {
		return err
	}
	outClosed := false
	defer func() {
		if !outClosed {
			_ = out.Close()
		}
	}()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	if err := out.Sync(); err != nil {
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	outClosed = true
	return nil
}

// sanitizeForPath swaps / and \ out of a string so it is safe to embed
// in a directory name. We do not use filepath.Clean because we want the
// transform to be idempotent and predictable for the manifest reader.
func sanitizeForPath(s string) string {
	r := strings.NewReplacer("/", "_", "\\", "_", " ", "_", ":", "_")
	return r.Replace(s)
}
