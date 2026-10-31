package commands

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// compat emit-os-matrix renders the committed public compatibility projection
// (docs/data/os-compat/latest.json, written only by the receipt projector
// scripts/compat/project-os-compat.mjs) into docs and the website feed.
// Private diagnostics are rejected here even when they have been redacted.
var (
	emitOSMatrixInput      string
	emitOSMatrixArchiveDir string
	emitOSMatrixDocsOut    string
	emitOSMatrixWebsiteOut string
)

var compatEmitOSMatrixCmd = &cobra.Command{
	Use:   "emit-os-matrix",
	Short: "Render the public OS and virtualization compatibility projection",
	RunE:  runCompatEmitOSMatrix,
}

func init() {
	compatEmitOSMatrixCmd.Flags().StringVar(&emitOSMatrixInput, "input", "docs/data/os-compat/latest.json", "committed public compatibility projection")
	compatEmitOSMatrixCmd.Flags().StringVar(&emitOSMatrixArchiveDir, "archive-dir", "docs/data/os-compat", "directory holding per-release v*.json archives")
	compatEmitOSMatrixCmd.Flags().StringVar(&emitOSMatrixDocsOut, "docs-out", "docs/OS_COMPATIBILITY.md", "generated markdown output")
	compatEmitOSMatrixCmd.Flags().StringVar(&emitOSMatrixWebsiteOut, "website-out", "website/public/os-compat.json", "generated website feed output")
	compatCmd.AddCommand(compatEmitOSMatrixCmd)
}

type osMatrixDoc struct {
	SchemaVersion             int                          `json:"schemaVersion"`
	StackKitsVersion          string                       `json:"stackkitsVersion"`
	GeneratedAt               string                       `json:"generatedAt"`
	Results                   []osMatrixDocRow             `json:"results"`
	Virtualization            []osMatrixVirtRow            `json:"virtualization,omitempty"`
	Applications              []osMatrixAppRow             `json:"applications,omitempty"`
	Environments              []osMatrixEnvRow             `json:"environments,omitempty"`
	RequestedStackKitsVersion string                       `json:"requestedStackkitsVersion,omitempty"`
	TestedStackKitsVersion    json.RawMessage              `json:"testedStackkitsVersion,omitempty"`
	ReferenceBinding          *osMatrixReferenceBinding    `json:"referenceBinding,omitempty"`
	CurrentResult             string                       `json:"currentResult,omitempty"`
	CurrentPassCount          *int                         `json:"currentPassCount,omitempty"`
	RuntimeObservations       []osMatrixRuntimeObservation `json:"runtimeObservations,omitempty"`
	WindowCoverage            *osMatrixWindowCoverage      `json:"windowCoverage,omitempty"`
}

// Advisory runtime observations have their own exact release/artifact binding.
// They never participate in the OS, hypervisor, application or environment grade.
type osMatrixRuntimeBindings struct {
	SourceCommit              string `json:"sourceCommit,omitempty"`
	ProducerCommit            string `json:"producerCommit,omitempty"`
	ConsumerSHA               string `json:"consumerSha,omitempty"`
	ArtifactSHA256            string `json:"artifactSha256,omitempty"`
	IndexSHA256               string `json:"indexSha256,omitempty"`
	CatalogSHA256             string `json:"catalogSha256,omitempty"`
	ScenarioDefinitionsDigest string `json:"scenarioDefinitionsDigest,omitempty"`
	CandidateDigest           string `json:"candidateDigest,omitempty"`
}

type osMatrixRuntimeIdentity struct {
	Family       string `json:"family"`
	Distribution string `json:"distribution"`
	Version      string `json:"version"`
	Arch         string `json:"arch"`
	Environment  string `json:"environment"`
}

type osMatrixReferenceBinding struct {
	Scope            string `json:"scope,omitempty"`
	RequestedRelease string `json:"requestedRelease"`
	OperationScope   string `json:"operationScope,omitempty"`
	Platform         string `json:"platform,omitempty"`
	osMatrixRuntimeBindings
	RuntimeIdentity *osMatrixRuntimeIdentity `json:"runtimeIdentity,omitempty"`
	RequiredPhases  []string                 `json:"requiredPhases,omitempty"`
}

type osMatrixRuntimeOperation struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

type osMatrixRuntimeObservation struct {
	SchemaVersion string `json:"schemaVersion"`
	ID            string `json:"id"`
	Subject       struct {
		Kind     string `json:"kind"`
		Product  string `json:"product"`
		UseCase  string `json:"useCase,omitempty"`
		Platform string `json:"platform,omitempty"`
	} `json:"subject"`
	OriginalOutcome string `json:"originalOutcome"`
	OperationScope  string `json:"operationScope,omitempty"`
	ReceiptSHA256   string `json:"receiptSha256"`
	TestedRelease   string `json:"testedRelease,omitempty"`
	osMatrixRuntimeBindings
	ControllerSourceCommit    string                   `json:"controllerSourceCommit,omitempty"`
	EvidenceIndexSHA256       string                   `json:"evidenceIndexSha256,omitempty"`
	SuiteBindingSHA256        string                   `json:"suiteBindingSha256,omitempty"`
	SuiteCandidateDigest      string                   `json:"suiteCandidateDigest,omitempty"`
	GitHubNativeArchiveSHA256 string                   `json:"githubNativeArchiveSha256,omitempty"`
	GitHubSuiteArchiveSHA256  string                   `json:"githubSuiteArchiveSha256,omitempty"`
	GitHubMetadataSHA256      string                   `json:"githubMetadataSha256,omitempty"`
	WindowReportSHA256        string                   `json:"windowReportSha256,omitempty"`
	SuiteBindingState         string                   `json:"suiteBindingState,omitempty"`
	ProducerArtifactSHA256    string                   `json:"producerArtifactSha256,omitempty"`
	ObservedAt                string                   `json:"observedAt,omitempty"`
	RuntimeIdentity           *osMatrixRuntimeIdentity `json:"runtimeIdentity,omitempty"`
	Capacity                  *struct {
		CPUs      int `json:"cpus"`
		MemoryMiB int `json:"memoryMiB"`
		DiskGiB   int `json:"diskGiB"`
	} `json:"capacity,omitempty"`
	Operations     []osMatrixRuntimeOperation `json:"operations"`
	ExecutionState string                     `json:"executionState"`
	Applicability  string                     `json:"applicability"`
	CurrentResult  string                     `json:"currentResult"`
	ReasonCodes    []string                   `json:"reasonCodes"`
}

