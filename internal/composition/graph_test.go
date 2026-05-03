package composition

import (
	"testing"

	cueval "github.com/kombifyio/stackkits/internal/cue"
)

func TestBuildGraph(t *testing.T) {
	contracts := sampleContracts()
	g := BuildGraph(contracts)

	if len(g.modules) != 4 {
		t.Errorf("expected 4 modules, got %d", len(g.modules))
	}
}

func TestTopologicalSort(t *testing.T) {
	contracts := sampleContracts()
	g := BuildGraph(contracts)

	sorted, err := g.TopologicalSort()
	if err != nil {
		t.Fatalf("TopologicalSort failed: %v", err)
	}

	// socket-proxy must come before traefik
	// traefik must come before tinyauth and dashboard
	indexOf := func(name string) int {
		for i, n := range sorted {
			if n == name {
				return i
			}
		}
		return -1
	}

	if indexOf("socket-proxy") > indexOf("traefik") {
		t.Errorf("socket-proxy should come before traefik, got order: %v", sorted)
	}
	if indexOf("traefik") > indexOf("tinyauth") {
		t.Errorf("traefik should come before tinyauth, got order: %v", sorted)
	}
	if indexOf("traefik") > indexOf("dashboard") {
		t.Errorf("traefik should come before dashboard, got order: %v", sorted)
	}
}

func TestValidate_OK(t *testing.T) {
	contracts := sampleContracts()
	g := BuildGraph(contracts)

	errs := g.Validate()
	if len(errs) != 0 {
		t.Errorf("expected no validation errors, got %v", errs)
	}
}

func TestValidate_MissingModule(t *testing.T) {
	contracts := []cueval.ModuleContract{
		{
			Metadata: cueval.ModuleMetadata{Name: "webapp"},
			Requires: &cueval.RequiresSpec{
				Services: map[string]cueval.RequiredService{
					"database": {Provides: []string{"sql"}},
				},
			},
		},
	}

	g := BuildGraph(contracts)
	errs := g.Validate()

	if len(errs) == 0 {
		t.Fatal("expected validation error for missing module")
	}
	if errs[0].Module != "webapp" {
		t.Errorf("expected error for webapp, got %s", errs[0].Module)
	}
}

func TestValidate_MissingCapability(t *testing.T) {
	contracts := []cueval.ModuleContract{
		{
			Metadata: cueval.ModuleMetadata{Name: "proxy"},
			Provides: &cueval.ProvidesSpec{
				Capabilities: map[string]bool{"http": true},
			},
		},
		{
			Metadata: cueval.ModuleMetadata{Name: "app"},
			Requires: &cueval.RequiresSpec{
				Services: map[string]cueval.RequiredService{
					"proxy": {Provides: []string{"grpc"}},
				},
			},
		},
	}

	g := BuildGraph(contracts)
	errs := g.Validate()

	if len(errs) == 0 {
		t.Fatal("expected validation error for missing capability")
	}
}

func TestDetectCycle(t *testing.T) {
	contracts := []cueval.ModuleContract{
		{
			Metadata: cueval.ModuleMetadata{Name: "a"},
			Requires: &cueval.RequiresSpec{
				Services: map[string]cueval.RequiredService{
					"b": {},
				},
			},
		},
		{
			Metadata: cueval.ModuleMetadata{Name: "b"},
			Requires: &cueval.RequiresSpec{
				Services: map[string]cueval.RequiredService{
					"a": {},
				},
			},
		},
	}

	g := BuildGraph(contracts)

	_, err := g.TopologicalSort()
	if err == nil {
		t.Fatal("expected cycle detection error")
	}

	errs := g.Validate()
	hasCycleErr := false
	for _, e := range errs {
		if e.Message != "" {
			hasCycleErr = true
		}
	}
	if !hasCycleErr {
		t.Error("expected cycle validation error")
	}
}

func TestDependenciesOf(t *testing.T) {
	contracts := sampleContracts()
	g := BuildGraph(contracts)

	deps := g.DependenciesOf("tinyauth")
	if len(deps) != 2 {
		t.Errorf("expected 2 dependencies for tinyauth, got %d: %v", len(deps), deps)
	}

	deps = g.DependenciesOf("socket-proxy")
	if len(deps) != 0 {
		t.Errorf("expected 0 dependencies for socket-proxy, got %d", len(deps))
	}
}

func TestTransitiveDependencies(t *testing.T) {
	contracts := sampleContracts()
	g := BuildGraph(contracts)

	trans := g.TransitiveDependencies("tinyauth")
	// tinyauth → traefik → socket-proxy
	if len(trans) != 2 {
		t.Errorf("expected 2 transitive deps for tinyauth, got %d: %v", len(trans), trans)
	}
}

func sampleContracts() []cueval.ModuleContract {
	return []cueval.ModuleContract{
		{
			Metadata: cueval.ModuleMetadata{Name: "socket-proxy"},
			Provides: &cueval.ProvidesSpec{
				Capabilities: map[string]bool{"docker-api-proxy": true},
			},
		},
		{
			Metadata: cueval.ModuleMetadata{Name: "traefik"},
			Requires: &cueval.RequiresSpec{
				Services: map[string]cueval.RequiredService{
					"socket-proxy": {Provides: []string{"docker-api-proxy"}},
				},
			},
			Provides: &cueval.ProvidesSpec{
				Capabilities: map[string]bool{"reverse-proxy": true, "forwardauth-host": true},
			},
		},
		{
			Metadata: cueval.ModuleMetadata{Name: "tinyauth"},
			Requires: &cueval.RequiresSpec{
				Services: map[string]cueval.RequiredService{
					"traefik":      {Provides: []string{"reverse-proxy", "forwardauth-host"}},
					"socket-proxy": {Provides: []string{"docker-api-proxy"}},
				},
			},
			Provides: &cueval.ProvidesSpec{
				Capabilities: map[string]bool{"forwardauth": true, "authentication": true},
			},
		},
		{
			Metadata: cueval.ModuleMetadata{Name: "dashboard"},
			Requires: &cueval.RequiresSpec{
				Services: map[string]cueval.RequiredService{
					"traefik": {Provides: []string{"reverse-proxy"}},
				},
			},
		},
	}
}
