package commands

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/kombifyio/stackkits/internal/applicationlifecycle"
	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
)

// requireArchitectureV2RemovalCapability admits removal from the exact
// CUE-verified adapter module. Adapter names alone do not grant an operation.
func requireArchitectureV2RemovalCapability(plan generationartifact.VerifiedPlan, target runtimeexecutor.RuntimeTarget) error {
	if architectureV2MarketplaceRemovalTarget(target) {
		resolved, err := resolvedplan.DecodeCanonicalPlan(plan.Canonical())
		if err != nil {
			return err
		}
		contract, err := applicationlifecycle.ContractFromResolvedPlan(resolved, target.WorkloadRef)
		if err != nil {
			return err
		}
		stage := contract.Stages["remove"]
		if contract.Delivery.Kind != "stackkit" || !stage.Mutation || !stage.Destructive || !stage.OwnerApproval || !slices.Contains(stage.Operations, "stackkit.remove") {
			return errors.New("Marketplace removal requires its verified Owner-approved installation lifecycle")
		}
		return nil
	}
	adapter := target.RuntimeAdapter
	if adapter == nil || adapter.ID == "" || adapter.ModuleRef == "" || adapter.ModuleContractHash == "" {
		return errors.New("workload removal requires an exact applied adapter binding")
	}
	var document struct {
		Modules []struct {
			ID             string `json:"id"`
			ContractHash   string `json:"contractHash"`
			RuntimeAdapter *struct {
				ID         string   `json:"id"`
				Operations []string `json:"operations"`
			} `json:"runtimeAdapter"`
		} `json:"modules"`
	}
	if err := json.Unmarshal(plan.Canonical(), &document); err != nil {
		return fmt.Errorf("read verified removal capability: %w", err)
	}
	for _, module := range document.Modules {
		if module.ID != adapter.ModuleRef {
			continue
		}
		if module.ContractHash != adapter.ModuleContractHash || module.RuntimeAdapter == nil || module.RuntimeAdapter.ID != adapter.ID {
			return errors.New("workload removal adapter differs from the verified plan")
		}
		if !slices.Contains(module.RuntimeAdapter.Operations, "remove") {
			return fmt.Errorf("applied adapter %q does not declare workload removal; update through the governed kit lifecycle before removing this workload", adapter.ID)
		}
		return nil
	}
	return errors.New("workload removal adapter is absent from the verified plan")
}

func architectureV2MarketplaceRemovalTarget(target runtimeexecutor.RuntimeTarget) bool {
	module := architecturev2renderer.MarketplaceCoolifyModuleRef
	return target.OwnerKind == "module" && target.OwnerRef == module && target.ModuleRef == module &&
		target.UnitRef == architecturev2renderer.MarketplaceCoolifyUnitRef && target.ProviderRef == "stackkits-marketplace" &&
		target.WorkloadRef == "marketplace" && target.RuntimeKind == "host" && target.RuntimeDelivery == "stackkit" &&
		target.RuntimeEngine == "" && target.RuntimeAdapter == nil && len(target.DaemonBindings) == 0 &&
		target.UnitContractHash == architecturev2renderer.MarketplaceCoolifyRendererContract().ContractHash
}
