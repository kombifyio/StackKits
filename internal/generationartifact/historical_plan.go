package generationartifact

import (
	"bytes"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

// HistoricalPlan retains immutable source-generation data. It cannot authorize
// Apply: the caller must authenticate the source binary's PlanInspection.
type HistoricalPlan struct {
	canonical    []byte
	binding      PlanBinding
	outputRoot   string
	requirements ApplyRequirements
}

func ParseHistoricalPlan(data []byte, inspection PlanInspection) (HistoricalPlan, error) {
	plan, err := resolvedplan.DecodeCanonicalPlan(data)
	if err != nil {
		return HistoricalPlan{}, err
	}
	binding, err := bindingFromPlan(plan)
	if err != nil {
		return HistoricalPlan{}, err
	}
	outputRoot, _, err := executionMetadataFromPlan(plan)
	if err != nil {
		return HistoricalPlan{}, err
	}
	if binding != inspection.Binding || outputRoot != inspection.OutputRoot {
		return HistoricalPlan{}, fail(ErrBindingMismatch, "historicalPlan", "canonical source plan differs from its attested inspection")
	}
	requirements, err := applyRequirementsFromPlan(plan, binding)
	if err != nil {
		return HistoricalPlan{}, err
	}
	return HistoricalPlan{canonical: append([]byte(nil), data...), binding: binding, outputRoot: outputRoot, requirements: requirements}, nil
}

func (p HistoricalPlan) Canonical() []byte                    { return append([]byte(nil), p.canonical...) }
func (p HistoricalPlan) Binding() PlanBinding                 { return p.binding }
func (p HistoricalPlan) OutputRoot() string                   { return p.outputRoot }
func (p HistoricalPlan) ApplyRequirements() ApplyRequirements { return p.requirements.Clone() }

// MatchApplyRequirements checks the exact historical scope through the same
// projection used by the original Apply, without issuing execution authority.
func (p HistoricalPlan) MatchApplyRequirements(actual ApplyRequirements, signedRequirementsHash string) error {
	// Apply signs the original requirements; custody carries their defensive
	// clone, which normalizes empty slices. Hash the original source projection.
	expected := p.requirements
	if actual.ExecutionScope != nil {
		var err error
		expected, err = scopeApplyRequirements(p.canonical, expected, *actual.ExecutionScope)
		if err != nil {
			return err
		}
	}
	hash, err := ComputeApplyRequirementsHash(expected)
	if err != nil {
		return err
	}
	if hash != signedRequirementsHash {
		return fail(ErrBindingMismatch, "historicalPlan.requirementsHash", "source requirements and scope differ from the owner-signed Apply hash")
	}
	left, err := resolvedplan.CanonicalJSON(expected.Clone())
	if err != nil {
		return err
	}
	right, err := resolvedplan.CanonicalJSON(actual)
	if err != nil {
		return err
	}
	if !bytes.Equal(left, right) {
		return fail(ErrBindingMismatch, "historicalPlan.requirements", "Apply requirements differ from the attested source plan and scope")
	}
	return nil
}
