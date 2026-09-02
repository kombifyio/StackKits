package backuplifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/kombifyio/stackkits/internal/localevidence"
)

const (
	restoreAbandonmentAPI            = "stackkit.local-backup-restore-abandonment/v1"
	restoreAbandonmentApprovalMethod = "explicit-cli-owner-custody"
)

// RestoreAbandonInput authorizes closing one exact pending or staged restore
// journal without invoking the repository runtime or touching live volumes.
type RestoreAbandonInput struct {
	OwnerRef       string           `json:"ownerRef"`
	AuthorityRef   string           `json:"authorityRef"`
	Lineage        AuthorityLineage `json:"lineage"`
	PolicyArtifact []byte           `json:"-"`
	OperationID    string           `json:"operationId"`
	OwnerApproved  bool             `json:"ownerApproved"`
}

// RestoreAbandonment is terminal evidence for an explicitly owner-approved
// abandonment of one historical restore operation. The recovery anchor and
// any staged receipt remain in the operation journal unchanged.
type RestoreAbandonment struct {
	APIVersion           string                                         `json:"apiVersion"`
	ID                   string                                         `json:"id"`
	OwnerRef             string                                         `json:"ownerRef"`
	AuthorityRef         string                                         `json:"authorityRef"`
	AuthorizationLineage AuthorityLineage                               `json:"authorizationLineage"`
	PolicyArtifactDigest string                                         `json:"policyArtifactDigest"`
	SnapshotAnchorID     string                                         `json:"snapshotAnchorId"`
	RecoveryAnchorID     string                                         `json:"recoveryAnchorId"`
	OperationID          string                                         `json:"operationId"`
	ApprovalMethod       string                                         `json:"approvalMethod"`
	AbandonedAt          time.Time                                      `json:"abandonedAt"`
	Signature            localevidence.OwnerRestoreAbandonmentSignature `json:"signature"`
}

