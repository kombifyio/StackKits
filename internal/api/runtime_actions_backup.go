package api

// Node-side backup runtime actions (backup_run, backup_status,
// backup_restore, backup_wipe). TechStack orchestrates these over the
// service-auth channel; the node executes them against the local kopia-agent
// container through the shared backupexec engine — the exact argv the
// `stackkit backup` CLI speaks.
//
// backup_run always detaches: the response reports StatusAccepted with
// Backup.Phase "running" and callers poll backup_status until the phase
// reaches "completed" or "failed". This keeps every wait inside the global
// 15-minute phase policy while first content snapshots (potentially hours)
// keep running node-side. Run state survives in
// <BaseDir>/.stackkit/backup/run-state.json so a poll after a server restart
// sees an honest "interrupted" failure instead of a phantom running phase.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/kombifyio/stackkits/internal/backupexec"
	"github.com/kombifyio/stackkits/internal/backuphooks"
	skerrors "github.com/kombifyio/stackkits/internal/errors"
	"github.com/kombifyio/stackkits/internal/runtimeaction"
)

const (
	backupPhaseRunning   = "running"
	backupPhaseCompleted = "completed"
	backupPhaseFailed    = "failed"

	// backupDetachedRunBudget bounds a detached snapshot run. First content
	// snapshots legitimately exceed the 15-minute wait policy; the wait is
	// split into backup_status polls, not the snapshot itself.
	backupDetachedRunBudget = 6 * time.Hour

	backupEngineName = "kopia"
)

type backupRunState struct {
	RunID      string                         `json:"run_id"`
	Phase      string                         `json:"phase"`
	Classes    []string                       `json:"classes,omitempty"`
	StartedAt  time.Time                      `json:"started_at"`
	FinishedAt time.Time                      `json:"finished_at"`
	Error      string                         `json:"error,omitempty"`
	Snapshots  []runtimeaction.BackupSnapshot `json:"snapshots,omitempty"`
	Hooks      []backupexec.HookResult        `json:"hooks,omitempty"`
	// Operation records which verb holds/held the slot (backup_run,
	// backup_restore, backup_wipe).
	Operation string `json:"operation,omitempty"`
	// InProcess is never persisted: a state file claiming "running" without a
	// live in-process run means the server restarted mid-backup.
	InProcess bool `json:"-"`
}

func (s *Server) backupEngine() backupexec.Engine {
	if s.backupExec != nil {
		return backupexec.Engine{Exec: s.backupExec}
	}
	return backupexec.NewDockerEngine(backupexec.DefaultContainer)
}

// backupDetachedEngine lifts the per-call cap for detached snapshot runs.
func (s *Server) backupDetachedEngine() backupexec.Engine {
	if s.backupExec != nil {
		return backupexec.Engine{Exec: s.backupExec}
	}
	return backupexec.Engine{Exec: backupexec.DockerExecutorUncapped(backupexec.DefaultContainer)}
}

// backupHookExecutor runs pre-snapshot quiesce hooks against the database
// containers themselves (not the kopia-agent).
func (s *Server) backupHookExecutor() backupexec.ContainerExecutor {
	if s.hookExec != nil {
		return s.hookExec
	}
	return backupexec.DockerContainerExecutor()
}

func (s *Server) backupStatePath() string {
	base := s.config.BaseDir
	if base == "" {
		base = "."
	}
	return filepath.Join(base, ".stackkit", "backup", "run-state.json")
}

