package architecturev2

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/kombifyio/stackkits/internal/resolvedplan"
	"github.com/kombifyio/stackkits/internal/stackspecmigration"
)

const (
	embeddedProductBundleRoot         = "authority_bundle"
	embeddedContractFixtureBundleRoot = "contract_fixture_bundle"
	embeddedBundleRoot                = embeddedProductBundleRoot // compatibility name for product-bundle tests
	embeddedBundleSchemaVersion       = "stackkit.architecture-authority-bundle/v2"
)

// Product and contract-fixture authorities are deliberately embedded as two
// independent roots. Loading the product service never parses, hashes, walks,
// or validates the fixture root, so fixture drift cannot affect product/API
// startup.
//
//go:embed authority_bundle contract_fixture_bundle
var embeddedBundleFS embed.FS

type authorityBundleManifest struct {
	SchemaVersion           string            `json:"schemaVersion"`
	Module                  string            `json:"module"`
	ProfileScope            string            `json:"profileScope,omitempty"`
	DistributionFingerprint string            `json:"distributionFingerprint,omitempty"`
	SourceHashes            map[string]string `json:"sourceHashes"`
	Documents               map[string]string `json:"documents"`
	Profiles                map[string]string `json:"profiles"`
}

type embeddedAuthorityRole struct {
	name            string
	root            string
	document        string
	planAuthority   resolvedplan.PlanAuthority
	requiredSources []string
	fixture         bool
}

var productAuthorityRole = embeddedAuthorityRole{
	name:          "product",
	root:          embeddedProductBundleRoot,
	document:      "catalog",
	planAuthority: resolvedplan.ProductPlanAuthority(),
	requiredSources: []string{
		"cue.mod/module.cue",
		"base/architecture_v2_profiles.cue",
		"base/architecture_v2.cue",
		"base/architecture_v2_definition_binding.cue",
		"base/architecture_v2_catalog.cue",
	},
}

var contractFixtureAuthorityRole = embeddedAuthorityRole{
	name:          "contract fixture",
	root:          embeddedContractFixtureBundleRoot,
	document:      "contractFixtureCatalog",
	planAuthority: resolvedplan.ContractFixturePlanAuthority(),
	requiredSources: []string{
		"cue.mod/module.cue",
		"base/architecture_v2_profiles.cue",
		"base/architecture_v2.cue",
		"base/architecture_v2_definition_binding.cue",
		"base/architecture_v2_catalog.cue",
		"architecture/v2/contractfixture/catalog.cue",
		"basement-kit/stackfile.cue",
	},
	fixture: true,
}

func loadEmbeddedAuthority() (*cueAuthority, error) {
	return loadEmbeddedAuthorityForRole(productAuthorityRole)
}

func loadEmbeddedContractFixtureAuthority() (*cueAuthority, error) {
	return loadEmbeddedAuthorityForRole(contractFixtureAuthorityRole)
}

func loadEmbeddedAuthorityForRole(role embeddedAuthorityRole) (*cueAuthority, error) {
	manifest, err := readEmbeddedManifestForRole(role)
	if err != nil {
		return nil, err
	}
	if err := verifyEmbeddedSourceHashesForRoot(role.root, manifest); err != nil {
		return nil, err
	}
	digest, err := embeddedBundleDigestForRoot(role.root)
	if err != nil {
		return nil, err
	}
	contractSources, err := embeddedContractSourcesForRoot(role.root, manifest)
	if err != nil {
		return nil, err
	}

	catalogPath := manifest.Documents[role.document]
	catalogDocument, err := readEmbeddedDocumentForRoot(role.root, catalogPath)
	if err != nil {
		return nil, fmt.Errorf("read embedded %s catalog: %w", role.name, err)
	}
	catalog, err := decodeCatalog(catalogDocument)
	if err != nil {
		return nil, fmt.Errorf("decode embedded %s catalog: %w", role.name, err)
	}
	authority := &cueAuthority{
		moduleRoot:      embeddedVirtualModuleRoot(role.name, digest),
		contractSources: contractSources,
		definitions:     make(map[stackspecmigration.KitProfile]resolvedplan.KitDefinition, len(manifest.Profiles)),
		catalog:         catalog,
		planAuthority:   role.planAuthority,
	}
	profileSlugs := make([]string, 0, len(manifest.Profiles))
	for slug := range manifest.Profiles {
		profileSlugs = append(profileSlugs, slug)
	}
	sort.Strings(profileSlugs)
	for _, slug := range profileSlugs {
		profile := stackspecmigration.KitProfile(slug)
		document, err := readEmbeddedDocumentForRoot(role.root, manifest.Profiles[slug])
		if err != nil {
			return nil, fmt.Errorf("read embedded %s %s Definition: %w", role.name, profile, err)
		}
		metadata, ok := document["metadata"].(map[string]any)
		documentSlug, slugOK := metadata["slug"].(string)
		if !ok || !slugOK || documentSlug != string(profile) {
			return nil, fmt.Errorf("embedded %s %s Definition exports metadata.slug %q", role.name, profile, metadata["slug"])
		}
		authority.definitions[profile] = resolvedplan.KitDefinition(document)
	}
	return authority, nil
}