type osMatrixWindowCoverage struct {
	ReportSHA256    string `json:"reportSha256"`
	CandidateDigest string `json:"candidateDigest"`
	GeneratedAt     string `json:"generatedAt"`
	Result          string `json:"result"`
	Declared        int    `json:"declared"`
	Passed          int    `json:"passed"`
	Pending         int    `json:"pending"`
	Failed          int    `json:"failed"`
	Blocked         int    `json:"blocked"`
	NotRun          int    `json:"notRun"`
	NotProjected    *int   `json:"notProjected,omitempty"`
	Obligations     []struct {
		Suite  string `json:"suite"`
		Result string `json:"result"`
	} `json:"obligations"`
}

type osMatrixEvidence struct {
	Grade               string   `json:"grade"`
	ReasonCodes         []string `json:"reasonCodes"`
	VerifiedPhases      []string `json:"verifiedPhases,omitempty"`
	LastVerifiedRelease string   `json:"lastVerifiedRelease,omitempty"`
}

type osMatrixDocRow struct {
	OS            osMatrixDocOS `json:"os"`
	Architectures []string      `json:"architectures,omitempty"`
	osMatrixEvidence
}

type osMatrixDocOS struct {
	Family       string `json:"family"`
	Distribution string `json:"distribution"`
	Version      string `json:"version"`
}

// osMatrixVirtRow is a hypervisor rollout target: StackKits run in a guest VM
// created on that hypervisor, and Rollout says how that guest is created.
type osMatrixVirtRow struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Rollout string `json:"rollout"`
	osMatrixEvidence
}

// osMatrixEnvRow grades one kit in one kind of environment (for example a
// fresh CI virtual machine or a virtual machine on a home network).
type osMatrixEnvRow struct {
	Kit         string `json:"kit"`
	Environment string `json:"environment"`
	Name        string `json:"name"`
	osMatrixEvidence
}

type osMatrixAppRow struct {
	UseCase string `json:"useCase"`
	Adapter string `json:"adapter"`
	osMatrixEvidence
}

