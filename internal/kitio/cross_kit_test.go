package kitio

import (
	"testing"
)

// allKits is the set of live kit slugs in the repo. Each must roundtrip
// losslessly through the kitio library — that's the validation the user
// asked for before going live.
var allKits = []string{"base-kit", "modern-homelab", "ha-kit"}

// TestAllKitsLocalRoundTrip is the comprehensive coverage gate: every
// production kit must complete the in-memory yaml -> KitDef -> yaml roundtrip
// without a critical-field diff. If a new kit is added to the repo, add its
// slug above and the test surface picks it up automatically.
func TestAllKitsLocalRoundTrip(t *testing.T) {
	for _, kit := range allKits {
		t.Run(kit, func(t *testing.T) {
			yamlBytes := readKitYAML(t, kit)

			report, err := LocalRoundTrip(yamlBytes)
			if err != nil {
				t.Fatalf("LocalRoundTrip %s: %v", kit, err)
			}

			t.Logf("kit=%s hashes_equal=%t cosmetic_only=%t diffs=%d",
				report.Slug, report.HashesEqual, report.CosmeticOnly, len(report.Differences))

			critical := 0
			for _, d := range report.Differences {
				if d.Severity == "critical" {
					t.Errorf("[%s] CRITICAL diff at %s: %v -> %v   (%s)",
						kit, d.Path, d.Original, d.Reconstructed, d.Note)
					critical++
				}
			}
			if critical > 0 {
				t.Fatalf("kit %s: %d critical differences in roundtrip", kit, critical)
			}
		})
	}
}

// TestAllKitsExportAllFormats stresses the four output formats for every
// production kit. Catches kit-specific edge cases (HA's swarm config, modern's
// nodeTypes, etc.) early.
func TestAllKitsExportAllFormats(t *testing.T) {
	for _, kit := range allKits {
		t.Run(kit, func(t *testing.T) {
			yamlBytes := readKitYAML(t, kit)
			def, err := Import(yamlBytes)
			if err != nil {
				t.Fatalf("import %s: %v", kit, err)
			}

			// YAML
			out, err := ExportYAML(def)
			if err != nil {
				t.Errorf("[%s] ExportYAML: %v", kit, err)
			}
			if len(out) < 100 {
				t.Errorf("[%s] ExportYAML output too small: %d bytes", kit, len(out))
			}
			if _, err := Import(out); err != nil {
				t.Errorf("[%s] re-import exported yaml: %v", kit, err)
			}

			// CUE
			stackfile, services, err := ExportCUE(def)
			if err != nil {
				t.Errorf("[%s] ExportCUE: %v", kit, err)
			}
			if len(stackfile) < 50 || len(services) < 50 {
				t.Errorf("[%s] CUE output too small: stackfile=%d services=%d",
					kit, len(stackfile), len(services))
			}

			// Terraform
			dir := t.TempDir()
			if err := ExportTerraform(def, dir); err != nil {
				t.Errorf("[%s] ExportTerraform: %v", kit, err)
			}

			// Compose
			if err := ExportCompose(def, dir); err != nil {
				t.Errorf("[%s] ExportCompose: %v", kit, err)
			}
		})
	}
}

// TestKitSpecificFields asserts the kit-type-specific features land in the
// right struct slots after import. Catches regressions where YAML changes
// to a kit silently land in metadata or get dropped.
func TestKitSpecificFields(t *testing.T) {
	tests := []struct {
		kit  string
		want func(t *testing.T, def KitDefinition)
	}{
		{"base-kit", func(t *testing.T, def KitDefinition) {
			if len(def.Application) == 0 {
				t.Errorf("base-kit: expected application populated, got 0")
			}
			if len(def.Foundation) == 0 {
				t.Errorf("base-kit: expected foundation populated, got 0")
			}
			if def.Platform.AsString != "" {
				t.Errorf("base-kit: platform should be a map, got string %q", def.Platform.AsString)
			}
			if len(def.Platform.AsMap) == 0 {
				t.Errorf("base-kit: expected platform map populated, got 0")
			}
			if len(def.ComputeTiers) == 0 {
				t.Errorf("base-kit: expected computeTiers populated, got 0")
			}
		}},
		{"modern-homelab", func(t *testing.T, def KitDefinition) {
			if def.Platform.AsString != "docker" {
				t.Errorf("modern-homelab: platform should be string 'docker', got %q + %v",
					def.Platform.AsString, def.Platform.AsMap)
			}
			if len(def.NodeTypes) == 0 {
				t.Errorf("modern-homelab: expected nodeTypes populated, got 0")
			}
			if len(def.Identity) == 0 {
				t.Errorf("modern-homelab: expected identity populated, got 0")
			}
			if len(def.Addons.Optional) == 0 && len(def.Addons.AutoActivated) == 0 {
				t.Errorf("modern-homelab: expected addons populated, got 0")
			}
		}},
		{"ha-kit", func(t *testing.T, def KitDefinition) {
			if def.Extends == "" {
				t.Errorf("ha-kit: expected extends to be set")
			}
			if def.Platform.AsString != "docker" {
				t.Errorf("ha-kit: platform should be string 'docker', got %q + %v",
					def.Platform.AsString, def.Platform.AsMap)
			}
			if len(def.Swarm) == 0 {
				t.Errorf("ha-kit: expected swarm populated, got 0")
			}
			if len(def.Variants) == 0 {
				t.Errorf("ha-kit: expected variants populated, got 0")
			}
			if len(def.Services) == 0 {
				t.Errorf("ha-kit: expected services populated, got 0")
			}
		}},
	}

	for _, tc := range tests {
		t.Run(tc.kit, func(t *testing.T) {
			def, err := Import(readKitYAML(t, tc.kit))
			if err != nil {
				t.Fatalf("import: %v", err)
			}
			tc.want(t, def)
		})
	}
}
