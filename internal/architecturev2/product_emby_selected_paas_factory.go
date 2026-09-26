package architecturev2

import "github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"

// NewProductEmbySelectedPaaSRegistration binds the Emby alternative of the
// media workload to the shared selected application executor.
func NewProductEmbySelectedPaaSRegistration(runtimeVersion, runtimeAdapterRef, runtimeAdapterModuleRef string, operations nativehost.SelectedPaaSWorkloadOperations) (ProductRuntimeOwnerRegistration, error) {
	return newProductApplicationSelectedPaaSRegistration(nativehost.SelectedPaaSApplicationEmby, runtimeVersion, runtimeAdapterRef, runtimeAdapterModuleRef, operations)
}
