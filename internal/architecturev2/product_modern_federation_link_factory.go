package architecturev2

import (
	"errors"
	"strings"

	"github.com/kombifyio/stackkits/internal/runtimeexecutorlocal"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
)

const productFederationLinkAdapterID = "stackkits-federation-link-local"

type productFederationLinkFactory struct {
	runtimeVersion string
	operations     runtimeexecutorlocal.FederationLinkOperations
}

// NewProductFederationLinkRegistration binds one exact node-local Modern
// federation policy to a construction-owned link implementation. Provider,
// endpoint, credential, lease, and fabric lifecycle authority remain outside
// StackKits.
func NewProductFederationLinkRegistration(runtimeVersion string, operations runtimeexecutorlocal.FederationLinkOperations) (ProductRuntimeOwnerRegistration, error) {
	if runtimeVersion == "" || runtimeVersion != strings.TrimSpace(runtimeVersion) || nilProductRuntimeOwnerValue(operations) {
		return ProductRuntimeOwnerRegistration{}, errors.New("federation-link product registration requires a runtime version and operations owner")
	}
	return ProductRuntimeOwnerRegistration{
		Selector: productFederationLinkSelector(),
		Factory:  &productFederationLinkFactory{runtimeVersion: runtimeVersion, operations: operations},
	}, nil
}

func (f *productFederationLinkFactory) PrepareRuntimeOwner(request ProductRuntimeOwnerRequest) (runtimeexecutor.Executor, error) {
	if f == nil || strings.TrimSpace(f.runtimeVersion) == "" || nilProductRuntimeOwnerValue(f.operations) {
		return nil, errors.New("federation-link product factory is not initialized")
	}
	target := cloneProductRuntimeTarget(request.Target)
	health := cloneProductHealthTargets(request.HealthTargets)
	if productRuntimeOwnerSelectorForTarget(target) != productFederationLinkSelector() ||
		len(target.SiteRefs) != 1 || len(target.NodeRefs) != 1 ||
		strings.TrimSpace(target.ExecutionChannelRef) == "" || len(health) != 1 ||
		!productHealthTargetsRuntime(health[0], target) {
		return nil, errors.New("federation-link product factory requires one exact channel-bound Site/node target and health contract")
	}
	identity, err := productRuntimeOwnerAdapterIdentity(productFederationLinkAdapterID, f.runtimeVersion, target, health)
	if err != nil {
		return nil, err
	}
	return runtimeexecutorlocal.NewFederationLinkExecutor(identity, runtimeexecutorlocal.LocalTargetBinding{
		SiteRef: target.SiteRefs[0], NodeRef: target.NodeRefs[0], ExecutionChannelRef: target.ExecutionChannelRef,
	}, runtimeexecutorlocal.FederationLinkAuthority{
		ProviderContractHash: target.ProviderContractHash,
		ModuleContractHash:   target.ModuleContractHash,
		HealthContractHash:   health[0].ContractHash,
	}, f.operations), nil
}

func productFederationLinkSelector() ProductRuntimeOwnerSelector {
	return ProductRuntimeOwnerSelector{
		OwnerKind: "module", OwnerRef: "stackkits-federation-link-runtime",
		ProviderRef: "stackkits-federation-link",
		ModuleRef:   "stackkits-federation-link-runtime", UnitRef: "executor-contract",
		RuntimeKind: "host", RuntimeDelivery: "stackkit",
	}
}

var _ ProductRuntimeOwnerFactory = (*productFederationLinkFactory)(nil)
