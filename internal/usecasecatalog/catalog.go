package usecasecatalog

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	cueapi "cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	"cuelang.org/go/cue/load"
)

const (
	UseCaseSchema       = "stackkits-use-case-catalog/v1"
	EvidenceSchema      = "stackkits-use-case-evidence/v1"
	CompatibilitySchema = "stackkits-compatibility/v1"
)

type Component struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Role string `json:"role"`
	Kind string `json:"kind"`
}

type UseCase struct {
	ID          string      `json:"id"`
	Title       string      `json:"title"`
	Description string      `json:"description"`
	Components  []Component `json:"components"`
}

type ReleaseIdentity struct {
	Tag             string `json:"tag"`
	Version         string `json:"version"`
	SourceSHA       string `json:"sourceSha"`
	PublicSourceSHA string `json:"publicSourceSha"`
	ReleaseURL      string `json:"releaseUrl"`
}

type UseCaseManifest struct {
	SchemaVersion    string          `json:"schemaVersion"`
	Release          ReleaseIdentity `json:"release"`
	GeneratedAt      string          `json:"generatedAt"`
	GeneratorVersion string          `json:"generatorVersion"`
	Catalog          struct {
		UseCases []UseCase `json:"useCases"`
	} `json:"catalog"`
	ContentDigest string `json:"contentDigest"`
}

type OSCompatibility struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Version      string `json:"version"`
	Architecture string `json:"architecture"`
	Status       string `json:"status"`
	Reason       string `json:"reason,omitempty"`
	EvidenceRef  string `json:"evidenceRef,omitempty"`
}

type DeliveryCapabilities struct {
	Deployment     bool `json:"deployment"`
	RouteTLS       bool `json:"routeTLS"`
	StatusEvidence bool `json:"statusEvidence"`
	BackupRestore  bool `json:"backupRestore"`
}

type ApplicationDelivery struct {
	UseCaseRef   string               `json:"useCaseRef"`
	WorkloadRef  string               `json:"workloadRef"`
	AdapterRef   string               `json:"adapterRef"`
	AdapterName  string               `json:"adapterName"`
	Status       string               `json:"status"`
	Capabilities DeliveryCapabilities `json:"capabilities"`
}

type CompatibilityManifest struct {
	SchemaVersion    string          `json:"schemaVersion"`
	Release          ReleaseIdentity `json:"release"`
	GeneratedAt      string          `json:"generatedAt"`
	GeneratorVersion string          `json:"generatorVersion"`
	Compatibility    struct {
		OS                  []OSCompatibility     `json:"os"`
		ApplicationDelivery []ApplicationDelivery `json:"applicationDelivery"`
	} `json:"compatibility"`
	ContentDigest string `json:"contentDigest"`
}

type Gate struct {
	ID         string   `json:"id"`
	Status     string   `json:"status"`
	ReasonCode string   `json:"reasonCode"`
	Detail     string   `json:"detail"`
	Sources    []string `json:"sources"`
}

type InternalUseCase struct {
	UseCase
	Gates []Gate `json:"gates"`
}

type TestReceipt struct {
	SchemaVersion string   `json:"schemaVersion"`
	SourceSHA     string   `json:"sourceSha"`
	Status        string   `json:"status"`
	UseCaseRefs   []string `json:"useCaseRefs"`
}

type EvidenceManifest struct {
	SchemaVersion    string            `json:"schemaVersion"`
	SourceSHA        string            `json:"sourceSha"`
	GeneratedAt      string            `json:"generatedAt"`
	GeneratorVersion string            `json:"generatorVersion"`
	UseCases         []InternalUseCase `json:"useCases"`
	ContentDigest    string            `json:"contentDigest"`
}

type RuntimeEvidenceReceipt struct {
	SchemaVersion string `json:"schemaVersion"`
	SourceSHA     string `json:"sourceSha"`
	UseCases      []struct {
		UseCaseRef  string `json:"useCaseRef"`
		Status      string `json:"status"`
		EvidenceRef string `json:"evidenceRef"`
	} `json:"useCases"`
}