func (s *Server) loadBackupState() *backupRunState {
	s.backupMu.Lock()
	defer s.backupMu.Unlock()
	if s.backupState != nil {
		return s.backupState
	}
	raw, err := os.ReadFile(s.backupStatePath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		// An unreadable state file must not silently hide the last run's
		// outcome — surface it as a failed record.
		s.backupState = &backupRunState{Phase: backupPhaseFailed, Error: "backup run state unreadable: " + err.Error()}
		return s.backupState
	}
	var state backupRunState
	if err := json.Unmarshal(raw, &state); err != nil {
		s.backupState = &backupRunState{Phase: backupPhaseFailed, Error: "backup run state corrupt: " + err.Error()}
		return s.backupState
	}
	// A persisted "running" without a live in-process run is an interrupted
	// backup (server restart) — report it as failed, never as running.
	if state.Phase == backupPhaseRunning {
		state.Phase = backupPhaseFailed
		state.Error = "backup run interrupted by server restart"
	}
	s.backupState = &state
	return s.backupState
}

func (s *Server) storeBackupState(state *backupRunState) {
	// Memory update and file write share the mutex so the on-disk record can
	// never end up older than the in-memory one when writers interleave.
	s.backupMu.Lock()
	defer s.backupMu.Unlock()
	s.backupState = state
	s.persistBackupStateLocked(state)
}

func (s *Server) persistBackupStateLocked(state *backupRunState) {
	path := s.backupStatePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return
	}
	_ = os.WriteFile(path, raw, 0o600)
}

// claimBackupSlot atomically claims the node's single backup slot. The claim
// happens BEFORE any engine work (repo wiring can take minutes), closing the
// check-then-act window in which two concurrent operations could both pass;
// backup_run, backup_restore, and backup_wipe are all mutually exclusive.
// The caller must finish the claim via storeBackupState (completed/failed).
func (s *Server) claimBackupSlot(operation string, classes []string) (*backupRunState, *skerrors.StackKitError) {
	s.backupMu.Lock()
	defer s.backupMu.Unlock()
	if s.backupState != nil && s.backupState.Phase == backupPhaseRunning && s.backupState.InProcess {
		return nil, skerrors.NewDeploymentError(
			"backup_run_in_flight",
			"another backup operation is in progress on this node",
			skerrors.WithField("run_id", s.backupState.RunID),
			skerrors.WithField("operation", s.backupState.Operation),
			skerrors.WithSuggestion("Poll backup_status until the active operation completes"),
		)
	}
	state := &backupRunState{
		RunID:     uuid.NewString(),
		Phase:     backupPhaseRunning,
		Operation: operation,
		Classes:   classes,
		StartedAt: time.Now().UTC(),
		InProcess: true,
	}
	s.backupState = state
	s.persistBackupStateLocked(state)
	return state, nil
}

// failBackupClaim finishes a claim whose setup failed before any detached
// work started.
func (s *Server) failBackupClaim(state *backupRunState, reason string) {
	finished := *state
	finished.InProcess = false
	finished.Phase = backupPhaseFailed
	finished.Error = reason
	finished.FinishedAt = time.Now().UTC()
	s.storeBackupState(&finished)
}

