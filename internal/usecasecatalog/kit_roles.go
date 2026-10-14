package usecasecatalog

import (
	"fmt"
	"os"
	"path/filepath"
)

// KitUseCaseRoles returns, per kit directory, the native selection role of
// every catalog use case the kit's stackfile Definition lists; a required
// use case counts as default. It is the
// authority that derived kit metadata (stackkit.yaml) must mirror.
func KitUseCaseRoles(root string) (map[string]map[string]string, error) {
	var registry struct {
		Entries map[string]struct{} `json:"entries"`
	}
	if err := loadCUE(root, "foundation", "UseCaseCatalog", &registry); err != nil {
		return nil, err
	}
	directories, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	result := map[string]map[string]string{}
	for _, directory := range directories {
		if !directory.IsDir() || !fileExists(filepath.Join(root, directory.Name(), "stackfile.cue")) {
			continue
		}
		var kit struct {
			Workloads struct{ Required, Defaults, Optional, Forbidden []string } `json:"workloads"`
		}
		if err := loadCUE(root, directory.Name(), "Definition", &kit); err != nil {
			return nil, err
		}
		roles := map[string]string{}
		for _, group := range []struct {
			role string
			refs []string
		}{{"default", kit.Workloads.Required}, {"default", kit.Workloads.Defaults}, {"optional", kit.Workloads.Optional}} {
			for _, ref := range group.refs {
				if _, ok := registry.Entries[ref]; !ok {
					continue
				}
				if previous, ok := roles[ref]; ok && previous != group.role {
					return nil, fmt.Errorf("kit %s lists use case %s as both %s and %s", directory.Name(), ref, previous, group.role)
				}
				roles[ref] = group.role
			}
		}
		result[directory.Name()] = roles
	}
	return result, nil
}
