package architecturev2

import (
	"errors"
	"strings"

	"github.com/kombifyio/stackkits/internal/runtimeexecutorlocal"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
)

const productSecurityBaselineAdapterID = "stackkits-security-baseline-local"

type productSecurityBaselineFactory struct {
	runtimeVersion string
	runner         runtimeexecutorlocal.CommandRunner
}

// NewProductSecurityBaselineRegistration binds the shared, node-local
// security baseline to the bounded OS command adapter. The adapter accepts
// only the exact generated CUE policy and has no provider, network, lease,
// credential, or workspace authority.
func NewProductSecurityBaselineRegistration(runtimeVersion string) (ProductRuntimeOwnerRegistration, error) {
	return newProductSecurityBaselineRegistration(runtimeVersion, nil)
}

func newProductSecurityBaselineRegistration(runtimeVersion string, runner runtimeexecutorlocal.CommandRunner) (ProductRuntimeOwnerRegistration, error) {
	if runtimeVersion == "" || runtimeVersion != strings.TrimSpace(runtimeVersion) {
		return ProductRuntimeOwnerRegistration{}, errors.New("security-baseline product registration requires a runtime version")
	}
	return ProductRuntimeOwnerRegistration{
		Selector: productSecurityBaselineSelector(),
		Factory:  &productSecurityBaselineFactory{runtimeVersion: runtimeVersion, runner: runner},
	}, nil
}

func (f *productSecurityBaselineFactory) PrepareRuntimeOwner(request ProductRuntimeOwnerRequest) (runtimeexecutor.Executor, error) {
	if f == nil || strings.TrimSpace(f.runtimeVersion) == "" {
		return nil, errors.New("security-baseline product factory is not initialized")
	}
	target := cloneProductRuntimeTarget(request.Target)
	health := cloneProductHealthTargets(request.HealthTargets)
	if productRuntimeOwnerSelectorForTarget(target) != productSecurityBaselineSelector() ||
		len(target.SiteRefs) != 1 || len(target.NodeRefs) != 1 || strings.TrimSpace(target.ExecutionChannelRef) == "" ||
		len(health) != 1 || !productHealthTargetsRuntime(health[0], target) {
		return nil, errors.New("security-baseline product factory requires one exact channel-bound target and health contract")
	}
	identity, err := productRuntimeOwnerAdapterIdentity(productSecurityBaselineAdapterID, f.runtimeVersion, target, health)
	if err != nil {
		return nil, err
	}
	return runtimeexecutorlocal.NewSecurityBaselineExecutor(identity, f.runner), nil
}

func productSecurityBaselineSelector() ProductRuntimeOwnerSelector {
	return ProductRuntimeOwnerSelector{
		OwnerKind: "module", OwnerRef: "security-baseline",
		ProviderRef: "stackkits-security-baseline", ModuleRef: "security-baseline", UnitRef: "host-policy",
		RuntimeKind: "host", RuntimeDelivery: "stackkit",
	}
}

var _ ProductRuntimeOwnerFactory = (*productSecurityBaselineFactory)(nil)
