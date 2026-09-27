package architecturev2renderer

import "github.com/kombifyio/stackkits/internal/generationartifact"

// PlanRuntimeEntryComponentRef returns the CUE-declared runtime entry
// component of one render unit in a verified plan. Callers that narrow a
// signed application bundle compare its entry against this plan fact instead
// of assuming the entry component is named after the unit. The boolean is
// false when the unit declares no entry component.
func PlanRuntimeEntryComponentRef(plan generationartifact.VerifiedPlan, moduleRef, unitRef string) (string, bool, error) {
	projection, err := parseVerifiedPlan(plan)
	if err != nil {
		return "", false, err
	}
	for _, module := range projection.modules {
		if module.id != moduleRef {
			continue
		}
		for _, unit := range module.units {
			if unit.id == unitRef {
				return unit.runtime.entryComponentRef, unit.runtime.entryComponentRef != "", nil
			}
		}
	}
	return "", false, fail(ErrInvalidPlan, "resolvedPlan.modules."+moduleRef+".renderUnits."+unitRef, "render unit is absent from the verified plan")
}
