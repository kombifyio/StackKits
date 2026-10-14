package restoreactivation

import (
	"encoding/json"
	"errors"
	"path"
	"slices"

	"github.com/kombifyio/stackkits/internal/backuplifecycle"
	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/localbackuppolicy"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

// ApplicationAuthorityForVolumes re-resolves a narrowed recovery from the
// original journal's exact canonical set; no caller-selected recovery scope.
func ApplicationAuthorityForVolumes(workspace string, plan generationartifact.VerifiedPlan, manifest generationartifact.ArtifactManifest, result backuplifecycle.RestoreResult, operation string, volumes []string) (Authority, string, bool, error) {
	var document map[string]any
	if err := json.Unmarshal(plan.Canonical(), &document); err != nil {
		return Authority{}, "", false, err
	}
	graphs, err := deriveStandaloneComposeRuntimeContracts(document)
	if err != nil {
		return Authority{}, "", false, err
	}
	for _, graph := range graphs {
		if graph.RuntimeAdapterRef != "coolify" {
			continue
		}
		a, err := DeriveApplicationAuthority(workspace, plan, manifest, result, operation, graph.WorkloadRef)
		if err != nil {
			return Authority{}, "", false, err
		}
		if slices.Equal(a.Volumes, volumes) {
			return a, graph.WorkloadRef, true, nil
		}
	}
	return Authority{}, "", false, nil
}

// DeriveApplicationAuthority narrows a verified node restore to one selected
// Coolify workload. Names remain canonical; the existing manager custody
// adapter translates them at the Docker copy boundary, never in the journal.
func DeriveApplicationAuthority(workspace string, plan generationartifact.VerifiedPlan, manifest generationartifact.ArtifactManifest, result backuplifecycle.RestoreResult, operation, workload string) (Authority, error) {
	if !activationOperationPattern.MatchString(operation) || !portableNamePattern.MatchString(workload) || manifest.Binding != plan.Binding() {
		return Authority{}, errors.New("restoreactivation: invalid application selection or manifest binding")
	}
	var document map[string]any
	if err := json.Unmarshal(plan.Canonical(), &document); err != nil {
		return Authority{}, err
	}
	modules, err := array(document, "modules")
	if err != nil {
		return Authority{}, err
	}
	kit, err := object(document, "kit")
	if err != nil {
		return Authority{}, err
	}
	core, profile, err := selectCoreBackupSource(text(kit, "slug"), modules)
	if err != nil {
		return Authority{}, err
	}
	raw, _, err := readSourcePolicy(core, profile.policyOutputRef)
	if err != nil {
		return Authority{}, err
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return Authority{}, err
	}
	var source localbackuppolicy.Source
	if err = json.Unmarshal(encoded, &source); err != nil {
		return Authority{}, err
	}
	if err = localbackuppolicy.ValidateSourceProjection(source); err != nil {
		return Authority{}, err
	}
	project := ""
	for _, graph := range source.ApplicationRuntimes {
		if graph.WorkloadRef == workload {
			if project != "" || graph.RuntimeAdapterRef != "coolify" {
				return Authority{}, errors.New("restoreactivation: workload has no unique Coolify source")
			}
			project = graph.ComposeProject
		}
	}
	if project == "" {
		return Authority{}, errors.New("restoreactivation: workload is outside the signed backup source")
	}
	var all, selected []Volume
	for _, v := range source.ApplicationVolumes {
		volume := Volume{ComponentRef: v.ComponentRef, LogicalName: v.LogicalName, ComposeProject: v.ComposeProject, LiveName: v.VolumeName, RollbackName: v.VolumeName + "-rollback-" + rollbackSuffixForOperation(operation)}
		all = append(all, volume)
		if v.WorkloadRef == workload && v.ComposeProject == project {
			selected = append(selected, volume)
		}
	}
	if len(selected) == 0 {
		return Authority{}, errors.New("restoreactivation: selected workload has no backup volumes")
	}
	derived, err := derivePlanAuthority(plan.Canonical(), manifest, operation, all)
	if err != nil {
		return Authority{}, err
	}
	if derived.renderTarget != renderTargetCompose {
		return Authority{}, errors.New("restoreactivation: Coolify activation requires the native compose generation target")
	}
	manifestHash, err := manifest.Hash()
	if err != nil {
		return Authority{}, err
	}
	graph := RuntimeRecoveryGraph{PlanBinding: plan.Binding(), PlanHash: plan.Binding().PlanHash, ManifestHash: manifestHash, CorePolicyDigest: derived.policyArtifact.SHA256, StagingRoot: derived.stagingRoot}
	if err = bindRestoreResult(graph, result); err != nil {
		return Authority{}, err
	}
	sortVolumesByLiveName(selected)
	names := make([]string, len(selected))
	for i := range selected {
		names[i] = selected[i].LiveName
		selected[i].StagingPath = path.Join(result.Request.StagingPath, selected[i].LiveName, "_data")
	}
	hash, err := resolvedplan.CanonicalSHA256(names)
	if err != nil {
		return Authority{}, err
	}
	return Authority{OperationID: operation, OwnerRef: result.OwnerRef, RestoreResultID: result.ID, PlanHash: plan.Binding().PlanHash, ManifestHash: manifestHash, ApplyResultHash: result.AuthorizationLineage.ApplyResultHash, ManagedVolumeSetHash: hash, StackID: derived.stackID, ComposeProject: derived.composeProject, ComposePath: derived.composeArtifact.Path, ComposeDigest: derived.composeArtifact.SHA256, KopiaHelperImage: derived.kopiaImage, StagingVolume: derived.stagingVolume, StagingPath: result.Request.StagingPath, Volumes: names, VolumeDetails: selected}, nil
}
