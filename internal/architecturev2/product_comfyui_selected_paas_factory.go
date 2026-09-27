package architecturev2

import "github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"

// NewProductComfyUISelectedPaaSRegistration binds the ComfyUI alternative of
// the ai-image-video workload to the shared selected application executor.
func NewProductComfyUISelectedPaaSRegistration(runtimeVersion, runtimeAdapterRef, runtimeAdapterModuleRef string, operations nativehost.SelectedPaaSWorkloadOperations) (ProductRuntimeOwnerRegistration, error) {
	return newProductApplicationSelectedPaaSRegistration(nativehost.SelectedPaaSApplicationComfyUI, runtimeVersion, runtimeAdapterRef, runtimeAdapterModuleRef, operations)
}
