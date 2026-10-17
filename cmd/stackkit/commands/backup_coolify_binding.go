package commands

import (
	"context"
	"errors"
	"reflect"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/backupexec"
	"github.com/kombifyio/stackkits/internal/localbackuppolicy"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/fallback/platformdeploy"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"
)

func nativeAppliedWorkloadDeploymentForBackup(authority nativeV2AppliedAuthority, workload string) (nativehost.SelectedPaaSWorkloadDeployment, error) {
	data := nativeAppliedWorkloadData{WorkspaceRoot: authority.WorkspaceRoot, Owner: authority.Owner, Manifest: authority.Manifest, AppliedWorkloads: authority.AppliedWorkloads}
	return nativeAppliedWorkloadDeploymentDataForLifecycle(data, authority.Plan.ApplyRequirements(), func(module, unit string) (string, bool, error) {
		return architecturev2renderer.PlanRuntimeEntryComponentRef(authority.Plan, module, unit)
	}, workload, false)
}

func nativeBackupApplicationBindings(ctx context.Context, authority nativeV2BackupAuthority) ([]backupexec.ApplicationRuntimeBinding, error) {
	deployments, err := nativeBackupApplicationDeployments(authority)
	if err != nil {
		return nil, err
	}
	source := authority.Policy.SourceProjection()
	digest, err := localbackuppolicy.SourceDigest(source)
	if err != nil {
		return nil, err
	}
	var bindings []backupexec.ApplicationRuntimeBinding
	for _, graph := range source.ApplicationRuntimes {
		if graph.RuntimeAdapterRef != "coolify" && graph.RuntimeAdapterRef != "komodo" {
			continue
		}
		deployment := deployments[graph.ComposeProject]
		volumes := []localbackuppolicy.ApplicationVolume{}
		for _, volume := range source.ApplicationVolumes {
			if volume.ComposeProject == graph.ComposeProject {
				volumes = append(volumes, volume)
			}
		}
		if graph.RuntimeAdapterRef == "komodo" {
			binding, err := nativehost.BindKomodoBackupRuntime(ctx, authority.WorkspaceRoot, deployment, graph, volumes)
			if err != nil {
				return nil, err
			}
			bindings = append(bindings, binding)
			continue
		}
		state, err := platformdeploy.LoadOwnerHostDeployment(authority.WorkspaceRoot, graph.ComposeProject)
		if err != nil {
			return nil, err
		}
		b := state.PlanBinding
		if b == nil || b.SourceAdapter != "coolify" || b.PlanHash != authority.Lineage.Binding.PlanHash || b.ApplyResultHash != authority.Lineage.ApplyResultHash || b.SourceDigest != digest || b.ArtifactDigest != deployment.ArtifactDigest {
			return nil, errors.New("Coolify backup source is not bound to the current signed Apply")
		}
		binding, err := nativehost.BindCoolifyBackupRuntime(ctx, authority.WorkspaceRoot, deployment, graph, volumes)
		if err != nil {
			return nil, err
		}
		if !reflect.DeepEqual(binding.VolumeNames, b.VolumeNames) {
			return nil, errors.New("Coolify physical source differs from its signed node binding")
		}
		bindings = append(bindings, binding)
	}
	return bindings, nil
}
