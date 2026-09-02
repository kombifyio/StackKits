package backuplifecycle

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"
)

// admitSnapshotRetention keeps a retention-bearing snapshot from expiring a
// source that an unfinished, owner-authorized restore still needs. The
// restore operation and its signed recovery anchor are the existing source
// of truth; this helper only reads them and never creates a second lease.
func (s *Service) admitSnapshotRetention(ctx context.Context, configuration Configuration) error {
	if configuration.Policy.Retention == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	operationIDs, err := s.historyIDs(ctx, restoreOperationDirectory)
	if err != nil {
		return fmt.Errorf("backuplifecycle: inspect restore operations before retention snapshot: %w", err)
	}
	leases := make([]restoreRetentionLease, 0, len(operationIDs))
	for _, operationID := range operationIDs {
		if err := ctx.Err(); err != nil {
			return err
		}
		path := restoreOperationDirectory + "/" + strings.TrimPrefix(operationID, "sha256:") + ".json"
		operation, err := s.loadRestoreOperation(path)
		if err != nil {
			return restoreRetentionJournalError(operationID, err)
		}
		// Scope itself comes from signed evidence. A damaged owner/repository
		// field must not hide an unfinished source from retention admission.
		if err := VerifyRestoreRecoveryAnchor(s.workspaceRoot, operation.Recovery); err != nil {
			return restoreRetentionJournalError(operationID, err)
		}
		if strings.TrimPrefix(operationID, "sha256:") != operationKey(operation.Recovery.OperationID) {
			return restoreRetentionJournalError(operationID, errors.New("restore journal path differs from its signed operation identity"))
		}
		if !restoreOperationUsesRetentionScope(operation, configuration) {
			continue
		}
		anchor, err := s.validateRestoreRetentionOperation(operation, configuration)
		if err != nil {
			return restoreRetentionJournalError(operation.Input.OperationID, err)
		}
		if (operation.State == "pending" || operation.State == "staged") && !anchor.ProtectRecovery {
			leases = append(leases, restoreRetentionLease{
				operationID: operation.Input.OperationID,
				anchorID:    operation.Input.SnapshotAnchorID,
				approvedAt:  operation.Recovery.ApprovedAt,
			})
		}
	}
	if len(leases) == 0 {
		return nil
	}
	completed, err := s.completedRestoreRetentionSources(ctx, configuration, leases)
	if err != nil {
		return err
	}
	for _, lease := range leases {
		if approvedAt, released := completed[lease.anchorID]; released && approvedAt.After(lease.approvedAt) {
			continue
		}
		return fmt.Errorf(
			"backuplifecycle: retention-bearing snapshot is blocked by restore operation %s for source anchor %s; resume or complete that restore before creating another snapshot",
			lease.operationID, lease.anchorID,
		)
	}
	return nil
}

type restoreRetentionLease struct {
	operationID string
	anchorID    string
	approvedAt  time.Time
}

func restoreOperationUsesRetentionScope(operation restoreOperation, configuration Configuration) bool {
	return operation.Recovery.OwnerRef == configuration.OwnerRef &&
		operation.Recovery.RepositoryID == configuration.Repository.RepositoryID
}

