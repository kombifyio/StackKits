package resolvedplan

import (
	"fmt"
	"sort"
)

type indexedCatalog struct {
	capabilities                 map[string]map[string]any
	providers                    map[string]map[string]any
	addons                       map[string]map[string]any
	modules                      map[string]map[string]any
	privilegedInterfaceApprovals []map[string]any
	planArtifacts                []map[string]any
}

func indexCatalog(catalog Catalog) (*indexedCatalog, error) {
	if err := validateCatalogGenerationArtifactUniqueness(catalog); err != nil {
		return nil, err
	}
	if err := validateCatalogImplementationInterfaces(catalog); err != nil {
		return nil, err
	}
	indexed := &indexedCatalog{
		capabilities:                 make(map[string]map[string]any, len(catalog.Capabilities)),
		providers:                    make(map[string]map[string]any, len(catalog.Providers)),
		addons:                       make(map[string]map[string]any, len(catalog.AddOns)),
		modules:                      make(map[string]map[string]any, len(catalog.Modules)),
		privilegedInterfaceApprovals: make([]map[string]any, 0, len(catalog.PrivilegedInterfaceApprovals)),
		planArtifacts:                make([]map[string]any, 0, len(catalog.PlanArtifacts)),
	}
	for _, artifact := range catalog.PlanArtifacts {
		indexed.planArtifacts = append(indexed.planArtifacts, map[string]any(artifact))
	}
	for _, approval := range catalog.PrivilegedInterfaceApprovals {
		indexed.privilegedInterfaceApprovals = append(indexed.privilegedInterfaceApprovals, map[string]any(approval))
	}

	for i, contract := range catalog.Capabilities {
		object := map[string]any(contract)
		id, err := metadataID(object, fmt.Sprintf("catalog.capabilities[%d]", i))
		if err != nil {
			return nil, err
		}
		if _, exists := indexed.capabilities[id]; exists {
			return nil, fail(ErrInvalidInput, "catalog.capabilities", "duplicate capability contract %q", id)
		}
		indexed.capabilities[id] = object
	}

	for i, provider := range catalog.Providers {
		object := map[string]any(provider)
		id, err := metadataID(object, fmt.Sprintf("catalog.providers[%d]", i))
		if err != nil {
			return nil, err
		}
		if _, exists := indexed.providers[id]; exists {
			return nil, fail(ErrInvalidInput, "catalog.providers", "duplicate provider contract %q", id)
		}
		indexed.providers[id] = object
	}

	for i, addon := range catalog.AddOns {
		object := map[string]any(addon)
		id, err := metadataID(object, fmt.Sprintf("catalog.addons[%d]", i))
		if err != nil {
			return nil, err
		}
		if _, exists := indexed.addons[id]; exists {
			return nil, fail(ErrInvalidInput, "catalog.addons", "duplicate add-on contract %q", id)
		}
		indexed.addons[id] = object
	}
	for i, module := range catalog.Modules {
		object := map[string]any(module)
		id, err := metadataID(object, fmt.Sprintf("catalog.modules[%d]", i))
		if err != nil {
			return nil, err
		}
		if _, exists := indexed.modules[id]; exists {
			return nil, fail(ErrInvalidInput, "catalog.modules", "duplicate module contract %q", id)
		}
		indexed.modules[id] = object
	}
	return indexed, nil
}

func metadataID(object map[string]any, path string) (string, error) {
	metadata, err := objectField(object, path, "metadata")
	if err != nil {
		return "", err
	}
	return stringField(metadata, joinPath(path, "metadata"), "id")
}

func metadataVersion(object map[string]any, path string) (string, error) {
	metadata, err := objectField(object, path, "metadata")
	if err != nil {
		return "", err
	}
	return stringField(metadata, joinPath(path, "metadata"), "version")
}

func (c *indexedCatalog) providerCandidates(capability string, requiredSiteKinds []string) ([]string, error) {
	var candidates []string
	for id, provider := range c.providers {
		provides, err := stringListField(provider, "catalog.providers."+id, "provides", true)
		if err != nil {
			return nil, err
		}
		if !contains(provides, capability) {
			continue
		}
		realization, err := objectField(provider, "catalog.providers."+id, "realization")
		if err != nil {
			return nil, err
		}
		kind, err := stringField(realization, "catalog.providers."+id+".realization", "kind")
		if err != nil {
			return nil, err
		}
		if kind == "none" {
			continue
		}
		supported, err := stringListField(provider, "catalog.providers."+id, "supportedSiteKinds", true)
		if err != nil {
			return nil, err
		}
		coversAll := true
		for _, requiredKind := range requiredSiteKinds {
			if !contains(supported, requiredKind) {
				coversAll = false
				break
			}
		}
		if coversAll {
			candidates = append(candidates, id)
		}
	}
	sort.Strings(candidates)
	return candidates, nil
}

func (c *indexedCatalog) defaultProviderCandidates(candidates, requiredSiteKinds []string) ([]string, error) {
	var defaults []string
	for _, id := range candidates {
		selection, exists, err := optionalObjectField(c.providers[id], "catalog.providers."+id, "selection")
		if err != nil {
			return nil, err
		}
		if !exists {
			continue
		}
		defaultKinds, err := stringListField(selection, "catalog.providers."+id+".selection", "defaultForSiteKinds", false)
		if err != nil {
			return nil, err
		}
		coversAll := true
		for _, kind := range requiredSiteKinds {
			if !contains(defaultKinds, kind) {
				coversAll = false
				break
			}
		}
		if coversAll {
			defaults = append(defaults, id)
		}
	}
	sort.Strings(defaults)
	return defaults, nil
}

func requirements(object map[string]any, path string) ([]requirement, error) {
	value, exists := object["requires"]
	if !exists {
		return nil, nil
	}
	values, ok := value.([]any)
	if !ok {
		return nil, fail(ErrInvalidInput, path+".requires", "expected list")
	}
	result := make([]requirement, 0, len(values))
	for i, value := range values {
		requirementObject, err := asObject(value, fmt.Sprintf("%s.requires[%d]", path, i))
		if err != nil {
			return nil, err
		}
		id, err := stringField(requirementObject, fmt.Sprintf("%s.requires[%d]", path, i), "id")
		if err != nil {
			return nil, err
		}
		optional, err := boolFieldDefault(requirementObject, fmt.Sprintf("%s.requires[%d]", path, i), "optional", false)
		if err != nil {
			return nil, err
		}
		minVersion, _, err := optionalStringField(requirementObject, fmt.Sprintf("%s.requires[%d]", path, i), "minVersion")
		if err != nil {
			return nil, err
		}
		result = append(result, requirement{id: id, optional: optional, minVersion: minVersion})
	}
	return result, nil
}

type requirement struct {
	id         string
	optional   bool
	minVersion string
}
