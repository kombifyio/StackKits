package releaseindex

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"reflect"
	"regexp"
	"strings"

	"github.com/kombifyio/stackkits/internal/productkits"
	"github.com/sigstore/sigstore-go/pkg/fulcio/certificate"
)

const (
	PreviewCandidateProvenanceSchema       = "stackkits.preview-candidate-provenance/v1"
	PreviewCandidateIndexSchema            = "stackkits.preview-candidate-index/v1"
	PreviewCandidateIndexName              = "stackkits-preview-candidate-index-v1.json"
	PreviewCandidatePayloadAttestationName = "payload.intoto.jsonl"
	previewCandidateRepository             = "kombify/kombify-StackKits"
	previewCandidateWorkflow               = ".github/workflows/release.yml"
	previewCandidateWorkflowRef            = previewCandidateRepository + "/" + previewCandidateWorkflow + "@refs/heads/main"
)

type PreviewCandidateSource struct {
	Repository string `json:"repository"`
	Commit     string `json:"commit"`
	Tree       string `json:"tree"`
	ExportTree string `json:"exportTree"`
}

type PreviewCandidateProducer struct {
	Workflow    string `json:"workflow"`
	WorkflowRef string `json:"workflowRef"`
	WorkflowSHA string `json:"workflowSha"`
	RunID       string `json:"runId"`
	RunAttempt  string `json:"runAttempt"`
	Event       string `json:"event"`
}

type PreviewCandidateArtifact struct {
	ID     string `json:"id"`
	SHA256 string `json:"sha256"`
}

// PreviewCandidateProvenance is the frozen cross-product identity. It carries
// no installation permission. Its artifact identity must come from separately
// authenticated GitHub metadata retained by the signed Stage admission.
type PreviewCandidateProvenance struct {
	SchemaVersion            string                   `json:"schemaVersion"`
	IndexSHA256              string                   `json:"indexSha256"`
	IndexAttestationSHA256   string                   `json:"indexAttestationSha256"`
	PayloadAttestationSHA256 string                   `json:"payloadAttestationSha256"`
	TrustedRootSHA256        string                   `json:"trustedRootSha256"`
	Artifact                 PreviewCandidateArtifact `json:"artifact"`
	Source                   PreviewCandidateSource   `json:"source"`
	Producer                 PreviewCandidateProducer `json:"producer"`
}

type PreviewCandidateBlob struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
}

// CandidateBuildProfile discloses the compiler variant, independently of the
// runtime data profile. Historical candidates without this disclosure are Standard.
type CandidateBuildProfile string

const (
	CandidateStandard  CandidateBuildProfile = "standard"
	CandidateFullLocal CandidateBuildProfile = "full-local"
	FullLocalBuildTag                        = "kombify_full_local"
)

func candidateBuildProfile(profile CandidateBuildProfile, tags []string) (CandidateBuildProfile, error) {
	if profile == "" && tags == nil {
		return CandidateStandard, nil
	}
	if profile == CandidateStandard && len(tags) == 0 {
		return profile, nil
	}
	if profile == CandidateFullLocal && reflect.DeepEqual(tags, []string{FullLocalBuildTag}) {
		return profile, nil
	}
	return "", errors.New("candidate build profile and canonical compiler tags disagree")
}

type previewCandidateIndex struct {
	SchemaVersion   string                   `json:"schemaVersion"`
	Channel         string                   `json:"channel"`
	InternalOnly    bool                     `json:"internalOnly"`
	DeclaredVersion string                   `json:"declaredVersion"`
	BuildProfile    CandidateBuildProfile    `json:"buildProfile,omitempty"`
	BuildTags       []string                 `json:"buildTags,omitempty"`
	Source          PreviewCandidateSource   `json:"source"`
	Producer        PreviewCandidateProducer `json:"producer"`
	Trust           struct {
		Issuer              string               `json:"issuer"`
		PredicateType       string               `json:"predicateType"`
		CertificateIdentity string               `json:"certificateIdentity"`
		PayloadAttestation  PreviewCandidateBlob `json:"payloadAttestation"`
		TrustedRoots        PreviewCandidateBlob `json:"trustedRoots"`
	} `json:"trust"`
	Assets      []PreviewCandidateBlob `json:"assets"`
	Publication map[string]*bool       `json:"publication"`
}

