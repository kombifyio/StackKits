package architecturev2

import (
	"errors"
	"strings"

	"github.com/kombifyio/stackkits/contracts/runtimeexecutor"
	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"
)

type productDokployBootstrapFactory struct {
	runtimeVersion string
	operations     nativehost.DokployBootstrapOperations
}

func NewProductDokployBootstrapRegistration(version string, operations nativehost.DokployBootstrapOperations) (ProductRuntimeOwnerRegistration, error) {
	if version == "" || version != strings.TrimSpace(version) || nilProductRuntimeOwnerValue(operations) {
		return ProductRuntimeOwnerRegistration{}, errors.New("Dokploy bootstrap registration requires its local operations owner and runtime version")
	}
	return ProductRuntimeOwnerRegistration{Selector: productDokployBootstrapSelector(), Factory: &productDokployBootstrapFactory{version, operations}}, nil
}

func (f *productDokployBootstrapFactory) PrepareRuntimeOwner(request ProductRuntimeOwnerRequest) (runtimeexecutor.Executor, error) {
	if f == nil || f.runtimeVersion == "" || nilProductRuntimeOwnerValue(f.operations) {
		return nil, errors.New("Dokploy bootstrap factory is not initialized")
	}
	target, health := cloneProductRuntimeTarget(request.Target), cloneProductHealthTargets(request.HealthTargets)
	if productRuntimeOwnerSelectorForTarget(target) != productDokployBootstrapSelector() || len(target.SiteRefs) != 1 || len(target.NodeRefs) != 1 ||
		target.ExecutionChannelRef == "" || len(health) != 1 || !productHealthTargetsRuntime(health[0], target) {
		return nil, errors.New("Dokploy bootstrap requires one selected node-local runtime and health contract")
	}
	identity, err := productRuntimeOwnerAdapterIdentity("stackkits-dokploy-bootstrap-local", f.runtimeVersion, target, health)
	if err != nil {
		return nil, err
	}
	return nativehost.NewDokployBootstrapExecutor(identity, nativehost.LocalTargetBinding{
		SiteRef: target.SiteRefs[0], NodeRef: target.NodeRefs[0], ExecutionChannelRef: target.ExecutionChannelRef,
	}, f.operations), nil
}

func productDokployBootstrapSelector() ProductRuntimeOwnerSelector {
	return ProductRuntimeOwnerSelector{
		OwnerKind: "module", OwnerRef: architecturev2renderer.DokployBootstrapModuleRef,
		ModuleRef: architecturev2renderer.DokployBootstrapModuleRef, UnitRef: architecturev2renderer.DokployBootstrapUnitRef,
		ProviderRef: "stackkits-dokploy", RuntimeKind: "host", RuntimeDelivery: "stackkit",
	}
}

var _ ProductRuntimeOwnerFactory = (*productDokployBootstrapFactory)(nil)
