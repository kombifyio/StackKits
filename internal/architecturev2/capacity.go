package architecturev2

import (
	"github.com/kombifyio/stackkits/internal/capacityfit"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
	"github.com/kombifyio/stackkits/internal/stackspecmigration"
)

type CapacityAssessment struct {
	PlanHash   string                 `json:"planHash"`
	Assessment capacityfit.Assessment `json:"assessment"`
}

// AssessCapacity binds advisory numeric results to the same CUE resolver used
// by generation. It neither persists a plan nor authorizes an executor.
func (s *Service) AssessCapacity(input ResolveInput) (CapacityAssessment, error) {
	result, err := s.Resolve(input)
	if err != nil {
		return CapacityAssessment{}, err
	}
	return s.assessCompiledCapacity(result)
}

// assessCompiledCapacity assesses one freshly resolved plan with the resident
// load policy its kit Definition declares in CUE and the load each selected
// module declares for its compute profile.
func (s *Service) assessCompiledCapacity(result Result) (CapacityAssessment, error) {
	context, err := s.capacityContext(result.Plan)
	if err != nil {
		return CapacityAssessment{}, resolveError(ErrResolveFailed, "bind capacity policy: "+err.Error(), err)
	}
	assessment, err := resolvedplan.AssessCapacityWithContext(result.Plan, context)
	if err != nil {
		return CapacityAssessment{}, resolveError(ErrResolveFailed, "assess compiled capacity: "+err.Error(), err)
	}
	return CapacityAssessment{PlanHash: result.PlanHash, Assessment: assessment}, nil
}

// capacityContext binds the resident-load policy of the plan's kit Definition
// and the declared module loads of the embedded authority. A filesystem or
// fixture authority declares no module load, so those modules stay unknown.
func (s *Service) capacityContext(plan resolvedplan.ResolvedPlan) (resolvedplan.CapacityContext, error) {
	kit, _ := plan["kit"].(map[string]any)
	slug, _ := kit["slug"].(string)
	definition, exists := s.authority.definitions[stackspecmigration.KitProfile(slug)]
	if !exists {
		return resolvedplan.CapacityContext{}, nil
	}
	policy, err := resolvedplan.CapacityPolicyFromDefinition(definition)
	if err != nil {
		return resolvedplan.CapacityContext{}, err
	}
	return resolvedplan.CapacityContext{Policy: policy, ModuleLoad: s.declaredModuleLoad}, nil
}

func (s *Service) declaredModuleLoad(moduleID, computeProfile string) *capacityfit.Load {
	declared, found := s.authority.moduleLoads[moduleID][computeProfile]
	if !found {
		return nil
	}
	return &capacityfit.Load{Residency: declared.Residency, Baseline: declared.Baseline, Burst: declared.Burst}
}
