package architecturev2

import (
	"errors"
	"strings"

	"github.com/kombifyio/stackkits/internal/runtimeexecutorlocal"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
)

const (
	productHomeAccessAdapterID    = "stackkits-home-access-policy-local"
	productLocalAutonomyAdapterID = "stackkits-local-autonomy-policy-local"
)

type productHomeAccessFactory struct {
	runtimeVersion string
	operations     runtimeexecutorlocal.HomeAccessPolicyOperations
}

type productLocalAutonomyFactory struct {
	runtimeVersion string
	operations     runtimeexecutorlocal.LocalAutonomyPolicyOperations
}

// NewProductHomeAccessRegistration binds the exact node-local Home access
// selector to construction-owned enforcement operations. Discovery, endpoints,
// credentials, transports, and provider lifecycle remain outside this factory.
func NewProductHomeAccessRegistration(runtimeVersion string, operations runtimeexecutorlocal.HomeAccessPolicyOperations) (ProductRuntimeOwnerRegistration, error) {
	if runtimeVersion == "" || runtimeVersion != strings.TrimSpace(runtimeVersion) || nilProductRuntimeOwnerValue(operations) {
		return ProductRuntimeOwnerRegistration{}, errors.New("Home access product registration requires a runtime version and operations owner")
	}
	return ProductRuntimeOwnerRegistration{
		Selector: productHomeAccessSelector(),
		Factory:  &productHomeAccessFactory{runtimeVersion: runtimeVersion, operations: operations},
	}, nil
}

func (f *productHomeAccessFactory) PrepareRuntimeOwner(request ProductRuntimeOwnerRequest) (runtimeexecutor.Executor, error) {
	if f == nil || strings.TrimSpace(f.runtimeVersion) == "" || nilProductRuntimeOwnerValue(f.operations) {
		return nil, errors.New("Home access product factory is not initialized")
	}
	target := cloneProductRuntimeTarget(request.Target)
	health := cloneProductHealthTargets(request.HealthTargets)
	if productRuntimeOwnerSelectorForTarget(target) != productHomeAccessSelector() || len(target.SiteRefs) != 1 || len(target.NodeRefs) != 1 ||
		strings.TrimSpace(target.ExecutionChannelRef) == "" || len(health) != 1 || !productHealthTargetsRuntime(health[0], target) {
		return nil, errors.New("Home access product factory requires one exact channel-bound target and health contract")
	}
	identity, err := productRuntimeOwnerAdapterIdentity(productHomeAccessAdapterID, f.runtimeVersion, target, health)
	if err != nil {
		return nil, err
	}
	return runtimeexecutorlocal.NewHomeAccessPolicyExecutor(identity, runtimeexecutorlocal.HomeAccessPolicyBinding{
		SiteRefs: target.SiteRefs, NodeRefs: target.NodeRefs, ExecutionChannelRef: target.ExecutionChannelRef,
	}, runtimeexecutorlocal.HomeAccessPolicyAuthority{
		ProviderContractHash: target.ProviderContractHash, ModuleContractHash: target.ModuleContractHash, HealthContractHash: health[0].ContractHash,
	}, f.operations), nil
}

// NewProductLocalAutonomyRegistration binds the exact Home control-authority
// node to construction-owned offline-autonomy enforcement operations.
func NewProductLocalAutonomyRegistration(runtimeVersion string, operations runtimeexecutorlocal.LocalAutonomyPolicyOperations) (ProductRuntimeOwnerRegistration, error) {
	if runtimeVersion == "" || runtimeVersion != strings.TrimSpace(runtimeVersion) || nilProductRuntimeOwnerValue(operations) {
		return ProductRuntimeOwnerRegistration{}, errors.New("local-autonomy product registration requires a runtime version and operations owner")
	}
	return ProductRuntimeOwnerRegistration{
		Selector: productLocalAutonomySelector(),
		Factory:  &productLocalAutonomyFactory{runtimeVersion: runtimeVersion, operations: operations},
	}, nil
}

func (f *productLocalAutonomyFactory) PrepareRuntimeOwner(request ProductRuntimeOwnerRequest) (runtimeexecutor.Executor, error) {
	if f == nil || strings.TrimSpace(f.runtimeVersion) == "" || nilProductRuntimeOwnerValue(f.operations) {
		return nil, errors.New("local-autonomy product factory is not initialized")
	}
	target := cloneProductRuntimeTarget(request.Target)
	health := cloneProductHealthTargets(request.HealthTargets)
	if productRuntimeOwnerSelectorForTarget(target) != productLocalAutonomySelector() || len(target.SiteRefs) != 1 || len(target.NodeRefs) != 1 ||
		strings.TrimSpace(target.ExecutionChannelRef) == "" || len(health) != 1 || !productHealthTargetsRuntime(health[0], target) {
		return nil, errors.New("local-autonomy product factory requires one exact channel-bound target and health contract")
	}
	identity, err := productRuntimeOwnerAdapterIdentity(productLocalAutonomyAdapterID, f.runtimeVersion, target, health)
	if err != nil {
		return nil, err
	}
	return runtimeexecutorlocal.NewLocalAutonomyPolicyExecutor(identity, runtimeexecutorlocal.LocalAutonomyPolicyBinding{
		HomeSiteRefs: target.SiteRefs, NodeRefs: target.NodeRefs, ExecutionChannelRef: target.ExecutionChannelRef,
	}, runtimeexecutorlocal.LocalAutonomyPolicyAuthority{
		ProviderContractHash: target.ProviderContractHash, ModuleContractHash: target.ModuleContractHash, HealthContractHash: health[0].ContractHash,
	}, f.operations), nil
}

func productHomeAccessSelector() ProductRuntimeOwnerSelector {
	return ProductRuntimeOwnerSelector{
		OwnerKind: "module", OwnerRef: "stackkits-home-access-policy-manifest",
		ProviderRef: "stackkits-home-access-policy", ModuleRef: "stackkits-home-access-policy-manifest", UnitRef: "policy-bundle",
		RuntimeKind: "native", RuntimeDelivery: "stackkit",
	}
}

func productLocalAutonomySelector() ProductRuntimeOwnerSelector {
	return ProductRuntimeOwnerSelector{
		OwnerKind: "module", OwnerRef: "stackkits-local-autonomy-policy-manifest",
		ProviderRef: "stackkits-local-autonomy-policy", ModuleRef: "stackkits-local-autonomy-policy-manifest", UnitRef: "policy-bundle",
		RuntimeKind: "native", RuntimeDelivery: "stackkit",
	}
}

var (
	_ ProductRuntimeOwnerFactory = (*productHomeAccessFactory)(nil)
	_ ProductRuntimeOwnerFactory = (*productLocalAutonomyFactory)(nil)
)
