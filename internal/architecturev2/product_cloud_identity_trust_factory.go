package architecturev2

import (
	"errors"
	"strings"

	"github.com/kombifyio/stackkits/internal/runtimeexecutorlocal"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
)

const productCloudIdentityTrustAdapterID = "stackkits-cloud-identity-trust-local"

type productCloudIdentityTrustFactory struct {
	runtimeVersion string
	operations     runtimeexecutorlocal.CloudIdentityTrustPolicyOperations
}

func NewProductCloudIdentityTrustRegistration(runtimeVersion string, operations runtimeexecutorlocal.CloudIdentityTrustPolicyOperations) (ProductRuntimeOwnerRegistration, error) {
	if runtimeVersion == "" || runtimeVersion != strings.TrimSpace(runtimeVersion) || nilProductRuntimeOwnerValue(operations) {
		return ProductRuntimeOwnerRegistration{}, errors.New("Cloud identity-trust product registration requires a runtime version and operations owner")
	}
	return ProductRuntimeOwnerRegistration{Selector: productCloudIdentityTrustSelector(), Factory: &productCloudIdentityTrustFactory{runtimeVersion: runtimeVersion, operations: operations}}, nil
}

func (f *productCloudIdentityTrustFactory) PrepareRuntimeOwner(request ProductRuntimeOwnerRequest) (runtimeexecutor.Executor, error) {
	if f == nil || strings.TrimSpace(f.runtimeVersion) == "" || nilProductRuntimeOwnerValue(f.operations) {
		return nil, errors.New("Cloud identity-trust product factory is not initialized")
	}
	target := cloneProductRuntimeTarget(request.Target)
	health := cloneProductHealthTargets(request.HealthTargets)
	if productRuntimeOwnerSelectorForTarget(target) != productCloudIdentityTrustSelector() || len(target.SiteRefs) != 1 || len(target.NodeRefs) != 1 ||
		strings.TrimSpace(target.ExecutionChannelRef) == "" || len(health) != 1 || !productHealthTargetsRuntime(health[0], target) {
		return nil, errors.New("Cloud identity-trust product factory requires one exact channel-bound target and health contract")
	}
	identity, err := productRuntimeOwnerAdapterIdentity(productCloudIdentityTrustAdapterID, f.runtimeVersion, target, health)
	if err != nil {
		return nil, err
	}
	return runtimeexecutorlocal.NewCloudIdentityTrustPolicyExecutor(identity, runtimeexecutorlocal.CloudIdentityTrustPolicyBinding{
		SiteRefs: target.SiteRefs, NodeRefs: target.NodeRefs, ExecutionChannelRef: target.ExecutionChannelRef,
	}, runtimeexecutorlocal.CloudIdentityTrustPolicyAuthority{
		ProviderContractHash: target.ProviderContractHash, ModuleContractHash: target.ModuleContractHash, HealthContractHash: health[0].ContractHash,
	}, f.operations), nil
}

func productCloudIdentityTrustSelector() ProductRuntimeOwnerSelector {
	return ProductRuntimeOwnerSelector{
		OwnerKind: "module", OwnerRef: "stackkits-cloud-identity-trust-policy-manifest",
		ProviderRef: "stackkits-cloud-identity-trust-policy", ModuleRef: "stackkits-cloud-identity-trust-policy-manifest", UnitRef: "policy-bundle",
		RuntimeKind: "native", RuntimeDelivery: "stackkit",
	}
}

var _ ProductRuntimeOwnerFactory = (*productCloudIdentityTrustFactory)(nil)
