package architecturev2

import "github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"

// NewProductImmichPowerToolsSelectedPaaSRegistration binds the ImmichPowerTools alternative of the
// photos-tools workload to the shared selected application executor.
func NewProductImmichPowerToolsSelectedPaaSRegistration(runtimeVersion, runtimeAdapterRef, runtimeAdapterModuleRef string, operations nativehost.SelectedPaaSWorkloadOperations) (ProductRuntimeOwnerRegistration, error) {
	return newProductApplicationSelectedPaaSRegistration(nativehost.SelectedPaaSApplicationImmichPowerTools, runtimeVersion, runtimeAdapterRef, runtimeAdapterModuleRef, operations)
}
