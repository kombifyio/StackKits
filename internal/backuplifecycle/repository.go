package backuplifecycle

import (
	"context"
	"errors"
	"time"

	"github.com/kombifyio/stackkits/internal/localbackuppolicy"
)

type RepositoryConfiguration struct {
	OwnerRef             string           `json:"ownerRef"`
	AuthorityRef         string           `json:"authorityRef"`
	Lineage              AuthorityLineage `json:"lineage"`
	PolicyArtifactDigest string           `json:"policyArtifactDigest"`
	OperationID          string           `json:"operationId"`
	Policy               Policy           `json:"policy"`
}

type RepositoryScope struct {
	OwnerRef     string           `json:"ownerRef"`
	AuthorityRef string           `json:"authorityRef"`
	RepositoryID string           `json:"repositoryId"`
	Lineage      AuthorityLineage `json:"lineage"`
}

type RepositoryReceipt struct {
	APIVersion          string `json:"apiVersion"`
	RepositoryID        string `json:"repositoryId"`
	Backend             string `json:"backend"`
	ConfigurationDigest string `json:"configurationDigest"`
}

type RepositoryStatus struct {
	RepositoryID string          `json:"repositoryId"`
	Ready        bool            `json:"ready"`
	Consistency  Consistency     `json:"consistency"`
	History      *History        `json:"history,omitempty"`
	Coverage     *BackupCoverage `json:"coverage,omitempty"`
}

type RepositorySnapshotRequest struct {
	Workloads            []string                           `json:"workloads,omitempty"`
	OwnerRef             string                             `json:"ownerRef"`
	AuthorityRef         string                             `json:"authorityRef"`
	Lineage              AuthorityLineage                   `json:"lineage"`
	PolicyArtifactDigest string                             `json:"policyArtifactDigest"`
	RepositoryID         string                             `json:"repositoryId"`
	OperationID          string                             `json:"operationId"`
	Source               string                             `json:"source"`
	Excludes             []string                           `json:"excludes"`
	Sources              []localbackuppolicy.SnapshotSource `json:"sources,omitempty"`
	Consistency          Consistency                        `json:"consistency"`
	ProtectRecovery      bool                               `json:"protectRecovery,omitempty"`
}

type RepositorySnapshotReceipt struct {
	APIVersion    string      `json:"apiVersion"`
	RepositoryID  string      `json:"repositoryId"`
	SnapshotID    string      `json:"snapshotId"`
	OperationID   string      `json:"operationId"`
	RequestDigest string      `json:"requestDigest"`
	ContentDigest string      `json:"contentDigest"`
	Consistency   Consistency `json:"consistency"`
	CreatedAt     time.Time   `json:"createdAt"`
}

type RepositoryRestoreRequest struct {
	OwnerRef             string                    `json:"ownerRef"`
	AuthorityRef         string                    `json:"authorityRef"`
	AuthorizationLineage AuthorityLineage          `json:"authorizationLineage"`
	PolicyArtifactDigest string                    `json:"policyArtifactDigest"`
	RepositoryID         string                    `json:"repositoryId"`
	SnapshotAnchorID     string                    `json:"snapshotAnchorId"`
	SnapshotSourceDigest string                    `json:"snapshotSourceDigest"`
	SnapshotRequest      RepositorySnapshotRequest `json:"snapshotRequest"`
	SnapshotReceipt      RepositorySnapshotReceipt `json:"snapshotReceipt"`
	OperationID          string                    `json:"operationId"`
	StagingPath          string                    `json:"stagingPath"`
}

type RepositoryRestoreReceipt struct {
	APIVersion                string    `json:"apiVersion"`
	RepositoryID              string    `json:"repositoryId"`
	SnapshotID                string    `json:"snapshotId"`
	OperationID               string    `json:"operationId"`
	RequestDigest             string    `json:"requestDigest"`
	StagingPath               string    `json:"stagingPath"`
	SnapshotContentDigest     string    `json:"snapshotContentDigest"`
	RepositoryContentVerified bool      `json:"repositoryContentVerified"`
	CompletedAt               time.Time `json:"completedAt"`
}

