package architecturev2

import "github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"

// NewProductImmichKioskSelectedPaaSRegistration binds the ImmichKiosk alternative of the
// photos-kiosk workload to the shared selected application executor.
func NewProductImmichKioskSelectedPaaSRegistration(runtimeVersion, runtimeAdapterRef, runtimeAdapterModuleRef string, operations nativehost.SelectedPaaSWorkloadOperations) (ProductRuntimeOwnerRegistration, error) {
	return newProductApplicationSelectedPaaSRegistration(nativehost.SelectedPaaSApplicationImmichKiosk, runtimeVersion, runtimeAdapterRef, runtimeAdapterModuleRef, operations)
}
