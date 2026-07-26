package architecturev2

import (
	"errors"
	"strings"

	"github.com/kombifyio/stackkits/internal/runtimeexecutorlocal"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
)

const productHostAdmissionAdapterID = "stackkits-host-admission-local"

type productHostAdmissionFactory struct {
	runtimeVersion string
}

func NewProductHostAdmissionRegistration(runtimeVersion string) (ProductRuntimeOwnerRegistration, error) {
	if runtimeVersion == "" || runtimeVersion != strings.TrimSpace(runtimeVersion) {
		return ProductRuntimeOwnerRegistration{}, errors.New("host-admission registration requires a runtime version")
	}
	return ProductRuntimeOwnerRegistration{
		Selector: productHostAdmissionSelector(),
		Factory:  &productHostAdmissionFactory{runtimeVersion: runtimeVersion},
	}, nil
}

func (f *productHostAdmissionFactory) PrepareRuntimeOwner(request ProductRuntimeOwnerRequest) (runtimeexecutor.Executor, error) {
	if f == nil || strings.TrimSpace(f.runtimeVersion) == "" {
		return nil, errors.New("host-admission product factory is not initialized")
	}
	target := cloneProductRuntimeTarget(request.Target)
	health := cloneProductHealthTargets(request.HealthTargets)
	if productRuntimeOwnerSelectorForTarget(target) != productHostAdmissionSelector() ||
		len(target.SiteRefs) != 1 || len(target.NodeRefs) != 1 ||
		strings.TrimSpace(target.ExecutionChannelRef) == "" || len(health) != 1 ||
		!productHealthTargetsRuntime(health[0], target) {
		return nil, errors.New("host-admission factory requires one exact channel-bound target and health contract")
	}
	identity, err := productRuntimeOwnerAdapterIdentity(productHostAdmissionAdapterID, f.runtimeVersion, target, health)
	if err != nil {
		return nil, err
	}
	return runtimeexecutorlocal.NewHostAdmissionExecutor(identity, runtimeexecutorlocal.LocalTargetBinding{
		SiteRef: target.SiteRefs[0], NodeRef: target.NodeRefs[0],
		ExecutionChannelRef: target.ExecutionChannelRef,
	}, runtimeexecutorlocal.HostAdmissionAuthority{
		ProviderContractHash: target.ProviderContractHash,
		HealthContractHash:   health[0].ContractHash,
	}), nil
}

func productHostAdmissionSelector() ProductRuntimeOwnerSelector {
	return ProductRuntimeOwnerSelector{
		OwnerKind: "provider-owner", OwnerRef: "stackkits-host-admission",
		ProviderRef: "stackkits-host-admission", RuntimeKind: "host",
		RuntimeDelivery: "provider-owner",
	}
}

var _ ProductRuntimeOwnerFactory = (*productHostAdmissionFactory)(nil)
