package architecturev2

import (
	"errors"
	"strings"

	"github.com/kombifyio/stackkits/internal/runtimeexecutorlocal"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
)

const productHomeDeviceAuthorityAdapterID = "stackkits-home-device-authority-local"

type productHomeDeviceAuthorityFactory struct {
	runtimeVersion string
	operations     runtimeexecutorlocal.HomeDeviceAuthorityPolicyOperations
}

// NewProductHomeDeviceAuthorityRegistration binds the exact node-local Home
// device-authority selector to construction-owned enforcement operations.
// Credentials, keys, endpoints, discovery, and provider lifecycle remain
// outside this factory and the generated policy artifact.
func NewProductHomeDeviceAuthorityRegistration(runtimeVersion string, operations runtimeexecutorlocal.HomeDeviceAuthorityPolicyOperations) (ProductRuntimeOwnerRegistration, error) {
	if runtimeVersion == "" || runtimeVersion != strings.TrimSpace(runtimeVersion) || nilProductRuntimeOwnerValue(operations) {
		return ProductRuntimeOwnerRegistration{}, errors.New("Home device-authority product registration requires a runtime version and operations owner")
	}
	return ProductRuntimeOwnerRegistration{
		Selector: productHomeDeviceAuthoritySelector(),
		Factory:  &productHomeDeviceAuthorityFactory{runtimeVersion: runtimeVersion, operations: operations},
	}, nil
}

func (f *productHomeDeviceAuthorityFactory) PrepareRuntimeOwner(request ProductRuntimeOwnerRequest) (runtimeexecutor.Executor, error) {
	if f == nil || strings.TrimSpace(f.runtimeVersion) == "" || nilProductRuntimeOwnerValue(f.operations) {
		return nil, errors.New("Home device-authority product factory is not initialized")
	}
	target := cloneProductRuntimeTarget(request.Target)
	health := cloneProductHealthTargets(request.HealthTargets)
	if productRuntimeOwnerSelectorForTarget(target) != productHomeDeviceAuthoritySelector() ||
		len(target.SiteRefs) != 1 || len(target.NodeRefs) != 1 || strings.TrimSpace(target.ExecutionChannelRef) == "" || len(health) != 1 ||
		!productHealthTargetsRuntime(health[0], target) {
		return nil, errors.New("Home device-authority product factory requires one exact channel-bound target and health contract")
	}
	identity, err := productRuntimeOwnerAdapterIdentity(productHomeDeviceAuthorityAdapterID, f.runtimeVersion, target, health)
	if err != nil {
		return nil, err
	}
	return runtimeexecutorlocal.NewHomeDeviceAuthorityPolicyExecutor(identity, runtimeexecutorlocal.HomeDeviceAuthorityPolicyBinding{
		SiteRefs: target.SiteRefs, NodeRefs: target.NodeRefs, ExecutionChannelRef: target.ExecutionChannelRef,
	}, runtimeexecutorlocal.HomeDeviceAuthorityPolicyAuthority{
		ProviderContractHash: target.ProviderContractHash, ModuleContractHash: target.ModuleContractHash, HealthContractHash: health[0].ContractHash,
	}, f.operations), nil
}

func productHomeDeviceAuthoritySelector() ProductRuntimeOwnerSelector {
	return ProductRuntimeOwnerSelector{
		OwnerKind: "module", OwnerRef: "stackkits-home-device-authority-policy-manifest",
		ProviderRef: "stackkits-home-device-authority", ModuleRef: "stackkits-home-device-authority-policy-manifest", UnitRef: "policy-bundle",
		RuntimeKind: "native", RuntimeDelivery: "stackkit",
	}
}

var _ ProductRuntimeOwnerFactory = (*productHomeDeviceAuthorityFactory)(nil)
