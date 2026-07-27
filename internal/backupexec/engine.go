// Package backupexec holds the Kopia backup engine primitives shared by the
// `stackkit backup` CLI and the node-local StackAction endpoints. The CLI
// and the server must call the same argv definitions — "CLI == Web UI ==
// server" honesty depends on there being exactly one implementation.
package backupexec

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/localbackuppolicy"
)

// Executor runs a command inside the kopia-agent container (or a fake in
// tests). It matches the CLI's historical `backupExecutor` seam so existing
// fakes plug in unchanged.
type Executor func(ctx context.Context, command []string) (string, error)

// SecretExecutor is the native-v2 invocation seam. Sensitive input is
// delivered separately from argv so the Docker adapter can pass it only on
// stdin and redact it from every observable result.
type SecretExecutor func(ctx context.Context, command []string, sensitiveInput []byte) (string, error)

// safeDiagnosticError marks an executor failure whose observable text has
// already crossed the native-v2 password-redaction boundary. The concrete
// type stays private so callers cannot bless arbitrary engine errors as safe.
type safeDiagnosticError struct {
	message string
}

func (e *safeDiagnosticError) Error() string {
	return e.message
}

// SafeDiagnostic returns an operator-safe diagnostic only when the error
// originated behind the native-v2 redaction boundary. Wrapping context added
// by this package is retained; arbitrary executor or test-double errors are
// deliberately rejected.
func SafeDiagnostic(err error) (string, bool) {
	if err == nil {
		return "", false
	}
	var safe *safeDiagnosticError
	if !errors.As(err, &safe) {
		return "", false
	}
	diagnostic := strings.TrimSpace(err.Error())
	return diagnostic, diagnostic != ""
}

// DefaultVolumeSource is the canonical snapshot source covering the Docker
// volumes mount inside the kopia-agent container.
const (
	DefaultVolumeSource   = localbackuppolicy.SourcePath
	DefaultRepositoryPath = localbackuppolicy.RepositoryPath
	DefaultConfigFile     = localbackuppolicy.ConfigPath + "/repository.config"
	DefaultCacheDirectory = localbackuppolicy.CachePath
)

// Engine exposes granular Kopia operations. Orchestration (messages, retry,
// sequencing) stays with the caller so the CLI keeps its exact behavior.
type Engine struct {
	Exec Executor
}

// V2Engine is the fail-closed native-v2 Kopia path. It deliberately does not
// reuse Engine because the historical Executor contract permits credentials
// in argv and must remain compatible until its callers migrate.
type V2Engine struct {
	Exec SecretExecutor
}

// NewV2Engine binds native-v2 operations to explicit persistent config and
// cache locations in the local Kopia runtime.
func NewV2Engine(exec SecretExecutor) V2Engine {
	return V2Engine{Exec: exec}
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
	ID          string    `json:"id"`
	SourcePath  string    `json:"sourcePath"`
	SourceHost  string    `json:"sourceHost"`
	Description string    `json:"description,omitempty"`
	OperationID string    `json:"operationId,omitempty"`
	StartTime   time.Time `json:"startTime"`
	EndTime     time.Time `json:"endTime"`
	TotalSize   int64     `json:"totalSize"`
}

// SnapshotRequest is the closed native-v2 snapshot input. OperationID is
// materialized as a Kopia tag so lifecycle evidence can bind the result
// without accepting arbitrary caller-supplied argv.
type SnapshotRequest struct {
	Source      string
	Description string
	OperationID string
}

// RestoreRequest is the closed native-v2 staged-restore input. StagingPath
// must be derived from OperationID below the governed isolated volume.
type RestoreRequest struct {
	SnapshotID  string
	OperationID string
	StagingPath string
}

type RestoreResult struct {
	SnapshotID                string `json:"snapshotId"`
	OperationID               string `json:"operationId"`
	StagingPath               string `json:"stagingPath"`
	RepositoryContentVerified bool   `json:"repositoryContentVerified"`
}

