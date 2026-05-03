package kitio

import (
	"reflect"
	"testing"
)

// Property tests: invariants that must hold across the full kit corpus,
// independently of which specific yaml fields are populated.

// TestRoundTripIsIdempotent: applying Import → Export → Import twice must
// land at the same KitDefinition. If the second cycle changes anything, the
// reverse path is not stable.
func TestRoundTripIsIdempotent(t *testing.T) {
	for _, kit := range allKits {
		t.Run(kit, func(t *testing.T) {
			yamlBytes := readKitYAML(t, kit)

			// First cycle
			defA, err := Import(yamlBytes)
			if err != nil {
				t.Fatalf("first import: %v", err)
			}
			outA, err := ExportYAML(defA)
			if err != nil {
				t.Fatalf("first export: %v", err)
			}

			// Second cycle
			defB, err := Import(outA)
			if err != nil {
				t.Fatalf("second import: %v", err)
			}
			outB, err := ExportYAML(defB)
			if err != nil {
				t.Fatalf("second export: %v", err)
			}

			// Third cycle
			defC, err := Import(outB)
			if err != nil {
				t.Fatalf("third import: %v", err)
			}

			// outA == outB (both pure-export from same struct)
			if !bytesEqual(outA, outB) {
				t.Errorf("[%s] export not idempotent: outA(%d bytes) != outB(%d bytes)",
					kit, len(outA), len(outB))
			}

			// defB == defC (struct equality after stable export form)
			if !structEqual(defB, defC) {
				t.Errorf("[%s] re-imported struct drifted between cycle 2 and 3", kit)
			}
		})
	}
}

// TestHashIsStableAcrossRuns: computing the canonical hash on the same
// KitDefinition value 50 times must produce the same result. Catches
// non-determinism in canonicalization (map iteration order, etc.).
func TestHashIsStableAcrossRuns(t *testing.T) {
	for _, kit := range allKits {
		t.Run(kit, func(t *testing.T) {
			def, err := Import(readKitYAML(t, kit))
			if err != nil {
				t.Fatalf("import: %v", err)
			}

			first, err := CanonicalHash(def)
			if err != nil {
				t.Fatalf("first hash: %v", err)
			}
			for i := 0; i < 50; i++ {
				h, err := CanonicalHash(def)
				if err != nil {
					t.Fatalf("hash run %d: %v", i, err)
				}
				if h != first {
					t.Errorf("[%s] hash unstable at run %d: first=%s now=%s",
						kit, i, first, h)
					return
				}
			}
		})
	}
}

// TestHashChangesOnFieldChange: mutating any critical field must change the
// hash. Catches over-aggressive canonicalization that loses signal.
func TestHashChangesOnFieldChange(t *testing.T) {
	def, err := Import(readKitYAML(t, "base-kit"))
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	original, err := CanonicalHash(def)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}

	mutations := []struct {
		name string
		mut  func(*KitDefinition)
	}{
		{"metadata.name", func(d *KitDefinition) { d.Metadata.Name = "different" }},
		{"metadata.version", func(d *KitDefinition) { d.Metadata.Version = "9.9.9" }},
		{"add supportedOS", func(d *KitDefinition) {
			d.SupportedOS = append([]string{}, d.SupportedOS...)
			d.SupportedOS = append(d.SupportedOS, "alpine-3")
		}},
		{"toggle feature flag", func(d *KitDefinition) {
			if d.Features == nil {
				d.Features = map[string]bool{}
			}
			d.Features["multiNode"] = !d.Features["multiNode"]
		}},
		{"change useCase role", func(d *KitDefinition) {
			if uc, ok := d.Application["media"]; ok {
				uc.Role = "alternative"
				d.Application["media"] = uc
			}
		}},
	}

	for _, m := range mutations {
		t.Run(m.name, func(t *testing.T) {
			mutated := def
			// Deep-copy maps that are mutated
			if mutated.Features != nil {
				cp := map[string]bool{}
				for k, v := range mutated.Features {
					cp[k] = v
				}
				mutated.Features = cp
			}
			if mutated.Application != nil {
				cp := map[string]ApplicationDef{}
				for k, v := range mutated.Application {
					cp[k] = v
				}
				mutated.Application = cp
			}
			m.mut(&mutated)

			h, err := CanonicalHash(mutated)
			if err != nil {
				t.Fatalf("mutated hash: %v", err)
			}
			if h == original {
				t.Errorf("mutation %s did NOT change hash; canonicalization too aggressive", m.name)
			}
		})
	}
}

