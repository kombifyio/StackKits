package architecturev2

import (
	"errors"
	"strings"

	"github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
)

const productHomeHostSecurityAdapterID = "stackkits-home-host-security-local"

type productHomeHostSecurityFactory struct {
	runtimeVersion string
	operations     nativehost.HomeHostSecurityOperations
}

// NewProductHomeHostSecurityRegistration binds the exact node-local Home host
// security owner to construction-owned enforcement operations. Public-edge,
// DNS, backup, mesh, credentials and provider lifecycle stay outside it.
func NewProductHomeHostSecurityRegistration(runtimeVersion string, operations nativehost.HomeHostSecurityOperations) (ProductRuntimeOwnerRegistration, error) {
	if runtimeVersion == "" || runtimeVersion != strings.TrimSpace(runtimeVersion) || nilProductRuntimeOwnerValue(operations) {
		return ProductRuntimeOwnerRegistration{}, errors.New("Home host-security product registration requires a runtime version and operations owner")
	}
	return ProductRuntimeOwnerRegistration{
		Selector: productHomeHostSecuritySelector(),
		Factory:  &productHomeHostSecurityFactory{runtimeVersion: runtimeVersion, operations: operations},
	}, nil
}

func (f *productHomeHostSecurityFactory) PrepareRuntimeOwner(request ProductRuntimeOwnerRequest) (runtimeexecutor.Executor, error) {
	if f == nil || strings.TrimSpace(f.runtimeVersion) == "" || nilProductRuntimeOwnerValue(f.operations) {
		return nil, errors.New("Home host-security product factory is not initialized")
	}
	target := cloneProductRuntimeTarget(request.Target)
	health := cloneProductHealthTargets(request.HealthTargets)
	if productRuntimeOwnerSelectorForTarget(target) != productHomeHostSecuritySelector() ||
		len(target.SiteRefs) != 1 || len(target.NodeRefs) != 1 || strings.TrimSpace(target.ExecutionChannelRef) == "" ||
		len(health) != 1 || !productHealthTargetsRuntime(health[0], target) {
		return nil, errors.New("Home host-security product factory requires one exact channel-bound target and health contract")
	}
	identity, err := productRuntimeOwnerAdapterIdentity(productHomeHostSecurityAdapterID, f.runtimeVersion, target, health)
	if err != nil {
		return nil, err
	}
	return nativehost.NewHomeHostSecurityExecutor(identity, nativehost.LocalTargetBinding{
		SiteRef: target.SiteRefs[0], NodeRef: target.NodeRefs[0], ExecutionChannelRef: target.ExecutionChannelRef,
	}, nativehost.HomeHostSecurityAuthority{
		ProviderContractHash: target.ProviderContractHash,
		ModuleContractHash:   target.ModuleContractHash,
		HealthContractHash:   health[0].ContractHash,
	}, f.operations), nil
}

func productHomeHostSecuritySelector() ProductRuntimeOwnerSelector {
	return ProductRuntimeOwnerSelector{
		OwnerKind: "module", OwnerRef: "stackkits-home-host-security-runtime",
		ProviderRef: "stackkits-home-host-security", ModuleRef: "stackkits-home-host-security-runtime", UnitRef: "executor-contract",
		RuntimeKind: "host", RuntimeDelivery: "stackkit",
	}
}

var _ ProductRuntimeOwnerFactory = (*productHomeHostSecurityFactory)(nil)
