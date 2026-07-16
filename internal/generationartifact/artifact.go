package generationartifact

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

const (
	ArtifactManifestAPIVersion  = "stackkit.generation-artifacts/v2"
	ArtifactManifestKind        = "GenerationArtifactManifest"
	GenerationReceiptAPIVersion = "stackkit.generation-receipt/v1"
	GenerationReceiptKind       = "GenerationReceipt"
	ArtifactManifestFileName    = "generation-manifest.json"
	GenerationReceiptFileName   = "generation-receipt.json"
)

// RenderedArtifact binds a portable relative path to the exact bytes rendered
// at that path. Paths always use slash separators, including on Windows.
type RenderedArtifact struct {
	ID     string `json:"id"`
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	Format string `json:"format"`
	Mode   string `json:"mode"`
	SHA256 string `json:"sha256"`
}

// ArtifactManifest binds every actual renderer output to one verified plan.
// Artifacts use the resolvedplan canonical set order for persistence.
type ArtifactManifest struct {
	APIVersion string             `json:"apiVersion"`
	Kind       string             `json:"kind"`
	Binding    PlanBinding        `json:"binding"`
	Artifacts  []RenderedArtifact `json:"artifacts"`
}

// GenerationReceipt records acceptance of one manifest. GeneratedAt is
// informational only: receipt identity and validation depend exclusively on
// Binding and ManifestHash, never wall-clock time.
type GenerationReceipt struct {
	APIVersion   string      `json:"apiVersion"`
	Kind         string      `json:"kind"`
	Binding      PlanBinding `json:"binding"`
	ManifestHash string      `json:"manifestHash"`
	GeneratedAt  string      `json:"generatedAt,omitempty"`
}

type manifestBuildState struct {
	expectedByPath map[string]expectedArtifact
	resolvedSeen   map[string]string
	resolvedInfos  []os.FileInfo
	pathSeen       map[string]struct{}
	includedIDs    map[string]struct{}
}