// ensureBackupRepository wires the requested repository target, mirroring the
// CLI configure sequence. Credentials arrive by value inside the service-auth
// request and are never persisted or logged.
func (s *Server) ensureBackupRepository(ctx context.Context, engine backupexec.Engine, backup *runtimeaction.BackupRequest) *skerrors.StackKitError {
	repo := backup.Repo
	if repo == nil {
		out, err := engine.RepositoryStatusJSON(ctx)
		if err == nil && backupexec.StatusConfigured(out) {
			return nil
		}
		return skerrors.NewValidationError(
			"backup_repo_not_configured",
			"no backup repository is configured on this node and the request carries no repo target",
			skerrors.WithSuggestion("Send backup.repo (type s3 for kombify-managed R2 or BYO S3-compatible stores) on the first backup_run"),
		)
	}
	switch strings.ToLower(strings.TrimSpace(repo.Type)) {
	case "s3":
		if repo.Endpoint == "" || repo.Bucket == "" || repo.AccessKeyID == "" || repo.SecretAccessKey == "" {
			return skerrors.NewValidationError(
				"backup_repo_incomplete",
				"s3 backup repo target requires endpoint, bucket, access_key_id, and secret_access_key",
			)
		}
		if _, err := engine.EnsureS3Repository(ctx, backupexec.S3Repository{
			Endpoint:        repo.Endpoint,
			Bucket:          repo.Bucket,
			Region:          repo.Region,
			Prefix:          repo.Prefix,
			AccessKeyID:     repo.AccessKeyID,
			SecretAccessKey: repo.SecretAccessKey,
		}, backup.RepoPassword); err != nil {
			return skerrors.NewDeploymentError(
				"backup_repo_connect_failed",
				"failed to create or connect the s3 backup repository",
				skerrors.WithField("endpoint", repo.Endpoint),
				skerrors.WithField("bucket", repo.Bucket),
				skerrors.WithField("error", err.Error()),
			)
		}
		return nil
	case "local", "filesystem":
		path := strings.TrimSpace(repo.Prefix)
		if path == "" {
			path = "/backup/kopia"
		}
		if _, err := engine.EnsureFilesystemRepository(ctx, path); err != nil {
			return skerrors.NewDeploymentError("backup_repo_connect_failed", "failed to create or connect the local repository",
				skerrors.WithField("path", path), skerrors.WithField("error", err.Error()))
		}
		return nil
	default:
		return skerrors.NewValidationError(
			"unsupported_backup_repo_type",
			"backup repo type must be s3 (kombify-managed R2 or BYO) or local; b2/sftp land with the BYO slice",
			skerrors.WithField("type", repo.Type),
		)
	}
}

func (s *Server) runBackupRun(ctx context.Context, resp runtimeActionResponse, req runtimeActionRequest) (runtimeActionResponse, int, *skerrors.StackKitError) {
	backup := req.Backup
	if backup == nil {
		backup = &runtimeaction.BackupRequest{}
	}

	classes := append([]string(nil), backup.Classes...)
	state, conflict := s.claimBackupSlot(string(runtimeaction.ActionBackupRun), classes)
	if conflict != nil {
		resp.Status = runtimeaction.StatusFailed
		return resp, http.StatusConflict, conflict
	}

	setupCtx, cancel := context.WithTimeout(ctx, backupexec.LongOperationTimeout)
	defer cancel()
	engine := s.backupEngine()
	if repoErr := s.ensureBackupRepository(setupCtx, engine, backup); repoErr != nil {
		s.failBackupClaim(state, repoErr.Message)
		resp.Status = runtimeaction.StatusFailed
		resp.Checks = append(resp.Checks, runtimeActionCheck{Name: "backup_repo", Status: runtimeaction.CheckStatusFailed, Detail: repoErr.Message})
		httpStatus := http.StatusBadRequest
		if repoErr.Code == "backup_repo_connect_failed" {
			httpStatus = http.StatusBadGateway
		}
		return resp, httpStatus, repoErr
	}
	resp.Checks = append(resp.Checks, runtimeActionCheck{Name: "backup_repo", Status: runtimeaction.CheckStatusOK, Detail: "repository configured"})

	hookManifest, hooksErr := backupexec.LoadHookManifest(resp.TofuDir)
	if hooksErr != nil {
		// A present-but-unreadable manifest means the deployed artifacts are
		// broken; running without quiesce would silently produce torn
		// database backups, so fail closed.
		s.failBackupClaim(state, hooksErr.Error())
		resp.Status = runtimeaction.StatusFailed
		return resp, http.StatusBadRequest, skerrors.NewValidationError(
			"backup_hooks_invalid",
			"the deployed backup hook manifest is unreadable",
			skerrors.WithField("error", hooksErr.Error()),
		)
	}
	if hookManifest != nil {
		resp.Checks = append(resp.Checks, runtimeActionCheck{Name: "backup_hooks", Status: runtimeaction.CheckStatusOK, Detail: fmt.Sprintf("%d hook(s) declared", len(hookManifest.Hooks))})
	} else {
		resp.Checks = append(resp.Checks, runtimeActionCheck{Name: "backup_hooks", Status: runtimeaction.CheckStatusWarning, Detail: "no hook manifest in artifacts — volumes snapshot without database quiesce"})
	}

	// The run must outlive this request: keep the request context's values
	// (trace linkage) but drop its cancellation, then bound the detached run
	// by its own budget.
	detachedCtx := context.WithoutCancel(ctx)
	detached := s.backupDetachedEngine()
	go s.executeDetachedBackupRun(detachedCtx, detached, s.backupHookExecutor(), hookManifest, state)

	resp.Status = runtimeaction.StatusAccepted
	resp.Checks = append(resp.Checks, runtimeActionCheck{Name: "backup_run", Status: runtimeaction.CheckStatusOK, Detail: "detached snapshot run started: " + state.RunID})
	resp.Backup = &runtimeaction.BackupResult{
		Engine:  backupEngineName,
		Phase:   backupPhaseRunning,
		Classes: classes,
	}
	return resp, http.StatusOK, nil
}

