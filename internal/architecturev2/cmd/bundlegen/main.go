// Command bundlegen builds the deterministic embedded Architecture v2
// authority projection. CUE remains the source of truth; this command copies
// the validation schemas and exports only the concrete catalog/Definitions.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	cueapi "cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	"cuelang.org/go/cue/load"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
	"gopkg.in/yaml.v3"
)

const (
	bundleSchemaVersion         = "stackkit.architecture-authority-bundle/v2"
	sourceSchemaVersion         = "stackkit.architecture-authority-source/v1"
	projectedSourceManifestPath = "architecture/v2/authority-manifest.json"
)

var contractFixtureSourceAllowlist = []string{
	"cue.mod/module.cue",
	"base/architecture_v2_profiles.cue",
	"base/architecture_v2.cue",
	"base/architecture_v2_definition_binding.cue",
	"base/architecture_v2_catalog.cue",
	"architecture/v2/contractfixture/catalog.cue",
	"basement-kit/stackfile.cue",
}

type sourceManifest struct {
	SchemaVersion string          `json:"schemaVersion"`
	ProfileSource string          `json:"profileSource"`
	BaseSources   []string        `json:"baseSources"`
	OpenAPI       string          `json:"openAPI"`
	Profiles      []sourceProfile `json:"profiles"`
}

type sourceProfile struct {
	Slug            string   `json:"slug"`
	Package         string   `json:"package"`
	Visibility      string   `json:"visibility"`
	ContractSources []string `json:"contractSources"`
}

type manifest struct {
	SchemaVersion           string            `json:"schemaVersion"`
	Module                  string            `json:"module"`
	ProfileScope            string            `json:"profileScope,omitempty"`
	DistributionFingerprint string            `json:"distributionFingerprint,omitempty"`
	SourceHashes            map[string]string `json:"sourceHashes"`
	Documents               map[string]string `json:"documents"`
	Profiles                map[string]string `json:"profiles"`
}

func main() {
	repoFlag := flag.String("repo", "../..", "StackKits CUE module root")
	outFlag := flag.String("out", "authority_bundle", "generated bundle output directory")
	sourceFlag := flag.String("manifest", "", "authority source manifest (defaults beneath -repo)")
	profilesFlag := flag.String("profiles", "all", "comma-separated authority profile slugs, public, or all")
	projectFlag := flag.Bool("project", false, "project the target repo to the selected profile set before bundling")
	distributionFingerprintOutFlag := flag.String("distribution-fingerprint-out", "", "generated Go product distribution fingerprint pin (must be internal/resolvedplan/product_distribution_fingerprint_generated.go beneath -repo)")
	contractFixtureFlag := flag.Bool("contract-fixture", false, "generate the isolated non-product contract-fixture authority bundle")
	flag.Parse()
	var err error
	if *contractFixtureFlag {
		if strings.TrimSpace(*sourceFlag) != "" || *profilesFlag != "all" || *projectFlag || strings.TrimSpace(*distributionFingerprintOutFlag) != "" {
			err = fmt.Errorf("-contract-fixture cannot be combined with -manifest, -profiles, -project, or -distribution-fingerprint-out")
		} else {
			err = runContractFixture(*repoFlag, *outFlag)
		}
	} else {
		err = runWithOptionsAndDistributionFingerprint(*repoFlag, *outFlag, *sourceFlag, *profilesFlag, *projectFlag, *distributionFingerprintOutFlag)
	}
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "bundlegen:", err)
		os.Exit(1)
	}
}

func run(repoFlag, outFlag string) error {
	return runWithOptions(repoFlag, outFlag, "", "all", false)
}

func runWithOptions(repoFlag, outFlag, sourceFlag, profilesFlag string, project bool) error {
	return runWithOptionsAndDistributionFingerprint(repoFlag, outFlag, sourceFlag, profilesFlag, project, "")
}

func runWithOptionsAndDistributionFingerprint(repoFlag, outFlag, sourceFlag, profilesFlag string, project bool, fingerprintOut string) error {
	repoRoot, source, profiles, err := resolveBundleInputs(repoFlag, sourceFlag, profilesFlag, project)
	if err != nil {
		return err
	}
	outRoot, parent, base, staging, err := prepareBundleOutput(outFlag)
	if err != nil {
		return err
	}
	defer func() {
		if staging != "" {
			_ = removeGuardedTree(staging, parent, "."+base+"-staging-")
		}
	}()
	distributionFingerprint, err := generateBundleProjection(repoRoot, staging, source, profiles)
	if err != nil {
		return err
	}
	if err := replaceGeneratedDirectory(staging, outRoot, parent, base); err != nil {
		return err
	}
	staging = ""
	if strings.TrimSpace(fingerprintOut) != "" {
		if err := writeProductDistributionFingerprintPin(repoRoot, fingerprintOut, distributionFingerprint); err != nil {
			return err
		}
	}
	return nil
}