func (provenance PreviewCandidateProvenance) Validate() error {
	commit := regexp.MustCompile(`^[0-9a-f]{40}$`)
	positive := regexp.MustCompile(`^[1-9][0-9]*$`)
	if provenance.SchemaVersion != PreviewCandidateProvenanceSchema || provenance.Source.Repository != previewCandidateRepository || !commit.MatchString(provenance.Source.Commit) || !commit.MatchString(provenance.Source.Tree) || !commit.MatchString(provenance.Source.ExportTree) || provenance.Producer.Workflow != previewCandidateWorkflow || provenance.Producer.WorkflowRef != previewCandidateWorkflowRef || provenance.Producer.WorkflowSHA != provenance.Source.Commit || provenance.Producer.Event != "workflow_dispatch" || !positive.MatchString(provenance.Producer.RunID) || !positive.MatchString(provenance.Producer.RunAttempt) || !positive.MatchString(provenance.Artifact.ID) {
		return errors.New("candidate provenance must identify the exact private main workflow dispatch, source and artifact")
	}
	for _, digest := range []string{provenance.IndexSHA256, provenance.IndexAttestationSHA256, provenance.PayloadAttestationSHA256, provenance.TrustedRootSHA256, provenance.Artifact.SHA256} {
		if !sha256Pattern.MatchString(digest) {
			return errors.New("candidate provenance has an invalid exact SHA256")
		}
	}
	return nil
}

// PreviewCandidateInput requires the exact index and selected payload bytes.
// Expected is selected only from the authenticated installed Stage admission,
// never from the downloaded index or an arbitrary caller-supplied receipt.
type PreviewCandidateInput struct {
	Expected                                                 PreviewCandidateProvenance
	Kit                                                      string
	Platform                                                 Platform
	ArchiveSHA256                                            string
	Index, IndexAttestation, PayloadAttestation, TrustedRoot []byte
	Archive, SBOM                                            []byte
}

type verifiedPreviewCandidateToken struct{}

// VerifiedPreviewCandidate proves only the offline private build provenance.
// It is deliberately unrelated to VerifiedInstallation and the public Receipt.
// Stage installation still needs authenticated assignment/Guard custody.
// The imagepreparation owner may separately admit a fresh neutral bundle only
// after full profile, process, five-tool, provider, image and model verification.
type VerifiedPreviewCandidate struct {
	token                            *verifiedPreviewCandidateToken
	input                            PreviewCandidateInput
	version, archiveName, sbomSHA256 string
	buildProfile                     CandidateBuildProfile
	buildTags                        []string
}

// AttestedPreviewCandidateIdentity deliberately excludes the caller-admitted
// artifact metadata, which the offline signature cannot authenticate.
type AttestedPreviewCandidateIdentity struct {
	Source                   PreviewCandidateSource   `json:"source"`
	Producer                 PreviewCandidateProducer `json:"producer"`
	IndexSHA256              string                   `json:"indexSha256"`
	ArchiveSHA256            string                   `json:"archiveSha256"`
	IndexAttestationSHA256   string                   `json:"indexAttestationSha256,omitempty"`
	PayloadAttestationSHA256 string                   `json:"payloadAttestationSha256,omitempty"`
	TrustedRootSHA256        string                   `json:"trustedRootSha256,omitempty"`
	SBOMSHA256               string                   `json:"sbomSha256,omitempty"`
	BuildProfile             CandidateBuildProfile    `json:"buildProfile,omitempty"`
	BuildTags                []string                 `json:"buildTags,omitempty"`
}

