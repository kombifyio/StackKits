package backuplifecycle

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/localbackuppolicy"
	"github.com/kombifyio/stackkits/internal/localevidence"
)

type RestoreInput struct {
	OwnerRef         string              `json:"ownerRef"`
	AuthorityRef     string              `json:"authorityRef"`
	Lineage          AuthorityLineage    `json:"lineage"`
	PolicyArtifact   []byte              `json:"-"`
	SnapshotAnchorID string              `json:"snapshotAnchorId"`
	OperationID      string              `json:"operationId"`
	OwnerApproved    bool                `json:"ownerApproved"`
	PostVerify       RestorePostVerifier `json:"-"`
}

type restoreOperation struct {
	APIVersion  string                    `json:"apiVersion"`
	State       string                    `json:"state"`
	Input       restoreOperationInput     `json:"input"`
	Recovery    RestoreRecoveryAnchor     `json:"recoveryAnchor"`
	Receipt     *RepositoryRestoreReceipt `json:"receipt,omitempty"`
	Result      *RestoreResult            `json:"result,omitempty"`
	Abandonment *RestoreAbandonment       `json:"abandonment,omitempty"`
}

type restoreOperationInput struct {
	OwnerRef             string           `json:"ownerRef"`
	AuthorityRef         string           `json:"authorityRef"`
	AuthorizationLineage AuthorityLineage `json:"authorizationLineage"`
	PolicyArtifactDigest string           `json:"policyArtifactDigest"`
	SnapshotAnchorID     string           `json:"snapshotAnchorId"`
	OperationID          string           `json:"operationId"`
}

// Restore verifies one owner-signed snapshot anchor, stages its complete
// contents below the governed isolated restore volume, and signs evidence only
// after the caller re-verifies the current local Plan/Apply/Owner/service
// closure. It deliberately does not mutate live Docker volumes.
func (s *Service) Restore(ctx context.Context, input RestoreInput) (RestoreResult, error) {
	if err := s.ready(ctx); err != nil {
		return RestoreResult{}, err
	}
	if err := RequireSettledRuntimeControls(s.workspaceRoot, ""); err != nil {
		return RestoreResult{}, err
	}
	if !input.OwnerApproved {
		return RestoreResult{}, errors.New("backuplifecycle: restore requires explicit local Owner approval")
	}
	if !validOperationID(input.OperationID) {
		return RestoreResult{}, errors.New("backuplifecycle: restore operation ID must match [A-Za-z0-9][A-Za-z0-9._-]{0,127}")
	}
	if !validDigest(input.SnapshotAnchorID) {
		return RestoreResult{}, errors.New("backuplifecycle: restore requires a content-addressed snapshot anchor ID")
	}
	if input.PostVerify == nil {
		return RestoreResult{}, errors.New("backuplifecycle: restore requires a current local post-verifier")
	}
	configuration, err := s.boundConfiguration(StatusInput{
		OwnerRef: input.OwnerRef, AuthorityRef: input.AuthorityRef,
		Lineage: input.Lineage, PolicyArtifact: input.PolicyArtifact,
	})
	if err != nil {
		return RestoreResult{}, err
	}
	anchor, err := s.loadStoredSnapshotAnchor(input.SnapshotAnchorID)
	if err != nil {
		return RestoreResult{}, err
	}
	if err := validateRestoreSource(configuration, anchor); err != nil {
		return RestoreResult{}, err
	}
	status, err := s.Status(ctx, StatusInput{
		OwnerRef: input.OwnerRef, AuthorityRef: input.AuthorityRef,
		Lineage: input.Lineage, PolicyArtifact: input.PolicyArtifact,
	})
	if err != nil {
		return RestoreResult{}, err
	}
	if !status.Ready || status.Consistency != ConsistencyCrashConsistent {
		return RestoreResult{}, errors.New("backuplifecycle: repository is not ready for a verified staged restore")
	}

	operationInput := restoreOperationInput{
		OwnerRef: configuration.OwnerRef, AuthorityRef: configuration.AuthorityRef,
		AuthorizationLineage: configuration.Lineage,
		PolicyArtifactDigest: configuration.PolicyArtifactDigest,
		SnapshotAnchorID:     anchor.ID, OperationID: input.OperationID,
	}
	operationPath := restoreOperationDirectory + "/" + operationKey(input.OperationID) + ".json"
	recovery, stagedReceipt, existingResult, err := s.prepareRestoreOperation(operationPath, operationInput, configuration, anchor)
	if err != nil {
		return RestoreResult{}, err
	}
	if existingResult != nil {
		return *existingResult, nil
	}

	request, err := repositoryRestoreRequest(configuration, anchor, input.OperationID)
	if err != nil {
		return RestoreResult{}, err
	}
	var receipt RepositoryRestoreReceipt
	if stagedReceipt != nil {
		receipt = *stagedReceipt
	} else {
		if !restoreApprovalCurrent(recovery, time.Now().UTC()) {
			return RestoreResult{}, errors.New("backuplifecycle: restore Owner approval expired before staging; use a new operation ID")
		}
		receipt, err = s.runtime.RestoreSnapshot(ctx, request)
		if err != nil {
			return RestoreResult{}, fmt.Errorf("backuplifecycle: stage repository snapshot: %w", err)
		}
		if err := validateRestoreReceipt(receipt, request); err != nil {
			return RestoreResult{}, err
		}
		if !restoreApprovalCurrent(recovery, receipt.CompletedAt) || receipt.CompletedAt.After(time.Now().UTC()) {
			return RestoreResult{}, errors.New("backuplifecycle: staged restore receipt is outside its Owner approval")
		}
		if err := s.persistStagedRestore(operationPath, operationInput, recovery, receipt); err != nil {
			return RestoreResult{}, err
		}
	}
	verificationStartedAt := time.Now().UTC()
	if !restoreApprovalCurrent(recovery, verificationStartedAt) {
		return RestoreResult{}, errors.New("backuplifecycle: restore Owner approval is not current before verification; authorize a new operation")
	}
	verification, err := input.PostVerify(ctx, RestoreVerificationRequest{
		OwnerRef:             configuration.OwnerRef,
		AuthorizationLineage: configuration.Lineage,
		SnapshotAnchorID:     anchor.ID,
		OperationID:          input.OperationID,
		StagingPath:          request.StagingPath,
	})
	if err != nil {
		return RestoreResult{}, fmt.Errorf("backuplifecycle: verify local platform after staged restore: %w", err)
	}
	if err := validateRestoreVerification(verification, configuration, receipt); err != nil {
		return RestoreResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return RestoreResult{}, err
	}
	if err := validateFreshRestoreVerification(verification, recovery, verificationStartedAt, time.Now().UTC()); err != nil {
		return RestoreResult{}, err
	}
	return s.completeRestore(operationPath, recovery, anchor, receipt, verification)
}

