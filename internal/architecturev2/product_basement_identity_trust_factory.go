package architecturev2

import (
	"errors"
	"strings"

	"github.com/kombifyio/stackkits/internal/runtimeexecutorlocal"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
)

const productBasementIdentityTrustAdapterID = "stackkits-basement-identity-trust-local"

type productBasementIdentityTrustFactory struct {
	runtimeVersion string
	operations     runtimeexecutorlocal.BasementIdentityTrustPolicyOperations
}

func NewProductBasementIdentityTrustRegistration(runtimeVersion string, operations runtimeexecutorlocal.BasementIdentityTrustPolicyOperations) (ProductRuntimeOwnerRegistration, error) {
	if runtimeVersion == "" || runtimeVersion != strings.TrimSpace(runtimeVersion) || nilProductRuntimeOwnerValue(operations) {
		return ProductRuntimeOwnerRegistration{}, errors.New("Basement identity-trust product registration requires a runtime version and operations owner")
	}
	return ProductRuntimeOwnerRegistration{Selector: productBasementIdentityTrustSelector(), Factory: &productBasementIdentityTrustFactory{runtimeVersion: runtimeVersion, operations: operations}}, nil
}

func (f *productBasementIdentityTrustFactory) PrepareRuntimeOwner(request ProductRuntimeOwnerRequest) (runtimeexecutor.Executor, error) {
	if f == nil || strings.TrimSpace(f.runtimeVersion) == "" || nilProductRuntimeOwnerValue(f.operations) {
		return nil, errors.New("Basement identity-trust product factory is not initialized")
	}
	target := cloneProductRuntimeTarget(request.Target)
	health := cloneProductHealthTargets(request.HealthTargets)
	if productRuntimeOwnerSelectorForTarget(target) != productBasementIdentityTrustSelector() || len(target.SiteRefs) != 1 || len(target.NodeRefs) != 1 ||
		strings.TrimSpace(target.ExecutionChannelRef) == "" || len(health) != 1 || !productHealthTargetsRuntime(health[0], target) {
		return nil, errors.New("Basement identity-trust product factory requires one exact channel-bound target and health contract")
	}
	identity, err := productRuntimeOwnerAdapterIdentity(productBasementIdentityTrustAdapterID, f.runtimeVersion, target, health)
	if err != nil {
		return nil, err
	}
	return runtimeexecutorlocal.NewBasementIdentityTrustPolicyExecutor(identity, runtimeexecutorlocal.BasementIdentityTrustPolicyBinding{
		SiteRefs: target.SiteRefs, NodeRefs: target.NodeRefs, ExecutionChannelRef: target.ExecutionChannelRef,
	}, runtimeexecutorlocal.BasementIdentityTrustPolicyAuthority{
		ProviderContractHash: target.ProviderContractHash, ModuleContractHash: target.ModuleContractHash, HealthContractHash: health[0].ContractHash,
	}, f.operations), nil
}

func productBasementIdentityTrustSelector() ProductRuntimeOwnerSelector {
	return ProductRuntimeOwnerSelector{
		OwnerKind: "module", OwnerRef: "stackkits-basement-identity-trust-policy-manifest",
		ProviderRef: "stackkits-basement-identity-trust-policy", ModuleRef: "stackkits-basement-identity-trust-policy-manifest", UnitRef: "policy-bundle",
		RuntimeKind: "native", RuntimeDelivery: "stackkit",
	}
}

var _ ProductRuntimeOwnerFactory = (*productBasementIdentityTrustFactory)(nil)
