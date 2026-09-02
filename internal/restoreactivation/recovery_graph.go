package restoreactivation

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

// runtimeRecoveryGraphDerivation keeps the private plan authority alongside
// its public, restore-result-free projection. Both are produced by the same
// CUE-owned extraction so DeriveAuthority cannot drift from graph custody.
type runtimeRecoveryGraphDerivation struct {
	graph RuntimeRecoveryGraph
	plan  planAuthority
}

func deriveRuntimeRecoveryGraph(
	workspaceRoot string,
	plan generationartifact.VerifiedPlan,
	manifest generationartifact.ArtifactManifest,
	operationID string,
) (runtimeRecoveryGraphDerivation, error) {
	if !activationOperationPattern.MatchString(operationID) {
		return runtimeRecoveryGraphDerivation{}, errors.New("restoreactivation: operation ID must match [A-Za-z0-9][A-Za-z0-9._-]{0,127}")
	}
	if manifest.Binding != plan.Binding() {
		return runtimeRecoveryGraphDerivation{}, errors.New("restoreactivation: generation manifest does not bind the verified plan")
	}
	manifestHash, err := manifest.Hash()
	if err != nil {
		return runtimeRecoveryGraphDerivation{}, fmt.Errorf("restoreactivation: hash generation manifest: %w", err)
	}
	var planDocument map[string]any
	if err := json.Unmarshal(plan.Canonical(), &planDocument); err != nil {
		return runtimeRecoveryGraphDerivation{}, fmt.Errorf("restoreactivation: decode verified plan for Application restore authority: %w", err)
	}
	applicationRuntimes, applicationVolumes, err := deriveStandaloneComposeApplications(
		workspaceRoot, planDocument, operationID,
	)
	if err != nil {
		return runtimeRecoveryGraphDerivation{}, err
	}
	derived, err := derivePlanAuthority(plan.Canonical(), manifest, operationID, applicationVolumes)
	if err != nil {
		return runtimeRecoveryGraphDerivation{}, err
	}

	volumeDetails := append([]Volume(nil), derived.volumes...)
	volumeDetails = append(volumeDetails, applicationVolumes...)
	// The graph carries the operation-derived identity and no restore staging
	// result. A real RestoreResult supplies the concrete staging path later.
	for index := range volumeDetails {
		volumeDetails[index].StagingPath = ""
	}
	// Keep this ordering identical to the existing Authority projection. The
	// canonical graph parser rejects reordering instead of silently normalizing
	// an owner-signed record.
	sortVolumesByLiveName(volumeDetails)
	liveNames := make([]string, len(volumeDetails))
	for index := range volumeDetails {
		liveNames[index] = volumeDetails[index].LiveName
	}
	volumeSetHash, err := resolvedplan.CanonicalSHA256(liveNames)
	if err != nil {
		return runtimeRecoveryGraphDerivation{}, fmt.Errorf("restoreactivation: hash managed volume set: %w", err)
	}
	composeRuntimes := []ComposeRuntime{{
		Project: derived.composeProject,
		Path:    derived.composeArtifact.Path,
		Digest:  derived.composeArtifact.SHA256,
	}}
	composeRuntimes = append(composeRuntimes, applicationRuntimes...)
	sortComposeRuntimes(composeRuntimes)
	graph := RuntimeRecoveryGraph{
		APIVersion:           RuntimeRecoveryGraphAPIVersion,
		Kind:                 RuntimeRecoveryGraphKind,
		OperationID:          operationID,
		PlanBinding:          plan.Binding(),
		PlanHash:             plan.Binding().PlanHash,
		ManifestHash:         manifestHash,
		ManagedVolumeSetHash: volumeSetHash,
		StackID:              derived.stackID,
		ComposeProject:       derived.composeProject,
		ComposePath:          derived.composeArtifact.Path,
		ComposeDigest:        derived.composeArtifact.SHA256,
		ComposeRuntimes:      composeRuntimes,
		CorePolicyArtifactID: derived.policyArtifact.ID,
		CorePolicyPath:       derived.policyArtifact.Path,
		CorePolicyDigest:     derived.policyArtifact.SHA256,
		KopiaHelperImage:     derived.kopiaImage,
		StagingVolume:        derived.stagingVolume,
		StagingRoot:          derived.stagingRoot,
		Volumes:              liveNames,
		VolumeDetails:        volumeDetails,
	}
	if err := graph.validate(); err != nil {
		return runtimeRecoveryGraphDerivation{}, err
	}
	return runtimeRecoveryGraphDerivation{graph: graph, plan: derived}, nil
}

func sortVolumesByLiveName(volumes []Volume) {
	sort.Slice(volumes, func(left, right int) bool { return volumes[left].LiveName < volumes[right].LiveName })
}

