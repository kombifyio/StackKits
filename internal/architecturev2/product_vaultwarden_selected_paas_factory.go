package architecturev2

import (
	"errors"
	"strings"

	"github.com/kombifyio/stackkits/internal/runtimeexecutorlocal"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
)

const productVaultwardenSelectedPaaSAdapterID = "stackkits-vaultwarden-selected-paas"

type productVaultwardenSelectedPaaSFactory struct {
	runtimeVersion          string
	runtimeAdapterRef       string
	runtimeAdapterModuleRef string
	operations              runtimeexecutorlocal.SelectedPaaSWorkloadOperations
}

func NewProductVaultwardenSelectedPaaSRegistration(
	runtimeVersion string,
	runtimeAdapterRef string,
	runtimeAdapterModuleRef string,
	operations runtimeexecutorlocal.SelectedPaaSWorkloadOperations,
) (ProductRuntimeOwnerRegistration, error) {
	if runtimeVersion == "" || runtimeVersion != strings.TrimSpace(runtimeVersion) ||
		runtimeAdapterRef == "" || runtimeAdapterRef != strings.TrimSpace(runtimeAdapterRef) ||
		runtimeAdapterModuleRef == "" || runtimeAdapterModuleRef != strings.TrimSpace(runtimeAdapterModuleRef) ||
		nilProductRuntimeOwnerValue(operations) {
		return ProductRuntimeOwnerRegistration{}, errors.New("Vaultwarden selected-PaaS product registration requires runtime version, exact adapter identity, and operations owner")
	}
	return ProductRuntimeOwnerRegistration{
		Selector: productVaultwardenSelectedPaaSSelector(runtimeAdapterRef, runtimeAdapterModuleRef),
		Factory: &productVaultwardenSelectedPaaSFactory{
			runtimeVersion: runtimeVersion, runtimeAdapterRef: runtimeAdapterRef,
			runtimeAdapterModuleRef: runtimeAdapterModuleRef, operations: operations,
		},
	}, nil
}

func (f *productVaultwardenSelectedPaaSFactory) PrepareRuntimeOwner(request ProductRuntimeOwnerRequest) (runtimeexecutor.Executor, error) {
	if f == nil || strings.TrimSpace(f.runtimeVersion) == "" || strings.TrimSpace(f.runtimeAdapterRef) == "" ||
		strings.TrimSpace(f.runtimeAdapterModuleRef) == "" || nilProductRuntimeOwnerValue(f.operations) {
		return nil, errors.New("Vaultwarden selected-PaaS product factory is not initialized")
	}
	target := cloneProductRuntimeTarget(request.Target)
	health := cloneProductHealthTargets(request.HealthTargets)
	selector := productVaultwardenSelectedPaaSSelector(f.runtimeAdapterRef, f.runtimeAdapterModuleRef)
	if productRuntimeOwnerSelectorForTarget(target) != selector || target.RuntimeAdapter == nil ||
		len(target.SiteRefs) != 1 || len(target.NodeRefs) != 1 ||
		strings.TrimSpace(target.ExecutionChannelRef) == "" || len(health) == 0 {
		return nil, errors.New("Vaultwarden selected-PaaS product factory requires one exact channel-bound workload, adapter, and health contract")
	}
	moduleHealthIndex := -1
	for index, requirement := range health {
		if !productHealthTargetsRuntime(requirement, target) {
			return nil, errors.New("Vaultwarden selected-PaaS product factory received Health outside its exact runtime authority")
		}
		if requirement.TargetKind == "module" {
			if moduleHealthIndex >= 0 {
				return nil, errors.New("Vaultwarden selected-PaaS product factory received more than one module Health contract")
			}
			moduleHealthIndex = index
		}
	}
	if moduleHealthIndex < 0 {
		return nil, errors.New("Vaultwarden selected-PaaS product factory requires its exact module Health contract")
	}
	identity, err := productRuntimeOwnerAdapterIdentity(productVaultwardenSelectedPaaSAdapterID, f.runtimeVersion, target, health)
	if err != nil {
		return nil, err
	}
	authority := runtimeexecutorlocal.VaultwardenWorkloadAuthority{
		ProviderContractHash: target.ProviderContractHash,
		ModuleContractHash:   target.ModuleContractHash,
		UnitContractHash:     target.UnitContractHash,
		HealthContractHash:   health[moduleHealthIndex].ContractHash,
		RuntimeAdapter:       selectedPaaSRuntimeAdapterAuthority(*target.RuntimeAdapter),
	}
	return runtimeexecutorlocal.NewVaultwardenSelectedPaaSExecutor(identity, runtimeexecutorlocal.LocalTargetBinding{
		SiteRef: target.SiteRefs[0], NodeRef: target.NodeRefs[0], ExecutionChannelRef: target.ExecutionChannelRef,
	}, authority, f.operations), nil
}

func productVaultwardenSelectedPaaSSelector(runtimeAdapterRef, runtimeAdapterModuleRef string) ProductRuntimeOwnerSelector {
	return ProductRuntimeOwnerSelector{
		OwnerKind: "module", OwnerRef: "stackkits-vaultwarden-runtime",
		ProviderRef: "stackkits-vaultwarden", ModuleRef: "stackkits-vaultwarden-runtime", UnitRef: "vaultwarden",
		RuntimeKind: "container", RuntimeDelivery: "selected-paas", RuntimeEngine: "docker", WorkloadRef: "vault",
		RuntimeAdapterRef: runtimeAdapterRef, RuntimeAdapterModuleRef: runtimeAdapterModuleRef,
	}
}

var _ ProductRuntimeOwnerFactory = (*productVaultwardenSelectedPaaSFactory)(nil)
