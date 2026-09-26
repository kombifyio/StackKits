package architecturev2

import "github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"

// NewProductEuroofficeSelectedPaaSRegistration binds the Eurooffice alternative of the
// files-office workload to the shared selected application executor.
func NewProductEuroofficeSelectedPaaSRegistration(runtimeVersion, runtimeAdapterRef, runtimeAdapterModuleRef string, operations nativehost.SelectedPaaSWorkloadOperations) (ProductRuntimeOwnerRegistration, error) {
	return newProductApplicationSelectedPaaSRegistration(nativehost.SelectedPaaSApplicationEurooffice, runtimeVersion, runtimeAdapterRef, runtimeAdapterModuleRef, operations)
}
