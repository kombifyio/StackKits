package commands

import (
	"fmt"

	"github.com/kombifyio/stackkits/internal/actionableerror"
	"github.com/kombifyio/stackkits/internal/applicationlifecycle"
	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

type unsupportedBackupDelivery struct {
	workload, adapter string
}

func (e *unsupportedBackupDelivery) Error() string {
	return fmt.Sprintf("workload %q selected adapter %q has no admitted backup/restore lifecycle", e.workload, e.adapter)
}

func (e *unsupportedBackupDelivery) ActionableError() actionableerror.Contract {
	return actionableerror.New("stackkit_command_denied", "selected_adapter_backup_restore_unavailable", e.Error(), []string{
		"Keep the existing application data and runtime-manager custody.",
		"Use an explicitly approved transition to an adapter with admitted backup/restore, or wait for that selected adapter's complete data lifecycle.",
		"A Core-only snapshot does not prove backup coverage for this application.",
	}, false)
}

func requireNativeBackupDelivery(plan generationartifact.VerifiedPlan) error {
	resolved, err := resolvedplan.DecodeCanonicalPlan(plan.Canonical())
	if err != nil {
		return err
	}
	contracts, err := applicationlifecycle.ContractsFromResolvedPlan(resolved)
	if err != nil {
		return err
	}
	for _, contract := range contracts {
		if !applicationLifecycleStageSupportedByDelivery(contract, "backup") {
			return &unsupportedBackupDelivery{workload: contract.WorkloadRef, adapter: contract.Delivery.AdapterRef}
		}
	}
	return nil
}
