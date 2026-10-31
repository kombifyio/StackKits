package architecturev2

import (
	"errors"
	"github.com/kombifyio/stackkits/contracts/runtimeexecutor"
	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"
	"strings"
)

type productMarketplaceFactory struct {
	version    string
	operations nativehost.MarketplaceOperations
}

func NewProductMarketplaceRegistration(version string, operations nativehost.MarketplaceOperations) (ProductRuntimeOwnerRegistration, error) {
	if version == "" || version != strings.TrimSpace(version) || nilProductRuntimeOwnerValue(operations) {
		return ProductRuntimeOwnerRegistration{}, errors.New("Marketplace requires its installed node owner and runtime version")
	}
	return ProductRuntimeOwnerRegistration{Selector: productMarketplaceSelector(), Factory: &productMarketplaceFactory{version, operations}}, nil
}
func (f *productMarketplaceFactory) PrepareRuntimeOwner(r ProductRuntimeOwnerRequest) (runtimeexecutor.Executor, error) {
	if f == nil || f.version == "" || nilProductRuntimeOwnerValue(f.operations) {
		return nil, errors.New("Marketplace factory is not initialized")
	}
	target, health := cloneProductRuntimeTarget(r.Target), cloneProductHealthTargets(r.HealthTargets)
	if productRuntimeOwnerSelectorForTarget(target) != productMarketplaceSelector() || len(target.SiteRefs) != 1 || len(target.NodeRefs) != 1 || target.ExecutionChannelRef == "" || len(health) == 0 {
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
	identity, err := productRuntimeOwnerAdapterIdentity("stackkits-marketplace-local", f.version, target, health)
	if err != nil {
		return nil, err
	}
	return nativehost.NewMarketplaceExecutor(identity, nativehost.LocalTargetBinding{SiteRef: target.SiteRefs[0], NodeRef: target.NodeRefs[0], ExecutionChannelRef: target.ExecutionChannelRef}, health[moduleHealthIndex].ContractHash, f.operations), nil
}
func productMarketplaceSelector() ProductRuntimeOwnerSelector {
	return ProductRuntimeOwnerSelector{OwnerKind: "module", OwnerRef: architecturev2renderer.MarketplaceCoolifyModuleRef, ModuleRef: architecturev2renderer.MarketplaceCoolifyModuleRef, UnitRef: architecturev2renderer.MarketplaceCoolifyUnitRef, ProviderRef: "stackkits-marketplace", RuntimeKind: "host", RuntimeDelivery: "stackkit", WorkloadRef: "marketplace"}
}
