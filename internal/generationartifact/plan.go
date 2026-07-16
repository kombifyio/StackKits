package generationartifact

import (
	"bytes"
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

// RendererIdentity is copied from ResolvedPlan.generation.renderer. Changing
// either value invalidates every manifest and receipt made for the old plan.
type RendererIdentity struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}

// PlanBinding contains every source and implementation identity required to
// prove that generated files belong to exactly one compiler result.
type PlanBinding struct {
	PlanHash        string                     `json:"planHash"`
	SpecHash        string                     `json:"specHash"`
	InventoryHash   string                     `json:"inventoryHash"`
	DefinitionHash  string                     `json:"definitionHash"`
	CompilerVersion string                     `json:"compilerVersion"`
	Renderer        RendererIdentity           `json:"renderer"`
	Authority       resolvedplan.PlanAuthority `json:"authority"`
}

// ComponentVersions are the binaries participating in a generation/apply
// decision. All three are required so a caller cannot accidentally skip one
// of the ResolvedPlan compatibility minima.
type ComponentVersions struct {
	CLI       string
	Generator string
	Runtime   string
}

type minimumVersions struct {
	CLI       string
	Generator string
	Runtime   string
}

type expectedArtifact struct {
	ID       string
	Path     string
	Kind     string
	Format   string
	Mode     string
	Required bool
}

// ExecutionPhase selects the governed readiness decision required by an
// execution command. Plan consumes generated artifacts and therefore uses the
// generation phase; apply additionally requires the apply phase.
type ExecutionPhase string

const (
	ExecutionPhaseGeneration ExecutionPhase = "generation"
	ExecutionPhaseApply      ExecutionPhase = "apply"
)

// ReadinessBlocker is the stable public projection of one ResolvedPlan
// execution blocker. Refs retain the governed provider/module/evidence IDs.
type ReadinessBlocker struct {
	Code string
	Refs []string
}

type readinessPhase struct {
	status   string
	blockers []ReadinessBlocker
}

// VerifiedPlan is produced only after canonical JSON, self-declared plan hash,
// and the concrete governed CUE #ResolvedPlan contract all pass. Its data is
// kept private so callers cannot mutate a verified map after that decision.
type VerifiedPlan struct {
	canonical         []byte
	binding           PlanBinding
	minimumVersions   minimumVersions
	expectedArtifacts []expectedArtifact
	outputRoot        string
	readiness         map[ExecutionPhase]readinessPhase
}

// VerifyPlan verifies a byte-for-byte canonical Architecture v2 ResolvedPlan
// against the non-substitutable CUE authority. A self-consistent hash is only
// integrity evidence, never schema authority. Legacy StackSpec v1 documents
// are neither accepted nor projected.
func VerifyPlan(data []byte, validator *resolvedplan.CUEContractValidator) (VerifiedPlan, error) {
	if validator == nil {
		return VerifiedPlan{}, fail(ErrInvalidPlan, "resolvedPlan.validator", "a concrete CUE contract validator is required")
	}
	plan, err := resolvedplan.DecodeCanonicalPlan(data)
	if err != nil {
		return VerifiedPlan{}, wrap(ErrInvalidPlan, "resolvedPlan", "canonical plan verification failed", err)
	}
	if err := validator.ValidateCanonicalPlan(plan); err != nil {
		return VerifiedPlan{}, wrap(ErrInvalidPlan, "resolvedPlan", "governed CUE #ResolvedPlan validation failed", err)
	}
	binding, err := bindingFromPlan(plan)
	if err != nil {
		return VerifiedPlan{}, err
	}
	minimums, err := minimumVersionsFromPlan(plan)
	if err != nil {
		return VerifiedPlan{}, err
	}
	outputRoot, readiness, err := executionMetadataFromPlan(plan)
	if err != nil {
		return VerifiedPlan{}, err
	}
	artifacts, err := expectedArtifactsFromPlan(plan, outputRoot)
	if err != nil {
		return VerifiedPlan{}, err
	}
	return VerifiedPlan{
		canonical:         append([]byte(nil), data...),
		binding:           binding,
		minimumVersions:   minimums,
		expectedArtifacts: artifacts,
		outputRoot:        outputRoot,
		readiness:         readiness,
	}, nil
}