func sortComposeRuntimes(runtimes []ComposeRuntime) {
	sort.Slice(runtimes, func(left, right int) bool { return runtimes[left].Project < runtimes[right].Project })
}

func cloneRuntimeRecoveryGraph(graph RuntimeRecoveryGraph) RuntimeRecoveryGraph {
	graph.ComposeRuntimes = append([]ComposeRuntime(nil), graph.ComposeRuntimes...)
	graph.Volumes = append([]string(nil), graph.Volumes...)
	graph.VolumeDetails = append([]Volume(nil), graph.VolumeDetails...)
	return graph
}

// Validate checks the closed, portable shape and internal bindings of a
// graph. It validates graph data only; it does not re-resolve CUE or authorize
// a mutation.
func (graph RuntimeRecoveryGraph) Validate() error {
	return graph.validate()
}

func (graph RuntimeRecoveryGraph) validate() error {
	if graph.APIVersion != RuntimeRecoveryGraphAPIVersion || graph.Kind != RuntimeRecoveryGraphKind {
		return errors.New("restoreactivation: runtime recovery graph has an unsupported contract identity")
	}
	if !activationOperationPattern.MatchString(graph.OperationID) {
		return errors.New("restoreactivation: runtime recovery graph operation ID is invalid")
	}
	if err := validateGraphPlanBinding(graph.PlanBinding); err != nil {
		return err
	}
	if graph.PlanHash != graph.PlanBinding.PlanHash || !digestPattern.MatchString(graph.PlanHash) ||
		!digestPattern.MatchString(graph.ManifestHash) || !digestPattern.MatchString(graph.ManagedVolumeSetHash) {
		return errors.New("restoreactivation: runtime recovery graph provenance hashes are invalid")
	}
	if !portableNamePattern.MatchString(graph.StackID) || graph.ComposeProject != basementComposeProject ||
		!portableNamePattern.MatchString(graph.ComposeProject) {
		return errors.New("restoreactivation: runtime recovery graph identity is invalid")
	}
	if err := validateGraphPath(graph.ComposePath, "Compose artifact"); err != nil {
		return err
	}
	if !digestPattern.MatchString(graph.ComposeDigest) {
		return errors.New("restoreactivation: runtime recovery graph Compose digest is invalid")
	}
	if !validGraphArtifactID(graph.CorePolicyArtifactID) {
		return errors.New("restoreactivation: runtime recovery graph source-policy artifact ID is absent")
	}
	if err := validateGraphPath(graph.CorePolicyPath, "source-policy artifact"); err != nil {
		return err
	}
	if graph.CorePolicyPath == graph.ComposePath {
		return errors.New("restoreactivation: runtime recovery graph source-policy path collides with Compose")
	}
	if !digestPattern.MatchString(graph.CorePolicyDigest) {
		return errors.New("restoreactivation: runtime recovery graph source-policy digest is invalid")
	}
	if err := validateKopiaHelperImage(graph.KopiaHelperImage); err != nil {
		return err
	}
	if !portableNamePattern.MatchString(graph.StagingVolume) ||
		!strings.HasPrefix(graph.StagingVolume, graph.ComposeProject+"_") ||
		graph.StagingRoot != "/restore-staging" {
		return errors.New("restoreactivation: runtime recovery graph staging authority is invalid")
	}
	if len(graph.ComposeRuntimes) == 0 {
		return errors.New("restoreactivation: runtime recovery graph has no Compose runtime")
	}
	runtimeProjects := make(map[string]struct{}, len(graph.ComposeRuntimes))
	runtimePaths := make(map[string]struct{}, len(graph.ComposeRuntimes)*2)
	var previousProject string
	coreFound := false
	for index, runtime := range graph.ComposeRuntimes {
		if !portableNamePattern.MatchString(runtime.Project) || runtime.Project == "" {
			return fmt.Errorf("restoreactivation: runtime recovery graph Compose project %d is invalid", index)
		}
		if index > 0 && runtime.Project <= previousProject {
			return errors.New("restoreactivation: runtime recovery graph Compose projects are not a unique canonical order")
		}
		previousProject = runtime.Project
		if _, duplicate := runtimeProjects[runtime.Project]; duplicate {
			return errors.New("restoreactivation: runtime recovery graph contains a duplicate Compose project")
		}
		runtimeProjects[runtime.Project] = struct{}{}
		if err := validateGraphPath(runtime.Path, "Compose runtime"); err != nil {
			return err
		}
		if runtime.Path == graph.CorePolicyPath {
			return errors.New("restoreactivation: runtime recovery graph Compose path collides with source policy")
		}
		if !digestPattern.MatchString(runtime.Digest) {
			return errors.New("restoreactivation: runtime recovery graph Compose runtime digest is invalid")
		}
		if _, duplicate := runtimePaths[runtime.Path]; duplicate {
			return errors.New("restoreactivation: runtime recovery graph contains a duplicate runtime path")
		}
		runtimePaths[runtime.Path] = struct{}{}
		if (runtime.EnvironmentPath == "") != (runtime.EnvironmentDigest == "") {
			return errors.New("restoreactivation: runtime recovery graph environment identity is incomplete")
		}
		if runtime.EnvironmentPath != "" {
			if err := validateGraphPath(runtime.EnvironmentPath, "Compose environment"); err != nil {
				return err
			}
			if runtime.EnvironmentPath == graph.CorePolicyPath {
				return errors.New("restoreactivation: runtime recovery graph environment path collides with source policy")
			}
			if !digestPattern.MatchString(runtime.EnvironmentDigest) {
				return errors.New("restoreactivation: runtime recovery graph environment digest is invalid")
			}
			if _, duplicate := runtimePaths[runtime.EnvironmentPath]; duplicate {
				return errors.New("restoreactivation: runtime recovery graph contains a duplicate environment path")
			}
			runtimePaths[runtime.EnvironmentPath] = struct{}{}
		}
		if runtime.Project == graph.ComposeProject {
			if coreFound || runtime.Path != graph.ComposePath || runtime.Digest != graph.ComposeDigest ||
				runtime.EnvironmentPath != "" || runtime.EnvironmentDigest != "" {
				return errors.New("restoreactivation: runtime recovery graph core Compose binding is ambiguous")
			}
			coreFound = true
		}
	}
	if !coreFound {
		return errors.New("restoreactivation: runtime recovery graph core Compose runtime is absent")
	}
	if len(graph.Volumes) == 0 || len(graph.VolumeDetails) != len(graph.Volumes) {
		return errors.New("restoreactivation: runtime recovery graph volume set is incomplete")
	}
	previousLive := ""
	seenLive := make(map[string]struct{}, len(graph.Volumes))
	allLiveNames := make(map[string]struct{}, len(graph.Volumes))
	for _, liveName := range graph.Volumes {
		allLiveNames[liveName] = struct{}{}
	}
	seenRollback := make(map[string]struct{}, len(graph.VolumeDetails))
	for index, liveName := range graph.Volumes {
		if !portableNamePattern.MatchString(liveName) || (index > 0 && liveName <= previousLive) {
			return errors.New("restoreactivation: runtime recovery graph live volumes are not a canonical portable set")
		}
		previousLive = liveName
		if _, duplicate := seenLive[liveName]; duplicate {
			return errors.New("restoreactivation: runtime recovery graph contains a duplicate live volume")
		}
		seenLive[liveName] = struct{}{}
		if liveName == graph.StagingVolume {
			return errors.New("restoreactivation: runtime recovery graph staging volume collides with a live volume")
		}
		volume := graph.VolumeDetails[index]
		if volume.LiveName != liveName || volume.StagingPath != "" ||
			!portableNamePattern.MatchString(volume.ComponentRef) ||
			!portableNamePattern.MatchString(volume.LogicalName) ||
			!portableNamePattern.MatchString(volume.ComposeProject) ||
			volume.LiveName != volume.ComposeProject+"_"+volume.LogicalName {
			return errors.New("restoreactivation: runtime recovery graph volume identity is invalid")
		}
		if _, knownProject := runtimeProjects[volume.ComposeProject]; !knownProject {
			return errors.New("restoreactivation: runtime recovery graph volume has no bound Compose project")
		}
		expectedRollback := liveName + "-rollback-" + rollbackSuffixForOperation(graph.OperationID)
		if volume.RollbackName != expectedRollback {
			return errors.New("restoreactivation: runtime recovery graph rollback volume is not operation-derived")
		}
		if _, liveCollision := allLiveNames[volume.RollbackName]; liveCollision {
			return errors.New("restoreactivation: runtime recovery graph rollback volume collides with a live volume")
		}
		if _, duplicate := seenRollback[volume.RollbackName]; duplicate || volume.RollbackName == graph.StagingVolume {
			return errors.New("restoreactivation: runtime recovery graph rollback volume collides with governed volumes")
		}
		seenRollback[volume.RollbackName] = struct{}{}
	}
	setHash, err := resolvedplan.CanonicalSHA256(graph.Volumes)
	if err != nil || setHash != graph.ManagedVolumeSetHash {
		return errors.New("restoreactivation: runtime recovery graph managed volume set hash is invalid")
	}
	return nil
}

