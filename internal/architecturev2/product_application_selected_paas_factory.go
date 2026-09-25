package architecturev2

import (
	"errors"
	"fmt"
	"strings"

	"github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
)

type productApplicationSelectedPaaSFactory struct {
	application             nativehost.SelectedPaaSApplication
	refs                    nativehost.SelectedPaaSApplicationRefs
	runtimeVersion          string
	runtimeAdapterRef       string
	runtimeAdapterModuleRef string
	operations              nativehost.SelectedPaaSWorkloadOperations
}

// NewProductJellyfinSelectedPaaSRegistration binds the Media Library workload
// to one explicitly selected application adapter implementation.
func NewProductJellyfinSelectedPaaSRegistration(
	runtimeVersion, runtimeAdapterRef, runtimeAdapterModuleRef string,
	operations nativehost.SelectedPaaSWorkloadOperations,
) (ProductRuntimeOwnerRegistration, error) {
	return newProductApplicationSelectedPaaSRegistration(
		nativehost.SelectedPaaSApplicationJellyfin, runtimeVersion, runtimeAdapterRef, runtimeAdapterModuleRef, operations,
	)
}

// NewProductPterodactylSelectedPaaSRegistration binds the Game workload to the
// existing standalone application adapter and lifecycle owner (ADR-0043).
func NewProductPterodactylSelectedPaaSRegistration(
	runtimeVersion, runtimeAdapterRef, runtimeAdapterModuleRef string,
	operations nativehost.SelectedPaaSWorkloadOperations,
) (ProductRuntimeOwnerRegistration, error) {
	return newProductApplicationSelectedPaaSRegistration(
		nativehost.SelectedPaaSApplicationPterodactyl, runtimeVersion, runtimeAdapterRef, runtimeAdapterModuleRef, operations,
	)
}

// NewProductRoundcubeSelectedPaaSRegistration binds the client-first Mail
// workload to the existing standalone application adapter and lifecycle owner.
func NewProductRoundcubeSelectedPaaSRegistration(
	runtimeVersion, runtimeAdapterRef, runtimeAdapterModuleRef string,
	operations nativehost.SelectedPaaSWorkloadOperations,
) (ProductRuntimeOwnerRegistration, error) {
	return newProductApplicationSelectedPaaSRegistration(
		nativehost.SelectedPaaSApplicationRoundcube, runtimeVersion, runtimeAdapterRef, runtimeAdapterModuleRef, operations,
	)
}

// NewProductStalwartSelectedPaaSRegistration binds the own mail server
// workload (ADR-0046) to the existing standalone application adapter and
// lifecycle owner.
func NewProductStalwartSelectedPaaSRegistration(
	runtimeVersion, runtimeAdapterRef, runtimeAdapterModuleRef string,
	operations nativehost.SelectedPaaSWorkloadOperations,
) (ProductRuntimeOwnerRegistration, error) {
	return newProductApplicationSelectedPaaSRegistration(
		nativehost.SelectedPaaSApplicationStalwart, runtimeVersion, runtimeAdapterRef, runtimeAdapterModuleRef, operations,
	)
}

// NewProductPaperlessSelectedPaaSRegistration binds the Documents workload to
// the existing standalone application adapter and lifecycle owner.
func NewProductPaperlessSelectedPaaSRegistration(
	runtimeVersion, runtimeAdapterRef, runtimeAdapterModuleRef string,
	operations nativehost.SelectedPaaSWorkloadOperations,
) (ProductRuntimeOwnerRegistration, error) {
	return newProductApplicationSelectedPaaSRegistration(
		nativehost.SelectedPaaSApplicationPaperless, runtimeVersion, runtimeAdapterRef, runtimeAdapterModuleRef, operations,
	)
}

// NewProductHomeAssistantSelectedPaaSRegistration binds the self-hosted Home
// Assistant container workload to one explicitly selected application
// adapter implementation. External HAOS or imported instances are not
// container workloads and never match this selector.
func NewProductHomeAssistantSelectedPaaSRegistration(
	runtimeVersion, runtimeAdapterRef, runtimeAdapterModuleRef string,
	operations nativehost.SelectedPaaSWorkloadOperations,
) (ProductRuntimeOwnerRegistration, error) {
	return newProductApplicationSelectedPaaSRegistration(
		nativehost.SelectedPaaSApplicationHomeAssistant, runtimeVersion, runtimeAdapterRef, runtimeAdapterModuleRef, operations,
	)
}

