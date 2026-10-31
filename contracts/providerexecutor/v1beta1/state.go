package providerexecutor

import "fmt"

// CanTransition reports whether the owning ledger may append the next phase.
// It deliberately does not allow skipping absence_pending during decommission.
func CanTransition(operation Operation, from, to Phase) bool {
	if !phaseAllowedForOperation(operation, from) || !phaseAllowedForOperation(operation, to) {
		return false
	}
	// Provider APIs are asynchronous. Re-appending the same nonterminal phase
	// lets the owning ledger retain polling evidence without forcing an adapter
	// to block or turn normal pending work into a terminal failure. Receipt
	// validation separately requires pending status on both sides.
	if from == to {
		return pendingSelfLoopPhase(from)
	}
	// Denied is proof that admission stopped before an executor invocation.
	// Accepted means invocation custody has begun; even a zero-handle ambiguous
	// outcome must fail for reconciliation rather than be rewritten as denial.
	if to == PhaseDenied {
		return from == PhaseRequested
	}
	if to == PhaseFailed {
		return from != PhaseFailed && from != PhaseDenied && from != PhasePresent && from != PhaseAbsent && from != PhasePlanned
	}
	allowed := map[Operation]map[Phase]Phase{
		OperationPlan: {
			PhaseRequested: PhaseAccepted,
			PhaseAccepted:  PhasePlanned,
		},
		OperationProvision: {
			PhaseRequested:      PhaseAccepted,
			PhaseAccepted:       PhaseResourcesBound,
			PhaseResourcesBound: PhasePresent,
		},
		OperationReconcile: {
			PhaseRequested:      PhaseAccepted,
			PhaseAccepted:       PhaseResourcesBound,
			PhaseResourcesBound: PhasePresent,
		},
		OperationDecommission: {
			PhaseRequested:      PhaseAccepted,
			PhaseAccepted:       PhaseDeleteAccepted,
			PhaseDeleteAccepted: PhaseAbsencePending,
			PhaseAbsencePending: PhaseAbsent,
		},
	}
	if operation == OperationObserve && from == PhaseRequested {
		return to == PhaseAccepted
	}
	if operation == OperationObserve && from == PhaseAccepted {
		return to == PhasePresent || to == PhaseAbsent
	}
	return allowed[operation][from] == to
}

func pendingSelfLoopPhase(phase Phase) bool {
	switch phase {
	case PhaseAccepted, PhaseResourcesBound, PhaseDeleteAccepted, PhaseAbsencePending:
		return true
	default:
		return false
	}
}

// ValidateTransition returns a descriptive error for an illegal ledger phase
// transition.
func ValidateTransition(operation Operation, from, to Phase) error {
	if !CanTransition(operation, from, to) {
		return fmt.Errorf("providerexecutor: invalid %s transition %s -> %s", operation, from, to)
	}
	return nil
}

func phaseAllowedForOperation(operation Operation, phase Phase) bool {
	if phase == PhaseFailed || phase == PhaseDenied || phase == PhaseRequested || phase == PhaseAccepted {
		return validOperation(operation)
	}
	switch operation {
	case OperationPlan:
		return phase == PhasePlanned
	case OperationProvision, OperationReconcile:
		return phase == PhaseResourcesBound || phase == PhasePresent
	case OperationObserve:
		return phase == PhasePresent || phase == PhaseAbsent
	case OperationDecommission:
		return phase == PhaseDeleteAccepted || phase == PhaseAbsencePending || phase == PhaseAbsent
	default:
		return false
	}
}
