package restoreactivation

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/kombifyio/stackkits/internal/backuplifecycle"
	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

const (
	basementKitSlug         = "basement-kit"
	basementCoreModuleID    = "stackkits-basement-core-runtime"
	basementComposeProject  = "stackkit-basement-core"
	restoreResultAPIVersion = "stackkit.local-backup-restore-result/v1"
	restoreRecoveryAPI      = "stackkit.local-backup-restore-recovery-anchor/v1"
	repositoryRestoreAPI    = "stackkit.local-backup-repository-restore/v1"
	restoreVerificationAPI  = "stackkit.local-backup-restore-verification/v1"
)

var (
	activationOperationPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	digestPattern              = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	portableNamePattern        = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)
	stagingLeafPattern         = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

type planAuthority struct {
	stackID         string
	composeProject  string
	composeArtifact generationartifact.RenderedArtifact
	policyArtifact  generationartifact.RenderedArtifact
	kopiaImage      string
	stagingVolume   string
	stagingRoot     string
	volumes         []Volume
	liveNames       []string
}

func deriveAuthority(
	plan generationartifact.VerifiedPlan,
	manifest generationartifact.ArtifactManifest,
	restoreResult backuplifecycle.RestoreResult,
	operationID string,
) (Authority, error) {
	if !activationOperationPattern.MatchString(operationID) {
		return Authority{}, errors.New("restoreactivation: operation ID must match [A-Za-z0-9][A-Za-z0-9._-]{0,127}")
	}
	if manifest.Binding != plan.Binding() {
		return Authority{}, errors.New("restoreactivation: generation manifest does not bind the verified plan")
	}
	manifestHash, err := manifest.Hash()
	if err != nil {
		return Authority{}, fmt.Errorf("restoreactivation: hash generation manifest: %w", err)
	}
	derived, err := derivePlanAuthority(plan.Canonical(), manifest, operationID)
	if err != nil {
		return Authority{}, err
	}
	if err := bindRestoreResult(plan.Binding(), manifestHash, derived, restoreResult); err != nil {
		return Authority{}, err
	}
	volumeDetails := append([]Volume(nil), derived.volumes...)
	for index := range volumeDetails {
		volumeDetails[index].StagingPath = path.Join(
			restoreResult.Request.StagingPath,
			volumeDetails[index].LiveName,
			"_data",
		)
	}
	volumeSetHash, err := resolvedplan.CanonicalSHA256(derived.liveNames)
	if err != nil {
		return Authority{}, fmt.Errorf("restoreactivation: hash managed volume set: %w", err)
	}
	return Authority{
		OperationID:          operationID,
		OwnerRef:             restoreResult.OwnerRef,
		RestoreResultID:      restoreResult.ID,
		PlanHash:             plan.Binding().PlanHash,
		ManifestHash:         manifestHash,
		ApplyResultHash:      restoreResult.AuthorizationLineage.ApplyResultHash,
		ManagedVolumeSetHash: volumeSetHash,
		StackID:              derived.stackID,
		ComposeProject:       derived.composeProject,
		ComposePath:          derived.composeArtifact.Path,
		ComposeDigest:        derived.composeArtifact.SHA256,
		KopiaHelperImage:     derived.kopiaImage,
		StagingVolume:        derived.stagingVolume,
		StagingPath:          restoreResult.Request.StagingPath,
		Volumes:              append([]string(nil), derived.liveNames...),
		VolumeDetails:        volumeDetails,
	}, nil
}