func validateGraphPlanBinding(binding generationartifact.PlanBinding) error {
	for _, value := range []string{binding.PlanHash, binding.SpecHash, binding.InventoryHash, binding.DefinitionHash, binding.Authority.CatalogHash} {
		if !digestPattern.MatchString(value) {
			return errors.New("restoreactivation: runtime recovery graph plan binding hash is invalid")
		}
	}
	if binding.CompilerVersion != strings.TrimSpace(binding.CompilerVersion) ||
		binding.Renderer.ID != strings.TrimSpace(binding.Renderer.ID) ||
		binding.Renderer.Version != strings.TrimSpace(binding.Renderer.Version) ||
		strings.TrimSpace(binding.CompilerVersion) == "" || strings.TrimSpace(binding.Renderer.ID) == "" || strings.TrimSpace(binding.Renderer.Version) == "" {
		return errors.New("restoreactivation: runtime recovery graph plan binding identity is incomplete")
	}
	// Semantic class/document/issuer policy belongs to generationartifact's
	// VerifiedPlan/ArtifactManifest validation. A parsed recovery graph is
	// historical derived custody data, so it only checks the provenance shape
	// here and never creates a second authority policy.
	authority := binding.Authority
	if authority.Class != strings.TrimSpace(authority.Class) || strings.TrimSpace(authority.Class) == "" ||
		authority.Document != strings.TrimSpace(authority.Document) || strings.TrimSpace(authority.Document) == "" ||
		authority.Issuer != strings.TrimSpace(authority.Issuer) || strings.TrimSpace(authority.Issuer) == "" {
		return errors.New("restoreactivation: runtime recovery graph plan authority identity is incomplete")
	}
	if authority.AuthorityFingerprint != "" && !digestPattern.MatchString(authority.AuthorityFingerprint) {
		return errors.New("restoreactivation: runtime recovery graph plan authority fingerprint is invalid")
	}
	return nil
}