func (candidate VerifiedPreviewCandidate) Inspect(inspect func(AttestedPreviewCandidateIdentity, string, string, Platform, io.Reader) error) error {
	if candidate.token == nil || inspect == nil || digestBytes(candidate.input.Archive) != candidate.input.ArchiveSHA256 {
		return errors.New("valid immutable verified Preview candidate proof is required")
	}
	expected := candidate.input.Expected
	return inspect(AttestedPreviewCandidateIdentity{
		Source: expected.Source, Producer: expected.Producer,
		IndexSHA256: expected.IndexSHA256, ArchiveSHA256: candidate.input.ArchiveSHA256,
		IndexAttestationSHA256: expected.IndexAttestationSHA256, PayloadAttestationSHA256: expected.PayloadAttestationSHA256,
		TrustedRootSHA256: expected.TrustedRootSHA256, SBOMSHA256: candidate.sbomSHA256,
		BuildProfile: candidate.buildProfile, BuildTags: append([]string(nil), candidate.buildTags...),
	}, candidate.input.Kit, candidate.version, candidate.input.Platform, bytes.NewReader(candidate.input.Archive))
}

// VerifyPreviewCandidate preserves the public tag trust policy. Candidate
// identity is explicit and adds certificate-bound source, signer, hosted runner
// and exact run invocation requirements to the same pinned-root verifier.
func (verifier SigstoreVerifier) VerifyPreviewCandidate(ctx context.Context, input PreviewCandidateInput) (VerifiedPreviewCandidate, error) {
	if err := ctx.Err(); err != nil {
		return VerifiedPreviewCandidate{}, err
	}
	if input.Kit != strings.TrimSpace(input.Kit) {
		return VerifiedPreviewCandidate{}, errors.New("candidate kit must be canonical")
	}
	if err := input.Expected.Validate(); err != nil {
		return VerifiedPreviewCandidate{}, err
	}
	if err := productkits.Validate(input.Kit); err != nil {
		return VerifiedPreviewCandidate{}, err
	}
	if (input.Platform.OS != "linux" && input.Platform.OS != "windows" && input.Platform.OS != "darwin") || (input.Platform.Arch != "amd64" && input.Platform.Arch != "arm64") || !sha256Pattern.MatchString(input.ArchiveSHA256) {
		return VerifiedPreviewCandidate{}, errors.New("candidate kit/platform/archive identity is invalid")
	}
	for _, blob := range []struct {
		data   []byte
		digest string
		limit  int
	}{
		{input.Index, input.Expected.IndexSHA256, 1 << 20}, {input.IndexAttestation, input.Expected.IndexAttestationSHA256, 32 << 20}, {input.PayloadAttestation, input.Expected.PayloadAttestationSHA256, 64 << 20}, {input.TrustedRoot, input.Expected.TrustedRootSHA256, 16 << 20}, {input.Archive, input.ArchiveSHA256, 256 << 20},
	} {
		if len(blob.data) == 0 || len(blob.data) > blob.limit || digestBytes(blob.data) != blob.digest {
			return VerifiedPreviewCandidate{}, errors.New("candidate retained bytes exceed bounds or differ from the admitted digest")
		}
	}
	trust := mustLoadReleaseTrustPolicy().PreviewCandidate
	if trust == nil || trust.SchemaVersion != "stackkit.preview-candidate-trust/v1" || trust.VerificationMode != "github-rfc3161" || trust.SourceRepositoryIdentifier != "1129956899" || trust.SourceRepositoryOwnerIdentifier != "249091777" || trust.SourceRepositoryVisibilityAtSigning != "internal" || len(trust.SigstoreTrustedRootDocumentSHA256) == 0 {
		return VerifiedPreviewCandidate{}, errors.New("explicit reviewed internal candidate trust policy required")
	}
	roots, err := selectTrustedRootDocuments(input.TrustedRoot, trust.SigstoreTrustedRootDocumentSHA256)
	if err != nil {
		return VerifiedPreviewCandidate{}, err
	}
	verifySubject := func(data, attestation []byte, name string) error {
		digest := digestBytes(data)
		decoded, _ := hex.DecodeString(digest)
		return verifyBundle(ctx, roots, attestation, decoded, GitHubOIDCIssuer, "https://github.com/"+previewCandidateWorkflowRef, name, GitHubAttestationPredicate, digest, "internal Preview candidate "+name, bundleVerificationPolicy{mode: trust.VerificationMode, extensions: certificate.Extensions{
			BuildSignerURI: "https://github.com/" + previewCandidateWorkflowRef, BuildSignerDigest: input.Expected.Source.Commit, RunnerEnvironment: "github-hosted", SourceRepositoryURI: "https://github.com/" + previewCandidateRepository, SourceRepositoryDigest: input.Expected.Source.Commit, SourceRepositoryRef: "refs/heads/main", BuildConfigURI: "https://github.com/" + previewCandidateWorkflowRef, BuildConfigDigest: input.Expected.Source.Commit, BuildTrigger: "workflow_dispatch", RunInvocationURI: "https://github.com/" + previewCandidateRepository + "/actions/runs/" + input.Expected.Producer.RunID + "/attempts/" + input.Expected.Producer.RunAttempt, SourceRepositoryVisibilityAtSigning: trust.SourceRepositoryVisibilityAtSigning, SourceRepositoryIdentifier: trust.SourceRepositoryIdentifier, SourceRepositoryOwnerIdentifier: trust.SourceRepositoryOwnerIdentifier,
		}})
	}
	if err := verifySubject(input.Index, input.IndexAttestation, PreviewCandidateIndexName); err != nil {
		return VerifiedPreviewCandidate{}, err
	}
	var index previewCandidateIndex
	if err := decodePreviewCandidateJSON(input.Index, &index); err != nil {
		return VerifiedPreviewCandidate{}, err
	}
	if index.SchemaVersion != PreviewCandidateIndexSchema || index.Channel != "preview" || !index.InternalOnly || index.DeclaredVersion != "0.54.8" || index.Source != input.Expected.Source || index.Producer != input.Expected.Producer || index.Trust.Issuer != GitHubOIDCIssuer || index.Trust.PredicateType != GitHubAttestationPredicate || index.Trust.CertificateIdentity != "https://github.com/"+previewCandidateWorkflowRef || index.Trust.PayloadAttestation != (PreviewCandidateBlob{PreviewCandidatePayloadAttestationName, input.Expected.PayloadAttestationSHA256}) || index.Trust.TrustedRoots != (PreviewCandidateBlob{TrustedRootAssetName, input.Expected.TrustedRootSHA256}) {
		return VerifiedPreviewCandidate{}, errors.New("candidate index differs from the exact private Preview contract and admitted source")
	}
	buildProfile, err := candidateBuildProfile(index.BuildProfile, index.BuildTags)
	if err != nil {
		return VerifiedPreviewCandidate{}, err
	}
	flags := []string{"githubRelease", "sourceMirror", "tag", "delivery", "stable", "earlyAccess"}
	if len(index.Publication) != len(flags) {
		return VerifiedPreviewCandidate{}, errors.New("candidate index must declare every publication flag false")
	}
	for _, flag := range flags {
		value, exists := index.Publication[flag]
		if !exists || value == nil || *value {
			return VerifiedPreviewCandidate{}, errors.New("candidate publication is forbidden")
		}
	}
	extension := ".tar.gz"
	if input.Platform.OS == "windows" {
		extension = ".zip"
	}
	archiveName := "stackkits-" + input.Kit + "_" + index.DeclaredVersion + "_" + input.Platform.OS + "_" + input.Platform.Arch + extension
	assets := map[string]string{}
	assetNamePattern := regexp.MustCompile(`^stackkits(?:-(?:basement-kit|cloud-kit|modern-homelab))?_0\.54\.8_(?:linux|darwin|windows)_(?:amd64|arm64)\.(?:tar\.gz|zip)(?:\.spdx\.json)?$`)
	if len(index.Assets) < 2 || len(index.Assets) > 128 {
		return VerifiedPreviewCandidate{}, errors.New("candidate asset count exceeds the fixed producer envelope")
	}
	for _, asset := range index.Assets {
		if (asset.Name != "checksums.txt" && asset.Name != "stackkits-preview-use-case-catalog-v1.json" && !assetNamePattern.MatchString(asset.Name)) || path.Base(asset.Name) != asset.Name || strings.ContainsAny(asset.Name, "\\\x00") || !sha256Pattern.MatchString(asset.SHA256) || assets[asset.Name] != "" {
			return VerifiedPreviewCandidate{}, errors.New("candidate index has unsafe or duplicate asset identities")
		}
		assets[asset.Name] = asset.SHA256
	}
	if assets[archiveName] != input.ArchiveSHA256 || len(input.SBOM) == 0 || len(input.SBOM) > 32<<20 || assets[archiveName+".spdx.json"] != digestBytes(input.SBOM) {
		return VerifiedPreviewCandidate{}, errors.New("candidate selected archive/SBOM differs from its verified index")
	}
	if err := verifySubject(input.Archive, input.PayloadAttestation, archiveName); err != nil {
		return VerifiedPreviewCandidate{}, err
	}
	if err := verifySubject(input.SBOM, input.PayloadAttestation, archiveName+".spdx.json"); err != nil {
		return VerifiedPreviewCandidate{}, err
	}
	input.Index = bytes.Clone(input.Index)
	input.IndexAttestation = bytes.Clone(input.IndexAttestation)
	input.PayloadAttestation = bytes.Clone(input.PayloadAttestation)
	input.TrustedRoot = bytes.Clone(input.TrustedRoot)
	input.Archive = bytes.Clone(input.Archive)
	input.SBOM = bytes.Clone(input.SBOM)
	return VerifiedPreviewCandidate{token: &verifiedPreviewCandidateToken{}, input: input, version: index.DeclaredVersion, archiveName: archiveName, sbomSHA256: digestBytes(input.SBOM), buildProfile: buildProfile, buildTags: append([]string(nil), index.BuildTags...)}, nil
}

