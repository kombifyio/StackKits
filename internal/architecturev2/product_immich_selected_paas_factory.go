package architecturev2

import (
	"errors"
	"fmt"
	"strings"

	"github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
)

const (
	productImmichSelectedPaaSAdapterID     = "stackkits-immich-selected-paas"
	productImmichLiteSelectedPaaSAdapterID = "stackkits-immich-lite-selected-paas"
)

// productImmichVariant names one catalog Photos Immich alternative. Each
// variant keeps its own exact selector and executor contract, so neither
// owner can prepare the other's generated workload.
type productImmichVariant struct {
	name, adapterID, providerRef, moduleRef string
	executor                                func(runtimeexecutor.ExecutorIdentity, nativehost.LocalTargetBinding, nativehost.ImmichWorkloadAuthority, nativehost.SelectedPaaSWorkloadOperations) *nativehost.ImmichSelectedPaaSExecutor
}

var (
	productImmichStandardVariant = productImmichVariant{
		name: "Immich", adapterID: productImmichSelectedPaaSAdapterID,
		providerRef: "stackkits-immich", moduleRef: "stackkits-immich-runtime",
		executor: nativehost.NewImmichSelectedPaaSExecutor,
	}
	productImmichLiteVariant = productImmichVariant{
		name: "Immich Lite", adapterID: productImmichLiteSelectedPaaSAdapterID,
		providerRef: "stackkits-immich-lite", moduleRef: "stackkits-immich-lite-runtime",
		executor: nativehost.NewImmichLiteSelectedPaaSExecutor,
	}
)

type productImmichSelectedPaaSFactory struct {
	variant                 productImmichVariant
	runtimeVersion          string
	runtimeAdapterRef       string
	runtimeAdapterModuleRef string
	operations              nativehost.SelectedPaaSWorkloadOperations
}

// NewProductImmichSelectedPaaSRegistration binds the governed Immich workload
// to one explicitly selected PaaS adapter implementation. StackKits fixes the
// catalog selector and exact request authority; the supplied operations owner
// retains PaaS API, endpoint, credential, and lifecycle custody.
func NewProductImmichSelectedPaaSRegistration(
	runtimeVersion string,
	runtimeAdapterRef string,
	runtimeAdapterModuleRef string,
	operations nativehost.SelectedPaaSWorkloadOperations,
) (ProductRuntimeOwnerRegistration, error) {
	return newProductImmichVariantSelectedPaaSRegistration(productImmichStandardVariant, runtimeVersion, runtimeAdapterRef, runtimeAdapterModuleRef, operations)
}

// NewProductImmichLiteSelectedPaaSRegistration binds the governed Immich Lite
// Photos alternative (machine learning omitted) to one explicitly selected
// PaaS adapter implementation with the same exact-contract custody as the
// standard Immich owner.
func NewProductImmichLiteSelectedPaaSRegistration(
	runtimeVersion string,
	runtimeAdapterRef string,
	runtimeAdapterModuleRef string,
	operations nativehost.SelectedPaaSWorkloadOperations,
) (ProductRuntimeOwnerRegistration, error) {
	return newProductImmichVariantSelectedPaaSRegistration(productImmichLiteVariant, runtimeVersion, runtimeAdapterRef, runtimeAdapterModuleRef, operations)
}

func newProductImmichVariantSelectedPaaSRegistration(
	variant productImmichVariant,
	runtimeVersion string,
	runtimeAdapterRef string,
	runtimeAdapterModuleRef string,
	operations nativehost.SelectedPaaSWorkloadOperations,
) (ProductRuntimeOwnerRegistration, error) {
	if runtimeVersion == "" || runtimeVersion != strings.TrimSpace(runtimeVersion) ||
		runtimeAdapterRef == "" || runtimeAdapterRef != strings.TrimSpace(runtimeAdapterRef) ||
		runtimeAdapterModuleRef == "" || runtimeAdapterModuleRef != strings.TrimSpace(runtimeAdapterModuleRef) ||
		nilProductRuntimeOwnerValue(operations) {
		return ProductRuntimeOwnerRegistration{}, fmt.Errorf("%s selected-PaaS product registration requires runtime version, exact adapter identity, and operations owner", variant.name)
	}
	return ProductRuntimeOwnerRegistration{
		Selector: productImmichVariantSelectedPaaSSelector(variant, runtimeAdapterRef, runtimeAdapterModuleRef),
		Factory: &productImmichSelectedPaaSFactory{
			variant: variant, runtimeVersion: runtimeVersion, runtimeAdapterRef: runtimeAdapterRef,
			runtimeAdapterModuleRef: runtimeAdapterModuleRef, operations: operations,
		},
	}, nil
}