// BuildManifest hashes actual files beneath root and returns a deterministic
// manifest. Missing files, escapes, aliases, and duplicate paths fail closed.
func BuildManifest(plan VerifiedPlan, root string, relativePaths []string) (ArtifactManifest, error) {
	if err := plan.RequireReady(ExecutionPhaseGeneration); err != nil {
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
	for i, relativePath := range relativePaths {
		if err := addManifestArtifact(plan, root, i, relativePath, &manifest, &state); err != nil {
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

func addManifestArtifact(plan VerifiedPlan, root string, index int, relativePath string, manifest *ArtifactManifest, state *manifestBuildState) error {
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
	resolvedPath, resolvedKey, info, err := resolveArtifact(root, canonicalPath)
	if err != nil {
		return err
	}
	if err := enforceArtifactMode(info, expected, canonicalPath); err != nil {
		return err
	}
	if previous, exists := state.resolvedSeen[resolvedKey]; exists {
		return fail(ErrDuplicateArtifact, fmt.Sprintf("artifacts[%d].path", index), "%q aliases already-listed artifact %q", canonicalPath, previous)
	}
	for previousIndex, previousInfo := range state.resolvedInfos {
		if os.SameFile(previousInfo, info) {
			return fail(ErrDuplicateArtifact, fmt.Sprintf("artifacts[%d].path", index), "%q aliases already-listed artifact %q", canonicalPath, manifest.Artifacts[previousIndex].Path)
		}
	}
	state.resolvedInfos = append(state.resolvedInfos, info)
	state.resolvedSeen[resolvedKey] = canonicalPath
	content, digest, err := readStableArtifact(root, canonicalPath, resolvedPath, info, expected.ID == "resolved-plan")
	if err != nil {
		return err
	}
	if expected.ID == "resolved-plan" && !bytes.Equal(content, plan.canonical) {
		return fail(ErrArtifactChanged, expected.Path, "resolved-plan artifact does not equal the verified canonical plan")
	}
	manifest.Artifacts = append(manifest.Artifacts, RenderedArtifact{
		ID: expected.ID, Path: canonicalPath, Kind: expected.Kind,
		Format: expected.Format, Mode: expected.Mode, SHA256: digest,
	})
	state.includedIDs[expected.ID] = struct{}{}
	return nil
}

// VerifyManifest checks contract identity, re-hashes every listed file, and
// proves that outputRoot contains no undeclared executor input.
func VerifyManifest(plan VerifiedPlan, root string, manifest ArtifactManifest) error {
	if err := validateManifest(manifest); err != nil {
		return err
	}
	if manifest.Binding != plan.Binding() {
		return fail(ErrBindingMismatch, "manifest.binding", "does not match the current ResolvedPlan")
	}
	if err := validateManifestAgainstPlan(plan, manifest); err != nil {
		return err
	}
	resolvedSeen := make(map[string]string, len(manifest.Artifacts))
	resolvedInfos := make([]os.FileInfo, 0, len(manifest.Artifacts))
	for i, artifact := range manifest.Artifacts {
		resolvedPath, resolvedKey, info, err := resolveArtifact(root, artifact.Path)
		if err != nil {
			return err
		}
		if previous, exists := resolvedSeen[resolvedKey]; exists {
			return fail(ErrDuplicateArtifact, fmt.Sprintf("manifest.artifacts[%d].path", i), "%q aliases already-listed artifact %q", artifact.Path, previous)
		}
		for index, previousInfo := range resolvedInfos {
			if os.SameFile(previousInfo, info) {
				return fail(ErrDuplicateArtifact, fmt.Sprintf("manifest.artifacts[%d].path", i), "%q aliases already-listed artifact %q", artifact.Path, manifest.Artifacts[index].Path)
			}
		}
		resolvedInfos = append(resolvedInfos, info)
		resolvedSeen[resolvedKey] = artifact.Path
		expected := plan.expectedArtifactByID(artifact.ID)
		if err := enforceArtifactMode(info, expected, artifact.Path); err != nil {
			return err
		}
		content, actual, err := readStableArtifact(root, artifact.Path, resolvedPath, info, expected.ID == "resolved-plan")
		if err != nil {
			return err
		}
		if expected.ID == "resolved-plan" && !bytes.Equal(content, plan.canonical) {
			return fail(ErrArtifactChanged, expected.Path, "resolved-plan artifact does not equal the verified canonical plan")
		}
		if actual != artifact.SHA256 {
			return fail(ErrArtifactChanged, artifact.Path, "declared %s, current file is %s", artifact.SHA256, actual)
		}
	}
	return verifyClosedExecutorTree(plan, root, manifest)
}

// NewReceipt binds one statically valid manifest to the same verified plan.
func NewReceipt(plan VerifiedPlan, manifest ArtifactManifest, generatedAt string) (GenerationReceipt, error) {
	if err := plan.RequireReady(ExecutionPhaseGeneration); err != nil {
		return GenerationReceipt{}, err
	}
	if err := validateManifest(manifest); err != nil {
		return GenerationReceipt{}, err
	}
	if manifest.Binding != plan.Binding() {
		return GenerationReceipt{}, fail(ErrBindingMismatch, "manifest.binding", "does not match the current ResolvedPlan")
	}
	if err := validateManifestAgainstPlan(plan, manifest); err != nil {
		return GenerationReceipt{}, err
	}
	manifestHash, err := manifest.Hash()
	if err != nil {
		return GenerationReceipt{}, err
	}
	receipt := GenerationReceipt{
		APIVersion:   GenerationReceiptAPIVersion,
		Kind:         GenerationReceiptKind,
		Binding:      plan.Binding(),
		ManifestHash: manifestHash,
		GeneratedAt:  generatedAt,
	}
	if err := validateReceiptContract(receipt); err != nil {
		return GenerationReceipt{}, err
	}
	return receipt, nil
}

// VerifyReceipt validates the plan and manifest identity. GeneratedAt is
// deliberately ignored and therefore cannot make a fresh or stale decision.
func VerifyReceipt(plan VerifiedPlan, manifest ArtifactManifest, receipt GenerationReceipt) error {
	if err := validateReceiptContract(receipt); err != nil {
		return err
	}
	if receipt.Binding != plan.Binding() {
		return fail(ErrBindingMismatch, "receipt.binding", "does not match the current ResolvedPlan")
	}
	if err := validateManifest(manifest); err != nil {
		return err
	}
	if manifest.Binding != receipt.Binding {
		return fail(ErrBindingMismatch, "manifest.binding", "does not match the receipt")
	}
	if err := validateManifestAgainstPlan(plan, manifest); err != nil {
		return err
	}
	want, err := manifest.Hash()
	if err != nil {
		return err
	}
	if !validSHA256(receipt.ManifestHash) || receipt.ManifestHash != want {
		return fail(ErrHashMismatch, "receipt.manifestHash", "declared %s, canonical manifest is %s", receipt.ManifestHash, want)
	}
	return nil
}

// ExecutionGateInput contains every input required for one non-composable
// execution authorization decision. CurrentCanonical must be the exact output
// of the current governed resolver invocation, not a previously persisted plan.
type ExecutionGateInput struct {
	CurrentCanonical []byte
	Plan             VerifiedPlan
	Phase            ExecutionPhase
	Versions         ComponentVersions
	Root             string
	Manifest         ArtifactManifest
	Receipt          GenerationReceipt
}

// VerifyExecution is the only top-level generation/apply authorization gate.
// Its order is intentional and stable: current resolver identity, phase,
// component compatibility, signed readiness, manifest bytes, receipt, then
// exact binding to the canonical control files beneath the governed root.
func VerifyExecution(input ExecutionGateInput) error {
	if err := input.Plan.VerifyCurrentResolution(input.CurrentCanonical); err != nil {
		return err
	}
	if input.Phase != ExecutionPhaseGeneration && input.Phase != ExecutionPhaseApply {
		return fail(ErrInvalidContract, "execution.phase", "must be generation or apply")
	}
	if err := input.Plan.VerifyCompatibility(input.Versions); err != nil {
		return err
	}
	if err := input.Plan.RequireReady(input.Phase); err != nil {
		return err
	}
	if err := VerifyManifest(input.Plan, input.Root, input.Manifest); err != nil {
		return err
	}
	if err := VerifyReceipt(input.Plan, input.Manifest, input.Receipt); err != nil {
		return err
	}
	return verifyCanonicalControls(input.Plan, input.Root, input.Manifest, input.Receipt)
}

func verifyCanonicalControls(plan VerifiedPlan, root string, manifest ArtifactManifest, receipt GenerationReceipt) error {
	_, manifestPath, receiptPath := plan.MetadataPaths(root)
	canonicalManifest, err := ReadManifest(manifestPath)
	if err != nil {
		return err
	}
	if err := requireSameCanonicalControl(manifestPath, "artifact manifest", canonicalManifest, manifest); err != nil {
		return err
	}
	canonicalReceipt, err := ReadReceipt(receiptPath)
	if err != nil {
		return err
	}
	return requireSameCanonicalControl(receiptPath, "generation receipt", canonicalReceipt, receipt)
}

func requireSameCanonicalControl(filePath, label string, persisted, verified interface {
	MarshalCanonical() ([]byte, error)
}) error {
	persistedBytes, err := persisted.MarshalCanonical()
	if err != nil {
		return err
	}
	verifiedBytes, err := verified.MarshalCanonical()
	if err != nil {
		return err
	}
	if !bytes.Equal(persistedBytes, verifiedBytes) {
		return fail(ErrArtifactChanged, filePath, "canonical %s does not equal the object accepted by the execution gate", label)
	}
	return nil
}

func validateManifestAgainstPlan(plan VerifiedPlan, manifest ArtifactManifest) error {
	included := make(map[string]struct{}, len(manifest.Artifacts))
	for i, artifact := range manifest.Artifacts {
		expected := plan.expectedArtifactByID(artifact.ID)
		if expected.ID == "" {
			return fail(ErrInvalidContract, fmt.Sprintf("manifest.artifacts[%d].id", i), "artifact %q is not declared by ResolvedPlan.generation.artifacts", artifact.ID)
		}
		if artifact.Path != expected.Path {
			return fail(ErrInvalidContract, fmt.Sprintf("manifest.artifacts[%d].path", i), "artifact %q must use governed path %q", artifact.ID, expected.Path)
		}
		if artifact.Kind != expected.Kind {
			return fail(ErrInvalidContract, fmt.Sprintf("manifest.artifacts[%d].kind", i), "artifact %q must use governed kind %q", artifact.ID, expected.Kind)
		}
		if artifact.Format != expected.Format {
			return fail(ErrInvalidContract, fmt.Sprintf("manifest.artifacts[%d].format", i), "artifact %q must use governed format %q", artifact.ID, expected.Format)
		}
		if artifact.Mode != expected.Mode {
			return fail(ErrInvalidContract, fmt.Sprintf("manifest.artifacts[%d].mode", i), "artifact %q must use governed mode %q", artifact.ID, expected.Mode)
		}
		included[expected.ID] = struct{}{}
	}
	for _, expected := range plan.expectedArtifacts {
		if expected.Required {
			if _, exists := included[expected.ID]; !exists {
				return fail(ErrArtifactMissing, expected.Path, "required governed artifact %q is absent from manifest", expected.ID)
			}
		}
	}
	return nil
}

func (p VerifiedPlan) expectedArtifactByID(id string) expectedArtifact {
	for _, expected := range p.expectedArtifacts {
		if expected.ID == id {
			return expected
		}
	}
	return expectedArtifact{}
}

func (m ArtifactManifest) MarshalCanonical() ([]byte, error) {
	if err := validateManifest(m); err != nil {
		return nil, err
	}
	return resolvedplan.CanonicalJSON(m)
}

func (m ArtifactManifest) Hash() (string, error) {
	if err := validateManifest(m); err != nil {
		return "", err
	}
	return resolvedplan.CanonicalSHA256(m)
}

func (r GenerationReceipt) MarshalCanonical() ([]byte, error) {
	if err := validateReceiptContract(r); err != nil {
		return nil, err
	}
	return resolvedplan.CanonicalJSON(r)
}

func validateReceiptContract(receipt GenerationReceipt) error {
	if receipt.APIVersion != GenerationReceiptAPIVersion || receipt.Kind != GenerationReceiptKind {
		return fail(ErrInvalidContract, "receipt", "must be %s %s", GenerationReceiptAPIVersion, GenerationReceiptKind)
	}
	if err := receipt.Binding.validate("receipt.binding"); err != nil {
		return err
	}
	if !validSHA256(receipt.ManifestHash) {
		return fail(ErrInvalidContract, "receipt.manifestHash", "must be a lowercase sha256:<64-hex> digest")
	}
	if receipt.GeneratedAt != "" {
		if _, err := time.Parse(time.RFC3339Nano, receipt.GeneratedAt); err != nil {
			return wrap(ErrInvalidContract, "receipt.generatedAt", "must be RFC3339 when present", err)
		}
	}
	return nil
}

func validateManifest(manifest ArtifactManifest) error {
	if manifest.APIVersion != ArtifactManifestAPIVersion || manifest.Kind != ArtifactManifestKind {
		return fail(ErrInvalidContract, "manifest", "must be %s %s", ArtifactManifestAPIVersion, ArtifactManifestKind)
	}
	if err := manifest.Binding.validate("manifest.binding"); err != nil {
		return err
	}
	if len(manifest.Artifacts) == 0 {
		return fail(ErrInvalidContract, "manifest.artifacts", "at least one rendered artifact is required")
	}
	seen := make(map[string]struct{}, len(manifest.Artifacts))
	seenIDs := make(map[string]struct{}, len(manifest.Artifacts))
	var previous []byte
	for i, artifact := range manifest.Artifacts {
		if strings.TrimSpace(artifact.ID) == "" {
			return fail(ErrInvalidContract, fmt.Sprintf("manifest.artifacts[%d].id", i), "governed artifact ID is required")
		}
		if !validArtifactKind(artifact.Kind) {
			return fail(ErrInvalidContract, fmt.Sprintf("manifest.artifacts[%d].kind", i), "unsupported artifact kind %q", artifact.Kind)
		}
		if !validArtifactFormat(artifact.Format) {
			return fail(ErrInvalidContract, fmt.Sprintf("manifest.artifacts[%d].format", i), "unsupported artifact format %q", artifact.Format)
		}
		if _, err := parseArtifactMode(artifact.Mode); err != nil {
			return wrap(ErrInvalidContract, fmt.Sprintf("manifest.artifacts[%d].mode", i), "invalid artifact mode", err)
		}
		if _, exists := seenIDs[artifact.ID]; exists {
			return fail(ErrDuplicateArtifact, fmt.Sprintf("manifest.artifacts[%d].id", i), "duplicate artifact ID %q", artifact.ID)
		}
		seenIDs[artifact.ID] = struct{}{}
		canonicalPath, err := validatePortablePath(artifact.Path)
		if err != nil {
			return err
		}
		if canonicalPath != artifact.Path {
			return fail(ErrInvalidPath, fmt.Sprintf("manifest.artifacts[%d].path", i), "path is not canonical")
		}
		key := portablePathKey(artifact.Path)
		if _, exists := seen[key]; exists {
			return fail(ErrDuplicateArtifact, fmt.Sprintf("manifest.artifacts[%d].path", i), "duplicate path %q", artifact.Path)
		}
		seen[key] = struct{}{}
		orderKey, err := resolvedplan.CanonicalJSON(artifact)
		if err != nil {
			return wrap(ErrInvalidContract, fmt.Sprintf("manifest.artifacts[%d]", i), "cannot canonicalize artifact", err)
		}
		if previous != nil && bytes.Compare(previous, orderKey) >= 0 {
			return fail(ErrInvalidContract, "manifest.artifacts", "entries must use ascending resolvedplan canonical set order")
		}
		previous = orderKey
		if !validSHA256(artifact.SHA256) {
			return fail(ErrInvalidContract, fmt.Sprintf("manifest.artifacts[%d].sha256", i), "must be a lowercase sha256:<64-hex> digest")
		}
	}
	return nil
}

func parseArtifactMode(value string) (os.FileMode, error) {
	if len(value) != 4 || value[0] != '0' {
		return 0, fmt.Errorf("mode %q must use four-digit octal form", value)
	}
	parsed, err := strconv.ParseUint(value, 8, 9)
	if err != nil {
		return 0, fmt.Errorf("mode %q is not octal: %w", value, err)
	}
	return os.FileMode(parsed), nil
}

func enforceArtifactMode(info os.FileInfo, expected expectedArtifact, artifactPath string) error {
	want, err := parseArtifactMode(expected.Mode)
	if err != nil {
		return wrap(ErrInvalidPlan, artifactPath, "invalid governed artifact mode", err)
	}
	if runtime.GOOS == "windows" {
		// Windows does not expose POSIX permission bits. Regular-file, symlink,
		// identity, byte hash, and the governed intended mode remain enforced;
		// no synthesized Go FileMode is presented as POSIX proof.
		return nil
	}
	if got := info.Mode().Perm(); got != want.Perm() {
		return fail(ErrArtifactChanged, artifactPath, "permission mode is %04o, governed contract requires %04o", got, want.Perm())
	}
	return nil
}

func validArtifactKind(value string) bool {
	switch value {
	case "opentofu", "compose", "metadata", "script", "native-config":
		return true
	default:
		return false
	}
}

func validArtifactFormat(value string) bool {
	switch value {
	case "json", "yaml", "hcl", "shell", "text":
		return true
	default:
		return false
	}
}

func validatePortablePath(value string) (string, error) {
	if value == "" || strings.ContainsRune(value, '\x00') || strings.Contains(value, `\`) || strings.ContainsAny(value, `<>:"|?*`) {
		return "", fail(ErrInvalidPath, "artifact.path", "must be a non-empty portable slash-separated relative path")
	}
	if strings.HasPrefix(value, "/") || (len(value) >= 2 && value[1] == ':' && ((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z'))) {
		return "", fail(ErrPathEscape, "artifact.path", "absolute, drive-relative, and UNC paths are forbidden: %q", value)
	}
	clean := path.Clean(value)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || clean != value {
		return "", fail(ErrPathEscape, "artifact.path", "path must be canonical and remain beneath the artifact root: %q", value)
	}
	for _, segment := range strings.Split(clean, "/") {
		if strings.TrimRight(segment, ". ") != segment || isWindowsReservedSegment(segment) {
			return "", fail(ErrInvalidPath, "artifact.path", "path is not portable to Windows: %q", value)
		}
	}
	return clean, nil
}

func isWindowsReservedSegment(segment string) bool {
	base := strings.ToUpper(strings.SplitN(segment, ".", 2)[0])
	switch base {
	case "CON", "PRN", "AUX", "NUL", "COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9", "LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		return true
	default:
		return false
	}
}

func resolveArtifact(root, relativePath string) (string, string, os.FileInfo, error) {
	if _, err := validatePortablePath(relativePath); err != nil {
		return "", "", nil, err
	}
	absRoot, err := validateArtifactRoot(root)
	if err != nil {
		return "", "", nil, err
	}
	candidate, err := validateArtifactComponents(absRoot, relativePath)
	if err != nil {
		return "", "", nil, err
	}
	info, err := os.Stat(candidate)
	if err != nil {
		if os.IsNotExist(err) {
			return "", "", nil, wrap(ErrArtifactMissing, relativePath, "rendered artifact does not exist", err)
		}
		return "", "", nil, wrap(ErrIO, relativePath, "stat rendered artifact", err)
	}
	if !info.Mode().IsRegular() {
		return "", "", nil, fail(ErrInvalidPath, relativePath, "rendered artifact is not a regular file")
	}
	key := filepath.Clean(candidate)
	if runtime.GOOS == "windows" {
		key = strings.ToLower(key)
	}
	return candidate, key, info, nil
}

func validateArtifactRoot(root string) (string, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", wrap(ErrIO, root, "resolve artifact root", err)
	}
	if err := rejectAnySymlinkInChain(absRoot); err != nil {
		return "", err
	}
	rootInfo, err := os.Lstat(absRoot)
	if err != nil {
		return "", wrap(ErrIO, root, "stat artifact root", err)
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 {
		return "", fail(ErrPathEscape, root, "artifact root must not be a symlink")
	}
	if !rootInfo.IsDir() {
		return "", fail(ErrInvalidPath, root, "artifact root is not a directory")
	}
	return absRoot, nil
}

func validateArtifactComponents(absRoot, relativePath string) (string, error) {
	candidate := filepath.Join(absRoot, filepath.FromSlash(relativePath))
	contained, err := filepath.Rel(absRoot, candidate)
	if err != nil || contained == ".." || strings.HasPrefix(contained, ".."+string(filepath.Separator)) || filepath.IsAbs(contained) {
		return "", fail(ErrPathEscape, relativePath, "artifact escapes root")
	}
	current := absRoot
	parts := strings.Split(filepath.FromSlash(relativePath), string(filepath.Separator))
	for index, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			if os.IsNotExist(err) {
				return "", wrap(ErrArtifactMissing, relativePath, "rendered artifact does not exist", err)
			}
			return "", wrap(ErrIO, relativePath, "inspect rendered artifact path", err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fail(ErrPathEscape, relativePath, "artifact file and parent components must not be symlinks")
		}
		if index < len(parts)-1 && !info.IsDir() {
			return "", fail(ErrInvalidPath, relativePath, "artifact parent component is not a directory")
		}
	}
	return candidate, nil
}

func readStableArtifact(root, relativePath, filePath string, expectedInfo os.FileInfo, capture bool) ([]byte, string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, "", wrap(ErrArtifactMissing, relativePath, "open rendered artifact", err)
		}
		return nil, "", wrap(ErrIO, relativePath, "open rendered artifact", err)
	}
	defer func() { _ = file.Close() }()
	openedInfo, err := file.Stat()
	if err != nil {
		return nil, "", wrap(ErrIO, relativePath, "stat opened rendered artifact", err)
	}
	if !os.SameFile(expectedInfo, openedInfo) {
		return nil, "", fail(ErrArtifactChanged, relativePath, "artifact changed between path validation and open")
	}
	hash := sha256.New()
	var content []byte
	if capture {
		content, err = io.ReadAll(file)
		if err == nil {
			_, err = hash.Write(content)
		}
	} else {
		_, err = io.Copy(hash, file)
	}
	if err != nil {
		return nil, "", wrap(ErrIO, relativePath, "hash rendered artifact", err)
	}
	afterReadInfo, err := file.Stat()
	if err != nil {
		return nil, "", wrap(ErrIO, relativePath, "restat opened rendered artifact", err)
	}
	if !os.SameFile(openedInfo, afterReadInfo) || openedInfo.Size() != afterReadInfo.Size() || !openedInfo.ModTime().Equal(afterReadInfo.ModTime()) {
		return nil, "", fail(ErrArtifactChanged, relativePath, "artifact changed while it was being hashed")
	}
	_, _, currentInfo, err := resolveArtifact(root, relativePath)
	if err != nil {
		return nil, "", err
	}
	if !os.SameFile(openedInfo, currentInfo) {
		return nil, "", fail(ErrArtifactChanged, relativePath, "artifact path changed while it was being hashed")
	}
	return content, "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

func portablePathKey(value string) string {
	// Governed artifact paths must retain one identity across every supported
	// host. Always fold case so a manifest accepted on a case-sensitive build
	// host cannot become ambiguous when consumed on Windows.
	return strings.ToLower(value)
}

func validSHA256(value string) bool {
	if len(value) != len("sha256:")+sha256.Size*2 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil && value == strings.ToLower(value)
}
