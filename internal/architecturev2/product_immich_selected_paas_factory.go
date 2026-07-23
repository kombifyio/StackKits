package architecturev2

import (
	"errors"
	"strings"

	"github.com/kombifyio/stackkits/internal/runtimeexecutorlocal"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
)

const productImmichSelectedPaaSAdapterID = "stackkits-immich-selected-paas"

type productImmichSelectedPaaSFactory struct {
	runtimeVersion          string
	runtimeAdapterRef       string
	runtimeAdapterModuleRef string
	operations              runtimeexecutorlocal.SelectedPaaSWorkloadOperations
}

// NewProductImmichSelectedPaaSRegistration binds the governed Immich workload
// to one explicitly selected PaaS adapter implementation. StackKits fixes the
// catalog selector and exact request authority; the supplied operations owner
// retains PaaS API, endpoint, credential, and lifecycle custody.
func NewProductImmichSelectedPaaSRegistration(
	runtimeVersion string,
	runtimeAdapterRef string,
	runtimeAdapterModuleRef string,
	operations runtimeexecutorlocal.SelectedPaaSWorkloadOperations,
) (ProductRuntimeOwnerRegistration, error) {
	if runtimeVersion == "" || runtimeVersion != strings.TrimSpace(runtimeVersion) ||
		runtimeAdapterRef == "" || runtimeAdapterRef != strings.TrimSpace(runtimeAdapterRef) ||
		runtimeAdapterModuleRef == "" || runtimeAdapterModuleRef != strings.TrimSpace(runtimeAdapterModuleRef) ||
		nilProductRuntimeOwnerValue(operations) {
		return ProductRuntimeOwnerRegistration{}, errors.New("Immich selected-PaaS product registration requires runtime version, exact adapter identity, and operations owner")
	}
	return ProductRuntimeOwnerRegistration{
		Selector: productImmichSelectedPaaSSelector(runtimeAdapterRef, runtimeAdapterModuleRef),
		Factory: &productImmichSelectedPaaSFactory{
			runtimeVersion: runtimeVersion, runtimeAdapterRef: runtimeAdapterRef,
			runtimeAdapterModuleRef: runtimeAdapterModuleRef, operations: operations,
		},
	}, nil
}

func (f *productImmichSelectedPaaSFactory) PrepareRuntimeOwner(request ProductRuntimeOwnerRequest) (runtimeexecutor.Executor, error) {
	if f == nil || strings.TrimSpace(f.runtimeVersion) == "" || strings.TrimSpace(f.runtimeAdapterRef) == "" ||
		strings.TrimSpace(f.runtimeAdapterModuleRef) == "" || nilProductRuntimeOwnerValue(f.operations) {
		return nil, errors.New("Immich selected-PaaS product factory is not initialized")
	}
	target := cloneProductRuntimeTarget(request.Target)
	health := cloneProductHealthTargets(request.HealthTargets)
	selector := productImmichSelectedPaaSSelector(f.runtimeAdapterRef, f.runtimeAdapterModuleRef)
	if productRuntimeOwnerSelectorForTarget(target) != selector || target.RuntimeAdapter == nil ||
		len(target.SiteRefs) != 1 || len(target.NodeRefs) != 1 || strings.TrimSpace(target.ExecutionChannelRef) == "" ||
		len(health) == 0 {
		return nil, errors.New("Immich selected-PaaS product factory requires one exact channel-bound workload, adapter, and health contract")
	}
	moduleHealthIndex := -1
	for index, requirement := range health {
		if !productHealthTargetsRuntime(requirement, target) {
			return nil, errors.New("Immich selected-PaaS product factory received Health outside its exact runtime authority")
		}
		if requirement.TargetKind == "module" {
			if moduleHealthIndex >= 0 {
				return nil, errors.New("Immich selected-PaaS product factory received more than one module Health contract")
			}
			moduleHealthIndex = index
		}
	}
	if moduleHealthIndex < 0 {
		return nil, errors.New("Immich selected-PaaS product factory requires its exact module Health contract")
	}
	identity, err := productRuntimeOwnerAdapterIdentity(productImmichSelectedPaaSAdapterID, f.runtimeVersion, target, health)
	if err != nil {
		return nil, err
	}
	authority := runtimeexecutorlocal.ImmichWorkloadAuthority{
		ProviderContractHash: target.ProviderContractHash,
		ModuleContractHash:   target.ModuleContractHash,
		UnitContractHash:     target.UnitContractHash,
		HealthContractHash:   health[moduleHealthIndex].ContractHash,
		RuntimeAdapter:       selectedPaaSRuntimeAdapterAuthority(*target.RuntimeAdapter),
	}
	return runtimeexecutorlocal.NewImmichSelectedPaaSExecutor(identity, runtimeexecutorlocal.LocalTargetBinding{
		SiteRef: target.SiteRefs[0], NodeRef: target.NodeRefs[0], ExecutionChannelRef: target.ExecutionChannelRef,
	}, authority, f.operations), nil
}

func productImmichSelectedPaaSSelector(runtimeAdapterRef, runtimeAdapterModuleRef string) ProductRuntimeOwnerSelector {
	return ProductRuntimeOwnerSelector{
		OwnerKind: "module", OwnerRef: "stackkits-immich-runtime",
		ProviderRef: "stackkits-immich", ModuleRef: "stackkits-immich-runtime", UnitRef: "immich-server",
		RuntimeKind: "container", RuntimeDelivery: "selected-paas", RuntimeEngine: "docker", WorkloadRef: "photos",
		RuntimeAdapterRef: runtimeAdapterRef, RuntimeAdapterModuleRef: runtimeAdapterModuleRef,
	}
}

func selectedPaaSRuntimeAdapterAuthority(binding runtimeexecutor.RuntimeAdapterBinding) runtimeexecutorlocal.SelectedPaaSRuntimeAdapterAuthority {
	authority := runtimeexecutorlocal.SelectedPaaSRuntimeAdapterAuthority{
		ID: binding.ID, ProviderRef: binding.ProviderRef, ProviderVersion: binding.ProviderVersion,
		ProviderContractHash: binding.ProviderContractHash, ModuleRef: binding.ModuleRef,
		ModuleVersion: binding.ModuleVersion, ModuleContractHash: binding.ModuleContractHash,
		Agents: make([]runtimeexecutorlocal.SelectedPaaSRuntimeAdapterAgentAuthority, len(binding.Agents)),
	}
	for index, agent := range binding.Agents {
		authority.Agents[index] = runtimeexecutorlocal.SelectedPaaSRuntimeAdapterAgentAuthority{
			ID: agent.ID, ModuleRef: agent.ModuleRef, ModuleVersion: agent.ModuleVersion, ModuleContractHash: agent.ModuleContractHash,
		}
	}
	return authority
}

var _ ProductRuntimeOwnerFactory = (*productImmichSelectedPaaSFactory)(nil)