// RepositoryStatus is the structured native-v2 projection of Kopia
// repository status. Raw Kopia output never crosses the V2Engine boundary.
type RepositoryStatus struct {
	Configured  bool   `json:"configured"`
	ConfigFile  string `json:"configFile"`
	Storage     string `json:"storage"`
	StoragePath string `json:"storagePath"`
}

// SourcePolicy is the typed effective-policy verdict for the governed source.
// Exact is true only for the CUE-owned ignore set and manual-only behavior.
type SourcePolicy struct {
	Source       string   `json:"source"`
	ExcludePaths []string `json:"excludePaths"`
	Exact        bool     `json:"exact"`
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

// CreateFilesystemRepository initializes a native-v2 local repository. The
// repository password is supplied only to the fixed per-process password
// adapter over sensitive stdin; it is never part of Docker argv or persistent
// container configuration.
func (e V2Engine) CreateFilesystemRepository(ctx context.Context, repositoryPath string, password []byte) error {
	_, err := e.createFilesystemRepository(ctx, repositoryPath, password)
	return err
}

func (e V2Engine) createFilesystemRepository(ctx context.Context, repositoryPath string, password []byte) (string, error) {
	if repositoryPath != DefaultRepositoryPath {
		return "", fmt.Errorf("repository path must be %q", DefaultRepositoryPath)
	}
	input, err := repositoryPasswordInput(password, 1)
	if err != nil {
		return "", err
	}
	defer clear(input)
	return e.invoke(ctx, []string{
		"repository", "create", "filesystem",
		"--path", repositoryPath,
		"--cache-directory", DefaultCacheDirectory,
	}, input)
}

// ConnectFilesystemRepository connects the native-v2 runtime to an existing
// local repository without placing its password in argv or environment.
func (e V2Engine) ConnectFilesystemRepository(ctx context.Context, repositoryPath string, password []byte) error {
	_, err := e.connectFilesystemRepository(ctx, repositoryPath, password)
	return err
}

func (e V2Engine) connectFilesystemRepository(ctx context.Context, repositoryPath string, password []byte) (string, error) {
	if repositoryPath != DefaultRepositoryPath {
		return "", fmt.Errorf("repository path must be %q", DefaultRepositoryPath)
	}
	input, err := repositoryPasswordInput(password, 1)
	if err != nil {
		return "", err
	}
	defer clear(input)
	return e.invoke(ctx, []string{
		"repository", "connect", "filesystem",
		"--path", repositoryPath,
		"--cache-directory", DefaultCacheDirectory,
	}, input)
}

// RepositoryStatus returns typed status. A clean first-run "not configured"
// answer is represented as Configured=false rather than an operation error.
func (e V2Engine) RepositoryStatus(ctx context.Context, password []byte) (RepositoryStatus, error) {
	input, err := repositoryPasswordInput(password, 1)
	if err != nil {
		return RepositoryStatus{}, err
	}
	defer clear(input)
	out, err := e.invoke(ctx, []string{"repository", "status", "--json"}, input)
	if err != nil {
		if OutputLooksNotConfigured(out, err) {
			return RepositoryStatus{}, nil
		}
		return RepositoryStatus{}, fmt.Errorf("read kopia repository status: %w", err)
	}
	var row struct {
		Configured bool            `json:"configured"`
		ConfigFile string          `json:"configFile"`
		Storage    json.RawMessage `json:"storage"`
	}
	if err := decodePTYJSONObject(out, &row); err != nil {
		return RepositoryStatus{}, fmt.Errorf("parse kopia repository status: %w", err)
	}
	status := RepositoryStatus{
		Configured: row.Configured,
		ConfigFile: row.ConfigFile,
	}
	if len(row.Storage) > 0 {
		var storage struct {
			Type   string `json:"type"`
			Config struct {
				Path string `json:"path"`
			} `json:"config"`
		}
		if err := json.Unmarshal(row.Storage, &storage); err != nil {
			return RepositoryStatus{}, fmt.Errorf("parse kopia repository storage: %w", err)
		}
		status.Storage = storage.Type
		status.StoragePath = storage.Config.Path
	}
	status.Configured = status.Configured || strings.TrimSpace(status.ConfigFile) != ""
	return status, nil
}

// EnsureFilesystemRepository idempotently creates or connects a local
// repository and returns only its structured final status.
func (e V2Engine) EnsureFilesystemRepository(ctx context.Context, repositoryPath string, password []byte) (RepositoryStatus, error) {
	if repositoryPath != DefaultRepositoryPath {
		return RepositoryStatus{}, fmt.Errorf("repository path must be %q", DefaultRepositoryPath)
	}
	status, err := e.RepositoryStatus(ctx, password)
	if err != nil {
		return RepositoryStatus{}, err
	}
	if status.Configured {
		if !exactFilesystemRepository(status) {
			return RepositoryStatus{}, fmt.Errorf("configured kopia repository differs from the governed local filesystem repository")
		}
		return status, nil
	}

	out, err := e.createFilesystemRepository(ctx, repositoryPath, password)
	if err != nil {
		if !OutputLooksRepoExists(out, err) {
			return RepositoryStatus{}, fmt.Errorf("create kopia repository: %w", err)
		}
		if _, err := e.connectFilesystemRepository(ctx, repositoryPath, password); err != nil {
			return RepositoryStatus{}, fmt.Errorf("connect kopia repository: %w", err)
		}
	}

	status, err = e.RepositoryStatus(ctx, password)
	if err != nil {
		return RepositoryStatus{}, err
	}
	if !status.Configured {
		return RepositoryStatus{}, fmt.Errorf("kopia repository did not report configured after initialization")
	}
	if !exactFilesystemRepository(status) {
		return RepositoryStatus{}, fmt.Errorf("configured kopia repository differs from the governed local filesystem repository")
	}
	return status, nil
}

func exactFilesystemRepository(status RepositoryStatus) bool {
	return status.Configured &&
		status.ConfigFile == DefaultConfigFile &&
		status.Storage == "filesystem" &&
		status.StoragePath == DefaultRepositoryPath
}

// ConfigureSourcePolicy applies the CUE-resolved exclusions to the exact
// snapshot source using a closed argv construction.
func (e V2Engine) ConfigureSourcePolicy(ctx context.Context, source string, excludePaths []string, password []byte) error {
	relativeExcludes, err := governedRelativeExcludes(source, excludePaths)
	if err != nil {
		return err
	}
	input, err := repositoryPasswordInput(password, 1)
	if err != nil {
		return err
	}
	if _, err := e.invoke(ctx, []string{"policy", "delete", source}, input); err != nil {
		clear(input)
		return fmt.Errorf("reset kopia source policy: %w", err)
	}
	clear(input)

	command := []string{
		"policy", "set", source,
		"--inherit=false",
		"--manual",
	}
	for _, relative := range relativeExcludes {
		command = append(command, "--add-ignore", relative)
	}
	input, err = repositoryPasswordInput(password, 1)
	if err != nil {
		return err
	}
	if _, err := e.invoke(ctx, command, input); err != nil {
		clear(input)
		return fmt.Errorf("configure kopia source policy: %w", err)
	}
	clear(input)

	policy, err := e.SourcePolicy(ctx, source, excludePaths, password)
	if err != nil {
		return err
	}
	if !policy.Exact {
		return fmt.Errorf("effective kopia source policy differs from the governed local backup policy")
	}
	return nil
}

// SourcePolicy reads back Kopia's complete effective source policy. Exact is
// true only for the governed ignores plus manual-only scheduling. Every other
// effective field is drift: inherited retention, dot-ignore files such as
// .kopiaignore, traversal/error filters, timers, actions, and future fields
// all fail closed instead of changing selection or autonomous behavior.
func (e V2Engine) SourcePolicy(ctx context.Context, source string, excludePaths []string, password []byte) (SourcePolicy, error) {
	expected, err := governedRelativeExcludes(source, excludePaths)
	if err != nil {
		return SourcePolicy{}, err
	}
	input, err := repositoryPasswordInput(password, 1)
	if err != nil {
		return SourcePolicy{}, err
	}
	defer clear(input)
	out, err := e.invoke(ctx, []string{"policy", "show", source, "--json"}, input)
	if err != nil {
		return SourcePolicy{}, fmt.Errorf("read effective kopia source policy: %w", err)
	}
	var row map[string]json.RawMessage
	if err := decodePTYJSONObject(out, &row); err != nil {
		return SourcePolicy{}, fmt.Errorf("parse effective kopia source policy: %w", err)
	}
	var files map[string]json.RawMessage
	if err := json.Unmarshal(row["files"], &files); err != nil {
		return SourcePolicy{}, fmt.Errorf("parse effective kopia source policy: %w", err)
	}
	var ignores []string
	if err := json.Unmarshal(files["ignore"], &ignores); err != nil {
		return SourcePolicy{}, fmt.Errorf("parse effective kopia source policy ignores: %w", err)
	}
	var scheduling map[string]json.RawMessage
	if err := json.Unmarshal(row["scheduling"], &scheduling); err != nil {
		return SourcePolicy{}, fmt.Errorf("parse effective kopia source scheduling policy: %w", err)
	}
	var manual bool
	if err := json.Unmarshal(scheduling["manual"], &manual); err != nil {
		return SourcePolicy{}, fmt.Errorf("parse effective kopia source manual policy: %w", err)
	}
	effective := make([]string, 0, len(ignores))
	relative := make([]string, 0, len(ignores))
	for _, ignore := range ignores {
		if ignore == "" || strings.HasPrefix(ignore, "/") || path.Clean(ignore) != ignore ||
			ignore == ".." || strings.HasPrefix(ignore, "../") {
			return SourcePolicy{}, fmt.Errorf("effective kopia source policy contains a non-canonical ignore rule")
		}
		relative = append(relative, ignore)
		effective = append(effective, source+"/"+ignore)
	}
	slices.Sort(relative)
	slices.Sort(expected)
	exactShape := len(row) == 2 &&
		len(files) == 1 &&
		len(scheduling) == 1 &&
		manual
	return SourcePolicy{
		Source:       source,
		ExcludePaths: effective,
		Exact:        exactShape && slices.Equal(relative, expected),
	}, nil
}

func governedRelativeExcludes(source string, excludePaths []string) ([]string, error) {
	governed := localbackuppolicy.GovernedSource()
	if source != governed.ContainerPath {
		return nil, fmt.Errorf("snapshot source must be %q", governed.ContainerPath)
	}
	if !slices.Equal(excludePaths, governed.ExcludePaths) {
		return nil, fmt.Errorf("snapshot exclusions must equal the governed local backup policy")
	}
	source, err := cleanAbsoluteContainerPath(source)
	if err != nil {
		return nil, fmt.Errorf("invalid snapshot source: %w", err)
	}
	if source != DefaultVolumeSource {
		return nil, fmt.Errorf("snapshot source must be %q", DefaultVolumeSource)
	}
	if len(excludePaths) == 0 {
		return nil, fmt.Errorf("at least one snapshot exclusion is required")
	}
	relative := make([]string, 0, len(excludePaths))
	for _, excludePath := range excludePaths {
		excludePath, err = cleanAbsoluteContainerPath(excludePath)
		if err != nil {
			return nil, fmt.Errorf("invalid snapshot exclusion: %w", err)
		}
		if !strings.HasPrefix(excludePath, source+"/") {
			return nil, fmt.Errorf("snapshot exclusion %q must be below source %q", excludePath, source)
		}
		relative = append(relative, strings.TrimPrefix(excludePath, source+"/"))
	}
	return relative, nil
}

func cleanAbsoluteContainerPath(value string) (string, error) {
	if value == "" || !strings.HasPrefix(value, "/") {
		return "", fmt.Errorf("path must be absolute")
	}
	cleaned := path.Clean(value)
	if cleaned != value {
		return "", fmt.Errorf("path must be canonical")
	}
	return cleaned, nil
}

func repositoryPasswordInput(password []byte, repetitions int) ([]byte, error) {
	if len(password) == 0 {
		return nil, fmt.Errorf("repository password is required")
	}
	if bytes.IndexAny(password, "\x00\r\n") >= 0 {
		return nil, fmt.Errorf("repository password must be a non-NUL single line")
	}
	input := make([]byte, 0, repetitions*(len(password)+1))
	for range repetitions {
		input = append(input, password...)
		input = append(input, '\n')
	}
	return input, nil
}

func validOperationID(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for index, char := range value {
		if (char >= 'a' && char <= 'z') ||
			(char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') ||
			(index > 0 && (char == '.' || char == '_' || char == '-')) {
			continue
		}
		return false
	}
	return true
}

// CreateSnapshot creates one native-v2 snapshot and parses Kopia's JSON result
// into the stable StackKits snapshot shape. Missing or malformed identity is
// rejected rather than being reported as a successful snapshot.
func (e V2Engine) CreateSnapshot(ctx context.Context, request SnapshotRequest, password []byte) (Snapshot, error) {
	if err := validateSnapshotRequest(request); err != nil {
		return Snapshot{}, err
	}
	existing, found, err := e.FindSnapshot(ctx, request, password)
	if err != nil {
		return Snapshot{}, err
	}
	if found {
		return existing, nil
	}
	input, err := repositoryPasswordInput(password, 1)
	if err != nil {
		return Snapshot{}, err
	}
	defer clear(input)
	out, err := e.invoke(ctx, []string{
		"snapshot", "create", request.Source,
		"--description", request.Description,
		"--tags", "stackkit.operation:" + request.OperationID,
		"--json",
	}, input)
	if err != nil {
		return Snapshot{}, fmt.Errorf("create kopia snapshot: %w", err)
	}
	snapshot, err := ParseSnapshotCreate(out)
	if err != nil {
		return Snapshot{}, fmt.Errorf("parse kopia snapshot create result: %w", err)
	}
	if snapshot.SourcePath != request.Source {
		return Snapshot{}, fmt.Errorf("kopia snapshot source %q does not match requested source %q", snapshot.SourcePath, request.Source)
	}
	if snapshot.SourceHost != localbackuppolicy.Hostname {
		return Snapshot{}, fmt.Errorf("kopia snapshot host does not match the governed local backup runtime")
	}
	if snapshot.Description != request.Description {
		return Snapshot{}, fmt.Errorf("kopia snapshot description does not match request")
	}
	if snapshot.OperationID != request.OperationID {
		return Snapshot{}, fmt.Errorf("kopia snapshot operation tag does not match request")
	}
	return snapshot, nil
}

// FindSnapshot returns the exact typed receipt for a prior operation without
// creating a snapshot. Pending lifecycle recovery uses this read-only seam
// before deciding whether a create side effect is still required.
func (e V2Engine) FindSnapshot(ctx context.Context, request SnapshotRequest, password []byte) (Snapshot, bool, error) {
	if err := validateSnapshotRequest(request); err != nil {
		return Snapshot{}, false, err
	}
	input, err := repositoryPasswordInput(password, 1)
	if err != nil {
		return Snapshot{}, false, err
	}
	defer clear(input)
	out, err := e.invoke(ctx, []string{
		"snapshot", "list", request.Source,
		"--tags", "stackkit.operation:" + request.OperationID,
		"--json",
	}, input)
	if err != nil {
		return Snapshot{}, false, fmt.Errorf("lookup kopia snapshot operation: %w", err)
	}
	snapshots, err := parseSnapshotListPTY(out)
	if err != nil {
		return Snapshot{}, false, fmt.Errorf("parse kopia snapshot lookup: %w", err)
	}
	var matched []Snapshot
	for _, snapshot := range snapshots {
		if snapshot.OperationID == request.OperationID {
			matched = append(matched, snapshot)
		}
	}
	if len(matched) == 0 {
		return Snapshot{}, false, nil
	}
	if len(matched) != 1 {
		return Snapshot{}, false, fmt.Errorf("snapshot operation %q has %d receipts", request.OperationID, len(matched))
	}
	snapshot := matched[0]
	if snapshot.SourcePath != request.Source ||
		snapshot.SourceHost != localbackuppolicy.Hostname ||
		snapshot.Description != request.Description {
		return Snapshot{}, false, fmt.Errorf("snapshot operation %q conflicts with requested source or description", request.OperationID)
	}
	return snapshot, true, nil
}

// RestoreSnapshot performs a full repository-content verification before and
// after an atomic restore into the fixed isolated staging volume. It never
// receives or writes the live Docker volume root.
func (e V2Engine) RestoreSnapshot(
	ctx context.Context,
	request RestoreRequest,
	password []byte,
) (RestoreResult, error) {
	if err := validateRestoreRequest(request); err != nil {
		return RestoreResult{}, err
	}
	if err := e.verifySnapshotContent(ctx, request.SnapshotID, password); err != nil {
		return RestoreResult{}, fmt.Errorf("verify Kopia snapshot before restore: %w", err)
	}
	input, err := repositoryPasswordInput(password, 1)
	if err != nil {
		return RestoreResult{}, err
	}
	_, restoreErr := e.invoke(ctx, []string{
		"snapshot", "restore", request.SnapshotID, request.StagingPath,
		"--no-overwrite-symlinks",
		"--write-files-atomically",
		"--no-ignore-permission-errors",
		"--no-ignore-errors",
		"--skip-owners",
	}, input)
	clear(input)
	if restoreErr != nil {
		return RestoreResult{}, fmt.Errorf("restore Kopia snapshot to isolated staging: %w", restoreErr)
	}
	if err := e.verifySnapshotContent(ctx, request.SnapshotID, password); err != nil {
		return RestoreResult{}, fmt.Errorf("verify Kopia snapshot after restore: %w", err)
	}
	return RestoreResult{
		SnapshotID:                request.SnapshotID,
		OperationID:               request.OperationID,
		StagingPath:               request.StagingPath,
		RepositoryContentVerified: true,
	}, nil
}

func (e V2Engine) verifySnapshotContent(ctx context.Context, snapshotID string, password []byte) error {
	if !validPortableID(snapshotID) {
		return fmt.Errorf("snapshot id is invalid")
	}
	input, err := repositoryPasswordInput(password, 1)
	if err != nil {
		return err
	}
	defer clear(input)
	if _, err := e.invoke(ctx, []string{
		"snapshot", "verify", snapshotID,
		"--verify-files-percent=100",
		"--max-errors=1",
	}, input); err != nil {
		return err
	}
	return nil
}

func validateSnapshotRequest(request SnapshotRequest) error {
	source, err := cleanAbsoluteContainerPath(request.Source)
	if err != nil {
		return fmt.Errorf("invalid snapshot source: %w", err)
	}
	if source != DefaultVolumeSource {
		return fmt.Errorf("snapshot source must be %q", DefaultVolumeSource)
	}
	if strings.TrimSpace(request.Description) == "" {
		return fmt.Errorf("snapshot description is required")
	}
	if !validOperationID(request.OperationID) {
		return fmt.Errorf("snapshot operation id is invalid")
	}
	return nil
}

func validateRestoreRequest(request RestoreRequest) error {
	if !validPortableID(request.SnapshotID) {
		return fmt.Errorf("restore snapshot id is invalid")
	}
	if !validOperationID(request.OperationID) {
		return fmt.Errorf("restore operation id is invalid")
	}
	if request.StagingPath != localbackuppolicy.RestorePathForOperation(request.OperationID) {
		return fmt.Errorf("restore staging path differs from the governed operation target")
	}
	return nil
}

func validPortableID(value string) bool {
	return validOperationID(value)
}

func (e V2Engine) invoke(ctx context.Context, command []string, sensitiveInput []byte) (string, error) {
	if e.Exec == nil {
		return "", fmt.Errorf("native-v2 kopia executor is required")
	}
	argv := append([]string{
		"kopia",
		"--config-file", DefaultConfigFile,
	}, command...)
	out, err := e.Exec(ctx, argv, sensitiveInput)
	if err == nil {
		return redactSensitiveValue(out, sensitiveInput), nil
	}
	return redactSensitiveValue(out, sensitiveInput), &safeDiagnosticError{
		message: redactSensitiveValue(err.Error(), sensitiveInput),
	}
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

// ParseSnapshotCreate decodes the single object emitted by
// `kopia snapshot create --json`.
func ParseSnapshotCreate(raw string) (Snapshot, error) {
	var row snapshotJSONRow
	if err := decodePTYJSONObject(raw, &row); err != nil {
		return Snapshot{}, err
	}
	return row.snapshot()
}

type snapshotJSONRow struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	Source      struct {
		Path string `json:"path"`
		Host string `json:"host"`
	} `json:"source"`
	Tags      map[string]string `json:"tags"`
	StartTime time.Time         `json:"startTime"`
	EndTime   time.Time         `json:"endTime"`
	Stats     struct {
		TotalSize int64 `json:"totalSize"`
	} `json:"stats"`
	RootEntry struct {
		Summary struct {
			Size int64 `json:"size"`
		} `json:"summ"`
	} `json:"rootEntry"`
}

func (row snapshotJSONRow) snapshot() (Snapshot, error) {
	if strings.TrimSpace(row.ID) == "" {
		return Snapshot{}, fmt.Errorf("snapshot create result has no id")
	}
	totalSize := row.Stats.TotalSize
	if totalSize == 0 {
		totalSize = row.RootEntry.Summary.Size
	}
	return Snapshot{
		ID:          row.ID,
		SourcePath:  row.Source.Path,
		SourceHost:  row.Source.Host,
		Description: row.Description,
		OperationID: row.Tags["tag:stackkit.operation"],
		StartTime:   row.StartTime,
		EndTime:     row.EndTime,
		TotalSize:   totalSize,
	}, nil
}

func parseSnapshotListPTY(raw string) ([]Snapshot, error) {
	var rows []snapshotJSONRow
	if err := decodePTYJSONValue(raw, '[', &rows); err != nil {
		return nil, err
	}
	snapshots := make([]Snapshot, 0, len(rows))
	for _, row := range rows {
		snapshot, err := row.snapshot()
		if err != nil {
			return nil, err
		}
		snapshots = append(snapshots, snapshot)
	}
	return snapshots, nil
}

func decodePTYJSONObject(raw string, destination any) error {
	return decodePTYJSONValue(raw, '{', destination)
}

var ansiEscapePattern = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)

func decodePTYJSONValue(raw string, opening byte, destination any) error {
	raw = ansiEscapePattern.ReplaceAllString(raw, "")
	offset := strings.IndexByte(raw, opening)
	if offset < 0 {
		return fmt.Errorf("kopia PTY output has no JSON value")
	}
	if err := validatePTYPrefix(raw[:offset]); err != nil {
		return err
	}

	decoder := json.NewDecoder(strings.NewReader(raw[offset:]))
	var payload json.RawMessage
	if err := decoder.Decode(&payload); err != nil {
		return fmt.Errorf("decode kopia PTY JSON: %w", err)
	}
	if len(payload) == 0 || payload[0] != opening {
		return fmt.Errorf("kopia PTY JSON has unexpected shape")
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return fmt.Errorf("kopia PTY output contains trailing data")
	}
	if err := json.Unmarshal(payload, destination); err != nil {
		return fmt.Errorf("decode kopia PTY JSON shape: %w", err)
	}
	return nil
}

func validatePTYPrefix(prefix string) error {
	prefix = strings.ReplaceAll(prefix, "\r", "\n")
	for _, line := range strings.Split(prefix, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "":
		case strings.HasPrefix(line, "Enter password to open repository:"):
		case strings.HasPrefix(line, "Snapshotting "):
		case strings.Contains(line, " hashing,") && strings.Contains(line, " uploaded "):
		default:
			return fmt.Errorf("kopia PTY output contains unexpected prefix")
		}
	}
	return nil
}

func redactSensitiveValue(value string, sensitiveInput []byte) string {
	whole := bytes.TrimRight(sensitiveInput, "\r\n")
	if len(whole) == 0 {
		return value
	}
	redacted := bytes.ReplaceAll([]byte(value), whole, []byte("[REDACTED]"))
	for _, secret := range bytes.FieldsFunc(whole, func(char rune) bool {
		return char == '\r' || char == '\n'
	}) {
		if len(secret) > 0 {
			redacted = bytes.ReplaceAll(redacted, secret, []byte("[REDACTED]"))
		}
	}
	return string(redacted)
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
