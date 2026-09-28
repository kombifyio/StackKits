package architecturev2

import "github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"

// NewProductOpenHandsSelectedPaaSRegistration binds the OpenHands alternative
// of the ai-harness workload to the shared selected application executor.
func NewProductOpenHandsSelectedPaaSRegistration(runtimeVersion, runtimeAdapterRef, runtimeAdapterModuleRef string, operations nativehost.SelectedPaaSWorkloadOperations) (ProductRuntimeOwnerRegistration, error) {
	return newProductApplicationSelectedPaaSRegistration(nativehost.SelectedPaaSApplicationOpenHands, runtimeVersion, runtimeAdapterRef, runtimeAdapterModuleRef, operations)
}
