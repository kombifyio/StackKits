package restoreactivation

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/backuplifecycle"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/localbackuppolicy"
	"gopkg.in/yaml.v3"
)

const (
	basementKitSlug                  = "basement-kit"
	basementCoreModuleID             = "stackkits-basement-core-runtime"
	basementCoreLiteModuleID         = "stackkits-basement-core-lite-runtime"
	basementCoreComposeOutputRef     = "platform/basement-core/compose.yaml"
	basementCoreLiteComposeOutputRef = "platform/basement-core-lite/compose.yaml"
	basementCorePolicyOutputRef      = "home/backup/kopia-source-policy.json"
	basementCoreLitePolicyOutputRef  = "home/backup/kopia-source-policy-lite.json"
	basementComposeProject           = "stackkit-basement-core"
	restoreResultAPIVersion          = "stackkit.local-backup-restore-result/v1"
	restoreRecoveryAPI               = "stackkit.local-backup-restore-recovery-anchor/v1"
	repositoryRestoreAPI             = "stackkit.local-backup-repository-restore/v1"
	restoreVerificationAPI           = "stackkit.local-backup-restore-verification/v1"
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
	workspaceRoot string,
	plan generationartifact.VerifiedPlan,
	manifest generationartifact.ArtifactManifest,
	restoreResult backuplifecycle.RestoreResult,
	operationID string,
) (Authority, error) {
	derived, err := deriveRuntimeRecoveryGraph(workspaceRoot, plan, manifest, operationID)
	if err != nil {
		return Authority{}, err
	}
	if err := bindRestoreResult(derived.graph.PlanBinding, derived.graph.ManifestHash, derived.plan, restoreResult); err != nil {
		return Authority{}, err
	}
	volumeDetails := append([]Volume(nil), derived.graph.VolumeDetails...)
	for index := range volumeDetails {
		volumeDetails[index].StagingPath = path.Join(
			restoreResult.Request.StagingPath,
			volumeDetails[index].LiveName,
			"_data",
		)
	}
	return Authority{
		OperationID:          operationID,
		OwnerRef:             restoreResult.OwnerRef,
		RestoreResultID:      restoreResult.ID,
		PlanHash:             derived.graph.PlanHash,
		ManifestHash:         derived.graph.ManifestHash,
		ApplyResultHash:      restoreResult.AuthorizationLineage.ApplyResultHash,
		ManagedVolumeSetHash: derived.graph.ManagedVolumeSetHash,
		StackID:              derived.graph.StackID,
		ComposeProject:       derived.graph.ComposeProject,
		ComposePath:          derived.graph.ComposePath,
		ComposeDigest:        derived.graph.ComposeDigest,
		ComposeRuntimes:      append([]ComposeRuntime(nil), derived.graph.ComposeRuntimes...),
		KopiaHelperImage:     derived.graph.KopiaHelperImage,
		StagingVolume:        derived.graph.StagingVolume,
		StagingPath:          restoreResult.Request.StagingPath,
		Volumes:              append([]string(nil), derived.graph.Volumes...),
		VolumeDetails:        volumeDetails,
	}, nil
}

