package architecturev2

import (
	"errors"
	"strings"

	"github.com/kombifyio/stackkits/internal/runtimeexecutorlocal"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
)

const productHAAvailabilityAdapterID = "stackkits-ha-availability-local"

var productHAAvailabilityProviders = map[string]string{
	"stackkits-ha-basement-warm-runtime":   "stackkits-ha-basement-warm",
	"stackkits-ha-basement-quorum-runtime": "stackkits-ha-basement-quorum",
	"stackkits-ha-cloud-warm-runtime":      "stackkits-ha-cloud-warm",
	"stackkits-ha-cloud-quorum-runtime":    "stackkits-ha-cloud-quorum",
	"stackkits-ha-modern-warm-runtime":     "stackkits-ha-modern-warm",
	"stackkits-ha-modern-quorum-runtime":   "stackkits-ha-modern-quorum",
}

type productHAAvailabilityFactory struct {
	runtimeVersion string
	moduleRef      string
	operations     runtimeexecutorlocal.HAAvailabilityOperations
}

// NewProductHAAvailabilityRegistration binds one of the six concrete CUE
// catalog realizations to the single provider-free member-local owner.
func NewProductHAAvailabilityRegistration(runtimeVersion, moduleRef string, operations runtimeexecutorlocal.HAAvailabilityOperations) (ProductRuntimeOwnerRegistration, error) {
	if runtimeVersion == "" || runtimeVersion != strings.TrimSpace(runtimeVersion) ||
		moduleRef != strings.TrimSpace(moduleRef) || productHAAvailabilityProviders[moduleRef] == "" ||
		nilProductRuntimeOwnerValue(operations) {
		return ProductRuntimeOwnerRegistration{}, errors.New("HA availability product registration requires an exact catalog module, runtime version, and operations owner")
	}
	return ProductRuntimeOwnerRegistration{
		Selector: productHAAvailabilitySelector(moduleRef),
		Factory:  &productHAAvailabilityFactory{runtimeVersion: runtimeVersion, moduleRef: moduleRef, operations: operations},
	}, nil
}

func (f *productHAAvailabilityFactory) PrepareRuntimeOwner(request ProductRuntimeOwnerRequest) (runtimeexecutor.Executor, error) {
	if f == nil || strings.TrimSpace(f.runtimeVersion) == "" || productHAAvailabilityProviders[f.moduleRef] == "" ||
		nilProductRuntimeOwnerValue(f.operations) {
		return nil, errors.New("HA availability product factory is not initialized")
	}
	target := cloneProductRuntimeTarget(request.Target)
	health := cloneProductHealthTargets(request.HealthTargets)
	if productRuntimeOwnerSelectorForTarget(target) != productHAAvailabilitySelector(f.moduleRef) ||
		len(target.SiteRefs) != 1 || len(target.NodeRefs) != 1 || strings.TrimSpace(target.ExecutionChannelRef) == "" ||
		len(health) != 1 || !productHealthTargetsRuntime(health[0], target) {
		return nil, errors.New("HA availability product factory requires one exact channel-bound member and health contract")
	}
	identity, err := productRuntimeOwnerAdapterIdentity(productHAAvailabilityAdapterID, f.runtimeVersion, target, health)
	if err != nil {
		return nil, err
	}
	return runtimeexecutorlocal.NewHAAvailabilityExecutor(identity, runtimeexecutorlocal.LocalTargetBinding{
		SiteRef: target.SiteRefs[0], NodeRef: target.NodeRefs[0], ExecutionChannelRef: target.ExecutionChannelRef,
	}, runtimeexecutorlocal.HAAvailabilityAuthority{
		ProviderContractHash: target.ProviderContractHash,
		ModuleContractHash:   target.ModuleContractHash,
		HealthContractHash:   health[0].ContractHash,
	}, f.moduleRef, f.operations), nil
}

func productHAAvailabilitySelector(moduleRef string) ProductRuntimeOwnerSelector {
	return ProductRuntimeOwnerSelector{
		OwnerKind: "module", OwnerRef: moduleRef, ProviderRef: productHAAvailabilityProviders[moduleRef],
		ModuleRef: moduleRef, UnitRef: "executor-contract", RuntimeKind: "host", RuntimeDelivery: "stackkit",
	}
}

var _ ProductRuntimeOwnerFactory = (*productHAAvailabilityFactory)(nil)