func (s *Server) executeDetachedBackupRun(parent context.Context, engine backupexec.Engine, hookExec backupexec.ContainerExecutor, manifest *backuphooks.Manifest, state *backupRunState) {
	ctx, cancel := context.WithTimeout(parent, backupDetachedRunBudget)
	defer cancel()

	finished := *state
	finished.InProcess = false

	hookResults, hookErr := backupexec.RunPreSnapshotHooks(ctx, hookExec, manifest)
	finished.Hooks = hookResults
	if hookErr != nil {
		// Without a consistent dump the database class would be torn —
		// fail the run instead of snapshotting garbage.
		finished.Phase = backupPhaseFailed
		finished.Error = hookErr.Error()
		finished.FinishedAt = time.Now().UTC()
		s.storeBackupState(&finished)
		return
	}

	description := fmt.Sprintf("stackkit backup_run %s classes=%s", state.RunID, strings.Join(state.Classes, ","))
	_, snapErr := engine.Snapshot(ctx, backupexec.DefaultVolumeSource, description)

	finished.FinishedAt = time.Now().UTC()
	if snapErr != nil {
		finished.Phase = backupPhaseFailed
		finished.Error = snapErr.Error()
		s.storeBackupState(&finished)
		return
	}
	if raw, err := engine.ListSnapshotsJSON(ctx); err == nil {
		if snapshots, err := backupexec.ParseSnapshots(raw); err == nil {
			for _, snap := range snapshots {
				if snap.StartTime.Before(state.StartedAt.Add(-time.Minute)) {
					continue
				}
				finished.Snapshots = append(finished.Snapshots, backupSnapshotFromEngine(snap, state.Classes))
			}
		}
	}
	finished.Phase = backupPhaseCompleted
	s.storeBackupState(&finished)
}

