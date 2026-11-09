package architecturev2

import (
	"errors"
	"strings"

	"github.com/kombifyio/stackkits/contracts/runtimeexecutor"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"
)

const productHomePrivateRemoteAccessAdapterID = "stackkits-home-private-remote-access-local"

type productHomePrivateRemoteAccessFactory struct {
	runtimeVersion string
	operations     nativehost.HomePrivateRemoteAccessOperations
}

// NewProductHomePrivateRemoteAccessRegistration binds the exact node-local
// private-remote-access owner to the node's reach custody. A standalone Home
// core realizes the capability through the standard reach paths; a
// Techstack-dispatched channel keeps the process-dispatched owner.
func NewProductHomePrivateRemoteAccessRegistration(runtimeVersion string, operations nativehost.HomePrivateRemoteAccessOperations) (ProductRuntimeOwnerRegistration, error) {
	if runtimeVersion == "" || runtimeVersion != strings.TrimSpace(runtimeVersion) || nilProductRuntimeOwnerValue(operations) {
		return ProductRuntimeOwnerRegistration{}, errors.New("Home private-remote-access product registration requires a runtime version and operations owner")
	}
	return ProductRuntimeOwnerRegistration{
		Selector: productHomePrivateRemoteAccessSelector(),
		Factory:  &productHomePrivateRemoteAccessFactory{runtimeVersion: runtimeVersion, operations: operations},
	}, nil
}

func (f *productHomePrivateRemoteAccessFactory) PrepareRuntimeOwner(request ProductRuntimeOwnerRequest) (runtimeexecutor.Executor, error) {
	if f == nil || strings.TrimSpace(f.runtimeVersion) == "" || nilProductRuntimeOwnerValue(f.operations) {
		return nil, errors.New("Home private-remote-access product factory is not initialized")
	}
	target := cloneProductRuntimeTarget(request.Target)
	health := cloneProductHealthTargets(request.HealthTargets)
	if productRuntimeOwnerSelectorForTarget(target) != productHomePrivateRemoteAccessSelector() ||
		len(target.SiteRefs) != 1 || len(target.NodeRefs) != 1 || strings.TrimSpace(target.ExecutionChannelRef) == "" ||
		len(health) != 1 || !productHealthTargetsRuntime(health[0], target) {
		return nil, errors.New("Home private-remote-access product factory requires one exact channel-bound target and health contract")
	}
	identity, err := productRuntimeOwnerAdapterIdentity(productHomePrivateRemoteAccessAdapterID, f.runtimeVersion, target, health)
	if err != nil {
		return nil, err
	}
	return nativehost.NewHomePrivateRemoteAccessExecutor(identity, nativehost.LocalTargetBinding{
		SiteRef: target.SiteRefs[0], NodeRef: target.NodeRefs[0], ExecutionChannelRef: target.ExecutionChannelRef,
	}, nativehost.HomePrivateRemoteAccessAuthority{
		ProviderContractHash: target.ProviderContractHash,
		ModuleContractHash:   target.ModuleContractHash,
		HealthContractHash:   health[0].ContractHash,
	}, f.operations), nil
}

var _ ProductRuntimeOwnerFactory = (*productHomePrivateRemoteAccessFactory)(nil)