// ReadPlan reads and verifies a persisted canonical plan.
func ReadPlan(path string, validator *resolvedplan.CUEContractValidator) (VerifiedPlan, error) {
	data, err := readRegularNoSymlink(path, "ResolvedPlan")
	if err != nil {
		return VerifiedPlan{}, err
	}
	return VerifyPlan(data, validator)
}

// PersistPlan verifies before writing and atomically installs the canonical
// bytes with mode 0600. Invalid or stale input can never replace the target.
func PersistPlan(path string, canonical []byte, validator *resolvedplan.CUEContractValidator) (VerifiedPlan, error) {
	plan, err := VerifyPlan(canonical, validator)
	if err != nil {
		return VerifiedPlan{}, err
	}
	if err := persist0600(path, plan.canonical); err != nil {
		return VerifiedPlan{}, err
	}
	return plan, nil
}

// Canonical returns a defensive copy of the verified canonical plan.
func (p VerifiedPlan) Canonical() []byte {
	return append([]byte(nil), p.canonical...)
}

// Binding returns the immutable identities extracted from the verified plan.
func (p VerifiedPlan) Binding() PlanBinding { return p.binding }

// OutputRoot is the portable path selected by ResolvedPlan.generation. It is
// relative to the deployment workspace; "." means the workspace itself.
func (p VerifiedPlan) OutputRoot() string { return p.outputRoot }

// MetadataPaths derives the plan/manifest/receipt locations from outputRoot.
// The latter two are control metadata and are never renderer artifacts.
func (p VerifiedPlan) MetadataPaths(workspaceRoot string) (planPath, manifestPath, receiptPath string) {
	metadataRoot := filepath.Join(workspaceRoot, filepath.FromSlash(p.outputRoot), ".stackkit")
	return filepath.Join(metadataRoot, "resolved-plan.json"),
		filepath.Join(metadataRoot, ArtifactManifestFileName),
		filepath.Join(metadataRoot, GenerationReceiptFileName)
}

// VerifyCurrentResolution rejects even a valid old plan when it is not the
// exact canonical output of the current StackSpec, Inventory, authority, and
// compiler contract.
func (p VerifiedPlan) VerifyCurrentResolution(canonical []byte) error {
	if !bytes.Equal(p.canonical, canonical) {
		return fail(ErrBindingMismatch, "resolvedPlan", "persisted plan does not equal the current canonical resolution")
	}
	return nil
}

// RequireReady enforces the signed readiness decision embedded by the
// compiler. Blocker codes and refs are included deterministically for operator
// diagnostics while callers classify the typed ErrorCode.
func (p VerifiedPlan) RequireReady(phase ExecutionPhase) error {
	decision, exists := p.readiness[phase]
	if !exists {
		return fail(ErrInvalidPlan, "resolvedPlan.executionReadiness", "phase %q is not governed", phase)
	}
	if decision.status == "ready" {
		return nil
	}
	parts := make([]string, 0, len(decision.blockers))
	for _, blocker := range decision.blockers {
		part := blocker.Code
		if len(blocker.Refs) > 0 {
			part += "[" + strings.Join(blocker.Refs, ",") + "]"
		}
		parts = append(parts, part)
	}
	return &Error{
		Code:     ErrReadinessBlocked,
		Path:     "resolvedPlan.executionReadiness." + string(phase),
		Message:  "blocked by " + strings.Join(parts, "; "),
		Phase:    phase,
		Blockers: cloneReadinessBlockers(decision.blockers),
	}
}

func cloneReadinessBlockers(blockers []ReadinessBlocker) []ReadinessBlocker {
	cloned := make([]ReadinessBlocker, len(blockers))
	for index, blocker := range blockers {
		cloned[index] = ReadinessBlocker{Code: blocker.Code, Refs: append([]string(nil), blocker.Refs...)}
	}
	return cloned
}

// RendererNotImplemented reports the intentional boundary between a ready
// governed plan and a concrete v2 renderer. It must be returned before any
// legacy StackSpec v1 generator is entered.
func RendererNotImplemented(renderer RendererIdentity) error {
	return fail(ErrRendererMissing, "resolvedPlan.generation.renderer", "Architecture v2 renderer %s@%s is not implemented", renderer.ID, renderer.Version)
}

