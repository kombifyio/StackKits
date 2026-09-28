package architecturev2

import "github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"

// NewProductSpeechKitSelectedPaaSRegistration binds the speechkit alternative
// of the ai-speech workload to the shared selected application executor.
func NewProductSpeechKitSelectedPaaSRegistration(runtimeVersion, runtimeAdapterRef, runtimeAdapterModuleRef string, operations nativehost.SelectedPaaSWorkloadOperations) (ProductRuntimeOwnerRegistration, error) {
	return newProductApplicationSelectedPaaSRegistration(nativehost.SelectedPaaSApplicationSpeechKit, runtimeVersion, runtimeAdapterRef, runtimeAdapterModuleRef, operations)
}
