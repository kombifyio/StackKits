package architecturev2

import (
	"errors"
	"strings"

	"github.com/kombifyio/stackkits/internal/runtimeexecutorlocal"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
)

const productCloudreveSelectedPaaSAdapterID = "stackkits-cloudreve-selected-paas"

type productCloudreveSelectedPaaSFactory struct {
	runtimeVersion          string
	runtimeAdapterRef       string
	runtimeAdapterModuleRef string
	operations              runtimeexecutorlocal.SelectedPaaSWorkloadOperations
}

func NewProductCloudreveSelectedPaaSRegistration(
	runtimeVersion string,
	runtimeAdapterRef string,
	runtimeAdapterModuleRef string,
	operations runtimeexecutorlocal.SelectedPaaSWorkloadOperations,
) (ProductRuntimeOwnerRegistration, error) {
	if runtimeVersion == "" || runtimeVersion != strings.TrimSpace(runtimeVersion) ||
		runtimeAdapterRef == "" || runtimeAdapterRef != strings.TrimSpace(runtimeAdapterRef) ||
		runtimeAdapterModuleRef == "" || runtimeAdapterModuleRef != strings.TrimSpace(runtimeAdapterModuleRef) ||
		nilProductRuntimeOwnerValue(operations) {
		return ProductRuntimeOwnerRegistration{}, errors.New("Cloudreve selected-PaaS product registration requires runtime version, exact adapter identity, and operations owner")
	}
	return ProductRuntimeOwnerRegistration{
		Selector: productCloudreveSelectedPaaSSelector(runtimeAdapterRef, runtimeAdapterModuleRef),
		Factory: &productCloudreveSelectedPaaSFactory{
			runtimeVersion: runtimeVersion, runtimeAdapterRef: runtimeAdapterRef,
			runtimeAdapterModuleRef: runtimeAdapterModuleRef, operations: operations,
		},
	}, nil
}

func (f *productCloudreveSelectedPaaSFactory) PrepareRuntimeOwner(request ProductRuntimeOwnerRequest) (runtimeexecutor.Executor, error) {
	if f == nil || strings.TrimSpace(f.runtimeVersion) == "" || strings.TrimSpace(f.runtimeAdapterRef) == "" ||
		strings.TrimSpace(f.runtimeAdapterModuleRef) == "" || nilProductRuntimeOwnerValue(f.operations) {
		return nil, errors.New("Cloudreve selected-PaaS product factory is not initialized")
	}
	target := cloneProductRuntimeTarget(request.Target)
	health := cloneProductHealthTargets(request.HealthTargets)
	selector := productCloudreveSelectedPaaSSelector(f.runtimeAdapterRef, f.runtimeAdapterModuleRef)
	if productRuntimeOwnerSelectorForTarget(target) != selector || target.RuntimeAdapter == nil ||
		len(target.SiteRefs) != 1 || len(target.NodeRefs) != 1 ||
		strings.TrimSpace(target.ExecutionChannelRef) == "" || len(health) == 0 {
		return nil, errors.New("Cloudreve selected-PaaS product factory requires one exact channel-bound workload, adapter, and health contract")
	}
	moduleHealthIndex := -1
	for index, requirement := range health {
		if !productHealthTargetsRuntime(requirement, target) {
			return nil, errors.New("Cloudreve selected-PaaS product factory received Health outside its exact runtime authority")
		}
		if requirement.TargetKind == "module" {
			if moduleHealthIndex >= 0 {
				return nil, errors.New("Cloudreve selected-PaaS product factory received more than one module Health contract")
			}
			moduleHealthIndex = index
		}
	}
	if moduleHealthIndex < 0 {
		return nil, errors.New("Cloudreve selected-PaaS product factory requires its exact module Health contract")
	}
	identity, err := productRuntimeOwnerAdapterIdentity(productCloudreveSelectedPaaSAdapterID, f.runtimeVersion, target, health)
	if err != nil {
		return nil, err
	}
	authority := runtimeexecutorlocal.CloudreveWorkloadAuthority{
		ProviderContractHash: target.ProviderContractHash,
		ModuleContractHash:   target.ModuleContractHash,
		UnitContractHash:     target.UnitContractHash,
		HealthContractHash:   health[moduleHealthIndex].ContractHash,
		RuntimeAdapter:       selectedPaaSRuntimeAdapterAuthority(*target.RuntimeAdapter),
	}
	return runtimeexecutorlocal.NewCloudreveSelectedPaaSExecutor(identity, runtimeexecutorlocal.LocalTargetBinding{
		SiteRef: target.SiteRefs[0], NodeRef: target.NodeRefs[0], ExecutionChannelRef: target.ExecutionChannelRef,
	}, authority, f.operations), nil
}

func productCloudreveSelectedPaaSSelector(runtimeAdapterRef, runtimeAdapterModuleRef string) ProductRuntimeOwnerSelector {
	return ProductRuntimeOwnerSelector{
		OwnerKind: "module", OwnerRef: "stackkits-cloudreve-runtime",
		ProviderRef: "stackkits-cloudreve", ModuleRef: "stackkits-cloudreve-runtime", UnitRef: "cloudreve",
		RuntimeKind: "container", RuntimeDelivery: "selected-paas", RuntimeEngine: "docker", WorkloadRef: "files",
		RuntimeAdapterRef: runtimeAdapterRef, RuntimeAdapterModuleRef: runtimeAdapterModuleRef,
	}
}

var _ ProductRuntimeOwnerFactory = (*productCloudreveSelectedPaaSFactory)(nil)
