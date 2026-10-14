package commands

import (
	"context"

	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/localbackuppolicy"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/fallback/platformdeploy"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"
	"github.com/kombifyio/stackkits/internal/servicecontrol"
)

func bindAppliedCoolifySources(ctx context.Context, workspace string, tx *confinedfs.Transaction, plan generationartifact.VerifiedPlan, manifest generationartifact.ArtifactManifest, result architecturev2.VerifiedApplyResult, controller *servicecontrol.Controller) error {
	needed := false
	for _, target := range plan.ApplyRequirements().RuntimeInstances {
		if target.RuntimeAdapter != nil && target.RuntimeAdapter.ID == "coolify" {
			needed = true
		}
	}
	if !needed {
		return nil
	}
	owner, err := localevidence.LoadOwnerCustody(workspace)
	if err != nil {
		return err
	}
	_, requirement, err := nativeV2BackupPolicyRequirement(plan, owner.Binding.SiteRef, owner.Binding.NodeRef)
	if err != nil {
		return err
	}
	_, _, policy, err := readNativeV2BackupPolicyForArtifact(tx, manifest, requirement.ID)
	if err != nil {
		return err
	}
	source := policy.SourceProjection()
	digest, err := localbackuppolicy.SourceDigest(source)
	if err != nil {
		return err
	}
	authority := nativeV2AppliedAuthority{WorkspaceRoot: workspace, Owner: owner, Plan: plan, Manifest: manifest, AppliedWorkloads: result.Summary().AppliedWorkloads}
	for _, graph := range source.ApplicationRuntimes {
		if graph.RuntimeAdapterRef != "coolify" {
			continue
		}
		deployment, err := nativeAppliedWorkloadDeploymentForBackup(authority, graph.WorkloadRef)
		if err != nil {
			return err
		}
		volumes := []localbackuppolicy.ApplicationVolume{}
		for _, volume := range source.ApplicationVolumes {
			if volume.ComposeProject == graph.ComposeProject {
				volumes = append(volumes, volume)
			}
		}
		bound, err := nativehost.BindCoolifyBackupRuntime(ctx, workspace, deployment, graph, volumes)
		if err != nil {
			return err
		}
		state, err := platformdeploy.LoadOwnerHostDeployment(workspace, graph.ComposeProject)
		if err != nil {
			return err
		}
		binding, err := controller.ManagerPlanBinding(state, result.ResultHash())
		if err != nil {
			return err
		}
		binding.SourceDigest, binding.VolumeNames = digest, bound.VolumeNames
		state.PlanBinding = &binding
		if err := platformdeploy.SaveOwnerHostDeployment(workspace, graph.ComposeProject, state); err != nil {
			return err
		}
	}
	return nil
}