type sourceCatalog struct {
	UseCases                      []UseCase
	Packages                      map[string]map[string]any
	Workloads                     map[string]map[string]any
	Lifecycles                    map[string]map[string]any
	Modules                       map[string]bool
	NotApplicable                 map[string]map[string]string
	RuntimeEvidence               map[string]string
	RuntimeEvidenceSourceMismatch bool
	OS                            []OSCompatibility
}

func Generate(repoRoot string, release ReleaseIdentity, generatorVersion string) (UseCaseManifest, CompatibilityManifest, []InternalUseCase, error) {
	return generate(repoRoot, release, generatorVersion, nil, false)
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
	generatedAt, err := generatedTime(repoRoot, release.SourceSHA)
	if err != nil {
		return UseCaseManifest{}, CompatibilityManifest{}, nil, err
	}
	useCases := UseCaseManifest{SchemaVersion: UseCaseSchema, Release: release, GeneratedAt: generatedAt, GeneratorVersion: generatorVersion}
	useCases.Catalog.UseCases = source.UseCases
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
	canonical, err := json.Marshal(document)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
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

func loadSource(root string, release ReleaseIdentity) (sourceCatalog, error) {
	var registry struct {
		Entries map[string]struct {
			Slug          string               `json:"slug"`
			DisplayName   string               `json:"displayName"`
			Description   string               `json:"description"`
			Components    map[string]Component `json:"components"`
			NotApplicable map[string]struct {
				Reason string `json:"reason"`
			} `json:"notApplicable"`
		} `json:"entries"`
	}
	if err := loadCUE(root, "base", "UseCaseCatalog", &registry); err != nil {
		return sourceCatalog{}, err
	}
	var architecture struct {
		Workloads             []map[string]any `json:"workloads"`
		ApplicationLifecycles []map[string]any `json:"applicationLifecycles"`
	}
	if err := loadCUE(root, "base", "ArchitectureV2Catalog", &architecture); err != nil {
		return sourceCatalog{}, err
	}
	result := sourceCatalog{Packages: map[string]map[string]any{}, Workloads: map[string]map[string]any{}, Lifecycles: map[string]map[string]any{}, Modules: map[string]bool{}, NotApplicable: map[string]map[string]string{}, RuntimeEvidence: map[string]string{}}
	for key, entry := range registry.Entries {
		if key != entry.Slug {
			return sourceCatalog{}, fmt.Errorf("catalog key %q differs from slug %q", key, entry.Slug)
		}
		components := make([]Component, 0, len(entry.Components))
		for componentKey, component := range entry.Components {
			if componentKey != component.ID {
				return sourceCatalog{}, fmt.Errorf("use case %s component key %q differs from id %q", key, componentKey, component.ID)
			}
			components = append(components, component)
		}
		sort.Slice(components, func(i, j int) bool { return components[i].ID < components[j].ID })
		result.UseCases = append(result.UseCases, UseCase{ID: key, Title: entry.DisplayName, Description: entry.Description, Components: components})
		if len(entry.NotApplicable) > 0 {
			result.NotApplicable[key] = map[string]string{}
			for gateID, exception := range entry.NotApplicable {
				if strings.TrimSpace(exception.Reason) == "" {
					return sourceCatalog{}, fmt.Errorf("use case %s notApplicable %s has no reason", key, gateID)
				}
				result.NotApplicable[key][gateID] = exception.Reason
			}
		}
	}
	sort.Slice(result.UseCases, func(i, j int) bool { return result.UseCases[i].ID < result.UseCases[j].ID })
	for _, workload := range architecture.Workloads {
		if stringField(workload, "kind") != "application" {
			continue
		}
		ref := stringField(workload, "useCaseRef")
		id := metadataID(workload)
		if _, ok := registry.Entries[ref]; !ok {
			return sourceCatalog{}, fmt.Errorf("workload %q references unknown use case %q", id, ref)
		}
		if _, duplicate := result.Workloads[ref]; duplicate {
			return sourceCatalog{}, fmt.Errorf("multiple application workloads reference use case %q", ref)
		}
		result.Workloads[ref] = workload
	}
	for _, lifecycle := range architecture.ApplicationLifecycles {
		ref := stringField(lifecycle, "useCaseRef")
		if _, ok := registry.Entries[ref]; !ok {
			return sourceCatalog{}, fmt.Errorf("lifecycle references unknown use case %q", ref)
		}
		workloadRef := stringField(lifecycle, "workloadRef")
		workload := result.Workloads[ref]
		if workload == nil || metadataID(workload) != workloadRef {
			return sourceCatalog{}, fmt.Errorf("lifecycle %q does not match use case workload", ref)
		}
		if _, duplicate := result.Lifecycles[ref]; duplicate {
			return sourceCatalog{}, fmt.Errorf("multiple application lifecycles reference use case %q", ref)
		}
		result.Lifecycles[ref] = lifecycle
	}
	useCasesRoot := filepath.Join(root, "use-cases")
	if directories, err := os.ReadDir(useCasesRoot); err == nil {
		for _, directory := range directories {
			if !directory.IsDir() {
				continue
			}
			var pkg map[string]any
			if err := loadCUE(root, filepath.ToSlash(filepath.Join("use-cases", directory.Name())), "Package", &pkg); err != nil {
				return sourceCatalog{}, err
			}
			metadata, _ := pkg["metadata"].(map[string]any)
			ref := stringField(metadata, "useCaseRef")
			if _, ok := registry.Entries[ref]; !ok {
				return sourceCatalog{}, fmt.Errorf("package %q references unknown use case %q", directory.Name(), ref)
			}
			if ref != stringField(metadata, "category") {
				return sourceCatalog{}, fmt.Errorf("package %q category/useCaseRef drift", directory.Name())
			}
			if _, duplicate := result.Packages[ref]; duplicate {
				return sourceCatalog{}, fmt.Errorf("multiple packages reference use case %q", ref)
			}
			result.Packages[ref] = pkg
		}
	}
	modulesRoot := filepath.Join(root, "modules")
	if directories, err := os.ReadDir(modulesRoot); err == nil {
		for _, directory := range directories {
			if !directory.IsDir() || !fileExists(filepath.Join(modulesRoot, directory.Name(), "module.cue")) {
				continue
			}
			// Module directories are the stable module slug namespace; their
			// module.cue contracts are validated by the affected CUE gate.
			result.Modules[directory.Name()] = true
		}
	}
	if err := loadOS(filepath.Join(root, "docs", "data", "os-compat", "latest.json"), release, &result.OS); err != nil {
		return sourceCatalog{}, err
	}
	if err := loadRuntimeEvidence(filepath.Join(root, "docs", "data", "use-case-runtime-evidence", "latest.json"), release.SourceSHA, &result); err != nil {
		return sourceCatalog{}, err
	}
	return result, nil
}

func loadRuntimeEvidence(path, sourceSHA string, target *sourceCatalog) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var receipt RuntimeEvidenceReceipt
	if err := json.Unmarshal(data, &receipt); err != nil {
		return fmt.Errorf("decode use-case runtime evidence: %w", err)
	}
	if receipt.SchemaVersion != "stackkits-use-case-runtime-evidence/v1" {
		return fmt.Errorf("unknown use-case runtime evidence schema %q", receipt.SchemaVersion)
	}
	if receipt.SourceSHA != sourceSHA {
		target.RuntimeEvidenceSourceMismatch = true
		return nil
	}
	known := map[string]bool{}
	for _, useCase := range target.UseCases {
		known[useCase.ID] = true
	}
	previous := ""
	for _, row := range receipt.UseCases {
		if row.UseCaseRef <= previous {
			return fmt.Errorf("runtime evidence use cases must be sorted and unique")
		}
		previous = row.UseCaseRef
		if !known[row.UseCaseRef] {
			return fmt.Errorf("runtime evidence references unknown use case %q", row.UseCaseRef)
		}
		if row.Status != "passed" || !strings.HasPrefix(row.EvidenceRef, "https://") {
			return fmt.Errorf("runtime evidence for %s is not passed with public evidence", row.UseCaseRef)
		}
		target.RuntimeEvidence[row.UseCaseRef] = row.EvidenceRef
	}
	return nil
}