// ExecutorNotImplemented prevents a verified v2 artifact set from falling
// through to an executor initialized from the legacy StackSpec v1 model.
func ExecutorNotImplemented(renderer RendererIdentity) error {
	return fail(ErrExecutorMissing, "resolvedPlan.generation.renderer", "Architecture v2 executor adapter for %s@%s is not implemented", renderer.ID, renderer.Version)
}

// VerifyCompatibility checks every plan minimum using the compiler's SemVer
// implementation. An empty or malformed current component version fails.
func (p VerifiedPlan) VerifyCompatibility(actual ComponentVersions) error {
	checks := []struct {
		name    string
		actual  string
		minimum string
	}{
		{name: "cli", actual: actual.CLI, minimum: p.minimumVersions.CLI},
		{name: "generator", actual: actual.Generator, minimum: p.minimumVersions.Generator},
		{name: "runtime", actual: actual.Runtime, minimum: p.minimumVersions.Runtime},
	}
	for _, check := range checks {
		compatible, err := resolvedplan.VersionAtLeast(check.actual, check.minimum)
		if err != nil {
			return wrap(ErrIncompatible, "compatibility."+check.name, "cannot compare component version", err)
		}
		if !compatible {
			return fail(ErrIncompatible, "compatibility."+check.name, "version %s is below required %s", check.actual, check.minimum)
		}
	}
	return nil
}

//nolint:gocyclo // Binding extraction verifies every repeated provenance field before an untrusted resolved plan can reach a renderer.
func bindingFromPlan(plan resolvedplan.ResolvedPlan) (PlanBinding, error) {
	binding := PlanBinding{}
	var err error
	if binding.PlanHash, err = requiredString(plan, "planHash", "resolvedPlan.planHash"); err != nil {
		return PlanBinding{}, err
	}
	if binding.SpecHash, err = requiredString(plan, "specHash", "resolvedPlan.specHash"); err != nil {
		return PlanBinding{}, err
	}
	if binding.InventoryHash, err = requiredString(plan, "inventoryHash", "resolvedPlan.inventoryHash"); err != nil {
		return PlanBinding{}, err
	}
	if binding.CompilerVersion, err = requiredString(plan, "compilerVersion", "resolvedPlan.compilerVersion"); err != nil {
		return PlanBinding{}, err
	}
	authority, err := requiredObject(plan, "authority", "resolvedPlan.authority")
	if err != nil {
		return PlanBinding{}, err
	}
	if binding.Authority.Class, err = requiredString(authority, "class", "resolvedPlan.authority.class"); err != nil {
		return PlanBinding{}, err
	}
	if binding.Authority.Document, err = requiredString(authority, "document", "resolvedPlan.authority.document"); err != nil {
		return PlanBinding{}, err
	}
	if binding.Authority.Issuer, err = requiredString(authority, "issuer", "resolvedPlan.authority.issuer"); err != nil {
		return PlanBinding{}, err
	}
	if fingerprint, exists := authority["authorityFingerprint"]; exists {
		var ok bool
		binding.Authority.AuthorityFingerprint, ok = fingerprint.(string)
		if !ok || binding.Authority.AuthorityFingerprint == "" {
			return PlanBinding{}, fail(ErrInvalidPlan, "resolvedPlan.authority.authorityFingerprint", "must be a non-empty string when present")
		}
	}
	if binding.Authority.CatalogHash, err = requiredString(authority, "catalogHash", "resolvedPlan.authority.catalogHash"); err != nil {
		return PlanBinding{}, err
	}
	graduationEligible, ok := authority["graduationEligible"].(bool)
	if !ok {
		return PlanBinding{}, fail(ErrInvalidPlan, "resolvedPlan.authority.graduationEligible", "must be boolean")
	}
	binding.Authority.GraduationEligible = graduationEligible
	if err := validateBindingAuthority(binding.Authority, "resolvedPlan.authority"); err != nil {
		return PlanBinding{}, err
	}

	kit, err := requiredObject(plan, "kit", "resolvedPlan.kit")
	if err != nil {
		return PlanBinding{}, err
	}
	if binding.DefinitionHash, err = requiredString(kit, "definitionHash", "resolvedPlan.kit.definitionHash"); err != nil {
		return PlanBinding{}, err
	}
	generation, err := requiredObject(plan, "generation", "resolvedPlan.generation")
	if err != nil {
		return PlanBinding{}, err
	}
	renderer, err := requiredObject(generation, "renderer", "resolvedPlan.generation.renderer")
	if err != nil {
		return PlanBinding{}, err
	}
	if binding.Renderer.ID, err = requiredString(renderer, "id", "resolvedPlan.generation.renderer.id"); err != nil {
		return PlanBinding{}, err
	}
	if binding.Renderer.Version, err = requiredString(renderer, "version", "resolvedPlan.generation.renderer.version"); err != nil {
		return PlanBinding{}, err
	}
	profileContractHash, err := requiredString(generation, "profileContractHash", "resolvedPlan.generation.profileContractHash")
	if err != nil {
		return PlanBinding{}, err
	}
	if profileContractHash != binding.DefinitionHash {
		return PlanBinding{}, fail(ErrBindingMismatch, "resolvedPlan.generation.profileContractHash", "does not match kit.definitionHash")
	}

	for path, digest := range map[string]string{
		"resolvedPlan.planHash":       binding.PlanHash,
		"resolvedPlan.specHash":       binding.SpecHash,
		"resolvedPlan.inventoryHash":  binding.InventoryHash,
		"resolvedPlan.definitionHash": binding.DefinitionHash,
	} {
		if !validSHA256(digest) {
			return PlanBinding{}, fail(ErrInvalidPlan, path, "must be a lowercase sha256:<64-hex> digest")
		}
	}

	// The compiler repeats provenance hashes inside source. Validate those
	// cross-references here so a renderer never receives internally divergent
	// source identities, even when a caller recomputed planHash after tampering.
	source, err := requiredObject(plan, "source", "resolvedPlan.source")
	if err != nil {
		return PlanBinding{}, err
	}
	if err := requireEqualNestedHash(source, "normalizedSpec", binding.SpecHash, "resolvedPlan.source.normalizedSpec"); err != nil {
		return PlanBinding{}, err
	}
	if err := requireEqualNestedHash(source, "inventory", binding.InventoryHash, "resolvedPlan.source.inventory"); err != nil {
		return PlanBinding{}, err
	}
	sourceDefinition, err := requiredString(source, "kitDefinitionHash", "resolvedPlan.source.kitDefinitionHash")
	if err != nil {
		return PlanBinding{}, err
	}
	if sourceDefinition != binding.DefinitionHash {
		return PlanBinding{}, fail(ErrBindingMismatch, "resolvedPlan.source.kitDefinitionHash", "does not match kit.definitionHash")
	}
	return binding, nil
}

