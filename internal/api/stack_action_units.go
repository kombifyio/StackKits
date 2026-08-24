package api

import (
	"github.com/kombifyio/stackkits/internal/applyledger"
	"github.com/kombifyio/stackkits/internal/stackaction"
)

// stackActionUnitOutcomes projects the per-unit account onto the StackAction
// wire contract.
//
// The response used to carry one aggregate status, so a rollout that installed
// nine of ten workloads and a rollout that installed all ten were indis-
// tinguishable to every caller downstream. Projecting the ledger is what lets a
// dashboard say which unit is missing and what to do about it, instead of
// showing a checkmark that means nothing.
func stackActionUnitOutcomes(ledger *applyledger.Ledger) ([]stackaction.UnitOutcome, stackaction.OverallOutcome) {
	if ledger == nil || len(ledger.Units) == 0 {
		return nil, ""
	}
	units := make([]stackaction.UnitOutcome, 0, len(ledger.Units))
	for _, unit := range ledger.Units {
		projected := stackaction.UnitOutcome{
			Ref: unit.Ref, Kind: unit.Kind,
			Outcome:     stackActionUnitOutcomeStatus(unit.Outcome),
			Criticality: stackActionUnitCriticality(unit.Criticality),
			WorkloadRef: unit.Subject.WorkloadRef, RequirementID: unit.Subject.RequirementID,
			InstanceRef: unit.Subject.InstanceRef, RuntimeOwnerRef: unit.Subject.RuntimeOwnerRef,
			ModuleRef: unit.Subject.ModuleRef, SiteRef: unit.Subject.SiteRef, NodeRef: unit.Subject.NodeRef,
		}
		if unit.Failure != nil {
			projected.FailureClass = unit.Failure.Class
			projected.FailureCode = unit.Failure.Code
			projected.Retryable = unit.Failure.Retryable
			projected.Transient = unit.Failure.Transient
			projected.Message = unit.Failure.Message
			projected.Remediation = append([]string(nil), unit.Failure.Remediation...)
		}
		units = append(units, projected)
	}
	return units, stackActionOverallOutcome(ledger.Overall)
}

// The three mappings below are exhaustive on purpose. An unmapped value must
// not silently become the empty string, which a reader would render as "no
// problem"; it becomes the closest honest pessimistic value instead.

func stackActionUnitOutcomeStatus(outcome applyledger.Outcome) stackaction.UnitOutcomeStatus {
	switch outcome {
	case applyledger.OutcomeApplied:
		return stackaction.UnitOutcomeApplied
	case applyledger.OutcomeDegraded:
		return stackaction.UnitOutcomeDegraded
	case applyledger.OutcomeSkipped:
		return stackaction.UnitOutcomeSkipped
	case applyledger.OutcomeUnverified:
		return stackaction.UnitOutcomeUnverified
	default:
		return stackaction.UnitOutcomeFailed
	}
}

func stackActionUnitCriticality(criticality applyledger.Criticality) stackaction.UnitCriticality {
	switch criticality {
	case applyledger.CriticalityPlatform:
		return stackaction.UnitCriticalityPlatform
	case applyledger.CriticalityWorkload:
		return stackaction.UnitCriticalityWorkload
	case applyledger.CriticalityAddon:
		return stackaction.UnitCriticalityAddon
	default:
		return stackaction.UnitCriticalityCore
	}
}

func stackActionOverallOutcome(overall applyledger.Overall) stackaction.OverallOutcome {
	switch overall {
	case applyledger.OverallApplied:
		return stackaction.OverallOutcomeApplied
	case applyledger.OverallCompletedDegraded:
		return stackaction.OverallOutcomeCompletedDegraded
	case applyledger.OverallBlocked:
		return stackaction.OverallOutcomeBlocked
	default:
		return stackaction.OverallOutcomeFailed
	}
}
