package commands

import (
	"errors"
	"slices"
	"time"

	"github.com/kombifyio/stackkits/internal/applicationlifecycle"
	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

// An interrupted removal reuses its current lifecycle operation under exactly
// the same plan authority. Fresh owner authorization still precedes each
// dispatch, and the runtime rechecks original removal custody.
func beginOrResumeArchitectureV2Removal(workspace string, plan generationartifact.VerifiedPlan, workload string, now time.Time) ([]architectureV2ApplicationLifecycleRun, bool, error) {
	resolved, err := resolvedplan.DecodeCanonicalPlan(plan.Canonical())
	if err != nil {
		return nil, false, err
	}
	contract, err := applicationlifecycle.ContractFromResolvedPlan(resolved, workload)
	if err != nil {
		return nil, false, err
	}
	store := applicationlifecycle.Store{Workspace: workspace}
	state, err := store.Load(contract)
	if err != nil {
		return nil, false, err
	}
	current := applicationLifecycleOperation(state, state.CurrentOperation)
	if current != nil && (current.Status == applicationlifecycle.StatusRunning || current.Status == applicationlifecycle.StatusRecoveryRequired) {
		expected := applicationlifecycle.Authority{
			PlanHash: contract.PlanHash, LifecycleContractHash: contract.ContractHash,
			LifecycleVersion: contract.Version, PackageRef: contract.PackageRef,
		}
		if current.Stage != "remove" || current.OperationRef != "stackkit.remove" || current.Authority != expected ||
			!slices.Contains(contract.Stages["remove"].Operations, "stackkit.remove") {
			return nil, false, errors.New("unfinished lifecycle must be recovered under its original operation and plan authority")
		}
		return []architectureV2ApplicationLifecycleRun{{Contract: contract, OperationID: current.ID}}, true, nil
	}
	runs, err := beginArchitectureV2ApplicationLifecycles(workspace, plan, "remove", "stackkit.remove", workload, now)
	return runs, false, err
}
