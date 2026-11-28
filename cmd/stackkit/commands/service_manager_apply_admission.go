package commands

import (
	"fmt"
	"slices"

	"github.com/kombifyio/stackkits/internal/deploymentcontract"
	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/localevidence"
)

// Reuse node custody before any Apply runtime effects. The native workload
// executors retain the same check at their own execution boundary.
func requireAppliedWorkloadManagers(workspace string, plan generationartifact.VerifiedPlan, binding localevidence.LocalBinding) error {
	for _, target := range plan.ApplyRequirements().RuntimeInstances {
		if target.RuntimeAdapter == nil {
			continue
		}
		manager := target.RuntimeAdapter.ID
		if target.WorkloadRef == "" || (manager != "standalone-compose" && manager != "komodo" && manager != "coolify" && manager != "dokploy") ||
			!slices.Contains(target.SiteRefs, binding.SiteRef) || !slices.Contains(target.NodeRefs, binding.NodeRef) {
			continue
		}
		project := "stackkit-" + target.WorkloadRef + "-" + binding.NodeRef
		present, err := deploymentcontract.HasOwnerHostDeployment(workspace, project)
		if err != nil {
			return err
		}
		if !present {
			continue
		}
		state, err := deploymentcontract.LoadOwnerHostDeployment(workspace, project)
		if err != nil {
			return err
		}
		if state.WorkloadRef != target.WorkloadRef || state.InstanceRef != target.InstanceRef || state.SiteRef != binding.SiteRef || state.NodeRef != binding.NodeRef {
			return fmt.Errorf("Apply refuses workload custody for another instance or node")
		}
		if err := deploymentcontract.RequireHostDeploymentManager(state, manager); err != nil {
			return fmt.Errorf("Apply refuses stale workload manager for %s: %w", target.WorkloadRef, err)
		}
	}
	return nil
}
