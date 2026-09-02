package backuplifecycle

import (
	"context"
	"reflect"
	"time"
)

// SnapshotAvailability is a fresh read-only observation, never an additional
// durable authority. Present means the exact signed snapshot manifest is
// still readable in the currently authorized repository; it is not a restore
// or full content-integrity result.
type SnapshotAvailability struct {
	State      string    `json:"state"`
	Reason     string    `json:"reason,omitempty"`
	Scope      string    `json:"scope"`
	ObservedAt time.Time `json:"observedAt"`
	EvidenceID string    `json:"evidenceId,omitempty"`
	SnapshotID string    `json:"snapshotId,omitempty"`
}

func (s *Service) snapshotAvailability(ctx context.Context, configuration Configuration, latest EvidenceAge, startedAt time.Time, repositoryReady bool) (SnapshotAvailability, error) {
	result := SnapshotAvailability{
		State: "unverified", Scope: "current-authority-snapshot-manifest", ObservedAt: startedAt,
		EvidenceID: latest.EvidenceID,
	}
	if !repositoryReady {
		result.Reason = "repository-not-ready"
		return result, nil
	}
	if latest.EvidenceID == "" || latest.State != "recorded" || !latest.CurrentPlan {
		result.Reason = "current-snapshot-receipt-unavailable"
		return result, nil
	}
	anchor, err := s.loadStoredSnapshotAnchor(latest.EvidenceID)
	if err != nil {
		result.Reason = "snapshot-receipt-not-authenticated"
		return result, nil
	}
	if anchor.OwnerRef != configuration.OwnerRef || anchor.AuthorityRef != configuration.AuthorityRef ||
		anchor.PolicyArtifactDigest != configuration.PolicyArtifactDigest ||
		anchor.Repository.RepositoryID != configuration.Repository.RepositoryID ||
		!reflect.DeepEqual(anchor.Lineage, configuration.Lineage) {
		result.Reason = "snapshot-authority-changed"
		return result, nil
	}
	request := snapshotRequestFromAnchor(anchor)
	receipt, found, lookupErr := s.runtime.LookupSnapshot(ctx, request)
	result.ObservedAt = time.Now().UTC()
	if err := ctx.Err(); err != nil {
		return SnapshotAvailability{}, err
	}
	if result.ObservedAt.Before(startedAt) || anchor.Snapshot.CreatedAt.After(result.ObservedAt) {
		result.Reason = "clock-skew"
		return result, nil
	}
	if lookupErr != nil {
		result.Reason = "snapshot-lookup-unavailable"
		return result, nil
	}
	if !found {
		result.State, result.Reason = "missing", "snapshot-not-retained"
		return result, nil
	}
	if err := validateSnapshotReceipt(receipt, request); err != nil || !reflect.DeepEqual(receipt, anchor.Snapshot) {
		result.Reason = "snapshot-receipt-mismatch"
		return result, nil
	}
	result.State, result.SnapshotID = "present", receipt.SnapshotID
	return result, nil
}