func derivePlanAuthority(raw []byte, manifest generationartifact.ArtifactManifest, operationID string) (planAuthority, error) {
	var plan map[string]any
	if err := json.Unmarshal(raw, &plan); err != nil {
		return planAuthority{}, fmt.Errorf("restoreactivation: decode verified plan: %w", err)
	}
	kit, err := object(plan, "kit")
	if err != nil || text(kit, "slug") != basementKitSlug {
		return planAuthority{}, errors.New("restoreactivation: activation requires the exact Basement Kit plan")
	}
	stackID := text(plan, "stackId")
	if !portableNamePattern.MatchString(stackID) {
		return planAuthority{}, errors.New("restoreactivation: plan stack ID is not a portable runtime identity")
	}
	modules, err := array(plan, "modules")
	if err != nil {
		return planAuthority{}, errors.New("restoreactivation: plan modules are absent")
	}
	var core map[string]any
	for _, candidate := range modules {
		module, ok := candidate.(map[string]any)
		if !ok {
			return planAuthority{}, errors.New("restoreactivation: plan module is not an object")
		}
		if text(module, "id") == basementCoreModuleID {
			if core != nil {
				return planAuthority{}, errors.New("restoreactivation: Basement core runtime selection is ambiguous")
			}
			core = module
		}
	}
	if core == nil || text(core, "renderTarget") != "compose" {
		return planAuthority{}, errors.New("restoreactivation: exact Compose Basement core runtime is not selected")
	}
	runtime, err := object(core, "runtime")
	if err != nil || text(runtime, "kind") != "container" || text(runtime, "engine") != "docker" ||
		text(runtime, "delivery") != "stackkit" || text(runtime, "execution") != "executable" {
		return planAuthority{}, errors.New("restoreactivation: Basement core runtime is not the exact executable StackKits Docker runtime")
	}
	components, err := array(runtime, "components")
	if err != nil || len(components) == 0 {
		return planAuthority{}, errors.New("restoreactivation: Basement core runtime has no governed components")
	}
	logicalVolumes, kopiaImage, stagingLogical, stagingRoot, err := deriveVolumes(components)
	if err != nil {
		return planAuthority{}, err
	}
	managedNames, policyArtifactID, err := deriveSourcePolicy(core, stagingRoot)
	if err != nil {
		return planAuthority{}, err
	}
	composeProject, volumes, liveNames, err := bindManagedVolumes(logicalVolumes, managedNames, operationID)
	if err != nil {
		return planAuthority{}, err
	}
	stagingVolume := composeProject + "_" + stagingLogical
	liveSet := make(map[string]struct{}, len(liveNames))
	for _, liveName := range liveNames {
		liveSet[liveName] = struct{}{}
		if liveName == stagingVolume {
			return planAuthority{}, errors.New("restoreactivation: restore staging volume collides with a managed live volume")
		}
	}
	for _, volume := range volumes {
		if _, collision := liveSet[volume.RollbackName]; collision || volume.RollbackName == stagingVolume {
			return planAuthority{}, errors.New("restoreactivation: deterministic rollback volume collides with governed runtime volumes")
		}
	}
	composeArtifact, policyArtifact, err := bindManifest(plan, manifest, policyArtifactID)
	if err != nil {
		return planAuthority{}, err
	}
	return planAuthority{
		stackID:         stackID,
		composeProject:  composeProject,
		composeArtifact: composeArtifact,
		policyArtifact:  policyArtifact,
		kopiaImage:      kopiaImage,
		stagingVolume:   stagingVolume,
		stagingRoot:     stagingRoot,
		volumes:         volumes,
		liveNames:       liveNames,
	}, nil
}

type logicalVolume struct {
	componentRef string
	logicalName  string
}

