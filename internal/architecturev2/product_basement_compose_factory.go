package architecturev2

import (
	"errors"
	"strings"

	"github.com/kombifyio/stackkits/internal/runtimeexecutorlocal"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
)

const productBasementComposeAdapterID = "stackkits-basement-compose-local"

type productBasementComposeFactory struct {
	runtimeVersion string
	operations     runtimeexecutorlocal.BasementComposeOperations
}

// NewProductBasementComposeRegistration binds only the optional Basement
// socket-proxy Compose unit to an authenticated local runtime owner. It does
// not make Compose a Kit-wide runtime and cannot select or discover Docker.
func NewProductBasementComposeRegistration(runtimeVersion string, operations runtimeexecutorlocal.BasementComposeOperations) (ProductRuntimeOwnerRegistration, error) {
	if runtimeVersion == "" || runtimeVersion != strings.TrimSpace(runtimeVersion) || nilProductRuntimeOwnerValue(operations) {
		return ProductRuntimeOwnerRegistration{}, errors.New("Basement Compose product registration requires a runtime version and operations owner")
	}
	return ProductRuntimeOwnerRegistration{
		Selector: productBasementComposeSelector(),
		Factory:  &productBasementComposeFactory{runtimeVersion: runtimeVersion, operations: operations},
	}, nil
}

func (f *productBasementComposeFactory) PrepareRuntimeOwner(request ProductRuntimeOwnerRequest) (runtimeexecutor.Executor, error) {
	if f == nil || strings.TrimSpace(f.runtimeVersion) == "" || nilProductRuntimeOwnerValue(f.operations) {
		return nil, errors.New("Basement Compose product factory is not initialized")
	}
	target := cloneProductRuntimeTarget(request.Target)
	health := cloneProductHealthTargets(request.HealthTargets)
	if productRuntimeOwnerSelectorForTarget(target) != productBasementComposeSelector() ||
		len(target.SiteRefs) != 1 || len(target.NodeRefs) != 1 || strings.TrimSpace(target.ExecutionChannelRef) == "" ||
		len(health) != 1 || !productHealthTargetsRuntime(health[0], target) {
		return nil, errors.New("Basement Compose product factory requires one exact channel-bound target and health contract")
	}
	identity, err := productRuntimeOwnerAdapterIdentity(productBasementComposeAdapterID, f.runtimeVersion, target, health)
	if err != nil {
		return nil, err
	}
	return runtimeexecutorlocal.NewBasementComposeExecutor(identity, runtimeexecutorlocal.LocalTargetBinding{
		SiteRef: target.SiteRefs[0], NodeRef: target.NodeRefs[0], ExecutionChannelRef: target.ExecutionChannelRef,
	}, runtimeexecutorlocal.BasementComposeAuthority{
		ProviderContractHash: target.ProviderContractHash,
		ModuleContractHash:   target.ModuleContractHash,
		HealthContractHash:   health[0].ContractHash,
	}, f.operations), nil
}

func productBasementComposeSelector() ProductRuntimeOwnerSelector {
	return ProductRuntimeOwnerSelector{
		OwnerKind: "module", OwnerRef: "socket-proxy",
		ProviderRef: "stackkits-basement-compose", ModuleRef: "socket-proxy", UnitRef: "compose",
		RuntimeKind: "container", RuntimeDelivery: "stackkit", RuntimeEngine: "docker",
	}
}

var _ ProductRuntimeOwnerFactory = (*productBasementComposeFactory)(nil)