func (s *Server) runBackupStatus(ctx context.Context, resp runtimeActionResponse) (runtimeActionResponse, int, *skerrors.StackKitError) {
	statusCtx, cancel := context.WithTimeout(ctx, backupexec.LongOperationTimeout)
	defer cancel()
	engine := s.backupEngine()

	out, err := engine.RepositoryStatusJSON(statusCtx)
	if err != nil || !backupexec.StatusConfigured(out) {
		if err != nil && !backupexec.OutputLooksNotConfigured(out, err) {
			resp.Status = runtimeaction.StatusFailed
			return resp, http.StatusBadGateway, skerrors.NewDeploymentError(
				"backup_status_unavailable",
				"kopia repository status check failed",
				skerrors.WithField("error", err.Error()),
			)
		}
		// Not configured is a legitimate answer, not an error: the caller
		// learns the node is reachable and backup is simply not enabled yet.
		resp.Status = runtimeaction.StatusReady
		resp.Checks = append(resp.Checks, runtimeActionCheck{Name: "backup_repo", Status: runtimeaction.CheckStatusMissing, Detail: "no kopia repository configured"})
		return resp, http.StatusOK, nil
	}
	resp.Checks = append(resp.Checks, runtimeActionCheck{Name: "backup_repo", Status: runtimeaction.CheckStatusOK, Detail: "repository configured"})

	result := &runtimeaction.BackupResult{Engine: backupEngineName}
	if state := s.loadBackupState(); state != nil {
		result.Phase = state.Phase
		result.Classes = state.Classes
		if state.Error != "" {
			resp.Checks = append(resp.Checks, runtimeActionCheck{Name: "backup_run", Status: runtimeaction.CheckStatusFailed, Detail: state.Error})
		}
		for _, hook := range state.Hooks {
			status := runtimeaction.CheckStatusOK
			switch hook.Status {
			case backupexec.HookStatusSkipped:
				status = runtimeaction.CheckStatusSkipped
			case backupexec.HookStatusFailed:
				status = runtimeaction.CheckStatusFailed
			}
			resp.Checks = append(resp.Checks, runtimeActionCheck{
				Name:   "backup_hook_" + hook.Container,
				Status: status,
				Detail: hook.Detail,
			})
		}
	}

	if raw, listErr := engine.ListSnapshotsJSON(statusCtx); listErr == nil {
		if snapshots, parseErr := backupexec.ParseSnapshots(raw); parseErr == nil {
			result.RepoSizeBytes = latestLogicalRepoSize(snapshots)
			for _, snap := range snapshots {
				result.Snapshots = append(result.Snapshots, backupSnapshotFromEngine(snap, nil))
			}
		}
	} else {
		resp.Checks = append(resp.Checks, runtimeActionCheck{Name: "backup_snapshots", Status: runtimeaction.CheckStatusWarning, Detail: listErr.Error()})
	}

	resp.Status = runtimeaction.StatusVerified
	resp.Backup = result
	return resp, http.StatusOK, nil
}

func (s *Server) runBackupRestore(ctx context.Context, resp runtimeActionResponse, req runtimeActionRequest) (runtimeActionResponse, int, *skerrors.StackKitError) {
	backup := req.Backup
	if backup == nil || strings.TrimSpace(backup.SnapshotID) == "" {
		resp.Status = runtimeaction.StatusFailed
		return resp, http.StatusBadRequest, skerrors.NewValidationError(
			"missing_snapshot_id",
			"backup_restore requires backup.snapshot_id",
		)
	}
	state, conflict := s.claimBackupSlot(string(runtimeaction.ActionBackupRestore), nil)
	if conflict != nil {
		resp.Status = runtimeaction.StatusFailed
		return resp, http.StatusConflict, conflict
	}

	restoreCtx, cancel := context.WithTimeout(ctx, backupexec.LongOperationTimeout)
	defer cancel()
	engine := s.backupEngine()

	// Staged restore: materialize the snapshot into the agent-local staging
	// directory. The in-place quiesce restore (stop containers -> restore
	// volumes -> db hooks -> start) is the Phase-4 slice — it needs an RW
	// volume path the RO kopia-agent mount deliberately does not have.
	target := "/tmp/stackkit-restore/" + strings.TrimSpace(backup.SnapshotID)
	if _, err := engine.Mkdir(restoreCtx, target); err != nil {
		s.failBackupClaim(state, err.Error())
		resp.Status = runtimeaction.StatusFailed
		return resp, http.StatusBadGateway, skerrors.NewDeploymentError(
			"backup_restore_failed",
			"failed to prepare restore staging directory",
			skerrors.WithField("target", target),
			skerrors.WithField("error", err.Error()),
		)
	}
	if out, err := engine.Restore(restoreCtx, backup.SnapshotID, target); err != nil {
		s.failBackupClaim(state, err.Error())
		resp.Status = runtimeaction.StatusFailed
		return resp, http.StatusBadGateway, skerrors.NewDeploymentError(
			"backup_restore_failed",
			"kopia snapshot restore failed",
			skerrors.WithField("snapshot_id", backup.SnapshotID),
			skerrors.WithField("error", err.Error()),
			skerrors.WithField("output", truncateForCheck(out)),
		)
	}
	s.finishBackupClaim(state)

	resp.Status = runtimeaction.StatusApplied
	resp.Checks = append(resp.Checks, runtimeActionCheck{Name: "backup_restore", Status: runtimeaction.CheckStatusOK, Detail: "restored " + backup.SnapshotID + " to " + target})
	resp.Backup = &runtimeaction.BackupResult{Engine: backupEngineName, Phase: backupPhaseCompleted}
	return resp, http.StatusOK, nil
}

