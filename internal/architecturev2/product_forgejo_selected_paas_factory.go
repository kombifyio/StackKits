package architecturev2

import "github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"

// NewProductForgejoSelectedPaaSRegistration binds the Forgejo alternative of
// the private Git workload to the shared selected application executor.
func NewProductForgejoSelectedPaaSRegistration(runtimeVersion, runtimeAdapterRef, runtimeAdapterModuleRef string, operations nativehost.SelectedPaaSWorkloadOperations) (ProductRuntimeOwnerRegistration, error) {
	return newProductApplicationSelectedPaaSRegistration(nativehost.SelectedPaaSApplicationForgejo, runtimeVersion, runtimeAdapterRef, runtimeAdapterModuleRef, operations)
}