// AbandonRestore closes one exact pending or staged restore operation. It is
// deliberately independent of source-topology compatibility and recovery
// expiry: those properties govern whether a restore may run, not whether its
// retained journal may be explicitly released by the current Owner.
func (s *Service) AbandonRestore(ctx context.Context, input RestoreAbandonInput) (RestoreAbandonment, error) {
	if err := s.ready(ctx); err != nil {
		return RestoreAbandonment{}, err
	}
	if !input.OwnerApproved {
		return RestoreAbandonment{}, errors.New("backuplifecycle: restore abandonment requires explicit local Owner approval")
	}
	if !validOperationID(input.OperationID) {
		return RestoreAbandonment{}, errors.New("backuplifecycle: restore operation ID must match [A-Za-z0-9][A-Za-z0-9._-]{0,127}")
	}
	if _, err := s.validateBinding(input.OwnerRef, input.AuthorityRef); err != nil {
		return RestoreAbandonment{}, err
	}
	currentBinding, err := normalizeBinding(input.OwnerRef, input.AuthorityRef, input.Lineage, input.PolicyArtifact)
	if err != nil {
		return RestoreAbandonment{}, err
	}
	operationPath := restoreOperationDirectory + "/" + operationKey(input.OperationID) + ".json"
	operation, err := s.loadRestoreOperation(operationPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return RestoreAbandonment{}, errors.New("backuplifecycle: restore operation does not exist")
		}
		return RestoreAbandonment{}, err
	}
	if operation.Input.OperationID != input.OperationID || operation.Recovery.OperationID != input.OperationID {
		return RestoreAbandonment{}, errors.New("backuplifecycle: restore operation identity differs from its requested operation")
	}
	if err := VerifyRestoreRecoveryAnchor(s.workspaceRoot, operation.Recovery); err != nil {
		return RestoreAbandonment{}, err
	}
	anchor, err := s.loadStoredSnapshotAnchor(operation.Recovery.SnapshotAnchorID)
	if err != nil {
		return RestoreAbandonment{}, err
	}
	if operation.Recovery.OwnerRef != currentBinding.OwnerRef || anchor.OwnerRef != currentBinding.OwnerRef {
		return RestoreAbandonment{}, errors.New("backuplifecycle: restore operation is not bound to the current Owner")
	}
	if err := validateRestoreRetentionRecoveryBinding(operation, anchor); err != nil {
		return RestoreAbandonment{}, err
	}

	switch operation.State {
	case "pending":
		if operation.Receipt != nil || operation.Result != nil || operation.Abandonment != nil {
			return RestoreAbandonment{}, errors.New("backuplifecycle: pending restore operation contains terminal evidence")
		}
	case "staged":
		if operation.Receipt == nil || operation.Result != nil || operation.Abandonment != nil {
			return RestoreAbandonment{}, errors.New("backuplifecycle: staged restore operation does not contain exactly its repository receipt")
		}
		request, requestErr := repositoryRestoreRequestFromRecovery(operation.Recovery, anchor)
		if requestErr != nil {
			return RestoreAbandonment{}, requestErr
		}
		if err := validateRestoreReceipt(*operation.Receipt, request); err != nil {
			return RestoreAbandonment{}, err
		}
	case "abandoned":
		if operation.Abandonment == nil || operation.Result != nil {
			return RestoreAbandonment{}, errors.New("backuplifecycle: abandoned restore operation lacks its terminal evidence")
		}
		if err := VerifyRestoreAbandonment(s.workspaceRoot, *operation.Abandonment); err != nil {
			return RestoreAbandonment{}, err
		}
		if err := validateRestoreAbandonmentBinding(operation, anchor, *operation.Abandonment); err != nil {
			return RestoreAbandonment{}, err
		}
		if err := validateRestoreAbandonmentCurrentBinding(*operation.Abandonment, currentBinding); err != nil {
			return RestoreAbandonment{}, err
		}
		if operation.Receipt != nil {
			request, requestErr := repositoryRestoreRequestFromRecovery(operation.Recovery, anchor)
			if requestErr != nil {
				return RestoreAbandonment{}, requestErr
			}
			if err := validateRestoreReceipt(*operation.Receipt, request); err != nil {
				return RestoreAbandonment{}, err
			}
		}
		return *operation.Abandonment, nil
	case "completed":
		return RestoreAbandonment{}, errors.New("backuplifecycle: completed restore operation cannot be abandoned")
	default:
		return RestoreAbandonment{}, errors.New("backuplifecycle: restore operation state is malformed")
	}

	now := time.Now().UTC()
	if now.Before(operation.Recovery.ApprovedAt) {
		return RestoreAbandonment{}, errors.New("backuplifecycle: restore abandonment clock precedes its Owner approval")
	}
	abandonment := RestoreAbandonment{
		APIVersion:           restoreAbandonmentAPI,
		OwnerRef:             currentBinding.OwnerRef,
		AuthorityRef:         currentBinding.AuthorityRef,
		AuthorizationLineage: currentBinding.Lineage,
		PolicyArtifactDigest: currentBinding.PolicyArtifactDigest,
		SnapshotAnchorID:     operation.Recovery.SnapshotAnchorID,
		RecoveryAnchorID:     operation.Recovery.ID,
		OperationID:          operation.Recovery.OperationID,
		ApprovalMethod:       restoreAbandonmentApprovalMethod,
		AbandonedAt:          now,
	}
	unsigned, err := canonicalRestoreAbandonment(abandonment)
	if err != nil {
		return RestoreAbandonment{}, err
	}
	abandonment.ID = digestBytes(unsigned)
	signingBytes, err := canonicalRestoreAbandonment(abandonment)
	if err != nil {
		return RestoreAbandonment{}, err
	}
	abandonment.Signature, err = localevidence.SignOwnerRestoreAbandonment(s.workspaceRoot, signingBytes)
	if err != nil {
		return RestoreAbandonment{}, fmt.Errorf("backuplifecycle: sign restore abandonment: %w", err)
	}
	if err := VerifyRestoreAbandonment(s.workspaceRoot, abandonment); err != nil {
		return RestoreAbandonment{}, err
	}
	if err := validateRestoreAbandonmentCurrentBinding(abandonment, currentBinding); err != nil {
		return RestoreAbandonment{}, err
	}
	if err := ctx.Err(); err != nil {
		return RestoreAbandonment{}, err
	}
	persisted := restoreOperation{
		APIVersion:  operation.APIVersion,
		State:       "abandoned",
		Input:       operation.Input,
		Recovery:    operation.Recovery,
		Receipt:     operation.Receipt,
		Abandonment: &abandonment,
	}
	if err := s.writeJSON(operationPath, persisted); err != nil {
		return RestoreAbandonment{}, err
	}
	return abandonment, nil
}