func (s *Server) runBackupWipe(ctx context.Context, resp runtimeActionResponse, req runtimeActionRequest) (runtimeActionResponse, int, *skerrors.StackKitError) {
	backup := req.Backup
	// Typed confirmation, fail-closed: wipe only proceeds when the caller
	// echoes the exact stack ID (contract: BackupRequest.Confirm).
	if backup == nil || backup.Confirm != req.StackID {
		resp.Status = runtimeaction.StatusFailed
		return resp, http.StatusBadRequest, skerrors.NewValidationError(
			"backup_wipe_confirmation_mismatch",
			"backup_wipe requires backup.confirm to equal the stack_id",
			skerrors.WithField("stack_id", req.StackID),
		)
	}
	state, conflict := s.claimBackupSlot(string(runtimeaction.ActionBackupWipe), nil)
	if conflict != nil {
		resp.Status = runtimeaction.StatusFailed
		return resp, http.StatusConflict, conflict
	}

	wipeCtx, cancel := context.WithTimeout(ctx, backupexec.LongOperationTimeout)
	defer cancel()
	engine := s.backupEngine()

	out, err := engine.RepositoryStatusJSON(wipeCtx)
	if err != nil || !backupexec.StatusConfigured(out) {
		if err != nil && !backupexec.OutputLooksNotConfigured(out, err) {
			s.failBackupClaim(state, err.Error())
			resp.Status = runtimeaction.StatusFailed
			return resp, http.StatusBadGateway, skerrors.NewDeploymentError(
				"backup_wipe_failed",
				"kopia repository status check failed before wipe",
				skerrors.WithField("error", err.Error()),
			)
		}
		s.finishBackupClaim(state)
		resp.Status = runtimeaction.StatusSkipped
		resp.Checks = append(resp.Checks, runtimeActionCheck{Name: "backup_wipe", Status: runtimeaction.CheckStatusSkipped, Detail: "no kopia repository configured — nothing to wipe"})
		resp.Backup = &runtimeaction.BackupResult{Engine: backupEngineName, Wiped: false}
		return resp, http.StatusOK, nil
	}

	raw, listErr := engine.ListSnapshotsJSON(wipeCtx)
	if listErr != nil {
		s.failBackupClaim(state, listErr.Error())
		resp.Status = runtimeaction.StatusFailed
		return resp, http.StatusBadGateway, skerrors.NewDeploymentError(
			"backup_wipe_failed", "failed to list snapshots before wipe",
			skerrors.WithField("error", listErr.Error()),
		)
	}
	snapshots, parseErr := backupexec.ParseSnapshots(raw)
	if parseErr != nil {
		s.failBackupClaim(state, parseErr.Error())
		resp.Status = runtimeaction.StatusFailed
		return resp, http.StatusBadGateway, skerrors.NewDeploymentError(
			"backup_wipe_failed", "failed to parse snapshot list before wipe",
			skerrors.WithField("error", parseErr.Error()),
		)
	}
	ids := make([]string, 0, len(snapshots))
	for _, snap := range snapshots {
		if snap.ID != "" {
			ids = append(ids, snap.ID)
		}
	}
	const deleteBatch = 50
	for start := 0; start < len(ids); start += deleteBatch {
		end := start + deleteBatch
		if end > len(ids) {
			end = len(ids)
		}
		if delOut, delErr := engine.DeleteSnapshots(wipeCtx, ids[start:end]); delErr != nil {
			s.failBackupClaim(state, delErr.Error())
			resp.Status = runtimeaction.StatusFailed
			return resp, http.StatusBadGateway, skerrors.NewDeploymentError(
				"backup_wipe_failed", "failed to delete snapshots",
				skerrors.WithField("error", delErr.Error()),
				skerrors.WithField("output", truncateForCheck(delOut)),
			)
		}
	}
	resp.Checks = append(resp.Checks, runtimeActionCheck{Name: "backup_wipe_snapshots", Status: runtimeaction.CheckStatusOK, Detail: fmt.Sprintf("%d snapshot(s) deleted", len(ids))})

	// Best-effort: maintenance reclaims blobs; disconnect drops credentials
	// from the node's kopia config. Failures degrade to warnings — the
	// control plane deletes the managed R2 bucket afterwards regardless.
	if _, err := engine.MaintenanceRunFull(wipeCtx); err != nil {
		resp.Checks = append(resp.Checks, runtimeActionCheck{Name: "backup_wipe_maintenance", Status: runtimeaction.CheckStatusWarning, Detail: err.Error()})
	} else {
		resp.Checks = append(resp.Checks, runtimeActionCheck{Name: "backup_wipe_maintenance", Status: runtimeaction.CheckStatusOK, Detail: "full maintenance completed"})
	}
	if _, err := engine.Disconnect(wipeCtx); err != nil {
		resp.Checks = append(resp.Checks, runtimeActionCheck{Name: "backup_wipe_disconnect", Status: runtimeaction.CheckStatusWarning, Detail: err.Error()})
	} else {
		resp.Checks = append(resp.Checks, runtimeActionCheck{Name: "backup_wipe_disconnect", Status: runtimeaction.CheckStatusOK, Detail: "repository disconnected"})
	}

	s.finishBackupClaim(state)

	resp.Status = runtimeaction.StatusApplied
	resp.Backup = &runtimeaction.BackupResult{Engine: backupEngineName, Wiped: true}
	return resp, http.StatusOK, nil
}

