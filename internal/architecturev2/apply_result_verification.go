package architecturev2

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

// ProductApplyResultVerificationInput is the complete read-only authority for
// accepting a persisted Apply result. Runtime owner identity and producer
// trust remain service-owned.
type ProductApplyResultVerificationInput struct {
	Plan     generationartifact.VerifiedPlan
	Manifest generationartifact.ArtifactManifest
	Receipt  generationartifact.GenerationReceipt
	Versions generationartifact.ComponentVersions
	Result   []byte
}

// ApplyResultSummary is the secret-free public projection consumed by CLI
// verification output.
type ApplyResultSummary struct {
	ResultHash         string    `json:"resultHash"`
	AppliedAt          time.Time `json:"appliedAt"`
	EvidenceBundleHash string    `json:"evidenceBundleHash"`
	RuntimeCount       int       `json:"runtimeCount"`
	HealthCount        int       `json:"healthCount"`
}

// ApplyRuntimeObservationSummary is the secret-free, immutable runtime
// evidence projection used by CLI/MCP observation surfaces.
type ApplyRuntimeObservationSummary struct {
	RequirementID     string `json:"requirementId"`
	InstanceRef       string `json:"instanceRef"`
	Status            string `json:"status"`
	ObservationRef    string `json:"observationRef"`
	ObservationDigest string `json:"observationDigest"`
}

// ApplyHealthObservationSummary is the secret-free health evidence projection
// used by CLI/MCP observation surfaces.
type ApplyHealthObservationSummary struct {
	RequirementID     string `json:"requirementId"`
	TargetRef         string `json:"targetRef"`
	Status            string `json:"status"`
	ObservationRef    string `json:"observationRef"`
	ObservationDigest string `json:"observationDigest"`
}

type ApplyObservationSummary struct {
	Runtime []ApplyRuntimeObservationSummary `json:"runtime"`
	Health  []ApplyHealthObservationSummary  `json:"health"`
}

// Summary returns a defensive public projection.
func (r VerifiedApplyResult) Summary() ApplyResultSummary {
	appliedAt, _ := time.Parse(time.RFC3339Nano, r.envelope.AppliedAt)
	return ApplyResultSummary{
		ResultHash: r.resultHash, AppliedAt: appliedAt,
		EvidenceBundleHash: r.envelope.EvidenceBundleHash,
		RuntimeCount:       len(r.envelope.Runtime), HealthCount: len(r.envelope.Health),
	}
}

// ObservationSummary returns defensive copies of the exact validated runtime
// and health outcome links. It never exposes request payloads or evidence bytes.
func (r VerifiedApplyResult) ObservationSummary() ApplyObservationSummary {
	result := ApplyObservationSummary{
		Runtime: make([]ApplyRuntimeObservationSummary, len(r.envelope.Runtime)),
		Health:  make([]ApplyHealthObservationSummary, len(r.envelope.Health)),
	}
	for index, item := range r.envelope.Runtime {
		result.Runtime[index] = ApplyRuntimeObservationSummary{
			RequirementID: item.RequirementID, InstanceRef: item.InstanceRef, Status: item.Status,
			ObservationRef: item.ObservationRef, ObservationDigest: item.ObservationDigest,
		}
	}
	for index, item := range r.envelope.Health {
		result.Health[index] = ApplyHealthObservationSummary{
			RequirementID: item.RequirementID, TargetRef: item.TargetRef, Status: item.Status,
			ObservationRef: item.ObservationRef, ObservationDigest: item.ObservationDigest,
		}
	}
	return result
}

// ExecutorIdentity returns the exact product-owned executor that produced the
// verified Apply result. Upgrade recovery uses this version to select the
// workspace release receipt that was actually applied, never the version of a
// newer CLI process inspecting the workspace.
func (r VerifiedApplyResult) ExecutorIdentity() generationartifact.ApplyExecutorIdentity {
	return r.envelope.Executor
}