// readEmbeddedManifest is the product-only compatibility wrapper used by
// existing bundle drift tests. Fixture tests use the explicitly named helper.
func readEmbeddedManifest() (authorityBundleManifest, error) {
	return readEmbeddedManifestForRole(productAuthorityRole)
}

func readEmbeddedContractFixtureManifest() (authorityBundleManifest, error) {
	return readEmbeddedManifestForRole(contractFixtureAuthorityRole)
}

//nolint:gocyclo // Manifest decoding intentionally validates every role-specific allowlist and embedded-file invariant before returning authority data.
func readEmbeddedManifestForRole(role embeddedAuthorityRole) (authorityBundleManifest, error) {
	data, err := embeddedBundleFS.ReadFile(role.root + "/manifest.json")
	if err != nil {
		return authorityBundleManifest{}, err
	}
	var manifest authorityBundleManifest
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return authorityBundleManifest{}, fmt.Errorf("decode embedded %s authority manifest: %w", role.name, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return authorityBundleManifest{}, fmt.Errorf("embedded %s authority manifest contains multiple JSON values", role.name)
		}
		return authorityBundleManifest{}, fmt.Errorf("decode trailing embedded %s authority manifest data: %w", role.name, err)
	}
	if manifest.SchemaVersion != embeddedBundleSchemaVersion {
		return authorityBundleManifest{}, fmt.Errorf("unsupported embedded %s authority schema %q", role.name, manifest.SchemaVersion)
	}
	if manifest.Module != "github.com/kombifyio/stackkits" {
		return authorityBundleManifest{}, fmt.Errorf("embedded %s authority module is %q", role.name, manifest.Module)
	}
	for _, required := range role.requiredSources {
		if manifest.SourceHashes[required] == "" {
			return authorityBundleManifest{}, fmt.Errorf("embedded %s authority manifest has no hash for %s", role.name, required)
		}
	}
	if len(manifest.Documents) != 1 || manifest.Documents[role.document] == "" {
		return authorityBundleManifest{}, fmt.Errorf("embedded %s authority manifest must contain exactly the %s document", role.name, role.document)
	}
	if role.fixture {
		if manifest.ProfileScope != "" || manifest.DistributionFingerprint != "" {
			return authorityBundleManifest{}, fmt.Errorf("embedded contract fixture authority cannot claim a product scope or distribution fingerprint")
		}
		if len(manifest.Profiles) != 1 || manifest.Profiles[string(stackspecmigration.KitProfileBasement)] == "" {
			return authorityBundleManifest{}, fmt.Errorf("embedded contract fixture authority must contain exactly its Basement fixture Definition")
		}
	} else if len(manifest.Profiles) == 0 {
		return authorityBundleManifest{}, fmt.Errorf("embedded product authority manifest has no profiles")
	}
	for slug, relativePath := range manifest.Profiles {
		if strings.TrimSpace(slug) == "" || strings.TrimSpace(relativePath) == "" {
			return authorityBundleManifest{}, fmt.Errorf("embedded %s authority manifest contains an empty profile or definition path", role.name)
		}
	}
	if err := validateAuthorityRoleManifest(role, manifest); err != nil {
		return authorityBundleManifest{}, err
	}
	if err := verifyBundleFileSet(embeddedBundleFS, role.root, manifest); err != nil {
		return authorityBundleManifest{}, err
	}
	if err := verifyEmbeddedAuthorityRoleContent(role, manifest); err != nil {
		return authorityBundleManifest{}, err
	}
	return manifest, nil
}

