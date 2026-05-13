package cue

import (
	"fmt"
	"sort"
	"strings"
)

// ModuleGraph holds resolved modules in dependency order with validation results.
type ModuleGraph struct {
	// Ordered is the topologically sorted list of enabled module names.
	Ordered []string
	// Modules maps module name to its contract.
	Modules map[string]ModuleContract
	// Layers groups module names by their layer for staged deployment.
	Layers map[string][]string
}

// ResolveError describes a dependency resolution failure.
type ResolveError struct {
	Module  string
	Message string
}

func (e *ResolveError) Error() string {
	return fmt.Sprintf("module %s: %s", e.Module, e.Message)
}

// Resolver validates and orders modules based on their dependency declarations.
type Resolver struct{}

// NewResolver creates a new dependency resolver.
func NewResolver() *Resolver {
	return &Resolver{}
}

// Resolve takes a set of module contracts and returns a validated, ordered ModuleGraph.
// Only enabled modules are included. Disabled modules that are required by enabled
// modules produce an error (unless the dependency is optional).
func (r *Resolver) Resolve(contracts []ModuleContract) (*ModuleGraph, error) {
	byName := ModulesByName(contracts)

	// Filter to enabled modules only
	enabled := make(map[string]ModuleContract)
	for name, mc := range byName {
		if mc.Enabled {
			enabled[name] = mc
		}
	}

	// Validate all required dependencies are present and enabled
	if err := r.validateDependencies(enabled, byName); err != nil {
		return nil, err
	}

	// Detect circular dependencies
	if err := r.detectCycles(enabled); err != nil {
		return nil, err
	}

	// Topological sort
	ordered, err := r.topologicalSort(enabled)
	if err != nil {
		return nil, err
	}

	// Group by layer
	layers := r.groupByLayer(enabled)

	return &ModuleGraph{
		Ordered: ordered,
		Modules: enabled,
		Layers:  layers,
	}, nil
}

// validateDependencies checks that every required dependency of an enabled module
// is itself enabled. Optional dependencies that are missing are silently skipped.
func (r *Resolver) validateDependencies(enabled, all map[string]ModuleContract) error {
	var errs []string

	for name, mc := range enabled {
		if mc.Requires == nil {
			continue
		}
		for depName, dep := range mc.Requires.Services {
			if _, ok := enabled[depName]; ok {
				continue
			}
			if dep.Optional {
				continue
			}
			// Check if the module exists at all but is disabled
			if _, exists := all[depName]; exists {
				errs = append(errs, fmt.Sprintf("module %q requires %q which is disabled", name, depName))
			} else {
				errs = append(errs, fmt.Sprintf("module %q requires %q which does not exist", name, depName))
			}
		}
	}

	if len(errs) > 0 {
		sort.Strings(errs)
		return fmt.Errorf("dependency validation failed:\n  %s", strings.Join(errs, "\n  "))
	}
	return nil
}

// detectCycles uses DFS coloring to find circular dependencies.
func (r *Resolver) detectCycles(modules map[string]ModuleContract) error {
	const (
		white = 0 // unvisited
		gray  = 1 // in progress
		black = 2 // done
	)

	color := make(map[string]int)
	for name := range modules {
		color[name] = white
	}

	var path []string

	var visit func(name string) error
	visit = func(name string) error {
		color[name] = gray
		path = append(path, name)

		mc := modules[name]
		if mc.Requires != nil {
			for depName, dep := range mc.Requires.Services {
				if _, ok := modules[depName]; !ok {
					if dep.Optional {
						continue
					}
					// Missing non-optional deps caught by validateDependencies
					continue
				}

				switch color[depName] {
				case gray:
					// Found cycle — extract the cycle path
					cycleStart := -1
					for i, p := range path {
						if p == depName {
							cycleStart = i
							break
						}
					}
					cycle := append(path[cycleStart:], depName)
					return &ResolveError{
						Module:  name,
						Message: fmt.Sprintf("circular dependency: %s", strings.Join(cycle, " -> ")),
					}
				case white:
					if err := visit(depName); err != nil {
						return err
					}
				}
			}
		}

		color[name] = black
		path = path[:len(path)-1]
		return nil
	}

	// Visit in sorted order for deterministic error messages
	names := sortedKeys(modules)
	for _, name := range names {
		if color[name] == white {
			if err := visit(name); err != nil {
				return err
			}
		}
	}

	return nil
}

// topologicalSort returns module names in dependency order (dependencies first).
func (r *Resolver) topologicalSort(modules map[string]ModuleContract) ([]string, error) {
	// Build adjacency: module -> modules it depends on
	inDegree := make(map[string]int)
	dependents := make(map[string][]string) // dep -> modules that depend on it

	for name := range modules {
		inDegree[name] = 0
	}

	for name, mc := range modules {
		if mc.Requires == nil {
			continue
		}
		for depName, dep := range mc.Requires.Services {
			if _, ok := modules[depName]; !ok {
				if dep.Optional {
					continue
				}
				continue
			}
			inDegree[name]++
			dependents[depName] = append(dependents[depName], name)
		}
	}

	// Kahn's algorithm with sorted queue for determinism
	var queue []string
	for name, deg := range inDegree {
		if deg == 0 {
			queue = append(queue, name)
		}
	}
	sort.Strings(queue)

	var ordered []string
	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		ordered = append(ordered, node)

		deps := dependents[node]
		sort.Strings(deps)
		for _, dep := range deps {
			inDegree[dep]--
			if inDegree[dep] == 0 {
				queue = append(queue, dep)
				sort.Strings(queue)
			}
		}
	}

	if len(ordered) != len(modules) {
		return nil, fmt.Errorf("topological sort incomplete: processed %d of %d modules", len(ordered), len(modules))
	}

	return ordered, nil
}

// groupByLayer groups module names by their metadata.layer value.
func (r *Resolver) groupByLayer(modules map[string]ModuleContract) map[string][]string {
	layers := make(map[string][]string)
	for name, mc := range modules {
		layer := mc.Metadata.Layer
		if layer == "" {
			layer = "unknown"
		}
		layers[layer] = append(layers[layer], name)
	}
	// Sort within each layer for determinism
	for layer := range layers {
		sort.Strings(layers[layer])
	}
	return layers
}

// DependenciesOf returns the direct dependency names for a module (enabled, non-optional only).
func (g *ModuleGraph) DependenciesOf(moduleName string) []string {
	mc, ok := g.Modules[moduleName]
	if !ok || mc.Requires == nil {
		return nil
	}

	var deps []string
	for depName, dep := range mc.Requires.Services {
		if dep.Optional {
			if _, ok := g.Modules[depName]; !ok {
				continue
			}
		}
		if _, ok := g.Modules[depName]; ok {
			deps = append(deps, depName)
		}
	}
	sort.Strings(deps)
	return deps
}

func sortedKeys(m map[string]ModuleContract) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
