package opentofu

import (
	"errors"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/fallback/platformdeploy"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"
)

const (
	komodoVaultModule = "stackkits-vaultwarden-runtime"
	komodoFilesModule = "stackkits-cloudreve-runtime"
)

type komodoWorkloadIdentity struct {
	workload, unit string
}

// This is provider admission for the existing product registrations, not a pin
// catalog. The identities come from the Vaultwarden and Cloudreve module render
// units and bundle contracts. Their producers still own versions and graphs.
func komodoProviderIdentity(module string) (komodoWorkloadIdentity, bool) {
	switch module {
	case komodoVaultModule:
		return komodoWorkloadIdentity{"vault", "vaultwarden"}, true
	case komodoFilesModule:
		return komodoWorkloadIdentity{"files", "cloudreve"}, true
	default:
		return komodoWorkloadIdentity{}, false
	}
}

func isKomodoProviderRoot(marker RootMarker) bool {
	_, admitted := komodoProviderIdentity(marker.ModuleRef)
	return admitted && marker.Execution == komodoProviderExecution && marker.Kind == RootKindWorkload &&
		marker.RuntimeDir == marker.ComposeProject
}

// Marker text alone cannot select a workload. Bind it to the authenticated
// deployment at each subprocess/destroy boundary before loading credentials.
func komodoProviderRootMatches(marker RootMarker, held platformdeploy.HostDeployment) bool {
	identity, admitted := komodoProviderIdentity(marker.ModuleRef)
	return admitted && isKomodoProviderRoot(marker) && held.WorkloadRef == identity.workload &&
		marker.InstanceRef == held.InstanceRef && held.Manager == "komodo" && held.Deployment.Platform == "komodo" &&
		held.Deployment.AppName == marker.ComposeProject && marker.ComposeProject == "stackkit-"+held.WorkloadRef+"-"+held.NodeRef
}

func parseKomodoProviderBundle(bundle []byte) (architecturev2renderer.ApplicationDeliveryBundleDescriptor, error) {
	d, err := architecturev2renderer.ParseApplicationDeliveryWorkloadBundle(bundle)
	if err != nil {
		return d, err
	}
	identity, admitted := komodoProviderIdentity(d.ModuleRef)
	if !admitted || d.WorkloadRef != identity.workload || d.EntryComponent != identity.unit {
		return d, errors.New("Komodo provider bundle has no admitted module/workload/unit identity")
	}
	return d, nil
}

func requireKomodoProviderDeployment(deployment nativehost.SelectedPaaSWorkloadDeployment) error {
	d, err := parseKomodoProviderBundle(deployment.Bundle)
	if err != nil {
		return err
	}
	if digestBytes(deployment.Bundle) != deployment.ArtifactDigest || d.ModuleRef != deployment.ModuleRef ||
		d.WorkloadRef != deployment.WorkloadRef || d.EntryComponent != deployment.UnitRef ||
		d.InstanceRef != deployment.InstanceRef || d.SiteRef != deployment.SiteRef || d.NodeRef != deployment.NodeRef ||
		d.Release != deployment.Release || deployment.RuntimeAdapter.ID != "komodo" ||
		deployment.RuntimeAdapter.ModuleRef != "stackkits-komodo-core-runtime" {
		return errors.New("Komodo provider bundle differs from its exact authorized deployment")
	}
	return nil
}
