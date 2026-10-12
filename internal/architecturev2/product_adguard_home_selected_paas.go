package architecturev2

import "github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"

func NewProductAdguardHomeSelectedPaaSRegistration(runtimeVersion, runtimeAdapterRef, runtimeAdapterModuleRef string, operations nativehost.SelectedPaaSWorkloadOperations) (ProductRuntimeOwnerRegistration, error) {
	return newProductApplicationSelectedPaaSRegistration(nativehost.SelectedPaaSApplicationAdguardHome, runtimeVersion, runtimeAdapterRef, runtimeAdapterModuleRef, operations)
}
