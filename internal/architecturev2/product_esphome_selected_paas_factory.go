package architecturev2

import "github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"

// NewProductEsphomeSelectedPaaSRegistration binds the Esphome alternative of the
// smart-home-esphome workload to the shared selected application executor.
func NewProductEsphomeSelectedPaaSRegistration(runtimeVersion, runtimeAdapterRef, runtimeAdapterModuleRef string, operations nativehost.SelectedPaaSWorkloadOperations) (ProductRuntimeOwnerRegistration, error) {
	return newProductApplicationSelectedPaaSRegistration(nativehost.SelectedPaaSApplicationEsphome, runtimeVersion, runtimeAdapterRef, runtimeAdapterModuleRef, operations)
}
