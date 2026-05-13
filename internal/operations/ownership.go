package operations

import (
	"errors"
	"fmt"
	"strings"
)

type OwnedResource struct {
	ID    string `json:"id" yaml:"id"`
	Kind  string `json:"kind" yaml:"kind"`
	Owner Owner  `json:"owner" yaml:"owner"`
}

type OwnershipManifest struct {
	Resources []OwnedResource `json:"resources" yaml:"resources"`
}

func (m OwnershipManifest) Validate() error {
	var errs []string
	seen := make(map[string]Owner, len(m.Resources))
	for i, resource := range m.Resources {
		id := strings.TrimSpace(resource.ID)
		if id == "" {
			errs = append(errs, fmt.Sprintf("resources[%d].id is required", i))
			continue
		}
		if strings.TrimSpace(resource.Kind) == "" {
			errs = append(errs, fmt.Sprintf("resources[%d].kind is required", i))
		}
		if !validOwner(resource.Owner) {
			errs = append(errs, fmt.Sprintf("resources[%d].owner %q is not supported", i, resource.Owner))
		}
		if previous, ok := seen[id]; ok {
			if previous != resource.Owner {
				errs = append(errs, fmt.Sprintf("resource %q has multiple owners: %s and %s", id, previous, resource.Owner))
			} else {
				errs = append(errs, fmt.Sprintf("resource %q is declared more than once", id))
			}
			continue
		}
		seen[id] = resource.Owner
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}
