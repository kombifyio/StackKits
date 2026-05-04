package cue

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	"cuelang.org/go/cue/load"
)

// TestCUEGoConsistencyGate validates that all CUE module contracts load correctly
// into Go structs and satisfy structural invariants required by the generator
// and resolver pipelines.
func TestCUEGoConsistencyGate(t *testing.T) {
	modulesDir := filepath.Join("..", "..", "modules")
	if _, err := os.Stat(modulesDir); os.IsNotExist(err) {
		t.Skipf("modules directory not found: %s", modulesDir)
	}

	reader := NewModuleReader()
	contracts, err := reader.ReadAllModules(modulesDir)
	if err != nil {
		t.Fatalf("ReadAllModules failed: %v", err)
	}

	if len(contracts) == 0 {
		t.Fatal("no module contracts loaded")
	}

	t.Run("metadata_required_fields", func(t *testing.T) {
		for _, mc := range contracts {
			if mc.Metadata.Name == "" {
				t.Error("module has empty metadata.name")
			}
			if mc.Metadata.Version == "" {
				t.Errorf("module %q has empty metadata.version", mc.Metadata.Name)
			}
			if mc.Metadata.Layer == "" {
				t.Errorf("module %q has empty metadata.layer", mc.Metadata.Name)
			}
		}
	})

	t.Run("valid_layer_prefixes", func(t *testing.T) {
		validPrefixes := []string{"L1-", "L2-", "L3-"}
		for _, mc := range contracts {
			layer := mc.Metadata.Layer
			if layer == "" {
				continue // caught above
			}
			valid := false
			for _, pfx := range validPrefixes {
				if strings.HasPrefix(layer, pfx) {
					valid = true
					break
				}
			}
			if !valid {
				t.Errorf("module %q has invalid layer %q (must start with L1-, L2-, or L3-)", mc.Metadata.Name, layer)
			}
		}
	})

	t.Run("unique_module_names", func(t *testing.T) {
		seen := make(map[string]int)
		for _, mc := range contracts {
			seen[mc.Metadata.Name]++
		}
		for name, count := range seen {
			if count > 1 {
				t.Errorf("duplicate module name %q (appears %d times)", name, count)
			}
		}
	})

	t.Run("unique_service_keys_across_modules", func(t *testing.T) {
		svcOwner := make(map[string]string)
		for _, mc := range contracts {
			for svcName := range mc.Services {
				if prev, exists := svcOwner[svcName]; exists {
					t.Errorf("service key %q defined by both %q and %q", svcName, prev, mc.Metadata.Name)
				}
				svcOwner[svcName] = mc.Metadata.Name
			}
		}
	})

	t.Run("services_have_image", func(t *testing.T) {
		for _, mc := range contracts {
			for svcName, svc := range mc.Services {
				if svc.Image == "" {
					t.Errorf("module %q service %q has no image", mc.Metadata.Name, svcName)
				}
			}
		}
	})

	t.Run("dependency_refs_exist", func(t *testing.T) {
		byName := ModulesByName(contracts)
		for _, mc := range contracts {
			if mc.Requires == nil {
				continue
			}
			for depName, dep := range mc.Requires.Services {
				if _, exists := byName[depName]; !exists && !dep.Optional {
					t.Errorf("module %q requires %q which does not exist in modules/", mc.Metadata.Name, depName)
				}
			}
		}
	})

	t.Run("no_self_dependency", func(t *testing.T) {
		for _, mc := range contracts {
			if mc.Requires == nil {
				continue
			}
			if _, selfRef := mc.Requires.Services[mc.Metadata.Name]; selfRef {
				t.Errorf("module %q depends on itself", mc.Metadata.Name)
			}
		}
	})

	t.Run("resolver_accepts_all_enabled", func(t *testing.T) {
		resolver := NewResolver()
		graph, err := resolver.Resolve(contracts)
		if err != nil {
			t.Fatalf("Resolver.Resolve failed on real contracts: %v", err)
		}
		if len(graph.Ordered) == 0 {
			t.Error("resolver produced empty ordering")
		}
		if len(graph.Layers) == 0 {
			t.Error("resolver produced no layers")
		}
		// Every enabled contract must appear in the graph
		for _, mc := range contracts {
			if !mc.Enabled {
				continue
			}
			if _, exists := graph.Modules[mc.Metadata.Name]; !exists {
				t.Errorf("enabled module %q missing from resolved graph", mc.Metadata.Name)
			}
		}
	})

	t.Run("generator_fields_present", func(t *testing.T) {
		// The generator consumes: Image, Ports, Volumes, Environment, Labels,
		// HealthCheck, Resources, TraefikRule/TraefikPort, RestartPolicy.
		// At minimum, every service must have Image set (checked above).
		// Validate port definitions are well-formed.
		for _, mc := range contracts {
			for svcName, svc := range mc.Services {
				for i, p := range svc.Ports {
					if p.Container <= 0 {
						t.Errorf("module %q service %q port[%d] has invalid container port %d",
							mc.Metadata.Name, svcName, i, p.Container)
					}
				}
				for i, v := range svc.Volumes {
					if v.Target == "" {
						t.Errorf("module %q service %q volume[%d] has empty target",
							mc.Metadata.Name, svcName, i)
					}
				}
				if svc.HealthCheck != nil {
					hc := svc.HealthCheck
					if hc.Port <= 0 && hc.Path != "" {
						t.Errorf("module %q service %q healthcheck has path but no port",
							mc.Metadata.Name, svcName)
					}
				}
			}
		}
	})

	t.Run("provisioner_fields_valid", func(t *testing.T) {
		for _, mc := range contracts {
			if mc.Provisioners == nil {
				continue
			}
			for provName, prov := range mc.Provisioners {
				if prov.Image == "" {
					t.Errorf("module %q provisioner %q has no image", mc.Metadata.Name, provName)
				}
				if prov.Command == "" {
					t.Errorf("module %q provisioner %q has no command", mc.Metadata.Name, provName)
				}
			}
		}
	})

	t.Run("generator_round_trip", func(t *testing.T) {
		// Verify the generator can produce output from the real module graph
		resolver := NewResolver()
		graph, err := resolver.Resolve(contracts)
		if err != nil {
			t.Skipf("resolver failed: %v", err)
		}

		dir := t.TempDir()
		gen := NewGenerator("test.local")
		if err := gen.GenerateAll(graph, dir); err != nil {
			t.Fatalf("GenerateAll failed on real module graph: %v", err)
		}

		// Verify key files exist
		for _, name := range []string{"providers.tf", "networks.tf", "variables.tf", "terraform.tfvars.json"} {
			path := filepath.Join(dir, name)
			if _, err := os.Stat(path); os.IsNotExist(err) {
				t.Errorf("expected file %s not generated", name)
			}
		}

		// Verify per-module .tf files are in the root module so OpenTofu loads them.
		for _, modName := range graph.Ordered {
			tfPath := filepath.Join(dir, modName+".tf")
			if _, err := os.Stat(tfPath); os.IsNotExist(err) {
				t.Errorf("module %q missing generated .tf file", modName)
			}
		}
	})
}