func runContractFixture(repoFlag, outFlag string) error {
	repoRoot, err := filepath.Abs(repoFlag)
	if err != nil {
		return err
	}
	source, err := readSourceManifest(filepath.Join(repoRoot, projectedSourceManifestPath))
	if err != nil {
		return err
	}
	outRoot, parent, base, staging, err := prepareBundleOutput(outFlag)
	if err != nil {
		return err
	}
	if base != "contract_fixture_bundle" {
		return fmt.Errorf("contract fixture bundle output directory must be named contract_fixture_bundle, got %q", base)
	}
	defer func() {
		if staging != "" {
			_ = removeGuardedTree(staging, parent, "."+base+"-staging-")
		}
	}()
	if err := generateContractFixtureBundle(repoRoot, staging, source); err != nil {
		return err
	}
	if err := replaceGeneratedDirectory(staging, outRoot, parent, base); err != nil {
		return err
	}
	staging = ""
	return nil
}

func resolveBundleInputs(repoFlag, sourceFlag, profilesFlag string, project bool) (string, sourceManifest, []sourceProfile, error) {
	repoRoot, err := filepath.Abs(repoFlag)
	if err != nil {
		return "", sourceManifest{}, nil, err
	}
	sourcePath := strings.TrimSpace(sourceFlag)
	if sourcePath == "" {
		sourcePath = filepath.Join(repoRoot, "architecture", "v2", "authority-manifest.json")
	} else if sourcePath, err = filepath.Abs(sourcePath); err != nil {
		return "", sourceManifest{}, nil, err
	}
	source, err := readSourceManifest(sourcePath)
	if err != nil {
		return "", sourceManifest{}, nil, err
	}
	profiles, err := selectProfiles(source.Profiles, profilesFlag)
	if err != nil {
		return "", sourceManifest{}, nil, err
	}
	if !project && len(profiles) != len(source.Profiles) {
		return "", sourceManifest{}, nil, fmt.Errorf("a subset profile bundle requires -project so CUE and OpenAPI are projected consistently")
	}
	if project {
		if pathIsWithin(repoRoot, sourcePath) {
			return "", sourceManifest{}, nil, fmt.Errorf("refusing to project the authority source repository in place; use a separate materialized export tree")
		}
		if err := projectRepository(repoRoot, source, profiles); err != nil {
			return "", sourceManifest{}, nil, err
		}
	}
	return repoRoot, source, profiles, nil
}

func prepareBundleOutput(outFlag string) (outRoot, parent, base, staging string, err error) {
	outRoot, err = filepath.Abs(outFlag)
	if err != nil {
		return "", "", "", "", err
	}
	parent, base, err = guardedOutput(outRoot)
	if err != nil {
		return "", "", "", "", err
	}
	if err = os.MkdirAll(parent, 0o755); err != nil {
		return "", "", "", "", err
	}
	staging, err = os.MkdirTemp(parent, "."+base+"-staging-")
	return outRoot, parent, base, staging, err
}

