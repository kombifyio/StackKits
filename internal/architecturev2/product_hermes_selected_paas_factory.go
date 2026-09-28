package architecturev2

import "github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"

// NewProductHermesSelectedPaaSRegistration binds the Hermes alternative of
// the ai-assistant workload to the shared selected application executor.
func NewProductHermesSelectedPaaSRegistration(runtimeVersion, runtimeAdapterRef, runtimeAdapterModuleRef string, operations nativehost.SelectedPaaSWorkloadOperations) (ProductRuntimeOwnerRegistration, error) {
	return newProductApplicationSelectedPaaSRegistration(nativehost.SelectedPaaSApplicationHermes, runtimeVersion, runtimeAdapterRef, runtimeAdapterModuleRef, operations)
}
