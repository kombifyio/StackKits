package architecturev2

import (
	"errors"
	"strings"

	"github.com/kombifyio/stackkits/internal/runtimeexecutorlocal"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
)

const productFederationControlAgentAdapterID = "stackkits-federation-control-agent-local"

type productFederationControlAgentFactory struct {
	runtimeVersion string
	operations     runtimeexecutorlocal.FederationControlAgentOperations
}

// NewProductFederationControlAgentRegistration binds exactly one governed
// Modern Site/node to service-constructed outbound control operations. The
// Operations implementation retains transport, credential and custody details.
func NewProductFederationControlAgentRegistration(runtimeVersion string, operations runtimeexecutorlocal.FederationControlAgentOperations) (ProductRuntimeOwnerRegistration, error) {
	if runtimeVersion == "" || runtimeVersion != strings.TrimSpace(runtimeVersion) || nilProductRuntimeOwnerValue(operations) {
		return ProductRuntimeOwnerRegistration{}, errors.New("federation control-agent product registration requires a runtime version and operations owner")
	}
	return ProductRuntimeOwnerRegistration{Selector: productFederationControlAgentSelector(), Factory: &productFederationControlAgentFactory{runtimeVersion: runtimeVersion, operations: operations}}, nil
}

func (f *productFederationControlAgentFactory) PrepareRuntimeOwner(request ProductRuntimeOwnerRequest) (runtimeexecutor.Executor, error) {
	if f == nil || strings.TrimSpace(f.runtimeVersion) == "" || nilProductRuntimeOwnerValue(f.operations) {
		return nil, errors.New("federation control-agent product factory is not initialized")
	}
	target, health := cloneProductRuntimeTarget(request.Target), cloneProductHealthTargets(request.HealthTargets)
	if productRuntimeOwnerSelectorForTarget(target) != productFederationControlAgentSelector() || len(target.SiteRefs) != 1 || len(target.NodeRefs) != 1 || strings.TrimSpace(target.ExecutionChannelRef) == "" || len(health) != 1 || !productHealthTargetsRuntime(health[0], target) {
		return nil, errors.New("federation control-agent product factory requires one exact channel-bound Site/node target and health contract")
	}
	identity, err := productRuntimeOwnerAdapterIdentity(productFederationControlAgentAdapterID, f.runtimeVersion, target, health)
	if err != nil {
		return nil, err
	}
	return runtimeexecutorlocal.NewFederationControlAgentExecutor(identity, runtimeexecutorlocal.LocalTargetBinding{SiteRef: target.SiteRefs[0], NodeRef: target.NodeRefs[0], ExecutionChannelRef: target.ExecutionChannelRef}, runtimeexecutorlocal.FederationControlAgentAuthority{ProviderContractHash: target.ProviderContractHash, ModuleContractHash: target.ModuleContractHash, HealthContractHash: health[0].ContractHash}, f.operations), nil
}

func productFederationControlAgentSelector() ProductRuntimeOwnerSelector {
	return ProductRuntimeOwnerSelector{OwnerKind: "module", OwnerRef: "stackkits-federation-control-agent-runtime", ProviderRef: "stackkits-federation-control-agent", ModuleRef: "stackkits-federation-control-agent-runtime", UnitRef: "executor-contract", RuntimeKind: "host", RuntimeDelivery: "stackkit"}
}

var _ ProductRuntimeOwnerFactory = (*productFederationControlAgentFactory)(nil)
