package opentofu

import (
	"errors"
	"regexp"
	"slices"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"
)

var coolifyFilesImageDigest = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// The product executor authenticates the CUE-rendered artifact and its image
// against the resolved target. Keep those pins in that authority, not a second
// provider table. This boundary admits only its existing single-container Files
// shape before preparation can persist files or touch the engine.
func admitCoolifyFilesDeployment(d nativehost.SelectedPaaSWorkloadDeployment) error {
	if len(d.Bundle) == 0 || len(d.Bundle) > 128<<10 || d.ArtifactDigest != digestBytes(d.Bundle) ||
		d.UnitRef != "cloudreve" || d.InstanceRef != "cloudreve-node-"+d.NodeRef ||
		d.ArtifactRef != "cloudreve-workload-bundle-instance-"+d.InstanceRef ||
		d.RuntimeAdapter.ID != "coolify" || d.RuntimeAdapter.ProviderRef != "stackkits-coolify" || d.RuntimeAdapter.ModuleRef != "stackkits-coolify-runtime" {
		return errors.New("Coolify provider requires the exact authenticated node Files artifact and adapter")
	}
	files, err := architecturev2renderer.ParseCloudreveWorkloadBundle(d.Bundle)
	if err != nil {
		return err
	}
	if files.WorkloadRef != d.WorkloadRef || files.ModuleRef != d.ModuleRef || files.Release != d.Release ||
		files.SiteRef != d.SiteRef || files.NodeRef != d.NodeRef || files.InstanceRef != d.InstanceRef || files.Route != d.Route {
		return errors.New("Coolify Files artifact differs from its authorized workload, node, release or route")
	}
	bundle, err := architecturev2renderer.ParseApplicationDeliveryWorkloadBundle(d.Bundle)
	if err != nil {
		return err
	}
	if bundle.EntryComponent != d.UnitRef || len(bundle.Components) != 1 || len(bundle.SecretRefs) != 0 || bundle.DaemonSocketPath != "" {
		return errors.New("Coolify Files requires its single application container without secret or daemon authority")
	}
	component := bundle.Components[0]
	if component.ID != bundle.EntryComponent || component.Role != "application" || component.Lifecycle != "daemon" || component.HealthFailure != "blocking" ||
		component.ImageRef == "" || !coolifyFilesImageDigest.MatchString(component.ImageDigest) ||
		len(component.DependsOn) != 0 || !slices.Equal(component.NetworkRefs, []string{"cloudreve-internal"}) ||
		component.HealthKind != "http" || component.HealthPath != "/" || component.HealthPort != 5212 ||
		len(component.HealthCommand) != 0 || len(component.Command) != 0 || len(component.Entrypoint) != 0 ||
		len(component.OwnerEnvironment) != 0 || len(component.SecretEnvironment) != 0 || component.PocketIDClient != nil ||
		len(component.SecretFiles) != 0 || len(component.RestoreActivationEnvironment) != 0 {
		return errors.New("Coolify Files container differs from the canonical Cloudreve application shape")
	}
	if len(component.Volumes) != 1 || component.Volumes[0] != (architecturev2renderer.ApplicationDeliveryVolumeDescriptor{
		ID: "data", Target: "/cloudreve/data", Class: "persistent", Backup: true,
	}) {
		return errors.New("Coolify Files requires its retained personal-data volume without additional mounts")
	}
	// The module parser binds CR_SETTING_siteURL to the declared HTTPS route and
	// requires forward-auth. Native rendering retains any governed config files,
	// and the existing recipe digest freezes their contents during pending work.
	for name := range component.Environment {
		if name != "CR_SETTING_siteURL" {
			return errors.New("Coolify Files environment differs from its route-owned site URL")
		}
	}
	return nil
}
