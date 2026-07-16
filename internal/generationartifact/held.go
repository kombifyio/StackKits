package generationartifact

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path"
	"sort"
	"strings"

	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

// BuildManifestHeld hashes actual files through one authorization-borrowed
// workspace transaction. prefix is a portable private staging directory (or
// "." for the workspace); manifest paths remain workspace-relative plan paths.
func BuildManifestHeld(plan VerifiedPlan, workspace *confinedfs.Transaction, prefix string, relativePaths []string) (ArtifactManifest, error) {
	if err := plan.RequireReady(ExecutionPhaseGeneration); err != nil {
		return ArtifactManifest{}, err
	}
	if workspace == nil {
		return ArtifactManifest{}, fail(ErrInvalidContract, "artifacts", "held workspace transaction is required")
	}
	if _, err := validateHeldPrefix(prefix); err != nil {
		return ArtifactManifest{}, err
	}
	if len(relativePaths) == 0 {
		return ArtifactManifest{}, fail(ErrInvalidContract, "artifacts", "at least one rendered artifact is required")
	}
	manifest := ArtifactManifest{
		APIVersion: ArtifactManifestAPIVersion,
		Kind:       ArtifactManifestKind,
		Binding:    plan.Binding(),
		Artifacts:  make([]RenderedArtifact, 0, len(relativePaths)),
	}
	state := manifestBuildState{
		expectedByPath: make(map[string]expectedArtifact, len(plan.expectedArtifacts)),
		resolvedSeen:   make(map[string]string, len(relativePaths)),
		resolvedInfos:  make([]os.FileInfo, 0, len(relativePaths)),
		pathSeen:       make(map[string]struct{}, len(relativePaths)),
		includedIDs:    make(map[string]struct{}, len(relativePaths)),
	}
	for _, expected := range plan.expectedArtifacts {
		state.expectedByPath[portablePathKey(expected.Path)] = expected
	}
	for index, relativePath := range relativePaths {
		if err := addHeldManifestArtifact(plan, workspace, prefix, index, relativePath, &manifest, &state); err != nil {
			return ArtifactManifest{}, err
		}
	}
	for _, expected := range plan.expectedArtifacts {
		if expected.Required {
			if _, included := state.includedIDs[expected.ID]; !included {
				return ArtifactManifest{}, fail(ErrArtifactMissing, expected.Path, "required governed artifact %q was not included", expected.ID)
			}
		}
	}
	sort.Slice(manifest.Artifacts, func(i, j int) bool {
		left, _ := resolvedplan.CanonicalJSON(manifest.Artifacts[i])
		right, _ := resolvedplan.CanonicalJSON(manifest.Artifacts[j])
		return bytes.Compare(left, right) < 0
	})
	if err := validateManifest(manifest); err != nil {
		return ArtifactManifest{}, err
	}
	return manifest, nil
}

func addHeldManifestArtifact(plan VerifiedPlan, workspace *confinedfs.Transaction, prefix string, index int, relativePath string, manifest *ArtifactManifest, state *manifestBuildState) error {
	canonicalPath, err := validatePortablePath(relativePath)
	if err != nil {
		return err
	}
	pathKey := portablePathKey(canonicalPath)
	if _, exists := state.pathSeen[pathKey]; exists {
		return fail(ErrDuplicateArtifact, fmt.Sprintf("artifacts[%d].path", index), "duplicate path %q", canonicalPath)
	}
	state.pathSeen[pathKey] = struct{}{}
	expected, declared := state.expectedByPath[pathKey]
	if !declared || expected.Path != canonicalPath {
		return fail(ErrInvalidContract, fmt.Sprintf("artifacts[%d].path", index), "output %q is not declared by ResolvedPlan.generation.artifacts", canonicalPath)
	}
	actualPath := joinHeldPrefix(prefix, canonicalPath)
	content, info, err := workspace.ReadStable(actualPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return wrap(ErrArtifactMissing, canonicalPath, "rendered artifact does not exist", err)
		}
		return wrap(ErrIO, canonicalPath, "read rendered artifact through held workspace", err)
	}
	if err := enforceArtifactMode(info, expected, canonicalPath); err != nil {
		return err
	}
	resolvedKey := portablePathKey(actualPath)
	if previous, exists := state.resolvedSeen[resolvedKey]; exists {
		return fail(ErrDuplicateArtifact, fmt.Sprintf("artifacts[%d].path", index), "%q aliases already-listed artifact %q", canonicalPath, previous)
	}
	for previousIndex, previousInfo := range state.resolvedInfos {
		if os.SameFile(previousInfo, info) {
			return fail(ErrDuplicateArtifact, fmt.Sprintf("artifacts[%d].path", index), "%q aliases already-listed artifact %q", canonicalPath, manifest.Artifacts[previousIndex].Path)
		}
	}
	if expected.ID == "resolved-plan" && !bytes.Equal(content, plan.canonical) {
		return fail(ErrArtifactChanged, expected.Path, "resolved-plan artifact does not equal the verified canonical plan")
	}
	digest := sha256.Sum256(content)
	manifest.Artifacts = append(manifest.Artifacts, RenderedArtifact{
		ID: expected.ID, Path: canonicalPath, Kind: expected.Kind,
		Format: expected.Format, Mode: expected.Mode, SHA256: "sha256:" + hex.EncodeToString(digest[:]),
	})
	state.resolvedInfos = append(state.resolvedInfos, info)
	state.resolvedSeen[resolvedKey] = canonicalPath
	state.includedIDs[expected.ID] = struct{}{}
	return nil
}

