package kitio

import (
	"strings"
	"testing"
)

// Negative + edge-case tests for the import/export pipeline.
// These exercise the failure modes a CI run is likely to hit when somebody
// hand-edits a stackkit.yaml or the Admin API returns unexpected shapes.

func TestImportRejectsTotallyMalformedYAML(t *testing.T) {
	cases := []struct {
		name string
		yaml string
	}{
		{"unclosed flow map", "metadata: { name: foo, version:"},
		{"tab indent", "metadata:\n\tname: foo\n\tversion: 1.0.0"},
		{"binary garbage", "\x00\x01\x02\x03malformed"},
		{"bad anchor", "metadata: &x\n  name: foo\nbar: *missing"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Import([]byte(tc.yaml))
			if err == nil {
				t.Errorf("expected error for %s; got nil", tc.name)
			}
		})
	}
}

func TestImportToleratesEmptyDocument(t *testing.T) {
	// Empty document should produce zero-value KitDefinition without panicking.
	def, err := Import([]byte(""))
	if err != nil {
		t.Errorf("empty yaml unexpectedly errored: %v", err)
	}
	if def.Metadata.Name != "" {
		t.Errorf("expected zero metadata.name; got %q", def.Metadata.Name)
	}
}

func TestImportToleratesYAMLWithOnlyMetadata(t *testing.T) {
	yaml := `apiVersion: stackkit/v1
kind: StackKit
metadata:
  name: minimal-kit
  version: 0.1.0
`
	def, err := Import([]byte(yaml))
	if err != nil {
		t.Fatalf("import minimal kit: %v", err)
	}
	if def.Metadata.Name != "minimal-kit" {
		t.Errorf("expected name=minimal-kit; got %q", def.Metadata.Name)
	}

	// Export -> reimport should still work
	out, err := ExportYAML(def)
	if err != nil {
		t.Fatalf("export minimal kit: %v", err)
	}
	def2, err := Import(out)
	if err != nil {
		t.Fatalf("re-import minimal kit: %v", err)
	}
	if def2.Metadata.Name != "minimal-kit" {
		t.Errorf("re-import lost metadata.name")
	}
}

func TestImportNormalizesPlatformBothShapes(t *testing.T) {
	stringCase := `metadata:
  name: ha-kit
  version: 1.0.0
platform: docker
`
	mapCase := `metadata:
  name: base-kit
  version: 1.0.0
platform:
  traefik:
    role: default
`
	stringDef, err := Import([]byte(stringCase))
	if err != nil {
		t.Fatalf("string platform: %v", err)
	}
	if stringDef.Platform.AsString != "docker" {
		t.Errorf("string platform: want AsString=docker, got %q", stringDef.Platform.AsString)
	}
	if len(stringDef.Platform.AsMap) != 0 {
		t.Errorf("string platform: AsMap should be empty; got %v", stringDef.Platform.AsMap)
	}

	mapDef, err := Import([]byte(mapCase))
	if err != nil {
		t.Fatalf("map platform: %v", err)
	}
	if mapDef.Platform.AsString != "" {
		t.Errorf("map platform: AsString should be empty; got %q", mapDef.Platform.AsString)
	}
	if mapDef.Platform.AsMap["traefik"].Role != "default" {
		t.Errorf("map platform: lost traefik role")
	}
}

func TestExportYAMLDoesNotLeakMetaFields(t *testing.T) {
	// Meta fields injected by import (cueSourcePath, importedBy, ...) must not
	// appear in the exported YAML — they're DB-shape only.
	def, err := Import(readKitYAMLBytes(t, "base-kit"))
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	def.CueSourcePath = "secret-path"
	def.ImportedBy = "secret-user@example.com"
	def.ContractHash = "abc123"
	def.DryRun = true

	out, err := ExportYAML(def)
	if err != nil {
		t.Fatalf("export: %v", err)
	}

	leaked := []string{"cueSourcePath", "importedBy", "contractHash", "dryRun", "secret-path", "secret-user"}
	for _, l := range leaked {
		if strings.Contains(string(out), l) {
			t.Errorf("exported yaml leaked meta field %q", l)
		}
	}
}

func TestSpecialCharactersInDescription(t *testing.T) {
	// Descriptions with quotes, newlines, unicode must roundtrip.
	yaml := `metadata:
  name: special-kit
  version: 1.0.0
  description: |
    Multi-line description with "quotes" and 'apostrophes'.
    Unicode: äöü 中文 🚀.
    Backslashes: C:\Users\test
`
	def, err := Import([]byte(yaml))
	if err != nil {
		t.Fatalf("import special chars: %v", err)
	}
	if !strings.Contains(def.Metadata.Description, `"quotes"`) {
		t.Errorf("lost quotes in description")
	}
	if !strings.Contains(def.Metadata.Description, "中文") {
		t.Errorf("lost unicode in description")
	}

	out, err := ExportYAML(def)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	def2, err := Import(out)
	if err != nil {
		t.Fatalf("re-import: %v", err)
	}
	if def2.Metadata.Description != def.Metadata.Description {
		t.Errorf("description lost in roundtrip:\n  was: %q\n  got: %q",
			def.Metadata.Description, def2.Metadata.Description)
	}
}

func TestCRLFLineEndings(t *testing.T) {
	// Windows-style line endings should not break parsing.
	yaml := "metadata:\r\n  name: crlf-kit\r\n  version: 1.0.0\r\n"
	def, err := Import([]byte(yaml))
	if err != nil {
		t.Fatalf("import CRLF yaml: %v", err)
	}
	if def.Metadata.Name != "crlf-kit" {
		t.Errorf("CRLF: lost name; got %q", def.Metadata.Name)
	}
}

func TestUTF8BOMIsRejectedOrTolerated(t *testing.T) {
	// UTF-8 BOM (0xEF 0xBB 0xBF) at start of file. yaml.v3 doesn't strip it,
	// so we expect a clear error rather than silent corruption.
	bom := []byte{0xEF, 0xBB, 0xBF}
	yaml := string(bom) + "metadata:\n  name: bom-kit\n  version: 1.0.0\n"
	def, err := Import([]byte(yaml))
	if err != nil {
		// Tolerated — error returned, which is fine
		t.Logf("BOM rejected with: %v", err)
		return
	}
	// If accepted, name must be parseable
	if def.Metadata.Name != "bom-kit" {
		t.Errorf("BOM-prefixed yaml: name corrupted; got %q", def.Metadata.Name)
	}
}

func TestVeryLongFieldsRoundTrip(t *testing.T) {
	longDesc := strings.Repeat("x ", 5000) // ~10KB description
	def := KitDefinition{
		Metadata: KitMetadata{
			Name:        "long-kit",
			Version:     "1.0.0",
			Description: longDesc,
		},
	}
	out, err := ExportYAML(def)
	if err != nil {
		t.Fatalf("export long kit: %v", err)
	}
	def2, err := Import(out)
	if err != nil {
		t.Fatalf("re-import long kit: %v", err)
	}
	if def2.Metadata.Description != longDesc {
		t.Errorf("long description corrupted (got %d chars, want %d)",
			len(def2.Metadata.Description), len(longDesc))
	}
}

// readKitYAMLBytes is the package-internal helper (to avoid name clash with
// roundtrip_test.go's helper).
func readKitYAMLBytes(t *testing.T, kit string) []byte {
	t.Helper()
	return readKitYAML(t, kit)
}
