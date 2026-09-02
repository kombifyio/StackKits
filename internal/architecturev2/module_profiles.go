package architecturev2

import (
	"encoding/json"
	"sort"
)

// ModuleProfileCatalogEntry is a read-only projection of module-owned CUE
// contracts. It makes no selection, recommendation or host-admission claim.
type ModuleProfileCatalogEntry struct {
	ModuleID            string                    `json:"module_id"`
	ComputeProfiles     map[string]map[string]any `json:"compute_profiles"`
	StorageProfiles     map[string]map[string]any `json:"storage_profiles,omitempty"`
	AcceleratorProfiles map[string]map[string]any `json:"accelerator_profiles,omitempty"`
}

func (s *Service) ListModuleProfileContracts() ([]ModuleProfileCatalogEntry, error) {
	if s == nil || s.authority == nil {
		return nil, resolveError(ErrAuthorityLoad, "module profile authority is unavailable", nil)
	}
	entries := []ModuleProfileCatalogEntry{}
	for _, module := range s.authority.catalog.Modules {
		if _, declared := module["computeProfiles"]; !declared {
			continue
		}
		// Decode an isolated copy; callers cannot modify embedded authority.
		raw, err := json.Marshal(module)
		if err != nil {
			return nil, err
		}
		var contract struct {
			Metadata struct {
				ID string `json:"id"`
			} `json:"metadata"`
			ComputeProfiles     map[string]map[string]any `json:"computeProfiles"`
			StorageProfiles     map[string]map[string]any `json:"storageProfiles"`
			AcceleratorProfiles map[string]map[string]any `json:"acceleratorProfiles"`
		}
		if err := json.Unmarshal(raw, &contract); err != nil {
			return nil, err
		}
		entries = append(entries, ModuleProfileCatalogEntry{
			ModuleID: contract.Metadata.ID, ComputeProfiles: contract.ComputeProfiles,
			StorageProfiles: contract.StorageProfiles, AcceleratorProfiles: contract.AcceleratorProfiles,
		})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].ModuleID < entries[j].ModuleID })
	return entries, nil
}