func minimumVersionsFromPlan(plan resolvedplan.ResolvedPlan) (minimumVersions, error) {
	compatibility, err := requiredObject(plan, "compatibility", "resolvedPlan.compatibility")
	if err != nil {
		return minimumVersions{}, err
	}
	minimums := minimumVersions{}
	if minimums.CLI, err = requiredString(compatibility, "minCLI", "resolvedPlan.compatibility.minCLI"); err != nil {
		return minimumVersions{}, err
	}
	if minimums.Generator, err = requiredString(compatibility, "minGenerator", "resolvedPlan.compatibility.minGenerator"); err != nil {
		return minimumVersions{}, err
	}
	if minimums.Runtime, err = requiredString(compatibility, "minRuntime", "resolvedPlan.compatibility.minRuntime"); err != nil {
		return minimumVersions{}, err
	}
	for path, version := range map[string]string{
		"resolvedPlan.compatibility.minCLI":       minimums.CLI,
		"resolvedPlan.compatibility.minGenerator": minimums.Generator,
		"resolvedPlan.compatibility.minRuntime":   minimums.Runtime,
	} {
		if _, err := resolvedplan.VersionAtLeast(version, version); err != nil {
			return minimumVersions{}, wrap(ErrInvalidPlan, path, "invalid semantic version", err)
		}
	}
	return minimums, nil
}

