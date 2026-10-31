package architecturev2

import (
	"errors"
	"strings"

	"github.com/kombifyio/stackkits/contracts/runtimeexecutor"
	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"
)

type productKomodoBootstrapFactory struct {
	runtimeVersion string
	operations     nativehost.KomodoBootstrapOperations
}

func NewProductKomodoBootstrapRegistration(version string, operations nativehost.KomodoBootstrapOperations) (ProductRuntimeOwnerRegistration, error) {
	if version == "" || version != strings.TrimSpace(version) || nilProductRuntimeOwnerValue(operations) {
		return ProductRuntimeOwnerRegistration{}, errors.New("Komodo bootstrap registration requires its local operations owner and runtime version")
	}
	return ProductRuntimeOwnerRegistration{Selector: productKomodoBootstrapSelector(), Factory: &productKomodoBootstrapFactory{version, operations}}, nil
}

func (f *productKomodoBootstrapFactory) PrepareRuntimeOwner(request ProductRuntimeOwnerRequest) (runtimeexecutor.Executor, error) {
	if f == nil || f.runtimeVersion == "" || nilProductRuntimeOwnerValue(f.operations) {
		return nil, errors.New("Komodo bootstrap factory is not initialized")
	}
	target, health := cloneProductRuntimeTarget(request.Target), cloneProductHealthTargets(request.HealthTargets)
	if productRuntimeOwnerSelectorForTarget(target) != productKomodoBootstrapSelector() || len(target.SiteRefs) != 1 || len(target.NodeRefs) != 1 ||
		target.ExecutionChannelRef == "" || len(health) != 1 || !productHealthTargetsRuntime(health[0], target) {
		return nil, errors.New("Komodo bootstrap requires one selected node-local runtime and health contract")
	}
	identity, err := productRuntimeOwnerAdapterIdentity("stackkits-komodo-bootstrap-local", f.runtimeVersion, target, health)
	if err != nil {
		return nil, err
	}
	return nativehost.NewKomodoBootstrapExecutor(identity, nativehost.LocalTargetBinding{
		SiteRef: target.SiteRefs[0], NodeRef: target.NodeRefs[0], ExecutionChannelRef: target.ExecutionChannelRef,
	}, f.operations), nil
}

func productKomodoBootstrapSelector() ProductRuntimeOwnerSelector {
	return ProductRuntimeOwnerSelector{
		OwnerKind: "module", OwnerRef: architecturev2renderer.KomodoBootstrapModuleRef,
		ModuleRef: architecturev2renderer.KomodoBootstrapModuleRef, UnitRef: architecturev2renderer.KomodoBootstrapUnitRef,
		ProviderRef: "stackkits-komodo", RuntimeKind: "host", RuntimeDelivery: "stackkit",
	}
}

var _ ProductRuntimeOwnerFactory = (*productKomodoBootstrapFactory)(nil)