//nolint:gocyclo // Product and fixture roles share this exhaustive fail-closed allowlist validator to prevent namespace or scope drift.
func validateAuthorityRoleManifest(role embeddedAuthorityRole, manifest authorityBundleManifest) error {
	if role.fixture {
		if manifest.Documents[role.document] != "contract-fixture-catalog.json" ||
			manifest.Profiles[string(stackspecmigration.KitProfileBasement)] != "definitions/basement-kit.json" {
			return fmt.Errorf("embedded contract fixture authority uses a non-allowlisted document or Definition path")
		}
		allowed := make(map[string]struct{}, len(role.requiredSources))
		for _, relativePath := range role.requiredSources {
			allowed[filepath.ToSlash(relativePath)] = struct{}{}
		}
		if len(manifest.SourceHashes) != len(allowed) {
			return fmt.Errorf("embedded contract fixture authority source set is not the exact fixture allowlist")
		}
		for relativePath := range manifest.SourceHashes {
			clean, err := cleanBundleRelativePath(relativePath)
			if err != nil {
				return err
			}
			if _, ok := allowed[clean]; !ok {
				return fmt.Errorf("embedded contract fixture authority contains non-allowlisted source %s", clean)
			}
		}
		return nil
	}

	if manifest.Documents[role.document] != "catalog.json" {
		return fmt.Errorf("embedded product authority catalog must use catalog.json")
	}
	if manifest.DistributionFingerprint != role.planAuthority.DistributionFingerprint {
		return fmt.Errorf("embedded product distribution fingerprint does not match the binary pin")
	}
	var expectedProfileCount int
	switch manifest.ProfileScope {
	case "platform", "oss":
		expectedProfileCount = 3
	default:
		return fmt.Errorf("embedded product authority uses unsupported profile scope %q", manifest.ProfileScope)
	}
	if len(manifest.Profiles) != expectedProfileCount {
		return fmt.Errorf("embedded product authority profile set does not match its %s scope", manifest.ProfileScope)
	}
	paths := make([]string, 0, len(manifest.SourceHashes)+len(manifest.Documents)+len(manifest.Profiles))
	for relativePath := range manifest.SourceHashes {
		paths = append(paths, relativePath)
	}
	for _, relativePath := range manifest.Documents {
		paths = append(paths, relativePath)
	}
	for slug, relativePath := range manifest.Profiles {
		if relativePath != "definitions/"+slug+".json" {
			return fmt.Errorf("embedded product authority profile %s uses non-canonical Definition path %s", slug, relativePath)
		}
		paths = append(paths, relativePath)
	}
	for _, relativePath := range paths {
		clean, err := cleanBundleRelativePath(relativePath)
		if err != nil {
			return err
		}
		if isContractFixtureBundlePath(clean) {
			return fmt.Errorf("embedded product authority references contract-fixture path %s", clean)
		}
	}
	return nil
}

func isContractFixtureBundlePath(relativePath string) bool {
	clean := strings.ToLower(filepath.ToSlash(relativePath))
	return strings.HasPrefix(clean, "architecture/v2/contractfixture/") ||
		strings.HasSuffix(clean, "/contract-fixture-catalog.json") ||
		clean == "contract-fixture-catalog.json"
}

func verifyEmbeddedAuthorityRoleContent(role embeddedAuthorityRole, manifest authorityBundleManifest) error {
	if role.fixture {
		return nil
	}
	expected, err := expectedBundleFiles(manifest)
	if err != nil {
		return err
	}
	for relativePath := range expected {
		if relativePath == "manifest.json" {
			continue
		}
		data, err := embeddedBundleFS.ReadFile(role.root + "/" + filepath.ToSlash(relativePath))
		if err != nil {
			return err
		}
		for _, forbidden := range []string{
			"stackkits-contract-fixture/",
			"stackkit-contract-fixture",
			"ArchitectureV2ContractFixtureCatalog",
			"ContractFixtureDefinition",
		} {
			if bytes.Contains(data, []byte(forbidden)) {
				return fmt.Errorf("embedded product authority file %s contains forbidden contract-fixture namespace %q", relativePath, forbidden)
			}
		}
	}
	return nil
}