func expectedArtifactsFromPlan(plan resolvedplan.ResolvedPlan, outputRoot string) ([]expectedArtifact, error) {
	generation, err := requiredObject(plan, "generation", "resolvedPlan.generation")
	if err != nil {
		return nil, err
	}
	rawArtifacts, ok := generation["artifacts"].([]any)
	if !ok || len(rawArtifacts) == 0 {
		return nil, fail(ErrInvalidPlan, "resolvedPlan.generation.artifacts", "must contain governed artifact contracts")
	}
	artifacts := make([]expectedArtifact, 0, len(rawArtifacts))
	ids := make(map[string]struct{}, len(rawArtifacts))
	paths := make(map[string]struct{}, len(rawArtifacts))
	resolvedPlanContracts := 0
	for i, rawArtifact := range rawArtifacts {
		expected, err := parseExpectedArtifact(rawArtifact, i, outputRoot)
		if err != nil {
			return nil, err
		}
		artifactPath := fmt.Sprintf("resolvedPlan.generation.artifacts[%d]", i)
		if _, exists := ids[expected.ID]; exists {
			return nil, fail(ErrInvalidPlan, artifactPath+".id", "duplicate governed artifact ID %q", expected.ID)
		}
		ids[expected.ID] = struct{}{}
		pathKey := portablePathKey(expected.Path)
		if _, exists := paths[pathKey]; exists {
			return nil, fail(ErrInvalidPlan, artifactPath+".path", "duplicate governed artifact path %q", expected.Path)
		}
		paths[pathKey] = struct{}{}
		if expected.ID == "resolved-plan" {
			resolvedPlanContracts++
		}
		artifacts = append(artifacts, expected)
	}
	if resolvedPlanContracts != 1 {
		return nil, fail(ErrInvalidPlan, "resolvedPlan.generation.artifacts", "must declare exactly one required resolved-plan artifact")
	}
	if err := validateArtifactPathHierarchy(artifacts, outputRoot); err != nil {
		return nil, err
	}
	return artifacts, nil
}

func validateArtifactPathHierarchy(artifacts []expectedArtifact, outputRoot string) error {
	files := make(map[string]expectedArtifact, len(artifacts))
	for _, artifact := range artifacts {
		files[portablePathKey(artifact.Path)] = artifact
	}
	for _, artifact := range artifacts {
		for directory := path.Dir(artifact.Path); ; directory = path.Dir(directory) {
			if owner, conflict := files[portablePathKey(directory)]; conflict {
				return fail(
					ErrInvalidPlan,
					"resolvedPlan.generation.artifacts",
					"artifact %q requires %q as a directory, but artifact %q declares the same portable path as a file",
					artifact.ID,
					directory,
					owner.ID,
				)
			}
			if directory == outputRoot || (outputRoot == "." && directory == ".") {
				break
			}
		}
	}
	return nil
}

func parseExpectedArtifact(raw any, index int, outputRoot string) (expectedArtifact, error) {
	artifactPath := fmt.Sprintf("resolvedPlan.generation.artifacts[%d]", index)
	object, ok := raw.(map[string]any)
	if !ok {
		return expectedArtifact{}, fail(ErrInvalidPlan, artifactPath, "must be an object")
	}
	result := expectedArtifact{}
	var err error
	if result.ID, err = requiredString(object, "id", artifactPath+".id"); err != nil {
		return expectedArtifact{}, err
	}
	if result.Path, err = requiredString(object, "path", artifactPath+".path"); err != nil {
		return expectedArtifact{}, err
	}
	if _, err := validatePortablePath(result.Path); err != nil {
		return expectedArtifact{}, wrap(ErrInvalidPlan, artifactPath+".path", "invalid governed artifact path", err)
	}
	if !pathWithinOutputRoot(outputRoot, result.Path) {
		return expectedArtifact{}, fail(ErrInvalidPlan, artifactPath+".path", "governed artifact %q is outside outputRoot %q", result.Path, outputRoot)
	}
	if result.Kind, err = requiredString(object, "kind", artifactPath+".kind"); err != nil {
		return expectedArtifact{}, err
	}
	if result.Format, err = requiredString(object, "format", artifactPath+".format"); err != nil {
		return expectedArtifact{}, err
	}
	if result.Mode, err = requiredString(object, "mode", artifactPath+".mode"); err != nil {
		return expectedArtifact{}, err
	}
	if _, err := parseArtifactMode(result.Mode); err != nil {
		return expectedArtifact{}, wrap(ErrInvalidPlan, artifactPath+".mode", "invalid governed artifact mode", err)
	}
	result.Required, ok = object["required"].(bool)
	if !ok {
		return expectedArtifact{}, fail(ErrInvalidPlan, artifactPath+".required", "must be boolean")
	}
	metadataRelative := path.Join(outputRoot, ".stackkit")
	if outputRoot == "." {
		metadataRelative = ".stackkit"
	}
	manifestPath := path.Join(metadataRelative, ArtifactManifestFileName)
	receiptPath := path.Join(metadataRelative, GenerationReceiptFileName)
	if portablePathKey(result.Path) == portablePathKey(manifestPath) || portablePathKey(result.Path) == portablePathKey(receiptPath) {
		return expectedArtifact{}, fail(ErrInvalidPlan, artifactPath+".path", "generation manifest and receipt are control metadata and cannot recursively be renderer artifacts")
	}
	if result.ID == "resolved-plan" {
		wantPath := path.Join(outputRoot, ".stackkit", "resolved-plan.json")
		if outputRoot == "." {
			wantPath = ".stackkit/resolved-plan.json"
		}
		if !result.Required || result.Path != wantPath {
			return expectedArtifact{}, fail(ErrInvalidPlan, artifactPath, "resolved-plan must be required at <outputRoot>/.stackkit/resolved-plan.json")
		}
	}
	return result, nil
}

