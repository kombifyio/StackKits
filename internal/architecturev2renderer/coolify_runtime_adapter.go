package architecturev2renderer

const (
	coolifyRuntimeAdapterModuleID    = "stackkits-coolify-runtime"
	coolifyRuntimeAdapterUnitID      = "coolify-adapter"
	coolifyRuntimeAdapterTemplateRef = "builtin://platform/coolify/runtime-adapter/v1.json"
	coolifyRuntimeAdapterOutputRef   = "platform/coolify/runtime-adapter.json"
)

const coolifyRuntimeAdapterRendererSchema = `stackkit.runtime-adapter/v1|WorkloadRuntimeAdapter|coolify|container:application-adapter|operations:apply,observe,remove|provider-lifecycle:not-owned|credentials:external-owner|routes:artifact-bound|evidence:required`

var coolifyRuntimeAdapterSpec = selectedPaaSAdapterSpec{
	moduleID: coolifyRuntimeAdapterModuleID, unitID: coolifyRuntimeAdapterUnitID,
	templateRef: coolifyRuntimeAdapterTemplateRef, outputRef: coolifyRuntimeAdapterOutputRef,
	adapterID: "coolify", providerRef: "stackkits-coolify", displayName: "Coolify",
	schema:      coolifyRuntimeAdapterRendererSchema,
	operations:  []string{"apply", "observe", "remove"},
	credentials: "external-owner",
	execution:   "authenticated-external-adapter", healthRef: "coolify-runtime-contract",
}

// CoolifyRuntimeAdapterRendererContract returns the exact implementation
// identity of the provider-free Coolify workload-adapter handoff.
func CoolifyRuntimeAdapterRendererContract() RendererContract {
	return coolifyRuntimeAdapterSpec.contract()
}

func newCoolifyRuntimeAdapterRenderer() selectedPaaSAdapterRenderer {
	return newSelectedPaaSAdapterRenderer(coolifyRuntimeAdapterSpec)
}
