package restoreactivation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/backuplifecycle"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/lifecyclemutation"
	"github.com/kombifyio/stackkits/internal/localevidence"
)

const (
	ResultAPIVersion   = "stackkit.restore-activation-result/v1"
	operationTimeout   = 10 * time.Minute
	resultRoot         = ".stackkit/backups/restore-activations"
	resultEvidenceRoot = resultRoot + "/results"
)

var errResultNotFound = errors.New("restoreactivation: persisted result is unavailable")

type LiveVerification = backuplifecycle.RestoreVerification

type ActivateInput struct {
	WorkspaceRoot        string
	OperationID          string
	OwnerApproved        bool
	Plan                 generationartifact.VerifiedPlan
	Manifest             generationartifact.ArtifactManifest
	RestoreResult        backuplifecycle.RestoreResult
	CurrentLineage       backuplifecycle.AuthorityLineage
	CreateSafetySnapshot func(context.Context, string) (backuplifecycle.SnapshotAnchor, error)
	VerifyLive           func(context.Context) (LiveVerification, error)
	FinalizeResult       func(context.Context, Result, error) error
}

type RecoverInput struct {
	WorkspaceRoot  string
	OperationID    string
	OwnerApproved  bool
	VerifyLive     func(context.Context) (LiveVerification, error)
	FinalizeResult func(context.Context, Result, error) error
}

type Result struct {
	APIVersion           string                                        `json:"apiVersion"`
	OperationID          string                                        `json:"operationId"`
	RestoreResultID      string                                        `json:"restoreResultId"`
	SafetySnapshotID     string                                        `json:"safetySnapshotId"`
	PlanHash             string                                        `json:"planHash"`
	ManagedVolumeSetHash string                                        `json:"managedVolumeSetHash"`
	Status               string                                        `json:"status"`
	Verification         LiveVerification                              `json:"verification"`
	Signature            localevidence.OwnerRestoreActivationSignature `json:"signature"`
}

// ActivationRecoveredError reports that activation failed but the prior live
// state was restored and its signed result was persisted. Callers must record
// the requested restore as recovered rather than asking for an impossible
// second recovery of the terminal journal.
type ActivationRecoveredError struct {
	Cause  error
	Result Result
}

func (err *ActivationRecoveredError) Error() string {
	return "restore activation failed; prior application state was restored: " + err.Cause.Error()
}

func (err *ActivationRecoveredError) Unwrap() error { return err.Cause }

type Runtime interface {
	Inspect(context.Context, Authority) error
	ValidateStaging(context.Context, Authority) error
	Stop(context.Context, Authority) error
	PrepareRollback(context.Context, Authority, Volume) error
	ActivateVolume(context.Context, Authority, Volume) error
	RestoreVolume(context.Context, Authority, Volume) error
	Start(context.Context, Authority) error
	CleanupRollback(context.Context, Authority, Volume) error
}

type RecoveryAuthorityResolver func(
	context.Context,
	lifecyclemutation.RestoreActivationAuthority,
) (Authority, error)

type Service struct {
	runtime         Runtime
	resolveRecovery RecoveryAuthorityResolver
}

func NewService(runtime Runtime, resolver RecoveryAuthorityResolver) (*Service, error) {
	if runtime == nil {
		return nil, errors.New("restoreactivation: runtime is required")
	}
	return &Service{runtime: runtime, resolveRecovery: resolver}, nil
}