// RepositoryRuntime owns repository-specific side effects. LookupSnapshot
// must return a receipt only for the exact request digest, allowing a pending
// journal to recover after repository success without duplicating a snapshot.
type RepositoryRuntime interface {
	Configure(context.Context, RepositoryConfiguration) (RepositoryReceipt, error)
	Status(context.Context, RepositoryScope) (RepositoryStatus, error)
	LookupSnapshot(context.Context, RepositorySnapshotRequest) (RepositorySnapshotReceipt, bool, error)
	CreateSnapshot(context.Context, RepositorySnapshotRequest) (RepositorySnapshotReceipt, error)
	RestoreSnapshot(context.Context, RepositoryRestoreRequest) (RepositoryRestoreReceipt, error)
}

// SnapshotQuiesceRuntime is an optional extension for productive runtimes
// that can stop and restore Docker writers around one Kopia snapshot. Keeping
// it optional preserves legacy repository fakes and v1 journals.
type SnapshotQuiesceRuntime interface {
	CreateSnapshotWithQuiescence(
		context.Context,
		RepositorySnapshotRequest,
		SnapshotQuiescence,
		func(SnapshotQuiescence) error,
	) (RepositorySnapshotReceipt, error)
}

func validateRepositoryReceipt(receipt RepositoryReceipt, configuration RepositoryConfiguration) error {
	if receipt.APIVersion != repositoryReceiptAPI ||
		receipt.RepositoryID == "" || receipt.Backend == "" ||
		receipt.ConfigurationDigest != RepositoryConfigurationDigest(configuration) {
		return errors.New("backuplifecycle: repository receipt is incomplete or differs from the exact configuration request")
	}
	return nil
}

func validateSnapshotReceipt(receipt RepositorySnapshotReceipt, request RepositorySnapshotRequest) error {
	if receipt.APIVersion != repositorySnapshotAPI ||
		receipt.RepositoryID != request.RepositoryID ||
		receipt.OperationID != request.OperationID ||
		receipt.RequestDigest != RepositorySnapshotRequestDigest(request) ||
		receipt.SnapshotID == "" || !validDigest(receipt.ContentDigest) ||
		receipt.Consistency != ConsistencyCrashConsistent || receipt.CreatedAt.IsZero() {
		return errors.New("backuplifecycle: repository snapshot receipt is incomplete or differs from the exact request")
	}
	return nil
}

func validateRestoreReceipt(receipt RepositoryRestoreReceipt, request RepositoryRestoreRequest) error {
	if receipt.APIVersion != repositoryRestoreAPI ||
		receipt.RepositoryID != request.RepositoryID ||
		receipt.SnapshotID != request.SnapshotReceipt.SnapshotID ||
		receipt.OperationID != request.OperationID ||
		receipt.RequestDigest != RepositoryRestoreRequestDigest(request) ||
		receipt.StagingPath != request.StagingPath ||
		receipt.SnapshotContentDigest != request.SnapshotReceipt.ContentDigest ||
		!receipt.RepositoryContentVerified ||
		receipt.CompletedAt.IsZero() {
		return errors.New("backuplifecycle: repository restore receipt is incomplete or differs from the exact staged restore request")
	}
	return nil
}

// RepositoryConfigurationDigest is the canonical digest a runtime must echo
// in its configuration receipt.
func RepositoryConfigurationDigest(configuration RepositoryConfiguration) string {
	return digestJSON(configuration)
}

// RepositorySnapshotRequestDigest is the canonical digest a runtime must echo
// in both lookup and create receipts.
func RepositorySnapshotRequestDigest(request RepositorySnapshotRequest) string {
	return digestJSON(request)
}

// RepositoryRestoreRequestDigest is the canonical digest a runtime must echo
// after full verification and staging of the exact selected snapshot anchor.
func RepositoryRestoreRequestDigest(request RepositoryRestoreRequest) string {
	return digestJSON(request)
}

func repositoryConfigurationsEqual(left, right RepositoryConfiguration) bool {
	return left.OwnerRef == right.OwnerRef &&
		left.AuthorityRef == right.AuthorityRef &&
		lineagesEqual(left.Lineage, right.Lineage) &&
		left.PolicyArtifactDigest == right.PolicyArtifactDigest &&
		left.OperationID == right.OperationID &&
		policiesEqual(left.Policy, right.Policy)
}