// VerifyManifestHeld validates and re-hashes every artifact through the held
// workspace transaction and proves the governed output tree is closed.
func VerifyManifestHeld(plan VerifiedPlan, workspace *confinedfs.Transaction, prefix string, manifest ArtifactManifest) error {
	if workspace == nil {
		return fail(ErrInvalidContract, "manifest", "held workspace transaction is required")
	}
	if _, err := validateHeldPrefix(prefix); err != nil {
		return err
	}
	if err := validateManifest(manifest); err != nil {
		return err
	}
	if manifest.Binding != plan.Binding() {
		return fail(ErrBindingMismatch, "manifest.binding", "does not match the current ResolvedPlan")
	}
	if err := validateManifestAgainstPlan(plan, manifest); err != nil {
		return err
	}
	seenInfos := make([]os.FileInfo, 0, len(manifest.Artifacts))
	for index, artifact := range manifest.Artifacts {
		content, info, err := workspace.ReadStable(joinHeldPrefix(prefix, artifact.Path))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return wrap(ErrArtifactMissing, artifact.Path, "rendered artifact does not exist", err)
			}
			return wrap(ErrIO, artifact.Path, "read rendered artifact through held workspace", err)
		}
		for previousIndex, previousInfo := range seenInfos {
			if os.SameFile(previousInfo, info) {
				return fail(ErrDuplicateArtifact, fmt.Sprintf("manifest.artifacts[%d].path", index), "%q aliases already-listed artifact %q", artifact.Path, manifest.Artifacts[previousIndex].Path)
			}
		}
		seenInfos = append(seenInfos, info)
		expected := plan.expectedArtifactByID(artifact.ID)
		if err := enforceArtifactMode(info, expected, artifact.Path); err != nil {
			return err
		}
		if expected.ID == "resolved-plan" && !bytes.Equal(content, plan.canonical) {
			return fail(ErrArtifactChanged, expected.Path, "resolved-plan artifact does not equal the verified canonical plan")
		}
		digest := sha256.Sum256(content)
		actual := "sha256:" + hex.EncodeToString(digest[:])
		if actual != artifact.SHA256 {
			return fail(ErrArtifactChanged, artifact.Path, "declared %s, current file is %s", artifact.SHA256, actual)
		}
	}
	return verifyClosedExecutorTreeHeld(plan, workspace, prefix, manifest)
}

// PersistManifestHeld writes canonical manifest bytes exclusively beneath the
// held private stage. It never accepts or opens an absolute pathname.
func PersistManifestHeld(plan VerifiedPlan, workspace *confinedfs.Transaction, prefix string, manifest ArtifactManifest) error {
	if err := plan.RequireReady(ExecutionPhaseGeneration); err != nil {
		return err
	}
	if manifest.Binding != plan.Binding() {
		return fail(ErrBindingMismatch, "manifest.binding", "does not match the current ResolvedPlan")
	}
	if err := validateManifestAgainstPlan(plan, manifest); err != nil {
		return err
	}
	data, err := manifest.MarshalCanonical()
	if err != nil {
		return err
	}
	manifestPath, _ := heldControlPaths(plan, prefix)
	if err := workspace.MkdirAll(path.Dir(manifestPath), 0o750); err != nil {
		return wrap(ErrIO, manifestPath, "create held manifest directory", err)
	}
	if err := workspace.WriteFileExclusive(manifestPath, data, 0o600); err != nil {
		return wrap(ErrIO, manifestPath, "persist manifest through held workspace", err)
	}
	return nil
}

// PersistReceiptHeld writes canonical receipt bytes exclusively beneath the
// same held private stage. The receipt remains the final control write.
func PersistReceiptHeld(plan VerifiedPlan, workspace *confinedfs.Transaction, prefix string, manifest ArtifactManifest, receipt GenerationReceipt) error {
	if err := plan.RequireReady(ExecutionPhaseGeneration); err != nil {
		return err
	}
	if err := VerifyReceipt(plan, manifest, receipt); err != nil {
		return err
	}
	data, err := receipt.MarshalCanonical()
	if err != nil {
		return err
	}
	_, receiptPath := heldControlPaths(plan, prefix)
	if err := workspace.MkdirAll(path.Dir(receiptPath), 0o750); err != nil {
		return wrap(ErrIO, receiptPath, "create held receipt directory", err)
	}
	if err := workspace.WriteFileExclusive(receiptPath, data, 0o600); err != nil {
		return wrap(ErrIO, receiptPath, "persist receipt through held workspace", err)
	}
	return nil
}

