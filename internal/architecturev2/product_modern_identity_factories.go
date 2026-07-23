package architecturev2

import (
	"errors"
	"strings"

	"github.com/kombifyio/stackkits/internal/runtimeexecutorlocal"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
)

const (
	productModernHomeIdentityAdapterID  = "stackkits-modern-home-identity-trust-local"
	productModernCloudIdentityAdapterID = "stackkits-modern-cloud-identity-verifier-local"
)

type productModernHomeIdentityFactory struct {
	runtimeVersion string
	operations     runtimeexecutorlocal.ModernHomeIdentityTrustPolicyOperations
}

type productModernCloudIdentityFactory struct {
	runtimeVersion string
	operations     runtimeexecutorlocal.ModernCloudIdentityVerifierPolicyOperations
}

// NewProductModernHomeIdentityRegistration binds only the node-local Home
// authority target. Transport, endpoints, credentials and provider lifecycle
// remain outside StackKits and outside this factory.
func NewProductModernHomeIdentityRegistration(runtimeVersion string, operations runtimeexecutorlocal.ModernHomeIdentityTrustPolicyOperations) (ProductRuntimeOwnerRegistration, error) {
	if runtimeVersion == "" || runtimeVersion != strings.TrimSpace(runtimeVersion) || nilProductRuntimeOwnerValue(operations) {
		return ProductRuntimeOwnerRegistration{}, errors.New("Modern Home identity registration requires a runtime version and operations owner")
	}
	return ProductRuntimeOwnerRegistration{Selector: productModernHomeIdentitySelector(), Factory: &productModernHomeIdentityFactory{runtimeVersion: runtimeVersion, operations: operations}}, nil
}

func (f *productModernHomeIdentityFactory) PrepareRuntimeOwner(request ProductRuntimeOwnerRequest) (runtimeexecutor.Executor, error) {
	if f == nil || strings.TrimSpace(f.runtimeVersion) == "" || nilProductRuntimeOwnerValue(f.operations) {
		return nil, errors.New("Modern Home identity product factory is not initialized")
	}
	target, health := cloneProductRuntimeTarget(request.Target), cloneProductHealthTargets(request.HealthTargets)
	if productRuntimeOwnerSelectorForTarget(target) != productModernHomeIdentitySelector() || len(target.SiteRefs) != 1 || len(target.NodeRefs) != 1 || strings.TrimSpace(target.ExecutionChannelRef) == "" || len(health) != 1 || !productHealthTargetsRuntime(health[0], target) {
		return nil, errors.New("Modern Home identity factory requires one exact channel-bound target and health contract")
	}
	identity, err := productRuntimeOwnerAdapterIdentity(productModernHomeIdentityAdapterID, f.runtimeVersion, target, health)
	if err != nil {
		return nil, err
	}
	return runtimeexecutorlocal.NewModernHomeIdentityTrustPolicyExecutor(identity, runtimeexecutorlocal.ModernIdentitySitePolicyBinding{SiteRef: target.SiteRefs[0], NodeRef: target.NodeRefs[0], ExecutionChannelRef: target.ExecutionChannelRef}, runtimeexecutorlocal.ModernIdentityTrustPolicyAuthority{
		ProviderContractHash: target.ProviderContractHash, ModuleContractHash: target.ModuleContractHash, HealthContractHash: health[0].ContractHash,
	}, f.operations), nil
}

// NewProductModernCloudIdentityRegistration binds only the node-local Cloud
// verifier target. It cannot construct or obtain a Home-authority executor.
func NewProductModernCloudIdentityRegistration(runtimeVersion string, operations runtimeexecutorlocal.ModernCloudIdentityVerifierPolicyOperations) (ProductRuntimeOwnerRegistration, error) {
	if runtimeVersion == "" || runtimeVersion != strings.TrimSpace(runtimeVersion) || nilProductRuntimeOwnerValue(operations) {
		return ProductRuntimeOwnerRegistration{}, errors.New("Modern Cloud identity registration requires a runtime version and operations owner")
	}
	return ProductRuntimeOwnerRegistration{Selector: productModernCloudIdentitySelector(), Factory: &productModernCloudIdentityFactory{runtimeVersion: runtimeVersion, operations: operations}}, nil
}

func (f *productModernCloudIdentityFactory) PrepareRuntimeOwner(request ProductRuntimeOwnerRequest) (runtimeexecutor.Executor, error) {
	if f == nil || strings.TrimSpace(f.runtimeVersion) == "" || nilProductRuntimeOwnerValue(f.operations) {
		return nil, errors.New("Modern Cloud identity product factory is not initialized")
	}
	target, health := cloneProductRuntimeTarget(request.Target), cloneProductHealthTargets(request.HealthTargets)
	if productRuntimeOwnerSelectorForTarget(target) != productModernCloudIdentitySelector() || len(target.SiteRefs) != 1 || len(target.NodeRefs) != 1 || strings.TrimSpace(target.ExecutionChannelRef) == "" || len(health) != 1 || !productHealthTargetsRuntime(health[0], target) {
		return nil, errors.New("Modern Cloud identity factory requires one exact channel-bound target and health contract")
	}
	identity, err := productRuntimeOwnerAdapterIdentity(productModernCloudIdentityAdapterID, f.runtimeVersion, target, health)
	if err != nil {
		return nil, err
	}
	return runtimeexecutorlocal.NewModernCloudIdentityVerifierPolicyExecutor(identity, runtimeexecutorlocal.ModernIdentitySitePolicyBinding{SiteRef: target.SiteRefs[0], NodeRef: target.NodeRefs[0], ExecutionChannelRef: target.ExecutionChannelRef}, runtimeexecutorlocal.ModernIdentityTrustPolicyAuthority{
		ProviderContractHash: target.ProviderContractHash, ModuleContractHash: target.ModuleContractHash, HealthContractHash: health[0].ContractHash,
	}, f.operations), nil
}

func productModernHomeIdentitySelector() ProductRuntimeOwnerSelector {
	return ProductRuntimeOwnerSelector{OwnerKind: "module", OwnerRef: "stackkits-modern-home-identity-trust-policy-manifest", ProviderRef: "stackkits-modern-identity-trust-policy", ModuleRef: "stackkits-modern-home-identity-trust-policy-manifest", UnitRef: "policy-bundle", RuntimeKind: "native", RuntimeDelivery: "stackkit"}
}

func productModernCloudIdentitySelector() ProductRuntimeOwnerSelector {
	return ProductRuntimeOwnerSelector{OwnerKind: "module", OwnerRef: "stackkits-modern-cloud-identity-verifier-policy-manifest", ProviderRef: "stackkits-modern-identity-trust-policy", ModuleRef: "stackkits-modern-cloud-identity-verifier-policy-manifest", UnitRef: "policy-bundle", RuntimeKind: "native", RuntimeDelivery: "stackkit"}
}

var (
	_ ProductRuntimeOwnerFactory = (*productModernHomeIdentityFactory)(nil)
	_ ProductRuntimeOwnerFactory = (*productModernCloudIdentityFactory)(nil)
)