func (s *Service) validateRestoreRetentionOperation(
	operation restoreOperation,
	configuration Configuration,
) (SnapshotAnchor, error) {
	anchor, err := s.loadStoredSnapshotAnchor(operation.Recovery.SnapshotAnchorID)
	if err != nil {
		return SnapshotAnchor{}, fmt.Errorf("load source anchor %s: %w", operation.Recovery.SnapshotAnchorID, err)
	}
	if err := validateRestoreRetentionRecoveryBinding(operation, anchor); err != nil {
		return SnapshotAnchor{}, err
	}
	if anchor.OwnerRef != configuration.OwnerRef || anchor.Repository.RepositoryID != configuration.Repository.RepositoryID {
		return SnapshotAnchor{}, errors.New("restore source anchor is not bound to the current Owner and repository")
	}
	if !anchor.ProtectRecovery && (operation.State == "pending" || operation.State == "staged") {
		if err := validateRestoreSource(configuration, anchor); err != nil {
			return SnapshotAnchor{}, err
		}
	}
	switch operation.State {
	case "pending":
		if operation.Receipt != nil || operation.Result != nil || operation.Abandonment != nil {
			return SnapshotAnchor{}, errors.New("pending restore operation contains a receipt or result")
		}
	case "staged":
		if operation.Receipt == nil || operation.Result != nil || operation.Abandonment != nil {
			return SnapshotAnchor{}, errors.New("staged restore operation does not contain exactly its repository receipt")
		}
		request, err := repositoryRestoreRequestFromRecovery(operation.Recovery, anchor)
		if err != nil {
			return SnapshotAnchor{}, err
		}
		if err := validateRestoreReceipt(*operation.Receipt, request); err != nil {
			return SnapshotAnchor{}, err
		}
	case "completed":
		if operation.Result == nil || operation.Abandonment != nil {
			return SnapshotAnchor{}, errors.New("completed restore operation has no result")
		}
		if err := s.validateRestoreRetentionResult(*operation.Result, operation.Input, anchor); err != nil {
			return SnapshotAnchor{}, err
		}
	case "abandoned":
		if operation.Abandonment == nil || operation.Result != nil {
			return SnapshotAnchor{}, errors.New("abandoned restore operation lacks its terminal evidence")
		}
		if err := VerifyRestoreAbandonment(s.workspaceRoot, *operation.Abandonment); err != nil {
			return SnapshotAnchor{}, err
		}
		if err := validateRestoreAbandonmentBinding(operation, anchor, *operation.Abandonment); err != nil {
			return SnapshotAnchor{}, err
		}
		if operation.Receipt != nil {
			request, err := repositoryRestoreRequestFromRecovery(operation.Recovery, anchor)
			if err != nil {
				return SnapshotAnchor{}, err
			}
			if err := validateRestoreReceipt(*operation.Receipt, request); err != nil {
				return SnapshotAnchor{}, err
			}
		}
	default:
		return SnapshotAnchor{}, fmt.Errorf("restore operation state %q is not resumable or terminal", operation.State)
	}
	return anchor, nil
}

func validateRestoreRetentionRecoveryBinding(operation restoreOperation, anchor SnapshotAnchor) error {
	recovery := operation.Recovery
	input := operation.Input
	if recovery.OwnerRef != input.OwnerRef ||
		recovery.AuthorityRef != input.AuthorityRef ||
		!lineagesEqual(recovery.AuthorizationLineage, input.AuthorizationLineage) ||
		recovery.PolicyArtifactDigest != input.PolicyArtifactDigest ||
		recovery.SnapshotAnchorID != input.SnapshotAnchorID ||
		recovery.OperationID != input.OperationID ||
		recovery.SnapshotAnchorID != anchor.ID ||
		!lineagesEqual(recovery.SnapshotLineage, anchor.Lineage) ||
		recovery.RepositoryID != anchor.Repository.RepositoryID ||
		recovery.SnapshotID != anchor.Snapshot.SnapshotID ||
		recovery.SnapshotContentDigest != anchor.Snapshot.ContentDigest ||
		recovery.StagingPath != RestoreStagingPath(input.OperationID) {
		return errors.New("restore recovery anchor differs from its journal or selected snapshot anchor")
	}
	return nil
}

func (s *Service) validateRestoreRetentionResult(
	result RestoreResult,
	input restoreOperationInput,
	anchor SnapshotAnchor,
) error {
	if err := VerifyRestoreResult(s.workspaceRoot, result); err != nil {
		return err
	}
	if result.OwnerRef != input.OwnerRef || result.AuthorityRef != input.AuthorityRef ||
		!lineagesEqual(result.AuthorizationLineage, input.AuthorizationLineage) ||
		result.SnapshotAnchorID != input.SnapshotAnchorID || result.OperationID != input.OperationID {
		return errors.New("restore result differs from its journal input")
	}
	if err := validateRestoreRetentionRecoveryBinding(
		restoreOperation{Input: input, Recovery: result.RecoveryAnchor}, anchor,
	); err != nil {
		return err
	}
	stored, err := s.loadStoredRestoreResult(result.ID)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(stored, result) {
		return errors.New("restore result differs from its content-addressed evidence")
	}
	return nil
}

