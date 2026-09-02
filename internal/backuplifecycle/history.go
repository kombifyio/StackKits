package backuplifecycle

import (
	"context"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/localbackuppolicy"
)

// History reports owner-authenticated receipts for the current source policy.
// Availability separately observes the latest current-authority snapshot
// manifest. Neither observation proves complete blob integrity, successful
// application activation, or compliance with a recovery-time objective.
type History struct {
	ObservedAt         time.Time                     `json:"observedAt"`
	Scope              string                        `json:"scope"`
	Issue              string                        `json:"issue,omitempty"`
	Snapshot           EvidenceAge                   `json:"snapshot"`
	StagedRestore      EvidenceAge                   `json:"stagedRestore"`
	Availability       *SnapshotAvailability         `json:"availability,omitempty"`
	RecoveryObjectives []RecoveryObjectiveAssessment `json:"recoveryObjectives,omitempty"`
}

type EvidenceAge struct {
	State            string     `json:"state"`
	EvidenceID       string     `json:"evidenceId,omitempty"`
	PlanHash         string     `json:"planHash,omitempty"`
	CurrentPlan      bool       `json:"currentPlan"`
	RecordedAt       *time.Time `json:"recordedAt,omitempty"`
	CaptureStartedAt *time.Time `json:"captureStartedAt,omitempty"`
	AgeSeconds       *int64     `json:"ageSeconds,omitempty"`
}

func (s *Service) history(ctx context.Context, configuration Configuration, now time.Time, repositoryReady bool) (History, error) {
	result := History{
		ObservedAt: now.UTC(), Scope: "current-source-policy-receipts",
		Snapshot: EvidenceAge{State: "unverified"}, StagedRestore: EvidenceAge{State: "unverified"},
	}
	currentSource, err := localbackuppolicy.SourceDigest(configuration.Policy.SourceProjection())
	if err != nil {
		return History{}, err
	}
	for _, directory := range []string{anchorDirectory, restoreResultDirectory} {
		ids, err := s.historyIDs(ctx, directory)
		if err != nil {
			return History{}, err
		}
		for _, id := range ids {
			if err := ctx.Err(); err != nil {
				return History{}, err
			}
			if directory == anchorDirectory {
				anchor, err := s.loadStoredSnapshotAnchor(id)
				if err != nil {
					result.Issue = "history-incomplete"
					continue
				}
				sourceDigest, err := localbackuppolicy.SourceDigest(anchor.Policy.SourceProjection())
				if err != nil {
					result.Issue = "history-incomplete"
					continue
				}
				if anchor.OwnerRef != configuration.OwnerRef || anchor.AuthorityRef != configuration.AuthorityRef ||
					anchor.PolicyArtifactDigest != configuration.PolicyArtifactDigest || sourceDigest != currentSource || anchor.Repository.RepositoryID != configuration.Repository.RepositoryID {
					continue
				}
				result.Snapshot = newerEvidence(result.Snapshot, id, anchor.Lineage.Binding.PlanHash, anchor.Snapshot.CreatedAt, now)
				if result.Snapshot.EvidenceID == id && anchor.Quiescence != nil && anchor.Quiescence.CaptureStartedAt != nil {
					startedAt := *anchor.Quiescence.CaptureStartedAt
					result.Snapshot.CaptureStartedAt = &startedAt
				}
				continue
			}
			restore, err := s.loadStoredRestoreResult(id)
			if err != nil {
				result.Issue = "history-incomplete"
				continue
			}
			if restore.OwnerRef != configuration.OwnerRef || restore.AuthorityRef != configuration.AuthorityRef ||
				restore.Request.PolicyArtifactDigest != configuration.PolicyArtifactDigest || restore.Receipt.RepositoryID != configuration.Repository.RepositoryID {
				continue
			}
			anchor, err := s.loadStoredSnapshotAnchor(restore.SnapshotAnchorID)
			if err != nil {
				result.Issue = "history-incomplete"
				continue
			}
			if err := validateRestoreSource(configuration, anchor); err != nil {
				continue
			}
			if restore.Request.SnapshotSourceDigest != "" && restore.Request.SnapshotSourceDigest != currentSource {
				continue
			}
			result.StagedRestore = newerEvidence(result.StagedRestore, id, restore.AuthorizationLineage.Binding.PlanHash, restore.Verification.VerifiedAt, now)
		}
	}
	result.Snapshot.CurrentPlan = result.Snapshot.PlanHash != "" && result.Snapshot.PlanHash == configuration.Lineage.Binding.PlanHash
	result.StagedRestore.CurrentPlan = result.StagedRestore.PlanHash != "" && result.StagedRestore.PlanHash == configuration.Lineage.Binding.PlanHash
	availability, err := s.snapshotAvailability(ctx, configuration, result.Snapshot, now, repositoryReady)
	if err != nil {
		return History{}, err
	}
	result.Availability = &availability
	result.RecoveryObjectives = assessRecoveryObjectives(configuration.Policy.RecoveryObjectives, result)
	return result, nil
}

func newerEvidence(previous EvidenceAge, id, planHash string, recordedAt, now time.Time) EvidenceAge {
	if previous.RecordedAt != nil && !recordedAt.After(*previous.RecordedAt) {
		return previous
	}
	utc := recordedAt.UTC()
	result := EvidenceAge{State: "recorded", EvidenceID: id, PlanHash: planHash, RecordedAt: &utc}
	if recordedAt.After(now) {
		result.State = "clock-skew"
		return result
	}
	seconds := int64(now.Sub(recordedAt) / time.Second)
	result.AgeSeconds = &seconds
	return result
}

func (s *Service) historyIDs(ctx context.Context, directory string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root, err := confinedfs.Open(s.workspaceRoot)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	transaction, err := root.BeginTransaction()
	if err != nil {
		return nil, err
	}
	defer func() { _ = transaction.Close() }()
	exists, _, err := transaction.Exists(directory)
	if err != nil || !exists {
		return nil, err
	}
	entries, err := transaction.Walk(directory)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if entry.Path == directory {
			continue
		}
		name := path.Base(entry.Path)
		id := "sha256:" + strings.TrimSuffix(name, ".json")
		if path.Dir(entry.Path) != directory || !strings.HasSuffix(name, ".json") || !validDigest(id) {
			return nil, fmt.Errorf("backup history contains an unexpected evidence entry: %s", entry.Path)
		}
		ids = append(ids, id)
	}
	return ids, nil
}
