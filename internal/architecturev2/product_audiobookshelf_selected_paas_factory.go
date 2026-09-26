package architecturev2

import "github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"

// NewProductAudiobookshelfSelectedPaaSRegistration binds the Audiobookshelf alternative of the
// media-audiobooks workload to the shared selected application executor.
func NewProductAudiobookshelfSelectedPaaSRegistration(runtimeVersion, runtimeAdapterRef, runtimeAdapterModuleRef string, operations nativehost.SelectedPaaSWorkloadOperations) (ProductRuntimeOwnerRegistration, error) {
	return newProductApplicationSelectedPaaSRegistration(nativehost.SelectedPaaSApplicationAudiobookshelf, runtimeVersion, runtimeAdapterRef, runtimeAdapterModuleRef, operations)
}