// VerifyProductApplyResult revalidates one self-contained, content-addressed
// Apply result against the exact current plan, generation controls, local
// producer trust, and product-owned executor identity.
func (s *Service) VerifyProductApplyResult(input ProductApplyResultVerificationInput) (VerifiedApplyResult, error) {
	if s == nil || len(input.Plan.Canonical()) == 0 {
		return VerifiedApplyResult{}, resolveError(ErrApplyAuthorization, "Apply result verification requires a verified current plan", nil)
	}
	if err := input.Plan.VerifyCompatibility(input.Versions); err != nil {
		return VerifiedApplyResult{}, err
	}
	if err := generationartifact.VerifyReceipt(input.Plan, input.Manifest, input.Receipt); err != nil {
		return VerifiedApplyResult{}, err
	}
	var (
		executor generationartifact.ApplyExecutorIdentity
		trust    map[string]generationartifact.ApplyEvidenceProducerTrust
		err      error
	)
	if s.productRuntimeOwners != nil {
		var registry *applyExecutorRegistry
		registry, err = s.productApplyExecutorRegistry(input.Plan, input.Versions.Runtime)
		if err != nil {
			return VerifiedApplyResult{}, err
		}
		executor = registry.entry.identity
		trust = registry.entry.trustedProducers
	} else {
		executor = s.productApplyVerifyExecutor
		if executor == (generationartifact.ApplyExecutorIdentity{}) || executor.Version != input.Versions.Runtime {
			return VerifiedApplyResult{}, resolveError(ErrApplyAuthorization, "read-only Apply verifier runtime identity is unavailable", nil)
		}
		trust, err = materializeProductApplyTrust(input.Plan, s.productApplyTrust)
		if err != nil {
			return VerifiedApplyResult{}, err
		}
	}
	var envelope verifiedApplyResultEnvelope
	if err := decodeStrictApplyResult(input.Result, &envelope); err != nil {
		return VerifiedApplyResult{}, applyExecutorError(generationartifact.ErrInvalidContract, "apply.result", "strictly decode persisted Apply result", err)
	}
	canonical, err := resolvedplan.CanonicalJSON(envelope)
	if err != nil {
		return VerifiedApplyResult{}, applyExecutorError(generationartifact.ErrInvalidContract, "apply.result", "canonicalize persisted Apply result", err)
	}
	if !bytes.Equal(input.Result, canonical) {
		return VerifiedApplyResult{}, applyExecutorError(generationartifact.ErrNonCanonical, "apply.result", "persisted Apply result is not canonical", nil)
	}
	if envelope.APIVersion != verifiedApplyResultAPIVersion || envelope.Kind != verifiedApplyResultKind {
		return VerifiedApplyResult{}, applyExecutorError(generationartifact.ErrInvalidContract, "apply.result", "unsupported Apply result contract", nil)
	}
	if envelope.Binding != input.Plan.Binding() {
		return VerifiedApplyResult{}, applyExecutorError(generationartifact.ErrBindingMismatch, "apply.result.binding", "does not match the current ResolvedPlan", nil)
	}
	manifestHash, err := input.Manifest.Hash()
	if err != nil {
		return VerifiedApplyResult{}, err
	}
	receiptHash, err := input.Receipt.Hash()
	if err != nil {
		return VerifiedApplyResult{}, err
	}
	request, err := input.Plan.ApplyEvidenceRequest()
	if err != nil {
		return VerifiedApplyResult{}, err
	}
	if envelope.ManifestHash != manifestHash ||
		envelope.GenerationReceiptHash != receiptHash ||
		envelope.RequirementsHash != request.RequirementsHash {
		return VerifiedApplyResult{}, applyExecutorError(generationartifact.ErrBindingMismatch, "apply.result.generation", "does not match the current manifest, receipt, and Apply requirements", nil)
	}
	if envelope.Executor != executor {
		return VerifiedApplyResult{}, applyExecutorError(generationartifact.ErrBindingMismatch, "apply.result.executor", "does not match the product-owned runtime executor", nil)
	}
	appliedAt, err := time.Parse(time.RFC3339Nano, envelope.AppliedAt)
	if err != nil || appliedAt.Location() != time.UTC || appliedAt.Format(time.RFC3339Nano) != envelope.AppliedAt {
		return VerifiedApplyResult{}, applyExecutorError(generationartifact.ErrInvalidContract, "apply.result.appliedAt", "must be an exact RFC3339 UTC instant", err)
	}
	evidence, err := generationartifact.VerifyApplyEvidenceBundleAt(generationartifact.ApplyEvidenceVerificationInput{
		Plan: input.Plan, Manifest: input.Manifest, GenerationReceipt: input.Receipt,
		Executor: envelope.Executor, Bundle: append([]byte(nil), envelope.ApplyEvidence...),
		TrustedProducers: trust,
	}, appliedAt)
	if err != nil {
		return VerifiedApplyResult{}, err
	}
	if evidence.BundleHash() != envelope.EvidenceBundleHash {
		return VerifiedApplyResult{}, applyExecutorError(generationartifact.ErrBindingMismatch, "apply.result.evidenceBundleHash", "does not match the embedded signed Apply evidence", nil)
	}
	if !validApplySHA256(envelope.ArtifactSetHash) ||
		(envelope.SharedArtifactSetHash != "" && !validApplySHA256(envelope.SharedArtifactSetHash)) ||
		(envelope.SharedRequestDigest != "" && !validApplySHA256(envelope.SharedRequestDigest)) ||
		(envelope.SharedResultDigest != "" && !validApplySHA256(envelope.SharedResultDigest)) {
		return VerifiedApplyResult{}, applyExecutorError(generationartifact.ErrInvalidContract, "apply.result.digests", "contains a non-canonical digest", nil)
	}
	requirements := input.Plan.ApplyRequirements()
	runtimeRequirements := append([]generationartifact.ApplyRuntimeRequirement(nil), requirements.RuntimeInstances...)
	runtimeObservations := append([]applyRuntimeObservation(nil), envelope.Runtime...)
	sort.Slice(runtimeRequirements, func(i, j int) bool { return runtimeRequirements[i].ID < runtimeRequirements[j].ID })
	sort.Slice(runtimeObservations, func(i, j int) bool {
		return runtimeObservations[i].RequirementID < runtimeObservations[j].RequirementID
	})
	if err := verifyApplyRuntimeObservations(runtimeRequirements, runtimeObservations); err != nil {
		return VerifiedApplyResult{}, err
	}
	healthRequirements := append([]generationartifact.ApplyHealthRequirement(nil), requirements.HealthRequirements...)
	healthObservations := append([]applyHealthObservation(nil), envelope.Health...)
	sort.Slice(healthRequirements, func(i, j int) bool { return healthRequirements[i].ID < healthRequirements[j].ID })
	sort.Slice(healthObservations, func(i, j int) bool { return healthObservations[i].RequirementID < healthObservations[j].RequirementID })
	if err := verifyApplyHealthObservations(healthRequirements, healthObservations); err != nil {
		return VerifiedApplyResult{}, err
	}
	hash, err := resolvedplan.CanonicalSHA256(envelope)
	if err != nil {
		return VerifiedApplyResult{}, fmt.Errorf("hash canonical Apply result: %w", err)
	}
	return VerifiedApplyResult{envelope: envelope, resultHash: hash}, nil
}

func decodeStrictApplyResult(data []byte, destination any) error {
	if len(data) == 0 {
		return io.ErrUnexpectedEOF
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return err
	}
	return nil
}