var (
	osMatrixForbiddenKeys = regexp.MustCompile(`"(runId|lane|stages|stage|overall|target|arch|architecture|kernel|packageMgr|initSystem|virtType|virtTier|runtime|engine|osReleaseRaw|evidencePath|mdnsHost|host|hostname|provider|device|resourceId|lease|cleanupState|producerCommit|attemptId|imageSha256|configSha256)"\s*:`)
	osMatrixRFC1918       = regexp.MustCompile(`(^|[^0-9.])(10\.[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}|192\.168\.[0-9]{1,3}\.[0-9]{1,3}|172\.(1[6-9]|2[0-9]|3[01])\.[0-9]{1,3}\.[0-9]{1,3})([^0-9.]|$)`)
	// Hypervisor names are public in the virtualization rows only; OS and
	// application rows stay free of infrastructure vocabulary, and server
	// providers never appear anywhere.
	osMatrixInfraText    = regexp.MustCompile(`(?i)\b(docker|container|wsl2?|proxmox|pico\s*kvm|kvm|hypervisor|bare[ -]?metal|virtual(?:ization| machine))\b`)
	osMatrixProviderText = regexp.MustCompile(`(?i)\b(ionos|centron|hetzner|netcup|contabo|digitalocean|linode|vultr|ovh)\b`)
	osMatrixSlug         = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]*$`)
	osMatrixVersion      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	osMatrixRelease      = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
	osMatrixV2Release    = regexp.MustCompile(`^(unreleased|v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?)$`)
	osMatrixPhase        = regexp.MustCompile(`^(install|init|generate|apply|verify|backup|restore|lan-access|setup-[a-z0-9-]+)$`)
	osMatrixKits         = map[string]bool{"basement-kit": true, "cloud-kit": true, "modern-homelab": true}
)

// Closed public reason codes. The two v2 codes stay readable for archives.
var osMatrixReasonLabels = map[string]string{
	"current-release-receipt-pending":   "No lifecycle receipt for this release yet",
	"no-automated-lane":                 "Not covered by the automated lifecycle tests yet",
	"install-failed":                    "Install phase failed on this release",
	"init-failed":                       "Init phase failed on this release",
	"generate-failed":                   "Generate phase failed on this release",
	"apply-failed":                      "Apply phase failed on this release",
	"setup-failed":                      "Application setup failed on this release",
	"verify-failed":                     "Verify phase failed on this release",
	"backup-failed":                     "Backup phase failed on this release",
	"restore-failed":                    "Restore phase failed on this release",
	"lan-access-failed":                 "Local service addresses were not reachable from the home network",
	"cleanup-failed":                    "Cleanup after the lifecycle run failed",
	"current-candidate-receipt-pending": "Current candidate receipt pending",
	"os-policy-not-yet-admitted":        "OS policy not yet admitted",
}

// Grade meanings shared word for word with the website Compatibility page and
// the Mintlify compatibility references.
var osMatrixGradeMeaning = map[string]string{
	"supported":  "Every lifecycle phase passed in the newest run on this release.",
	"preview":    "Install through verify passed in the newest run; a later phase failed.",
	"unverified": "No completed run on this release yet, or the newest run failed before verify.",
}

const osMatrixBestCellNote = "Operating-system and hypervisor rows show the best result across the environments tested on this release; Kits by environment lists each environment on its own."

func runCompatEmitOSMatrix(cmd *cobra.Command, args []string) error {
	raw, err := os.ReadFile(emitOSMatrixInput)
	if err != nil {
		return fmt.Errorf("read matrix input: %w", err)
	}
	matrix, err := decodeOSMatrix(raw)
	if err != nil {
		return err
	}
	if matrix.SchemaVersion != 3 {
		return fmt.Errorf("matrix input must be public schemaVersion 3; schemaVersion 2 is readable only as an archive")
	}

	sourceHash := sha256.Sum256(raw)
	if err := writeGenerated(emitOSMatrixDocsOut, []byte(renderOSMatrixMarkdown(matrix, fmt.Sprintf("%x", sourceHash)))); err != nil {
		return err
	}
	feed, err := renderOSMatrixFeed(matrix, emitOSMatrixArchiveDir)
	if err != nil {
		return err
	}
	if err := writeGenerated(emitOSMatrixWebsiteOut, feed); err != nil {
		return err
	}
	fmt.Printf("emitted %s and %s from %s (%d OS, %d virtualization, %d application, %d environment rows)\n", emitOSMatrixDocsOut, emitOSMatrixWebsiteOut, emitOSMatrixInput, len(matrix.Results), len(matrix.Virtualization), len(matrix.Applications), len(matrix.Environments))
	return nil
}

func decodeOSMatrix(raw []byte) (osMatrixDoc, error) {
	if osMatrixRFC1918.Match(raw) {
		return osMatrixDoc{}, fmt.Errorf("matrix input contains RFC1918 addresses")
	}
	if osMatrixProviderText.Match(raw) {
		return osMatrixDoc{}, fmt.Errorf("matrix input names a server provider")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var matrix osMatrixDoc
	if err := decoder.Decode(&matrix); err != nil {
		return osMatrixDoc{}, fmt.Errorf("parse OS compatibility projection: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return osMatrixDoc{}, fmt.Errorf("parse OS compatibility projection: trailing JSON data")
	}
	if err := validateOSMatrixRuntimeObservations(matrix); err != nil {
		return osMatrixDoc{}, err
	}
	// New advisory keys such as producerCommit and runtimeIdentity.arch are
	// permitted only inside the strict typed structure above. Legacy rows retain
	// their existing diagnostic/infrastructure prohibition.
	legacy := matrix
	legacy.RequestedStackKitsVersion, legacy.TestedStackKitsVersion = "", nil
	legacy.ReferenceBinding, legacy.CurrentResult, legacy.CurrentPassCount = nil, "", nil
	legacy.RuntimeObservations, legacy.WindowCoverage = nil, nil
	legacyRaw, err := json.Marshal(legacy)
	if err != nil {
		return osMatrixDoc{}, err
	}
	if match := osMatrixForbiddenKeys.Find(legacyRaw); match != nil {
		return osMatrixDoc{}, fmt.Errorf("matrix input carries forbidden diagnostic/infrastructure field %s", string(match))
	}
	if _, err := time.Parse(time.RFC3339, matrix.GeneratedAt); err != nil {
		return osMatrixDoc{}, fmt.Errorf("matrix generatedAt must be RFC3339: %w", err)
	}
	if len(matrix.Results) == 0 {
		return osMatrixDoc{}, fmt.Errorf("matrix input needs at least one OS result")
	}
	switch matrix.SchemaVersion {
	case 2:
		if !osMatrixV2Release.MatchString(matrix.StackKitsVersion) || len(matrix.Virtualization) > 0 || len(matrix.Applications) > 0 {
			return osMatrixDoc{}, fmt.Errorf("schemaVersion 2 archives carry only OS rows and a version")
		}
	case 3:
		if !osMatrixRelease.MatchString(matrix.StackKitsVersion) {
			return osMatrixDoc{}, fmt.Errorf("schemaVersion 3 must be bound to an exact public release tag, got %q", matrix.StackKitsVersion)
		}
	default:
		return osMatrixDoc{}, fmt.Errorf("unknown compatibility schemaVersion %d", matrix.SchemaVersion)
	}

	infraText, err := json.Marshal(struct {
		Results      []osMatrixDocRow `json:"results"`
		Applications []osMatrixAppRow `json:"applications"`
	}{matrix.Results, matrix.Applications})
	if err != nil {
		return osMatrixDoc{}, err
	}
	if osMatrixInfraText.Match(infraText) {
		return osMatrixDoc{}, fmt.Errorf("OS and application rows must not contain infrastructure/runtime terminology")
	}

	identities := map[string]struct{}{}
	unique := func(kind, key string) error {
		if _, exists := identities[kind+"|"+key]; exists {
			return fmt.Errorf("matrix duplicates %s identity %s", kind, key)
		}
		identities[kind+"|"+key] = struct{}{}
		return nil
	}
	for i, row := range matrix.Results {
		if !osMatrixSlug.MatchString(row.OS.Family) || !osMatrixSlug.MatchString(row.OS.Distribution) || !osMatrixVersion.MatchString(row.OS.Version) {
			return osMatrixDoc{}, fmt.Errorf("matrix result %d has an incomplete OS identity", i)
		}
		if err := unique("os", osMatrixRowKey(row)); err != nil {
			return osMatrixDoc{}, err
		}
		for _, arch := range row.Architectures {
			if arch != "amd64" && arch != "arm64" {
				return osMatrixDoc{}, fmt.Errorf("matrix result %d has unknown architecture %q", i, arch)
			}
		}
		if err := validateOSMatrixEvidence(matrix.SchemaVersion, fmt.Sprintf("OS result %d", i), row.osMatrixEvidence); err != nil {
			return osMatrixDoc{}, err
		}
	}
	for i, row := range matrix.Virtualization {
		if !osMatrixSlug.MatchString(row.ID) || strings.TrimSpace(row.Name) == "" {
			return osMatrixDoc{}, fmt.Errorf("virtualization row %d needs an id and a name", i)
		}
		// Where a lifecycle guest happened to run is not a hypervisor claim; a row
		// exists only as a rollout target that says how the guest VM is created.
		if strings.TrimSpace(row.Rollout) == "" || len(row.Rollout) > 240 || strings.ContainsAny(row.Rollout, "<>|\n") {
			return osMatrixDoc{}, fmt.Errorf("virtualization row %d needs a rollout description of how the guest VM is created (at most 240 characters, no markup)", i)
		}
		if err := unique("virtualization", row.ID); err != nil {
			return osMatrixDoc{}, err
		}
		if err := validateOSMatrixEvidence(matrix.SchemaVersion, fmt.Sprintf("virtualization row %d", i), row.osMatrixEvidence); err != nil {
			return osMatrixDoc{}, err
		}
	}
	for i, row := range matrix.Environments {
		if matrix.SchemaVersion < 3 || !osMatrixKits[row.Kit] || !osMatrixSlug.MatchString(row.Environment) {
			return osMatrixDoc{}, fmt.Errorf("environment row %d needs a known kit and an environment id", i)
		}
		if strings.TrimSpace(row.Name) == "" || len(row.Name) > 80 || strings.ContainsAny(row.Name, "<>|\n") {
			return osMatrixDoc{}, fmt.Errorf("environment row %d needs a short plain name", i)
		}
		if err := unique("environment", row.Kit+"@"+row.Environment); err != nil {
			return osMatrixDoc{}, err
		}
		if err := validateOSMatrixEvidence(matrix.SchemaVersion, fmt.Sprintf("environment row %d", i), row.osMatrixEvidence); err != nil {
			return osMatrixDoc{}, err
		}
	}
	for i, row := range matrix.Applications {
		if !osMatrixSlug.MatchString(row.UseCase) || !osMatrixSlug.MatchString(row.Adapter) {
			return osMatrixDoc{}, fmt.Errorf("application row %d needs a use case and an adapter", i)
		}
		if err := unique("application", row.UseCase+"/"+row.Adapter); err != nil {
			return osMatrixDoc{}, err
		}
		if err := validateOSMatrixEvidence(matrix.SchemaVersion, fmt.Sprintf("application row %d", i), row.osMatrixEvidence); err != nil {
			return osMatrixDoc{}, err
		}
	}
	return matrix, nil
}

func validateOSMatrixEvidence(schemaVersion int, label string, evidence osMatrixEvidence) error {
	switch evidence.Grade {
	case "unverified":
	case "supported", "preview":
		if schemaVersion < 3 {
			return fmt.Errorf("%s: schemaVersion 2 rows must remain unverified", label)
		}
	case "unsupported":
		return fmt.Errorf("%s: unsupported requires a versioned OS support policy, which does not exist yet", label)
	default:
		return fmt.Errorf("%s has unknown grade %q", label, evidence.Grade)
	}
	if evidence.Grade == "supported" && len(evidence.ReasonCodes) > 0 {
		return fmt.Errorf("%s: a supported row carries no reason code", label)
	}
	if evidence.Grade != "supported" && len(evidence.ReasonCodes) == 0 {
		return fmt.Errorf("%s requires a closed reason code", label)
	}
	seen := map[string]struct{}{}
	for _, code := range evidence.ReasonCodes {
		if _, known := osMatrixReasonLabels[code]; !known {
			return fmt.Errorf("%s has unknown public reason code %q", label, code)
		}
		if _, exists := seen[code]; exists {
			return fmt.Errorf("%s duplicates public reason code %q", label, code)
		}
		seen[code] = struct{}{}
	}
	for _, phase := range evidence.VerifiedPhases {
		if !osMatrixPhase.MatchString(phase) {
			return fmt.Errorf("%s has unknown lifecycle phase %q", label, phase)
		}
	}
	if evidence.LastVerifiedRelease != "" && !osMatrixRelease.MatchString(evidence.LastVerifiedRelease) {
		return fmt.Errorf("%s has an invalid lastVerifiedRelease %q", label, evidence.LastVerifiedRelease)
	}
	return nil
}

func osMatrixOneOf(value string, allowed ...string) bool {
	for _, item := range allowed {
		if value == item {
			return true
		}
	}
	return false
}

func osMatrixBindingValues(binding osMatrixRuntimeBindings) map[string]string {
	return map[string]string{"sourceCommit": binding.SourceCommit, "producerCommit": binding.ProducerCommit, "consumerSha": binding.ConsumerSHA,
		"artifactSha256": binding.ArtifactSHA256, "indexSha256": binding.IndexSHA256, "catalogSha256": binding.CatalogSHA256,
		"scenarioDefinitionsDigest": binding.ScenarioDefinitionsDigest, "candidateDigest": binding.CandidateDigest}
}

func validateOSMatrixPublicDigests(value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		return err
	}
	for key, data := range fields {
		length := 0
		if strings.HasSuffix(key, "Commit") || key == "consumerSha" {
			length = 40
		}
		if strings.HasSuffix(key, "Sha256") || strings.HasSuffix(key, "Digest") {
			length = 64
		}
		if length == 0 {
			continue
		}
		var text string
		if json.Unmarshal(data, &text) != nil || len(text) != length || strings.Trim(text, "0123456789abcdef") != "" {
			return fmt.Errorf("invalid public runtime digest binding")
		}
	}
	return nil
}

func validOSMatrixRuntime(identity *osMatrixRuntimeIdentity) bool {
	return identity == nil || identity.Family == "linux" && osMatrixOneOf(identity.Distribution, "ubuntu", "unknown") &&
		osMatrixOneOf(identity.Version, "26.04", "24.04", "unknown") && osMatrixOneOf(identity.Arch, "amd64", "arm64", "unknown") &&
		osMatrixOneOf(identity.Environment, "depot-vm", "disposable-vm", "retained-lxd-guest")
}

func validOSMatrixRuntimePhase(phase string) bool {
	return osMatrixOneOf(phase, "install", "init", "generate", "apply", "verify", "backup", "restore", "setup-files", "setup-photos",
		"files-restart", "photos-restart", "owner-setup", "enrollment", "connected", "independent-readback", "cleanup", "remove", "retain-data")
}

func validateOSMatrixWindowCoverage(coverage *osMatrixWindowCoverage) error {
	if coverage == nil {
		return nil
	}
	if err := validateOSMatrixPublicDigests(coverage); err != nil {
		return err
	}
	if _, err := time.Parse(time.RFC3339, coverage.GeneratedAt); err != nil {
		return fmt.Errorf("invalid runtime window timestamp")
	}
	if !osMatrixOneOf(coverage.Result, "passed", "pending", "failed", "blocked", "not-run") || len(coverage.Obligations) == 0 {
		return fmt.Errorf("invalid public runtime window coverage")
	}
	counts, seen := map[string]int{}, map[string]bool{}
	for _, row := range coverage.Obligations {
		if !osMatrixOneOf(row.Suite, "stackkits-standalone-files-restore", "stackkits-standalone-photos-restore",
			"stackkits-standalone-files-restore-ubuntu-24.04", "stackkits-standalone-photos-restore-ubuntu-24.04",
			"stackkits-standalone-files-restore-debian-13", "stackkits-standalone-photos-restore-debian-13", "stackkits-retained-marketplace",
			"stackkits-coolify-files-restore", "stackkits-coolify-photos-restore") ||
			!osMatrixOneOf(row.Result, "passed", "pending", "failed", "blocked", "not-run") || seen[row.Suite] {
			return fmt.Errorf("invalid or duplicate runtime window obligation")
		}
		seen[row.Suite] = true
		counts[row.Result]++
	}
	// Obligations without a coverage codec are declared but only counted.
	notProjected := 0
	if coverage.NotProjected != nil {
		if *coverage.NotProjected < 1 {
			return fmt.Errorf("invalid public runtime window coverage")
		}
		notProjected = *coverage.NotProjected
	}
	if coverage.Declared != len(coverage.Obligations)+notProjected || coverage.Passed != counts["passed"] || coverage.Pending != counts["pending"] ||
		coverage.Failed != counts["failed"] || coverage.Blocked != counts["blocked"] || coverage.NotRun != counts["not-run"] {
		return fmt.Errorf("runtime window coverage totals disagree with its obligations")
	}
	return nil
}

func validateOSMatrixRuntimeObservations(matrix osMatrixDoc) error {
	advisory := matrix.ReferenceBinding != nil || matrix.RequestedStackKitsVersion != "" || len(matrix.TestedStackKitsVersion) > 0 ||
		matrix.CurrentResult != "" || matrix.CurrentPassCount != nil || matrix.RuntimeObservations != nil || matrix.WindowCoverage != nil
	if !advisory {
		return nil
	}
	bad := func() error { return fmt.Errorf("invalid public runtime observation binding") }
	binding := matrix.ReferenceBinding
	if matrix.SchemaVersion != 3 || !osMatrixRelease.MatchString(matrix.RequestedStackKitsVersion) || binding == nil ||
		binding.RequestedRelease != matrix.RequestedStackKitsVersion || !osMatrixOneOf(binding.Scope, "product-artifact", "test-window-candidate") ||
		!validOSMatrixRuntime(binding.RuntimeIdentity) || binding.OperationScope != "" && binding.OperationScope != "retained-selective-files-removal" ||
		binding.Platform != "" && (binding.Platform != "coolify" || binding.OperationScope != "") || matrix.CurrentPassCount == nil || *matrix.CurrentPassCount < 0 {
		return bad()
	}
	if len(matrix.TestedStackKitsVersion) == 0 {
		return bad()
	}
	if !bytes.Equal(matrix.TestedStackKitsVersion, []byte("null")) {
		var tested string
		if json.Unmarshal(matrix.TestedStackKitsVersion, &tested) != nil || !osMatrixRelease.MatchString(tested) {
			return bad()
		}
	}
	if err := validateOSMatrixPublicDigests(binding); err != nil {
		return err
	}
	seenPhases := map[string]bool{}
	for _, phase := range binding.RequiredPhases {
		if !validOSMatrixRuntimePhase(phase) || seenPhases[phase] {
			return bad()
		}
		seenPhases[phase] = true
	}
	seen, passed, results := map[string]bool{}, 0, map[string]int{}
	for _, observation := range matrix.RuntimeObservations {
		if observation.SchemaVersion != "stackkits.runtime-observation/v1" || len(observation.ID) != len("runtime_")+64 ||
			!strings.HasPrefix(observation.ID, "runtime_") || strings.Trim(strings.TrimPrefix(observation.ID, "runtime_"), "0123456789abcdef") != "" || seen[observation.ID] ||
			!osMatrixOneOf(observation.OriginalOutcome, "PASS", "FAIL", "PARTIAL", "NOT_RUN") || !validOSMatrixRuntime(observation.RuntimeIdentity) {
			return bad()
		}
		seen[observation.ID] = true
		if err := validateOSMatrixPublicDigests(observation); err != nil {
			return err
		}
		if observation.TestedRelease != "" && !osMatrixRelease.MatchString(observation.TestedRelease) {
			return bad()
		}
		if observation.ObservedAt != "" {
			if _, err := time.Parse(time.RFC3339, observation.ObservedAt); err != nil {
				return bad()
			}
		}
		if observation.Capacity != nil && (observation.Capacity.CPUs < 1 || observation.Capacity.MemoryMiB < 1 || observation.Capacity.DiskGiB < 1) {
			return bad()
		}
		if observation.SuiteBindingState != "" && !osMatrixOneOf(observation.SuiteBindingState, "source-only", "artifact-verified") {
			return bad()
		}
		if observation.SuiteBindingState == "artifact-verified" && (observation.SuiteBindingSHA256 == "" || observation.GitHubNativeArchiveSHA256 == "" ||
			observation.GitHubSuiteArchiveSHA256 == "" || observation.GitHubMetadataSHA256 == "" || observation.WindowReportSHA256 == "" ||
			observation.CandidateDigest == "" || observation.CandidateDigest != observation.SuiteCandidateDigest || observation.ConsumerSHA != observation.ProducerCommit) {
			return bad()
		}
		mandatory := []string{"owner-setup", "install", "enrollment", "connected", "independent-readback", "cleanup"}
		if observation.Subject.Kind == "StackKitUseCase" && observation.Subject.Product == "stackkits" && osMatrixOneOf(observation.Subject.UseCase, "files", "photos") &&
			osMatrixOneOf(observation.Subject.Platform, "", "coolify") {
			mandatory = []string{"install", "init", "generate", "apply", "setup-" + observation.Subject.UseCase, "verify", "backup", "restore", "cleanup"}
		} else if observation.Subject.Kind != "Product" || observation.Subject.Product != "guard" || observation.Subject.UseCase != "" || observation.Subject.Platform != "" {
			return bad()
		}
		if observation.OperationScope != "" {
			if observation.OperationScope != "retained-selective-files-removal" || observation.Subject.Kind != "StackKitUseCase" || observation.Subject.Product != "stackkits" || observation.Subject.UseCase != "files" || observation.Subject.Platform != "" || observation.SuiteBindingState != "artifact-verified" || len(observation.Operations) != 4 ||
				observation.RuntimeIdentity == nil || *observation.RuntimeIdentity != (osMatrixRuntimeIdentity{Family: "linux", Distribution: "ubuntu", Version: "24.04", Arch: "amd64", Environment: "retained-lxd-guest"}) {
				return bad()
			}
			mandatory = []string{"verify", "remove", "retain-data", "cleanup"}
		}
		if observation.RuntimeIdentity != nil && observation.RuntimeIdentity.Environment == "retained-lxd-guest" && observation.OperationScope == "" {
			return bad()
		}
		operations, failed := map[string]string{}, observation.OriginalOutcome == "FAIL"
		for _, operation := range observation.Operations {
			if !validOSMatrixRuntimePhase(operation.Name) || !osMatrixOneOf(operation.Status, "passed", "failed", "partial", "not-run") || operations[operation.Name] != "" {
				return bad()
			}
			if observation.OperationScope != "" && !osMatrixOneOf(operation.Name, "verify", "remove", "retain-data", "cleanup") ||
				observation.OperationScope == "" && osMatrixOneOf(operation.Name, "remove", "retain-data") {
				return bad()
			}
			operations[operation.Name] = operation.Status
			failed = failed || operation.Status == "failed"
		}
		allPassed := true
		for _, phase := range append(mandatory, binding.RequiredPhases...) {
			allPassed = allPassed && operations[phase] == "passed"
		}
		execution := "partial"
		if observation.OriginalOutcome == "NOT_RUN" {
			execution = "not-run"
		} else if failed {
			execution = "failed"
		} else if allPassed && observation.OriginalOutcome == "PASS" {
			execution = "passed"
		}
		if observation.ExecutionState != execution || !osMatrixOneOf(observation.Applicability, "current", "historical", "unbound", "stale") {
			return bad()
		}
		if observation.Applicability == "current" {
			if binding.OperationScope != observation.OperationScope || binding.Platform != observation.Subject.Platform || binding.RequestedRelease != observation.TestedRelease || binding.SourceCommit == "" || binding.ProducerCommit == "" || binding.ArtifactSHA256 == "" ||
				binding.RuntimeIdentity == nil || observation.RuntimeIdentity == nil || *binding.RuntimeIdentity != *observation.RuntimeIdentity {
				return bad()
			}
			actual := osMatrixBindingValues(observation.osMatrixRuntimeBindings)
			for key, expected := range osMatrixBindingValues(binding.osMatrixRuntimeBindings) {
				if expected != "" && actual[key] != expected {
					return bad()
				}
			}
			if observation.OperationScope == "retained-selective-files-removal" && (binding.CandidateDigest == "" || binding.IndexSHA256 == "" || binding.ConsumerSHA == "") {
				return bad()
			}
			if observation.OperationScope == "" && binding.Scope == "test-window-candidate" && (binding.IndexSHA256 == "" || binding.CatalogSHA256 == "" || binding.ScenarioDefinitionsDigest == "" || binding.CandidateDigest == "" || binding.ConsumerSHA == "") {
				return bad()
			}
			if observation.CurrentResult != map[string]string{"passed": "PASS", "failed": "FAIL", "partial": "PARTIAL", "not-run": "NOT_RUN"}[execution] {
				return bad()
			}
			if execution == "passed" {
				passed++
			}
		} else if observation.CurrentResult != "NOT_RUN" {
			return bad()
		}
		reasons := map[string]bool{}
		for _, code := range observation.ReasonCodes {
			if !osMatrixOneOf(code, "release-mismatch", "exact-bindings-required", "required-phases-incomplete", "reference-binding-mismatch") || reasons[code] {
				return bad()
			}
			reasons[code] = true
		}
		if observation.Applicability == "historical" && (!reasons["release-mismatch"] || observation.TestedRelease == "" || observation.TestedRelease == binding.RequestedRelease) ||
			observation.Applicability == "unbound" && !reasons["exact-bindings-required"] || observation.Applicability == "stale" && !reasons["reference-binding-mismatch"] ||
			!allPassed && !reasons["required-phases-incomplete"] {
			return bad()
		}
		results[observation.CurrentResult]++
	}
	current := "NOT_RUN"
	if results["FAIL"] > 0 {
		current = "FAIL"
	} else if len(matrix.RuntimeObservations) > 0 && results["PASS"] == len(matrix.RuntimeObservations) {
		current = "PASS"
	} else if results["PASS"]+results["PARTIAL"] > 0 {
		current = "PARTIAL"
	}
	if matrix.CurrentResult != current || *matrix.CurrentPassCount != passed {
		return bad()
	}
	return validateOSMatrixWindowCoverage(matrix.WindowCoverage)
}

func gradeGlyph(grade string) string {
	switch grade {
	case "supported":
		return "✅"
	case "preview":
		return "🟡"
	case "unsupported":
		return "❌"
	case "unverified":
		return "⚪"
	default:
		return "—"
	}
}

func osMatrixRowKey(row osMatrixDocRow) string {
	return strings.Join([]string{row.OS.Family, row.OS.Distribution, row.OS.Version}, "/")
}

func osMatrixReason(code string) string {
	if label, ok := osMatrixReasonLabels[code]; ok {
		return label
	}
	return code
}

func osMatrixEvidenceCells(evidence osMatrixEvidence) (string, string) {
	reasons := make([]string, 0, len(evidence.ReasonCodes))
	for _, code := range evidence.ReasonCodes {
		reasons = append(reasons, osMatrixReason(code))
	}
	note := strings.Join(reasons, "; ")
	if evidence.Grade == "supported" {
		note = "All lifecycle phases passed"
	}
	last := "—"
	if evidence.LastVerifiedRelease != "" {
		last = "`" + evidence.LastVerifiedRelease + "`"
	}
	return note, last
}

func renderOSMatrixMarkdown(matrix osMatrixDoc, sourceHash string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "<!-- Code generated by 'stackkit compat emit-os-matrix'. DO NOT EDIT. source_hash: %s -->\n\n", sourceHash)
	b.WriteString("# OS Compatibility\n\n")
	b.WriteString("Where the StackKits lifecycle was tested. Every row is projected from automated lifecycle receipts\n")
	b.WriteString("(install, init, generate, apply, verify, backup, restore) of one StackKits release, the evidence\n")
	b.WriteString("release. Runs land after a release ships, so the evidence release can be older than the newest\n")
	b.WriteString("release. Missing evidence is `unverified`, never an implied pass.\n\n")
	fmt.Fprintf(&b, "- Evidence release: `%s`\n- Last lifecycle run: %s\n\n", matrix.StackKitsVersion, matrix.GeneratedAt)
	b.WriteString("Grades:\n\n")
	for _, grade := range []string{"supported", "preview", "unverified"} {
		fmt.Fprintf(&b, "- **%s %s**: %s\n", gradeGlyph(grade), grade, osMatrixGradeMeaning[grade])
	}
	b.WriteString("\n" + osMatrixBestCellNote + "\n\n")

	b.WriteString("## Operating systems\n\n")
	b.WriteString("The lifecycle runs install each operating system fresh in a KVM/QEMU virtual machine.\n\n")
	b.WriteString("| OS family | Distribution | Version | Tested architecture | Grade | Evidence | Last verified |\n")
	b.WriteString("| --- | --- | --- | --- | :-: | --- | --- |\n")
	for _, row := range matrix.Results {
		note, last := osMatrixEvidenceCells(row.osMatrixEvidence)
		arch := "—"
		if len(row.Architectures) > 0 {
			arch = strings.Join(row.Architectures, ", ")
		}
		fmt.Fprintf(&b, "| %s | %s | `%s` | %s | **%s %s** | %s | %s |\n", row.OS.Family, row.OS.Distribution, row.OS.Version, arch, gradeGlyph(row.Grade), row.Grade, note, last)
	}

	if len(matrix.Virtualization) > 0 {
		b.WriteString("\n## Hypervisors\n\n")
		b.WriteString("StackKits run inside a guest VM on your hypervisor, never on the hypervisor host itself. The grade\n")
		b.WriteString("covers that rollout end to end: the guest is created on the hypervisor and the StackKit lifecycle\n")
		b.WriteString("runs inside it.\n\n")
		b.WriteString("| Hypervisor | How the guest is created | Grade | Evidence | Last verified |\n")
		b.WriteString("| --- | --- | :-: | --- | --- |\n")
		for _, row := range matrix.Virtualization {
			note, last := osMatrixEvidenceCells(row.osMatrixEvidence)
			fmt.Fprintf(&b, "| %s | %s | **%s %s** | %s | %s |\n", row.Name, row.Rollout, gradeGlyph(row.Grade), row.Grade, note, last)
		}
	}

	if len(matrix.Environments) > 0 {
		b.WriteString("\n## Kits by environment\n\n")
		b.WriteString("Each kit graded in the kind of environment it was run in. A home-network row also opens the kit's\n")
		b.WriteString("local service addresses from another device on that network.\n\n")
		b.WriteString("| Kit | Environment | Grade | Evidence | Last verified |\n")
		b.WriteString("| --- | --- | :-: | --- | --- |\n")
		for _, row := range matrix.Environments {
			note, last := osMatrixEvidenceCells(row.osMatrixEvidence)
			fmt.Fprintf(&b, "| `%s` | %s | **%s %s** | %s | %s |\n", row.Kit, row.Name, gradeGlyph(row.Grade), row.Grade, note, last)
		}
	}

	if len(matrix.Applications) > 0 {
		b.WriteString("\n## Applications\n\n")
		b.WriteString("Use cases installed and set up during the lifecycle run, with backup and restore of their data.\n\n")
		b.WriteString("| Use case | Adapter | Grade | Evidence | Last verified |\n")
		b.WriteString("| --- | --- | :-: | --- | --- |\n")
		for _, row := range matrix.Applications {
			note, last := osMatrixEvidenceCells(row.osMatrixEvidence)
			fmt.Fprintf(&b, "| `%s` | `%s` | **%s %s** | %s | %s |\n", row.UseCase, row.Adapter, gradeGlyph(row.Grade), row.Grade, note, last)
		}
	}

	if matrix.ReferenceBinding != nil {
		b.WriteString("\n## Runtime observations\n\n")
		fmt.Fprintf(&b, "Requested release: `%s`. Current bound result: **%s**; current passing observations: %d.\n\n", matrix.RequestedStackKitsVersion, matrix.CurrentResult, *matrix.CurrentPassCount)
		b.WriteString("Original runtime outcomes retain their tested release. A current result requires the exact artifact, source, producer and runtime binding. These observations do not change the OS grades above and do not establish bare-metal or modular lifecycle support.\n\n")
		b.WriteString("| Subject | Tested release | Original outcome | Applicability | Current result |\n")
		b.WriteString("| --- | --- | --- | --- | --- |\n")
		for _, observation := range matrix.RuntimeObservations {
			subject := observation.Subject.Product
			if observation.Subject.UseCase != "" {
				subject += "/" + observation.Subject.UseCase
			}
			if observation.Subject.Platform != "" {
				subject += " via " + observation.Subject.Platform
			}
			if observation.OperationScope != "" {
				subject += " (retained selective removal)"
			}
			tested := observation.TestedRelease
			if tested == "" {
				tested = "unbound"
			}
			fmt.Fprintf(&b, "| `%s` | `%s` | %s | %s | %s |\n", subject, tested, observation.OriginalOutcome, observation.Applicability, observation.CurrentResult)
		}
		if coverage := matrix.WindowCoverage; coverage != nil {
			notProjected := ""
			if coverage.NotProjected != nil {
				notProjected = fmt.Sprintf(", %d not projected (no StackKits coverage codec)", *coverage.NotProjected)
			}
			fmt.Fprintf(&b, "\nOriginal window coverage: %d declared, %d passed, %d pending, %d failed, %d blocked, %d not run%s. Window result: `%s`. This is the original window's coverage, not a new execution or a current-release admission.\n", coverage.Declared, coverage.Passed, coverage.Pending, coverage.Failed, coverage.Blocked, coverage.NotRun, notProjected, coverage.Result)
		}
	}
	b.WriteString("\n## Authority boundary\n\n")
	b.WriteString("The only writer of this data is the receipt projector (`scripts/compat/project-os-compat.mjs`) through\n")
	b.WriteString("an auto-PR. Rows never name a server provider, and host diagnostics never create a claim.\n\n")
	b.WriteString("Run `stackkit compat` on a target for non-destructive host diagnostics and the published evidence\n")
	b.WriteString("for its OS and hypervisor.\n")
	return b.String()
}

type osMatrixHistoryEntry struct {
	Version     string            `json:"version"`
	GeneratedAt string            `json:"generatedAt"`
	Grades      map[string]string `json:"grades"`
}

func renderOSMatrixFeed(latest osMatrixDoc, archiveDir string) ([]byte, error) {
	history := []osMatrixHistoryEntry{}
	entries, err := os.ReadDir(archiveDir)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("read archive dir: %w", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, "v") || !strings.HasSuffix(name, ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(archiveDir, name))
		if err != nil {
			return nil, fmt.Errorf("read archive %s: %w", name, err)
		}
		matrix, err := decodeOSMatrix(raw)
		if err != nil {
			return nil, fmt.Errorf("archive %s: %w", name, err)
		}
		grades := map[string]string{}
		for _, row := range matrix.Results {
			grades[osMatrixRowKey(row)] = row.Grade
		}
		history = append(history, osMatrixHistoryEntry{Version: matrix.StackKitsVersion, GeneratedAt: matrix.GeneratedAt, Grades: grades})
	}
	sort.Slice(history, func(i, j int) bool { return history[i].Version > history[j].Version })
	feed := struct {
		SchemaVersion int                    `json:"schemaVersion"`
		Latest        osMatrixDoc            `json:"latest"`
		History       []osMatrixHistoryEntry `json:"history"`
	}{SchemaVersion: 3, Latest: latest, History: history}
	out, err := json.MarshalIndent(feed, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

func writeGenerated(path string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, content, 0o644)
}
