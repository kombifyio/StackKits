package architecturev2

import "github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"

// NewProductPaperclipSelectedPaaSRegistration binds the Paperclip alternative
// of the ai-control-plane workload to the shared selected application executor.
func NewProductPaperclipSelectedPaaSRegistration(runtimeVersion, runtimeAdapterRef, runtimeAdapterModuleRef string, operations nativehost.SelectedPaaSWorkloadOperations) (ProductRuntimeOwnerRegistration, error) {
	return newProductApplicationSelectedPaaSRegistration(nativehost.SelectedPaaSApplicationPaperclip, runtimeVersion, runtimeAdapterRef, runtimeAdapterModuleRef, operations)
}