func loadCUE(root, directory, expression string, target any) error {
	instances := load.Instances([]string{"./" + filepath.ToSlash(directory)}, &load.Config{Dir: root, ModuleRoot: root})
	if len(instances) != 1 || instances[0].Err != nil {
		if len(instances) == 1 {
			return fmt.Errorf("load %s.%s: %w", directory, expression, instances[0].Err)
		}
		return fmt.Errorf("load %s.%s: got %d instances", directory, expression, len(instances))
	}
	value := cuecontext.New().BuildInstance(instances[0]).LookupPath(cueapi.ParsePath(expression))
	if err := value.Validate(cueapi.Concrete(true)); err != nil {
		return fmt.Errorf("%s.%s is not concrete: %w", directory, expression, err)
	}
	data, err := value.MarshalJSON()
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("decode %s.%s: %w", directory, expression, err)
	}
	return nil
}

func loadOS(path string, release ReleaseIdentity, target *[]OSCompatibility) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var source struct {
		Results []struct {
			OS          struct{ Family, Distribution, Version string } `json:"os"`
			Grade       string                                         `json:"grade"`
			ReasonCodes []string                                       `json:"reasonCodes"`
			Receipt     *struct {
				ReleaseTag      string `json:"releaseTag"`
				SourceSHA       string `json:"sourceSha"`
				PublicSourceSHA string `json:"publicSourceSha"`
				EvidenceRef     string `json:"evidenceRef"`
			} `json:"receipt"`
		} `json:"results"`
	}
	if err := json.Unmarshal(data, &source); err != nil {
		return err
	}
	for _, row := range source.Results {
		status := row.Grade
		if status == "pending" {
			status = "unverified"
		}
		id := row.OS.Distribution + "-" + row.OS.Version
		evidenceRef := ""
		if status == "supported" || status == "preview" {
			receipt := row.Receipt
			if receipt == nil || receipt.ReleaseTag != release.Tag || receipt.SourceSHA != release.SourceSHA || receipt.PublicSourceSHA != release.PublicSourceSHA || !strings.HasPrefix(receipt.EvidenceRef, "https://") {
				return fmt.Errorf("OS %s has positive status without an exact release-bound receipt", id)
			}
			evidenceRef = receipt.EvidenceRef
		} else if status != "unverified" && status != "unsupported" {
			return fmt.Errorf("OS %s has invalid status %q", id, status)
		}
		if status == "unsupported" && len(row.ReasonCodes) == 0 {
			return fmt.Errorf("OS %s is unsupported without a policy reason", id)
		}
		*target = append(*target, OSCompatibility{ID: id, Name: title(row.OS.Distribution), Version: row.OS.Version, Architecture: "amd64/arm64", Status: status, Reason: strings.Join(row.ReasonCodes, ", "), EvidenceRef: evidenceRef})
	}
	sort.Slice(*target, func(i, j int) bool { return (*target)[i].ID < (*target)[j].ID })
	return nil
}

