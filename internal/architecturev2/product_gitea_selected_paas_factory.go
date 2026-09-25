package architecturev2

import "github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"

// NewProductGiteaSelectedPaaSRegistration binds the explicit private Git workload
// to the existing selected application executor and operations owner.
func NewProductGiteaSelectedPaaSRegistration(runtimeVersion, runtimeAdapterRef, runtimeAdapterModuleRef string, operations nativehost.SelectedPaaSWorkloadOperations) (ProductRuntimeOwnerRegistration, error) {
	return newProductApplicationSelectedPaaSRegistration(nativehost.SelectedPaaSApplicationGitea, runtimeVersion, runtimeAdapterRef, runtimeAdapterModuleRef, operations)
}
