package architecturev2

import (
	"fmt"
	"sort"

	"github.com/kombifyio/stackkits/internal/resolvedplan"
	"github.com/kombifyio/stackkits/internal/stackspecmigration"
)

// AddOnCatalogEntry is the read-only Architecture v2 add-on metadata exposed
// to CLI and API adapters. It is projected exclusively from the CUE-bound
// catalog snapshot owned by Service; it does not describe selection state,
// mutation support, plan readiness, or runtime availability.
type AddOnCatalogEntry struct {
	ID            string                          `json:"id"`
	Version       string                          `json:"version"`
	Description   string                          `json:"description"`
	SupportedKits []stackspecmigration.KitProfile `json:"supportedKits"`
}

// ListSupportedAddOns returns the embedded catalog entries that declare
// support for profile. Results and each SupportedKits list are sorted so
// callers never depend on map, CUE, or bundle serialization order.
//
// This is discovery metadata only. Presence in this list is not an authoring,
// mutation, plan-readiness, or deployment claim.
func (s *Service) ListSupportedAddOns(profile stackspecmigration.KitProfile) ([]AddOnCatalogEntry, error) {
	if s == nil || s.authority == nil {
		return nil, resolveError(ErrResolveFailed, "service is not initialized", nil)
	}
	if len(s.authority.contractSources) == 0 || s.authority.planAuthority.Class != "product" || s.authority.planAuthority.Document != "catalog" {
		return nil, resolveError(ErrAuthorityLoad, "embedded Architecture v2 product catalog authority is required", nil)
	}
	if !isCanonicalProductKitProfile(profile) {
		return nil, resolveError(ErrInvalidStackSpec, fmt.Sprintf("kit profile %q is not a canonical product kit", profile), nil)
	}
	if _, exists := s.authority.definitions[profile]; !exists {
		return nil, resolveError(ErrAuthorityLoad, fmt.Sprintf("no governed Definition exists for %q", profile), nil)
	}

	entries, err := supportedAddOnCatalogEntries(s.authority.catalog.AddOns, profile)
	if err != nil {
		return nil, resolveError(ErrAuthorityLoad, "decode Architecture v2 add-on catalog: "+err.Error(), err)
	}
	return entries, nil
}

func supportedAddOnCatalogEntries(contracts []resolvedplan.AddOnContract, profile stackspecmigration.KitProfile) ([]AddOnCatalogEntry, error) {
	entries := make([]AddOnCatalogEntry, 0, len(contracts))
	seen := make(map[string]struct{}, len(contracts))
	for index, contract := range contracts {
		entry, err := decodeAddOnCatalogEntry(contract)
		if err != nil {
			return nil, fmt.Errorf("addons[%d]: %w", index, err)
		}
		if _, duplicate := seen[entry.ID]; duplicate {
			return nil, fmt.Errorf("add-on %q is duplicated", entry.ID)
		}
		seen[entry.ID] = struct{}{}
		if supportsAddOnKit(entry.SupportedKits, profile) {
			entries = append(entries, entry)
		}
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].ID == entries[j].ID {
			return entries[i].Version < entries[j].Version
		}
		return entries[i].ID < entries[j].ID
	})
	return entries, nil
}

func decodeAddOnCatalogEntry(contract resolvedplan.AddOnContract) (AddOnCatalogEntry, error) {
	metadata, ok := contract["metadata"].(map[string]any)
	if !ok || metadata == nil {
		return AddOnCatalogEntry{}, fmt.Errorf("metadata must be an object")
	}
	id, ok := metadata["id"].(string)
	if !ok || id == "" {
		return AddOnCatalogEntry{}, fmt.Errorf("metadata.id must be a non-empty string")
	}
	version, ok := metadata["version"].(string)
	if !ok {
		return AddOnCatalogEntry{}, fmt.Errorf("add-on %q metadata.version must be a string", id)
	}
	description, ok := metadata["description"].(string)
	if !ok {
		return AddOnCatalogEntry{}, fmt.Errorf("add-on %q metadata.description must be a string", id)
	}

	supportedKits, err := decodeSupportedAddOnKits(contract["supportedKits"], id)
	if err != nil {
		return AddOnCatalogEntry{}, err
	}
	return AddOnCatalogEntry{
		ID:            id,
		Version:       version,
		Description:   description,
		SupportedKits: supportedKits,
	}, nil
}

func decodeSupportedAddOnKits(value any, id string) ([]stackspecmigration.KitProfile, error) {
	items, ok := value.([]any)
	if !ok || len(items) == 0 {
		return nil, fmt.Errorf("add-on %q supportedKits must be a non-empty list", id)
	}
	kits := make([]stackspecmigration.KitProfile, 0, len(items))
	seen := make(map[stackspecmigration.KitProfile]struct{}, len(items))
	for index, item := range items {
		slug, ok := item.(string)
		profile := stackspecmigration.KitProfile(slug)
		if !ok || !isCanonicalProductKitProfile(profile) {
			return nil, fmt.Errorf("add-on %q supportedKits[%d] is not a canonical product kit", id, index)
		}
		if _, duplicate := seen[profile]; duplicate {
			return nil, fmt.Errorf("add-on %q repeats supported kit %q", id, profile)
		}
		seen[profile] = struct{}{}
		kits = append(kits, profile)
	}
	sort.Slice(kits, func(i, j int) bool { return kits[i] < kits[j] })
	return kits, nil
}

func supportsAddOnKit(supported []stackspecmigration.KitProfile, profile stackspecmigration.KitProfile) bool {
	index := sort.Search(len(supported), func(index int) bool { return supported[index] >= profile })
	return index < len(supported) && supported[index] == profile
}