func (service *Service) Activate(ctx context.Context, input ActivateInput) (Result, error) {
	if service == nil || service.runtime == nil {
		return Result{}, errors.New("restoreactivation: service is not initialized")
	}
	if !input.OwnerApproved {
		return Result{}, errors.New("restoreactivation: explicit local Owner approval is required")
	}
	if input.CreateSafetySnapshot == nil || input.VerifyLive == nil || input.FinalizeResult == nil {
		return Result{}, errors.New("restoreactivation: safety snapshot, live verification, and result finalizer are required")
	}
	if input.CurrentLineage != input.RestoreResult.AuthorizationLineage {
		return Result{}, errors.New("restoreactivation: current authority differs from the staged restore")
	}
	if err := backuplifecycle.VerifyRestoreResult(input.WorkspaceRoot, input.RestoreResult); err != nil {
		return Result{}, fmt.Errorf("restoreactivation: verify staged restore result: %w", err)
	}
	authority, err := DeriveAuthority(
		input.WorkspaceRoot, input.Plan, input.Manifest, input.RestoreResult, input.OperationID,
	)
	if err != nil {
		return Result{}, err
	}
	bounded, cancel := boundedContext(ctx)
	defer cancel()

	var safety backuplifecycle.SnapshotAnchor
	session, err := lifecyclemutation.BeginRestoreActivationPrepared(
		input.WorkspaceRoot,
		func() (lifecyclemutation.RestoreActivationBeginRequest, error) {
			if err := service.runtime.Inspect(bounded, authority); err != nil {
				return lifecyclemutation.RestoreActivationBeginRequest{}, err
			}
			if err := service.runtime.ValidateStaging(bounded, authority); err != nil {
				return lifecyclemutation.RestoreActivationBeginRequest{}, err
			}
			safety, err = input.CreateSafetySnapshot(
				bounded, input.OperationID+"-safety",
			)
			if err != nil {
				return lifecyclemutation.RestoreActivationBeginRequest{}, fmt.Errorf("restoreactivation: create safety snapshot: %w", err)
			}
			if err := verifySafetySnapshot(authority, safety); err != nil {
				return lifecyclemutation.RestoreActivationBeginRequest{}, err
			}
			return beginRequest(authority, safety.ID), nil
		},
	)
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = session.Close() }()

	verification, executionErr := service.activateLocked(
		bounded, session, authority, input.VerifyLive,
	)
	if executionErr != nil {
		recovered, rollbackErr := service.recoverAfterFailure(bounded, session, input.WorkspaceRoot, authority, safety.ID, input.VerifyLive, input.FinalizeResult, executionErr)
		if rollbackErr == nil {
			return Result{}, &ActivationRecoveredError{Cause: executionErr, Result: recovered}
		}
		return Result{}, errors.Join(executionErr, rollbackErr)
	}
	result, err := signResult(
		input.WorkspaceRoot, authority, safety.ID, "activated", verification,
	)
	if err != nil {
		recovered, rollbackErr := service.recoverAfterFailure(bounded, session, input.WorkspaceRoot, authority, safety.ID, input.VerifyLive, input.FinalizeResult, err)
		if rollbackErr == nil {
			return Result{}, &ActivationRecoveredError{Cause: err, Result: recovered}
		}
		return Result{}, errors.Join(err, rollbackErr)
	}
	progress := session.Record().RestoreActivation
	if progress == nil {
		return Result{}, errors.New("restoreactivation: verified activation journal is unavailable")
	}
	terminalProgress := lifecyclemutation.RestoreActivationProgress{
		RollbackPrepared: append([]string(nil), progress.RollbackPrepared...),
		Activated:        append([]string(nil), progress.Activated...),
	}
	if err := session.TransitionRestoreActivation(
		lifecyclemutation.PhaseVerifySucceeded,
		lifecyclemutation.PhaseCommitStarted,
		terminalProgress,
	); err != nil {
		return Result{}, err
	}
	if err := session.TransitionRestoreActivation(
		lifecyclemutation.PhaseCommitStarted,
		lifecyclemutation.PhaseCommitSucceeded,
		terminalProgress,
	); err != nil {
		return Result{}, err
	}
	if err := persistResult(input.WorkspaceRoot, result); err != nil {
		return Result{}, err
	}
	if err := cleanupRollbackVolumes(bounded, service.runtime, authority); err != nil {
		return Result{}, err
	}
	if err := input.FinalizeResult(bounded, result, nil); err != nil {
		return Result{}, fmt.Errorf("restoreactivation: finalize activated result: %w", err)
	}
	if err := session.Complete(lifecyclemutation.StatusSucceeded); err != nil {
		return Result{}, err
	}
	return result, nil
}