func executionMetadataFromPlan(plan resolvedplan.ResolvedPlan) (string, map[ExecutionPhase]readinessPhase, error) {
	generation, err := requiredObject(plan, "generation", "resolvedPlan.generation")
	if err != nil {
		return "", nil, err
	}
	outputRoot, err := requiredString(generation, "outputRoot", "resolvedPlan.generation.outputRoot")
	if err != nil {
		return "", nil, err
	}
	if outputRoot != "." {
		if _, err := validatePortablePath(outputRoot); err != nil {
			return "", nil, wrap(ErrInvalidPlan, "resolvedPlan.generation.outputRoot", "invalid portable output root", err)
		}
	}

	readiness, err := requiredObject(plan, "executionReadiness", "resolvedPlan.executionReadiness")
	if err != nil {
		return "", nil, err
	}
	contractVersion, err := requiredString(readiness, "contractVersion", "resolvedPlan.executionReadiness.contractVersion")
	if err != nil {
		return "", nil, err
	}
	if contractVersion != "1.0.0" {
		return "", nil, fail(ErrInvalidPlan, "resolvedPlan.executionReadiness.contractVersion", "unsupported contract version %q", contractVersion)
	}
	phases := make(map[ExecutionPhase]readinessPhase, 2)
	for _, phase := range []ExecutionPhase{ExecutionPhaseGeneration, ExecutionPhaseApply} {
		decision, err := readinessPhaseFromPlan(readiness, phase)
		if err != nil {
			return "", nil, err
		}
		phases[phase] = decision
	}
	return outputRoot, phases, nil
}

func readinessPhaseFromPlan(readiness map[string]any, phase ExecutionPhase) (readinessPhase, error) {
	phasePath := "resolvedPlan.executionReadiness." + string(phase)
	object, err := requiredObject(readiness, string(phase), phasePath)
	if err != nil {
		return readinessPhase{}, err
	}
	status, err := requiredString(object, "status", phasePath+".status")
	if err != nil {
		return readinessPhase{}, err
	}
	if status != "ready" && status != "blocked" {
		return readinessPhase{}, fail(ErrInvalidPlan, phasePath+".status", "must be ready or blocked")
	}
	rawBlockers, ok := object["blockers"].([]any)
	if !ok {
		return readinessPhase{}, fail(ErrInvalidPlan, phasePath+".blockers", "must be an array")
	}
	blockers := make([]ReadinessBlocker, 0, len(rawBlockers))
	for index, raw := range rawBlockers {
		blockerPath := fmt.Sprintf("%s.blockers[%d]", phasePath, index)
		blocker, ok := raw.(map[string]any)
		if !ok {
			return readinessPhase{}, fail(ErrInvalidPlan, blockerPath, "must be an object")
		}
		code, err := requiredString(blocker, "code", blockerPath+".code")
		if err != nil {
			return readinessPhase{}, err
		}
		rawRefs, ok := blocker["refs"].([]any)
		if !ok || len(rawRefs) == 0 {
			return readinessPhase{}, fail(ErrInvalidPlan, blockerPath+".refs", "must be a non-empty array")
		}
		refs := make([]string, 0, len(rawRefs))
		for refIndex, rawRef := range rawRefs {
			ref, ok := rawRef.(string)
			if !ok || ref == "" {
				return readinessPhase{}, fail(ErrInvalidPlan, fmt.Sprintf("%s.refs[%d]", blockerPath, refIndex), "must be a non-empty string")
			}
			refs = append(refs, ref)
		}
		sort.Strings(refs)
		blockers = append(blockers, ReadinessBlocker{Code: code, Refs: refs})
	}
	if (status == "ready") != (len(blockers) == 0) {
		return readinessPhase{}, fail(ErrInvalidPlan, phasePath, "ready requires no blockers and blocked requires at least one blocker")
	}
	sort.Slice(blockers, func(i, j int) bool {
		if blockers[i].Code != blockers[j].Code {
			return blockers[i].Code < blockers[j].Code
		}
		return strings.Join(blockers[i].Refs, "\x00") < strings.Join(blockers[j].Refs, "\x00")
	})
	return readinessPhase{status: status, blockers: blockers}, nil
}

