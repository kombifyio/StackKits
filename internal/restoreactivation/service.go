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
}

type RecoverInput struct {
	WorkspaceRoot string
	OperationID   string
	OwnerApproved bool
	VerifyLive    func(context.Context) (LiveVerification, error)
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
	if input.CreateSafetySnapshot == nil || input.VerifyLive == nil {
		return Result{}, errors.New("restoreactivation: safety snapshot and live verification are required")
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
		_, rollbackErr := service.rollbackLocked(
			bounded, session, authority, input.VerifyLive,
		)
		return Result{}, errors.Join(executionErr, rollbackErr)
	}
	result, err := signResult(
		input.WorkspaceRoot, authority, safety.ID, "activated", verification,
	)
	if err != nil {
		_, rollbackErr := service.rollbackLocked(
			bounded, session, authority, input.VerifyLive,
		)
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
	if err := session.Complete(lifecyclemutation.StatusSucceeded); err != nil {
		return Result{}, err
	}
	if err := persistResult(input.WorkspaceRoot, result); err != nil {
		return Result{}, err
	}
	cleanupRollbackVolumes(bounded, service.runtime, authority)
	return result, nil
}

func (service *Service) Recover(ctx context.Context, input RecoverInput) (Result, error) {
	if service == nil || service.runtime == nil || service.resolveRecovery == nil {
		return Result{}, errors.New("restoreactivation: recovery authority resolver is unavailable")
	}
	if !input.OwnerApproved || input.VerifyLive == nil {
		return Result{}, errors.New("restoreactivation: explicit Owner approval and verification are required")
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
	verification, err := service.rollbackLocked(
		bounded, session, authority, input.VerifyLive,
	)
	if err != nil {
		return Result{}, err
	}
	result, err := signResult(
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
	}
	phase := record.Phase
	if phase != lifecyclemutation.PhaseRollbackStarted {
		if phase == lifecyclemutation.PhaseActivationCopyStarted &&
			state.InFlight != nil {
			progress.Activated = append(
				progress.Activated, state.InFlight.Volume,
			)
		}
		if err := session.TransitionRestoreActivation(
			phase, lifecyclemutation.PhaseRollbackStarted, progress,
		); err != nil {
			return LiveVerification{}, err
		}
		phase = lifecyclemutation.PhaseRollbackStarted
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
		progress.InFlight = &lifecyclemutation.RestoreActivationStep{Volume: name}
		if err := session.TransitionRestoreActivation(
			phase, lifecyclemutation.PhaseRollbackVolumeStarted, progress,
		); err != nil {
			return LiveVerification{}, err
		}
		phase = lifecyclemutation.PhaseRollbackVolumeStarted
		if err := service.runtime.RestoreVolume(ctx, authority, volume); err != nil {
			return LiveVerification{}, err
		}
		progress.Activated = progress.Activated[:len(progress.Activated)-1]
		progress.InFlight = nil
		if err := session.TransitionRestoreActivation(
			phase, lifecyclemutation.PhaseRollbackVolumeSucceeded, progress,
		); err != nil {
			return LiveVerification{}, err
		}
		phase = lifecyclemutation.PhaseRollbackVolumeSucceeded
	}
	if err := session.TransitionRestoreActivation(
		phase, lifecyclemutation.PhaseRollbackRuntimeStarted, progress,
	); err != nil {
		return LiveVerification{}, err
	}
	if err := service.runtime.Start(ctx, authority); err != nil {
		return LiveVerification{}, err
	}
	if err := session.TransitionRestoreActivation(
		lifecyclemutation.PhaseRollbackRuntimeStarted,
		lifecyclemutation.PhaseRollbackRuntimeSucceeded, progress,
	); err != nil {
		return LiveVerification{}, err
	}
	if err := session.TransitionRestoreActivation(
		lifecyclemutation.PhaseRollbackRuntimeSucceeded,
		lifecyclemutation.PhaseRollbackActivationVerifyStarted, progress,
	); err != nil {
		return LiveVerification{}, err
	}
	verification, err := verify(ctx)
	if err != nil {
		return LiveVerification{}, err
	}
	if err := validateLiveVerification(authority, verification); err != nil {
		return LiveVerification{}, err
	}
	if err := session.TransitionRestoreActivation(
		lifecyclemutation.PhaseRollbackActivationVerifyStarted,
		lifecyclemutation.PhaseRollbackActivationVerifyDone, progress,
	); err != nil {
		return LiveVerification{}, err
	}
	if err := session.TransitionRestoreActivation(
		lifecyclemutation.PhaseRollbackActivationVerifyDone,
		lifecyclemutation.PhaseRollbackSucceeded, progress,
	); err != nil {
		return LiveVerification{}, err
	}
	if err := session.Complete(lifecyclemutation.StatusRecovered); err != nil {
		return LiveVerification{}, err
	}
	cleanupRollbackVolumes(ctx, service.runtime, authority)
	return verification, nil
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

func cleanupRollbackVolumes(ctx context.Context, runtime Runtime, authority Authority) {
	for _, volume := range authority.VolumeDetails {
		_ = runtime.CleanupRollback(ctx, authority, volume)
	}
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
