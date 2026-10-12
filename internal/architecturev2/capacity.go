package architecturev2

import (
	"github.com/kombifyio/stackkits/internal/capacityfit"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
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
	assessment, err := resolvedplan.AssessCapacity(result.Plan)
	if err != nil {
		return CapacityAssessment{}, resolveError(ErrResolveFailed, "assess compiled capacity: "+err.Error(), err)
	}
	return CapacityAssessment{PlanHash: result.PlanHash, Assessment: assessment}, nil
}
