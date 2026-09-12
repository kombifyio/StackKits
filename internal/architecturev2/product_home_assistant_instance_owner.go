package architecturev2

const (
	ProductRuntimeOwnerHomeAssistantHAOS     ProductRuntimeOwnerID = "stackkits-home-assistant-haos-runtime"
	ProductRuntimeOwnerHomeAssistantExisting ProductRuntimeOwnerID = "stackkits-home-assistant-existing-runtime"
	ProductRuntimeOwnerHomeAssistantImported ProductRuntimeOwnerID = "stackkits-home-assistant-imported-runtime"
)

// The external API owner is admitted through the existing authenticated
// execution-channel registry. It owns no container, local proxy or guest
// lifecycle, and admission alone produces no runtime success evidence.
func productHomeAssistantInstanceSelector(id ProductRuntimeOwnerID) ProductRuntimeOwnerSelector {
	return ProductRuntimeOwnerSelector{
		OwnerKind: "module", OwnerRef: string(id), ProviderRef: "stackkits-home-assistant-appliance",
		ModuleRef: string(id), UnitRef: "instance", WorkloadRef: "smart-home",
		RuntimeKind: "external", RuntimeDelivery: "external-control-plane", RuntimeEngine: "api",
	}
}
