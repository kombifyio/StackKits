package usecasecatalog

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func Generate(repoRoot string, release ReleaseIdentity, generatorVersion string) (UseCaseManifest, CompatibilityManifest, []InternalUseCase, error) {
	return generate(repoRoot, release, generatorVersion, nil, false)
}

// LoadUseCaseComputeTiers returns package compute-tier fits for Unifier/MCP.
func LoadUseCaseComputeTiers(repoRoot string) ([]UseCase, error) {
	source, err := loadSource(repoRoot, ReleaseIdentity{SourceSHA: strings.Repeat("0", 40)})
	if err != nil {
		return nil, err
	}
	if err := attachAuthoringVocabulary(&source, source.catalogWorkloads, source.catalogModules); err != nil {
		return nil, err
	}
	// Compute-tier consumers carry portable fit facts only. Configuration
	// input metadata belongs to the released native authoring catalog.
	for i := range source.UseCases {
		for j := range source.UseCases[i].Alternatives {
			source.UseCases[i].Alternatives[j].Configuration = nil
		}
		for j := range source.UseCases[i].AddOns {
			for k := range source.UseCases[i].AddOns[j].Alternatives {
				source.UseCases[i].AddOns[j].Alternatives[k].Configuration = nil
			}
		}
	}
	return source.UseCases, nil
}

func GenerateWithReceipt(repoRoot string, release ReleaseIdentity, generatorVersion string, receipt *TestReceipt) (UseCaseManifest, CompatibilityManifest, []InternalUseCase, error) {
	return generate(repoRoot, release, generatorVersion, receipt, false)
}

func GenerateWithPassedTests(repoRoot string, release ReleaseIdentity, generatorVersion string) (UseCaseManifest, CompatibilityManifest, []InternalUseCase, error) {
	return generate(repoRoot, release, generatorVersion, nil, true)
}

func generate(repoRoot string, release ReleaseIdentity, generatorVersion string, receipt *TestReceipt, testsPassed bool) (UseCaseManifest, CompatibilityManifest, []InternalUseCase, error) {
	if err := validateRelease(release); err != nil {
		return UseCaseManifest{}, CompatibilityManifest{}, nil, err
	}
	source, err := loadSource(repoRoot, release)
	if err != nil {
		return UseCaseManifest{}, CompatibilityManifest{}, nil, err
	}
	if err := attachAuthoringVocabulary(&source, source.catalogWorkloads, source.catalogModules); err != nil {
		return UseCaseManifest{}, CompatibilityManifest{}, nil, err
	}
	generatedAt, err := generatedTime(repoRoot, release.SourceSHA)
	if err != nil {
		return UseCaseManifest{}, CompatibilityManifest{}, nil, err
	}
	useCases := UseCaseManifest{SchemaVersion: UseCaseSchema, Release: release, GeneratedAt: generatedAt, GeneratorVersion: generatorVersion}
	useCases.Catalog = catalogContent(source)
	useCases.ContentDigest, err = contentDigest(useCases)
	if err != nil {
		return UseCaseManifest{}, CompatibilityManifest{}, nil, err
	}
	compat := CompatibilityManifest{SchemaVersion: CompatibilitySchema, Release: release, GeneratedAt: generatedAt, GeneratorVersion: generatorVersion}
	compat.Compatibility.OS = source.OS
	compat.Compatibility.ApplicationDelivery, err = deliveryRows(source)
	if err != nil {
		return UseCaseManifest{}, CompatibilityManifest{}, nil, err
	}
	if err := validateRecoverySequences(source); err != nil {
		return UseCaseManifest{}, CompatibilityManifest{}, nil, err
	}
	if err := validatePlatformOwnership(source); err != nil {
		return UseCaseManifest{}, CompatibilityManifest{}, nil, err
	}
	if err := validateBackupVolumesAllocated(source); err != nil {
		return UseCaseManifest{}, CompatibilityManifest{}, nil, err
	}
	if err := validateBackupHasARestoreClaim(source); err != nil {
		return UseCaseManifest{}, CompatibilityManifest{}, nil, err
	}
	if err := validateRestoreClaimHasBackupStorage(source); err != nil {
		return UseCaseManifest{}, CompatibilityManifest{}, nil, err
	}
	if err := validateBackedUpAllocationsAreClassified(source); err != nil {
		return UseCaseManifest{}, CompatibilityManifest{}, nil, err
	}
	compat.ContentDigest, err = contentDigest(compat)
	if err != nil {
		return UseCaseManifest{}, CompatibilityManifest{}, nil, err
	}
	if err := validateTestReceipt(receipt, release.SourceSHA, source.UseCases); err != nil {
		return UseCaseManifest{}, CompatibilityManifest{}, nil, err
	}
	if testsPassed {
		passed, receiptErr := NewTestReceipt(release.SourceSHA, source.UseCases)
		if receiptErr != nil {
			return UseCaseManifest{}, CompatibilityManifest{}, nil, receiptErr
		}
		receipt = &passed
	}
	return useCases, compat, internalProjection(source, receipt, release.Tag != "v0.0.0"), nil
}

