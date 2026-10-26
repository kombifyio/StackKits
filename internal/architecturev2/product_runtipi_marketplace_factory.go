package architecturev2

import (
	"errors"
	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
	"strings"
)

type productRuntipiMarketplaceFactory struct {
	version    string
	operations nativehost.RuntipiMarketplaceOperations
}

// PreparedRuntipiRuntimeOwners assembles both owners for an eventual qualified
// Marketplace plus Files slice. The caller must explicitly install the returned
// registrations; the production registry never invokes this preparation hook.
func PreparedRuntipiRuntimeOwners(version, workspace string) ([]ProductRuntimeOwnerRegistration, error) {
	bootstrap, err := nativehost.NewOSRuntipiMarketplaceOperations(workspace)
	if err != nil {
		return nil, err
	}
	marketplace, err := NewProductRuntipiMarketplaceRegistration(version, bootstrap)
	if err != nil {
		return nil, err
	}
	workloads, err := nativehost.NewOSRuntipiWorkloadOperations(workspace)
	if err != nil {
		return nil, err
	}
	files, err := NewProductCloudreveSelectedPaaSRegistration(version, "runtipi", "stackkits-runtipi-runtime", workloads)
	if err != nil {
		return nil, err
	}
	return []ProductRuntimeOwnerRegistration{marketplace, files}, nil
}

// NewProductRuntipiMarketplaceRegistration builds the owner of the explicit, non-default Runtipi Marketplace alternative. Selection never defaults to it; support still needs a current-pin receipt.
func NewProductRuntipiMarketplaceRegistration(version string, operations nativehost.RuntipiMarketplaceOperations) (ProductRuntimeOwnerRegistration, error) {
	if version == "" || version != strings.TrimSpace(version) || nilProductRuntimeOwnerValue(operations) {
		return ProductRuntimeOwnerRegistration{}, errors.New("Marketplace requires its installed node owner and runtime version")
	}
	return ProductRuntimeOwnerRegistration{Selector: productRuntipiMarketplaceSelector(), Factory: &productRuntipiMarketplaceFactory{version, operations}}, nil
}
func (f *productRuntipiMarketplaceFactory) PrepareRuntimeOwner(r ProductRuntimeOwnerRequest) (runtimeexecutor.Executor, error) {
	if f == nil || f.version == "" || nilProductRuntimeOwnerValue(f.operations) {
		return nil, errors.New("Marketplace factory is not initialized")
	}
	target, health := cloneProductRuntimeTarget(r.Target), cloneProductHealthTargets(r.HealthTargets)
	if productRuntimeOwnerSelectorForTarget(target) != productRuntipiMarketplaceSelector() || len(target.SiteRefs) != 1 || len(target.NodeRefs) != 1 || target.ExecutionChannelRef == "" || len(health) == 0 {
		return nil, errors.New("Marketplace requires its exact selected application node")
	}
	moduleHealthIndex := -1
	for index, requirement := range health {
		if !productHealthTargetsRuntime(requirement, target) {
			return nil, errors.New("Marketplace received health outside its node authority")
		}
		if requirement.TargetKind == "module" {
			if moduleHealthIndex >= 0 {
				return nil, errors.New("Marketplace received multiple module health contracts")
			}
			moduleHealthIndex = index
		}
	}
	if moduleHealthIndex < 0 {
		return nil, errors.New("Marketplace module health contract is missing")
	}
	identity, err := productRuntimeOwnerAdapterIdentity("stackkits-marketplace-runtipi-local", f.version, target, health)
	if err != nil {
		return nil, err
	}
	return nativehost.NewRuntipiMarketplaceExecutor(identity, nativehost.LocalTargetBinding{SiteRef: target.SiteRefs[0], NodeRef: target.NodeRefs[0], ExecutionChannelRef: target.ExecutionChannelRef}, health[moduleHealthIndex].ContractHash, f.operations), nil
}
func productRuntipiMarketplaceSelector() ProductRuntimeOwnerSelector {
	return ProductRuntimeOwnerSelector{OwnerKind: "module", OwnerRef: architecturev2renderer.MarketplaceRuntipiModuleRef, ModuleRef: architecturev2renderer.MarketplaceRuntipiModuleRef, UnitRef: architecturev2renderer.MarketplaceRuntipiUnitRef, ProviderRef: "stackkits-marketplace", RuntimeKind: "host", RuntimeDelivery: "stackkit", WorkloadRef: "marketplace"}
}
