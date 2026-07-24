package architecturev2

import (
	"errors"
	"strings"

	"github.com/kombifyio/stackkits/internal/runtimeexecutorlocal"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
)

const productBridgePublicationAdapterID = "stackkits-bridge-publication-local"

type productBridgePublicationFactory struct {
	runtimeVersion string
	operations     runtimeexecutorlocal.BridgePublicationOperations
}

// NewProductBridgePublicationRegistration binds the exact node-local Modern
// publication policy to a construction-owned Cloud edge implementation.
func NewProductBridgePublicationRegistration(runtimeVersion string, operations runtimeexecutorlocal.BridgePublicationOperations) (ProductRuntimeOwnerRegistration, error) {
	if runtimeVersion == "" || runtimeVersion != strings.TrimSpace(runtimeVersion) || nilProductRuntimeOwnerValue(operations) {
		return ProductRuntimeOwnerRegistration{}, errors.New("publication product registration requires a runtime version and operations owner")
	}
	return ProductRuntimeOwnerRegistration{
		Selector: productBridgePublicationSelector(),
		Factory:  &productBridgePublicationFactory{runtimeVersion: runtimeVersion, operations: operations},
	}, nil
}

func (f *productBridgePublicationFactory) PrepareRuntimeOwner(request ProductRuntimeOwnerRequest) (runtimeexecutor.Executor, error) {
	if f == nil || strings.TrimSpace(f.runtimeVersion) == "" || nilProductRuntimeOwnerValue(f.operations) {
		return nil, errors.New("publication product factory is not initialized")
	}
	target := cloneProductRuntimeTarget(request.Target)
	health := cloneProductHealthTargets(request.HealthTargets)
	if productRuntimeOwnerSelectorForTarget(target) != productBridgePublicationSelector() ||
		len(target.SiteRefs) != 1 || len(target.NodeRefs) != 1 ||
		strings.TrimSpace(target.ExecutionChannelRef) == "" || len(health) != 1 ||
		!productHealthTargetsRuntime(health[0], target) {
		return nil, errors.New("publication product factory requires one exact channel-bound Cloud edge target and health contract")
	}
	identity, err := productRuntimeOwnerAdapterIdentity(productBridgePublicationAdapterID, f.runtimeVersion, target, health)
	if err != nil {
		return nil, err
	}
	return runtimeexecutorlocal.NewBridgePublicationExecutor(identity, runtimeexecutorlocal.LocalTargetBinding{
		SiteRef: target.SiteRefs[0], NodeRef: target.NodeRefs[0], ExecutionChannelRef: target.ExecutionChannelRef,
	}, runtimeexecutorlocal.BridgePublicationAuthority{
		ProviderContractHash: target.ProviderContractHash,
		ModuleContractHash:   target.ModuleContractHash,
		HealthContractHash:   health[0].ContractHash,
	}, f.operations), nil
}

func productBridgePublicationSelector() ProductRuntimeOwnerSelector {
	return ProductRuntimeOwnerSelector{
		OwnerKind: "module", OwnerRef: "stackkits-bridge-publication-runtime",
		ProviderRef: "stackkits-service-publication-contract",
		ModuleRef:   "stackkits-bridge-publication-runtime", UnitRef: "executor-contract",
		RuntimeKind: "native", RuntimeDelivery: "stackkit",
	}
}

var _ ProductRuntimeOwnerFactory = (*productBridgePublicationFactory)(nil)
