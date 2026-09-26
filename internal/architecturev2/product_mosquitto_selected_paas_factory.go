package architecturev2

import "github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"

// NewProductMosquittoSelectedPaaSRegistration binds the Mosquitto alternative of the
// smart-home-mqtt workload to the shared selected application executor.
func NewProductMosquittoSelectedPaaSRegistration(runtimeVersion, runtimeAdapterRef, runtimeAdapterModuleRef string, operations nativehost.SelectedPaaSWorkloadOperations) (ProductRuntimeOwnerRegistration, error) {
	return newProductApplicationSelectedPaaSRegistration(nativehost.SelectedPaaSApplicationMosquitto, runtimeVersion, runtimeAdapterRef, runtimeAdapterModuleRef, operations)
}