func (service *Service) Recover(ctx context.Context, input RecoverInput) (Result, error) {
	if service == nil || service.runtime == nil || service.resolveRecovery == nil {
		return Result{}, errors.New("restoreactivation: recovery authority resolver is unavailable")
	}
	if !input.OwnerApproved || input.VerifyLive == nil || input.FinalizeResult == nil {
		return Result{}, errors.New("restoreactivation: explicit Owner approval, verification, and result finalizer are required")
	}
	bounded, cancel := boundedContext(ctx)
	defer cancel()
	session, record, err := lifecyclemutation.OpenRestoreActivationRecovery(
		input.WorkspaceRoot, input.OperationID,
	)
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = session.Close() }()
	authority, err := service.resolveRecovery(
		bounded, record.RestoreActivation.Authority,
	)
	if err != nil {
		return Result{}, err
	}
	if err := bindJournalAuthority(authority, record.RestoreActivation.Authority); err != nil {
		return Result{}, err
	}
	if record.Status == lifecyclemutation.StatusSucceeded || record.Status == lifecyclemutation.StatusRecovered {
		result, err := loadPersistedResult(input.WorkspaceRoot, input.OperationID)
		if err != nil {
			return Result{}, err
		}
		expectedStatus := "activated"
		if record.Status == lifecyclemutation.StatusRecovered {
			expectedStatus = "recovered"
		}
		if err := bindResultAuthority(result, authority, record.RestoreActivation.Authority.SafetySnapshotID, expectedStatus); err != nil {
			return Result{}, err
		}
		return result, nil
	}
	if record.Phase == lifecyclemutation.PhaseCommitSucceeded {
		verification, err := input.VerifyLive(bounded)
		if err != nil {
			return Result{}, err
		}
		if err := validateLiveVerification(authority, verification); err != nil {
			return Result{}, err
		}
		result, err := resultForFinalization(
			input.WorkspaceRoot, authority,
			record.RestoreActivation.Authority.SafetySnapshotID,
			"activated", verification,
		)
		if err != nil {
			return Result{}, err
		}
		if err := persistResult(input.WorkspaceRoot, result); err != nil {
			return Result{}, err
		}
		if err := cleanupRollbackVolumes(bounded, service.runtime, authority); err != nil {
			return Result{}, err
		}
		if err := input.FinalizeResult(bounded, result, nil); err != nil {
			return Result{}, fmt.Errorf("restoreactivation: finalize recovered activation commit: %w", err)
		}
		if err := session.Complete(lifecyclemutation.StatusSucceeded); err != nil {
			return Result{}, err
		}
		return result, nil
	}
	verification, err := service.rollbackLocked(
		bounded, session, authority, input.VerifyLive,
	)
	if err != nil {
		return Result{}, err
	}
	result, err := resultForFinalization(
		input.WorkspaceRoot, authority,
		record.RestoreActivation.Authority.SafetySnapshotID,
		"recovered", verification,
	)
	if err != nil {
		return Result{}, err
	}
	if err := persistResult(input.WorkspaceRoot, result); err != nil {
		return Result{}, err
	}
	if err := cleanupRollbackVolumes(bounded, service.runtime, authority); err != nil {
		return Result{}, err
	}
	if err := input.FinalizeResult(bounded, result, nil); err != nil {
		return Result{}, fmt.Errorf("restoreactivation: finalize recovered result: %w", err)
	}
	if err := session.Complete(lifecyclemutation.StatusRecovered); err != nil {
		return Result{}, err
	}
	return result, nil
}

