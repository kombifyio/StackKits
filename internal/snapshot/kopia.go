// Package snapshot wraps the Kopia CLI and combines it with the OpenTofu
// state copy to form the atomic-snapshot step that gates every
// `stackkit kit upgrade` (kit-update-phase-1, ADR-0018).
//
// The wrapper is intentionally thin: it shells out to `kopia` with JSON
// output and parses the structured response. We do not vendor the Kopia
// SDK because (1) Kopia is the operator-facing backup engine standardized
// in ADR-0016 and (2) shelling out keeps the StackKits binary independent
// of Kopia's internal API churn.
package snapshot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"time"
)

// Kopia is a thin CLI wrapper around the `kopia` binary.
type Kopia struct {
	binary  string
	timeout time.Duration
}

// KopiaOption configures the Kopia wrapper.
type KopiaOption func(*Kopia)

// WithBinary overrides the default `kopia` lookup. Tests pass a fake
// script path; production lets PATH resolution find the real binary.
func WithBinary(path string) KopiaOption { return func(k *Kopia) { k.binary = path } }

// WithTimeout sets the per-command timeout. Default 10 minutes — Kopia
// snapshot of a multi-GB volume legitimately takes minutes.
func WithTimeout(d time.Duration) KopiaOption { return func(k *Kopia) { k.timeout = d } }

// NewKopia builds a Kopia wrapper. It does NOT verify that the repo is
// configured; call Status for that.
func NewKopia(opts ...KopiaOption) *Kopia {
	k := &Kopia{binary: "kopia", timeout: 10 * time.Minute}
	for _, opt := range opts {
		opt(k)
	}
	return k
}

// StatusResult is the subset of `kopia repository status --json` we care
// about. Kopia emits more fields; we ignore them.
type StatusResult struct {
	// Configured is false when the local repo is not yet set up. The
	// `stackkit kit upgrade` Pre-Flight A check fails fast on this.
	Configured bool `json:"configured"`

	// ConfigFile is the path Kopia loaded its config from when configured.
	ConfigFile string `json:"configFile,omitempty"`

	// Storage describes the backing storage type (filesystem, s3, ...).
	Storage string `json:"storage,omitempty"`
}

// ErrRepoNotConfigured is returned by Status when no Kopia repo is
// reachable. Callers should surface this with the operator-facing message
// "kopia-repo nicht konfiguriert. Erst 'stackkit backup configure' ausführen."
var ErrRepoNotConfigured = errors.New("kopia: repository not configured")

// Status checks whether a Kopia repository is reachable. A non-nil error
// means we could not even run kopia (binary missing, exit-non-zero with
// unparseable output). Configured=false (with nil err) means kopia ran
// but reports no configured repo — caller must wrap with
// ErrRepoNotConfigured before bubbling up.
func (k *Kopia) Status(ctx context.Context) (StatusResult, error) {
	out, err := k.run(ctx, "repository", "status", "--json")
	if err != nil {
		return parseStatus(out, err)
	}
	return parseStatus(out, nil)
}

// parseStatus is the pure-function half of Status — testable without exec.
// out is the combined stdout+stderr from the kopia call; runErr is the
// non-nil error from exec.Cmd.Run when the command itself failed.
func parseStatus(out []byte, runErr error) (StatusResult, error) {
	if runErr != nil {
		// "ERROR: not connected to a repository" is Kopia's response on
		// a fresh install. We translate to Configured=false rather than
		// surfacing the raw error — callers know what to do with that.
		if bytes.Contains(out, []byte("not connected")) {
			return StatusResult{Configured: false}, nil
		}
		return StatusResult{}, fmt.Errorf("kopia repository status: %w (output=%q)", runErr, string(out))
	}
	var sr StatusResult
	if jerr := json.Unmarshal(out, &sr); jerr != nil {
		return StatusResult{}, fmt.Errorf("kopia status: parse json: %w", jerr)
	}
	if sr.ConfigFile != "" {
		sr.Configured = true
	}
	return sr, nil
}

// CreateSpec describes a snapshot-create call. Path is the source
// directory or volume mount; Tags are merged into Kopia's snapshot
// metadata so we can find the snapshot later by tag.
type CreateSpec struct {
	Path string
	Tags []string // tag1=value1, tag2=value2 — pre-formatted "k=v"
}

