package architecturev2

import (
	"context"
	"errors"
	"fmt"
	"runtime"

	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/releaseindex"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

const ApplyProgramAPIVersion = "stackkit.apply-program/v1"

// ApplyProgramIdentity is an observation in the existing signed Apply result.
// An absent value on historical results means executable identity is unproven.
type ApplyProgramIdentity struct {
	SchemaVersion    string                     `json:"schemaVersion"`
	ProcessRole      string                     `json:"processRole"`
	ExecutableSHA256 string                     `json:"executableSha256"`
	PreviewCandidate *ApplyPreviewProgramSource `json:"previewCandidate,omitempty"`
}

type ApplyPreviewProgramSource struct {
	Kit                      string                                `json:"kit"`
	Version                  string                                `json:"version"`
	Platform                 releaseindex.Platform                 `json:"platform"`
	IndexSHA256              string                                `json:"indexSha256"`
	ArchiveSHA256            string                                `json:"archiveSha256"`
	Source                   releaseindex.PreviewCandidateSource   `json:"source"`
	Producer                 releaseindex.PreviewCandidateProducer `json:"producer"`
	IndexAttestationSHA256   string                                `json:"indexAttestationSha256,omitempty"`
	PayloadAttestationSHA256 string                                `json:"payloadAttestationSha256,omitempty"`
	TrustedRootSHA256        string                                `json:"trustedRootSha256,omitempty"`
	SBOMSHA256               string                                `json:"sbomSha256,omitempty"`
	BuildProfile             releaseindex.CandidateBuildProfile    `json:"buildProfile,omitempty"`
	BuildTags                []string                              `json:"buildTags,omitempty"`
}

// ProductApplyProgramSourceVerifier is fixed by trusted CLI composition. Its
// implementation must reverify the opaque signed candidate and use the shared
// archive parser to prove that its CLI bytes equal observedExecutableSHA256.
// A selector, YAML value, version, cache entry or caller flag is not proof.
type ProductApplyProgramSourceVerifier interface {
	VerifyApplyProgramSource(ctx context.Context, observedExecutableSHA256 string) (*ApplyPreviewProgramSource, error)
}

func (s *Service) BindProductApplyProgram(processRole string, verifier ProductApplyProgramSourceVerifier) error {
	if s == nil || s.productApplyProcessRole != "" || (processRole != "stackkit-cli" && processRole != "embedded-runtime") {
		return errors.New("Apply program observation requires one fixed supported process role at service composition")
	}
	s.productApplyProcessRole = processRole
	s.productApplyProgramSource = verifier
	return nil
}

func (s *Service) observeApplyProgram(ctx context.Context) (program *ApplyProgramIdentity, returnErr error) {
	if s == nil || s.productApplyProcessRole == "" {
		return nil, nil
	}
	defer func() {
		if recover() != nil {
			program = nil
			returnErr = errors.New("service-owned Apply program source verifier panicked")
		}
	}()
	digest, err := localevidence.CurrentExecutableSHA256()
	if err != nil {
		return nil, err
	}
	program = &ApplyProgramIdentity{SchemaVersion: ApplyProgramAPIVersion, ProcessRole: s.productApplyProcessRole, ExecutableSHA256: digest}
	if s.productApplyProgramSource != nil {
		program.PreviewCandidate, err = s.productApplyProgramSource.VerifyApplyProgramSource(ctx, digest)
		if err != nil {
			return nil, fmt.Errorf("verify current Apply program source: %w", err)
		}
	}
	program = cloneApplyProgram(program)
	if err := validateApplyProgram(program); err != nil {
		return nil, err
	}
	if program.PreviewCandidate != nil && (program.PreviewCandidate.Platform.OS != runtime.GOOS || program.PreviewCandidate.Platform.Arch != runtime.GOARCH) {
		return nil, errors.New("Apply program source platform differs from the running process")
	}
	return cloneApplyProgram(program), nil
}

func validateApplyProgram(program *ApplyProgramIdentity) error {
	if program == nil {
		return nil
	}
	if program.SchemaVersion != ApplyProgramAPIVersion || (program.ProcessRole != "stackkit-cli" && program.ProcessRole != "embedded-runtime") || !validApplySHA256(program.ExecutableSHA256) {
		return errors.New("Apply program identity is incomplete or noncanonical")
	}
	if candidate := program.PreviewCandidate; candidate != nil {
		for _, digest := range []string{candidate.IndexAttestationSHA256, candidate.PayloadAttestationSHA256, candidate.TrustedRootSHA256, candidate.SBOMSHA256} {
			if digest != "" && !validApplySHA256("sha256:"+digest) {
				return errors.New("Apply Preview program attestation identity is invalid")
			}
		}
		if candidate.BuildProfile != "" && candidate.BuildProfile != releaseindex.CandidateStandard && candidate.BuildProfile != releaseindex.CandidateFullLocal {
			return errors.New("Apply Preview program build profile is invalid")
		}
		if candidate.BuildProfile == releaseindex.CandidateFullLocal && (len(candidate.BuildTags) != 1 || candidate.BuildTags[0] != releaseindex.FullLocalBuildTag) {
			return errors.New("Apply Preview Full Local program compiler tags differ")
		}
		if candidate.Kit == "" || candidate.Version == "" || candidate.Platform.OS == "" || candidate.Platform.Arch == "" || !validApplySHA256("sha256:"+candidate.IndexSHA256) || !validApplySHA256("sha256:"+candidate.ArchiveSHA256) {
			return errors.New("Apply Preview program source needs exact kit/platform/index/archive identity")
		}
		// Shape validation is not offline signature or archive membership proof.
		// The fixed source verifier supplies those checks before this observation;
		// external Artifact IDs are deliberately absent from the signed identity.
		if candidate.Source.Repository == "" || len(candidate.Source.Commit) != 40 || len(candidate.Source.Tree) != 40 || len(candidate.Source.ExportTree) != 40 || candidate.Producer.Workflow == "" || candidate.Producer.WorkflowRef == "" || candidate.Producer.WorkflowSHA != candidate.Source.Commit || candidate.Producer.Event != "workflow_dispatch" || candidate.Producer.RunID == "" || candidate.Producer.RunAttempt == "" {
			return errors.New("Apply Preview program source/producer identity is incomplete")
		}
	}
	return nil
}

func cloneApplyProgram(program *ApplyProgramIdentity) *ApplyProgramIdentity {
	if program == nil {
		return nil
	}
	value := *program
	if program.PreviewCandidate != nil {
		source := *program.PreviewCandidate
		source.BuildTags = append([]string(nil), source.BuildTags...)
		value.PreviewCandidate = &source
	}
	return &value
}

func withObservedApplyProgram(result VerifiedApplyResult, program *ApplyProgramIdentity) (VerifiedApplyResult, error) {
	if program == nil {
		return result, nil
	}
	if err := validateApplyProgram(program); err != nil {
		return VerifiedApplyResult{}, err
	}
	result.envelope.Program = cloneApplyProgram(program)
	hash, err := resolvedplan.CanonicalSHA256(result.envelope)
	if err != nil {
		return VerifiedApplyResult{}, err
	}
	result.resultHash = hash
	return result, nil
}