func deliveryRows(source sourceCatalog) ([]ApplicationDelivery, error) {
	var rows []ApplicationDelivery
	seen := map[string]bool{}
	for useCaseRef, workload := range source.Workloads {
		workloadRef := metadataID(workload)
		alternatives, _ := workload["alternatives"].([]any)
		for _, rawAlternative := range alternatives {
			alternative, _ := rawAlternative.(map[string]any)
			runtime, _ := alternative["runtime"].(map[string]any)
			compatibility, _ := runtime["compatibility"].([]any)
			for _, rawRow := range compatibility {
				row, _ := rawRow.(map[string]any)
				adapter := stringField(row, "adapterRef")
				status := stringField(row, "maturity")
				if status == "contract-only" {
					status = "preview"
				}
				if status != "supported" && status != "beta" && status != "preview" && status != "unsupported" {
					return nil, fmt.Errorf("invalid delivery status %q", status)
				}
				key := useCaseRef + "/" + workloadRef + "/" + adapter
				if seen[key] {
					return nil, fmt.Errorf("duplicate application delivery row %s", key)
				}
				seen[key] = true
				capMap, _ := row["capabilities"].(map[string]any)
				rows = append(rows, ApplicationDelivery{UseCaseRef: useCaseRef, WorkloadRef: workloadRef, AdapterRef: adapter, AdapterName: adapterName(adapter), Status: status, Capabilities: DeliveryCapabilities{Deployment: boolField(capMap, "deployment"), RouteTLS: boolField(capMap, "routeTLS"), StatusEvidence: boolField(capMap, "statusEvidence"), BackupRestore: boolField(capMap, "backupRestore")}})
			}
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.UseCaseRef != b.UseCaseRef {
			return a.UseCaseRef < b.UseCaseRef
		}
		return a.AdapterRef < b.AdapterRef
	})
	return rows, nil
}

