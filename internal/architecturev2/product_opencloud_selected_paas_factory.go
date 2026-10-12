package architecturev2

import "github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"

// NewProductOpencloudSelectedPaaSRegistration binds Files to the existing
// node-local selected application owner; it grants no provider lifecycle.
func NewProductOpencloudSelectedPaaSRegistration(runtimeVersion, runtimeAdapterRef, runtimeAdapterModuleRef string, operations nativehost.SelectedPaaSWorkloadOperations) (ProductRuntimeOwnerRegistration, error) {
	return newProductApplicationSelectedPaaSRegistration(nativehost.SelectedPaaSApplicationOpencloud, runtimeVersion, runtimeAdapterRef, runtimeAdapterModuleRef, operations)
}
