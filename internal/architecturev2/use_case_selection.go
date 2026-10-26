package architecturev2

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/kombifyio/stackkits/internal/generationartifact"
)

// SelectedUseCaseWorkloads returns only currently selected members of one
// product use case, including its selected companion workloads. Membership
// comes from the embedded CUE catalog, never a slug prefix or initial defaults.
func (s *Service) SelectedUseCaseWorkloads(plan generationartifact.VerifiedPlan, slug string) ([]string, error) {
	verified, err := s.VerifyCanonicalPlan(plan.Canonical())
	if err != nil {
		return nil, err
	}
	slug = strings.TrimSpace(slug)
	members := map[string]bool{}
	for _, contract := range s.authority.catalog.Workloads {
		if contract["useCaseRef"] != slug {
			continue
		}
		metadata, _ := contract["metadata"].(map[string]any)
		id, _ := metadata["id"].(string)
		members[id] = true
	}
	var document struct {
		Workloads []struct {
			ID string `json:"id"`
		} `json:"workloads"`
	}
	if err := json.Unmarshal(verified.Canonical(), &document); err != nil {
		return nil, err
	}
	var selected []string
	for _, workload := range document.Workloads {
		if members[workload.ID] {
			selected = append(selected, workload.ID)
		}
	}
	if len(selected) == 0 {
		return nil, resolveError(ErrInvalidStackSpec, fmt.Sprintf("use case %q has no selected catalog workloads in this plan", slug), nil)
	}
	sort.Strings(selected)
	return selected, nil
}

// OrderWorkloadRemovals projects the current module dependencies and declared
// peer-network consumers onto an exact selected workload set. Consumers are
// stopped before the workload whose network or module they still use.
func (s *Service) OrderWorkloadRemovals(plan generationartifact.VerifiedPlan, selected []string) ([]string, error) {
	verified, err := s.VerifyCanonicalPlan(plan.Canonical())
	if err != nil {
		return nil, err
	}
	var document struct {
		Workloads []struct {
			ID          string `json:"id"`
			Alternative struct {
				ModuleRef string `json:"moduleRef"`
			} `json:"alternative"`
		} `json:"workloads"`
		Modules []struct {
			ID       string   `json:"id"`
			Requires []string `json:"requires"`
		} `json:"modules"`
	}
	if err := json.Unmarshal(verified.Canonical(), &document); err != nil {
		return nil, err
	}
	modules := map[string]string{}
	for _, workload := range document.Workloads {
		if slices.Contains(selected, workload.ID) {
			modules[workload.Alternative.ModuleRef] = workload.ID
		}
	}
	if len(modules) != len(selected) {
		return nil, resolveError(ErrInvalidStackSpec, "removal set must identify unique currently selected workloads", nil)
	}
	consumers := map[string][]string{}
	adapterOwners := map[string]string{}
	for _, contract := range s.authority.catalog.Workloads {
		metadata, _ := contract["metadata"].(map[string]any)
		id, _ := metadata["id"].(string)
		if !slices.Contains(selected, id) {
			continue
		}
		alternatives, _ := contract["alternatives"].([]any)
		for _, raw := range alternatives {
			alternative, _ := raw.(map[string]any)
			module, _ := alternative["moduleRef"].(string)
			adapter, _ := alternative["providesRuntimeAdapterRef"].(string)
			if modules[module] == id && adapter != "" {
				adapterOwners[adapter] = id
			}
		}
	}
	// The verified requirements carry the effective adapter, including a
	// catalog-inherited default. Consumers retire before their platform owner.
	for _, runtime := range verified.ApplyRequirements().RuntimeInstances {
		if !slices.Contains(selected, runtime.WorkloadRef) || runtime.RuntimeAdapter == nil {
			continue
		}
		if owner := adapterOwners[runtime.RuntimeAdapter.ID]; owner != "" && owner != runtime.WorkloadRef {
			consumers[owner] = append(consumers[owner], runtime.WorkloadRef)
		}
	}
	for _, module := range document.Modules {
		consumer, selectedModule := modules[module.ID]
		if !selectedModule {
			continue
		}
		for _, dependency := range module.Requires {
			if owner, selected := modules[dependency]; selected {
				consumers[owner] = append(consumers[owner], consumer)
			}
		}
		for _, peer := range modulePeerWorkloads(s.authority.catalog, module.ID) {
			if slices.Contains(selected, peer) {
				consumers[peer] = append(consumers[peer], consumer)
			}
		}
	}
	remaining := slices.Clone(selected)
	sort.Strings(remaining)
	var order []string
	for len(remaining) > 0 {
		found := false
		for index, id := range remaining {
			if slices.ContainsFunc(consumers[id], func(consumer string) bool { return slices.Contains(remaining, consumer) }) {
				continue
			}
			order = append(order, id)
			remaining = slices.Delete(remaining, index, index+1)
			found = true
			break
		}
		if !found {
			return nil, resolveError(ErrInvalidStackSpec, "selected removal graph contains a dependency cycle", nil)
		}
	}
	return order, nil
}