func deriveVolumes(components []any) ([]logicalVolume, string, string, string, error) {
	seenComponents := make(map[string]struct{}, len(components))
	seenVolumes := make(map[string]struct{})
	var managed []logicalVolume
	var kopiaImage, stagingLogical, stagingRoot string
	for _, raw := range components {
		component, ok := raw.(map[string]any)
		if !ok {
			return nil, "", "", "", errors.New("restoreactivation: Basement component is not an object")
		}
		componentID := text(component, "id")
		if !portableNamePattern.MatchString(componentID) {
			return nil, "", "", "", errors.New("restoreactivation: Basement component has invalid identity")
		}
		if _, exists := seenComponents[componentID]; exists {
			return nil, "", "", "", fmt.Errorf("restoreactivation: duplicate Basement component %q", componentID)
		}
		seenComponents[componentID] = struct{}{}
		if componentID == "kopia-agent" {
			if kopiaImage != "" {
				return nil, "", "", "", errors.New("restoreactivation: Kopia helper component is ambiguous")
			}
			image, imageErr := object(component, "image")
			if imageErr != nil || text(image, "ref") == "" || !digestPattern.MatchString(text(image, "digest")) {
				return nil, "", "", "", errors.New("restoreactivation: Kopia helper image is not digest pinned")
			}
			kopiaImage = text(image, "ref") + "@" + text(image, "digest")
		}
		volumes, _ := array(component, "volumes")
		for _, rawVolume := range volumes {
			volume, ok := rawVolume.(map[string]any)
			if !ok {
				return nil, "", "", "", errors.New("restoreactivation: Basement component volume is not an object")
			}
			logicalName := text(volume, "id")
			if !portableNamePattern.MatchString(logicalName) {
				return nil, "", "", "", errors.New("restoreactivation: Basement volume has invalid identity")
			}
			if _, exists := seenVolumes[logicalName]; exists {
				return nil, "", "", "", fmt.Errorf("restoreactivation: duplicate Basement volume %q", logicalName)
			}
			seenVolumes[logicalName] = struct{}{}
			backup, _ := volume["backup"].(bool)
			class := text(volume, "class")
			target := text(volume, "target")
			if backup {
				if class != "persistent" || componentID == "kopia-agent" {
					return nil, "", "", "", fmt.Errorf("restoreactivation: backup volume %q is not a persistent workload volume", logicalName)
				}
				managed = append(managed, logicalVolume{componentRef: componentID, logicalName: logicalName})
			}
			if componentID == "kopia-agent" && target == "/restore-staging" {
				if backup || class != "persistent" || stagingLogical != "" {
					return nil, "", "", "", errors.New("restoreactivation: restore staging selection is not exact and isolated")
				}
				stagingLogical, stagingRoot = logicalName, target
			}
		}
	}
	if len(managed) == 0 || kopiaImage == "" || stagingLogical == "" {
		return nil, "", "", "", errors.New("restoreactivation: Basement core lacks managed volumes, pinned Kopia helper, or isolated staging")
	}
	sort.Slice(managed, func(i, j int) bool { return managed[i].logicalName < managed[j].logicalName })
	return managed, kopiaImage, stagingLogical, stagingRoot, nil
}

