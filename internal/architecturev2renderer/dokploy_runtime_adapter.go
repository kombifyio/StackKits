package architecturev2renderer

const (
	dokployRuntimeAdapterModuleID  = "stackkits-dokploy-runtime"
	dokployRuntimeAdapterUnitID    = "dokploy-adapter"
	dokployRuntimeAdapterOutputRef = "platform/dokploy/runtime-adapter.json"
)

const dokployRuntimeAdapterRendererSchema = `stackkit.runtime-adapter/v1|WorkloadRuntimeAdapter|dokploy|container:application-adapter|operations:apply,observe,remove|provider-lifecycle:not-owned|credentials:local-owner|bootstrap:stackkits-dokploy-bootstrap-runtime`

func init() {
	registerProductRegistryExtension(RegisterDokployRuntimeAdapterRenderer)
}

// The Dokploy handoff names the node's own Dokploy, which the selected
// provider's bootstrap module installs; its owner API key stays node-local.
var dokployRuntimeAdapterSpec = selectedPaaSAdapterSpec{
	moduleID: dokployRuntimeAdapterModuleID, unitID: dokployRuntimeAdapterUnitID,
	templateRef: "builtin://platform/dokploy/runtime-adapter/v1.json", outputRef: dokployRuntimeAdapterOutputRef,
	adapterID: "dokploy", providerRef: "stackkits-dokploy", displayName: "Dokploy",
	schema:      dokployRuntimeAdapterRendererSchema,
	operations:  []string{"apply", "observe", "remove"},
	credentials: "local-owner",
	execution:   "authenticated-external-adapter", healthRef: "dokploy-runtime-contract",
}

// DokployRuntimeAdapterRendererContract identifies the Dokploy workload-adapter handoff.
func DokployRuntimeAdapterRendererContract() RendererContract {
	return dokployRuntimeAdapterSpec.contract()
}

// RegisterDokployRuntimeAdapterRenderer adds the Dokploy adapter renderer.
func RegisterDokployRuntimeAdapterRenderer(registry *Registry) error {
	renderer := newSelectedPaaSAdapterRenderer(dokployRuntimeAdapterSpec)
	return registry.Register(renderer.contract, renderer)
}
