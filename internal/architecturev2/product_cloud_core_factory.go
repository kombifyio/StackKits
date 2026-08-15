package architecturev2

import (
	"errors"
	"strings"

	"github.com/kombifyio/stackkits/internal/runtimeexecutorlocal"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
)

const productCloudCoreAdapterID = "stackkits-cloud-core-local"

type productCloudCoreFactory struct {
	runtimeVersion string
	operations     runtimeexecutorlocal.CloudCoreOperations
}

func NewProductCloudCoreRegistration(runtimeVersion string, operations runtimeexecutorlocal.CloudCoreOperations) (ProductRuntimeOwnerRegistration, error) {
	if runtimeVersion == "" || runtimeVersion != strings.TrimSpace(runtimeVersion) || nilProductRuntimeOwnerValue(operations) {
		return ProductRuntimeOwnerRegistration{}, errors.New("Cloud core registration requires a runtime version and host operations owner")
	}
	return ProductRuntimeOwnerRegistration{Selector: productCloudCoreSelector(), Factory: &productCloudCoreFactory{runtimeVersion: runtimeVersion, operations: operations}}, nil
}

func (f *productCloudCoreFactory) PrepareRuntimeOwner(request ProductRuntimeOwnerRequest) (runtimeexecutor.Executor, error) {
	if f == nil || strings.TrimSpace(f.runtimeVersion) == "" || nilProductRuntimeOwnerValue(f.operations) {
		return nil, errors.New("Cloud core product factory is not initialized")
	}
	target := cloneProductRuntimeTarget(request.Target)
	health := cloneProductHealthTargets(request.HealthTargets)
	if productRuntimeOwnerSelectorForTarget(target) != productCloudCoreSelector() || len(target.SiteRefs) != 1 || len(target.NodeRefs) != 1 ||
		strings.TrimSpace(target.ExecutionChannelRef) == "" || len(health) == 0 {
		return nil, errors.New("Cloud core product factory requires one channel-bound target and at least one postcondition")
	}
	healthHashes := make(map[string]string, len(health))
	for _, item := range health {
		if !productHealthTargetsRuntime(item, target) || item.SourceRef == "" || item.ContractHash == "" {
			return nil, errors.New("Cloud core health authority does not target the exact runtime")
		}
		if _, duplicate := healthHashes[item.SourceRef]; duplicate {
			return nil, errors.New("Cloud core health authority contains a duplicate source")
		}
		healthHashes[item.SourceRef] = item.ContractHash
	}
	identity, err := productRuntimeOwnerAdapterIdentity(productCloudCoreAdapterID, f.runtimeVersion, target, health)
	if err != nil {
		return nil, err
	}
	return runtimeexecutorlocal.NewCloudCoreExecutor(identity, runtimeexecutorlocal.LocalTargetBinding{SiteRef: target.SiteRefs[0], NodeRef: target.NodeRefs[0], ExecutionChannelRef: target.ExecutionChannelRef},
		runtimeexecutorlocal.CloudCoreAuthority{ProviderContractHash: target.ProviderContractHash, ModuleContractHash: target.ModuleContractHash, HealthContractHashes: healthHashes}, f.operations), nil
}

func productCloudCoreSelector() ProductRuntimeOwnerSelector {
	return ProductRuntimeOwnerSelector{OwnerKind: "module", OwnerRef: "stackkits-cloud-core-runtime", ProviderRef: "stackkits-cloud-core",
		ModuleRef: "stackkits-cloud-core-runtime", UnitRef: "compose", RuntimeKind: "container", RuntimeDelivery: "stackkit", RuntimeEngine: "docker", WorkloadRef: "cloud-core"}
}

var _ ProductRuntimeOwnerFactory = (*productCloudCoreFactory)(nil)
