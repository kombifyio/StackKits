package architecturev2

import (
	"errors"
	"fmt"
	"strings"

	"github.com/kombifyio/stackkits/internal/runtimeexecutorlocal"
	"github.com/kombifyio/stackkits/internal/runtimeexecutoropentofu"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
)

const productOpenTofuAdapterID = "stackkits-opentofu-local"

type productOpenTofuFactory struct {
	runtimeVersion string
	module         runtimeexecutoropentofu.ModuleBinding
	runtime        runtimeexecutoropentofu.Runtime
	selector       ProductRuntimeOwnerSelector
}

// NewProductOpenTofuRegistrations binds the OpenTofu unit of every listed
// module to the standard OpenTofu executor. Selectors are exact per module
// because the registry matches whole selectors; the native Compose
// registrations keep the compose unit of the same modules (S-F fallback).
func NewProductOpenTofuRegistrations(runtimeVersion string, runtime runtimeexecutoropentofu.Runtime, modules []runtimeexecutoropentofu.ModuleBinding) ([]ProductRuntimeOwnerRegistration, error) {
	if runtimeVersion == "" || runtimeVersion != strings.TrimSpace(runtimeVersion) ||
		strings.TrimSpace(runtime.WorkspaceRoot) == "" || nilProductRuntimeOwnerValue(runtime.Native) || len(modules) == 0 {
		return nil, errors.New("OpenTofu registrations require a runtime version, workspace, native Verify owner, and modules")
	}
	registrations := make([]ProductRuntimeOwnerRegistration, 0, 2*len(modules))
	for _, module := range modules {
		if err := module.Validate(); err != nil {
			return nil, err
		}
		// The opentofu unit (target opentofu) and the terramate unit (target
		// terramate: the same root plus its stack file) of one Core module.
		for _, unitRef := range []string{runtimeexecutoropentofu.UnitRef, runtimeexecutoropentofu.TerramateUnitRef} {
			selector := productOpenTofuSelector(module, unitRef)
			registrations = append(registrations, ProductRuntimeOwnerRegistration{
				Selector: selector,
				Factory: &productOpenTofuFactory{
					runtimeVersion: runtimeVersion, module: module, runtime: runtime, selector: selector,
				},
			})
		}
	}
	return registrations, nil
}

// WithProductOpenTofuContractRoot gives one local native owner registration
// (Cloud public edge, federation link, bridge origin mTLS) its OpenTofu
// contract root under the opentofu and terramate targets. The selector,
// execution mode, and compensation stay the native registration's; under
// the compose target the prepared executor is the native one.
func WithProductOpenTofuContractRoot(registration ProductRuntimeOwnerRegistration, runtime runtimeexecutoropentofu.Runtime) (ProductRuntimeOwnerRegistration, error) {
	if nilProductRuntimeOwnerFactory(registration.Factory) || registration.Execution == ProductRuntimeOwnerExecutionRemoteOnly ||
		strings.TrimSpace(runtime.WorkspaceRoot) == "" {
		return ProductRuntimeOwnerRegistration{}, errors.New("an OpenTofu contract root requires a local native owner registration and a workspace")
	}
	registration.Factory = &productOpenTofuContractRootFactory{native: registration.Factory, runtime: runtime}
	return registration, nil
}

type productOpenTofuContractRootFactory struct {
	native  ProductRuntimeOwnerFactory
	runtime runtimeexecutoropentofu.Runtime
}

func (f *productOpenTofuContractRootFactory) PrepareRuntimeOwner(request ProductRuntimeOwnerRequest) (runtimeexecutor.Executor, error) {
	if f == nil || nilProductRuntimeOwnerFactory(f.native) {
		return nil, errors.New("OpenTofu contract root factory is not initialized")
	}
	native, err := f.native.PrepareRuntimeOwner(request)
	if err != nil {
		return nil, err
	}
	return runtimeexecutoropentofu.NewContractRootExecutor(native, f.runtime), nil
}

func (f *productOpenTofuFactory) PrepareRuntimeOwner(request ProductRuntimeOwnerRequest) (runtimeexecutor.Executor, error) {
	if f == nil || strings.TrimSpace(f.runtimeVersion) == "" || nilProductRuntimeOwnerValue(f.runtime.Native) {
		return nil, errors.New("OpenTofu product factory is not initialized")
	}
	target := cloneProductRuntimeTarget(request.Target)
	health := cloneProductHealthTargets(request.HealthTargets)
	if productRuntimeOwnerSelectorForTarget(target) != f.selector ||
		len(target.SiteRefs) != 1 || len(target.NodeRefs) != 1 ||
		strings.TrimSpace(target.ExecutionChannelRef) == "" || len(health) == 0 {
		return nil, fmt.Errorf("OpenTofu product factory requires one exact channel-bound %s target with postconditions", f.module.ModuleRef)
	}
	healthHashes := make(map[string]string, len(health))
	for _, item := range health {
		if !productHealthTargetsRuntime(item, target) || item.SourceRef == "" || item.ContractHash == "" {
			return nil, errors.New("OpenTofu health authority does not target the exact runtime")
		}
		if _, duplicate := healthHashes[item.SourceRef]; duplicate {
			return nil, errors.New("OpenTofu health authority contains a duplicate source")
		}
		healthHashes[item.SourceRef] = item.ContractHash
	}
	identity, err := productRuntimeOwnerAdapterIdentity(productOpenTofuAdapterID, f.runtimeVersion, target, health)
	if err != nil {
		return nil, err
	}
	return runtimeexecutoropentofu.NewExecutor(identity, runtimeexecutorlocal.LocalTargetBinding{
		SiteRef: target.SiteRefs[0], NodeRef: target.NodeRefs[0],
		ExecutionChannelRef: target.ExecutionChannelRef,
	}, runtimeexecutoropentofu.Authority{
		ProviderContractHash: target.ProviderContractHash,
		ModuleContractHash:   target.ModuleContractHash,
		HealthContractHashes: healthHashes,
	}, f.module, f.runtime), nil
}

func productOpenTofuSelector(module runtimeexecutoropentofu.ModuleBinding, unitRef string) ProductRuntimeOwnerSelector {
	return ProductRuntimeOwnerSelector{
		OwnerKind: "module", OwnerRef: module.ModuleRef,
		ProviderRef: module.ProviderRef, ModuleRef: module.ModuleRef,
		UnitRef: unitRef, RuntimeKind: "container", RuntimeDelivery: "stackkit",
		RuntimeEngine: "docker", WorkloadRef: module.WorkloadRef,
	}
}

var (
	_ ProductRuntimeOwnerFactory = (*productOpenTofuFactory)(nil)
	_ ProductRuntimeOwnerFactory = (*productOpenTofuContractRootFactory)(nil)
)