func (service *Service) activateLocked(
	ctx context.Context,
	session *lifecyclemutation.Session,
	authority Authority,
	verify func(context.Context) (LiveVerification, error),
) (LiveVerification, error) {
	progress := lifecyclemutation.RestoreActivationProgress{
		RollbackPrepared: []string{}, Activated: []string{},
	}
	phase := lifecyclemutation.PhasePrepared
	transition := func(next string) error {
		if err := session.TransitionRestoreActivation(phase, next, progress); err != nil {
			return err
		}
		phase = next
		return nil
	}
	if err := transition(lifecyclemutation.PhaseQuiesceStarted); err != nil {
		return LiveVerification{}, err
	}
	if err := service.runtime.Stop(ctx, authority); err != nil {
		return LiveVerification{}, err
	}
	if err := transition(lifecyclemutation.PhaseQuiesced); err != nil {
		return LiveVerification{}, err
	}
	for _, volume := range authority.VolumeDetails {
		progress.InFlight = &lifecyclemutation.RestoreActivationStep{Volume: volume.LiveName}
		if err := transition(lifecyclemutation.PhaseRollbackCopyStarted); err != nil {
			return LiveVerification{}, err
		}
		if err := service.runtime.PrepareRollback(ctx, authority, volume); err != nil {
			return LiveVerification{}, err
		}
		progress.RollbackPrepared = append(progress.RollbackPrepared, volume.LiveName)
		progress.InFlight = nil
		if err := transition(lifecyclemutation.PhaseRollbackCopySucceeded); err != nil {
			return LiveVerification{}, err
		}
	}
	if err := transition(lifecyclemutation.PhaseRollbackReady); err != nil {
		return LiveVerification{}, err
	}
	for _, volume := range authority.VolumeDetails {
		progress.InFlight = &lifecyclemutation.RestoreActivationStep{Volume: volume.LiveName}
		if err := transition(lifecyclemutation.PhaseActivationCopyStarted); err != nil {
			return LiveVerification{}, err
		}
		if err := service.runtime.ActivateVolume(ctx, authority, volume); err != nil {
			return LiveVerification{}, err
		}
		progress.Activated = append(progress.Activated, volume.LiveName)
		progress.InFlight = nil
		if err := transition(lifecyclemutation.PhaseActivationCopySucceeded); err != nil {
			return LiveVerification{}, err
		}
	}
	if err := transition(lifecyclemutation.PhaseActivationSucceeded); err != nil {
		return LiveVerification{}, err
	}
	if err := transition(lifecyclemutation.PhaseRuntimeStartStarted); err != nil {
		return LiveVerification{}, err
	}
	if err := service.runtime.Start(ctx, authority); err != nil {
		return LiveVerification{}, err
	}
	if err := transition(lifecyclemutation.PhaseRuntimeStartSucceeded); err != nil {
		return LiveVerification{}, err
	}
	if err := transition(lifecyclemutation.PhaseVerifyStarted); err != nil {
		return LiveVerification{}, err
	}
	verification, err := verify(ctx)
	if err != nil {
		return LiveVerification{}, err
	}
	if err := validateLiveVerification(authority, verification); err != nil {
		return LiveVerification{}, err
	}
	if err := transition(lifecyclemutation.PhaseVerifySucceeded); err != nil {
		return LiveVerification{}, err
	}
	return verification, nil
}

