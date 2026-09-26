package architecturev2

import "github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"

// NewProductPassboltSelectedPaaSRegistration binds the Passbolt alternative of the
// vault workload to the shared selected application executor.
func NewProductPassboltSelectedPaaSRegistration(runtimeVersion, runtimeAdapterRef, runtimeAdapterModuleRef string, operations nativehost.SelectedPaaSWorkloadOperations) (ProductRuntimeOwnerRegistration, error) {
	return newProductApplicationSelectedPaaSRegistration(nativehost.SelectedPaaSApplicationPassbolt, runtimeVersion, runtimeAdapterRef, runtimeAdapterModuleRef, operations)
}