func requireEqualNestedHash(parent map[string]any, field, expected, objectPath string) error {
	object, err := requiredObject(parent, field, objectPath)
	if err != nil {
		return err
	}
	hashPath := objectPath + ".hash"
	actual, err := requiredString(object, "hash", hashPath)
	if err != nil {
		return err
	}
	if actual != expected {
		return fail(ErrBindingMismatch, hashPath, "does not match top-level provenance hash")
	}
	return nil
}

func requiredObject(parent map[string]any, field, path string) (map[string]any, error) {
	value, ok := parent[field].(map[string]any)
	if !ok || value == nil {
		return nil, fail(ErrInvalidPlan, path, "must be an object, got %T", parent[field])
	}
	return value, nil
}

func requiredString(parent map[string]any, field, path string) (string, error) {
	value, ok := parent[field].(string)
	if !ok || value == "" {
		return "", fail(ErrInvalidPlan, path, "must be a non-empty string, got %T", parent[field])
	}
	return value, nil
}

func (b PlanBinding) validate(path string) error {
	for field, value := range map[string]string{
		"planHash":       b.PlanHash,
		"specHash":       b.SpecHash,
		"inventoryHash":  b.InventoryHash,
		"definitionHash": b.DefinitionHash,
	} {
		if !validSHA256(value) {
			return fail(ErrInvalidContract, fmt.Sprintf("%s.%s", path, field), "must be a lowercase sha256:<64-hex> digest")
		}
	}
	if b.CompilerVersion == "" || b.Renderer.ID == "" || b.Renderer.Version == "" {
		return fail(ErrInvalidContract, path, "compilerVersion and renderer id/version are required")
	}
	if err := validateBindingAuthority(b.Authority, path+".authority"); err != nil {
		return err
	}
	return nil
}

func validateBindingAuthority(authority resolvedplan.PlanAuthority, valuePath string) error {
	if !validSHA256(authority.CatalogHash) {
		return fail(ErrInvalidContract, valuePath+".catalogHash", "must be a lowercase sha256:<64-hex> digest")
	}
	if authority.DistributionFingerprint != "" {
		return fail(ErrInvalidContract, valuePath+".distributionFingerprint", "distribution attestation is build-local and must not enter a plan binding")
	}
	switch authority.Class {
	case "product":
		expected := resolvedplan.ProductPlanAuthority()
		if authority.Document != "catalog" || !authority.GraduationEligible ||
			authority.Issuer != expected.Issuer || !validSHA256(authority.AuthorityFingerprint) {
			return fail(ErrInvalidContract, valuePath, "product authority requires its issuer, semantic fingerprint, selected catalog hash, and graduationEligible=true")
		}
	case "contract-fixture":
		if authority.Document != "contractFixtureCatalog" || authority.GraduationEligible ||
			authority.Issuer != "stackkits-contract-fixture-authority/v1" || authority.AuthorityFingerprint != "" {
			return fail(ErrInvalidContract, valuePath, "contract-fixture authority requires contractFixtureCatalog and graduationEligible=false")
		}
	case "development":
		if authority.Document != "catalog" || authority.GraduationEligible ||
			authority.Issuer != "stackkits-development-authority/v1" || authority.AuthorityFingerprint != "" {
			return fail(ErrInvalidContract, valuePath, "development authority requires catalog and graduationEligible=false")
		}
	default:
		return fail(ErrInvalidContract, valuePath+".class", "unsupported authority class %q", authority.Class)
	}
	return nil
}