func NewTestReceipt(sourceSHA string, useCases []UseCase) (TestReceipt, error) {
	if len(sourceSHA) != 40 {
		return TestReceipt{}, fmt.Errorf("sourceSha must be a full 40-character SHA")
	}
	receipt := TestReceipt{SchemaVersion: "stackkits-use-case-test-receipt/v1", SourceSHA: sourceSHA, Status: "passed"}
	for _, useCase := range useCases {
		receipt.UseCaseRefs = append(receipt.UseCaseRefs, useCase.ID)
	}
	return receipt, nil
}

func NewEvidenceManifest(sourceSHA, generatedAt, generatorVersion string, entries []InternalUseCase) (EvidenceManifest, error) {
	manifest := EvidenceManifest{SchemaVersion: EvidenceSchema, SourceSHA: sourceSHA, GeneratedAt: generatedAt, GeneratorVersion: generatorVersion, UseCases: entries}
	var err error
	manifest.ContentDigest, err = contentDigest(manifest)
	return manifest, err
}

func WriteJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func contentDigest(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		return "", err
	}
	delete(document, "contentDigest")
	canonical, err := MarshalCanonical(document)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// MarshalCanonical encodes a decoded document the way JavaScript consumers
// recompute contentDigest (JSON.stringify with sorted keys): encoding/json
// sorts map keys, but its default HTML escaping of &, < and > would make any
// title such as "Documents & Files" fail their digest check.
func MarshalCanonical(document any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(document); err != nil {
		return nil, err
	}
	// Encode appends a newline that is not part of the canonical form.
	return bytes.TrimSuffix(buffer.Bytes(), []byte("\n")), nil
}

func validateRelease(release ReleaseIdentity) error {
	if !strings.HasPrefix(release.Tag, "v") || release.Version != strings.TrimPrefix(release.Tag, "v") {
		return fmt.Errorf("release version %q must equal tag %q without v", release.Version, release.Tag)
	}
	for name, value := range map[string]string{"sourceSha": release.SourceSHA, "publicSourceSha": release.PublicSourceSHA} {
		if len(value) != 40 {
			return fmt.Errorf("%s must be a full 40-character SHA", name)
		}
		if _, err := hex.DecodeString(value); err != nil || value != strings.ToLower(value) {
			return fmt.Errorf("%s must be a lowercase hexadecimal SHA", name)
		}
	}
	want := "https://github.com/kombifyio/StackKits/releases/tag/" + release.Tag
	if release.ReleaseURL != want {
		return fmt.Errorf("releaseUrl must be %s", want)
	}
	return nil
}

func generatedTime(root, sourceSHA string) (string, error) {
	if raw := strings.TrimSpace(os.Getenv("SOURCE_DATE_EPOCH")); raw != "" {
		seconds, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return "", fmt.Errorf("SOURCE_DATE_EPOCH: %w", err)
		}
		return time.Unix(seconds, 0).UTC().Format(time.RFC3339), nil
	}
	cmd := exec.Command("git", "show", "-s", "--format=%cI", sourceSHA)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("derive generatedAt from source SHA: %w", err)
	}
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(string(out)))
	if err != nil {
		return "", fmt.Errorf("parse source commit time: %w", err)
	}
	return parsed.UTC().Format(time.RFC3339), nil
}

func validateTestReceipt(receipt *TestReceipt, sourceSHA string, useCases []UseCase) error {
	if receipt == nil {
		return nil
	}
	if receipt.SchemaVersion != "stackkits-use-case-test-receipt/v1" || receipt.SourceSHA != sourceSHA || receipt.Status != "passed" {
		return fmt.Errorf("test receipt is not passed and bound to source SHA %s", sourceSHA)
	}
	known := map[string]bool{}
	for _, useCase := range useCases {
		known[useCase.ID] = true
	}
	previous := ""
	for _, ref := range receipt.UseCaseRefs {
		if !known[ref] {
			return fmt.Errorf("test receipt references unknown use case %q", ref)
		}
		if ref <= previous {
			return fmt.Errorf("test receipt useCaseRefs must be sorted and unique")
		}
		previous = ref
	}
	return nil
}

// Both release and candidate envelopes project the same compiled CUE facts.
// The capability belongs to this exact compiler, never a version comparison.
func catalogContent(source sourceCatalog) CatalogContent {
	var content CatalogContent
	content.UseCases, content.KitCores = source.UseCases, source.KitCores
	content.Authoring.RetainedRemovalCandidates = true
	return content
}
