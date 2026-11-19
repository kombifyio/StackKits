package backuplifecycle

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/localevidence"
)

type RestoreRecoveryAnchor struct {
	APIVersion            string                                      `json:"apiVersion"`
	ID                    string                                      `json:"id"`
	OwnerRef              string                                      `json:"ownerRef"`
	AuthorityRef          string                                      `json:"authorityRef"`
	AuthorizationLineage  AuthorityLineage                            `json:"authorizationLineage"`
	PolicyArtifactDigest  string                                      `json:"policyArtifactDigest"`
	SnapshotAnchorID      string                                      `json:"snapshotAnchorId"`
	SnapshotLineage       AuthorityLineage                            `json:"snapshotLineage"`
	RepositoryID          string                                      `json:"repositoryId"`
	SnapshotID            string                                      `json:"snapshotId"`
	SnapshotContentDigest string                                      `json:"snapshotContentDigest"`
	OperationID           string                                      `json:"operationId"`
	StagingPath           string                                      `json:"stagingPath"`
	Mode                  string                                      `json:"mode"`
	ApprovalMethod        string                                      `json:"approvalMethod"`
	ApprovedAt            time.Time                                   `json:"approvedAt"`
	ExpiresAt             time.Time                                   `json:"expiresAt"`
	Signature             localevidence.OwnerRestoreRecoverySignature `json:"signature"`
}

type RestoreResult struct {
	APIVersion           string                                    `json:"apiVersion"`
	ID                   string                                    `json:"id"`
	OwnerRef             string                                    `json:"ownerRef"`
	AuthorityRef         string                                    `json:"authorityRef"`
	AuthorizationLineage AuthorityLineage                          `json:"authorizationLineage"`
	SnapshotAnchorID     string                                    `json:"snapshotAnchorId"`
	SnapshotLineage      AuthorityLineage                          `json:"snapshotLineage"`
	OperationID          string                                    `json:"operationId"`
	RecoveryAnchor       RestoreRecoveryAnchor                     `json:"recoveryAnchor"`
	Request              RepositoryRestoreRequest                  `json:"request"`
	Receipt              RepositoryRestoreReceipt                  `json:"receipt"`
	Verification         RestoreVerification                       `json:"verification"`
	Signature            localevidence.OwnerRestoreResultSignature `json:"signature"`
}

func VerifyRestoreRecoveryAnchor(workspaceRoot string, recovery RestoreRecoveryAnchor) error {
	if recovery.APIVersion != restoreRecoveryAPI ||
		!validDigest(recovery.ID) ||
		recovery.OwnerRef == "" || recovery.AuthorityRef == "" ||
		!validDigest(recovery.PolicyArtifactDigest) ||
		!validDigest(recovery.SnapshotAnchorID) ||
		recovery.RepositoryID == "" || !validPortableValue(recovery.SnapshotID) ||
		!validDigest(recovery.SnapshotContentDigest) ||
		!validOperationID(recovery.OperationID) ||
		recovery.StagingPath != RestoreStagingPath(recovery.OperationID) ||
		recovery.Mode != "verified-staging-only" ||
		recovery.ApprovalMethod != "explicit-cli-owner-custody" ||
		recovery.ApprovedAt.IsZero() || recovery.ExpiresAt.IsZero() ||
		!recovery.ExpiresAt.After(recovery.ApprovedAt) ||
		recovery.ExpiresAt.Sub(recovery.ApprovedAt) > 24*time.Hour {
		return errors.New("backuplifecycle: restore recovery anchor is incomplete or widens staging authority")
	}
	if err := validateLineage(recovery.AuthorizationLineage); err != nil {
		return err
	}
	if err := validateLineage(recovery.SnapshotLineage); err != nil {
		return err
	}
	unsigned := recovery
	unsigned.ID = ""
	unsigned.Signature = localevidence.OwnerRestoreRecoverySignature{}
	canonicalUnsigned, err := json.Marshal(unsigned)
	if err != nil {
		return fmt.Errorf("backuplifecycle: encode restore recovery identity: %w", err)
	}
	if recovery.ID != digestBytes(canonicalUnsigned) {
		return errors.New("backuplifecycle: restore recovery anchor content identity does not verify")
	}
	signingBytes, err := canonicalRestoreRecovery(recovery)
	if err != nil {
		return err
	}
	return localevidence.VerifyOwnerRestoreRecovery(workspaceRoot, signingBytes, recovery.Signature)
}

