package restoreactivation

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/kombifyio/stackkits/internal/backuplifecycle"
	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/localbackuppolicy"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

// DeriveApplicationReleaseAuthority narrows restore authority to one
// standalone Compose application and its governed persistent volumes for a
// scoped release change, against the current applied plan. Its rollback
// volumes are bound to the operation, so the release rollback rebuilds the
// same authority from its signed record without the plan the target release
// generated.
func DeriveApplicationReleaseAuthority(workspace string, plan generationartifact.VerifiedPlan, manifest generationartifact.ArtifactManifest, lineage backuplifecycle.AuthorityLineage, operation, workload string) (Authority, error) {
	if !activationOperationPattern.MatchString(operation) || !portableNamePattern.MatchString(workload) {
		return Authority{}, errors.New("restoreactivation: invalid release operation or workload")
	}
	owner, err := localevidence.LoadOwnerCustody(workspace)
	if err != nil {
		return Authority{}, err
	}
	graph, err := DeriveRuntimeRecoveryGraph(workspace, plan, manifest, operation)
	if err != nil {
		return Authority{}, err
	}
	if lineage.Binding != plan.Binding() || lineage.ManifestHash != graph.ManifestHash {
		return Authority{}, errors.New("restoreactivation: release change requires the current applied Plan")
	}
	root, err := os.ReadFile(filepath.Join(workspace, filepath.FromSlash(graph.CorePolicyPath)))
	if err != nil {
		return Authority{}, err
	}
	if runtimeFileDigest(root) != graph.CorePolicyDigest {
		return Authority{}, errors.New("restoreactivation: backup policy differs from verified generation")
	}
	policy, err := localbackuppolicy.Decode(root)
	if err != nil {
		return Authority{}, err
	}
	if _, err := ApplicationTransferPersistentWorkloads(policy, []string{workload}); err != nil {
		return Authority{}, errors.New("restoreactivation: the application has no governed persistent data to retain")
	}
	custody, err := ApplicationTransferRuntimeCustody(workspace, plan, manifest, []string{workload})
	if err != nil {
		return Authority{}, err
	}
	if len(custody) != 1 {
		return Authority{}, errors.New("restoreactivation: a release change covers exactly one Compose project")
	}
	var details []Volume
	for _, declared := range policy.Source.ApplicationVolumes {
		if declared.WorkloadRef != workload {
			continue
		}
		index := slices.IndexFunc(graph.VolumeDetails, func(v Volume) bool { return v.LiveName == declared.VolumeName })
		if index < 0 || graph.VolumeDetails[index].ComposeProject != custody[0].Project {
			return Authority{}, errors.New("restoreactivation: application volume is outside its runtime authority")
		}
		details = append(details, graph.VolumeDetails[index])
	}
	sortVolumesByLiveName(details)
	names := make([]string, len(details))
	for i, v := range details {
		names[i] = v.LiveName
	}
	hash, err := resolvedplan.CanonicalSHA256(names)
	if err != nil {
		return Authority{}, err
	}
	return Authority{OperationID: operation, RenderTarget: graph.RenderTarget, OwnerRef: owner.OwnerRef, PlanHash: graph.PlanHash, ManifestHash: graph.ManifestHash, ApplyResultHash: lineage.ApplyResultHash, ManagedVolumeSetHash: hash, StackID: graph.StackID, ComposeProject: graph.ComposeProject, ComposePath: graph.ComposePath, ComposeDigest: graph.ComposeDigest, ComposeRuntimes: []ComposeRuntime{custody[0].Runtime}, KopiaHelperImage: graph.KopiaHelperImage, StagingVolume: graph.StagingVolume, Volumes: names, VolumeDetails: details}, nil
}

// HoldApplicationProject stops every container of one Compose project and any
// copy helper still bound to the operation. It reads only Docker labels, so a
// rollback can hold the application whichever release last rendered it.
func HoldApplicationProject(ctx context.Context, workspace string, authority Authority, project string) error {
	runtime, err := newReleaseDockerRuntime(workspace)
	if err != nil {
		return err
	}
	if err := runtime.stopCopyHelpers(ctx, authority); err != nil {
		return err
	}
	ids, err := runtime.projectContainers(ctx, project, false)
	if err != nil || len(ids) == 0 {
		return err
	}
	_, err = runtime.docker(ctx, append([]string{"stop", "--time", "60"}, ids...)...)
	return wrapDocker("stop application project "+project, err)
}

// ResumeApplicationProject starts the held containers of one Compose project
// unchanged; it is used only when a release change is abandoned before the
// target release was selected.
func ResumeApplicationProject(ctx context.Context, workspace, project string) error {
	runtime, err := newReleaseDockerRuntime(workspace)
	if err != nil {
		return err
	}
	ids, err := runtime.projectContainers(ctx, project, true)
	if err != nil || len(ids) == 0 {
		return err
	}
	_, err = runtime.docker(ctx, append([]string{"start"}, ids...)...)
	return wrapDocker("start application project "+project, err)
}

func newReleaseDockerRuntime(workspace string) (*dockerRuntime, error) {
	runtime, err := NewDockerRuntime(workspace)
	if err != nil {
		return nil, err
	}
	return runtime.(*dockerRuntime), nil
}

func (runtime *dockerRuntime) projectContainers(ctx context.Context, project string, all bool) ([]string, error) {
	if !portableNamePattern.MatchString(strings.TrimPrefix(project, "stackkit-")) || !strings.HasPrefix(project, "stackkit-") {
		return nil, errors.New("restoreactivation: invalid application project")
	}
	args := []string{"ps", "-q", "--no-trunc", "--filter", "label=com.docker.compose.project=" + project}
	if all {
		args = append(args, "--all")
	}
	output, err := runtime.docker(ctx, args...)
	if err != nil {
		return nil, wrapDocker("list application project containers", err)
	}
	return strings.Fields(string(output)), nil
}

// DigestRetainedVolume reads the operation-bound retained copy of one volume
// back, read-only, as the same file-tree digest DigestVolume gives the live one.
func DigestRetainedVolume(ctx context.Context, workspace string, authority Authority, volume Volume) (backuplifecycle.VolumeReadback, error) {
	if err := requireAuthorityVolume(authority, volume); err != nil {
		return backuplifecycle.VolumeReadback{}, err
	}
	runtime, err := newReleaseDockerRuntime(workspace)
	if err != nil {
		return backuplifecycle.VolumeReadback{}, err
	}
	if err := runtime.inspectRollback(ctx, authority, volume); err != nil {
		return backuplifecycle.VolumeReadback{}, err
	}
	retained := volume
	retained.LiveName, retained.RollbackName = volume.RollbackName, volume.LiveName
	view := authority
	view.VolumeDetails = []Volume{retained}
	return runtime.DigestVolume(ctx, view, retained, false)
}
