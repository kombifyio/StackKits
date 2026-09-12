package architecturev2

import "github.com/kombifyio/stackkits/internal/runtimeexecutorlocal"

// NewProductGiteaSelectedPaaSRegistration binds the explicit private Git workload
// to the existing selected application executor and operations owner.
func NewProductGiteaSelectedPaaSRegistration(runtimeVersion, runtimeAdapterRef, runtimeAdapterModuleRef string, operations runtimeexecutorlocal.SelectedPaaSWorkloadOperations) (ProductRuntimeOwnerRegistration, error) {
	return newProductApplicationSelectedPaaSRegistration(runtimeexecutorlocal.SelectedPaaSApplicationGitea, runtimeVersion, runtimeAdapterRef, runtimeAdapterModuleRef, operations)
}
