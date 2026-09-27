package architecturev2

import "github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"

// NewProductSearxngSelectedPaaSRegistration binds the SearXNG alternative of
// the ai-search workload to the shared selected application executor.
func NewProductSearxngSelectedPaaSRegistration(runtimeVersion, runtimeAdapterRef, runtimeAdapterModuleRef string, operations nativehost.SelectedPaaSWorkloadOperations) (ProductRuntimeOwnerRegistration, error) {
	return newProductApplicationSelectedPaaSRegistration(nativehost.SelectedPaaSApplicationSearxng, runtimeVersion, runtimeAdapterRef, runtimeAdapterModuleRef, operations)
}

// NewProductTikaSelectedPaaSRegistration binds the Tika alternative of the
// ai-documents workload to the shared selected application executor.
func NewProductTikaSelectedPaaSRegistration(runtimeVersion, runtimeAdapterRef, runtimeAdapterModuleRef string, operations nativehost.SelectedPaaSWorkloadOperations) (ProductRuntimeOwnerRegistration, error) {
	return newProductApplicationSelectedPaaSRegistration(nativehost.SelectedPaaSApplicationTika, runtimeVersion, runtimeAdapterRef, runtimeAdapterModuleRef, operations)
}

// NewProductDoclingSelectedPaaSRegistration binds the Docling alternative of
// the ai-documents workload to the shared selected application executor.
func NewProductDoclingSelectedPaaSRegistration(runtimeVersion, runtimeAdapterRef, runtimeAdapterModuleRef string, operations nativehost.SelectedPaaSWorkloadOperations) (ProductRuntimeOwnerRegistration, error) {
	return newProductApplicationSelectedPaaSRegistration(nativehost.SelectedPaaSApplicationDocling, runtimeVersion, runtimeAdapterRef, runtimeAdapterModuleRef, operations)
}
