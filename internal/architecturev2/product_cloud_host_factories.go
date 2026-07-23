package architecturev2

import (
	"errors"
	"strings"

	"github.com/kombifyio/stackkits/internal/runtimeexecutorlocal"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
)

const (
	productCloudHostSecurityAdapterID = "stackkits-cloud-host-security-local"
	productCloudPublicEdgeAdapterID   = "stackkits-cloud-public-edge-local"
)

type productCloudHostSecurityFactory struct {
	runtimeVersion string
	operations     runtimeexecutorlocal.CloudHostSecurityOperations
}

type productCloudPublicEdgeFactory struct {
	runtimeVersion string
	operations     runtimeexecutorlocal.CloudPublicEdgeOperations
}

// NewProductCloudHostSecurityRegistration binds the exact node-local Cloud
// firewall/hardening owner to an authenticated host-channel implementation.
// Provider lifecycle and host transport remain outside the registration.
func NewProductCloudHostSecurityRegistration(runtimeVersion string, operations runtimeexecutorlocal.CloudHostSecurityOperations) (ProductRuntimeOwnerRegistration, error) {
	if runtimeVersion == "" || runtimeVersion != strings.TrimSpace(runtimeVersion) || nilProductRuntimeOwnerValue(operations) {
		return ProductRuntimeOwnerRegistration{}, errors.New("Cloud host-security product registration requires a runtime version and operations owner")
	}
	return ProductRuntimeOwnerRegistration{
		Selector: productCloudHostSecuritySelector(),
		Factory:  &productCloudHostSecurityFactory{runtimeVersion: runtimeVersion, operations: operations},
	}, nil
}

func (f *productCloudHostSecurityFactory) PrepareRuntimeOwner(request ProductRuntimeOwnerRequest) (runtimeexecutor.Executor, error) {
	if f == nil || strings.TrimSpace(f.runtimeVersion) == "" || nilProductRuntimeOwnerValue(f.operations) {
		return nil, errors.New("Cloud host-security product factory is not initialized")
	}
	target := cloneProductRuntimeTarget(request.Target)
	health := cloneProductHealthTargets(request.HealthTargets)
	if productRuntimeOwnerSelectorForTarget(target) != productCloudHostSecuritySelector() ||
		len(target.SiteRefs) != 1 || len(target.NodeRefs) != 1 || strings.TrimSpace(target.ExecutionChannelRef) == "" ||
		len(health) != 1 || !productHealthTargetsRuntime(health[0], target) {
		return nil, errors.New("Cloud host-security product factory requires one exact channel-bound target and health contract")
	}
	identity, err := productRuntimeOwnerAdapterIdentity(productCloudHostSecurityAdapterID, f.runtimeVersion, target, health)
	if err != nil {
		return nil, err
	}
	return runtimeexecutorlocal.NewCloudHostSecurityExecutor(identity, runtimeexecutorlocal.LocalTargetBinding{
		SiteRef: target.SiteRefs[0], NodeRef: target.NodeRefs[0], ExecutionChannelRef: target.ExecutionChannelRef,
	}, runtimeexecutorlocal.CloudHostSecurityAuthority{
		ProviderContractHash: target.ProviderContractHash,
		ModuleContractHash:   target.ModuleContractHash,
		HealthContractHash:   health[0].ContractHash,
	}, f.operations), nil
}

// NewProductCloudPublicEdgeRegistration binds only the Cloud node-local edge
// policy owner. DNS, certificate issuance, secrets, provider resources, and
// generic proxy commands are not part of its Operations capability.
func NewProductCloudPublicEdgeRegistration(runtimeVersion string, operations runtimeexecutorlocal.CloudPublicEdgeOperations) (ProductRuntimeOwnerRegistration, error) {
	if runtimeVersion == "" || runtimeVersion != strings.TrimSpace(runtimeVersion) || nilProductRuntimeOwnerValue(operations) {
		return ProductRuntimeOwnerRegistration{}, errors.New("Cloud public-edge product registration requires a runtime version and operations owner")
	}
	return ProductRuntimeOwnerRegistration{
		Selector: productCloudPublicEdgeSelector(),
		Factory:  &productCloudPublicEdgeFactory{runtimeVersion: runtimeVersion, operations: operations},
	}, nil
}

func (f *productCloudPublicEdgeFactory) PrepareRuntimeOwner(request ProductRuntimeOwnerRequest) (runtimeexecutor.Executor, error) {
	if f == nil || strings.TrimSpace(f.runtimeVersion) == "" || nilProductRuntimeOwnerValue(f.operations) {
		return nil, errors.New("Cloud public-edge product factory is not initialized")
	}
	target := cloneProductRuntimeTarget(request.Target)
	health := cloneProductHealthTargets(request.HealthTargets)
	if productRuntimeOwnerSelectorForTarget(target) != productCloudPublicEdgeSelector() ||
		len(target.SiteRefs) != 1 || len(target.NodeRefs) != 1 || strings.TrimSpace(target.ExecutionChannelRef) == "" ||
		len(health) != 1 || !productHealthTargetsRuntime(health[0], target) {
		return nil, errors.New("Cloud public-edge product factory requires one exact channel-bound target and health contract")
	}
	identity, err := productRuntimeOwnerAdapterIdentity(productCloudPublicEdgeAdapterID, f.runtimeVersion, target, health)
	if err != nil {
		return nil, err
	}
	return runtimeexecutorlocal.NewCloudPublicEdgeExecutor(identity, runtimeexecutorlocal.LocalTargetBinding{
		SiteRef: target.SiteRefs[0], NodeRef: target.NodeRefs[0], ExecutionChannelRef: target.ExecutionChannelRef,
	}, runtimeexecutorlocal.CloudPublicEdgeAuthority{
		ProviderContractHash: target.ProviderContractHash,
		ModuleContractHash:   target.ModuleContractHash,
		HealthContractHash:   health[0].ContractHash,
	}, f.operations), nil
}

func productCloudHostSecuritySelector() ProductRuntimeOwnerSelector {
	return ProductRuntimeOwnerSelector{
		OwnerKind: "module", OwnerRef: "stackkits-cloud-host-security-runtime",
		ProviderRef: "stackkits-cloud-host-security", ModuleRef: "stackkits-cloud-host-security-runtime", UnitRef: "executor-contract",
		RuntimeKind: "host", RuntimeDelivery: "stackkit",
	}
}

func productCloudPublicEdgeSelector() ProductRuntimeOwnerSelector {
	return ProductRuntimeOwnerSelector{
		OwnerKind: "module", OwnerRef: "stackkits-cloud-public-edge-runtime",
		ProviderRef: "stackkits-cloud-public-edge", ModuleRef: "stackkits-cloud-public-edge-runtime", UnitRef: "executor-contract",
		RuntimeKind: "host", RuntimeDelivery: "stackkit",
	}
}

var (
	_ ProductRuntimeOwnerFactory = (*productCloudHostSecurityFactory)(nil)
	_ ProductRuntimeOwnerFactory = (*productCloudPublicEdgeFactory)(nil)
)
