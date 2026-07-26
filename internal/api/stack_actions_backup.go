package api

// Node-side backup StackActions (backup_run, backup_status,
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
	stackaction "github.com/kombifyio/stackkits/internal/stackaction"
)

const (
	backupPhaseRunningStackAction   = "running"
	backupPhaseCompletedStackAction = "completed"
	backupPhaseFailedStackAction    = "failed"

	// backupDetachedRunBudget bounds a detached snapshot run. First content
	// snapshots legitimately exceed the 15-minute wait policy; the wait is
	// split into backup_status polls, not the snapshot itself.
	backupDetachedRunBudgetStackAction = 6 * time.Hour

	backupEngineNameStackAction = "kopia"
)

type backupRunStateStackAction struct {
	RunID      string                       `json:"run_id"`
	Phase      string                       `json:"phase"`
	Classes    []string                     `json:"classes,omitempty"`
	StartedAt  time.Time                    `json:"started_at"`
	FinishedAt time.Time                    `json:"finished_at"`
	Error      string                       `json:"error,omitempty"`
	Snapshots  []stackaction.BackupSnapshot `json:"snapshots,omitempty"`
	Hooks      []backupexec.HookResult      `json:"hooks,omitempty"`
	// Operation records which verb holds/held the slot (backup_run,
	// backup_restore, backup_wipe).
	Operation string `json:"operation,omitempty"`
	// InProcess is never persisted: a state file claiming "running" without a
	// live in-process run means the server restarted mid-backup.
	InProcess bool `json:"-"`
}

func (s *Server) backupEngineStackAction() backupexec.Engine {
	if s.backupExec != nil {
		return backupexec.Engine{Exec: s.backupExec}
	}
	return backupexec.NewDockerEngine(backupexec.DefaultContainer)
}

// backupDetachedEngine lifts the per-call cap for detached snapshot runs.
func (s *Server) backupDetachedEngineStackAction() backupexec.Engine {
	if s.backupExec != nil {
		return backupexec.Engine{Exec: s.backupExec}
	}
	return backupexec.Engine{Exec: backupexec.DockerExecutorUncapped(backupexec.DefaultContainer)}
}

// backupHookExecutor runs pre-snapshot quiesce hooks against the database
// containers themselves (not the kopia-agent).
func (s *Server) backupHookExecutorStackAction() backupexec.ContainerExecutor {
	if s.hookExec != nil {
		return s.hookExec
	}
	return backupexec.DockerContainerExecutor()
}

func (s *Server) backupStatePathStackAction() string {
	base := s.config.BaseDir
	if base == "" {
		base = "."
	}
	return filepath.Join(base, ".stackkit", "backup", "run-state.json")
}

func (s *Server) loadBackupStateStackAction() *backupRunStateStackAction {
	s.backupMu.Lock()
	defer s.backupMu.Unlock()
	if s.stackActionBackupState != nil {
		return s.stackActionBackupState
	}
	raw, err := os.ReadFile(s.backupStatePathStackAction())
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		// An unreadable state file must not silently hide the last run's
		// outcome — surface it as a failed record.
		s.stackActionBackupState = &backupRunStateStackAction{Phase: backupPhaseFailedStackAction, Error: "backup run state unreadable: " + err.Error()}
		return s.stackActionBackupState
	}
	var state backupRunStateStackAction
	if err := json.Unmarshal(raw, &state); err != nil {
		s.stackActionBackupState = &backupRunStateStackAction{Phase: backupPhaseFailedStackAction, Error: "backup run state corrupt: " + err.Error()}
		return s.stackActionBackupState
	}
	// A persisted "running" without a live in-process run is an interrupted
	// backup (server restart) — report it as failed, never as running.
	if state.Phase == backupPhaseRunningStackAction {
		state.Phase = backupPhaseFailedStackAction
		state.Error = "backup run interrupted by server restart"
	}
	s.stackActionBackupState = &state
	return s.stackActionBackupState
}

func (s *Server) storeBackupStateStackAction(state *backupRunStateStackAction) {
	// Memory update and file write share the mutex so the on-disk record can
	// never end up older than the in-memory one when writers interleave.
	s.backupMu.Lock()
	defer s.backupMu.Unlock()
	s.stackActionBackupState = state
	s.persistBackupStateLockedStackAction(state)
}