func deriveSourcePolicy(core map[string]any, stagingRoot string) ([]string, string, error) {
	units, err := array(core, "renderUnits")
	if err != nil {
		return nil, "", errors.New("restoreactivation: Basement core render units are absent")
	}
	var source map[string]any
	var artifactID string
	for _, raw := range units {
		unit, ok := raw.(map[string]any)
		if !ok {
			return nil, "", errors.New("restoreactivation: Basement render unit is not an object")
		}
		if text(unit, "id") != "source-policy" {
			continue
		}
		if source != nil || text(unit, "kind") != "native-config" || text(unit, "applyMode") != "artifact-only" {
			return nil, "", errors.New("restoreactivation: backup source-policy selection is ambiguous")
		}
		values, valueErr := object(unit, "values")
		if valueErr != nil {
			return nil, "", errors.New("restoreactivation: backup source-policy values are absent")
		}
		source, valueErr = object(values, "backup-source")
		if valueErr != nil {
			return nil, "", errors.New("restoreactivation: backup source authority is absent")
		}
		instances, instanceErr := array(unit, "instances")
		if instanceErr != nil || len(instances) != 1 {
			return nil, "", errors.New("restoreactivation: backup source-policy must have one exact local instance")
		}
		instance, ok := instances[0].(map[string]any)
		if !ok || text(instance, "scope") != "node-local" {
			return nil, "", errors.New("restoreactivation: backup source-policy instance is not node-local")
		}
		outputs, outputErr := array(instance, "outputs")
		if outputErr != nil || len(outputs) != 1 {
			return nil, "", errors.New("restoreactivation: backup source-policy must have one exact artifact output")
		}
		output, ok := outputs[0].(map[string]any)
		if !ok {
			return nil, "", errors.New("restoreactivation: backup source-policy output is invalid")
		}
		artifactID = text(output, "artifactRef")
	}
	if source == nil || artifactID == "" || text(source, "kind") != "docker-volume-root" ||
		text(source, "containerPath") == "" || text(source, "hostPath") == "" || source["readOnly"] != true {
		return nil, "", errors.New("restoreactivation: backup source authority is incomplete")
	}
	managed, err := stringArray(source, "managedVolumeNames")
	if err != nil || len(managed) == 0 {
		return nil, "", errors.New("restoreactivation: backup source managedVolumeNames is empty or invalid")
	}
	excludes, err := stringArray(source, "excludePaths")
	if err != nil {
		return nil, "", errors.New("restoreactivation: backup source exclusions are invalid")
	}
	expectedStagingExclusion := text(source, "containerPath") + "/" +
		basementComposeProject + "_kopia-restore-staging/_data"
	stagingExcluded := false
	for _, excluded := range excludes {
		if excluded == expectedStagingExclusion {
			if stagingExcluded {
				return nil, "", errors.New("restoreactivation: backup staging exclusion is ambiguous")
			}
			stagingExcluded = true
		}
	}
	if !stagingExcluded || stagingRoot != "/restore-staging" {
		return nil, "", errors.New("restoreactivation: isolated restore staging is not excluded from backups")
	}
	return managed, artifactID, nil
}

func bindManagedVolumes(
	logical []logicalVolume,
	managedNames []string,
	operationID string,
) (string, []Volume, []string, error) {
	if len(logical) != len(managedNames) {
		return "", nil, nil, errors.New("restoreactivation: source-policy managedVolumeNames does not match persistent backup volumes 1:1")
	}
	nameSet := make(map[string]struct{}, len(managedNames))
	var project string
	for _, entry := range managedNames {
		if !portableNamePattern.MatchString(entry) {
			return "", nil, nil, errors.New("restoreactivation: managed Docker volume name is invalid")
		}
		if _, exists := nameSet[entry]; exists {
			return "", nil, nil, fmt.Errorf("restoreactivation: duplicate managed Docker volume %q", entry)
		}
		nameSet[entry] = struct{}{}
	}
	for _, candidate := range logical {
		suffix := "_" + candidate.logicalName
		matches := make([]string, 0, 1)
		for managed := range nameSet {
			if strings.HasSuffix(managed, suffix) {
				matches = append(matches, managed)
			}
		}
		if len(matches) != 1 {
			return "", nil, nil, fmt.Errorf("restoreactivation: logical volume %q has no unique qualified source-policy volume", candidate.logicalName)
		}
		candidateProject := strings.TrimSuffix(matches[0], suffix)
		if candidateProject == "" || (project != "" && candidateProject != project) {
			return "", nil, nil, errors.New("restoreactivation: managed volumes do not share one exact Compose project")
		}
		project = candidateProject
		delete(nameSet, matches[0])
	}
	if len(nameSet) != 0 || project != basementComposeProject {
		return "", nil, nil, errors.New("restoreactivation: source-policy contains a substituted managed volume")
	}
	operationHash := sha256.Sum256([]byte(operationID))
	rollbackSuffix := "-rollback-" + hex.EncodeToString(operationHash[:8])
	volumes := make([]Volume, len(logical))
	liveNames := make([]string, len(logical))
	rollbackSeen := make(map[string]struct{}, len(logical))
	for index, candidate := range logical {
		liveName := project + "_" + candidate.logicalName
		rollbackName := liveName + rollbackSuffix
		if _, collision := rollbackSeen[rollbackName]; collision {
			return "", nil, nil, errors.New("restoreactivation: deterministic rollback volume collision")
		}
		rollbackSeen[rollbackName] = struct{}{}
		liveNames[index] = liveName
		volumes[index] = Volume{
			ComponentRef: candidate.componentRef,
			LogicalName:  candidate.logicalName,
			LiveName:     liveName,
			StagingPath:  "",
			RollbackName: rollbackName,
		}
	}
	sort.Slice(volumes, func(i, j int) bool { return volumes[i].LiveName < volumes[j].LiveName })
	sort.Strings(liveNames)
	return project, volumes, liveNames, nil
}