func verifyBundleFileSet(bundleFS fs.FS, bundleRoot string, manifest authorityBundleManifest) error {
	expected, err := expectedBundleFiles(manifest)
	if err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(expected))
	err = fs.WalkDir(bundleFS, bundleRoot, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		relativePath := strings.TrimPrefix(filepath.ToSlash(path), filepath.ToSlash(bundleRoot)+"/")
		if _, ok := expected[relativePath]; !ok {
			return fmt.Errorf("embedded authority %s contains unexpected file %s", bundleRoot, relativePath)
		}
		seen[relativePath] = struct{}{}
		return nil
	})
	if err != nil {
		return err
	}
	for relativePath := range expected {
		if _, ok := seen[relativePath]; !ok {
			return fmt.Errorf("embedded authority %s is missing %s", bundleRoot, relativePath)
		}
	}
	return nil
}

func expectedBundleFiles(manifest authorityBundleManifest) (map[string]struct{}, error) {
	expected := map[string]struct{}{"manifest.json": {}}
	for relativePath := range manifest.SourceHashes {
		clean, err := cleanBundleRelativePath(relativePath)
		if err != nil {
			return nil, err
		}
		expected[clean] = struct{}{}
	}
	for _, relativePath := range manifest.Documents {
		clean, err := cleanBundleRelativePath(relativePath)
		if err != nil {
			return nil, err
		}
		expected[clean] = struct{}{}
	}
	for _, relativePath := range manifest.Profiles {
		clean, err := cleanBundleRelativePath(relativePath)
		if err != nil {
			return nil, err
		}
		expected[clean] = struct{}{}
	}
	return expected, nil
}

func cleanBundleRelativePath(relativePath string) (string, error) {
	clean := filepath.ToSlash(filepath.Clean(relativePath))
	if clean == "." || strings.HasPrefix(clean, "../") || filepath.IsAbs(relativePath) {
		return "", fmt.Errorf("unsafe embedded authority path %q", relativePath)
	}
	return clean, nil
}

func readEmbeddedDocumentForRoot(root, relativePath string) (map[string]any, error) {
	clean, err := cleanBundleRelativePath(relativePath)
	if err != nil {
		return nil, err
	}
	data, err := embeddedBundleFS.ReadFile(root + "/" + clean)
	if err != nil {
		return nil, err
	}
	return resolvedplan.DecodeDocument[map[string]any](data)
}

func embeddedBundleDigestForRoot(root string) (string, error) {
	var paths []string
	err := fs.WalkDir(embeddedBundleFS, root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			paths = append(paths, filepath.ToSlash(path))
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(paths)
	hash := sha256.New()
	for _, path := range paths {
		data, err := embeddedBundleFS.ReadFile(path)
		if err != nil {
			return "", err
		}
		_, _ = hash.Write([]byte(path))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write(data)
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func embeddedContractSourcesForRoot(root string, manifest authorityBundleManifest) (map[string][]byte, error) {
	sources := make(map[string][]byte, len(manifest.SourceHashes))
	for relativePath := range manifest.SourceHashes {
		clean, err := cleanBundleRelativePath(relativePath)
		if err != nil {
			return nil, err
		}
		data, err := embeddedBundleFS.ReadFile(root + "/" + clean)
		if err != nil {
			return nil, err
		}
		sources[clean] = data
	}
	return sources, nil
}

func embeddedVirtualModuleRoot(role, digest string) string {
	role = strings.ReplaceAll(role, " ", "-")
	if runtime.GOOS == "windows" {
		return filepath.Clean(`C:\__stackkits_cue_overlay__\` + role + `\` + digest)
	}
	return filepath.Join(string(filepath.Separator), "__stackkits_cue_overlay__", role, digest)
}

func verifyEmbeddedSourceHashesForRoot(root string, manifest authorityBundleManifest) error {
	for relativePath, wantHash := range manifest.SourceHashes {
		data, err := embeddedBundleFS.ReadFile(root + "/" + filepath.ToSlash(relativePath))
		if err != nil {
			return fmt.Errorf("read embedded source %s/%s: %w", root, relativePath, err)
		}
		if gotHash := sha256ContentHash(data); gotHash != wantHash {
			return fmt.Errorf("embedded source %s/%s hash is %s, manifest requires %s", root, relativePath, gotHash, wantHash)
		}
	}
	return nil
}

func sha256ContentHash(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}
