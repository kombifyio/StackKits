package architecturev2

import "github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"

// NewProductAnythingLLMSelectedPaaSRegistration binds the AnythingLLM chat
// alternative of the ai workload to the shared selected application executor.
func NewProductAnythingLLMSelectedPaaSRegistration(runtimeVersion, runtimeAdapterRef, runtimeAdapterModuleRef string, operations nativehost.SelectedPaaSWorkloadOperations) (ProductRuntimeOwnerRegistration, error) {
	return newProductApplicationSelectedPaaSRegistration(nativehost.SelectedPaaSApplicationAnythingLLM, runtimeVersion, runtimeAdapterRef, runtimeAdapterModuleRef, operations)
}