func internalProjection(source sourceCatalog, receipt *TestReceipt, releaseProjection bool) []InternalUseCase {
	result := make([]InternalUseCase, 0, len(source.UseCases))
	for _, useCase := range source.UseCases {
		pkg, workload, lifecycle := source.Packages[useCase.ID], source.Workloads[useCase.ID], source.Lifecycles[useCase.ID]
		completeComponents := componentClosure(useCase, source.Modules)
		tested := receiptCovers(receipt, useCase.ID)
		runtimeEvidenceRef, runtimeEvidence := source.RuntimeEvidence[useCase.ID]
		runtimeReason := "RUNTIME_EVIDENCE_MISSING"
		if source.RuntimeEvidenceSourceMismatch {
			runtimeReason = "RUNTIME_EVIDENCE_SOURCE_MISMATCH"
		}
		packageSource := "base/use_case_catalog.cue"
		if pkg != nil {
			packageSource = "use-cases/" + useCase.ID + "/use-case.cue"
		}
		gates := []Gate{
			completeGate("product-intent", "typed CUE catalog entry exists", "base/use_case_catalog.cue"),
			gate("use-case-package", pkg != nil, "typed UseCasePackage exists", "USE_CASE_PACKAGE_MISSING", packageSource),
			gate("runtime-workload", workload != nil, "Architecture v2 application workload exists", "RUNTIME_WORKLOAD_MISSING", "base/architecture_v2_catalog.cue"),
			gate("component-closure", completeComponents, "all declared component IDs resolve to module contracts", "COMPONENT_REFERENCE_MISSING", "base/use_case_catalog.cue"),
			gate("delivery-adapter", hasDelivery(workload), "runtime workload declares delivery adapter rows", "DELIVERY_ADAPTER_MISSING", "base/architecture_v2_catalog.cue"),
			gate("setup-network-auth-data-backup", hasOperationalShape(pkg, workload), "setup and runtime data/backup shape exist", "OPERATIONAL_CONTRACTS_INCOMPLETE", "docs/USE_CASE_PACKAGES.md", "base/architecture_v2_catalog.cue"),
			gate("application-lifecycle", lifecycle != nil, "seven-stage Architecture v2 lifecycle exists", "APPLICATION_LIFECYCLE_MISSING", "base/architecture_v2_catalog.cue"),
			gate("source-sha-tests", tested, "source-SHA-bound catalog tests passed", "SOURCE_SHA_TEST_EVIDENCE_MISSING", "schemas/stackkits-use-case-evidence-v1.schema.json"),
			gate("runtime-evidence", runtimeEvidence, "runtime evidence is bound to the same source SHA: "+runtimeEvidenceRef, runtimeReason, "docs/data/use-case-runtime-evidence/latest.json"),
			gate("release-documentation", releaseProjection, "release manifests are being emitted for a published tag", "RELEASE_DOCUMENTATION_PENDING", "schemas/stackkits-use-case-catalog-v1.schema.json"),
		}
		for index := range gates {
			if reason, ok := source.NotApplicable[useCase.ID][gates[index].ID]; ok {
				gates[index] = Gate{ID: gates[index].ID, Status: "not-applicable", ReasonCode: "NOT_APPLICABLE_WITH_REASON", Detail: reason, Sources: []string{"base/use_case_catalog.cue"}}
			}
		}
		result = append(result, InternalUseCase{UseCase: useCase, Gates: gates})
	}
	return result
}