func (service *Service) rollbackLocked(
	ctx context.Context,
	session *lifecyclemutation.Session,
	authority Authority,
	verify func(context.Context) (LiveVerification, error),
) (LiveVerification, error) {
	record := session.Record()
	if record.Status == lifecyclemutation.StatusRecovered {
		return LiveVerification{}, errors.New("restoreactivation: operation is already recovered")
	}
	if record.Status == lifecyclemutation.StatusSucceeded {
		return LiveVerification{}, errors.New("restoreactivation: committed activation cannot be rolled back by recovery")
	}
	state := record.RestoreActivation
	if state == nil {
		return LiveVerification{}, errors.New("restoreactivation: active journal lacks activation state")
	}
	progress := lifecyclemutation.RestoreActivationProgress{
		RollbackPrepared: append([]string(nil), state.RollbackPrepared...),
		Activated:        append([]string(nil), state.Activated...),
		InFlight:         state.InFlight,
	}
	phase := record.Phase
	transition := func(next string) error {
		if err := session.TransitionRestoreActivation(phase, next, progress); err != nil {
			return err
		}
		phase = next
		return nil
	}
	if !rollbackPhase(phase) {
		if phase == lifecyclemutation.PhaseActivationCopyStarted &&
			state.InFlight != nil {
			progress.Activated = append(
				progress.Activated, state.InFlight.Volume,
			)
		}
		progress.InFlight = nil
		if err := transition(lifecyclemutation.PhaseRollbackStarted); err != nil {
			return LiveVerification{}, err
		}
	}
	// Activation may have started the services before verification failed.
	// Stop again before replacing any live bytes, including after a process
	// restart during rollback. The persisted phase remains resumable on error.
	if phase == lifecyclemutation.PhaseRollbackStarted || phase == lifecyclemutation.PhaseRollbackVolumeStarted || phase == lifecyclemutation.PhaseRollbackVolumeSucceeded {
		if err := service.runtime.Stop(ctx, authority); err != nil {
			return LiveVerification{}, fmt.Errorf("restoreactivation: quiesce before rollback: %w", err)
		}
	}
	byName := make(map[string]Volume, len(authority.VolumeDetails))
	for _, volume := range authority.VolumeDetails {
		byName[volume.LiveName] = volume
	}
	for len(progress.Activated) > 0 {
		name := progress.Activated[len(progress.Activated)-1]
		volume, exists := byName[name]
		if !exists {
			return LiveVerification{}, errors.New("restoreactivation: journal contains a foreign active volume")
		}
		if phase != lifecyclemutation.PhaseRollbackVolumeStarted {
			progress.InFlight = &lifecyclemutation.RestoreActivationStep{Volume: name}
			if err := transition(lifecyclemutation.PhaseRollbackVolumeStarted); err != nil {
				return LiveVerification{}, err
			}
		} else if progress.InFlight == nil || progress.InFlight.Volume != name {
			return LiveVerification{}, errors.New("restoreactivation: in-flight rollback volume differs from journal tail")
		}
		if err := service.runtime.RestoreVolume(ctx, authority, volume); err != nil {
			return LiveVerification{}, err
		}
		progress.Activated = progress.Activated[:len(progress.Activated)-1]
		progress.InFlight = nil
		if err := transition(lifecyclemutation.PhaseRollbackVolumeSucceeded); err != nil {
			return LiveVerification{}, err
		}
	}
	if phase == lifecyclemutation.PhaseRollbackStarted || phase == lifecyclemutation.PhaseRollbackVolumeSucceeded {
		if err := transition(lifecyclemutation.PhaseRollbackRuntimeStarted); err != nil {
			return LiveVerification{}, err
		}
	}
	if phase == lifecyclemutation.PhaseRollbackRuntimeStarted {
		if err := service.runtime.Start(ctx, authority); err != nil {
			return LiveVerification{}, err
		}
		if err := transition(lifecyclemutation.PhaseRollbackRuntimeSucceeded); err != nil {
			return LiveVerification{}, err
		}
	}
	if phase == lifecyclemutation.PhaseRollbackRuntimeSucceeded {
		if err := transition(lifecyclemutation.PhaseRollbackActivationVerifyStarted); err != nil {
			return LiveVerification{}, err
		}
	}
	verification, err := verify(ctx)
	if err != nil {
		return LiveVerification{}, err
	}
	if err := validateLiveVerification(authority, verification); err != nil {
		return LiveVerification{}, err
	}
	if phase == lifecyclemutation.PhaseRollbackActivationVerifyStarted {
		if err := transition(lifecyclemutation.PhaseRollbackActivationVerifyDone); err != nil {
			return LiveVerification{}, err
		}
	}
	if phase == lifecyclemutation.PhaseRollbackActivationVerifyDone {
		if err := transition(lifecyclemutation.PhaseRollbackSucceeded); err != nil {
			return LiveVerification{}, err
		}
	}
	return verification, nil
}