// finishBackupClaim completes a synchronous operation's slot claim.
func (s *Server) finishBackupClaim(state *backupRunState) {
	finished := *state
	finished.InProcess = false
	finished.Phase = backupPhaseCompleted
	finished.FinishedAt = time.Now().UTC()
	s.storeBackupState(&finished)
}

func backupSnapshotFromEngine(snap backupexec.Snapshot, classes []string) runtimeaction.BackupSnapshot {
	out := runtimeaction.BackupSnapshot{
		ID:         snap.ID,
		Source:     snap.SourcePath,
		Classes:    classes,
		TotalBytes: snap.TotalSize,
	}
	if !snap.StartTime.IsZero() {
		out.StartedAt = snap.StartTime.UTC().Format(time.RFC3339)
	}
	if !snap.EndTime.IsZero() {
		out.FinishedAt = snap.EndTime.UTC().Format(time.RFC3339)
	}
	return out
}

// latestLogicalRepoSize approximates the repository's current logical size as
// the sum of the newest snapshot per source path. That is what the user
// "stores" and what TechStack meters against the tier quota; physical
// (deduplicated) size refinement lands with the control-plane R2 metering.
func latestLogicalRepoSize(snapshots []backupexec.Snapshot) int64 {
	latest := make(map[string]backupexec.Snapshot)
	for _, snap := range snapshots {
		current, ok := latest[snap.SourcePath]
		if !ok || snap.StartTime.After(current.StartTime) {
			latest[snap.SourcePath] = snap
		}
	}
	sources := make([]string, 0, len(latest))
	for source := range latest {
		sources = append(sources, source)
	}
	sort.Strings(sources)
	var total int64
	for _, source := range sources {
		total += latest[source].TotalSize
	}
	return total
}

func truncateForCheck(out string) string {
	out = strings.TrimSpace(out)
	if len(out) > 400 {
		return out[:400] + "…"
	}
	return out
}
