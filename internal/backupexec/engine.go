// Package backupexec holds the Kopia backup engine primitives shared by the
// `stackkit backup` CLI and the node-local runtime-action endpoints. The CLI
// and the server must call the same argv definitions — "CLI == Web UI ==
// server" honesty depends on there being exactly one implementation.
package backupexec

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Executor runs a command inside the kopia-agent container (or a fake in
// tests). It matches the CLI's historical `backupExecutor` seam so existing
// fakes plug in unchanged.
type Executor func(ctx context.Context, command []string) (string, error)

// DefaultVolumeSource is the canonical snapshot source covering the Docker
// volumes mount inside the kopia-agent container.
const DefaultVolumeSource = "/source/docker-volumes"

// Engine exposes granular Kopia operations. Orchestration (messages, retry,
// sequencing) stays with the caller so the CLI keeps its exact behavior.
type Engine struct {
	Exec Executor
}

// S3Repository describes an S3-compatible repository target (kombify-managed
// R2 or bring-your-own). Credentials travel by value and must never be
// logged or persisted by callers.
type S3Repository struct {
	Endpoint        string
	Bucket          string
	Region          string
	Prefix          string
	AccessKeyID     string
	SecretAccessKey string
}

type Snapshot struct {
	ID         string    `json:"id"`
	SourcePath string    `json:"sourcePath"`
	SourceHost string    `json:"sourceHost"`
	StartTime  time.Time `json:"startTime"`
	EndTime    time.Time `json:"endTime"`
	TotalSize  int64     `json:"totalSize"`
}

func (e Engine) RepositoryStatusJSON(ctx context.Context) (string, error) {
	return e.Exec(ctx, []string{"kopia", "repository", "status", "--json"})
}

func (e Engine) Mkdir(ctx context.Context, path string) (string, error) {
	return e.Exec(ctx, []string{"mkdir", "-p", path})
}

func (e Engine) CreateFilesystemRepository(ctx context.Context, path string) (string, error) {
	return e.Exec(ctx, []string{"kopia", "repository", "create", "filesystem", "--path", path})
}

func (e Engine) ConnectFilesystemRepository(ctx context.Context, path string) (string, error) {
	return e.Exec(ctx, []string{"kopia", "repository", "connect", "filesystem", "--path", path})
}

func (e Engine) CreateS3Repository(ctx context.Context, repo S3Repository, password string) (string, error) {
	return e.Exec(ctx, s3RepositoryArgs("create", repo, password))
}

func (e Engine) ConnectS3Repository(ctx context.Context, repo S3Repository, password string) (string, error) {
	return e.Exec(ctx, s3RepositoryArgs("connect", repo, password))
}

// EnsureFilesystemRepository connects to (or first creates) a local
// filesystem repository: status pre-check, mkdir, create, and connect when
// the repository already exists. CLI configure and the backup_run local
// branch share this single sequence.
func (e Engine) EnsureFilesystemRepository(ctx context.Context, path string) (string, error) {
	out, err := e.RepositoryStatusJSON(ctx)
	if err == nil && StatusConfigured(out) {
		return out, nil
	}
	if err != nil && !OutputLooksNotConfigured(out, err) {
		return out, fmt.Errorf("check kopia repository status: %w", err)
	}
	if _, err := e.Mkdir(ctx, path); err != nil {
		return "", fmt.Errorf("prepare repository path %s: %w", path, err)
	}
	out, err = e.CreateFilesystemRepository(ctx, path)
	if err != nil {
		if !OutputLooksRepoExists(out, err) {
			return out, fmt.Errorf("create kopia repository %s: %w", path, err)
		}
		out, err = e.ConnectFilesystemRepository(ctx, path)
		if err != nil {
			return out, fmt.Errorf("connect existing kopia repository %s: %w", path, err)
		}
	}
	return out, nil
}

// EnsureS3Repository connects to (or first creates) the S3-compatible
// repository. It mirrors the filesystem sequence the CLI uses: create, and
// when the repository already exists, connect instead.
func (e Engine) EnsureS3Repository(ctx context.Context, repo S3Repository, password string) (string, error) {
	out, err := e.RepositoryStatusJSON(ctx)
	if err == nil && StatusConfigured(out) {
		return out, nil
	}
	if err != nil && !OutputLooksNotConfigured(out, err) {
		return out, fmt.Errorf("check kopia repository status: %w", err)
	}
	out, err = e.CreateS3Repository(ctx, repo, password)
	if err != nil {
		if !OutputLooksRepoExists(out, err) {
			return out, fmt.Errorf("create kopia s3 repository %s/%s: %w", repo.Endpoint, repo.Bucket, err)
		}
		out, err = e.ConnectS3Repository(ctx, repo, password)
		if err != nil {
			return out, fmt.Errorf("connect existing kopia s3 repository %s/%s: %w", repo.Endpoint, repo.Bucket, err)
		}
	}
	return out, nil
}

func (e Engine) Snapshot(ctx context.Context, source, description string) (string, error) {
	return e.Exec(ctx, []string{
		"kopia", "snapshot", "create", source,
		"--description", description,
	})
}

func (e Engine) ListSnapshotsJSON(ctx context.Context) (string, error) {
	return e.Exec(ctx, []string{"kopia", "snapshot", "list", "--json"})
}

func (e Engine) Restore(ctx context.Context, snapshotID, target string) (string, error) {
	return e.Exec(ctx, []string{"kopia", "snapshot", "restore", snapshotID, target})
}

func (e Engine) ValidateProvider(ctx context.Context) (string, error) {
	return e.Exec(ctx, []string{"kopia", "repository", "validate-provider"})
}

// DeleteSnapshots removes the given snapshot manifests. Kopia requires the
// --delete flag to confirm destructive intent (kopia.io command-line
// reference, snapshot-delete).
func (e Engine) DeleteSnapshots(ctx context.Context, ids []string) (string, error) {
	if len(ids) == 0 {
		return "", nil
	}
	args := append([]string{"kopia", "snapshot", "delete", "--delete"}, ids...)
	return e.Exec(ctx, args)
}

// MaintenanceRunFull compacts and garbage-collects the repository after bulk
// snapshot deletion so wiped data actually leaves the store.
func (e Engine) MaintenanceRunFull(ctx context.Context) (string, error) {
	return e.Exec(ctx, []string{"kopia", "maintenance", "run", "--full"})
}

// Disconnect detaches the agent from the repository without deleting remote
// data. backup_wipe calls it last so a wiped node no longer holds
// repository credentials in its Kopia config.
func (e Engine) Disconnect(ctx context.Context) (string, error) {
	return e.Exec(ctx, []string{"kopia", "repository", "disconnect"})
}

func s3RepositoryArgs(verb string, repo S3Repository, password string) []string {
	args := []string{
		"kopia", "repository", verb, "s3",
		"--bucket", repo.Bucket,
		"--endpoint", repo.Endpoint,
		"--access-key", repo.AccessKeyID,
		"--secret-access-key", repo.SecretAccessKey,
	}
	if repo.Region != "" {
		args = append(args, "--region", repo.Region)
	}
	if repo.Prefix != "" {
		args = append(args, "--prefix", repo.Prefix)
	}
	if password != "" {
		args = append(args, "--password", password)
	}
	return args
}

// ParseSnapshots decodes Kopia's `snapshot list --json` output into the
// engine's snapshot shape. Unknown fields are ignored so Kopia schema drift
// degrades gracefully.
func ParseSnapshots(raw string) ([]Snapshot, error) {
	var rows []struct {
		ID     string `json:"id"`
		Source struct {
			Path string `json:"path"`
			Host string `json:"host"`
		} `json:"source"`
		StartTime time.Time `json:"startTime"`
		EndTime   time.Time `json:"endTime"`
		Stats     struct {
			TotalSize int64 `json:"totalSize"`
		} `json:"stats"`
	}
	if err := json.Unmarshal([]byte(raw), &rows); err != nil {
		return nil, err
	}
	snapshots := make([]Snapshot, 0, len(rows))
	for _, row := range rows {
		snapshots = append(snapshots, Snapshot{
			ID:         row.ID,
			SourcePath: row.Source.Path,
			SourceHost: row.Source.Host,
			StartTime:  row.StartTime,
			EndTime:    row.EndTime,
			TotalSize:  row.Stats.TotalSize,
		})
	}
	return snapshots, nil
}

// StatusConfigured reports whether `kopia repository status --json` output
// describes a connected repository. Semantics are ported verbatim from the
// CLI (`backupStatusConfigured`).
func StatusConfigured(out string) bool {
	if strings.TrimSpace(out) == "" {
		return false
	}
	var status struct {
		Configured bool   `json:"configured"`
		ConfigFile string `json:"configFile"`
	}
	if err := json.Unmarshal([]byte(out), &status); err != nil {
		return false
	}
	return status.Configured || status.ConfigFile != ""
}

// OutputLooksNotConfigured classifies "repository not connected/initialized"
// answers so callers can distinguish first-run from real failures. Ported
// verbatim from the CLI (`backupOutputLooksNotConfigured`).
func OutputLooksNotConfigured(out string, err error) bool {
	lower := strings.ToLower(out)
	if err != nil {
		lower += "\n" + strings.ToLower(err.Error())
	}
	return strings.Contains(lower, "not connected") ||
		strings.Contains(lower, "not configured") ||
		strings.Contains(lower, "repository is not initialized")
}

// OutputLooksRepoExists classifies "repository already exists" answers from
// `kopia repository create` so callers can connect instead. Ported verbatim
// from the CLI (`backupOutputLooksRepoExists`).
func OutputLooksRepoExists(out string, err error) bool {
	lower := strings.ToLower(out)
	if err != nil {
		lower += "\n" + strings.ToLower(err.Error())
	}
	return strings.Contains(lower, "already exists") ||
		strings.Contains(lower, "already initialized") ||
		strings.Contains(lower, "repository exists")
}
