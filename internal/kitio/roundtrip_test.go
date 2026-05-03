package kitio

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestBaseKitLocalRoundTrip runs the in-memory roundtrip on the live
// base-kit/stackkit.yaml. Critical-field deltas fail the test; cosmetic
// drift (yaml comments, quote style) is acceptable.
//
// This is the main lossless guarantee for the kit-import / kit-export
// surface. It needs no DB or API — just the source-of-truth yaml file.
func TestBaseKitLocalRoundTrip(t *testing.T) {
	yamlBytes := readKitYAML(t, "base-kit")

	report, err := LocalRoundTrip(yamlBytes)
	if err != nil {
		t.Fatalf("LocalRoundTrip: %v", err)
	}

	t.Logf("slug=%s hashes_equal=%t cosmetic_only=%t diffs=%d",
		report.Slug, report.HashesEqual, report.CosmeticOnly, len(report.Differences))

	for _, d := range report.Differences {
		if d.Severity == "critical" {
			t.Errorf("CRITICAL diff at %s: %v -> %v   (note: %s)",
				d.Path, d.Original, d.Reconstructed, d.Note)
		} else {
			t.Logf("cosmetic diff at %s: %v -> %v", d.Path, d.Original, d.Reconstructed)
		}
	}

	if !report.CosmeticOnly {
		t.Errorf("base-kit roundtrip has %d critical differences", criticalCountTest(report.Differences))
	}
}

// TestBaseKitExportYAMLProducesValidYAML asserts the reconstructed yaml
// re-imports without error. Catches yaml encoder bugs that would silently
// produce malformed output.
func TestBaseKitExportYAMLProducesValidYAML(t *testing.T) {
	yamlBytes := readKitYAML(t, "base-kit")

	def, err := Import(yamlBytes)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	out, err := ExportYAML(def)
	if err != nil {
		t.Fatalf("ExportYAML: %v", err)
	}
	if _, err := Import(out); err != nil {
		t.Fatalf("re-import of exported yaml: %v", err)
	}

	wantSubstrings := []string{
		"name: base-kit",
		"version: 6.0.0-draft",
		"simple",
		"advanced",
		"computeTiers",
		"application",
	}
	for _, want := range wantSubstrings {
		if !contains(out, want) {
			t.Errorf("exported yaml missing substring %q", want)
		}
	}
}

// TestBaseKitExportCUEEvaluates checks that the CUE renderer produces
// well-formed CUE.
func TestBaseKitExportCUEEvaluates(t *testing.T) {
	yamlBytes := readKitYAML(t, "base-kit")
	def, err := Import(yamlBytes)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	stackfile, services, err := ExportCUE(def)
	if err != nil {
		t.Fatalf("ExportCUE: %v", err)
	}
	if len(stackfile) < 100 {
		t.Errorf("stackfile.cue too small (%d bytes)", len(stackfile))
	}
	if len(services) < 100 {
		t.Errorf("services.cue too small (%d bytes)", len(services))
	}

	wantInStackfile := []string{
		`package base_kit`,
		`#KitMetadata`,
		`#SupportedOS`,
		`#Features`,
		`#Modes`,
		`#ComputeTiers`,
		`name:        "base-kit"`,
	}
	for _, w := range wantInStackfile {
		if !contains(stackfile, w) {
			t.Errorf("stackfile.cue missing %q", w)
		}
	}
	wantInServices := []string{
		`package base_kit`,
		`#Foundation`,
		`#Platform`,
		`#Application`,
	}
	for _, w := range wantInServices {
		if !contains(services, w) {
			t.Errorf("services.cue missing %q", w)
		}
	}
}

// TestBaseKitExportTerraformAndCompose runs the kit-level wrappers.
func TestBaseKitExportTerraformAndCompose(t *testing.T) {
	yamlBytes := readKitYAML(t, "base-kit")
	def, err := Import(yamlBytes)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	dir := t.TempDir()
	if err := ExportTerraform(def, filepath.Join(dir, "deploy")); err != nil {
		t.Fatalf("ExportTerraform: %v", err)
	}
	tfvarsBytes, err := os.ReadFile(filepath.Join(dir, "deploy", "kit.tfvars.json"))
	if err != nil {
		t.Fatalf("read tfvars: %v", err)
	}
	for _, w := range []string{`"kit_name": "base-kit"`, `"kit_version": "6.0.0-draft"`, `"compute_tiers"`, `"features"`} {
		if !contains(tfvarsBytes, w) {
			t.Errorf("kit.tfvars.json missing %q", w)
		}
	}

	if err := ExportCompose(def, filepath.Join(dir, "compose")); err != nil {
		t.Fatalf("ExportCompose: %v", err)
	}
	composeBytes, err := os.ReadFile(filepath.Join(dir, "compose", "kit-overview.compose.yml"))
	if err != nil {
		t.Fatalf("read compose overview: %v", err)
	}
	for _, w := range []string{`x-kit: base-kit`, `services:`, `stackkit.kit: base-kit`} {
		if !contains(composeBytes, w) {
			t.Errorf("kit-overview.compose.yml missing %q", w)
		}
	}
}

func readKitYAML(t *testing.T, kit string) []byte {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	repoRoot := filepath.Dir(filepath.Dir(filepath.Dir(file)))
	path := filepath.Join(repoRoot, kit, "stackkit.yaml")
	bytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return bytes
}

func contains(haystack []byte, needle string) bool {
	if needle == "" {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		match := true
		for j := 0; j < len(needle); j++ {
			if haystack[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func criticalCountTest(diffs []FieldDifference) int {
	n := 0
	for _, d := range diffs {
		if d.Severity == "critical" {
			n++
		}
	}
	return n
}