//nolint:gocyclo // Projection is an atomic fail-closed pipeline whose source, namespace, catalog, definition, and fingerprint checks must remain visibly ordered.
func generateBundleProjection(repoRoot, staging string, source sourceManifest, profiles []sourceProfile) (distributionFingerprint string, returnErr error) {
	stagingRoot, err := os.OpenRoot(staging)
	if err != nil {
		return "", fmt.Errorf("open guarded bundle staging root: %w", err)
	}
	defer func() {
		if err := stagingRoot.Close(); returnErr == nil && err != nil {
			returnErr = fmt.Errorf("close guarded bundle staging root: %w", err)
		}
	}()

	result := manifest{
		SchemaVersion: bundleSchemaVersion,
		Module:        "github.com/kombifyio/stackkits",
		SourceHashes:  make(map[string]string),
		Documents:     map[string]string{"catalog": "catalog.json"},
		Profiles:      make(map[string]string, len(profiles)),
	}
	result.ProfileScope, err = productProfileScope(source.Profiles, profiles)
	if err != nil {
		return "", err
	}
	sourceFiles := selectedSourceFiles(source, profiles)
	authoritySources := make(map[string][]byte, len(sourceFiles))
	for _, relativePath := range sourceFiles {
		if err := validateProductBundlePath(relativePath); err != nil {
			return "", err
		}
		data, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(relativePath)))
		if err != nil {
			return "", err
		}
		if err := rejectProductFixtureNamespace(relativePath, data); err != nil {
			return "", err
		}
		if err := writeGenerated(stagingRoot, relativePath, data); err != nil {
			return "", err
		}
		result.SourceHashes[relativePath] = contentHash(data)
		authoritySources[relativePath] = data
	}

	catalog, err := loadCUEJSON(repoRoot, "base", "ArchitectureV2Catalog")
	if err != nil {
		return "", err
	}
	if err := rejectProductFixtureValue("catalog.json", catalog); err != nil {
		return "", err
	}
	catalogPath := "catalog.json"
	if err := writeCanonicalJSON(stagingRoot, catalogPath, catalog); err != nil {
		return "", err
	}
	decodedCatalog, err := decodeResolvedPlanCatalog(catalog)
	if err != nil {
		return "", err
	}
	definitions := make([]resolvedplan.KitDefinition, 0, len(profiles))
	for _, profile := range profiles {
		document, err := loadCUEJSON(repoRoot, profile.Package, "Definition")
		if err != nil {
			return "", err
		}
		relativePath := filepath.ToSlash(filepath.Join("definitions", profile.Slug+".json"))
		if err := validateProductBundlePath(relativePath); err != nil {
			return "", err
		}
		if err := rejectProductFixtureValue(relativePath, document); err != nil {
			return "", err
		}
		if err := writeCanonicalJSON(stagingRoot, relativePath, document); err != nil {
			return "", err
		}
		result.Profiles[profile.Slug] = relativePath
		definition, err := resolvedplan.DecodeDocument[resolvedplan.KitDefinition](document)
		if err != nil {
			return "", err
		}
		definitions = append(definitions, definition)
	}
	distributionFingerprint, err = resolvedplan.ComputeDistributionFingerprint(authoritySources, decodedCatalog, definitions)
	if err != nil {
		return "", err
	}
	result.DistributionFingerprint = distributionFingerprint
	if err := writeGeneratedJSON(stagingRoot, "manifest.json", result); err != nil {
		return "", err
	}
	return distributionFingerprint, nil
}

func generateContractFixtureBundle(repoRoot, staging string, source sourceManifest) (returnErr error) {
	stagingRoot, err := os.OpenRoot(staging)
	if err != nil {
		return fmt.Errorf("open guarded contract fixture bundle staging root: %w", err)
	}
	defer func() {
		if err := stagingRoot.Close(); returnErr == nil && err != nil {
			returnErr = fmt.Errorf("close guarded contract fixture bundle staging root: %w", err)
		}
	}()

	result := manifest{
		SchemaVersion: bundleSchemaVersion,
		Module:        "github.com/kombifyio/stackkits",
		SourceHashes:  make(map[string]string),
		Documents:     map[string]string{"contractFixtureCatalog": "contract-fixture-catalog.json"},
		Profiles:      map[string]string{"basement-kit": "definitions/basement-kit.json"},
	}
	sourceFiles, err := contractFixtureSourceFiles(source)
	if err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(sourceFiles))
	for _, relativePath := range sourceFiles {
		relativePath = filepath.ToSlash(filepath.Clean(filepath.FromSlash(relativePath)))
		if _, duplicate := seen[relativePath]; duplicate {
			continue
		}
		seen[relativePath] = struct{}{}
		data, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(relativePath)))
		if err != nil {
			return err
		}
		if err := writeGenerated(stagingRoot, relativePath, data); err != nil {
			return err
		}
		result.SourceHashes[relativePath] = contentHash(data)
	}

	catalog, err := loadCUEJSON(repoRoot, "architecture/v2/contractfixture", "ArchitectureV2ContractFixtureCatalog")
	if err != nil {
		return err
	}
	if err := writeCanonicalJSON(stagingRoot, "contract-fixture-catalog.json", catalog); err != nil {
		return err
	}
	definition, err := loadCUEJSON(repoRoot, "architecture/v2/contractfixture", "ContractFixtureDefinition")
	if err != nil {
		return err
	}
	if err := writeCanonicalJSON(stagingRoot, "definitions/basement-kit.json", definition); err != nil {
		return err
	}
	return writeGeneratedJSON(stagingRoot, "manifest.json", result)
}

func productProfileScope(available, selected []sourceProfile) (string, error) {
	public := make([]sourceProfile, 0, len(available))
	for _, profile := range available {
		if profile.Visibility == "public" {
			public = append(public, profile)
		}
	}
	if sameProfileSet(selected, public) {
		return "oss", nil
	}
	if sameProfileSet(selected, available) {
		return "platform", nil
	}
	slugs := make([]string, 0, len(selected))
	for _, profile := range selected {
		slugs = append(slugs, profile.Slug)
	}
	sort.Strings(slugs)
	return "", fmt.Errorf(
		"unsupported product authority profile set %v; expected all public profiles or the complete platform profile set",
		slugs,
	)
}