// CreatedSnapshot is the parsed response from `kopia snapshot create
// --json`. Kopia returns more fields; we keep the ones the atomic
// snapshotter writes into its manifest.
type CreatedSnapshot struct {
	ID        string    `json:"id"`
	StartTime time.Time `json:"startTime,omitempty"`
	EndTime   time.Time `json:"endTime,omitempty"`
	Path      string    `json:"path,omitempty"`
}

// SnapshotCreate creates one snapshot per CreateSpec. We do not parallelize:
// Kopia does its own dedup-aware concurrency, and a sequential operator-
// readable progress is more useful than overlapping Snapshot lines.
func (k *Kopia) SnapshotCreate(ctx context.Context, specs []CreateSpec) ([]CreatedSnapshot, error) {
	if len(specs) == 0 {
		return nil, nil
	}
	out := make([]CreatedSnapshot, 0, len(specs))
	for _, s := range specs {
		args := []string{"snapshot", "create", s.Path, "--json"}
		for _, tag := range s.Tags {
			args = append(args, "--tags", tag)
		}
		raw, err := k.run(ctx, args...)
		if err != nil {
			return out, fmt.Errorf("kopia snapshot create %s: %w (output=%q)", s.Path, err, string(raw))
		}
		var snap CreatedSnapshot
		if jerr := json.Unmarshal(raw, &snap); jerr != nil {
			return out, fmt.Errorf("kopia snapshot create %s: parse json: %w", s.Path, jerr)
		}
		out = append(out, snap)
	}
	return out, nil
}

// SnapshotFilter narrows `kopia snapshot list` results.
type SnapshotFilter struct {
	// Tag matches snapshots that carry this k=v tag. Empty = no filter.
	Tag string
}

// Snapshot is the subset of `kopia snapshot list --json` rows we use.
type Snapshot struct {
	ID        string    `json:"id"`
	Source    string    `json:"source,omitempty"`
	StartTime time.Time `json:"startTime,omitempty"`
	EndTime   time.Time `json:"endTime,omitempty"`
	Tags      []string  `json:"tags,omitempty"`
}

// SnapshotList enumerates snapshots, optionally filtered by tag.
func (k *Kopia) SnapshotList(ctx context.Context, f SnapshotFilter) ([]Snapshot, error) {
	args := []string{"snapshot", "list", "--json", "--all"}
	if f.Tag != "" {
		args = append(args, "--tags", f.Tag)
	}
	raw, err := k.run(ctx, args...)
	if err != nil {
		return nil, fmt.Errorf("kopia snapshot list: %w (output=%q)", err, string(raw))
	}
	var snaps []Snapshot
	if jerr := json.Unmarshal(raw, &snaps); jerr != nil {
		return nil, fmt.Errorf("kopia snapshot list: parse json: %w", jerr)
	}
	return snaps, nil
}

// SnapshotRestore restores one snapshot to targetPath. Kopia uses
// non-zero exit on failure; we surface the stderr output verbatim so
// operators can act.
func (k *Kopia) SnapshotRestore(ctx context.Context, snapshotID, targetPath string) error {
	if snapshotID == "" || targetPath == "" {
		return fmt.Errorf("snapshot id and target path are required")
	}
	out, err := k.run(ctx, "snapshot", "restore", snapshotID, targetPath)
	if err != nil {
		return fmt.Errorf("kopia snapshot restore %s: %w (output=%q)", snapshotID, err, string(out))
	}
	return nil
}

// run executes the configured kopia binary with timeout. Stdout is
// returned as bytes; stderr is folded into the error on non-zero exit.
func (k *Kopia) run(ctx context.Context, args ...string) ([]byte, error) {
	cctx, cancel := context.WithTimeout(ctx, k.timeout)
	defer cancel()

	cmd := exec.CommandContext(cctx, k.binary, args...) // #nosec G204 — binary path is operator/test-controlled, args are package-internal
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		// Combine stdout + stderr so callers can grep for "not connected" etc.
		combined := append(stdout.Bytes(), stderr.Bytes()...)
		return combined, err
	}
	return stdout.Bytes(), nil
}