func TestModuleReaderExtractsNestedFlexibleSettings(t *testing.T) {
	modulesDir := filepath.Join("..", "..", "modules")
	if _, err := os.Stat(modulesDir); os.IsNotExist(err) {
		t.Skipf("modules directory not found: %s", modulesDir)
	}

	reader := NewModuleReader()

	agentContract, err := reader.ReadModule(filepath.Join(modulesDir, "monitoring-agent"))
	if err != nil {
		t.Fatalf("ReadModule(monitoring-agent) failed: %v", err)
	}
	agentCollector, ok := agentContract.Settings.Flexible["collector"].(map[string]any)
	if !ok {
		t.Fatalf("monitoring-agent collector settings = %T, want map[string]any", agentContract.Settings.Flexible["collector"])
	}
	if agentCollector["endpoint"] != "techstack:4317" {
		t.Fatalf("monitoring-agent collector endpoint = %v, want techstack:4317", agentCollector["endpoint"])
	}
	if agentCollector["collectionInterval"] != "30s" {
		t.Fatalf("monitoring-agent collector collectionInterval = %v, want 30s", agentCollector["collectionInterval"])
	}

	coreContract, err := reader.ReadModule(filepath.Join(modulesDir, "monitoring-core"))
	if err != nil {
		t.Fatalf("ReadModule(monitoring-core) failed: %v", err)
	}
	backend, ok := coreContract.Settings.Flexible["backend"].(map[string]any)
	if !ok {
		t.Fatalf("monitoring-core backend settings = %T, want map[string]any", coreContract.Settings.Flexible["backend"])
	}
	if backend["retentionPeriod"] != "30d" {
		t.Fatalf("monitoring-core backend retentionPeriod = %v, want 30d", backend["retentionPeriod"])
	}
	gateway, ok := coreContract.Settings.Flexible["gateway"].(map[string]any)
	if !ok {
		t.Fatalf("monitoring-core gateway settings = %T, want map[string]any", coreContract.Settings.Flexible["gateway"])
	}
	if gateway["batchTimeout"] != "15s" {
		t.Fatalf("monitoring-core gateway batchTimeout = %v, want 15s", gateway["batchTimeout"])
	}
}