func decodePreviewCandidateJSON(raw []byte, target any) error {
	// First reject duplicate members at every depth; encoding/json otherwise
	// silently accepts the last occurrence, including in signed documents.
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var walk func() error
	walk = func() error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		if delim, ok := token.(json.Delim); ok {
			switch delim {
			case '{':
				seen := map[string]bool{}
				for decoder.More() {
					key, err := decoder.Token()
					if err != nil {
						return err
					}
					name, ok := key.(string)
					if !ok || seen[strings.ToLower(name)] {
						return errors.New("candidate JSON contains duplicate object members")
					}
					seen[strings.ToLower(name)] = true
					if err := walk(); err != nil {
						return err
					}
				}
			case '[':
				for decoder.More() {
					if err := walk(); err != nil {
						return err
					}
				}
			default:
				return errors.New("unexpected candidate JSON delimiter")
			}
			_, err = decoder.Token()
			return err
		}
		return nil
	}
	if err := walk(); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return errors.New("candidate JSON has trailing data")
	}
	var shape any
	if err := json.Unmarshal(raw, &shape); err != nil {
		return err
	}
	if err := requireExactCandidateKeys(shape, reflect.TypeOf(target)); err != nil {
		return err
	}
	decoder = json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode private candidate: %w", err)
	}
	return nil
}

