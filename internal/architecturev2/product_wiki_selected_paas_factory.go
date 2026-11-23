package architecturev2

import "github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"

func NewProductWikiSelectedPaaSRegistration(version, adapter, module string, operations nativehost.SelectedPaaSWorkloadOperations) (ProductRuntimeOwnerRegistration, error) {
	return newProductApplicationSelectedPaaSRegistration(nativehost.SelectedPaaSApplicationWiki, version, adapter, module, operations)
}
