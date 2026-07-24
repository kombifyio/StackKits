package architecturev2

import (
	"errors"
	"strings"

	"github.com/kombifyio/stackkits/internal/runtimeexecutorlocal"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
)

const productInternalPKIAdapterID = "stackkits-internal-pki-local"

type productInternalPKIFactory struct {
	runtimeVersion string
	root           runtimeexecutorlocal.InternalPKIRootOperations
	leaf           runtimeexecutorlocal.InternalPKILeafOperations
	trust          runtimeexecutorlocal.InternalPKITrustOperations
	verify         runtimeexecutorlocal.InternalPKIVerifyOperations
}

// NewProductInternalPKIRegistration binds the single authority-node policy to
// four separately constructed operations owners. Certificate/key material and
// authenticated transport remain outside StackKits.
func NewProductInternalPKIRegistration(
	runtimeVersion string,
	root runtimeexecutorlocal.InternalPKIRootOperations,
	leaf runtimeexecutorlocal.InternalPKILeafOperations,
	trust runtimeexecutorlocal.InternalPKITrustOperations,
	verify runtimeexecutorlocal.InternalPKIVerifyOperations,
) (ProductRuntimeOwnerRegistration, error) {
	if runtimeVersion == "" || runtimeVersion != strings.TrimSpace(runtimeVersion) ||
		nilProductRuntimeOwnerValue(root) || nilProductRuntimeOwnerValue(leaf) ||
		nilProductRuntimeOwnerValue(trust) || nilProductRuntimeOwnerValue(verify) {
		return ProductRuntimeOwnerRegistration{}, errors.New("internal PKI product registration requires a runtime version and four separated operations owners")
	}
	return ProductRuntimeOwnerRegistration{
		Selector: productInternalPKISelector(),
		Factory: &productInternalPKIFactory{
			runtimeVersion: runtimeVersion, root: root, leaf: leaf, trust: trust, verify: verify,
		},
	}, nil
}

func (f *productInternalPKIFactory) PrepareRuntimeOwner(request ProductRuntimeOwnerRequest) (runtimeexecutor.Executor, error) {
	if f == nil || strings.TrimSpace(f.runtimeVersion) == "" ||
		nilProductRuntimeOwnerValue(f.root) || nilProductRuntimeOwnerValue(f.leaf) ||
		nilProductRuntimeOwnerValue(f.trust) || nilProductRuntimeOwnerValue(f.verify) {
		return nil, errors.New("internal PKI product factory is not initialized")
	}
	target := cloneProductRuntimeTarget(request.Target)
	health := cloneProductHealthTargets(request.HealthTargets)
	if productRuntimeOwnerSelectorForTarget(target) != productInternalPKISelector() ||
		len(target.SiteRefs) != 1 || len(target.NodeRefs) != 1 ||
		strings.TrimSpace(target.ExecutionChannelRef) == "" ||
		len(health) != 1 || !productHealthTargetsRuntime(health[0], target) {
		return nil, errors.New("internal PKI product factory requires one exact authority-node target and renewal health contract")
	}
	identity, err := productRuntimeOwnerAdapterIdentity(productInternalPKIAdapterID, f.runtimeVersion, target, health)
	if err != nil {
		return nil, err
	}
	return runtimeexecutorlocal.NewInternalPKIExecutor(
		identity,
		runtimeexecutorlocal.LocalTargetBinding{
			SiteRef: target.SiteRefs[0], NodeRef: target.NodeRefs[0], ExecutionChannelRef: target.ExecutionChannelRef,
		},
		runtimeexecutorlocal.InternalPKIAuthority{
			ProviderContractHash: target.ProviderContractHash,
			ModuleContractHash:   target.ModuleContractHash,
			HealthContractHash:   health[0].ContractHash,
		},
		f.root, f.leaf, f.trust, f.verify,
	), nil
}

func productInternalPKISelector() ProductRuntimeOwnerSelector {
	return ProductRuntimeOwnerSelector{
		OwnerKind: "module", OwnerRef: "stackkits-internal-pki-contract",
		ProviderRef: "stackkits-internal-pki", ModuleRef: "stackkits-internal-pki-contract",
		UnitRef: "executor-contract", RuntimeKind: "native", RuntimeDelivery: "stackkit",
	}
}

var _ ProductRuntimeOwnerFactory = (*productInternalPKIFactory)(nil)