// Failure recovery is its own bounded phase. The canceled activation context
// must not prevent restoring the already authorized prior state.
func (service *Service) recoverAfterFailure(parent context.Context, session *lifecyclemutation.Session, workspace string, authority Authority, safetySnapshotID string, verify func(context.Context) (LiveVerification, error), finalize func(context.Context, Result, error) error, cause error) (Result, error) {
	ctx, cancel := lifecyclemutation.RecoveryContext(parent)
	defer cancel()
	if err := session.ReconcileForRecovery(); err != nil {
		return Result{}, fmt.Errorf("restoreactivation: reconcile failed journal commit before rollback: %w", err)
	}
	verification, err := service.rollbackLocked(ctx, session, authority, verify)
	if err != nil {
		return Result{}, err
	}
	result, err := signResult(workspace, authority, safetySnapshotID, "recovered", verification)
	if err != nil {
		return Result{}, err
	}
	if err := persistResult(workspace, result); err != nil {
		return Result{}, err
	}
	if err := cleanupRollbackVolumes(ctx, service.runtime, authority); err != nil {
		return Result{}, err
	}
	if err := finalize(ctx, result, cause); err != nil {
		return Result{}, fmt.Errorf("restoreactivation: finalize automatic recovery result: %w", err)
	}
	if err := session.Complete(lifecyclemutation.StatusRecovered); err != nil {
		return Result{}, err
	}
	return result, nil
}

func rollbackPhase(phase string) bool {
	switch phase {
	case lifecyclemutation.PhaseRollbackStarted,
		lifecyclemutation.PhaseRollbackVolumeStarted, lifecyclemutation.PhaseRollbackVolumeSucceeded,
		lifecyclemutation.PhaseRollbackRuntimeStarted, lifecyclemutation.PhaseRollbackRuntimeSucceeded,
		lifecyclemutation.PhaseRollbackActivationVerifyStarted, lifecyclemutation.PhaseRollbackActivationVerifyDone,
		lifecyclemutation.PhaseRollbackSucceeded:
		return true
	default:
		return false
	}
}

func beginRequest(authority Authority, safetySnapshotID string) lifecyclemutation.RestoreActivationBeginRequest {
	return lifecyclemutation.RestoreActivationBeginRequest{
		OperationID: authority.OperationID, OwnerRef: authority.OwnerRef,
		RestoreResultID:  authority.RestoreResultID,
		SafetySnapshotID: safetySnapshotID,
		PlanHash:         authority.PlanHash, ManifestHash: authority.ManifestHash,
		ApplyResultHash:      authority.ApplyResultHash,
		ManagedVolumeSetHash: authority.ManagedVolumeSetHash,
		Volumes:              append([]string(nil), authority.Volumes...),
	}
}

func verifySafetySnapshot(authority Authority, anchor backuplifecycle.SnapshotAnchor) error {
	if anchor.ID == "" || anchor.OwnerRef != authority.OwnerRef ||
		anchor.Lineage.Binding.PlanHash != authority.PlanHash ||
		anchor.Lineage.ManifestHash != authority.ManifestHash ||
		anchor.Lineage.ApplyResultHash != authority.ApplyResultHash {
		return errors.New("restoreactivation: safety snapshot does not bind the activation authority")
	}
	return nil
}

func validateLiveVerification(authority Authority, verification LiveVerification) error {
	if verification.APIVersion == "" ||
		verification.OwnerRef != authority.OwnerRef ||
		verification.PlanHash != authority.PlanHash ||
		!verification.ServicesVerified ||
		verification.VerifiedAt.IsZero() {
		return errors.New("restoreactivation: live service and Owner verification is incomplete")
	}
	return nil
}

