package kitio

import (
	"bytes"
	"fmt"

	"gopkg.in/yaml.v3"
)

// ExportYAML produces a stackkit.yaml byte representation of the kit
// definition. The output is structurally equivalent to the original
// stackkit.yaml — same sections, same keys, same nesting — modulo
// cosmetic ordering and whitespace.
//
// Used by:
//   - stackkit kit export (writes to disk)
//   - stackkit kit roundtrip (compares against original)
//   - kit_export_test.go golden tests
func ExportYAML(def KitDefinition) ([]byte, error) {
	// Strip meta fields injected by import path so they don't leak into the
	// reconstructed YAML.
	clean := def
	clean.CueSourcePath = ""
	clean.ContractHash = ""

	// Default header fields if missing
	if clean.APIVersion == "" {
		clean.APIVersion = "stackkit/v1"
	}
	if clean.Kind == "" {
		clean.Kind = "StackKit"
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(clean); err != nil {
		return nil, fmt.Errorf("yaml.Encode kitdef: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("yaml encoder close: %w", err)
	}

	return buf.Bytes(), nil
}