func newProductApplicationSelectedPaaSRegistration(
	application nativehost.SelectedPaaSApplication,
	runtimeVersion, runtimeAdapterRef, runtimeAdapterModuleRef string,
	operations nativehost.SelectedPaaSWorkloadOperations,
) (ProductRuntimeOwnerRegistration, error) {
	refs, known := application.Refs()
	if !known {
		return ProductRuntimeOwnerRegistration{}, fmt.Errorf("selected-PaaS application %q is not a closed StackKits workload", application)
	}
	if runtimeVersion == "" || runtimeVersion != strings.TrimSpace(runtimeVersion) ||
		runtimeAdapterRef == "" || runtimeAdapterRef != strings.TrimSpace(runtimeAdapterRef) ||
		runtimeAdapterModuleRef == "" || runtimeAdapterModuleRef != strings.TrimSpace(runtimeAdapterModuleRef) ||
		nilProductRuntimeOwnerValue(operations) {
		return ProductRuntimeOwnerRegistration{}, fmt.Errorf("%s selected-PaaS product registration requires runtime version, exact adapter identity, and operations owner", refs.Name)
	}
	factory := &productApplicationSelectedPaaSFactory{
		application: application, refs: refs, runtimeVersion: runtimeVersion,
		runtimeAdapterRef: runtimeAdapterRef, runtimeAdapterModuleRef: runtimeAdapterModuleRef, operations: operations,
	}
	return ProductRuntimeOwnerRegistration{Selector: factory.selector(), Factory: factory}, nil
}

func (f *productApplicationSelectedPaaSFactory) PrepareRuntimeOwner(request ProductRuntimeOwnerRequest) (runtimeexecutor.Executor, error) {
	if f == nil || strings.TrimSpace(f.refs.Name) == "" || strings.TrimSpace(f.runtimeVersion) == "" ||
		strings.TrimSpace(f.runtimeAdapterRef) == "" || strings.TrimSpace(f.runtimeAdapterModuleRef) == "" ||
		nilProductRuntimeOwnerValue(f.operations) {
		return nil, errors.New("selected-PaaS application product factory is not initialized")
	}
	target := cloneProductRuntimeTarget(request.Target)
	health := cloneProductHealthTargets(request.HealthTargets)
	if productRuntimeOwnerSelectorForTarget(target) != f.selector() || target.RuntimeAdapter == nil ||
		len(target.SiteRefs) != 1 || len(target.NodeRefs) != 1 ||
		strings.TrimSpace(target.ExecutionChannelRef) == "" || len(health) == 0 {
		return nil, fmt.Errorf("%s selected-PaaS product factory requires one exact channel-bound workload, adapter, and health contract", f.refs.Name)
	}
	moduleHealthIndex := -1
	for index, requirement := range health {
		if !productHealthTargetsRuntime(requirement, target) {
			return nil, fmt.Errorf("%s selected-PaaS product factory received Health outside its exact runtime authority", f.refs.Name)
		}
		if requirement.TargetKind == "module" {
			if moduleHealthIndex >= 0 {
				return nil, fmt.Errorf("%s selected-PaaS product factory received more than one module Health contract", f.refs.Name)
			}
			moduleHealthIndex = index
		}
	}
	if moduleHealthIndex < 0 {
		return nil, fmt.Errorf("%s selected-PaaS product factory requires its exact module Health contract", f.refs.Name)
	}
	identity, err := productRuntimeOwnerAdapterIdentity("stackkits-"+string(f.application)+"-selected-paas", f.runtimeVersion, target, health)
	if err != nil {
		return nil, err
	}
	authority := nativehost.SelectedPaaSWorkloadAuthority{
		ProviderContractHash: target.ProviderContractHash,
		ModuleContractHash:   target.ModuleContractHash,
		UnitContractHash:     target.UnitContractHash,
		HealthContractHash:   health[moduleHealthIndex].ContractHash,
		RuntimeAdapter:       selectedPaaSRuntimeAdapterAuthority(*target.RuntimeAdapter),
	}
	return nativehost.NewSelectedPaaSApplicationExecutor(f.application, identity, nativehost.LocalTargetBinding{
		SiteRef: target.SiteRefs[0], NodeRef: target.NodeRefs[0], ExecutionChannelRef: target.ExecutionChannelRef,
	}, authority, f.operations), nil
}

func (f *productApplicationSelectedPaaSFactory) selector() ProductRuntimeOwnerSelector {
	return ProductRuntimeOwnerSelector{
		OwnerKind: "module", OwnerRef: f.refs.ModuleRef,
		ProviderRef: f.refs.ProviderRef, ModuleRef: f.refs.ModuleRef, UnitRef: f.refs.UnitRef,
		RuntimeKind: "container", RuntimeDelivery: sharedApplicationAdapterRuntimeDelivery, RuntimeEngine: "docker", WorkloadRef: f.refs.WorkloadRef,
		RuntimeAdapterRef: f.runtimeAdapterRef, RuntimeAdapterModuleRef: f.runtimeAdapterModuleRef,
	}
}

var _ ProductRuntimeOwnerFactory = (*productApplicationSelectedPaaSFactory)(nil)