func validateGraphPath(value, identity string) error {
	canonical, err := confinedfs.ValidatePortablePath(value)
	if err != nil || canonical != value {
		return fmt.Errorf("restoreactivation: runtime recovery graph %s path is not portable", identity)
	}
	return nil
}

func validateKopiaHelperImage(value string) error {
	parts := strings.Split(value, "@")
	if len(parts) != 2 || parts[0] != strings.TrimSpace(parts[0]) || strings.TrimSpace(parts[0]) == "" ||
		strings.IndexFunc(parts[0], func(r rune) bool { return r < 0x20 || r == 0x7f || r == ' ' || r == '\t' }) >= 0 ||
		!digestPattern.MatchString(parts[1]) {
		return errors.New("restoreactivation: runtime recovery graph Kopia helper image is not digest pinned")
	}
	return nil
}

func validGraphArtifactID(value string) bool {
	return len(value) <= 256 && value != "" && value == strings.TrimSpace(value) &&
		strings.IndexFunc(value, func(r rune) bool { return r < 0x20 || r == 0x7f }) < 0
}

func rollbackSuffixForOperation(operationID string) string {
	sum := sha256.Sum256([]byte(operationID))
	return hex.EncodeToString(sum[:8])
}

// ParseRuntimeRecoveryGraph accepts only the exact canonical JSON emitted by
// MarshalCanonical and never turns the result into a verified plan.
func ParseRuntimeRecoveryGraph(data []byte) (RuntimeRecoveryGraph, error) {
	var graph RuntimeRecoveryGraph
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&graph); err != nil {
		return RuntimeRecoveryGraph{}, fmt.Errorf("restoreactivation: decode runtime recovery graph: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return RuntimeRecoveryGraph{}, errors.New("restoreactivation: runtime recovery graph contains multiple JSON values")
	} else if !errors.Is(err, io.EOF) {
		return RuntimeRecoveryGraph{}, fmt.Errorf("restoreactivation: runtime recovery graph has invalid trailing data: %w", err)
	}
	if err := graph.validate(); err != nil {
		return RuntimeRecoveryGraph{}, err
	}
	canonical, err := graph.MarshalCanonical()
	if err != nil {
		return RuntimeRecoveryGraph{}, err
	}
	if !bytes.Equal(canonical, data) {
		return RuntimeRecoveryGraph{}, errors.New("restoreactivation: runtime recovery graph is not byte-for-byte canonical JSON")
	}
	return cloneRuntimeRecoveryGraph(graph), nil
}

// MarshalCanonical emits the one stable JSON representation used by signed
// recovery custody. It does not add CUE or runtime authorization semantics.
func (graph RuntimeRecoveryGraph) MarshalCanonical() ([]byte, error) {
	if err := graph.validate(); err != nil {
		return nil, err
	}
	return resolvedplan.CanonicalJSON(graph)
}
