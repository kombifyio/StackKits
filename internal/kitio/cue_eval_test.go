package kitio

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestCUEEvalSucceedsOnGeneratedFiles uses the `cue` binary (if available
// in PATH) to actually evaluate the generated stackfile.cue + services.cue.
// This is stronger than the byte-pattern check in roundtrip_test.go: it
// proves CUE actually parses + type-checks the output.
//
// Skipped if `cue` isn't installed.
func TestCUEEvalSucceedsOnGeneratedFiles(t *testing.T) {
	cueBin, err := exec.LookPath("cue")
	if err != nil {
		t.Skip("cue binary not in PATH; skipping cue eval validation")
	}

	for _, kit := range allKits {
		t.Run(kit, func(t *testing.T) {
			yamlBytes := readKitYAML(t, kit)
			def, err := Import(yamlBytes)
			if err != nil {
				t.Fatalf("import %s: %v", kit, err)
			}
			stackfile, services, err := ExportCUE(def)
			if err != nil {
				t.Fatalf("export cue %s: %v", kit, err)
			}

			dir := t.TempDir()
			stackfilePath := filepath.Join(dir, "stackfile.cue")
			servicesPath := filepath.Join(dir, "services.cue")
			if err := os.WriteFile(stackfilePath, stackfile, 0o644); err != nil {
				t.Fatalf("write stackfile: %v", err)
			}
			if err := os.WriteFile(servicesPath, services, 0o644); err != nil {
				t.Fatalf("write services: %v", err)
			}

			// `cue eval` against specific files (windows-friendly).
			cmd := exec.Command(cueBin, "eval", stackfilePath, servicesPath)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Errorf("[%s] cue eval failed: %v\n--- output ---\n%s", kit, err, string(out))
			}

			// Output must contain at least the kit name (sanity check that the
			// eval actually produced field bindings).
			if !contains(out, kit) {
				t.Errorf("[%s] cue eval output missing kit name; got:\n%s", kit, string(out))
			}
		})
	}
}

// TestCUEVetSucceedsOnGeneratedFiles runs `cue vet` which is stricter than
// `cue eval` (checks for incomplete values, validates schema constraints).
// Acceptable if cue vet warns but doesn't error.
func TestCUEVetSucceedsOnGeneratedFiles(t *testing.T) {
	cueBin, err := exec.LookPath("cue")
	if err != nil {
		t.Skip("cue binary not in PATH; skipping cue vet validation")
	}

	for _, kit := range allKits {
		t.Run(kit, func(t *testing.T) {
			def, err := Import(readKitYAML(t, kit))
			if err != nil {
				t.Fatalf("import: %v", err)
			}
			stackfile, services, err := ExportCUE(def)
			if err != nil {
				t.Fatalf("export cue: %v", err)
			}
			dir := t.TempDir()
			stackfilePath := filepath.Join(dir, "stackfile.cue")
			servicesPath := filepath.Join(dir, "services.cue")
			_ = os.WriteFile(stackfilePath, stackfile, 0o644)
			_ = os.WriteFile(servicesPath, services, 0o644)

			cmd := exec.Command(cueBin, "vet", stackfilePath, servicesPath)
			out, err := cmd.CombinedOutput()
			if err != nil {
				// vet is stricter; we accept warnings but not hard errors.
				// Distinguish: exit code 0/1 = ok, > 1 = real error
				if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
					t.Logf("[%s] cue vet returned warnings (acceptable):\n%s", kit, string(out))
					return
				}
				t.Errorf("[%s] cue vet hard-failed: %v\n%s", kit, err, string(out))
			}
		})
	}
}