// VerifyRestoreAbandonment verifies the content identity and current local
// Owner signature of one terminal abandonment record.
func VerifyRestoreAbandonment(workspaceRoot string, abandonment RestoreAbandonment) error {
	if abandonment.APIVersion != restoreAbandonmentAPI ||
		!validDigest(abandonment.ID) ||
		abandonment.OwnerRef == "" || abandonment.AuthorityRef == "" ||
		!validDigest(abandonment.PolicyArtifactDigest) ||
		!validDigest(abandonment.SnapshotAnchorID) ||
		!validDigest(abandonment.RecoveryAnchorID) ||
		!validOperationID(abandonment.OperationID) ||
		abandonment.ApprovalMethod != restoreAbandonmentApprovalMethod ||
		abandonment.AbandonedAt.IsZero() {
		return errors.New("backuplifecycle: restore abandonment is incomplete")
	}
	if err := validateLineage(abandonment.AuthorizationLineage); err != nil {
		return err
	}
	unsigned := abandonment
	unsigned.ID = ""
	unsigned.Signature = localevidence.OwnerRestoreAbandonmentSignature{}
	canonicalUnsigned, err := canonicalRestoreAbandonment(unsigned)
	if err != nil {
		return fmt.Errorf("backuplifecycle: encode restore abandonment identity: %w", err)
	}
	if abandonment.ID != digestBytes(canonicalUnsigned) {
		return errors.New("backuplifecycle: restore abandonment content identity does not verify")
	}
	signingBytes, err := canonicalRestoreAbandonment(abandonment)
	if err != nil {
		return err
	}
	return localevidence.VerifyOwnerRestoreAbandonment(workspaceRoot, signingBytes, abandonment.Signature)
}

func validateRestoreAbandonmentBinding(
	operation restoreOperation,
	anchor SnapshotAnchor,
	abandonment RestoreAbandonment,
) error {
	recovery := operation.Recovery
	input := operation.Input
	if abandonment.OwnerRef != recovery.OwnerRef ||
		abandonment.SnapshotAnchorID != recovery.SnapshotAnchorID ||
		abandonment.SnapshotAnchorID != anchor.ID ||
		abandonment.RecoveryAnchorID != recovery.ID ||
		abandonment.OperationID != recovery.OperationID ||
		abandonment.OperationID != input.OperationID {
		return errors.New("backuplifecycle: restore abandonment differs from its journal or signed recovery anchor")
	}
	if abandonment.AbandonedAt.Before(recovery.ApprovedAt) {
		return errors.New("backuplifecycle: restore abandonment precedes its Owner approval")
	}
	return nil
}

func validateRestoreAbandonmentCurrentBinding(
	abandonment RestoreAbandonment,
	current normalizedBinding,
) error {
	if abandonment.OwnerRef != current.OwnerRef ||
		abandonment.AuthorityRef != current.AuthorityRef ||
		!lineagesEqual(abandonment.AuthorizationLineage, current.Lineage) ||
		abandonment.PolicyArtifactDigest != current.PolicyArtifactDigest {
		return errors.New("backuplifecycle: restore abandonment differs from current authorization")
	}
	return nil
}

func canonicalRestoreAbandonment(abandonment RestoreAbandonment) ([]byte, error) {
	abandonment.Signature = localevidence.OwnerRestoreAbandonmentSignature{}
	encoded, err := json.Marshal(abandonment)
	if err != nil {
		return nil, fmt.Errorf("backuplifecycle: encode canonical restore abandonment: %w", err)
	}
	return encoded, nil
}
