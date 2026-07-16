// Package architecturecontractproof verifies the non-graduating public
// Architecture v2 contract fixture against the binary's embedded authority.
package architecturecontractproof

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

const (
	manifestSchema    = "stackkit.architecture-contract-fixtures/v2"
	manifestFile      = "contract-fixtures.manifest.json"
	compilerPrefix    = "stackkits-contract-fixture/"
	specFile          = "contract-two-node.yaml"
	inventoryFile     = "contract-two-node.inventory.yaml"
	planFile          = "contract-two-node.resolved-plan.json"
	kitSlug           = "basement-kit"
	rendererRef       = "stackkit-contract-fixture"
	authorityDocument = "contractFixtureCatalog"
)

var sha256Pattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

type fixtureManifest struct {
	SchemaVersion    string                 `json:"schemaVersion"`
	CompilerVersion  string                 `json:"compilerVersion"`
	ContractFixtures []fixtureManifestEntry `json:"contractFixtures"`
}

type fixtureManifestEntry struct {
	Spec                 string `json:"spec"`
	Inventory            string `json:"inventory"`
	Plan                 string `json:"plan"`
	Kit                  string `json:"kit"`
	AuthorityClass       string `json:"authorityClass"`
	AuthorityDocument    string `json:"authorityDocument"`
	Scope                string `json:"scope"`
	GraduationEligible   bool   `json:"graduationEligible"`
	AuthorityIssuer      string `json:"authorityIssuer"`
	AuthorityFingerprint string `json:"authorityFingerprint,omitempty"`
	AuthorityCatalogHash string `json:"authorityCatalogHash"`
	RendererRef          string `json:"rendererRef"`
	CompilerVersion      string `json:"compilerVersion"`
	SpecSHA256           string `json:"specSha256"`
	InventorySHA256      string `json:"inventorySha256"`
	PlanSHA256           string `json:"planSha256"`
	PlanHash             string `json:"planHash"`
}

// VerifyRepository recompiles the packaged contract fixture with the binary's
// embedded contract authority and requires byte-identical canonical evidence.
// Updating file hashes or planHash cannot substitute for this semantic proof.
//
//nolint:gocyclo // The proof is intentionally one fail-closed chain so no authority, digest, or byte-identity check can be skipped.
func VerifyRepository(repoRoot string) error {
	fixturesDir := filepath.Join(repoRoot, "architecture", "v2", "fixtures")
	manifestData, err := os.ReadFile(filepath.Join(fixturesDir, manifestFile))
	if err != nil {
		return fmt.Errorf("read contract fixture manifest: %w", err)
	}
	var manifest fixtureManifest
	decoder := json.NewDecoder(bytes.NewReader(manifestData))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return fmt.Errorf("decode contract fixture manifest: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return fmt.Errorf("decode contract fixture manifest: %w", err)
	}
	if manifest.SchemaVersion != manifestSchema || len(manifest.ContractFixtures) != 1 {
		return fmt.Errorf("contract fixture manifest shape is invalid")
	}
	entry := manifest.ContractFixtures[0]
	if manifest.CompilerVersion == "" || entry.CompilerVersion != manifest.CompilerVersion ||
		!strings.HasPrefix(entry.CompilerVersion, compilerPrefix) {
		return fmt.Errorf("contract fixture compiler namespace is invalid")
	}
	if entry.Spec != specFile || entry.Inventory != inventoryFile || entry.Plan != planFile || entry.Kit != kitSlug {
		return fmt.Errorf("contract fixture file or kit identity is invalid")
	}
	expectedAuthority := resolvedplan.ContractFixturePlanAuthority()
	if entry.Scope != "contract" || entry.GraduationEligible || entry.AuthorityClass != expectedAuthority.Class ||
		entry.AuthorityDocument != authorityDocument || entry.AuthorityIssuer != expectedAuthority.Issuer ||
		entry.AuthorityFingerprint != "" || !sha256Pattern.MatchString(entry.AuthorityCatalogHash) ||
		entry.RendererRef != rendererRef {
		return fmt.Errorf("contract fixture authority boundary is invalid")
	}

	spec, err := readLocalFixture(fixturesDir, entry.Spec)
	if err != nil {
		return err
	}
	inventory, err := readLocalFixture(fixturesDir, entry.Inventory)
	if err != nil {
		return err
	}
	committedPlan, err := readLocalFixture(fixturesDir, entry.Plan)
	if err != nil {
		return err
	}
	if entry.SpecSHA256 != contentSHA256(spec) || entry.InventorySHA256 != contentSHA256(inventory) ||
		entry.PlanSHA256 != contentSHA256(committedPlan) {
		return fmt.Errorf("contract fixture content digest does not match its manifest")
	}

	buildVersion := strings.TrimPrefix(entry.CompilerVersion, compilerPrefix)
	if buildVersion == "" {
		return fmt.Errorf("contract fixture build version is empty")
	}
	service, err := architecturev2.NewEmbeddedContractFixtureService(architecturev2.ContractFixtureV1Contract(buildVersion))
	if err != nil {
		return fmt.Errorf("construct embedded contract fixture authority: %w", err)
	}
	result, err := service.Resolve(architecturev2.ResolveInput{StackSpec: spec, Inventory: inventory})
	if err != nil {
		return fmt.Errorf("resolve contract fixture under embedded authority: %w", err)
	}
	if result.PlanHash != entry.PlanHash {
		return fmt.Errorf("resolved contract fixture planHash %s does not match manifest %s", result.PlanHash, entry.PlanHash)
	}
	if err := verifyPlanIdentity(result.Plan, entry, expectedAuthority); err != nil {
		return err
	}
	if !bytes.Equal(result.CanonicalPlan, committedPlan) {
		return fmt.Errorf("committed contract fixture plan is not the byte-identical resolver result")
	}
	if _, err := service.VerifyCanonicalPlan(committedPlan); err != nil {
		return fmt.Errorf("verify committed contract fixture plan under embedded authority: %w", err)
	}
	return nil
}