func bindManifest(
	plan map[string]any,
	manifest generationartifact.ArtifactManifest,
	policyArtifactID string,
) (generationartifact.RenderedArtifact, generationartifact.RenderedArtifact, error) {
	generation, err := object(plan, "generation")
	if err != nil {
		return generationartifact.RenderedArtifact{}, generationartifact.RenderedArtifact{}, errors.New("restoreactivation: plan generation authority is absent")
	}
	declared, err := array(generation, "artifacts")
	if err != nil {
		return generationartifact.RenderedArtifact{}, generationartifact.RenderedArtifact{}, errors.New("restoreactivation: plan artifact authority is absent")
	}
	declaredByID := make(map[string]map[string]any, len(declared))
	for _, raw := range declared {
		artifact, ok := raw.(map[string]any)
		if !ok || text(artifact, "id") == "" {
			return generationartifact.RenderedArtifact{}, generationartifact.RenderedArtifact{}, errors.New("restoreactivation: plan artifact declaration is invalid")
		}
		id := text(artifact, "id")
		if _, duplicate := declaredByID[id]; duplicate {
			return generationartifact.RenderedArtifact{}, generationartifact.RenderedArtifact{}, errors.New("restoreactivation: duplicate plan artifact identity")
		}
		declaredByID[id] = artifact
	}
	manifestByID := make(map[string]generationartifact.RenderedArtifact, len(manifest.Artifacts))
	for _, artifact := range manifest.Artifacts {
		if _, duplicate := manifestByID[artifact.ID]; duplicate {
			return generationartifact.RenderedArtifact{}, generationartifact.RenderedArtifact{}, errors.New("restoreactivation: duplicate manifest artifact identity")
		}
		declaredArtifact := declaredByID[artifact.ID]
		if declaredArtifact == nil || artifact.Path != text(declaredArtifact, "path") ||
			artifact.Kind != text(declaredArtifact, "kind") || artifact.Format != text(declaredArtifact, "format") ||
			artifact.Mode != text(declaredArtifact, "mode") {
			return generationartifact.RenderedArtifact{}, generationartifact.RenderedArtifact{}, fmt.Errorf("restoreactivation: manifest artifact %q substitutes its plan declaration", artifact.ID)
		}
		manifestByID[artifact.ID] = artifact
	}
	var compose generationartifact.RenderedArtifact
	for id, declaration := range declaredByID {
		required, _ := declaration["required"].(bool)
		if required {
			if _, exists := manifestByID[id]; !exists {
				return generationartifact.RenderedArtifact{}, generationartifact.RenderedArtifact{}, fmt.Errorf("restoreactivation: required manifest artifact %q is absent", id)
			}
		}
		owner, _ := object(declaration, "owner")
		if text(owner, "moduleRef") == basementCoreModuleID && text(owner, "unitRef") == "compose" {
			if compose.ID != "" || text(declaration, "kind") != "compose" || text(declaration, "format") != "yaml" {
				return generationartifact.RenderedArtifact{}, generationartifact.RenderedArtifact{}, errors.New("restoreactivation: Basement Compose artifact selection is ambiguous")
			}
			compose = manifestByID[id]
		}
	}
	policy := manifestByID[policyArtifactID]
	if compose.ID == "" || !digestPattern.MatchString(compose.SHA256) ||
		policy.ID == "" || !digestPattern.MatchString(policy.SHA256) {
		return generationartifact.RenderedArtifact{}, generationartifact.RenderedArtifact{}, errors.New("restoreactivation: exact Compose or source-policy manifest artifact is absent")
	}
	return compose, policy, nil
}