func (s *Service) completedRestoreRetentionSources(
	ctx context.Context,
	configuration Configuration,
	leases []restoreRetentionLease,
) (map[string]time.Time, error) {
	needed := make(map[string]struct{}, len(leases))
	for _, lease := range leases {
		needed[lease.anchorID] = struct{}{}
	}
	completed := make(map[string]time.Time, len(needed))
	resultIDs, err := s.historyIDs(ctx, restoreResultDirectory)
	if err != nil {
		return nil, fmt.Errorf("backuplifecycle: inspect completed restore results before retention snapshot: %w", err)
	}
	for _, resultID := range resultIDs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		path := restoreResultDirectory + "/" + strings.TrimPrefix(resultID, "sha256:") + ".json"
		var result RestoreResult
		if err := s.readJSON(path, &result); err != nil {
			return nil, fmt.Errorf("backuplifecycle: inspect restore result %s before retention snapshot: %w", resultID, err)
		}
		if _, relevant := needed[result.SnapshotAnchorID]; !relevant || result.OwnerRef != configuration.OwnerRef {
			continue
		}
		if result.RecoveryAnchor.RepositoryID != configuration.Repository.RepositoryID {
			return nil, fmt.Errorf("backuplifecycle: restore result %s is bound to a different repository", resultID)
		}
		if result.ID != resultID {
			return nil, fmt.Errorf("backuplifecycle: restore result %s identity differs from its content address", resultID)
		}
		anchor, err := s.loadStoredSnapshotAnchor(result.SnapshotAnchorID)
		if err != nil {
			return nil, fmt.Errorf("backuplifecycle: validate completed restore result %s source anchor: %w", resultID, err)
		}
		if err := validateRestoreSource(configuration, anchor); err != nil {
			return nil, fmt.Errorf("backuplifecycle: validate completed restore result %s source: %w", resultID, err)
		}
		input := restoreOperationInput{
			OwnerRef: result.OwnerRef, AuthorityRef: result.AuthorityRef,
			AuthorizationLineage: result.AuthorizationLineage,
			PolicyArtifactDigest: result.Request.PolicyArtifactDigest,
			SnapshotAnchorID:     result.SnapshotAnchorID,
			OperationID:          result.OperationID,
		}
		if err := s.validateRestoreRetentionResult(result, input, anchor); err != nil {
			return nil, fmt.Errorf("backuplifecycle: validate completed restore result %s: %w", resultID, err)
		}
		if result.Verification.VerifiedAt.Before(result.RecoveryAnchor.ApprovedAt) || result.Verification.VerifiedAt.After(time.Now().UTC()) {
			return nil, fmt.Errorf("backuplifecycle: completed restore result %s has no verification within its approval and the current clock", resultID)
		}
		// Only a newly authorized, completed restore supersedes an older
		// pending attempt. A late completion of an earlier attempt cannot.
		approvedAt := result.RecoveryAnchor.ApprovedAt
		if previous, exists := completed[result.SnapshotAnchorID]; !exists || approvedAt.After(previous) {
			completed[result.SnapshotAnchorID] = approvedAt
		}
	}
	return completed, nil
}

func restoreRetentionJournalError(operationID string, err error) error {
	if strings.TrimSpace(operationID) == "" {
		operationID = "<unknown>"
	}
	return fmt.Errorf(
		"backuplifecycle: retention-bearing snapshot is blocked by invalid restore operation %s; inspect or resume that restore before creating another snapshot: %w",
		operationID, err,
	)
}
