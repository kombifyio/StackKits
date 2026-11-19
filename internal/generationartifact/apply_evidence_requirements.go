package generationartifact

import (
	"fmt"

	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

func appendApplyEvidenceGateRequirements(plan resolvedplan.ResolvedPlan, result *ApplyRequirements) error {
	gates, err := requiredObject(plan, "gates", "resolvedPlan.gates")
	if err != nil {
		return err
	}
	rawEvidence, ok := gates["evidence"].([]any)
	if !ok {
		return fail(ErrInvalidPlan, "resolvedPlan.gates.evidence", "must be an array")
	}
	for index, raw := range rawEvidence {
		path := fmt.Sprintf("resolvedPlan.gates.evidence[%d]", index)
		gate, ok := raw.(map[string]any)
		if !ok {
			return fail(ErrInvalidPlan, path, "must be an object")
		}
		gateRef, err := requiredString(gate, "id", path+".id")
		if err != nil {
			return err
		}
		requirement := ApplyEvidenceRequirement{ID: "gate/" + gateRef, GateRef: gateRef, OwnerKind: "evidence-gate", OwnerRef: gateRef}
		if requirement.Phase, err = requiredString(gate, "phase", path+".phase"); err != nil {
			return err
		}
		if requirement.Producer, err = requiredString(gate, "producer", path+".producer"); err != nil {
			return err
		}
		if requirement.Scenario, err = requiredString(gate, "scenario", path+".scenario"); err != nil {
			return err
		}
		if required, ok := gate["required"].(bool); !ok || !required {
			return fail(ErrInvalidPlan, path+".required", "must be true")
		}
		if rawRefs, exists := gate["healthGateRefs"]; exists {
			requirement.HealthGateRefs, err = applyStringList(map[string]any{"refs": rawRefs}, "refs", path+".healthGateRefs")
			if err != nil {
				return err
			}
		}
		if rawRefs, exists := gate["artifactRefs"]; exists {
			requirement.ArtifactRefs, err = applyStringList(map[string]any{"refs": rawRefs}, "refs", path+".artifactRefs")
			if err != nil {
				return err
			}
		}
		result.EvidenceRequirements = append(result.EvidenceRequirements, requirement)
	}
	return nil
}
