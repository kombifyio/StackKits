package capacityfit

import (
	"fmt"
	"slices"
	"sort"
)

func Max(left, right Vector) Vector {
	var result Vector
	for _, axis := range axes {
		a, b := left.Value(axis), right.Value(axis)
		switch {
		case a == nil && b == nil:
			continue
		case a == nil:
			result.Set(axis, *b)
		case b == nil || *a >= *b:
			result.Set(axis, *a)
		default:
			result.Set(axis, *b)
		}
	}
	return result
}

func Add(left, right Vector) Vector {
	var result Vector
	for _, axis := range axes {
		a, b := left.Value(axis), right.Value(axis)
		if a == nil && b == nil {
			continue
		}
		value := 0.0
		if a != nil {
			value += *a
		}
		if b != nil {
			value += *b
		}
		result.Set(axis, value)
	}
	return result
}

// Minimum preserves exact budgets; only the inventory comparison rounds up,
// since current inventory facts attest whole cores/GiB.
func Minimum(demand Demand) Vector { return Max(demand.HostFloor, demand.Reservation) }

func uniqueStrings(values []string) []string {
	values = slices.Clone(values)
	sort.Strings(values)
	return slices.Compact(values)
}

// Aggregate extracts the compiler's original max-floor, additive reservation,
// recommendation and headroom calculation without changing persisted facts.
func Aggregate(modules []Module) ([]Demand, error) {
	byNode := map[string]*Demand{}
	for _, module := range modules {
		if module.ID == "" {
			return nil, fmt.Errorf("module id is required")
		}
		switch module.CapacityDeclaration {
		case "", "declared", "partial", "not_declared":
		default:
			return nil, fmt.Errorf("module %s has an invalid capacity declaration", module.ID)
		}
		for _, budget := range []Vector{module.Compute.HostFloor, module.Compute.Reservation, module.Compute.Recommended, module.Compute.Headroom} {
			if err := validateVector(budget, false); err != nil {
				return nil, fmt.Errorf("module %s: %w", module.ID, err)
			}
		}
		reservation := module.Compute.Reservation
		recommended := Max(module.Compute.Recommended, reservation)
		minimum := Max(module.Compute.HostFloor, reservation)
		incomplete := map[Axis]bool{}
		for _, axis := range axes {
			incomplete[axis] = minimum.Value(axis) == nil || module.CapacityDeclaration == "not_declared"
		}
		for _, orthogonal := range module.OrthogonalReservations {
			if err := validateVector(orthogonal, false); err != nil {
				return nil, fmt.Errorf("module %s orthogonal profile: %w", module.ID, err)
			}
			reservation, recommended = Add(reservation, orthogonal), Add(recommended, orthogonal)
			for _, axis := range axes {
				incomplete[axis] = incomplete[axis] || orthogonal.Value(axis) == nil
			}
		}
		for _, nodeRef := range module.NodeRefs {
			if nodeRef == "" {
				return nil, fmt.Errorf("module %s has an empty node reference", module.ID)
			}
			entry := byNode[nodeRef]
			if entry == nil {
				entry = &Demand{NodeRef: nodeRef, ModuleRefs: []string{}, UnverifiedModuleRefs: []string{}, UnverifiedAxes: []Axis{}}
				byNode[nodeRef] = entry
			}
			entry.ModuleRefs = append(entry.ModuleRefs, module.ID)
			unverified := false
			for _, axis := range axes {
				if incomplete[axis] {
					unverified = true
					if !slices.Contains(entry.UnverifiedAxes, axis) {
						entry.UnverifiedAxes = append(entry.UnverifiedAxes, axis)
					}
				}
			}
			if unverified {
				entry.UnverifiedModuleRefs = append(entry.UnverifiedModuleRefs, module.ID)
			}
			entry.HostFloor = Max(entry.HostFloor, module.Compute.HostFloor)
			entry.Reservation = Add(entry.Reservation, reservation)
			entry.Recommended = Add(entry.Recommended, recommended)
			entry.Headroom = Add(entry.Headroom, module.Compute.Headroom)
		}
	}
	nodes := make([]string, 0, len(byNode))
	for nodeRef := range byNode {
		nodes = append(nodes, nodeRef)
	}
	sort.Strings(nodes)
	result := make([]Demand, 0, len(nodes))
	for _, nodeRef := range nodes {
		entry := byNode[nodeRef]
		entry.Recommended = Max(Minimum(*entry), Add(entry.Recommended, entry.Headroom))
		for _, budget := range []Vector{entry.HostFloor, entry.Reservation, entry.Recommended, entry.Headroom} {
			if err := validateVector(budget, false); err != nil {
				return nil, fmt.Errorf("node %s aggregate: %w", nodeRef, err)
			}
		}
		entry.ModuleRefs, entry.UnverifiedModuleRefs = uniqueStrings(entry.ModuleRefs), uniqueStrings(entry.UnverifiedModuleRefs)
		slices.Sort(entry.UnverifiedAxes)
		result = append(result, *entry)
	}
	return result, nil
}
