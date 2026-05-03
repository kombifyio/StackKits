// Package composition resolves module dependencies and determines deployment order.
package composition

import (
	"fmt"
	"sort"
	"strings"

	cueval "github.com/kombifyio/stackkits/internal/cue"
)

// DependencyGraph represents the directed dependency graph of module contracts.
type DependencyGraph struct {
	// Modules keyed by module name
	modules map[string]*cueval.ModuleContract
	// Adjacency list: module → modules it depends on
	edges map[string][]string
}

// ValidationError describes a dependency validation failure.
type ValidationError struct {
	Module  string
	Message string
}

func (e ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", e.Module, e.Message)
}

// BuildGraph constructs a dependency graph from module contracts.
func BuildGraph(contracts []cueval.ModuleContract) *DependencyGraph {
	g := &DependencyGraph{
		modules: make(map[string]*cueval.ModuleContract, len(contracts)),
		edges:   make(map[string][]string, len(contracts)),
	}

	for i := range contracts {
		mc := &contracts[i]
		name := mc.Metadata.Name
		g.modules[name] = mc

		if mc.Requires != nil {
			for dep := range mc.Requires.Services {
				g.edges[name] = append(g.edges[name], dep)
			}
			// Sort for deterministic output
			sort.Strings(g.edges[name])
		}
	}

	return g
}

// Validate checks that all dependencies are satisfiable:
//   - Every required service exists as a module
//   - Required capabilities are provided by the dependency
//   - No cycles exist in the graph
func (g *DependencyGraph) Validate() []ValidationError {
	var errs []ValidationError

	// Check all required services exist
	for name, deps := range g.edges {
		for _, dep := range deps {
			if _, ok := g.modules[dep]; !ok {
				errs = append(errs, ValidationError{
					Module:  name,
					Message: fmt.Sprintf("requires module %q which does not exist", dep),
				})
				continue
			}

			// Check required capabilities
			mc := g.modules[name]
			if mc.Requires == nil {
				continue
			}
			reqSvc, ok := mc.Requires.Services[dep]
			if !ok {
				continue
			}
			depMod := g.modules[dep]
			for _, cap := range reqSvc.Provides {
				if depMod.Provides == nil || !depMod.Provides.Capabilities[cap] {
					if !reqSvc.Optional {
						errs = append(errs, ValidationError{
							Module:  name,
							Message: fmt.Sprintf("requires capability %q from %q, but %q does not provide it", cap, dep, dep),
						})
					}
				}
			}
		}
	}

	// Check for cycles
	if cycle := g.detectCycle(); cycle != nil {
		errs = append(errs, ValidationError{
			Module:  cycle[0],
			Message: fmt.Sprintf("circular dependency: %s", strings.Join(cycle, " → ")),
		})
	}

	return errs
}

// TopologicalSort returns modules in dependency order (dependencies first).
// Returns an error if the graph contains a cycle.
func (g *DependencyGraph) TopologicalSort() ([]string, error) {
	// Kahn's algorithm.
	// Our edges map: module → [modules it depends on].
	// For topological sort, "A depends on B" means edge B→A, so
	// in-degree of A = number of modules A depends on (that exist).
	inDegree := make(map[string]int, len(g.modules))
	for name := range g.modules {
		inDegree[name] = 0
	}
	for name, deps := range g.edges {
		count := 0
		for _, dep := range deps {
			if _, ok := g.modules[dep]; ok {
				count++
			}
		}
		inDegree[name] = count
	}

	// Queue starts with modules that have no dependencies
	var queue []string
	for name, deg := range inDegree {
		if deg == 0 {
			queue = append(queue, name)
		}
	}
	sort.Strings(queue) // deterministic

	var sorted []string
	for len(queue) > 0 {
		// Pop
		node := queue[0]
		queue = queue[1:]
		sorted = append(sorted, node)

		// For all modules that depend on this node, decrease in-degree
		for name, deps := range g.edges {
			for _, dep := range deps {
				if dep == node {
					inDegree[name]--
					if inDegree[name] == 0 {
						queue = append(queue, name)
						sort.Strings(queue) // keep deterministic
					}
				}
			}
		}
	}

	if len(sorted) != len(g.modules) {
		return nil, fmt.Errorf("dependency cycle detected: processed %d of %d modules", len(sorted), len(g.modules))
	}

	return sorted, nil
}

// DependenciesOf returns the direct dependencies of a module.
func (g *DependencyGraph) DependenciesOf(module string) []string {
	return g.edges[module]
}

// TransitiveDependencies returns all transitive dependencies of a module (not including itself).
func (g *DependencyGraph) TransitiveDependencies(module string) []string {
	visited := make(map[string]bool)
	g.collectDeps(module, visited)
	delete(visited, module)

	result := make([]string, 0, len(visited))
	for dep := range visited {
		result = append(result, dep)
	}
	sort.Strings(result)
	return result
}

func (g *DependencyGraph) collectDeps(module string, visited map[string]bool) {
	if visited[module] {
		return
	}
	visited[module] = true
	for _, dep := range g.edges[module] {
		if _, ok := g.modules[dep]; ok {
			g.collectDeps(dep, visited)
		}
	}
}

// detectCycle uses DFS to find a cycle. Returns the cycle path or nil.
func (g *DependencyGraph) detectCycle() []string {
	const (
		white = 0 // unvisited
		gray  = 1 // in current path
		black = 2 // fully processed
	)

	color := make(map[string]int, len(g.modules))
	parent := make(map[string]string, len(g.modules))

	// Sort module names for deterministic cycle detection
	names := make([]string, 0, len(g.modules))
	for name := range g.modules {
		names = append(names, name)
	}
	sort.Strings(names)

	var dfs func(node string) []string
	dfs = func(node string) []string {
		color[node] = gray

		for _, dep := range g.edges[node] {
			if _, ok := g.modules[dep]; !ok {
				continue // skip missing modules (caught by Validate)
			}
			if color[dep] == gray {
				// Found cycle — reconstruct path
				cycle := []string{dep, node}
				cur := node
				for cur != dep {
					cur = parent[cur]
					cycle = append(cycle, cur)
				}
				// Reverse to get forward order
				for i, j := 0, len(cycle)-1; i < j; i, j = i+1, j-1 {
					cycle[i], cycle[j] = cycle[j], cycle[i]
				}
				return cycle
			}
			if color[dep] == white {
				parent[dep] = node
				if cycle := dfs(dep); cycle != nil {
					return cycle
				}
			}
		}

		color[node] = black
		return nil
	}

	for _, name := range names {
		if color[name] == white {
			if cycle := dfs(name); cycle != nil {
				return cycle
			}
		}
	}

	return nil
}
