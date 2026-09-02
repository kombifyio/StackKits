package backuplifecycle

import (
	"time"

	"github.com/kombifyio/stackkits/internal/localbackuppolicy"
)

// RecoveryObjectiveAssessment derives current observations from one governed
// target-local objective. It is not persisted and cannot authorize operations.
type RecoveryObjectiveAssessment struct {
	BindingRef   string                 `json:"bindingRef"`
	WorkloadRefs []string               `json:"workloadRefs"`
	DataLoss     RecoveryObjectiveCheck `json:"dataLoss"`
	RecoveryTime RecoveryObjectiveCheck `json:"recoveryTime"`
}

type RecoveryObjectiveCheck struct {
	State           string `json:"state"`
	Basis           string `json:"basis"`
	LimitSeconds    int    `json:"limitSeconds"`
	ObservedSeconds *int64 `json:"observedSeconds,omitempty"`
	EvidenceID      string `json:"evidenceId,omitempty"`
	Reason          string `json:"reason,omitempty"`
}

func assessRecoveryObjectives(objectives []localbackuppolicy.RecoveryObjective, history History) []RecoveryObjectiveAssessment {
	if len(objectives) == 0 {
		return nil
	}
	results := make([]RecoveryObjectiveAssessment, 0, len(objectives))
	for _, objective := range objectives {
		assessment := RecoveryObjectiveAssessment{
			BindingRef: objective.BindingRef, WorkloadRefs: append([]string(nil), objective.WorkloadRefs...),
			DataLoss:     RecoveryObjectiveCheck{State: "unverified", Basis: "retained-snapshot-quiescence-start-age", LimitSeconds: objective.MaxDataLossSeconds, Reason: "current-snapshot-capture-unverified"},
			RecoveryTime: RecoveryObjectiveCheck{State: "unverified", Basis: "functional-application-recovery-duration", LimitSeconds: objective.RecoveryTimeSeconds, Reason: "functional-recovery-not-verified"},
		}
		if history.Availability != nil && history.Availability.State == "present" && history.Snapshot.State == "recorded" &&
			history.Availability.EvidenceID == history.Snapshot.EvidenceID && history.Snapshot.CaptureStartedAt != nil &&
			!history.Snapshot.CaptureStartedAt.After(history.Availability.ObservedAt) {
			elapsed := history.Availability.ObservedAt.Sub(*history.Snapshot.CaptureStartedAt)
			age := int64(elapsed / time.Second)
			if elapsed%time.Second != 0 {
				age++
			}
			assessment.DataLoss.State, assessment.DataLoss.Reason = "within-objective", ""
			assessment.DataLoss.ObservedSeconds, assessment.DataLoss.EvidenceID = &age, history.Snapshot.EvidenceID
			if age > int64(objective.MaxDataLossSeconds) {
				assessment.DataLoss.State, assessment.DataLoss.Reason = "breached", "retained-snapshot-too-old"
			}
		}
		results = append(results, assessment)
	}
	return results
}
