package backupexec

import (
	"errors"

	"github.com/kombifyio/stackkits/internal/localbackuppolicy"
)

// NewDockerV2CoreQuiescerWithApplicationBindings keeps native app groups out of
// the v1 exact-container controller. The full source is independently retained
// by the Kopia engine and settlement owner; this selection controls power only.
func NewDockerV2CoreQuiescerWithApplicationBindings(source localbackuppolicy.Source, verifier ApplicationContainerCustodyVerifier, bindings []ApplicationRuntimeBinding) (ContainerQuiescer, error) {
	if err := localbackuppolicy.ValidateSourceProjection(source); err != nil {
		return nil, err
	}
	if _, _, err := bindApplicationSources(source, bindings); err != nil {
		return nil, err
	}
	groups := map[string]bool{}
	var graphs []localbackuppolicy.ApplicationRuntime
	for _, graph := range source.ApplicationRuntimes {
		if graph.RuntimeAdapterRef == "runtipi" {
			groups[graph.ComposeProject] = true
		} else {
			graphs = append(graphs, graph)
		}
	}
	if len(groups) != 1 {
		return nil, errors.New("app-group snapshot requires one selected Runtipi runtime")
	}
	var volumes []localbackuppolicy.ApplicationVolume
	for _, volume := range source.ApplicationVolumes {
		if !groups[volume.ComposeProject] {
			volumes = append(volumes, volume)
		}
	}
	core, err := localbackuppolicy.GovernedSourceWithApplicationVolumesAndRuntimesForCoreModule(source.CoreModuleRef, volumes, graphs)
	if err != nil {
		return nil, err
	}
	core, err = core.WithMarketplaceServices(source.MarketplaceServices)
	if err != nil {
		return nil, err
	}
	var controls []ApplicationRuntimeBinding
	for _, binding := range bindings {
		if !groups[binding.Runtime.ComposeProject] {
			controls = append(controls, binding)
		}
	}
	return NewDockerV2QuiescerWithApplicationBindings(core, verifier, controls)
}