func derivePlanAuthority(
	raw []byte,
	manifest generationartifact.ArtifactManifest,
	operationID string,
	applicationVolumeSets ...[]Volume,
) (planAuthority, error) {
	if len(applicationVolumeSets) > 1 {
		return planAuthority{}, errors.New("restoreactivation: application volume authority is ambiguous")
	}
	var applicationVolumes []Volume
	if len(applicationVolumeSets) == 1 {
		applicationVolumes = applicationVolumeSets[0]
	}
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
	var coreModuleID string
	for _, candidate := range modules {
		module, ok := candidate.(map[string]any)
		if !ok {
			return planAuthority{}, errors.New("restoreactivation: plan module is not an object")
		}
		moduleID := text(module, "id")
		if moduleID == basementCoreModuleID || moduleID == basementCoreLiteModuleID {
			if core != nil {
				return planAuthority{}, errors.New("restoreactivation: Basement core runtime selection is ambiguous")
			}
			core = module
			coreModuleID = moduleID
		}
	}
	if core == nil || text(core, "renderTarget") != "compose" {
		return planAuthority{}, errors.New("restoreactivation: exact Compose Basement core runtime is not selected")
	}
	composeOutputRef, policyOutputRef, ok := basementCoreRestoreContract(coreModuleID)
	if !ok {
		return planAuthority{}, errors.New("restoreactivation: selected Basement core profile is unsupported")
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
	managedNames, policyArtifactID, err := deriveSourcePolicy(core, coreModuleID, policyOutputRef, stagingRoot)
	if err != nil {
		return planAuthority{}, err
	}
	composeProject, volumes, liveNames, err := bindManagedVolumes(logicalVolumes, managedNames, applicationVolumes, operationID)
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
	composeArtifact, policyArtifact, err := bindManifest(plan, manifest, policyArtifactID, coreModuleID, composeOutputRef)
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

func basementCoreRestoreContract(moduleID string) (composeOutputRef, policyOutputRef string, ok bool) {
	switch moduleID {
	case basementCoreModuleID:
		return basementCoreComposeOutputRef, basementCorePolicyOutputRef, true
	case basementCoreLiteModuleID:
		return basementCoreLiteComposeOutputRef, basementCoreLitePolicyOutputRef, true
	default:
		return "", "", false
	}
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

func deriveSourcePolicy(core map[string]any, coreModuleID, expectedOutputRef, stagingRoot string) ([]string, string, error) {
	source, artifactID, err := readSourcePolicy(core, expectedOutputRef)
	if err != nil {
		return nil, "", err
	}
	if source == nil || artifactID == "" || text(source, "kind") != "docker-volume-root" ||
		text(source, "containerPath") == "" || text(source, "hostPath") == "" || source["readOnly"] != true {
		return nil, "", errors.New("restoreactivation: backup source authority is incomplete")
	}
	boundCoreModuleID := text(source, "coreModuleRef")
	if (boundCoreModuleID != "" && boundCoreModuleID != coreModuleID) ||
		(coreModuleID == basementCoreLiteModuleID && boundCoreModuleID != coreModuleID) {
		return nil, "", errors.New("restoreactivation: backup source authority is not bound to the selected core profile")
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

// readSourcePolicy is the single reader for the CUE-owned source-policy unit.
// Callers may apply their domain-specific checks after this structural binding,
// but must not read mutable generated policy files as a replacement authority.
func readSourcePolicy(core map[string]any, expectedOutputRef string) (map[string]any, string, error) {
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
		if text(output, "ref") != expectedOutputRef {
			return nil, "", errors.New("restoreactivation: backup source-policy output is not bound to the selected core profile")
		}
		artifactID = text(output, "artifactRef")
	}
	if source == nil || artifactID == "" {
		return nil, "", errors.New("restoreactivation: exact Basement backup source authority is absent")
	}
	return source, artifactID, nil
}

func bindManagedVolumes(
	logical []logicalVolume,
	managedNames []string,
	applicationVolumes []Volume,
	operationID string,
) (string, []Volume, []string, error) {
	if len(logical)+len(applicationVolumes) != len(managedNames) {
		return "", nil, nil, errors.New("restoreactivation: source-policy managedVolumeNames does not match the selected persistent backup volumes")
	}
	nameSet := make(map[string]struct{}, len(managedNames))
	applicationNames := make(map[string]struct{}, len(applicationVolumes))
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
	for _, volume := range applicationVolumes {
		if !portableNamePattern.MatchString(volume.LiveName) || volume.ComposeProject == "" || volume.LogicalName == "" ||
			volume.LiveName != volume.ComposeProject+"_"+volume.LogicalName {
			return "", nil, nil, errors.New("restoreactivation: selected Application volume identity is invalid")
		}
		if _, duplicate := applicationNames[volume.LiveName]; duplicate {
			return "", nil, nil, fmt.Errorf("restoreactivation: duplicate selected Application volume %q", volume.LiveName)
		}
		applicationNames[volume.LiveName] = struct{}{}
	}
	for _, candidate := range logical {
		suffix := "_" + candidate.logicalName
		matches := make([]string, 0, 1)
		for managed := range nameSet {
			if _, application := applicationNames[managed]; application {
				continue
			}
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
	for applicationName := range applicationNames {
		if _, exists := nameSet[applicationName]; !exists {
			return "", nil, nil, fmt.Errorf("restoreactivation: source-policy omits selected Application volume %q", applicationName)
		}
		delete(nameSet, applicationName)
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
			ComponentRef:   candidate.componentRef,
			LogicalName:    candidate.logicalName,
			ComposeProject: project,
			LiveName:       liveName,
			StagingPath:    "",
			RollbackName:   rollbackName,
		}
	}
	sort.Slice(volumes, func(i, j int) bool { return volumes[i].LiveName < volumes[j].LiveName })
	sort.Strings(liveNames)
	return project, volumes, liveNames, nil
}

type standaloneComposeCustodyDocument struct {
	Name     string                                     `yaml:"name"`
	Services map[string]standaloneComposeCustodyService `yaml:"services"`
	Volumes  map[string]map[string]any                  `yaml:"volumes"`
}

type standaloneComposeCustodyService struct {
	Image   string   `yaml:"image"`
	Volumes []string `yaml:"volumes"`
}

type standaloneComposeApplicationBinding struct {
	WorkloadRef       string
	BundleWorkloadRef string
	ModuleRef         string
	SiteRef           string
	NodeRef           string
	Runtime           ComposeRuntime
	Volumes           []Volume
	Custody           StandaloneComposeRuntimeCustody
}

// deriveStandaloneComposeApplications selects only the local adapter rows
// whose CUE-owned lifecycle projection explicitly advertises backup/restore.
// Coolify, Komodo, and any unknown delivery remain outside this authority.
func deriveStandaloneComposeApplications(
	workspaceRoot string,
	plan map[string]any,
	operationID string,
) ([]ComposeRuntime, []Volume, error) {
	bindings, err := deriveStandaloneComposeApplicationBindings(workspaceRoot, plan, operationID)
	if err != nil {
		return nil, nil, err
	}
	runtimes := make([]ComposeRuntime, 0, len(bindings))
	volumes := make([]Volume, 0)
	for _, binding := range bindings {
		runtimes = append(runtimes, binding.Runtime)
		volumes = append(volumes, binding.Volumes...)
	}
	sort.Slice(volumes, func(i, j int) bool { return volumes[i].LiveName < volumes[j].LiveName })
	return runtimes, volumes, nil
}

func deriveStandaloneComposeApplicationBindings(
	workspaceRoot string,
	plan map[string]any,
	operationID string,
) ([]standaloneComposeApplicationBinding, error) {
	expectedRuntimes, err := deriveStandaloneComposeRuntimeContracts(plan)
	if err != nil {
		return nil, err
	}
	lifecycles, err := array(plan, "applicationLifecycles")
	if err != nil {
		return nil, errors.New("restoreactivation: Application lifecycle authority is absent")
	}
	supported := make(map[string]struct{}, len(lifecycles))
	for _, raw := range lifecycles {
		lifecycle, ok := raw.(map[string]any)
		if !ok {
			return nil, errors.New("restoreactivation: Application lifecycle is not an object")
		}
		delivery, deliveryErr := object(lifecycle, "delivery")
		if deliveryErr != nil || text(delivery, "kind") != "application-adapter" ||
			text(delivery, "adapterRef") != "standalone-compose" {
			continue
		}
		capabilities, capabilityErr := object(delivery, "capabilities")
		if capabilityErr != nil || capabilities["backupRestore"] != true {
			continue
		}
		workloadRef := text(lifecycle, "workloadRef")
		if !portableNamePattern.MatchString(workloadRef) {
			return nil, errors.New("restoreactivation: standalone Application lifecycle identity is invalid")
		}
		if _, duplicate := supported[workloadRef]; duplicate {
			return nil, errors.New("restoreactivation: standalone Application lifecycle identity is ambiguous")
		}
		supported[workloadRef] = struct{}{}
	}

	workloads, err := array(plan, "workloads")
	if err != nil {
		return nil, errors.New("restoreactivation: plan workloads are absent")
	}
	bindings := make([]standaloneComposeApplicationBinding, 0, len(supported))
	seenSupported := make(map[string]struct{}, len(supported))
	operationHash := sha256.Sum256([]byte(operationID))
	rollbackSuffix := "-rollback-" + hex.EncodeToString(operationHash[:8])
	for _, raw := range workloads {
		workload, ok := raw.(map[string]any)
		if !ok || text(workload, "kind") != "application" {
			continue
		}
		workloadRef := text(workload, "id")
		alternative, alternativeErr := object(workload, "alternative")
		if alternativeErr != nil {
			continue
		}
		runtimeContract, runtimeErr := object(alternative, "runtime")
		if runtimeErr != nil || text(runtimeContract, "delivery") != "application-adapter" {
			continue
		}
		adapter, adapterErr := object(runtimeContract, "adapter")
		if adapterErr != nil || text(adapter, "id") != "standalone-compose" {
			continue
		}
		if _, admitted := supported[workloadRef]; !admitted {
			return nil, fmt.Errorf("restoreactivation: standalone Application %q lacks explicit backup/restore capability", workloadRef)
		}
		seenSupported[workloadRef] = struct{}{}
		nodeRefs, nodeErr := stringArray(workload, "nodeRefs")
		if nodeErr != nil || len(nodeRefs) != 1 || !portableNamePattern.MatchString(nodeRefs[0]) {
			return nil, fmt.Errorf("restoreactivation: standalone Application %q must bind one exact local node", workloadRef)
		}
		project := "stackkit-" + workloadRef + "-" + nodeRefs[0]
		infrastructure, infrastructureErr := object(alternative, "infrastructure")
		if infrastructureErr != nil {
			return nil, fmt.Errorf("restoreactivation: standalone Application %q has no storage authority", workloadRef)
		}
		storage, storageErr := object(infrastructure, "storageAllocation")
		if storageErr != nil {
			return nil, fmt.Errorf("restoreactivation: standalone Application %q has no storage allocation", workloadRef)
		}
		allocations, allocationErr := array(storage, "allocations")
		if allocationErr != nil || len(allocations) == 0 {
			return nil, fmt.Errorf("restoreactivation: standalone Application %q storage allocation is empty", workloadRef)
		}
		applicationVolumes := make([]Volume, 0, len(allocations))
		allLogical := make([]string, 0, len(allocations))
		backupCount := 0
		for _, rawAllocation := range allocations {
			allocation, valid := rawAllocation.(map[string]any)
			if !valid {
				return nil, errors.New("restoreactivation: standalone Application storage allocation is invalid")
			}
			componentRef, volumeRef := text(allocation, "componentRef"), text(allocation, "volumeRef")
			logicalName := componentRef + "-" + volumeRef
			if !portableNamePattern.MatchString(componentRef) || !portableNamePattern.MatchString(volumeRef) ||
				!portableNamePattern.MatchString(logicalName) {
				return nil, errors.New("restoreactivation: standalone Application volume identity is invalid")
			}
			allLogical = append(allLogical, logicalName)
			backup, _ := allocation["backup"].(bool)
			if !backup {
				continue
			}
			if text(allocation, "class") != "persistent" {
				return nil, errors.New("restoreactivation: backup-enabled standalone Application volume is not persistent")
			}
			liveName := project + "_" + logicalName
			volume := Volume{
				ComponentRef: componentRef, LogicalName: logicalName, ComposeProject: project,
				LiveName: liveName, RollbackName: liveName + rollbackSuffix,
			}
			applicationVolumes = append(applicationVolumes, volume)
			backupCount++
		}
		if backupCount == 0 {
			return nil, fmt.Errorf("restoreactivation: standalone Application %q has no backup-enabled volume", workloadRef)
		}
		expectedRuntime, exists := expectedRuntimes[project]
		if !exists {
			return nil, fmt.Errorf("restoreactivation: standalone Application %q has no CUE-owned runtime graph", workloadRef)
		}
		custody, custodyErr := bindStandaloneComposeCustody(workspaceRoot, project, allLogical, expectedRuntime)
		if custodyErr != nil {
			return nil, fmt.Errorf("restoreactivation: bind standalone Application %q runtime custody: %w", workloadRef, custodyErr)
		}
		bindings = append(bindings, standaloneComposeApplicationBinding{
			WorkloadRef: workloadRef, BundleWorkloadRef: text(alternative, "id"),
			ModuleRef: text(alternative, "moduleRef"), SiteRef: expectedRuntime.SiteRef,
			NodeRef: expectedRuntime.NodeRef,
			Runtime: custody.Runtime, Volumes: applicationVolumes, Custody: custody,
		})
	}
	if len(seenSupported) != len(supported) {
		return nil, errors.New("restoreactivation: standalone backup/restore lifecycle has no exact selected workload")
	}
	sort.Slice(bindings, func(i, j int) bool { return bindings[i].Runtime.Project < bindings[j].Runtime.Project })
	return bindings, nil
}

func deriveStandaloneComposeRuntimeCustody(
	workspaceRoot string,
	plan generationartifact.VerifiedPlan,
	manifest generationartifact.ArtifactManifest,
	operationID string,
) ([]StandaloneComposeRuntimeCustody, error) {
	if !activationOperationPattern.MatchString(operationID) {
		return nil, errors.New("restoreactivation: operation ID must match [A-Za-z0-9][A-Za-z0-9._-]{0,127}")
	}
	if manifest.Binding != plan.Binding() {
		return nil, errors.New("restoreactivation: generation manifest does not bind the verified plan")
	}
	var planDocument map[string]any
	if err := json.Unmarshal(plan.Canonical(), &planDocument); err != nil {
		return nil, fmt.Errorf("restoreactivation: decode verified plan for runtime custody: %w", err)
	}
	bindings, err := deriveStandaloneComposeApplicationBindings(workspaceRoot, planDocument, operationID)
	if err != nil {
		return nil, err
	}
	custody := make([]StandaloneComposeRuntimeCustody, 0, len(bindings))
	for _, binding := range bindings {
		configFiles, configErr := bindStandaloneComposeConfigCustody(
			workspaceRoot, planDocument, plan.OutputRoot(), manifest, binding,
		)
		if configErr != nil {
			return nil, fmt.Errorf("restoreactivation: bind standalone Application %q configuration custody: %w", binding.WorkloadRef, configErr)
		}
		bound := binding.Custody
		bound.ConfigFiles = configFiles
		custody = append(custody, bound)
	}
	return custody, nil
}

func bindStandaloneComposeConfigCustody(
	workspaceRoot string,
	plan map[string]any,
	outputRoot string,
	manifest generationartifact.ArtifactManifest,
	binding standaloneComposeApplicationBinding,
) ([]StandaloneComposeRuntimeFile, error) {
	artifact, err := standaloneComposeBundleArtifact(plan, outputRoot, manifest, binding)
	if err != nil {
		return nil, err
	}
	root, err := confinedfs.Open(workspaceRoot)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	transaction, err := root.BeginTransaction()
	if err != nil {
		return nil, err
	}
	defer func() { _ = transaction.Close() }()
	bundleBytes, err := readStandaloneComposeCustodyFile(transaction, artifact.Path, false)
	if err != nil {
		return nil, fmt.Errorf("read generated application bundle %q: %w", artifact.ID, err)
	}
	bundleDigest := sha256.Sum256(bundleBytes)
	if artifact.SHA256 != "sha256:"+hex.EncodeToString(bundleDigest[:]) {
		return nil, fmt.Errorf("generated application bundle %q differs from the generation manifest", artifact.ID)
	}
	bundle, err := architecturev2renderer.ParseApplicationDeliveryWorkloadBundle(bundleBytes)
	if err != nil {
		return nil, fmt.Errorf("decode generated application bundle %q: %w", artifact.ID, err)
	}
	if bundle.WorkloadRef != binding.WorkloadRef || bundle.ModuleRef != binding.ModuleRef ||
		bundle.SiteRef != binding.SiteRef || bundle.NodeRef != binding.NodeRef {
		return nil, errors.New("generated application bundle does not bind the selected standalone runtime")
	}
	files := make([]StandaloneComposeRuntimeFile, 0, len(bundle.ConfigFiles))
	for _, config := range bundle.ConfigFiles {
		relativePath := path.Join(
			".stackkit", "runtime", "applications", binding.Runtime.Project,
			architecturev2renderer.StandaloneComposeConfigRelPath(config.Path),
		)
		data, readErr := readStandaloneComposeCustodyFile(transaction, relativePath, false)
		if readErr != nil {
			return nil, fmt.Errorf("read generated application configuration %q: %w", config.Path, readErr)
		}
		if !bytes.Equal(data, []byte(config.Body)) {
			return nil, fmt.Errorf("application configuration %q differs from the generated workload bundle", config.Path)
		}
		files = append(files, StandaloneComposeRuntimeFile{
			Path: relativePath, Mode: "0600", Data: append([]byte(nil), data...),
		})
	}
	sort.Slice(files, func(left, right int) bool { return files[left].Path < files[right].Path })
	return files, nil
}

func standaloneComposeBundleArtifact(
	plan map[string]any,
	outputRoot string,
	manifest generationartifact.ArtifactManifest,
	binding standaloneComposeApplicationBinding,
) (generationartifact.RenderedArtifact, error) {
	if outputRoot == "" {
		return generationartifact.RenderedArtifact{}, errors.New("restoreactivation: generation output root is absent")
	}
	if binding.ModuleRef == "" || binding.BundleWorkloadRef == "" || binding.SiteRef == "" || binding.NodeRef == "" {
		return generationartifact.RenderedArtifact{}, errors.New("restoreactivation: standalone Application bundle identity is incomplete")
	}
	modules, err := array(plan, "modules")
	if err != nil {
		return generationartifact.RenderedArtifact{}, errors.New("restoreactivation: plan modules are absent")
	}
	var module map[string]any
	for _, raw := range modules {
		candidate, ok := raw.(map[string]any)
		if ok && text(candidate, "id") == binding.ModuleRef {
			if module != nil {
				return generationartifact.RenderedArtifact{}, fmt.Errorf("restoreactivation: standalone Application module %q is ambiguous", binding.ModuleRef)
			}
			module = candidate
		}
	}
	if module == nil {
		return generationartifact.RenderedArtifact{}, fmt.Errorf("restoreactivation: standalone Application module %q is absent", binding.ModuleRef)
	}
	units, err := array(module, "renderUnits")
	if err != nil {
		return generationartifact.RenderedArtifact{}, fmt.Errorf("restoreactivation: standalone Application module %q render units are absent", binding.ModuleRef)
	}
	wantOutputRef := "workloads/" + binding.BundleWorkloadRef + "/bundle.json"
	var selected *generationartifact.RenderedArtifact
	for _, rawUnit := range units {
		unit, ok := rawUnit.(map[string]any)
		if !ok {
			return generationartifact.RenderedArtifact{}, errors.New("restoreactivation: standalone Application render unit is not an object")
		}
		instances, instanceErr := array(unit, "instances")
		if instanceErr != nil {
			return generationartifact.RenderedArtifact{}, fmt.Errorf("restoreactivation: standalone Application render instances are absent: %w", instanceErr)
		}
		for _, rawInstance := range instances {
			instance, ok := rawInstance.(map[string]any)
			if !ok || text(instance, "siteRef") != binding.SiteRef || text(instance, "nodeRef") != binding.NodeRef {
				continue
			}
			outputs, outputErr := array(instance, "outputs")
			if outputErr != nil {
				return generationartifact.RenderedArtifact{}, errors.New("restoreactivation: standalone Application render outputs are absent")
			}
			for _, rawOutput := range outputs {
				output, ok := rawOutput.(map[string]any)
				if !ok || text(output, "ref") != wantOutputRef {
					continue
				}
				artifactID, artifactPath := text(output, "artifactRef"), text(output, "path")
				if artifactID == "" || artifactPath == "" {
					return generationartifact.RenderedArtifact{}, errors.New("restoreactivation: standalone Application bundle output identity is incomplete")
				}
				var found generationartifact.RenderedArtifact
				for _, candidate := range manifest.Artifacts {
					if candidate.ID == artifactID {
						found = candidate
						break
					}
				}
				expectedArtifactPath := path.Join(outputRoot, artifactPath)
				if found.ID == "" || found.Path != expectedArtifactPath {
					return generationartifact.RenderedArtifact{}, fmt.Errorf("restoreactivation: standalone Application bundle %q is not bound by the generation manifest", artifactID)
				}
				if selected != nil {
					return generationartifact.RenderedArtifact{}, fmt.Errorf("restoreactivation: standalone Application bundle output %q is ambiguous", wantOutputRef)
				}
				copy := found
				selected = &copy
			}
		}
	}
	if selected == nil {
		return generationartifact.RenderedArtifact{}, fmt.Errorf("restoreactivation: standalone Application bundle output %q is absent", wantOutputRef)
	}
	return *selected, nil
}

// deriveStandaloneComposeRuntimeContracts reads the typed application runtime
// graph already embedded in the verified CUE-owned source projection. The
// graph, including its exact image pins, is the expected contract; current
// Compose bytes are only an observation and must never redefine it.
func deriveStandaloneComposeRuntimeContracts(
	plan map[string]any,
) (map[string]localbackuppolicy.ApplicationRuntime, error) {
	modules, err := array(plan, "modules")
	if err != nil {
		return nil, errors.New("restoreactivation: plan modules are absent")
	}
	var source localbackuppolicy.Source
	found := false
	for _, raw := range modules {
		module, ok := raw.(map[string]any)
		if !ok {
			return nil, errors.New("restoreactivation: plan module is not an object")
		}
		moduleID := text(module, "id")
		if moduleID != basementCoreModuleID && moduleID != basementCoreLiteModuleID {
			continue
		}
		_, policyOutputRef, ok := basementCoreRestoreContract(moduleID)
		if !ok {
			continue
		}
		rawSource, _, sourceErr := readSourcePolicy(module, policyOutputRef)
		if sourceErr != nil {
			return nil, sourceErr
		}
		if found {
			return nil, errors.New("restoreactivation: backup source runtime authority is ambiguous")
		}
		encoded, marshalErr := json.Marshal(rawSource)
		if marshalErr != nil {
			return nil, fmt.Errorf("restoreactivation: encode backup source authority: %w", marshalErr)
		}
		if unmarshalErr := json.Unmarshal(encoded, &source); unmarshalErr != nil {
			return nil, fmt.Errorf("restoreactivation: decode backup source authority: %w", unmarshalErr)
		}
		if validateErr := localbackuppolicy.ValidateSourceProjection(source); validateErr != nil {
			return nil, fmt.Errorf("restoreactivation: backup source authority is not the governed projection: %w", validateErr)
		}
		found = true
	}
	if !found {
		return nil, errors.New("restoreactivation: exact Basement backup source authority is absent")
	}
	runtimes := make(map[string]localbackuppolicy.ApplicationRuntime, len(source.ApplicationRuntimes))
	for _, runtime := range source.ApplicationRuntimes {
		if _, duplicate := runtimes[runtime.ComposeProject]; duplicate {
			return nil, fmt.Errorf("restoreactivation: duplicate standalone Application runtime %q", runtime.ComposeProject)
		}
		runtimes[runtime.ComposeProject] = runtime
	}
	return runtimes, nil
}

func bindStandaloneComposeCustody(
	workspaceRoot, project string,
	expectedLogicalVolumes []string,
	expectedRuntime localbackuppolicy.ApplicationRuntime,
) (StandaloneComposeRuntimeCustody, error) {
	relativePath := path.Join(".stackkit", "runtime", "applications", project, "compose.yaml")
	environmentPath := path.Join(".stackkit", "runtime", "applications", project, ".env")
	root, err := confinedfs.Open(workspaceRoot)
	if err != nil {
		return StandaloneComposeRuntimeCustody{}, err
	}
	defer func() { _ = root.Close() }()
	transaction, err := root.BeginTransaction()
	if err != nil {
		return StandaloneComposeRuntimeCustody{}, err
	}
	defer func() { _ = transaction.Close() }()
	raw, err := readStandaloneComposeCustodyFile(transaction, relativePath, false)
	if err != nil {
		return StandaloneComposeRuntimeCustody{}, errors.New("standalone Compose custody is not a bounded plain file")
	}
	var document standaloneComposeCustodyDocument
	if err := yaml.Unmarshal(raw, &document); err != nil || document.Name != project || len(document.Services) == 0 {
		return StandaloneComposeRuntimeCustody{}, errors.New("standalone Compose custody does not bind the expected project")
	}
	expectedServices := make(map[string]struct{}, len(expectedRuntime.Components))
	for _, component := range expectedRuntime.Components {
		expectedServices[component.ComponentRef] = struct{}{}
		service, exists := document.Services[component.ComponentRef]
		if !exists || service.Image != component.ImageRef+"@"+component.ImageDigest {
			return StandaloneComposeRuntimeCustody{}, fmt.Errorf("standalone Compose service %q does not match its CUE-owned pinned image", component.ComponentRef)
		}
	}
	if len(document.Services) != len(expectedServices) {
		return StandaloneComposeRuntimeCustody{}, errors.New("standalone Compose service set differs from the CUE-owned runtime graph")
	}
	for serviceName := range document.Services {
		if _, exists := expectedServices[serviceName]; !exists {
			return StandaloneComposeRuntimeCustody{}, fmt.Errorf("standalone Compose contains unselected service %q", serviceName)
		}
	}
	declared := make(map[string]struct{}, len(document.Volumes))
	for name := range document.Volumes {
		declared[name] = struct{}{}
	}
	mounted := map[string]struct{}{}
	for _, service := range document.Services {
		for _, mount := range service.Volumes {
			name, _, found := strings.Cut(mount, ":")
			if found {
				mounted[name] = struct{}{}
			}
		}
	}
	if len(declared) != len(expectedLogicalVolumes) {
		return StandaloneComposeRuntimeCustody{}, errors.New("standalone Compose volume set differs from the resolved storage allocation")
	}
	for _, name := range expectedLogicalVolumes {
		if _, exists := declared[name]; !exists {
			return StandaloneComposeRuntimeCustody{}, errors.New("standalone Compose omits a resolved storage allocation")
		}
		if _, exists := mounted[name]; !exists {
			return StandaloneComposeRuntimeCustody{}, errors.New("standalone Compose does not mount a resolved storage allocation")
		}
	}
	sum := sha256.Sum256(raw)
	environment, err := readStandaloneComposeCustodyFile(transaction, environmentPath, true)
	if err != nil {
		return StandaloneComposeRuntimeCustody{}, errors.New("standalone Compose environment custody is not a bounded plain file")
	}
	environmentSum := sha256.Sum256(environment)
	return StandaloneComposeRuntimeCustody{
		Project: project,
		Runtime: ComposeRuntime{
			Project: project, Path: relativePath, Digest: "sha256:" + hex.EncodeToString(sum[:]),
			EnvironmentPath: environmentPath, EnvironmentDigest: "sha256:" + hex.EncodeToString(environmentSum[:]),
		},
		Compose:     StandaloneComposeRuntimeFile{Path: relativePath, Mode: "0600", Data: append([]byte(nil), raw...)},
		Environment: StandaloneComposeRuntimeFile{Path: environmentPath, Mode: "0600", Data: append([]byte(nil), environment...)},
	}, nil
}

func readStandaloneComposeCustodyFile(
	transaction *confinedfs.Transaction,
	relativePath string,
	allowEmpty bool,
) ([]byte, error) {
	data, info, err := transaction.ReadStableBounded(relativePath, 1<<20)
	if err != nil || info == nil || info.Size() > 1<<20 || (!allowEmpty && info.Size() == 0) {
		return nil, errors.New("standalone Compose custody file is not bounded")
	}
	return append([]byte(nil), data...), nil
}

func bindManifest(
	plan map[string]any,
	manifest generationartifact.ArtifactManifest,
	policyArtifactID string,
	coreModuleID string,
	composeOutputRef string,
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
		if text(owner, "moduleRef") == coreModuleID && text(owner, "unitRef") == "compose" {
			if compose.ID != "" || text(owner, "outputRef") != composeOutputRef || text(declaration, "kind") != "compose" || text(declaration, "format") != "yaml" {
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