func TestBaseStackKitExposesCanonicalObservabilitySurface(t *testing.T) {
	baseDir := filepath.Join("..", "..", "base")
	if _, err := os.Stat(baseDir); os.IsNotExist(err) {
		t.Skipf("base directory not found: %s", baseDir)
	}

	repoRoot := filepath.Join("..", "..")
	cfg := cueLoadConfig(baseDir, repoRoot)
	instances := load.Instances([]string{"."}, cfg)
	if len(instances) == 0 {
		t.Fatal("no CUE instances loaded for base package")
	}
	if instErr := instances[0].Err; instErr != nil {
		if strings.Contains(instErr.Error(), "cannot find package") {
			t.Skipf("CUE module resolution not available: %v", instErr)
		}
		t.Fatalf("failed to load base package: %v", instErr)
	}

	ctx := cuecontext.New()
	value := ctx.BuildInstance(instances[0])
	if err := value.Err(); err != nil {
		if strings.Contains(err.Error(), "cannot find package") {
			t.Skipf("CUE module resolution not available: %v", err)
		}
		t.Fatalf("failed to build base package: %v", err)
	}

	monitoring := value.LookupPath(cue.ParsePath("#BaseStackKit.observability.monitoring"))
	if !monitoring.Exists() {
		t.Fatal("#BaseStackKit.observability.monitoring does not exist")
	}
	if got, err := monitoring.LookupPath(cue.ParsePath("collector.endpoint")).String(); err != nil || got != "techstack:4317" {
		t.Fatalf("collector.endpoint = %q, %v, want techstack:4317", got, err)
	}
	if got, err := monitoring.LookupPath(cue.ParsePath("collector.collectionInterval")).String(); err != nil || got != "30s" {
		t.Fatalf("collector.collectionInterval = %q, %v, want 30s", got, err)
	}
	if got, err := monitoring.LookupPath(cue.ParsePath("gateway.memoryLimitMiB")).Int64(); err != nil || got != 256 {
		t.Fatalf("gateway.memoryLimitMiB = %d, %v, want 256", got, err)
	}
	if got, err := monitoring.LookupPath(cue.ParsePath("backend.retentionPeriod")).String(); err != nil || got != "30d" {
		t.Fatalf("backend.retentionPeriod = %q, %v, want 30d", got, err)
	}
	if got, err := monitoring.LookupPath(cue.ParsePath("signals.metrics")).Bool(); err != nil || !got {
		t.Fatalf("signals.metrics = %t, %v, want true", got, err)
	}

	coreContract, err := NewModuleReader().ReadModule(filepath.Join("..", "..", "modules", "monitoring-core"))
	if err != nil {
		t.Fatalf("ReadModule(monitoring-core) failed: %v", err)
	}
	gateway, ok := coreContract.Settings.Flexible["gateway"].(map[string]any)
	if !ok {
		t.Fatalf("monitoring-core gateway settings = %T, want map[string]any", coreContract.Settings.Flexible["gateway"])
	}
	if gateway["remoteWriteEndpoint"] != "http://victoriametrics:8428/api/v1/write" {
		t.Fatalf("monitoring-core gateway remoteWriteEndpoint = %v, want http://victoriametrics:8428/api/v1/write", gateway["remoteWriteEndpoint"])
	}
}
