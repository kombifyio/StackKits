package commands

import (
	"fmt"
	"slices"

	"github.com/kombifyio/stackkits/internal/actionableerror"
	"github.com/kombifyio/stackkits/internal/applicationlifecycle"
	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
	"github.com/kombifyio/stackkits/internal/restoreactivation"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/fallback/platformdeploy"
)

type unsupportedBackupDelivery struct {
	workload, adapter string
	planAdapter       string
}

func (e *unsupportedBackupDelivery) Error() string {
	if e.planAdapter != "" {
		return fmt.Sprintf("workload %q current manager %q differs from plan adapter %q; backup/restore is not admitted for that manager", e.workload, e.adapter, e.planAdapter)
	}
	return fmt.Sprintf("workload %q selected adapter %q has no admitted backup/restore lifecycle", e.workload, e.adapter)
}

func (e *unsupportedBackupDelivery) ActionableError() actionableerror.Contract {
	contract, _ := actionableerror.Diagnostic(actionableerror.ReasonSelectedAdapterBackupRestoreUnavailable)
	contract.Message = e.Error()
	return contract
}

func requireNativeBackupDelivery(workspace string, plan generationartifact.VerifiedPlan, binding localevidence.LocalBinding) error {
	if err := requireNativeBackupManagerDelivery(workspace, plan.ApplyRequirements().RuntimeInstances, binding); err != nil {
		return err
	}
	resolved, err := resolvedplan.DecodeCanonicalPlan(plan.Canonical())
	if err != nil {
		return err
	}
	contracts, err := applicationlifecycle.ContractsFromResolvedPlan(resolved)
	if err != nil {
		return err
	}
	for _, contract := range contracts {
		if applicationLifecycleStageSupportedByDelivery(contract, "backup") {
			continue
		}
		// A standalone Compose workload without backup-enabled storage holds
		// nothing for the backup, as restore already skips it (Remote, ntfy).
		stateless, err := restoreactivation.StandaloneComposeWorkloadHoldsNoBackupData(plan.Canonical(), contract.WorkloadRef)
		if err != nil {
			return err
		}
		if !stateless {
			return &unsupportedBackupDelivery{workload: contract.WorkloadRef, adapter: contract.Delivery.AdapterRef}
		}
	}
	return nil
}

// Denial only: a source plan's backup capability does not follow a manager
// transfer. Successful admission still requires the current signed Apply and
// exact backup source graph through inspectNativeV2BackupAuthority.
func requireNativeBackupManagerDelivery(workspace string, targets []generationartifact.ApplyRuntimeRequirement, binding localevidence.LocalBinding) error {
	for _, target := range targets {
		if target.RuntimeAdapter == nil || target.WorkloadRef == "" ||
			!slices.Contains(target.SiteRefs, binding.SiteRef) || !slices.Contains(target.NodeRefs, binding.NodeRef) {
			continue
		}
		project := "stackkit-" + target.WorkloadRef + "-" + binding.NodeRef
		present, err := platformdeploy.HasOwnerHostDeployment(workspace, project)
		if err != nil {
			return err
		}
		if !present {
			continue
		}
		state, err := platformdeploy.LoadOwnerHostDeployment(workspace, project)
		if err != nil {
			return err
		}
		if state.WorkloadRef != target.WorkloadRef || state.InstanceRef != target.InstanceRef || state.SiteRef != binding.SiteRef || state.NodeRef != binding.NodeRef {
			return fmt.Errorf("backup refuses workload custody for another instance or node")
		}
		manager := platformdeploy.HostDeploymentManager(state)
		if err := platformdeploy.RequireHostDeploymentManager(state, manager); err != nil {
			return &pendingBackupManagerDelivery{workload: target.WorkloadRef, operation: state.Handover.ID}
		}
		if manager != target.RuntimeAdapter.ID {
			return &unsupportedBackupDelivery{workload: target.WorkloadRef, adapter: manager, planAdapter: target.RuntimeAdapter.ID}
		}
	}
	return nil
}

type pendingBackupManagerDelivery struct{ workload, operation string }

func (e *pendingBackupManagerDelivery) Error() string {
	return fmt.Sprintf("workload %q backup/restore is denied while manager handover %q is pending", e.workload, e.operation)
}

func (e *pendingBackupManagerDelivery) ActionableError() actionableerror.Contract {
	return actionableerror.New("stackkit_command_denied", "workload_manager_handover_pending", e.Error(), []string{
		"Preserve the existing application data and both manager claims.",
		"Inspect and resume only the exact pending manager handover, then recheck backup coverage.",
		"A Core-only snapshot does not prove backup coverage for this application.",
	}, true)
}
