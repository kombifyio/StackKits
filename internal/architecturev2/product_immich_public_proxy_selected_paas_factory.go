package architecturev2

import "github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"

// NewProductImmichPublicProxySelectedPaaSRegistration binds the ImmichPublicProxy alternative of the
// photos-share workload to the shared selected application executor.
func NewProductImmichPublicProxySelectedPaaSRegistration(runtimeVersion, runtimeAdapterRef, runtimeAdapterModuleRef string, operations nativehost.SelectedPaaSWorkloadOperations) (ProductRuntimeOwnerRegistration, error) {
	return newProductApplicationSelectedPaaSRegistration(nativehost.SelectedPaaSApplicationImmichPublicProxy, runtimeVersion, runtimeAdapterRef, runtimeAdapterModuleRef, operations)
}