func (s *Service) prepareRestoreOperation(
	operationPath string,
	input restoreOperationInput,
	configuration Configuration,
	anchor SnapshotAnchor,
) (RestoreRecoveryAnchor, *RepositoryRestoreReceipt, *RestoreResult, error) {
	if existing, err := s.loadRestoreOperation(operationPath); err == nil {
		if !reflect.DeepEqual(existing.Input, input) {
			return RestoreRecoveryAnchor{}, nil, nil, errors.New("backuplifecycle: restore operation differs from its persisted owner-authorized input")
		}
		if err := VerifyRestoreRecoveryAnchor(s.workspaceRoot, existing.Recovery); err != nil {
			return RestoreRecoveryAnchor{}, nil, nil, err
		}
		if err := validateRestoreRecoveryBinding(existing.Recovery, input, configuration, anchor); err != nil {
			return RestoreRecoveryAnchor{}, nil, nil, err
		}
		switch existing.State {
		case "pending":
			if existing.Receipt != nil || existing.Result != nil || existing.Abandonment != nil {
				return RestoreRecoveryAnchor{}, nil, nil, errors.New("backuplifecycle: pending restore operation unexpectedly contains a receipt or result")
			}
			return existing.Recovery, nil, nil, nil
		case "staged":
			if existing.Receipt == nil || existing.Result != nil || existing.Abandonment != nil {
				return RestoreRecoveryAnchor{}, nil, nil, errors.New("backuplifecycle: staged restore operation must contain only its repository receipt")
			}
			request, requestErr := repositoryRestoreRequest(configuration, anchor, input.OperationID)
			if requestErr != nil {
				return RestoreRecoveryAnchor{}, nil, nil, requestErr
			}
			if err := validateRestoreReceipt(*existing.Receipt, request); err != nil {
				return RestoreRecoveryAnchor{}, nil, nil, err
			}
			receipt := *existing.Receipt
			return existing.Recovery, &receipt, nil, nil
		case "completed":
			if existing.Result == nil {
				return RestoreRecoveryAnchor{}, nil, nil, errors.New("backuplifecycle: completed restore operation has no result")
			}
			if err := VerifyRestoreResult(s.workspaceRoot, *existing.Result); err != nil {
				return RestoreRecoveryAnchor{}, nil, nil, err
			}
			stored, err := s.loadStoredRestoreResult(existing.Result.ID)
			if err != nil {
				return RestoreRecoveryAnchor{}, nil, nil, err
			}
			if !reflect.DeepEqual(stored, *existing.Result) {
				return RestoreRecoveryAnchor{}, nil, nil, errors.New("backuplifecycle: restore journal differs from its content-addressed result")
			}
			return existing.Recovery, nil, &stored, nil
		case "abandoned":
			if existing.Abandonment == nil || existing.Result != nil {
				return RestoreRecoveryAnchor{}, nil, nil, errors.New("backuplifecycle: abandoned restore operation lacks its terminal evidence")
			}
			if err := VerifyRestoreAbandonment(s.workspaceRoot, *existing.Abandonment); err != nil {
				return RestoreRecoveryAnchor{}, nil, nil, err
			}
			if err := validateRestoreAbandonmentBinding(existing, anchor, *existing.Abandonment); err != nil {
				return RestoreRecoveryAnchor{}, nil, nil, err
			}
			if existing.Receipt != nil {
				request, requestErr := repositoryRestoreRequest(configuration, anchor, input.OperationID)
				if requestErr != nil {
					return RestoreRecoveryAnchor{}, nil, nil, requestErr
				}
				if err := validateRestoreReceipt(*existing.Receipt, request); err != nil {
					return RestoreRecoveryAnchor{}, nil, nil, err
				}
			}
			return RestoreRecoveryAnchor{}, nil, nil, errors.New("backuplifecycle: restore operation was explicitly abandoned; use a new operation ID")
		default:
			return RestoreRecoveryAnchor{}, nil, nil, errors.New("backuplifecycle: restore operation state is malformed")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return RestoreRecoveryAnchor{}, nil, nil, err
	}

	now := time.Now().UTC()
	recovery := RestoreRecoveryAnchor{
		APIVersion: restoreRecoveryAPI,
		OwnerRef:   configuration.OwnerRef, AuthorityRef: configuration.AuthorityRef,
		AuthorizationLineage: configuration.Lineage,
		PolicyArtifactDigest: configuration.PolicyArtifactDigest,
		SnapshotAnchorID:     anchor.ID, SnapshotLineage: anchor.Lineage,
		RepositoryID:          anchor.Repository.RepositoryID,
		SnapshotID:            anchor.Snapshot.SnapshotID,
		SnapshotContentDigest: anchor.Snapshot.ContentDigest,
		OperationID:           input.OperationID,
		StagingPath:           RestoreStagingPath(input.OperationID),
		Mode:                  "verified-staging-only",
		ApprovalMethod:        "explicit-cli-owner-custody",
		ApprovedAt:            now, ExpiresAt: now.Add(24 * time.Hour),
	}
	unsigned, err := canonicalRestoreRecovery(recovery)
	if err != nil {
		return RestoreRecoveryAnchor{}, nil, nil, err
	}
	recovery.ID = digestBytes(unsigned)
	signingBytes, err := canonicalRestoreRecovery(recovery)
	if err != nil {
		return RestoreRecoveryAnchor{}, nil, nil, err
	}
	recovery.Signature, err = localevidence.SignOwnerRestoreRecovery(s.workspaceRoot, signingBytes)
	if err != nil {
		return RestoreRecoveryAnchor{}, nil, nil, err
	}
	if err := VerifyRestoreRecoveryAnchor(s.workspaceRoot, recovery); err != nil {
		return RestoreRecoveryAnchor{}, nil, nil, err
	}
	if err := validateRestoreRecoveryBinding(recovery, input, configuration, anchor); err != nil {
		return RestoreRecoveryAnchor{}, nil, nil, err
	}
	recoveryPath := restoreRecoveryDirectory + "/" + operationKey(input.OperationID) + ".json"
	if err := s.writeJSON(recoveryPath, recovery); err != nil {
		return RestoreRecoveryAnchor{}, nil, nil, err
	}
	if err := s.writeJSON(operationPath, restoreOperation{
		APIVersion: restoreOperationAPI,
		State:      "pending", Input: input, Recovery: recovery,
	}); err != nil {
		return RestoreRecoveryAnchor{}, nil, nil, err
	}
	return recovery, nil, nil, nil
}

func (s *Service) persistStagedRestore(
	operationPath string,
	input restoreOperationInput,
	recovery RestoreRecoveryAnchor,
	receipt RepositoryRestoreReceipt,
) error {
	return s.writeJSON(operationPath, restoreOperation{
		APIVersion: restoreOperationAPI,
		State:      "staged",
		Input:      input,
		Recovery:   recovery,
		Receipt:    &receipt,
	})
}

func (s *Service) completeRestore(
	operationPath string,
	recovery RestoreRecoveryAnchor,
	anchor SnapshotAnchor,
	receipt RepositoryRestoreReceipt,
	verification RestoreVerification,
) (RestoreResult, error) {
	request, err := repositoryRestoreRequestFromRecovery(recovery, anchor)
	if err != nil {
		return RestoreResult{}, err
	}
	result := RestoreResult{
		APIVersion: restoreResultAPI,
		OwnerRef:   recovery.OwnerRef, AuthorityRef: recovery.AuthorityRef,
		AuthorizationLineage: recovery.AuthorizationLineage,
		SnapshotAnchorID:     anchor.ID, SnapshotLineage: anchor.Lineage,
		OperationID:    recovery.OperationID,
		RecoveryAnchor: recovery,
		Request:        request,
		Receipt:        receipt, Verification: verification,
	}
	unsigned, err := canonicalRestoreResult(result)
	if err != nil {
		return RestoreResult{}, err
	}
	result.ID = digestBytes(unsigned)
	signingBytes, err := canonicalRestoreResult(result)
	if err != nil {
		return RestoreResult{}, err
	}
	result.Signature, err = localevidence.SignOwnerRestoreResult(s.workspaceRoot, signingBytes)
	if err != nil {
		return RestoreResult{}, err
	}
	if err := VerifyRestoreResult(s.workspaceRoot, result); err != nil {
		return RestoreResult{}, err
	}
	resultPath := restoreResultDirectory + "/" + strings.TrimPrefix(result.ID, "sha256:") + ".json"
	if stored, err := s.loadStoredRestoreResult(result.ID); err == nil {
		if !reflect.DeepEqual(stored, result) {
			return RestoreResult{}, errors.New("backuplifecycle: content-addressed restore result differs from completion")
		}
	} else if errors.Is(err, os.ErrNotExist) {
		if err := s.writeJSON(resultPath, result); err != nil {
			return RestoreResult{}, err
		}
	} else {
		return RestoreResult{}, err
	}
	if err := s.writeJSON(operationPath, restoreOperation{
		APIVersion: restoreOperationAPI,
		State:      "completed",
		Input: restoreOperationInput{
			OwnerRef: recovery.OwnerRef, AuthorityRef: recovery.AuthorityRef,
			AuthorizationLineage: recovery.AuthorizationLineage,
			PolicyArtifactDigest: recovery.PolicyArtifactDigest,
			SnapshotAnchorID:     recovery.SnapshotAnchorID,
			OperationID:          recovery.OperationID,
		},
		Recovery: recovery, Receipt: &receipt, Result: &result,
	}); err != nil {
		return RestoreResult{}, err
	}
	return result, nil
}

// RestoreStagingPath returns the fixed isolated target for one restore
// operation. Callers cannot supply an alternate host or container path.
func RestoreStagingPath(operationID string) string {
	return localbackuppolicy.RestorePathForOperation(operationID)
}

func validateRestoreSource(configuration Configuration, anchor SnapshotAnchor) error {
	if len(anchor.Workloads) > 0 {
		return errors.New("application-scoped safety snapshot requires its selected activation recovery; whole-node restore is denied")
	}
	if anchor.OwnerRef != configuration.OwnerRef ||
		anchor.Repository.RepositoryID != configuration.Repository.RepositoryID ||
		anchor.Policy.StackID != configuration.Policy.StackID ||
		!restoreTargetsCompatible(anchor.Policy.Target, configuration.Policy.Target) ||
		!restoreDataTopologyCompatible(anchor.Policy.Source, configuration.Policy.Source) {
		return errors.New("backuplifecycle: snapshot anchor is not compatible with the current Owner, Stack target, repository, or governed data topology")
	}
	return nil
}

func restoreDataTopologyCompatible(historical, current localbackuppolicy.Source) bool {
	// Classification changes policy scope, never the physical data identity.
	// Preserve restoration of signed pre-classification whole-root anchors.
	historical.ApplicationVolumes = append([]localbackuppolicy.ApplicationVolume(nil), historical.ApplicationVolumes...)
	current.ApplicationVolumes = append([]localbackuppolicy.ApplicationVolume(nil), current.ApplicationVolumes...)
	for i := range historical.ApplicationVolumes {
		historical.ApplicationVolumes[i].BackupDataClass = ""
	}
	for i := range current.ApplicationVolumes {
		current.ApplicationVolumes[i].BackupDataClass = ""
	}
	historicalDigest, err := localbackuppolicy.SourceDigest(historical)
	if err != nil {
		return false
	}
	currentDigest, err := localbackuppolicy.SourceDigest(current)
	return err == nil && historicalDigest == currentDigest
}

func restoreTargetsCompatible(historical, current localbackuppolicy.Target) bool {
	if historical.SiteRef != current.SiteRef || historical.NodeRef != current.NodeRef {
		return false
	}
	legacy := historical.DaemonRef == "" && historical.DaemonEngine == "" &&
		historical.DaemonSocketPath == "" && historical.HostScope == ""
	return legacy || historical == current
}

func validateRestoreRecoveryBinding(
	recovery RestoreRecoveryAnchor,
	input restoreOperationInput,
	configuration Configuration,
	anchor SnapshotAnchor,
) error {
	if recovery.OwnerRef != input.OwnerRef ||
		recovery.AuthorityRef != input.AuthorityRef ||
		!lineagesEqual(recovery.AuthorizationLineage, input.AuthorizationLineage) ||
		recovery.PolicyArtifactDigest != input.PolicyArtifactDigest ||
		recovery.SnapshotAnchorID != input.SnapshotAnchorID ||
		recovery.OperationID != input.OperationID ||
		recovery.OwnerRef != configuration.OwnerRef ||
		recovery.AuthorityRef != configuration.AuthorityRef ||
		!lineagesEqual(recovery.AuthorizationLineage, configuration.Lineage) ||
		recovery.PolicyArtifactDigest != configuration.PolicyArtifactDigest ||
		recovery.SnapshotAnchorID != anchor.ID ||
		!lineagesEqual(recovery.SnapshotLineage, anchor.Lineage) ||
		recovery.RepositoryID != anchor.Repository.RepositoryID ||
		recovery.SnapshotID != anchor.Snapshot.SnapshotID ||
		recovery.SnapshotContentDigest != anchor.Snapshot.ContentDigest ||
		recovery.StagingPath != RestoreStagingPath(input.OperationID) {
		return errors.New("backuplifecycle: restore recovery anchor differs from its journal, current authorization, or selected snapshot anchor")
	}
	return nil
}

func repositoryRestoreRequest(
	configuration Configuration,
	anchor SnapshotAnchor,
	operationID string,
) (RepositoryRestoreRequest, error) {
	sourceDigest, err := localbackuppolicy.SourceDigest(anchor.Policy.Source)
	if err != nil {
		return RepositoryRestoreRequest{}, fmt.Errorf("backuplifecycle: digest snapshot source policy: %w", err)
	}
	snapshotRequest, err := snapshotRequestFromAnchor(anchor)
	if err != nil {
		return RepositoryRestoreRequest{}, err
	}
	return RepositoryRestoreRequest{
		OwnerRef:             configuration.OwnerRef,
		AuthorityRef:         configuration.AuthorityRef,
		AuthorizationLineage: configuration.Lineage,
		PolicyArtifactDigest: configuration.PolicyArtifactDigest,
		RepositoryID:         configuration.Repository.RepositoryID,
		SnapshotAnchorID:     anchor.ID,
		SnapshotSourceDigest: sourceDigest,
		SnapshotRequest:      snapshotRequest,
		SnapshotReceipt:      anchor.Snapshot,
		OperationID:          operationID,
		StagingPath:          RestoreStagingPath(operationID),
	}, nil
}

func repositoryRestoreRequestFromRecovery(
	recovery RestoreRecoveryAnchor,
	anchor SnapshotAnchor,
) (RepositoryRestoreRequest, error) {
	sourceDigest, err := localbackuppolicy.SourceDigest(anchor.Policy.Source)
	if err != nil {
		return RepositoryRestoreRequest{}, fmt.Errorf("backuplifecycle: digest recovered snapshot source policy: %w", err)
	}
	snapshotRequest, err := snapshotRequestFromAnchor(anchor)
	if err != nil {
		return RepositoryRestoreRequest{}, err
	}
	return RepositoryRestoreRequest{
		OwnerRef:             recovery.OwnerRef,
		AuthorityRef:         recovery.AuthorityRef,
		AuthorizationLineage: recovery.AuthorizationLineage,
		PolicyArtifactDigest: recovery.PolicyArtifactDigest,
		RepositoryID:         recovery.RepositoryID,
		SnapshotAnchorID:     recovery.SnapshotAnchorID,
		SnapshotSourceDigest: sourceDigest,
		SnapshotRequest:      snapshotRequest,
		SnapshotReceipt:      anchor.Snapshot,
		OperationID:          recovery.OperationID,
		StagingPath:          recovery.StagingPath,
	}, nil
}