// ReadManifestHeld reads and canonicalizes the control manifest without
// reopening the workspace pathname.
func ReadManifestHeld(plan VerifiedPlan, workspace *confinedfs.Transaction, prefix string) (ArtifactManifest, error) {
	manifestPath, _ := heldControlPaths(plan, prefix)
	var manifest ArtifactManifest
	if err := readHeldCanonicalControl(workspace, manifestPath, "artifact manifest", &manifest); err != nil {
		return ArtifactManifest{}, err
	}
	return manifest, nil
}

// ReadReceiptHeld reads and canonicalizes the control receipt without
// reopening the workspace pathname.
func ReadReceiptHeld(plan VerifiedPlan, workspace *confinedfs.Transaction, prefix string) (GenerationReceipt, error) {
	_, receiptPath := heldControlPaths(plan, prefix)
	var receipt GenerationReceipt
	if err := readHeldCanonicalControl(workspace, receiptPath, "generation receipt", &receipt); err != nil {
		return GenerationReceipt{}, err
	}
	return receipt, nil
}

func readHeldCanonicalControl(workspace *confinedfs.Transaction, relative, label string, target interface {
	MarshalCanonical() ([]byte, error)
}) error {
	data, err := readHeldControl0600(workspace, relative, label)
	if err != nil {
		return err
	}
	if err := decodeStrictJSON(data, target); err != nil {
		return wrap(ErrInvalidContract, relative, "decode "+label, err)
	}
	canonical, err := target.MarshalCanonical()
	if err != nil {
		return err
	}
	if !bytes.Equal(data, canonical) {
		return fail(ErrNonCanonical, relative, "%s is not byte-for-byte canonical JSON", label)
	}
	return nil
}

func readHeldControl0600(workspace *confinedfs.Transaction, relative, label string) ([]byte, error) {
	if workspace == nil {
		return nil, fail(ErrInvalidContract, relative, "held workspace transaction is required")
	}
	data, info, err := workspace.ReadStable(relative)
	if err != nil {
		return nil, wrap(ErrIO, relative, "read held "+label, err)
	}
	required := os.FileMode(0o600)
	if err := requireReadMode(relative, label, info, &required); err != nil {
		return nil, err
	}
	return data, nil
}

func verifyClosedExecutorTreeHeld(plan VerifiedPlan, workspace *confinedfs.Transaction, prefix string, manifest ArtifactManifest) error {
	expected, err := newClosedExecutorTree(plan, manifest)
	if err != nil {
		return err
	}
	for _, controlPath := range expected.controlFiles {
		info, err := workspace.Lstat(joinHeldPrefix(prefix, controlPath))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return wrap(ErrArtifactMissing, controlPath, "generation control file does not exist", err)
			}
			return wrap(ErrIO, controlPath, "inspect held generation control file", err)
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return fail(ErrInvalidPath, controlPath, "generation control file must be a regular non-symlink file")
		}
	}
	outputPath := joinHeldPrefix(prefix, plan.outputRoot)
	entries, err := workspace.Walk(outputPath)
	if err != nil {
		return wrap(ErrIO, plan.outputRoot, "walk governed executor input tree through held workspace", err)
	}
	seenFiles := make(map[string]string, len(expected.files))
	seenDirectories := make(map[string]string, len(expected.directories))
	for _, entry := range entries {
		portable, err := stripHeldPrefix(prefix, entry.Path)
		if err != nil {
			return err
		}
		if entry.Info.IsDir() {
			if err := observeClosedEntry(expected.directories, seenDirectories, portable, "directory"); err != nil {
				return err
			}
			continue
		}
		if !entry.Info.Mode().IsRegular() {
			return fail(ErrInvalidPath, portable, "executor input tree may contain only declared regular files and required directories")
		}
		if err := observeClosedEntry(expected.files, seenFiles, portable, "regular file"); err != nil {
			return err
		}
	}
	return expected.requireComplete(seenFiles, seenDirectories)
}

func validateHeldPrefix(prefix string) (string, error) {
	if prefix == "." {
		return prefix, nil
	}
	canonical, err := validatePortablePath(prefix)
	if err != nil {
		return "", wrap(ErrInvalidPath, "held.prefix", "invalid held workspace prefix", err)
	}
	return canonical, nil
}

func joinHeldPrefix(prefix, relative string) string {
	if prefix == "." {
		return relative
	}
	return path.Join(prefix, relative)
}

func stripHeldPrefix(prefix, value string) (string, error) {
	if prefix == "." {
		return value, nil
	}
	if value == prefix {
		return ".", nil
	}
	want := prefix + "/"
	if !strings.HasPrefix(value, want) {
		return "", fail(ErrPathEscape, value, "held tree entry escaped prefix %q", prefix)
	}
	result := strings.TrimPrefix(value, want)
	if result == "" {
		return ".", nil
	}
	return result, nil
}

func heldControlPaths(plan VerifiedPlan, prefix string) (string, string) {
	controls := metadataControlPaths(plan.outputRoot)
	return joinHeldPrefix(prefix, controls[0]), joinHeldPrefix(prefix, controls[1])
}