func bindRestoreResult(
	binding generationartifact.PlanBinding,
	manifestHash string,
	derived planAuthority,
	result backuplifecycle.RestoreResult,
) error {
	if result.APIVersion != restoreResultAPIVersion ||
		result.RecoveryAnchor.APIVersion != restoreRecoveryAPI ||
		result.Receipt.APIVersion != repositoryRestoreAPI ||
		result.Verification.APIVersion != restoreVerificationAPI ||
		!digestPattern.MatchString(result.ID) || result.OwnerRef == "" ||
		!activationOperationPattern.MatchString(result.OperationID) ||
		result.AuthorizationLineage.Binding != binding ||
		result.AuthorizationLineage.ManifestHash != manifestHash ||
		!digestPattern.MatchString(result.AuthorizationLineage.ApplyResultHash) {
		return errors.New("restoreactivation: restore result does not bind the current verified plan, manifest, and Apply result")
	}
	if result.Request.OwnerRef != result.OwnerRef ||
		result.Request.AuthorityRef != result.AuthorityRef ||
		result.Request.OperationID != result.OperationID ||
		result.Request.AuthorizationLineage != result.AuthorizationLineage ||
		result.RecoveryAnchor.OwnerRef != result.OwnerRef ||
		result.RecoveryAnchor.AuthorityRef != result.AuthorityRef ||
		result.RecoveryAnchor.OperationID != result.OperationID ||
		result.Receipt.OperationID != result.OperationID ||
		result.Receipt.SnapshotID != result.Request.SnapshotReceipt.SnapshotID ||
		result.Request.StagingPath != result.Receipt.StagingPath ||
		result.Request.StagingPath != result.RecoveryAnchor.StagingPath ||
		result.Verification.OwnerRef != result.OwnerRef ||
		result.Verification.PlanHash != binding.PlanHash ||
		!result.Verification.ServicesVerified ||
		!result.Receipt.RepositoryContentVerified {
		return errors.New("restoreactivation: restore result lacks one exact repository-verified staging result")
	}
	expectedPrefix := derived.stagingRoot + "/"
	stagingLeaf := strings.TrimPrefix(result.Request.StagingPath, expectedPrefix)
	if !strings.HasPrefix(result.Request.StagingPath, expectedPrefix) || !stagingLeafPattern.MatchString(stagingLeaf) {
		return errors.New("restoreactivation: restore staging path is not the governed content-addressed path")
	}
	expectedLeaf := sha256.Sum256([]byte(result.OperationID))
	if stagingLeaf != hex.EncodeToString(expectedLeaf[:]) {
		return errors.New("restoreactivation: restore staging path does not derive from the signed restore operation")
	}
	if result.Request.PolicyArtifactDigest != derived.policyArtifact.SHA256 ||
		result.Request.PolicyArtifactDigest != result.RecoveryAnchor.PolicyArtifactDigest {
		return errors.New("restoreactivation: restore result policy binding is incomplete")
	}
	return nil
}

func object(parent map[string]any, key string) (map[string]any, error) {
	value, ok := parent[key].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s is not an object", key)
	}
	return value, nil
}

func array(parent map[string]any, key string) ([]any, error) {
	value, ok := parent[key].([]any)
	if !ok {
		return nil, fmt.Errorf("%s is not an array", key)
	}
	return value, nil
}

func stringArray(parent map[string]any, key string) ([]string, error) {
	values, err := array(parent, key)
	if err != nil {
		return nil, err
	}
	result := make([]string, len(values))
	for index, value := range values {
		item, ok := value.(string)
		if !ok || item == "" {
			return nil, fmt.Errorf("%s[%d] is not a non-empty string", key, index)
		}
		result[index] = item
	}
	return result, nil
}

func text(parent map[string]any, key string) string {
	value, _ := parent[key].(string)
	return value
}
