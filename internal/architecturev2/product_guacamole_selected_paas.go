package architecturev2

import "github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"

// NewProductGuacamoleSelectedPaaSRegistration binds Remote to the existing
// selected application owner, without provider or cross-server authority.
func NewProductGuacamoleSelectedPaaSRegistration(runtimeVersion, runtimeAdapterRef, runtimeAdapterModuleRef string, operations nativehost.SelectedPaaSWorkloadOperations) (ProductRuntimeOwnerRegistration, error) {
	return newProductApplicationSelectedPaaSRegistration(nativehost.SelectedPaaSApplicationGuacamole, runtimeVersion, runtimeAdapterRef, runtimeAdapterModuleRef, operations)
}