func sameProfileSet(left, right []sourceProfile) bool {
	if len(left) != len(right) {
		return false
	}
	expected := make(map[string]struct{}, len(right))
	for _, profile := range right {
		expected[profile.Slug] = struct{}{}
	}
	for _, profile := range left {
		if _, ok := expected[profile.Slug]; !ok {
			return false
		}
	}
	return true
}

func decodeResolvedPlanCatalog(data []byte) (resolvedplan.Catalog, error) {
	var wire struct {
		Capabilities                 []resolvedplan.CapabilityContract          `json:"capabilities"`
		Providers                    []resolvedplan.CapabilityProvider          `json:"providers"`
		AddOns                       []resolvedplan.AddOnContract               `json:"addons"`
		Modules                      []resolvedplan.ModuleContract              `json:"modules"`
		PrivilegedInterfaceApprovals []resolvedplan.PrivilegedInterfaceApproval `json:"privilegedInterfaceApprovals"`
		PlanArtifacts                []resolvedplan.PlanArtifactContract        `json:"planArtifacts"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&wire); err != nil {
		return resolvedplan.Catalog{}, err
	}
	return resolvedplan.Catalog{
		Capabilities: wire.Capabilities, Providers: wire.Providers, AddOns: wire.AddOns,
		Modules: wire.Modules, PrivilegedInterfaceApprovals: wire.PrivilegedInterfaceApprovals,
		PlanArtifacts: wire.PlanArtifacts,
	}, nil
}

func writeProductDistributionFingerprintPin(repoRoot, outputPath, fingerprint string) error {
	if !strings.HasPrefix(fingerprint, "sha256:") {
		return fmt.Errorf("invalid product distribution fingerprint %q", fingerprint)
	}
	absoluteOutput, err := filepath.Abs(outputPath)
	if err != nil {
		return err
	}
	expected := filepath.Join(repoRoot, "internal", "resolvedplan", "product_distribution_fingerprint_generated.go")
	if filepath.Clean(absoluteOutput) != filepath.Clean(expected) {
		return fmt.Errorf("product distribution fingerprint output must be %s", expected)
	}
	content := []byte("// Code generated by internal/architecturev2/cmd/bundlegen; DO NOT EDIT.\n\n" +
		"package resolvedplan\n\n" +
		"// pinnedProductDistributionFingerprint is regenerated from the exact product\n" +
		"// CUE sources plus their complete concrete Catalog and Definition projections.\n" +
		fmt.Sprintf("const pinnedProductDistributionFingerprint = %q\n", fingerprint))
	temporary, err := os.CreateTemp(filepath.Dir(expected), ".product-distribution-fingerprint-*.go")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if _, err := temporary.Write(content); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, expected)
}

func contractFixtureSourceFiles(source sourceManifest) ([]string, error) {
	declaredBase := make(map[string]struct{}, len(source.BaseSources))
	for _, relativePath := range source.BaseSources {
		declaredBase[cleanRelativePath(relativePath)] = struct{}{}
	}
	for _, required := range contractFixtureSourceAllowlist[:5] {
		if _, exists := declaredBase[required]; !exists {
			return nil, fmt.Errorf("authority source manifest omits contract-fixture base dependency %s", required)
		}
	}
	return append([]string(nil), contractFixtureSourceAllowlist...), nil
}

func validateProductBundlePath(relativePath string) error {
	clean := strings.ToLower(cleanRelativePath(relativePath))
	if strings.HasPrefix(clean, "architecture/v2/contractfixture/") ||
		strings.HasSuffix(clean, "/contract-fixture-catalog.json") ||
		clean == "contract-fixture-catalog.json" {
		return fmt.Errorf("product authority cannot include contract-fixture path %s", relativePath)
	}
	return nil
}

func rejectProductFixtureValue(relativePath string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode %s for product namespace validation: %w", relativePath, err)
	}
	return rejectProductFixtureNamespace(relativePath, data)
}

func rejectProductFixtureNamespace(relativePath string, data []byte) error {
	for _, forbidden := range []string{
		"stackkits-contract-fixture/",
		"stackkit-contract-fixture",
		"ArchitectureV2ContractFixtureCatalog",
		"ContractFixtureDefinition",
	} {
		if bytes.Contains(data, []byte(forbidden)) {
			return fmt.Errorf("product authority source %s contains forbidden contract-fixture namespace %q", relativePath, forbidden)
		}
	}
	return nil
}

func pathIsWithin(root, candidate string) bool {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(candidate))
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func readSourceManifest(path string) (sourceManifest, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return sourceManifest{}, fmt.Errorf("read authority source manifest: %w", err)
	}
	var source sourceManifest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&source); err != nil {
		return sourceManifest{}, fmt.Errorf("decode authority source manifest: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return sourceManifest{}, fmt.Errorf("authority source manifest contains trailing JSON values")
		}
		return sourceManifest{}, fmt.Errorf("decode trailing authority source manifest data: %w", err)
	}
	if source.SchemaVersion != sourceSchemaVersion {
		return sourceManifest{}, fmt.Errorf("unsupported authority source schema %q", source.SchemaVersion)
	}
	if len(source.Profiles) == 0 {
		return sourceManifest{}, fmt.Errorf("authority source manifest has no profiles")
	}
	if err := validateRelativeSourcePath(source.ProfileSource); err != nil {
		return sourceManifest{}, fmt.Errorf("profileSource: %w", err)
	}
	if err := validateRelativeSourcePath(source.OpenAPI); err != nil {
		return sourceManifest{}, fmt.Errorf("openAPI: %w", err)
	}
	seenProfiles := make(map[string]struct{}, len(source.Profiles))
	for index, profile := range source.Profiles {
		if strings.TrimSpace(profile.Slug) == "" || strings.TrimSpace(profile.Package) == "" {
			return sourceManifest{}, fmt.Errorf("profiles[%d] requires slug and package", index)
		}
		if _, exists := seenProfiles[profile.Slug]; exists {
			return sourceManifest{}, fmt.Errorf("duplicate authority profile %q", profile.Slug)
		}
		seenProfiles[profile.Slug] = struct{}{}
		if err := validateRelativeSourcePath(profile.Package); err != nil {
			return sourceManifest{}, fmt.Errorf("profile %s package: %w", profile.Slug, err)
		}
		if profile.Visibility != "public" && profile.Visibility != "private" {
			return sourceManifest{}, fmt.Errorf("profile %s has unsupported visibility %q", profile.Slug, profile.Visibility)
		}
		for _, relativePath := range profile.ContractSources {
			if err := validateRelativeSourcePath(relativePath); err != nil {
				return sourceManifest{}, fmt.Errorf("profile %s contract source: %w", profile.Slug, err)
			}
		}
	}
	for _, relativePath := range source.BaseSources {
		if err := validateRelativeSourcePath(relativePath); err != nil {
			return sourceManifest{}, fmt.Errorf("base source: %w", err)
		}
	}
	return source, nil
}

func validateRelativeSourcePath(relativePath string) error {
	clean := filepath.Clean(filepath.FromSlash(strings.TrimSpace(relativePath)))
	if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("unsafe repository-relative path %q", relativePath)
	}
	return nil
}

func selectProfiles(available []sourceProfile, raw string) ([]sourceProfile, error) {
	requested := strings.TrimSpace(raw)
	if requested == "" || requested == "all" {
		return append([]sourceProfile(nil), available...), nil
	}
	if requested == "public" {
		selected := make([]sourceProfile, 0, len(available))
		for _, profile := range available {
			if profile.Visibility == "public" {
				selected = append(selected, profile)
			}
		}
		if len(selected) == 0 {
			return nil, fmt.Errorf("authority source manifest has no public profiles")
		}
		return selected, nil
	}
	bySlug := make(map[string]sourceProfile, len(available))
	for _, profile := range available {
		bySlug[profile.Slug] = profile
	}
	seen := make(map[string]struct{})
	selected := make([]sourceProfile, 0)
	for _, slug := range strings.Split(requested, ",") {
		slug = strings.TrimSpace(slug)
		if slug == "" {
			return nil, fmt.Errorf("profiles contains an empty slug")
		}
		profile, exists := bySlug[slug]
		if !exists {
			return nil, fmt.Errorf("unknown authority profile %q", slug)
		}
		if _, duplicate := seen[slug]; duplicate {
			return nil, fmt.Errorf("duplicate selected authority profile %q", slug)
		}
		seen[slug] = struct{}{}
		selected = append(selected, profile)
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("at least one authority profile is required")
	}
	sort.Slice(selected, func(i, j int) bool { return selected[i].Slug < selected[j].Slug })
	return selected, nil
}

func selectedSourceFiles(source sourceManifest, profiles []sourceProfile) []string {
	seen := make(map[string]struct{})
	files := make([]string, 0, len(source.BaseSources)+len(profiles))
	appendFile := func(relativePath string) {
		clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(relativePath)))
		if _, exists := seen[clean]; exists {
			return
		}
		seen[clean] = struct{}{}
		files = append(files, clean)
	}
	for _, relativePath := range source.BaseSources {
		appendFile(relativePath)
	}
	for _, profile := range profiles {
		for _, relativePath := range profile.ContractSources {
			appendFile(relativePath)
		}
	}
	sort.Strings(files)
	return files
}

func projectRepository(repoRoot string, source sourceManifest, profiles []sourceProfile) error {
	selected := make(map[string]struct{}, len(profiles))
	selectedSources := make(map[string]struct{})
	selectedPackages := make(map[string]struct{}, len(profiles))
	for _, profile := range profiles {
		selected[profile.Slug] = struct{}{}
		selectedPackages[cleanRelativePath(profile.Package)] = struct{}{}
		for _, relativePath := range profile.ContractSources {
			selectedSources[cleanRelativePath(relativePath)] = struct{}{}
		}
	}
	for _, profile := range source.Profiles {
		if _, keep := selected[profile.Slug]; keep {
			continue
		}
		for _, relativePath := range profile.ContractSources {
			clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(relativePath)))
			if _, sharedWithSelectedProfile := selectedSources[clean]; sharedWithSelectedProfile {
				continue
			}
			if err := removeProjectedSource(repoRoot, relativePath); err != nil {
				return err
			}
		}
		packagePath := cleanRelativePath(profile.Package)
		for selectedPackage := range selectedPackages {
			if relativePathsOverlap(packagePath, selectedPackage) {
				return fmt.Errorf("cannot safely remove unselected profile package %q because it overlaps selected package %q", packagePath, selectedPackage)
			}
		}
		if err := removeProjectedProfilePackage(repoRoot, packagePath); err != nil {
			return err
		}
	}
	profileSource, err := renderProfileSource(profiles)
	if err != nil {
		return err
	}
	if err := writeProjectedSource(repoRoot, source.ProfileSource, profileSource); err != nil {
		return err
	}
	if err := projectOpenAPI(repoRoot, source.OpenAPI, profiles); err != nil {
		return err
	}
	projectedManifest, err := renderProjectedSourceManifest(source, profiles)
	if err != nil {
		return err
	}
	return writeProjectedSource(repoRoot, projectedSourceManifestPath, projectedManifest)
}

func cleanRelativePath(relativePath string) string {
	return filepath.ToSlash(filepath.Clean(filepath.FromSlash(relativePath)))
}

func relativePathsOverlap(left, right string) bool {
	return left == right || strings.HasPrefix(left, right+"/") || strings.HasPrefix(right, left+"/")
}

func renderProjectedSourceManifest(source sourceManifest, profiles []sourceProfile) ([]byte, error) {
	if len(profiles) == 0 {
		return nil, fmt.Errorf("cannot render an empty authority source manifest")
	}
	projected := sourceManifest{
		SchemaVersion: source.SchemaVersion,
		ProfileSource: source.ProfileSource,
		BaseSources:   append([]string(nil), source.BaseSources...),
		OpenAPI:       source.OpenAPI,
		Profiles:      make([]sourceProfile, len(profiles)),
	}
	for index, profile := range profiles {
		profile.ContractSources = append([]string(nil), profile.ContractSources...)
		projected.Profiles[index] = profile
	}
	data, err := json.MarshalIndent(projected, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode projected authority source manifest: %w", err)
	}
	return append(data, '\n'), nil
}

func renderProfileSource(profiles []sourceProfile) ([]byte, error) {
	if len(profiles) == 0 {
		return nil, fmt.Errorf("cannot render an empty authority profile set")
	}
	var output strings.Builder
	output.WriteString("// Code generated from the governed Architecture v2 authority source; DO NOT EDIT.\n")
	output.WriteString("// Package base declares the exact product profiles present in this authority.\n")
	output.WriteString("package base\n\n#KitSlug: ")
	for index, profile := range profiles {
		if index > 0 {
			output.WriteString(" | ")
		}
		fmt.Fprintf(&output, "%q", profile.Slug)
	}
	output.WriteString("\n\nArchitectureV2AuthorityProfiles: [\n")
	for _, profile := range profiles {
		fmt.Fprintf(&output, "\t{slug: %q, package: %q},\n", profile.Slug, profile.Package)
	}
	output.WriteString("]\n")
	return []byte(output.String()), nil
}

func removeProjectedSource(repoRoot, relativePath string) error {
	path, err := guardedRepositoryPath(repoRoot, relativePath)
	if err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("refusing to remove non-regular projected source %s", relativePath)
	}
	return os.Remove(path)
}

func removeProjectedProfilePackage(repoRoot, relativePath string) error {
	path, err := guardedRepositoryPath(repoRoot, relativePath)
	if err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("refusing to remove non-directory projected profile package %s", relativePath)
	}
	if err := ensureProjectedDirectory(repoRoot, filepath.Dir(path)); err != nil {
		return err
	}
	if err := validateRegularProjectedTree(path); err != nil {
		return err
	}
	return os.RemoveAll(path)
}

func validateRegularProjectedTree(root string) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing projected profile package containing symlink or junction %s", path)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("refusing projected profile package containing non-regular entry %s", path)
		}
		return nil
	})
}

func writeProjectedSource(repoRoot, relativePath string, data []byte) error {
	path, err := guardedRepositoryPath(repoRoot, relativePath)
	if err != nil {
		return err
	}
	if err := ensureProjectedDirectory(repoRoot, filepath.Dir(path)); err != nil {
		return err
	}
	if info, statErr := os.Lstat(path); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to replace symlinked projected source %s", relativePath)
	} else if statErr != nil && !os.IsNotExist(statErr) {
		return statErr
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".authority-projection-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	installed := false
	defer func() {
		_ = temporary.Close()
		if !installed {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o644); err != nil {
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := atomicReplaceProjectedSource(temporaryPath, path); err != nil {
		return err
	}
	installed = true
	return nil
}

func ensureProjectedDirectory(repoRoot, directory string) error {
	root := filepath.Clean(repoRoot)
	relative, err := filepath.Rel(root, filepath.Clean(directory))
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("projected directory escapes repository: %q", directory)
	}
	rootInfo, err := os.Lstat(root)
	if err != nil {
		return err
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() {
		return fmt.Errorf("projected repository root is not a regular directory: %s", root)
	}
	current := root
	if relative == "." {
		return nil
	}
	for _, component := range strings.Split(relative, string(filepath.Separator)) {
		current = filepath.Join(current, component)
		info, statErr := os.Lstat(current)
		if os.IsNotExist(statErr) {
			if err := os.Mkdir(current, 0o755); err != nil {
				return err
			}
			continue
		}
		if statErr != nil {
			return statErr
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("refusing projected output through non-directory path %s", current)
		}
	}
	return nil
}

func guardedRepositoryPath(repoRoot, relativePath string) (string, error) {
	if err := validateRelativeSourcePath(relativePath); err != nil {
		return "", err
	}
	root := filepath.Clean(repoRoot)
	target := filepath.Clean(filepath.Join(root, filepath.FromSlash(relativePath)))
	relative, err := filepath.Rel(root, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("projected source escapes repository: %q", relativePath)
	}
	return target, nil
}

func projectOpenAPI(repoRoot, relativePath string, profiles []sourceProfile) error {
	path, err := guardedRepositoryPath(repoRoot, relativePath)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read OpenAPI projection source: %w", err)
	}
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return fmt.Errorf("decode OpenAPI projection source: %w", err)
	}
	kitProfile, err := yamlMappingPath(&document, "components", "schemas", "ArchitectureV2KitProfile")
	if err != nil {
		return err
	}
	enumNode, err := yamlMappingValue(kitProfile, "enum")
	if err != nil {
		return err
	}
	enumNode.Kind = yaml.SequenceNode
	enumNode.Tag = "!!seq"
	enumNode.Content = nil
	for _, profile := range profiles {
		enumNode.Content = append(enumNode.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: profile.Slug})
	}
	var output bytes.Buffer
	encoder := yaml.NewEncoder(&output)
	encoder.SetIndent(2)
	if err := encoder.Encode(&document); err != nil {
		return fmt.Errorf("encode projected OpenAPI: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return err
	}
	return writeProjectedSource(repoRoot, relativePath, output.Bytes())
}

func yamlMappingPath(root *yaml.Node, keys ...string) (*yaml.Node, error) {
	current := root
	if current.Kind == yaml.DocumentNode && len(current.Content) == 1 {
		current = current.Content[0]
	}
	var err error
	for _, key := range keys {
		current, err = yamlMappingValue(current, key)
		if err != nil {
			return nil, err
		}
	}
	return current, nil
}

func yamlMappingValue(mapping *yaml.Node, key string) (*yaml.Node, error) {
	if mapping.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("OpenAPI node before %q is not a mapping", key)
	}
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		if mapping.Content[index].Value == key {
			return mapping.Content[index+1], nil
		}
	}
	return nil, fmt.Errorf("OpenAPI projection field %q is missing", key)
}

func loadCUEJSON(moduleRoot, directory, expression string) ([]byte, error) {
	instances := load.Instances([]string{"./" + filepath.ToSlash(directory)}, &load.Config{
		Dir: moduleRoot, ModuleRoot: moduleRoot,
	})
	if len(instances) != 1 {
		return nil, fmt.Errorf("CUE loader returned %d instances for %s", len(instances), directory)
	}
	if instances[0].Err != nil {
		return nil, instances[0].Err
	}
	root := cuecontext.New().BuildInstance(instances[0])
	if err := root.Err(); err != nil {
		return nil, err
	}
	value := root.LookupPath(cueapi.ParsePath(expression))
	if err := value.Validate(cueapi.Concrete(true)); err != nil {
		return nil, err
	}
	return value.MarshalJSON()
}

func writeGeneratedJSON(root *os.Root, relativePath string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return writeCanonicalJSON(root, relativePath, data)
}

func writeCanonicalJSON(root *os.Root, relativePath string, data []byte) error {
	var compact any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&compact); err != nil {
		return err
	}
	pretty, err := json.MarshalIndent(compact, "", "  ")
	if err != nil {
		return err
	}
	return writeGenerated(root, relativePath, append(pretty, '\n'))
}

func writeGenerated(root *os.Root, relativePath string, data []byte) error {
	if root == nil {
		return fmt.Errorf("generated output root is required")
	}
	if err := validateRelativeSourcePath(relativePath); err != nil {
		return fmt.Errorf("generated output path: %w", err)
	}
	clean := filepath.Clean(filepath.FromSlash(relativePath))
	if parent := filepath.Dir(clean); parent != "." {
		if err := root.MkdirAll(parent, 0o755); err != nil {
			return fmt.Errorf("create generated output parent for %s: %w", relativePath, err)
		}
	}
	file, err := root.OpenFile(clean, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("create generated output %s: %w", relativePath, err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = file.Close()
		}
	}()
	if _, err := file.Write(data); err != nil {
		return fmt.Errorf("write generated output %s: %w", relativePath, err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync generated output %s: %w", relativePath, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close generated output %s: %w", relativePath, err)
	}
	closed = true
	return nil
}

func contentHash(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func guardedOutput(outRoot string) (string, string, error) {
	clean := filepath.Clean(outRoot)
	if clean == "" || clean == "." || filepath.Dir(clean) == clean {
		return "", "", fmt.Errorf("unsafe bundle output path %q", outRoot)
	}
	parent, base := filepath.Dir(clean), filepath.Base(clean)
	if base == "" || base == "." || base == string(filepath.Separator) {
		return "", "", fmt.Errorf("unsafe bundle output directory %q", outRoot)
	}
	if base != "authority_bundle" && base != "contract_fixture_bundle" {
		return "", "", fmt.Errorf("bundle output directory must be named authority_bundle or contract_fixture_bundle, got %q", base)
	}
	return parent, base, nil
}

func replaceGeneratedDirectory(staging, outRoot, parent, base string) error {
	if filepath.Dir(filepath.Clean(staging)) != parent || filepath.Dir(filepath.Clean(outRoot)) != parent {
		return fmt.Errorf("refusing to replace output outside guarded parent %s", parent)
	}
	if _, err := os.Lstat(outRoot); os.IsNotExist(err) {
		return os.Rename(staging, outRoot)
	} else if err != nil {
		return err
	}

	backup, err := os.MkdirTemp(parent, "."+base+"-previous-")
	if err != nil {
		return err
	}
	if err := os.Remove(backup); err != nil {
		return err
	}
	if err := os.Rename(outRoot, backup); err != nil {
		return err
	}
	if err := os.Rename(staging, outRoot); err != nil {
		if rollbackErr := os.Rename(backup, outRoot); rollbackErr != nil {
			return fmt.Errorf("install generated bundle: %v; rollback failed: %w", err, rollbackErr)
		}
		return err
	}
	if err := removeGuardedTree(backup, parent, "."+base+"-previous-"); err != nil {
		return fmt.Errorf("remove previous generated bundle: %w", err)
	}
	return nil
}

func removeGuardedTree(target, parent, requiredPrefix string) error {
	cleanTarget, cleanParent := filepath.Clean(target), filepath.Clean(parent)
	if target == "" || cleanTarget == "." || filepath.Dir(cleanTarget) != cleanParent ||
		!strings.HasPrefix(filepath.Base(cleanTarget), requiredPrefix) {
		return fmt.Errorf("refusing unsafe recursive removal %q", target)
	}
	return os.RemoveAll(cleanTarget)
}
