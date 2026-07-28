package architecturev2

import (
	"errors"
	"strings"

	"github.com/kombifyio/stackkits/internal/runtimeexecutorlocal"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
)

const productBasementCoreAdapterID = "stackkits-basement-core-local"

type productBasementCoreFactory struct {
	runtimeVersion string
	operations     runtimeexecutorlocal.BasementCoreOperations
}

// NewProductBasementCoreRegistration binds only the CUE-owned standard
// Basement Compose unit to the local runtime owner.
func NewProductBasementCoreRegistration(runtimeVersion string, operations runtimeexecutorlocal.BasementCoreOperations) (ProductRuntimeOwnerRegistration, error) {
	if runtimeVersion == "" || runtimeVersion != strings.TrimSpace(runtimeVersion) || nilProductRuntimeOwnerValue(operations) {
		return ProductRuntimeOwnerRegistration{}, errors.New("Basement core registration requires a runtime version and local operations owner")
	}
	return ProductRuntimeOwnerRegistration{
		Selector: productBasementCoreSelector(),
		Factory:  &productBasementCoreFactory{runtimeVersion: runtimeVersion, operations: operations},
	}, nil
}

func (f *productBasementCoreFactory) PrepareRuntimeOwner(request ProductRuntimeOwnerRequest) (runtimeexecutor.Executor, error) {
	if f == nil || strings.TrimSpace(f.runtimeVersion) == "" || nilProductRuntimeOwnerValue(f.operations) {
		return nil, errors.New("Basement core product factory is not initialized")
	}
	target := cloneProductRuntimeTarget(request.Target)
	health := cloneProductHealthTargets(request.HealthTargets)
	if productRuntimeOwnerSelectorForTarget(target) != productBasementCoreSelector() ||
		len(target.SiteRefs) != 1 || len(target.NodeRefs) != 1 ||
		strings.TrimSpace(target.ExecutionChannelRef) == "" || len(health) != 7 {
		return nil, errors.New("Basement core product factory requires one exact channel-bound target and seven postconditions")
	}
	healthHashes := make(map[string]string, len(health))
	for _, item := range health {
		if !productHealthTargetsRuntime(item, target) || item.SourceRef == "" || item.ContractHash == "" {
			return nil, errors.New("Basement core health authority does not target the exact runtime")
		}
		if _, duplicate := healthHashes[item.SourceRef]; duplicate {
			return nil, errors.New("Basement core health authority contains a duplicate source")
		}
		healthHashes[item.SourceRef] = item.ContractHash
	}
	identity, err := productRuntimeOwnerAdapterIdentity(productBasementCoreAdapterID, f.runtimeVersion, target, health)
	if err != nil {
		return nil, err
	}
	return runtimeexecutorlocal.NewBasementCoreExecutor(identity, runtimeexecutorlocal.LocalTargetBinding{
		SiteRef: target.SiteRefs[0], NodeRef: target.NodeRefs[0],
		ExecutionChannelRef: target.ExecutionChannelRef,
	}, runtimeexecutorlocal.BasementCoreAuthority{
		ProviderContractHash: target.ProviderContractHash,
		ModuleContractHash:   target.ModuleContractHash,
		HealthContractHashes: healthHashes,
	}, f.operations), nil
}

func productBasementCoreSelector() ProductRuntimeOwnerSelector {
	return ProductRuntimeOwnerSelector{
		OwnerKind: "module", OwnerRef: "stackkits-basement-core-runtime",
		ProviderRef: "stackkits-basement-core", ModuleRef: "stackkits-basement-core-runtime",
		UnitRef: "compose", RuntimeKind: "container", RuntimeDelivery: "stackkit",
		RuntimeEngine: "docker", WorkloadRef: "basement-core",
	}
}

var _ ProductRuntimeOwnerFactory = (*productBasementCoreFactory)(nil)