func RenderInternalMarkdown(entries []InternalUseCase) string {
	var b strings.Builder
	encoded, _ := json.Marshal(entries)
	digest := sha256.Sum256(encoded)
	fmt.Fprintf(&b, "<!-- Code generated by 'stackkit docs emit-use-case-overview'. DO NOT EDIT. projection_digest: sha256:%s -->\n\n# Use Case Development Overview\n\nThis internal projection is derived from CUE, Architecture v2 and source-bound evidence. Package metadata never overrides runtime authority.\n\n", hex.EncodeToString(digest[:]))
	for _, entry := range entries {
		fmt.Fprintf(&b, "## %s (`%s`)\n\n", entry.Title, entry.ID)
		fmt.Fprintf(&b, "%s\n\n**Components:** ", entry.Description)
		for i, component := range entry.Components {
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "%s (%s)", component.Name, component.Role)
		}
		b.WriteString("\n\n| Gate | Result | Reason code | Detail | Sources |\n| --- | --- | --- | --- | --- |\n")
		for _, gate := range entry.Gates {
			fmt.Fprintf(&b, "| `%s` | %s | `%s` | %s | %s |\n", gate.ID, gate.Status, gate.ReasonCode, gate.Detail, linkSources(gate.Sources))
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}
func linkSources(sources []string) string {
	links := make([]string, 0, len(sources))
	for _, source := range sources {
		target := "../" + source
		if strings.HasPrefix(source, "docs/") {
			target = strings.TrimPrefix(source, "docs/")
		}
		links = append(links, fmt.Sprintf("[%s](%s)", source, target))
	}
	return strings.Join(links, ", ")
}

func gate(id string, ok bool, good, missing string, sources ...string) Gate {
	if ok {
		return Gate{ID: id, Status: "complete", ReasonCode: "GATE_COMPLETE", Detail: good, Sources: sources}
	}
	return Gate{ID: id, Status: "missing", ReasonCode: missing, Detail: "required source or evidence is absent", Sources: sources}
}
func completeGate(id, detail string, sources ...string) Gate {
	return Gate{ID: id, Status: "complete", ReasonCode: "GATE_COMPLETE", Detail: detail, Sources: sources}
}
func missingGate(id, reason string, sources ...string) Gate {
	return Gate{ID: id, Status: "missing", ReasonCode: reason, Detail: "required source or evidence is absent", Sources: sources}
}
func hasDelivery(workload map[string]any) bool {
	if workload == nil {
		return false
	}
	alternatives, _ := workload["alternatives"].([]any)
	for _, raw := range alternatives {
		alt, _ := raw.(map[string]any)
		runtime, _ := alt["runtime"].(map[string]any)
		rows, _ := runtime["compatibility"].([]any)
		if len(rows) > 0 {
			return true
		}
	}
	return false
}
func hasOperationalShape(pkg, workload map[string]any) bool {
	if pkg == nil || workload == nil {
		return false
	}
	if _, ok := pkg["setup"].(map[string]any); !ok {
		return false
	}
	alternatives, _ := workload["alternatives"].([]any)
	for _, raw := range alternatives {
		alt, _ := raw.(map[string]any)
		infra, _ := alt["infrastructure"].(map[string]any)
		if infra["dataBinding"] != nil && infra["backupSource"] != nil {
			return true
		}
	}
	return false
}
func componentClosure(useCase UseCase, known map[string]bool) bool {
	for _, component := range useCase.Components {
		if !known[component.ID] {
			return false
		}
	}
	return true
}
func receiptCovers(receipt *TestReceipt, ref string) bool {
	if receipt == nil || receipt.Status != "passed" {
		return false
	}
	for _, item := range receipt.UseCaseRefs {
		if item == ref {
			return true
		}
	}
	return false
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
func fileExists(path string) bool { info, err := os.Stat(path); return err == nil && !info.IsDir() }
func metadataID(value map[string]any) string {
	metadata, _ := value["metadata"].(map[string]any)
	return stringField(metadata, "id")
}
func stringField(value map[string]any, field string) string {
	result, _ := value[field].(string)
	return result
}
func boolField(value map[string]any, field string) bool {
	result, _ := value[field].(bool)
	return result
}
func adapterName(ref string) string {
	switch ref {
	case "coolify":
		return "Coolify"
	case "komodo":
		return "Komodo"
	case "standalone-compose":
		return "Standalone Compose"
	default:
		return ref
	}
}
func title(value string) string {
	if value == "" {
		return value
	}
	return strings.ToUpper(value[:1]) + value[1:]
}
