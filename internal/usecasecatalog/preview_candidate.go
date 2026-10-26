package usecasecatalog

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/kombifyio/stackkits/internal/releaseindex"
	"golang.org/x/mod/semver"
)

const PreviewCatalogSchema = "stackkits.preview-use-case-catalog/v1"
const PreviewCatalogFile = "stackkits-preview-use-case-catalog-v1.json"

// PreviewCatalog contains generated facts, not a public release or execution
// grant. The existing producer attests its exact bytes; consumers authenticate
// those bytes and the source/run against their independent candidate admission.
type PreviewCatalog struct {
	SchemaVersion   string                                `json:"schemaVersion"`
	DeclaredVersion string                                `json:"declaredVersion"`
	Source          releaseindex.PreviewCandidateSource   `json:"source"`
	Producer        releaseindex.PreviewCandidateProducer `json:"producer"`
	GeneratedAt     string                                `json:"generatedAt"`
	Catalog         CatalogContent                        `json:"catalog"`
	ContentDigest   string                                `json:"contentDigest"`
}

func GeneratePreviewCatalog(sourceRoot, catalogRoot, declaredVersion string, source releaseindex.PreviewCandidateSource, producer releaseindex.PreviewCandidateProducer) (PreviewCatalog, error) {
	var output PreviewCatalog
	if !semver.IsValid("v"+declaredVersion) || semver.Canonical("v"+declaredVersion) != "v"+declaredVersion || semver.Prerelease("v"+declaredVersion) != "" ||
		source.Repository == "" || producer.Workflow != ".github/workflows/release.yml" || producer.WorkflowSHA != source.Commit || producer.Event != "workflow_dispatch" ||
		producer.WorkflowRef != source.Repository+"/"+producer.Workflow+"@refs/heads/main" {
		return output, fmt.Errorf("candidate catalog needs canonical source/run metadata, not a release tag")
	}
	for _, id := range []string{producer.RunID, producer.RunAttempt} {
		value, err := strconv.ParseUint(id, 10, 64)
		if err != nil || value == 0 || strconv.FormatUint(value, 10) != id {
			return output, fmt.Errorf("candidate catalog run identity is invalid")
		}
	}
	git := func(root string, args ...string) (string, error) {
		command := exec.Command("git", append([]string{"-C", root}, args...)...)
		raw, err := command.Output()
		return strings.TrimSpace(string(raw)), err
	}
	for _, item := range []struct {
		root, selector, expected string
	}{
		{sourceRoot, "HEAD", source.Commit},
		{sourceRoot, "HEAD^{tree}", source.Tree},
		{catalogRoot, "HEAD^{tree}", source.ExportTree},
	} {
		actual, err := git(item.root, "rev-parse", item.selector)
		if err != nil || len(actual) != 40 || actual != item.expected {
			return output, fmt.Errorf("candidate catalog source differs from its actual source/export tree")
		}
	}
	if status, err := git(catalogRoot, "status", "--porcelain", "--untracked-files=normal"); err != nil || status != "" {
		return output, fmt.Errorf("candidate catalog export tree changed before projection")
	}
	// Only SourceSHA scopes existing evidence loading. An empty release tag
	// deliberately cannot promote a historical public compatibility claim.
	facts, err := loadSource(catalogRoot, ReleaseIdentity{SourceSHA: source.Commit})
	if err != nil {
		return output, err
	}
	if err := attachAuthoringVocabulary(&facts, facts.catalogWorkloads, facts.catalogModules); err != nil {
		return output, err
	}
	generatedAt, err := generatedTime(sourceRoot, source.Commit)
	if err != nil {
		return output, err
	}
	output = PreviewCatalog{SchemaVersion: PreviewCatalogSchema, DeclaredVersion: declaredVersion, Source: source, Producer: producer, GeneratedAt: generatedAt, Catalog: catalogContent(facts)}
	output.ContentDigest, err = contentDigest(output)
	return output, err
}
