package architecturev2

import "github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"

// NewProductNavidromeSelectedPaaSRegistration binds the Navidrome alternative of the
// media-music workload to the shared selected application executor.
func NewProductNavidromeSelectedPaaSRegistration(runtimeVersion, runtimeAdapterRef, runtimeAdapterModuleRef string, operations nativehost.SelectedPaaSWorkloadOperations) (ProductRuntimeOwnerRegistration, error) {
	return newProductApplicationSelectedPaaSRegistration(nativehost.SelectedPaaSApplicationNavidrome, runtimeVersion, runtimeAdapterRef, runtimeAdapterModuleRef, operations)
}
