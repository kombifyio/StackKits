package architecturev2

import "github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"

// NewProductEntePhotosSelectedPaaSRegistration binds the Ente Photos
// alternative of the photos workload to the shared selected application executor.
func NewProductEntePhotosSelectedPaaSRegistration(runtimeVersion, runtimeAdapterRef, runtimeAdapterModuleRef string, operations nativehost.SelectedPaaSWorkloadOperations) (ProductRuntimeOwnerRegistration, error) {
	return newProductApplicationSelectedPaaSRegistration(nativehost.SelectedPaaSApplicationEntePhotos, runtimeVersion, runtimeAdapterRef, runtimeAdapterModuleRef, operations)
}