func bindJournalAuthority(
	authority Authority,
	journal lifecyclemutation.RestoreActivationAuthority,
) error {
	if authority.OperationID != journal.OperationID ||
		authority.OwnerRef != journal.OwnerRef ||
		authority.RestoreResultID != journal.RestoreResultID ||
		authority.PlanHash != journal.PlanHash ||
		authority.ManifestHash != journal.ManifestHash ||
		authority.ApplyResultHash != journal.ApplyResultHash ||
		authority.ManagedVolumeSetHash != journal.ManagedVolumeSetHash ||
		len(authority.Volumes) != len(journal.Volumes) {
		return errors.New("restoreactivation: recovered authority differs from the signed journal")
	}
	for index := range authority.Volumes {
		if authority.Volumes[index] != journal.Volumes[index] {
			return errors.New("restoreactivation: recovered volume set differs from the signed journal")
		}
	}
	return nil
}

func signResult(
	workspace string,
	authority Authority,
	safetySnapshotID, status string,
	verification LiveVerification,
) (Result, error) {
	result := Result{
		APIVersion: ResultAPIVersion, OperationID: authority.OperationID,
		RestoreResultID:  authority.RestoreResultID,
		SafetySnapshotID: safetySnapshotID, PlanHash: authority.PlanHash,
		ManagedVolumeSetHash: authority.ManagedVolumeSetHash,
		Status:               status, Verification: verification,
	}
	canonical, err := canonicalUnsignedResult(result)
	if err != nil {
		return Result{}, err
	}
	result.Signature, err = localevidence.SignOwnerRestoreActivation(
		workspace, canonical,
	)
	if err != nil {
		return Result{}, err
	}
	return result, nil
}

func canonicalUnsignedResult(result Result) ([]byte, error) {
	result.Signature = localevidence.OwnerRestoreActivationSignature{}
	return json.Marshal(result)
}

func VerifyResult(workspace string, result Result) error {
	if result.APIVersion != ResultAPIVersion ||
		!activationOperationPattern.MatchString(result.OperationID) ||
		result.RestoreResultID == "" || result.SafetySnapshotID == "" ||
		result.PlanHash == "" || result.ManagedVolumeSetHash == "" ||
		(result.Status != "activated" && result.Status != "recovered") {
		return errors.New("restoreactivation: result is incomplete")
	}
	if result.Verification.OwnerRef != result.Signature.OwnerRef ||
		result.Verification.PlanHash != result.PlanHash ||
		!result.Verification.ServicesVerified {
		return errors.New("restoreactivation: result verification differs from its signed authority")
	}
	canonical, err := canonicalUnsignedResult(result)
	if err != nil {
		return err
	}
	return localevidence.VerifyOwnerRestoreActivation(
		workspace, canonical, result.Signature,
	)
}

func bindResultAuthority(result Result, authority Authority, safetySnapshotID, status string) error {
	if result.OperationID != authority.OperationID || result.RestoreResultID != authority.RestoreResultID ||
		result.SafetySnapshotID != safetySnapshotID || result.PlanHash != authority.PlanHash ||
		result.ManagedVolumeSetHash != authority.ManagedVolumeSetHash || result.Status != status ||
		result.Signature.OwnerRef != authority.OwnerRef {
		return errors.New("restoreactivation: persisted result differs from the terminal journal authority")
	}
	return nil
}

// A finalizer can commit only part of a multi-application operation before
// failing. Its retry must carry the original signed evidence, including the
// original verification timestamp, so those committed transitions remain valid.
func resultForFinalization(workspace string, authority Authority, safetySnapshotID, status string, verification LiveVerification) (Result, error) {
	result, err := loadPersistedResult(workspace, authority.OperationID)
	if errors.Is(err, errResultNotFound) {
		return signResult(workspace, authority, safetySnapshotID, status, verification)
	}
	if err != nil {
		return Result{}, err
	}
	if err := bindResultAuthority(result, authority, safetySnapshotID, status); err != nil {
		return Result{}, err
	}
	return result, nil
}

