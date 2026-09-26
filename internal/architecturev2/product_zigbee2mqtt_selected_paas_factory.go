package architecturev2

import "github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"

// NewProductZigbee2mqttSelectedPaaSRegistration binds the Zigbee2mqtt alternative of the
// smart-home-zigbee workload to the shared selected application executor.
func NewProductZigbee2mqttSelectedPaaSRegistration(runtimeVersion, runtimeAdapterRef, runtimeAdapterModuleRef string, operations nativehost.SelectedPaaSWorkloadOperations) (ProductRuntimeOwnerRegistration, error) {
	return newProductApplicationSelectedPaaSRegistration(nativehost.SelectedPaaSApplicationZigbee2mqtt, runtimeVersion, runtimeAdapterRef, runtimeAdapterModuleRef, operations)
}