// DecodePreviewCandidateProvenance is the strict JSON boundary for installed
// signed admissions. It authenticates no metadata by itself.
func DecodePreviewCandidateProvenance(raw []byte) (PreviewCandidateProvenance, error) {
	var p PreviewCandidateProvenance
	if len(raw) == 0 || len(raw) > 1<<20 {
		return p, errors.New("candidate provenance exceeds bounds")
	}
	if err := decodePreviewCandidateJSON(raw, &p); err != nil {
		return p, err
	}
	return p, p.Validate()
}

func requireExactCandidateKeys(value any, t reflect.Type) error {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Struct:
		object, ok := value.(map[string]any)
		if !ok {
			return nil
		}
		fields := map[string]reflect.Type{}
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			name := strings.Split(field.Tag.Get("json"), ",")[0]
			if name != "" && name != "-" {
				fields[name] = field.Type
			}
		}
		for name, child := range object {
			field, ok := fields[name]
			if !ok {
				return fmt.Errorf("candidate JSON key %q is not an exact schema member", name)
			}
			if err := requireExactCandidateKeys(child, field); err != nil {
				return err
			}
		}
	case reflect.Map:
		if object, ok := value.(map[string]any); ok {
			for _, child := range object {
				if err := requireExactCandidateKeys(child, t.Elem()); err != nil {
					return err
				}
			}
		}
	case reflect.Slice, reflect.Array:
		if array, ok := value.([]any); ok {
			for _, child := range array {
				if err := requireExactCandidateKeys(child, t.Elem()); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