func loadPersistedResult(workspace, operationID string) (Result, error) {
	if !activationOperationPattern.MatchString(operationID) {
		return Result{}, errors.New("restoreactivation: result operation ID is invalid")
	}
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = root.Close() }()
	transaction, err := root.BeginTransaction()
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = transaction.Close() }()
	name := filepath.ToSlash(filepath.Join(resultRoot, operationID+".json"))
	exists, _, err := transaction.Exists(name)
	if err != nil {
		return Result{}, err
	}
	if !exists {
		return Result{}, errResultNotFound
	}
	raw, info, err := transaction.ReadStable(name)
	if err != nil {
		return Result{}, fmt.Errorf("restoreactivation: read persisted result: %w", err)
	}
	if !info.Mode().IsRegular() {
		return Result{}, errors.New("restoreactivation: persisted result is not a regular file")
	}
	var result Result
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return Result{}, fmt.Errorf("restoreactivation: decode terminal result: %w", err)
	}
	canonical, err := json.MarshalIndent(result, "", "  ")
	if err != nil || !bytes.Equal(raw, canonical) {
		return Result{}, errors.New("restoreactivation: terminal result is not canonical")
	}
	if result.OperationID != operationID {
		return Result{}, errors.New("restoreactivation: terminal result operation differs from request")
	}
	if err := VerifyResult(workspace, result); err != nil {
		return Result{}, err
	}
	return result, nil
}

func persistResult(workspace string, result Result) error {
	if err := VerifyResult(workspace, result); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	transaction, err := root.BeginTransaction()
	if err != nil {
		return err
	}
	if err := transaction.MkdirAll(resultEvidenceRoot, 0o700); err != nil {
		_ = transaction.Close()
		return err
	}
	evidenceRef, _, err := ResultEvidence(workspace, result)
	if err != nil {
		_ = transaction.Close()
		return err
	}
	if err := transaction.WriteFileExclusive(evidenceRef, encoded, 0o600); err != nil {
		existing, info, readErr := transaction.ReadStable(evidenceRef)
		if readErr != nil || !info.Mode().IsRegular() || !bytes.Equal(existing, encoded) {
			_ = transaction.Close()
			return fmt.Errorf("restoreactivation: persist content-addressed result: %w", err)
		}
	}
	if err := transaction.Close(); err != nil {
		return err
	}
	view, err := root.View(".")
	if err != nil {
		return err
	}
	name := filepath.ToSlash(filepath.Join(resultRoot, result.OperationID+".json"))
	_, err = view.WriteAtomic0600(name, encoded)
	return err
}

// ResultEvidence returns the immutable owner-custody location and digest of
// the exact signed activation result bytes persisted by the service.
func ResultEvidence(workspace string, result Result) (string, string, error) {
	if err := VerifyResult(workspace, result); err != nil {
		return "", "", err
	}
	encoded, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", "", err
	}
	digest := sha256.Sum256(encoded)
	digestText := "sha256:" + hex.EncodeToString(digest[:])
	ref := filepath.ToSlash(filepath.Join(
		resultEvidenceRoot, strings.TrimPrefix(digestText, "sha256:")+".json",
	))
	return ref, digestText, nil
}

func cleanupRollbackVolumes(ctx context.Context, runtime Runtime, authority Authority) error {
	var result error
	for _, volume := range authority.VolumeDetails {
		if err := runtime.CleanupRollback(ctx, authority, volume); err != nil {
			result = errors.Join(result, fmt.Errorf("cleanup rollback volume %q: %w", volume.RollbackName, err))
		}
	}
	return result
}

func boundedContext(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	if deadline, ok := parent.Deadline(); ok && time.Until(deadline) <= operationTimeout {
		return context.WithCancel(parent)
	}
	return context.WithTimeout(parent, operationTimeout)
}
