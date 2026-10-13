package architecturev2

import "github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"

// NewProductNtfyLiveSelectedPaaSRegistration uses the existing local application
// owner. No provider, database, credential or durable-message authority follows.
func NewProductNtfyLiveSelectedPaaSRegistration(version, adapter, module string, operations nativehost.SelectedPaaSWorkloadOperations) (ProductRuntimeOwnerRegistration, error) {
	return newProductApplicationSelectedPaaSRegistration(nativehost.SelectedPaaSApplicationNtfyLive, version, adapter, module, operations)
}