func (s *Server) persistBackupStateLockedStackAction(state *backupRunStateStackAction) {
	path := s.backupStatePathStackAction()
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
func (s *Server) claimBackupSlotStackAction(operation string, classes []string) (*backupRunStateStackAction, *skerrors.StackKitError) {
	s.backupMu.Lock()
	defer s.backupMu.Unlock()
	if s.stackActionBackupState != nil && s.stackActionBackupState.Phase == backupPhaseRunningStackAction && s.stackActionBackupState.InProcess {
		return nil, skerrors.NewDeploymentError(
			"backup_run_in_flight",
			"another backup operation is in progress on this node",
			skerrors.WithField("run_id", s.stackActionBackupState.RunID),
			skerrors.WithField("operation", s.stackActionBackupState.Operation),
			skerrors.WithSuggestion("Poll backup_status until the active operation completes"),
		)
	}
	state := &backupRunStateStackAction{
		RunID:     uuid.NewString(),
		Phase:     backupPhaseRunningStackAction,
		Operation: operation,
		Classes:   classes,
		StartedAt: time.Now().UTC(),
		InProcess: true,
	}
	s.stackActionBackupState = state
	s.persistBackupStateLockedStackAction(state)
	return state, nil
}

// failBackupClaim finishes a claim whose setup failed before any detached
// work started.
func (s *Server) failBackupClaimStackAction(state *backupRunStateStackAction, reason string) {
	finished := *state
	finished.InProcess = false
	finished.Phase = backupPhaseFailedStackAction
	finished.Error = reason
	finished.FinishedAt = time.Now().UTC()
	s.storeBackupStateStackAction(&finished)
}

// ensureBackupRepository wires the requested repository target. The public
// request contains only an opaque credential reference; raw material exists
// only behind the internal resolver seam and is never persisted or logged.
func (s *Server) ensureBackupRepositoryStackAction(ctx context.Context, engine backupexec.Engine, backup *stackaction.BackupRequest) *skerrors.StackKitError {
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
		if repo.Endpoint == "" || repo.Bucket == "" || repo.CredentialRef == nil {
			return skerrors.NewValidationError(
				"backup_repo_incomplete",
				"s3 backup repo target requires endpoint, bucket, and credential_ref",
			)
		}
		credentialRef, refErr := normalizeStackActionReference(repo.CredentialRef, stackActionScopeBackupRepository, time.Now())
		if refErr != nil {
			return skerrors.NewValidationError("invalid_backup_credential_ref", "backup credential_ref is invalid",
				skerrors.WithField("error", refErr.Error()))
		}
		if s.stackActionReferenceResolver == nil {
			return skerrors.NewDeploymentError("stackaction_reference_resolver_unavailable", "StackAction reference resolver is not configured")
		}
		credential, resolveErr := s.stackActionReferenceResolver.ResolveBackupCredential(ctx, *credentialRef)
		if resolveErr != nil {
			return skerrors.NewDeploymentError("backup_credential_resolution_failed", "failed to resolve backup credential_ref",
				skerrors.WithField("error", resolveErr.Error()))
		}
		defer func() {
			credential.AccessKeyID = ""
			credential.SecretAccessKey = ""
			credential.RepositoryPassword = ""
		}()
		if strings.TrimSpace(credential.AccessKeyID) == "" || strings.TrimSpace(credential.SecretAccessKey) == "" || credential.RepositoryPassword == "" {
			return skerrors.NewDeploymentError("backup_credential_incomplete", "resolved backup credential is incomplete")
		}
		if _, err := engine.EnsureS3Repository(ctx, backupexec.S3Repository{
			Endpoint:        repo.Endpoint,
			Bucket:          repo.Bucket,
			Region:          repo.Region,
			Prefix:          repo.Prefix,
			AccessKeyID:     credential.AccessKeyID,
			SecretAccessKey: credential.SecretAccessKey,
		}, credential.RepositoryPassword); err != nil {
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

func (s *Server) runBackupRunStackAction(ctx context.Context, resp stackActionResponse, req stackActionRequest) (stackActionResponse, int, *skerrors.StackKitError) {
	backup := req.Backup
	if backup == nil {
		backup = &stackaction.BackupRequest{}
	}

	classes := append([]string(nil), backup.Classes...)
	state, conflict := s.claimBackupSlotStackAction(string(stackaction.ActionBackupRun), classes)
	if conflict != nil {
		resp.Status = stackaction.StatusFailed
		return resp, http.StatusConflict, conflict
	}

	setupCtx, cancel := context.WithTimeout(ctx, backupexec.LongOperationTimeout)
	defer cancel()
	engine := s.backupEngineStackAction()
	if repoErr := s.ensureBackupRepositoryStackAction(setupCtx, engine, backup); repoErr != nil {
		s.failBackupClaimStackAction(state, repoErr.Message)
		resp.Status = stackaction.StatusFailed
		resp.Checks = append(resp.Checks, stackActionCheck{Name: "backup_repo", Status: stackaction.CheckStatusFailed, Detail: repoErr.Message})
		httpStatus := http.StatusBadRequest
		if repoErr.Code == "backup_repo_connect_failed" {
			httpStatus = http.StatusBadGateway
		}
		return resp, httpStatus, repoErr
	}
	resp.Checks = append(resp.Checks, stackActionCheck{Name: "backup_repo", Status: stackaction.CheckStatusOK, Detail: "repository configured"})

	hookManifest, hooksErr := backupexec.LoadHookManifest(resp.TofuDir)
	if hooksErr != nil {
		// A present-but-unreadable manifest means the deployed artifacts are
		// broken; running without quiesce would silently produce torn
		// database backups, so fail closed.
		s.failBackupClaimStackAction(state, hooksErr.Error())
		resp.Status = stackaction.StatusFailed
		return resp, http.StatusBadRequest, skerrors.NewValidationError(
			"backup_hooks_invalid",
			"the deployed backup hook manifest is unreadable",
			skerrors.WithField("error", hooksErr.Error()),
		)
	}
	if hookManifest != nil {
		resp.Checks = append(resp.Checks, stackActionCheck{Name: "backup_hooks", Status: stackaction.CheckStatusOK, Detail: fmt.Sprintf("%d hook(s) declared", len(hookManifest.Hooks))})
	} else {
		resp.Checks = append(resp.Checks, stackActionCheck{Name: "backup_hooks", Status: stackaction.CheckStatusWarning, Detail: "no hook manifest in artifacts — volumes snapshot without database quiesce"})
	}

	// The run must outlive this request: keep the request context's values
	// (trace linkage) but drop its cancellation, then bound the detached run
	// by its own budget.
	detachedCtx := context.WithoutCancel(ctx)
	detached := s.backupDetachedEngineStackAction()
	go s.executeDetachedBackupRunStackAction(detachedCtx, detached, s.backupHookExecutorStackAction(), hookManifest, state)

	resp.Status = stackaction.StatusAccepted
	resp.Checks = append(resp.Checks, stackActionCheck{Name: "backup_run", Status: stackaction.CheckStatusOK, Detail: "detached snapshot run started: " + state.RunID})
	resp.Backup = &stackaction.BackupResult{
		Engine:  backupEngineNameStackAction,
		Phase:   backupPhaseRunningStackAction,
		Classes: classes,
	}
	return resp, http.StatusOK, nil
}

func (s *Server) executeDetachedBackupRunStackAction(parent context.Context, engine backupexec.Engine, hookExec backupexec.ContainerExecutor, manifest *backuphooks.Manifest, state *backupRunStateStackAction) {
	ctx, cancel := context.WithTimeout(parent, backupDetachedRunBudgetStackAction)
	defer cancel()

	finished := *state
	finished.InProcess = false

	hookResults, hookErr := backupexec.RunPreSnapshotHooks(ctx, hookExec, manifest)
	finished.Hooks = hookResults
	if hookErr != nil {
		// Without a consistent dump the database class would be torn —
		// fail the run instead of snapshotting garbage.
		finished.Phase = backupPhaseFailedStackAction
		finished.Error = hookErr.Error()
		finished.FinishedAt = time.Now().UTC()
		s.storeBackupStateStackAction(&finished)
		return
	}

	description := fmt.Sprintf("stackkit backup_run %s classes=%s", state.RunID, strings.Join(state.Classes, ","))
	_, snapErr := engine.Snapshot(ctx, backupexec.DefaultVolumeSource, description)

	finished.FinishedAt = time.Now().UTC()
	if snapErr != nil {
		finished.Phase = backupPhaseFailedStackAction
		finished.Error = snapErr.Error()
		s.storeBackupStateStackAction(&finished)
		return
	}
	if raw, err := engine.ListSnapshotsJSON(ctx); err == nil {
		if snapshots, err := backupexec.ParseSnapshots(raw); err == nil {
			for _, snap := range snapshots {
				if snap.StartTime.Before(state.StartedAt.Add(-time.Minute)) {
					continue
				}
				finished.Snapshots = append(finished.Snapshots, backupSnapshotFromEngineStackAction(snap, state.Classes))
			}
		}
	}
	finished.Phase = backupPhaseCompletedStackAction
	s.storeBackupStateStackAction(&finished)
}

func (s *Server) runBackupStatusStackAction(ctx context.Context, resp stackActionResponse) (stackActionResponse, int, *skerrors.StackKitError) {
	statusCtx, cancel := context.WithTimeout(ctx, backupexec.LongOperationTimeout)
	defer cancel()
	engine := s.backupEngineStackAction()

	out, err := engine.RepositoryStatusJSON(statusCtx)
	if err != nil || !backupexec.StatusConfigured(out) {
		if err != nil && !backupexec.OutputLooksNotConfigured(out, err) {
			resp.Status = stackaction.StatusFailed
			return resp, http.StatusBadGateway, skerrors.NewDeploymentError(
				"backup_status_unavailable",
				"kopia repository status check failed",
				skerrors.WithField("error", err.Error()),
			)
		}
		// Not configured is a legitimate answer, not an error: the caller
		// learns the node is reachable and backup is simply not enabled yet.
		resp.Status = stackaction.StatusReady
		resp.Checks = append(resp.Checks, stackActionCheck{Name: "backup_repo", Status: stackaction.CheckStatusMissing, Detail: "no kopia repository configured"})
		return resp, http.StatusOK, nil
	}
	resp.Checks = append(resp.Checks, stackActionCheck{Name: "backup_repo", Status: stackaction.CheckStatusOK, Detail: "repository configured"})

	result := &stackaction.BackupResult{Engine: backupEngineNameStackAction}
	if state := s.loadBackupStateStackAction(); state != nil {
		result.Phase = state.Phase
		result.Classes = state.Classes
		if state.Error != "" {
			resp.Checks = append(resp.Checks, stackActionCheck{Name: "backup_run", Status: stackaction.CheckStatusFailed, Detail: state.Error})
		}
		for _, hook := range state.Hooks {
			status := stackaction.CheckStatusOK
			switch hook.Status {
			case backupexec.HookStatusSkipped:
				status = stackaction.CheckStatusSkipped
			case backupexec.HookStatusFailed:
				status = stackaction.CheckStatusFailed
			}
			resp.Checks = append(resp.Checks, stackActionCheck{
				Name:   "backup_hook_" + hook.Container,
				Status: status,
				Detail: hook.Detail,
			})
		}
	}

	if raw, listErr := engine.ListSnapshotsJSON(statusCtx); listErr == nil {
		if snapshots, parseErr := backupexec.ParseSnapshots(raw); parseErr == nil {
			result.RepoSizeBytes = latestLogicalRepoSizeStackAction(snapshots)
			for _, snap := range snapshots {
				result.Snapshots = append(result.Snapshots, backupSnapshotFromEngineStackAction(snap, nil))
			}
		}
	} else {
		resp.Checks = append(resp.Checks, stackActionCheck{Name: "backup_snapshots", Status: stackaction.CheckStatusWarning, Detail: listErr.Error()})
	}

	resp.Status = stackaction.StatusVerified
	resp.Backup = result
	return resp, http.StatusOK, nil
}

func (s *Server) runBackupRestoreStackAction(ctx context.Context, resp stackActionResponse, req stackActionRequest) (stackActionResponse, int, *skerrors.StackKitError) {
	backup := req.Backup
	if backup == nil || strings.TrimSpace(backup.SnapshotID) == "" {
		resp.Status = stackaction.StatusFailed
		return resp, http.StatusBadRequest, skerrors.NewValidationError(
			"missing_snapshot_id",
			"backup_restore requires backup.snapshot_id",
		)
	}
	state, conflict := s.claimBackupSlotStackAction(string(stackaction.ActionBackupRestore), nil)
	if conflict != nil {
		resp.Status = stackaction.StatusFailed
		return resp, http.StatusConflict, conflict
	}

	restoreCtx, cancel := context.WithTimeout(ctx, backupexec.LongOperationTimeout)
	defer cancel()
	engine := s.backupEngineStackAction()

	// Staged restore: materialize the snapshot into the agent-local staging
	// directory. The in-place quiesce restore (stop containers -> restore
	// volumes -> db hooks -> start) is the Phase-4 slice — it needs an RW
	// volume path the RO kopia-agent mount deliberately does not have.
	target := "/tmp/stackkit-restore/" + strings.TrimSpace(backup.SnapshotID)
	if _, err := engine.Mkdir(restoreCtx, target); err != nil {
		s.failBackupClaimStackAction(state, err.Error())
		resp.Status = stackaction.StatusFailed
		return resp, http.StatusBadGateway, skerrors.NewDeploymentError(
			"backup_restore_failed",
			"failed to prepare restore staging directory",
			skerrors.WithField("target", target),
			skerrors.WithField("error", err.Error()),
		)
	}
	if out, err := engine.Restore(restoreCtx, backup.SnapshotID, target); err != nil {
		s.failBackupClaimStackAction(state, err.Error())
		resp.Status = stackaction.StatusFailed
		return resp, http.StatusBadGateway, skerrors.NewDeploymentError(
			"backup_restore_failed",
			"kopia snapshot restore failed",
			skerrors.WithField("snapshot_id", backup.SnapshotID),
			skerrors.WithField("error", err.Error()),
			skerrors.WithField("output", truncateForCheckStackAction(out)),
		)
	}
	s.finishBackupClaimStackAction(state)

	resp.Status = stackaction.StatusApplied
	resp.Checks = append(resp.Checks, stackActionCheck{Name: "backup_restore", Status: stackaction.CheckStatusOK, Detail: "restored " + backup.SnapshotID + " to " + target})
	resp.Backup = &stackaction.BackupResult{Engine: backupEngineNameStackAction, Phase: backupPhaseCompletedStackAction}
	return resp, http.StatusOK, nil
}

func (s *Server) runBackupWipeStackAction(ctx context.Context, resp stackActionResponse, req stackActionRequest) (stackActionResponse, int, *skerrors.StackKitError) {
	backup := req.Backup
	// Typed confirmation, fail-closed: wipe only proceeds when the caller
	// echoes the exact stack ID (contract: BackupRequest.Confirm).
	if backup == nil || backup.Confirm != req.StackID {
		resp.Status = stackaction.StatusFailed
		return resp, http.StatusBadRequest, skerrors.NewValidationError(
			"backup_wipe_confirmation_mismatch",
			"backup_wipe requires backup.confirm to equal the stack_id",
			skerrors.WithField("stack_id", req.StackID),
		)
	}
	state, conflict := s.claimBackupSlotStackAction(string(stackaction.ActionBackupWipe), nil)
	if conflict != nil {
		resp.Status = stackaction.StatusFailed
		return resp, http.StatusConflict, conflict
	}

	wipeCtx, cancel := context.WithTimeout(ctx, backupexec.LongOperationTimeout)
	defer cancel()
	engine := s.backupEngineStackAction()

	out, err := engine.RepositoryStatusJSON(wipeCtx)
	if err != nil || !backupexec.StatusConfigured(out) {
		if err != nil && !backupexec.OutputLooksNotConfigured(out, err) {
			s.failBackupClaimStackAction(state, err.Error())
			resp.Status = stackaction.StatusFailed
			return resp, http.StatusBadGateway, skerrors.NewDeploymentError(
				"backup_wipe_failed",
				"kopia repository status check failed before wipe",
				skerrors.WithField("error", err.Error()),
			)
		}
		s.finishBackupClaimStackAction(state)
		resp.Status = stackaction.StatusSkipped
		resp.Checks = append(resp.Checks, stackActionCheck{Name: "backup_wipe", Status: stackaction.CheckStatusSkipped, Detail: "no kopia repository configured — nothing to wipe"})
		resp.Backup = &stackaction.BackupResult{Engine: backupEngineNameStackAction, Wiped: false}
		return resp, http.StatusOK, nil
	}

	raw, listErr := engine.ListSnapshotsJSON(wipeCtx)
	if listErr != nil {
		s.failBackupClaimStackAction(state, listErr.Error())
		resp.Status = stackaction.StatusFailed
		return resp, http.StatusBadGateway, skerrors.NewDeploymentError(
			"backup_wipe_failed", "failed to list snapshots before wipe",
			skerrors.WithField("error", listErr.Error()),
		)
	}
	snapshots, parseErr := backupexec.ParseSnapshots(raw)
	if parseErr != nil {
		s.failBackupClaimStackAction(state, parseErr.Error())
		resp.Status = stackaction.StatusFailed
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
			s.failBackupClaimStackAction(state, delErr.Error())
			resp.Status = stackaction.StatusFailed
			return resp, http.StatusBadGateway, skerrors.NewDeploymentError(
				"backup_wipe_failed", "failed to delete snapshots",
				skerrors.WithField("error", delErr.Error()),
				skerrors.WithField("output", truncateForCheckStackAction(delOut)),
			)
		}
	}
	resp.Checks = append(resp.Checks, stackActionCheck{Name: "backup_wipe_snapshots", Status: stackaction.CheckStatusOK, Detail: fmt.Sprintf("%d snapshot(s) deleted", len(ids))})

	// Best-effort: maintenance reclaims blobs; disconnect drops credentials
	// from the node's kopia config. Failures degrade to warnings — the
	// control plane deletes the managed R2 bucket afterwards regardless.
	if _, err := engine.MaintenanceRunFull(wipeCtx); err != nil {
		resp.Checks = append(resp.Checks, stackActionCheck{Name: "backup_wipe_maintenance", Status: stackaction.CheckStatusWarning, Detail: err.Error()})
	} else {
		resp.Checks = append(resp.Checks, stackActionCheck{Name: "backup_wipe_maintenance", Status: stackaction.CheckStatusOK, Detail: "full maintenance completed"})
	}
	if _, err := engine.Disconnect(wipeCtx); err != nil {
		resp.Checks = append(resp.Checks, stackActionCheck{Name: "backup_wipe_disconnect", Status: stackaction.CheckStatusWarning, Detail: err.Error()})
	} else {
		resp.Checks = append(resp.Checks, stackActionCheck{Name: "backup_wipe_disconnect", Status: stackaction.CheckStatusOK, Detail: "repository disconnected"})
	}

	s.finishBackupClaimStackAction(state)

	resp.Status = stackaction.StatusApplied
	resp.Backup = &stackaction.BackupResult{Engine: backupEngineNameStackAction, Wiped: true}
	return resp, http.StatusOK, nil
}

// finishBackupClaim completes a synchronous operation's slot claim.
func (s *Server) finishBackupClaimStackAction(state *backupRunStateStackAction) {
	finished := *state
	finished.InProcess = false
	finished.Phase = backupPhaseCompletedStackAction
	finished.FinishedAt = time.Now().UTC()
	s.storeBackupStateStackAction(&finished)
}

func backupSnapshotFromEngineStackAction(snap backupexec.Snapshot, classes []string) stackaction.BackupSnapshot {
	out := stackaction.BackupSnapshot{
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
func latestLogicalRepoSizeStackAction(snapshots []backupexec.Snapshot) int64 {
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

func truncateForCheckStackAction(out string) string {
	out = strings.TrimSpace(out)
	if len(out) > 400 {
		return out[:400] + "…"
	}
	return out
}
