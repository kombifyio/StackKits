package architecturev2

import (
	"bytes"
	"errors"
	"time"

	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

// ApplyProgramRecord is untrusted data until the existing Owner/member Apply
// receipt authenticates its exact content address and generation binding. It
// is deliberately not a VerifiedApplyResult or fresh execution authority.
type ApplyProgramRecord struct {
	Binding               generationartifact.PlanBinding
	ManifestHash          string
	GenerationReceiptHash string
	ResultHash            string
	AppliedAt             time.Time
	Program               *ApplyProgramIdentity
}

func InspectApplyProgramRecord(canonicalResult []byte) (ApplyProgramRecord, error) {
	var envelope verifiedApplyResultEnvelope
	if err := decodeStrictApplyResult(canonicalResult, &envelope); err != nil {
		return ApplyProgramRecord{}, err
	}
	canonical, err := resolvedplan.CanonicalJSON(envelope)
	if err != nil || !bytes.Equal(canonical, canonicalResult) || envelope.APIVersion != verifiedApplyResultAPIVersion || envelope.Kind != verifiedApplyResultKind {
		return ApplyProgramRecord{}, errors.New("original Apply program record is not a canonical supported result")
	}
	if err := validateApplyProgram(envelope.Program); err != nil {
		return ApplyProgramRecord{}, err
	}
	at, err := time.Parse(time.RFC3339Nano, envelope.AppliedAt)
	if err != nil || at.Location() != time.UTC || at.Format(time.RFC3339Nano) != envelope.AppliedAt {
		return ApplyProgramRecord{}, errors.New("original Apply program record has an invalid Apply instant")
	}
	if !validApplySHA256(envelope.ManifestHash) || !validApplySHA256(envelope.GenerationReceiptHash) {
		return ApplyProgramRecord{}, errors.New("original Apply program record lacks exact generation digests")
	}
	hash, err := resolvedplan.CanonicalSHA256(envelope)
	if err != nil {
		return ApplyProgramRecord{}, err
	}
	return ApplyProgramRecord{Binding: envelope.Binding, ManifestHash: envelope.ManifestHash, GenerationReceiptHash: envelope.GenerationReceiptHash, ResultHash: hash, AppliedAt: at, Program: cloneApplyProgram(envelope.Program)}, nil
}
