package architecturev2

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/kombifyio/stackkits/contracts/runtimeexecutor"
	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

// HistoricalAppliedRuntimeCustody is immutable historical request data. Its
// canonical representation is checked, but the caller must independently bind
// these bytes to an owner-signed checkpoint. It is never fresh Apply authority.
type HistoricalAppliedRuntimeCustody struct {
	canonical []byte
	capsule   productApplyRecoveryCapsule
	path      string
	workloads []AppliedWorkloadIdentity
}

// LoadHistoricalAppliedRuntimeCustody binds retained data to an independently
// owner-signed Apply result verified by the attested source release. This API
// never creates VerifiedPlan, VerifiedApplyResult or execution authorization.
func (j *ProductApplyFileJournal) LoadHistoricalAppliedRuntimeCustody(ctx context.Context, binding generationartifact.PlanBinding, outputRoot string, signedResult []byte) (HistoricalAppliedRuntimeCustody, error) {
	var envelope verifiedApplyResultEnvelope
	if err := json.Unmarshal(signedResult, &envelope); err != nil {
		return HistoricalAppliedRuntimeCustody{}, err
	}
	canonical, err := resolvedplan.CanonicalJSON(envelope)
	if err != nil || !bytes.Equal(canonical, signedResult) || !validApplySHA256(envelope.SharedRequestDigest) {
		return HistoricalAppliedRuntimeCustody{}, errors.New("historical Apply result has no canonical shared custody binding")
	}
	filePath, data, capsule, err := j.loadProductApplyRecovery(ctx, envelope.SharedRequestDigest)
	if err != nil {
		return HistoricalAppliedRuntimeCustody{}, err
	}
	if err := validateHistoricalAppliedRuntimeCustodyBinding(binding, outputRoot, envelope, capsule); err != nil {
		return HistoricalAppliedRuntimeCustody{}, err
	}
	custody, err := ParseHistoricalAppliedRuntimeCustody(data)
	if err != nil {
		return HistoricalAppliedRuntimeCustody{}, err
	}
	custody.path = filePath
	custody.workloads = envelope.AppliedWorkloads
	return custody, nil
}

func (custody HistoricalAppliedRuntimeCustody) Path() string { return custody.path }
func (custody HistoricalAppliedRuntimeCustody) AppliedWorkloads() []AppliedWorkloadIdentity {
	return cloneAppliedWorkloadIdentities(custody.workloads)
}

// ParseHistoricalAppliedRuntimeCustody uses the same bounded canonical capsule
// validation as Apply recovery. Expiry is retained as historical data; parsing
// does not authorize execution or renew an expired request.
func ParseHistoricalAppliedRuntimeCustody(data []byte) (HistoricalAppliedRuntimeCustody, error) {
	if err := validateProductApplyRecoveryCanonicalSize(data); err != nil {
		return HistoricalAppliedRuntimeCustody{}, err
	}
	capsule, err := parseProductApplyRecoveryCapsule(data)
	if err != nil {
		return HistoricalAppliedRuntimeCustody{}, err
	}
	return HistoricalAppliedRuntimeCustody{canonical: append([]byte(nil), data...), capsule: capsule}, nil
}

func (custody HistoricalAppliedRuntimeCustody) Canonical() []byte {
	return append([]byte(nil), custody.canonical...)
}

func (custody HistoricalAppliedRuntimeCustody) Request() runtimeexecutor.ExecutionRequest {
	return runtimeexecutor.CloneExecutionRequest(custody.capsule.Shared)
}

func (custody HistoricalAppliedRuntimeCustody) Requirements() generationartifact.ApplyRequirements {
	return custody.capsule.Request.Requirements.Clone()
}

func (custody HistoricalAppliedRuntimeCustody) Binding() generationartifact.PlanBinding {
	return custody.capsule.Request.Binding
}

func (custody HistoricalAppliedRuntimeCustody) ManifestHash() string {
	return custody.capsule.Request.ManifestHash
}

func (custody HistoricalAppliedRuntimeCustody) OutputRoot() string {
	return custody.capsule.OutputRoot
}

func (custody HistoricalAppliedRuntimeCustody) ExecutedAt() time.Time {
	return custody.capsule.Request.ExecutionAt
}

func (custody HistoricalAppliedRuntimeCustody) ValidUntil() time.Time {
	value, _ := time.Parse(time.RFC3339Nano, custody.capsule.ValidUntil)
	return value
}
