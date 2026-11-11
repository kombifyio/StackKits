package architecturev2

import "github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"

func NewProductWorkbenchHostSelectedPaaSRegistration(version, adapter, module string, operations nativehost.SelectedPaaSWorkloadOperations) (ProductRuntimeOwnerRegistration, error) {
	return newProductApplicationSelectedPaaSRegistration(nativehost.SelectedPaaSApplicationWorkbenchHost, version, adapter, module, operations)
}
