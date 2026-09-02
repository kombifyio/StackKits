package architecturev2

import (
	"time"

	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
)

// HistoricalAppliedRuntimeCustody is immutable historical request data. Its
// canonical representation is checked, but the caller must independently bind
// these bytes to an owner-signed checkpoint. It is never fresh Apply authority.
type HistoricalAppliedRuntimeCustody struct {
	canonical []byte
	capsule   productApplyRecoveryCapsule
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
