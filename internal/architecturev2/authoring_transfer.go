package architecturev2

import (
	"encoding/json"
	"fmt"
	"sort"
)

// PortableApplicationConfiguration is one receiving application's portable
// source configuration: the alternative it runs, its public settings and its
// selected module profiles. Placement, routes, Foundation identities and
// secret references stay with their own authority; only values that mean the
// same thing on any node are carried.
type PortableApplicationConfiguration struct {
	Alternative string
	Settings    map[string]any
	Profiles    ModuleProfileOverride
}

// MaterializeApplicationTransferAddition authors every configured application
// into retained intent. It uses the same catalog selection and public-setting
// validation as use-cases add/configure, adds only applications the destination
// does not already select, and returns the canonical receiving candidate.
func (s *Service) MaterializeApplicationTransferAddition(raw []byte, applications map[string]PortableApplicationConfiguration) (StackSpecValidation, error) {
	if len(applications) == 0 {
		return StackSpecValidation{}, resolveError(ErrInvalidStackSpec, "a receiving transfer candidate requires at least one application configuration", nil)
	}
	ids := make([]string, 0, len(applications))
	for id := range applications {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	candidate, err := s.ValidateStackSpec(raw)
	if err != nil {
		return StackSpecValidation{}, err
	}
	var selected struct {
		Workloads map[string]json.RawMessage `json:"workloads"`
	}
	if err := json.Unmarshal(candidate.CanonicalStackSpec, &selected); err != nil {
		return StackSpecValidation{}, err
	}
	for _, id := range ids {
		if _, exists := selected.Workloads[id]; exists {
			return StackSpecValidation{}, resolveError(ErrInvalidStackSpec, fmt.Sprintf("application %s is already selected on the receiving node; a transfer adds only new applications", id), nil)
		}
	}
	for _, id := range ids {
		application := applications[id]
		if err := validatePublicConfigurationSettings(s.authority.catalog, id, application.Alternative, application.Settings); err != nil {
			return StackSpecValidation{}, fmt.Errorf("application %s: %w", id, err)
		}
		candidate, err = s.MaterializeUseCaseChange(candidate.CanonicalStackSpec, id, UseCaseChange{
			Alternative: application.Alternative, RuntimeAdapterRef: "standalone-compose", Settings: application.Settings, Profiles: application.Profiles,
		}, true)
		if err != nil {
			return StackSpecValidation{}, fmt.Errorf("application %s: %w", id, err)
		}
		var added struct {
			Workloads map[string]struct {
				RuntimeAdapterRef string `json:"runtimeAdapterRef"`
			} `json:"workloads"`
		}
		if err := json.Unmarshal(candidate.CanonicalStackSpec, &added); err != nil {
			return StackSpecValidation{}, err
		}
		// A transferred runtime is restored only by the native standalone-compose
		// adapter; a catalog default of another adapter is refused here instead
		// of failing after the source is held.
		if added.Workloads[id].RuntimeAdapterRef != "standalone-compose" {
			return StackSpecValidation{}, resolveError(ErrInvalidStackSpec, fmt.Sprintf("application %s must use the native standalone-compose runtime adapter for a transfer", id), nil)
		}
	}
	return candidate, nil
}
