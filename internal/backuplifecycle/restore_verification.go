package backuplifecycle

import (
	"context"
	"errors"
	"time"
)

type RestoreVerificationRequest struct {
	OwnerRef             string           `json:"ownerRef"`
	AuthorizationLineage AuthorityLineage `json:"authorizationLineage"`
	SnapshotAnchorID     string           `json:"snapshotAnchorId"`
	OperationID          string           `json:"operationId"`
	StagingPath          string           `json:"stagingPath"`
}

type RestoreVerification struct {
	APIVersion         string    `json:"apiVersion"`
	OwnerRef           string    `json:"ownerRef"`
	OwnerBindingDigest string    `json:"ownerBindingDigest"`
	PocketIDSubject    string    `json:"pocketIdSubject"`
	PlanHash           string    `json:"planHash"`
	ServicesVerified   bool      `json:"servicesVerified"`
	VerifiedAt         time.Time `json:"verifiedAt"`
	// DataReadback is set by live restore activation only: it proves the bytes
	// now in each live volume equal the bytes restored from the snapshot.
	DataReadback *DataReadback `json:"dataReadback,omitempty"`
}

// DataReadbackMethod names the volume digest every DataReadback uses.
const DataReadbackMethod = "volume-file-tree-sha256/v1"

// DataReadback compares restored application data with its staged snapshot
// content before services start. It does not prove application behaviour.
type DataReadback struct {
	Method  string           `json:"method"`
	Volumes []VolumeReadback `json:"volumes"`
}

type VolumeReadback struct {
	MetadataDigest string `json:"metadataDigest,omitempty"`
	LiveName       string `json:"liveName"`
	Files          int64  `json:"files"`
	Bytes          int64  `json:"bytes"`
	Digest         string `json:"digest"`
}

type RestorePostVerifier func(context.Context, RestoreVerificationRequest) (RestoreVerification, error)

func validateRestoreVerification(
	verification RestoreVerification,
	configuration Configuration,
	receipt RepositoryRestoreReceipt,
) error {
	if verification.APIVersion != restoreVerificationAPI ||
		verification.OwnerRef != configuration.OwnerRef ||
		verification.OwnerBindingDigest != configuration.Lineage.OwnerBindingDigest ||
		verification.PocketIDSubject != configuration.Lineage.PocketIDSubject ||
		verification.PlanHash != configuration.Lineage.Binding.PlanHash ||
		!verification.ServicesVerified ||
		verification.VerifiedAt.IsZero() ||
		verification.VerifiedAt.Before(receipt.CompletedAt) {
		return errors.New("backuplifecycle: staged restore post-verification differs from the current Owner, Plan, or service closure")
	}
	return nil
}