// TestExportYAMLOrderIndependent: yaml field order in the source should
// not affect the resulting KitDefinition (already implicit in
// TestRoundTripIsIdempotent, but tested explicitly here on synthetic input).
func TestImportFieldOrderIndependent(t *testing.T) {
	yamlA := `apiVersion: stackkit/v1
kind: StackKit
metadata:
  name: order-test
  version: 1.0.0
supportedOS:
  - ubuntu-24
features:
  multiNode: true
modes:
  simple:
    engine: opentofu
`
	yamlB := `features:
  multiNode: true
modes:
  simple:
    engine: opentofu
supportedOS:
  - ubuntu-24
metadata:
  name: order-test
  version: 1.0.0
kind: StackKit
apiVersion: stackkit/v1
`
	defA, err := Import([]byte(yamlA))
	if err != nil {
		t.Fatalf("yamlA: %v", err)
	}
	defB, err := Import([]byte(yamlB))
	if err != nil {
		t.Fatalf("yamlB: %v", err)
	}

	hashA, _ := CanonicalHash(defA)
	hashB, _ := CanonicalHash(defB)
	if hashA != hashB {
		t.Errorf("hash differs by yaml field order: A=%s B=%s", hashA, hashB)
	}
}

// TestEachKitHasUniqueHash: import each kit, compute hash, ensure all three
// hashes differ. Cheap sanity that we're not silently collapsing kits.
func TestEachKitHasUniqueHash(t *testing.T) {
	hashes := map[string]string{}
	for _, kit := range allKits {
		def, err := Import(readKitYAML(t, kit))
		if err != nil {
			t.Fatalf("import %s: %v", kit, err)
		}
		h, err := CanonicalHash(def)
		if err != nil {
			t.Fatalf("hash %s: %v", kit, err)
		}
		if existing, found := hashes[h]; found {
			t.Errorf("hash collision: kit %s and kit %s share hash %s", kit, existing, h)
		}
		hashes[h] = kit
	}
	if len(hashes) != len(allKits) {
		t.Errorf("expected %d distinct hashes; got %d", len(allKits), len(hashes))
	}
}

// bytesEqual compares two byte slices.
func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// structEqual compares two KitDefinition values via reflect.DeepEqual.
// Maps with empty entries can drift (nil vs empty map), so we normalize.
func structEqual(a, b KitDefinition) bool {
	return reflect.DeepEqual(normalizeForComparison(a), normalizeForComparison(b))
}

// normalizeForComparison turns nil maps/slices into empty ones so that
// reflect.DeepEqual considers them equal.
func normalizeForComparison(d KitDefinition) KitDefinition {
	if d.SupportedOS == nil {
		d.SupportedOS = []string{}
	}
	if d.TunnelOptions == nil {
		d.TunnelOptions = []string{}
	}
	if d.Features == nil {
		d.Features = map[string]bool{}
	}
	if d.Application == nil {
		d.Application = map[string]ApplicationDef{}
	}
	if d.Foundation == nil {
		d.Foundation = map[string]FoundationDef{}
	}
	if d.Platform.AsMap == nil {
		d.Platform.AsMap = map[string]PlatformDef{}
	}
	if d.Modes == nil {
		d.Modes = map[string]ModeDef{}
	}
	if d.ComputeTiers == nil {
		d.ComputeTiers = map[string]ComputeTierDef{}
	}
	if d.Variants == nil {
		d.Variants = map[string]VariantDef{}
	}
	if d.NodeTypes == nil {
		d.NodeTypes = map[string]NodeTypeDef{}
	}
	if d.Services == nil {
		d.Services = []ServiceSpecDef{}
	}
	if d.Changelog == nil {
		d.Changelog = []ChangelogEntry{}
	}
	if d.Addons.AutoActivated == nil {
		d.Addons.AutoActivated = []string{}
	}
	if d.Addons.Optional == nil {
		d.Addons.Optional = []string{}
	}
	return d
}