func VerifyRestoreResult(workspaceRoot string, result RestoreResult) error {
	if result.APIVersion != restoreResultAPI ||
		!validDigest(result.ID) ||
		result.OwnerRef == "" || result.AuthorityRef == "" ||
		!validDigest(result.SnapshotAnchorID) ||
		!validOperationID(result.OperationID) {
		return errors.New("backuplifecycle: restore result is incomplete")
	}
	if err := VerifyRestoreRecoveryAnchor(workspaceRoot, result.RecoveryAnchor); err != nil {
		return err
	}
	if result.OwnerRef != result.RecoveryAnchor.OwnerRef ||
		result.AuthorityRef != result.RecoveryAnchor.AuthorityRef ||
		!lineagesEqual(result.AuthorizationLineage, result.RecoveryAnchor.AuthorizationLineage) ||
		result.SnapshotAnchorID != result.RecoveryAnchor.SnapshotAnchorID ||
		!lineagesEqual(result.SnapshotLineage, result.RecoveryAnchor.SnapshotLineage) ||
		result.OperationID != result.RecoveryAnchor.OperationID {
		return errors.New("backuplifecycle: restore result differs from its Owner-approved recovery anchor")
	}
	if result.Request.OwnerRef != result.OwnerRef ||
		result.Request.AuthorityRef != result.AuthorityRef ||
		!lineagesEqual(result.Request.AuthorizationLineage, result.AuthorizationLineage) ||
		result.Request.PolicyArtifactDigest != result.RecoveryAnchor.PolicyArtifactDigest ||
		!validDigest(result.Request.SnapshotSourceDigest) ||
		result.Request.RepositoryID != result.RecoveryAnchor.RepositoryID ||
		result.Request.SnapshotAnchorID != result.SnapshotAnchorID ||
		result.Request.SnapshotReceipt.SnapshotID != result.RecoveryAnchor.SnapshotID ||
		result.Request.SnapshotReceipt.ContentDigest != result.RecoveryAnchor.SnapshotContentDigest ||
		result.Request.OperationID != result.OperationID ||
		result.Request.StagingPath != result.RecoveryAnchor.StagingPath {
		return errors.New("backuplifecycle: restore request differs from its signed recovery anchor")
	}
	if err := validateSnapshotReceipt(result.Request.SnapshotReceipt, result.Request.SnapshotRequest); err != nil {
		return err
	}
	if err := validateRestoreReceipt(result.Receipt, result.Request); err != nil {
		return err
	}
	if result.Verification.APIVersion != restoreVerificationAPI ||
		result.Verification.OwnerRef != result.OwnerRef ||
		result.Verification.OwnerBindingDigest != result.AuthorizationLineage.OwnerBindingDigest ||
		result.Verification.PocketIDSubject != result.AuthorizationLineage.PocketIDSubject ||
		result.Verification.PlanHash != result.AuthorizationLineage.Binding.PlanHash ||
		!result.Verification.ServicesVerified ||
		!restoreApprovalCurrent(result.RecoveryAnchor, result.Receipt.CompletedAt) ||
		!restoreApprovalCurrent(result.RecoveryAnchor, result.Verification.VerifiedAt) ||
		result.Verification.VerifiedAt.Before(result.Receipt.CompletedAt) {
		return errors.New("backuplifecycle: restore result lacks exact current local platform verification")
	}
	unsigned := result
	unsigned.ID = ""
	unsigned.Signature = localevidence.OwnerRestoreResultSignature{}
	canonicalUnsigned, err := json.Marshal(unsigned)
	if err != nil {
		return fmt.Errorf("backuplifecycle: encode restore result identity: %w", err)
	}
	if result.ID != digestBytes(canonicalUnsigned) {
		return errors.New("backuplifecycle: restore result content identity does not verify")
	}
	signingBytes, err := canonicalRestoreResult(result)
	if err != nil {
		return err
	}
	return localevidence.VerifyOwnerRestoreResult(workspaceRoot, signingBytes, result.Signature)
}

// LoadRestoreResult loads one content-addressed staged restore result and
// verifies its complete owner-signed recovery chain before returning it.
// Callers cannot select a path: resultID alone derives the confined location.
func LoadRestoreResult(workspaceRoot, resultID string) (RestoreResult, error) {
	if !validDigest(resultID) {
		return RestoreResult{}, errors.New("backuplifecycle: restore result identity is invalid")
	}
	root, err := confinedfs.Open(workspaceRoot)
	if err != nil {
		return RestoreResult{}, err
	}
	defer func() { _ = root.Close() }()
	view, err := root.View(".")
	if err != nil {
		return RestoreResult{}, err
	}
	resultPath := restoreResultDirectory + "/" + strings.TrimPrefix(resultID, "sha256:") + ".json"
	file, err := view.Open(resultPath)
	if err != nil {
		return RestoreResult{}, err
	}
	defer func() { _ = file.Close() }()
	var result RestoreResult
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return RestoreResult{}, fmt.Errorf("backuplifecycle: decode %s: %w", resultPath, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return RestoreResult{}, fmt.Errorf("backuplifecycle: decode %s: trailing content: %w", resultPath, err)
	}
	if result.ID != resultID {
		return RestoreResult{}, errors.New("backuplifecycle: stored restore result identity differs from its content address")
	}
	if err := VerifyRestoreResult(root.Name(), result); err != nil {
		return RestoreResult{}, err
	}
	return result, nil
}

func (s *Service) loadStoredRestoreResult(resultID string) (RestoreResult, error) {
	return LoadRestoreResult(s.workspaceRoot, resultID)
}

func canonicalRestoreRecovery(recovery RestoreRecoveryAnchor) ([]byte, error) {
	recovery.Signature = localevidence.OwnerRestoreRecoverySignature{}
	encoded, err := json.Marshal(recovery)
	if err != nil {
		return nil, fmt.Errorf("backuplifecycle: encode canonical restore recovery anchor: %w", err)
	}
	return encoded, nil
}

func canonicalRestoreResult(result RestoreResult) ([]byte, error) {
	result.Signature = localevidence.OwnerRestoreResultSignature{}
	encoded, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("backuplifecycle: encode canonical restore result: %w", err)
	}
	return encoded, nil
}