func (f *productImmichSelectedPaaSFactory) PrepareRuntimeOwner(request ProductRuntimeOwnerRequest) (runtimeexecutor.Executor, error) {
	if f == nil || f.variant.executor == nil || strings.TrimSpace(f.runtimeVersion) == "" || strings.TrimSpace(f.runtimeAdapterRef) == "" ||
		strings.TrimSpace(f.runtimeAdapterModuleRef) == "" || nilProductRuntimeOwnerValue(f.operations) {
		return nil, errors.New("Immich selected-PaaS product factory is not initialized")
	}
	target := cloneProductRuntimeTarget(request.Target)
	health := cloneProductHealthTargets(request.HealthTargets)
	selector := productImmichVariantSelectedPaaSSelector(f.variant, f.runtimeAdapterRef, f.runtimeAdapterModuleRef)
	if productRuntimeOwnerSelectorForTarget(target) != selector || target.RuntimeAdapter == nil ||
		len(target.SiteRefs) != 1 || len(target.NodeRefs) != 1 || strings.TrimSpace(target.ExecutionChannelRef) == "" ||
		len(health) == 0 {
		return nil, fmt.Errorf("%s selected-PaaS product factory requires one exact channel-bound workload, adapter, and health contract", f.variant.name)
	}
	moduleHealthIndex := -1
	for index, requirement := range health {
		if !productHealthTargetsRuntime(requirement, target) {
			return nil, fmt.Errorf("%s selected-PaaS product factory received Health outside its exact runtime authority", f.variant.name)
		}
		if requirement.TargetKind == "module" {
			if moduleHealthIndex >= 0 {
				return nil, fmt.Errorf("%s selected-PaaS product factory received more than one module Health contract", f.variant.name)
			}
			moduleHealthIndex = index
		}
	}
	if moduleHealthIndex < 0 {
		return nil, fmt.Errorf("%s selected-PaaS product factory requires its exact module Health contract", f.variant.name)
	}
	identity, err := productRuntimeOwnerAdapterIdentity(f.variant.adapterID, f.runtimeVersion, target, health)
	if err != nil {
		return nil, err
	}
	authority := nativehost.ImmichWorkloadAuthority{
		ProviderContractHash: target.ProviderContractHash,
		ModuleContractHash:   target.ModuleContractHash,
		UnitContractHash:     target.UnitContractHash,
		HealthContractHash:   health[moduleHealthIndex].ContractHash,
		RuntimeAdapter:       selectedPaaSRuntimeAdapterAuthority(*target.RuntimeAdapter),
	}
	return f.variant.executor(identity, nativehost.LocalTargetBinding{
		SiteRef: target.SiteRefs[0], NodeRef: target.NodeRefs[0], ExecutionChannelRef: target.ExecutionChannelRef,
	}, authority, f.operations), nil
}

func productImmichSelectedPaaSSelector(runtimeAdapterRef, runtimeAdapterModuleRef string) ProductRuntimeOwnerSelector {
	return productImmichVariantSelectedPaaSSelector(productImmichStandardVariant, runtimeAdapterRef, runtimeAdapterModuleRef)
}

func productImmichVariantSelectedPaaSSelector(variant productImmichVariant, runtimeAdapterRef, runtimeAdapterModuleRef string) ProductRuntimeOwnerSelector {
	return ProductRuntimeOwnerSelector{
		OwnerKind: "module", OwnerRef: variant.moduleRef,
		ProviderRef: variant.providerRef, ModuleRef: variant.moduleRef, UnitRef: "immich-server",
		RuntimeKind: "container", RuntimeDelivery: sharedApplicationAdapterRuntimeDelivery, RuntimeEngine: "docker", WorkloadRef: "photos",
		RuntimeAdapterRef: runtimeAdapterRef, RuntimeAdapterModuleRef: runtimeAdapterModuleRef,
	}
}

func selectedPaaSRuntimeAdapterAuthority(binding runtimeexecutor.RuntimeAdapterBinding) nativehost.SelectedPaaSRuntimeAdapterAuthority {
	authority := nativehost.SelectedPaaSRuntimeAdapterAuthority{
		ID: binding.ID, ProviderRef: binding.ProviderRef, ProviderVersion: binding.ProviderVersion,
		ProviderContractHash: binding.ProviderContractHash, ModuleRef: binding.ModuleRef,
		ModuleVersion: binding.ModuleVersion, ModuleContractHash: binding.ModuleContractHash,
		Agents: make([]nativehost.SelectedPaaSRuntimeAdapterAgentAuthority, len(binding.Agents)),
	}
	for index, agent := range binding.Agents {
		authority.Agents[index] = nativehost.SelectedPaaSRuntimeAdapterAgentAuthority{
			ID: agent.ID, ModuleRef: agent.ModuleRef, ModuleVersion: agent.ModuleVersion, ModuleContractHash: agent.ModuleContractHash,
		}
	}
	return authority
}

var _ ProductRuntimeOwnerFactory = (*productImmichSelectedPaaSFactory)(nil)
