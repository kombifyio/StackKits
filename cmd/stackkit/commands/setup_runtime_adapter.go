package commands

import (
	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
)

// nativeApplicationSetupAdapterForWorkload projects the setup implementation
// from one verified runtime requirement. Multiple targets stay unsupported
// until setup has an explicit target selector.
func nativeApplicationSetupAdapterForWorkload(requirements generationartifact.ApplyRequirements, workload string) (nativehost.ApplicationSetupAdapter, bool) {
	var selected nativehost.ApplicationSetupAdapter
	for _, target := range requirements.RuntimeInstances {
		if target.WorkloadRef != workload || target.RuntimeAdapter == nil {
			continue
		}
		if selected != nil {
			return nil, false
		}
		adapter := target.RuntimeAdapter
		binding := runtimeexecutor.RuntimeAdapterBinding{
			ID: adapter.ID, ProviderRef: adapter.ProviderRef, ProviderVersion: adapter.ProviderVersion,
			ProviderContractHash: adapter.ProviderContractHash, ModuleRef: adapter.ModuleRef,
			ModuleVersion: adapter.ModuleVersion, ModuleContractHash: adapter.ModuleContractHash,
		}
		resolved, err := nativehost.ResolveApplicationSetupAdapter(binding, adapter.Capabilities)
		if err != nil {
			return nil, false
		}
		selected = resolved
	}
	return selected, selected != nil
}