//nolint:gocyclo // Exhaustive identity validation keeps the fixture authority boundary explicit and fail closed.
func verifyPlanIdentity(plan resolvedplan.ResolvedPlan, entry fixtureManifestEntry, expected resolvedplan.PlanAuthority) error {
	if compiler, ok := plan["compilerVersion"].(string); !ok || compiler != entry.CompilerVersion {
		return fmt.Errorf("resolved contract fixture compiler does not match manifest")
	}
	kit, err := nestedString(plan, "kit", "slug")
	if err != nil || kit != entry.Kit {
		return fmt.Errorf("resolved contract fixture kit does not match manifest")
	}
	generation, ok := plan["generation"].(map[string]any)
	if !ok {
		return fmt.Errorf("resolved contract fixture generation is missing")
	}
	renderer, err := nestedString(generation, "renderer", "id")
	if err != nil || renderer != entry.RendererRef {
		return fmt.Errorf("resolved contract fixture renderer does not match manifest")
	}
	authority, ok := plan["authority"].(map[string]any)
	if !ok || len(authority) != 5 {
		return fmt.Errorf("resolved contract fixture authority does not match manifest")
	}
	class, classOK := authority["class"].(string)
	document, documentOK := authority["document"].(string)
	eligible, eligibleOK := authority["graduationEligible"].(bool)
	issuer, issuerOK := authority["issuer"].(string)
	catalogHash, catalogOK := authority["catalogHash"].(string)
	if !classOK || !documentOK || !eligibleOK || !issuerOK || !catalogOK ||
		class != entry.AuthorityClass || class != expected.Class || document != entry.AuthorityDocument || document != expected.Document ||
		eligible != entry.GraduationEligible || eligible != expected.GraduationEligible || issuer != entry.AuthorityIssuer || issuer != expected.Issuer ||
		catalogHash != entry.AuthorityCatalogHash {
		return fmt.Errorf("resolved contract fixture authority does not match manifest")
	}
	return nil
}

func nestedString(value map[string]any, objectField, stringField string) (string, error) {
	object, ok := value[objectField].(map[string]any)
	if !ok {
		return "", fmt.Errorf("%s is missing", objectField)
	}
	result, ok := object[stringField].(string)
	if !ok || strings.TrimSpace(result) == "" {
		return "", fmt.Errorf("%s.%s is missing", objectField, stringField)
	}
	return result, nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("unexpected trailing JSON value")
		}
		return err
	}
	return nil
}

func readLocalFixture(fixturesDir, name string) ([]byte, error) {
	if strings.TrimSpace(name) == "" || filepath.Base(name) != name || strings.ContainsAny(name, `/\\`) {
		return nil, fmt.Errorf("contract fixture path %q is not a local filename", name)
	}
	data, err := os.ReadFile(filepath.Join(fixturesDir, name))
	if err != nil {
		return nil, fmt.Errorf("read contract fixture %s: %w", name, err)
	}
	return data, nil
}

func contentSHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}
