package cue

import (
	"strings"
	"testing"
)

func makeContract(name, layer string, deps map[string]RequiredService) ModuleContract {
	mc := ModuleContract{
		Metadata: ModuleMetadata{Name: name, Layer: layer, Version: "1.0.0"},
		Services: map[string]ServiceDef{name: {Image: "test/" + name, Tag: "latest"}},
		Enabled:  true,
	}
	if len(deps) > 0 {
		mc.Requires = &RequiresSpec{Services: deps}
	}
	return mc
}

func dep(optional bool) RequiredService {
	return RequiredService{Optional: optional}
}

func TestResolverBasicOrdering(t *testing.T) {
	resolver := NewResolver()
	contracts := []ModuleContract{
		makeContract("traefik", "l2-platform", map[string]RequiredService{
			"socket-proxy": dep(false),
		}),
		makeContract("socket-proxy", "l1-foundation", nil),
		makeContract("whoami", "l3-application", map[string]RequiredService{
			"traefik": dep(false),
		}),
	}

	graph, err := resolver.Resolve(contracts)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	if len(graph.Ordered) != 3 {
		t.Fatalf("expected 3 modules, got %d", len(graph.Ordered))
	}

	// socket-proxy must come before traefik, traefik before whoami
	idx := make(map[string]int)
	for i, name := range graph.Ordered {
		idx[name] = i
	}

	if idx["socket-proxy"] >= idx["traefik"] {
		t.Errorf("socket-proxy (%d) should come before traefik (%d)", idx["socket-proxy"], idx["traefik"])
	}
	if idx["traefik"] >= idx["whoami"] {
		t.Errorf("traefik (%d) should come before whoami (%d)", idx["traefik"], idx["whoami"])
	}
}

func TestResolverDisabledModules(t *testing.T) {
	resolver := NewResolver()

	sp := makeContract("socket-proxy", "l1-foundation", nil)
	traefik := makeContract("traefik", "l2-platform", map[string]RequiredService{
		"socket-proxy": dep(false),
	})
	traefik.Enabled = false

	whoami := makeContract("whoami", "l3-application", map[string]RequiredService{
		"traefik": dep(true), // optional dep
	})

	contracts := []ModuleContract{sp, traefik, whoami}

	graph, err := resolver.Resolve(contracts)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	// traefik disabled, so only socket-proxy and whoami
	if len(graph.Ordered) != 2 {
		t.Fatalf("expected 2 enabled modules, got %d: %v", len(graph.Ordered), graph.Ordered)
	}

	if _, ok := graph.Modules["traefik"]; ok {
		t.Error("disabled traefik should not be in graph")
	}
}

func TestResolverMissingRequiredDep(t *testing.T) {
	resolver := NewResolver()

	traefik := makeContract("traefik", "l2-platform", map[string]RequiredService{
		"socket-proxy": dep(false), // required, but socket-proxy is disabled
	})
	sp := makeContract("socket-proxy", "l1-foundation", nil)
	sp.Enabled = false

	contracts := []ModuleContract{traefik, sp}

	_, err := resolver.Resolve(contracts)
	if err == nil {
		t.Fatal("expected error for missing required dependency")
	}
	if !strings.Contains(err.Error(), "socket-proxy") {
		t.Errorf("error should mention socket-proxy: %v", err)
	}
}

func TestResolverMissingNonexistentDep(t *testing.T) {
	resolver := NewResolver()

	traefik := makeContract("traefik", "l2-platform", map[string]RequiredService{
		"nonexistent": dep(false),
	})
	contracts := []ModuleContract{traefik}

	_, err := resolver.Resolve(contracts)
	if err == nil {
		t.Fatal("expected error for nonexistent dependency")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("error should mention 'does not exist': %v", err)
	}
}

func TestResolverCircularDep(t *testing.T) {
	resolver := NewResolver()
	contracts := []ModuleContract{
		makeContract("a", "l1", map[string]RequiredService{"b": dep(false)}),
		makeContract("b", "l1", map[string]RequiredService{"c": dep(false)}),
		makeContract("c", "l1", map[string]RequiredService{"a": dep(false)}),
	}

	_, err := resolver.Resolve(contracts)
	if err == nil {
		t.Fatal("expected error for circular dependency")
	}
	if !strings.Contains(err.Error(), "circular") {
		t.Errorf("error should mention 'circular': %v", err)
	}
}

func TestResolverLayerGrouping(t *testing.T) {
	resolver := NewResolver()
	contracts := []ModuleContract{
		makeContract("sp", "l1-foundation", nil),
		makeContract("traefik", "l2-platform", nil),
		makeContract("whoami", "l3-application", nil),
		makeContract("lldap", "l1-foundation", nil),
	}

	graph, err := resolver.Resolve(contracts)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	if len(graph.Layers["l1-foundation"]) != 2 {
		t.Errorf("l1-foundation should have 2 modules, got %d", len(graph.Layers["l1-foundation"]))
	}
	if len(graph.Layers["l2-platform"]) != 1 {
		t.Errorf("l2-platform should have 1 module, got %d", len(graph.Layers["l2-platform"]))
	}
	if len(graph.Layers["l3-application"]) != 1 {
		t.Errorf("l3-application should have 1 module, got %d", len(graph.Layers["l3-application"]))
	}
}

func TestResolverDeterministicOrder(t *testing.T) {
	resolver := NewResolver()

	// All independent — order should be alphabetical
	contracts := []ModuleContract{
		makeContract("zulu", "l1", nil),
		makeContract("alpha", "l1", nil),
		makeContract("mike", "l1", nil),
	}

	// Run multiple times to verify determinism
	var firstOrder []string
	for i := 0; i < 5; i++ {
		graph, err := resolver.Resolve(contracts)
		if err != nil {
			t.Fatalf("Resolve() error = %v", err)
		}
		if firstOrder == nil {
			firstOrder = graph.Ordered
		} else {
			for j, name := range graph.Ordered {
				if name != firstOrder[j] {
					t.Fatalf("non-deterministic order on iteration %d: got %v, want %v", i, graph.Ordered, firstOrder)
				}
			}
		}
	}

	// Should be alphabetical for independent modules
	if firstOrder[0] != "alpha" || firstOrder[1] != "mike" || firstOrder[2] != "zulu" {
		t.Errorf("independent modules should be alphabetically ordered, got %v", firstOrder)
	}
}

func TestDependenciesOf(t *testing.T) {
	resolver := NewResolver()
	contracts := []ModuleContract{
		makeContract("traefik", "l2", map[string]RequiredService{
			"socket-proxy": dep(false),
		}),
		makeContract("socket-proxy", "l1", nil),
		makeContract("tinyauth", "l2", map[string]RequiredService{
			"traefik":      dep(false),
			"socket-proxy": dep(true), // optional but present
		}),
	}

	graph, err := resolver.Resolve(contracts)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	deps := graph.DependenciesOf("tinyauth")
	if len(deps) != 2 {
		t.Fatalf("tinyauth should have 2 deps, got %d: %v", len(deps), deps)
	}

	// DependenciesOf for module with no deps
	deps = graph.DependenciesOf("socket-proxy")
	if len(deps) != 0 {
		t.Errorf("socket-proxy should have 0 deps, got %d", len(deps))
	}

	// DependenciesOf for unknown module
	deps = graph.DependenciesOf("nonexistent")
	if deps != nil {
		t.Errorf("nonexistent module should return nil deps, got %v", deps)
	}
}
