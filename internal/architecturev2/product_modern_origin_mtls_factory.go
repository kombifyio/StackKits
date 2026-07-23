package architecturev2

import (
	"errors"
	"strings"

	"github.com/kombifyio/stackkits/internal/runtimeexecutorlocal"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
)

const productBridgeOriginMTLSAdapterID = "stackkits-bridge-origin-mtls-local"

type productBridgeOriginMTLSFactory struct {
	runtimeVersion string
	operations     runtimeexecutorlocal.BridgeOriginMTLSOperations
}

// NewProductBridgeOriginMTLSRegistration binds the exact node-local Modern
// origin policy to an authenticated Home operations implementation.
func NewProductBridgeOriginMTLSRegistration(runtimeVersion string, operations runtimeexecutorlocal.BridgeOriginMTLSOperations) (ProductRuntimeOwnerRegistration, error) {
	if runtimeVersion == "" || runtimeVersion != strings.TrimSpace(runtimeVersion) || nilProductRuntimeOwnerValue(operations) {
		return ProductRuntimeOwnerRegistration{}, errors.New("origin mTLS product registration requires a runtime version and operations owner")
	}
	return ProductRuntimeOwnerRegistration{
		Selector: productBridgeOriginMTLSSelector(),
		Factory:  &productBridgeOriginMTLSFactory{runtimeVersion: runtimeVersion, operations: operations},
	}, nil
}

func (f *productBridgeOriginMTLSFactory) PrepareRuntimeOwner(request ProductRuntimeOwnerRequest) (runtimeexecutor.Executor, error) {
	if f == nil || strings.TrimSpace(f.runtimeVersion) == "" || nilProductRuntimeOwnerValue(f.operations) {
		return nil, errors.New("origin mTLS product factory is not initialized")
	}
	target := cloneProductRuntimeTarget(request.Target)
	health := cloneProductHealthTargets(request.HealthTargets)
	if productRuntimeOwnerSelectorForTarget(target) != productBridgeOriginMTLSSelector() ||
		len(target.SiteRefs) != 1 || len(target.NodeRefs) != 1 || strings.TrimSpace(target.ExecutionChannelRef) == "" ||
		len(health) != 1 || !productHealthTargetsRuntime(health[0], target) {
		return nil, errors.New("origin mTLS product factory requires one exact channel-bound Home target and health contract")
	}
	identity, err := productRuntimeOwnerAdapterIdentity(productBridgeOriginMTLSAdapterID, f.runtimeVersion, target, health)
	if err != nil {
		return nil, err
	}
	return runtimeexecutorlocal.NewBridgeOriginMTLSExecutor(identity, runtimeexecutorlocal.LocalTargetBinding{
		SiteRef: target.SiteRefs[0], NodeRef: target.NodeRefs[0], ExecutionChannelRef: target.ExecutionChannelRef,
	}, runtimeexecutorlocal.BridgeOriginMTLSAuthority{
		ProviderContractHash: target.ProviderContractHash,
		ModuleContractHash:   target.ModuleContractHash,
		HealthContractHash:   health[0].ContractHash,
	}, f.operations), nil
}

func productBridgeOriginMTLSSelector() ProductRuntimeOwnerSelector {
	return ProductRuntimeOwnerSelector{
		OwnerKind: "module", OwnerRef: "stackkits-bridge-origin-mtls-runtime",
		ProviderRef: "stackkits-service-publication-contract",
		ModuleRef:   "stackkits-bridge-origin-mtls-runtime", UnitRef: "executor-contract",
		RuntimeKind: "native", RuntimeDelivery: "stackkit",
	}
}

var _ ProductRuntimeOwnerFactory = (*productBridgeOriginMTLSFactory)(nil)
