package architecturev2

import "github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"

// NewProductNextcloudSelectedPaaSRegistration binds the Nextcloud alternative of the
// files workload to the shared selected application executor.
func NewProductNextcloudSelectedPaaSRegistration(runtimeVersion, runtimeAdapterRef, runtimeAdapterModuleRef string, operations nativehost.SelectedPaaSWorkloadOperations) (ProductRuntimeOwnerRegistration, error) {
	return newProductApplicationSelectedPaaSRegistration(nativehost.SelectedPaaSApplicationNextcloud, runtimeVersion, runtimeAdapterRef, runtimeAdapterModuleRef, operations)
}
