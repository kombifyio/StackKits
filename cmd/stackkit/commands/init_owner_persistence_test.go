package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kombifyio/stackkits/internal/config"
	"github.com/kombifyio/stackkits/pkg/models"
)

// TestStackSpec_OwnerYAMLRoundTrip verifies that the Owner block survives a
// SaveStackSpec → LoadStackSpec round-trip. Without this, `stackkit init`
// could silently drop the owner data, leaving `stackkit apply` with nothing
// to bootstrap (the Phase-1 final-review #3.1 stopper).
func TestStackSpec_OwnerYAMLRoundTrip(t *testing.T) {
	dir := t.TempDir()
	loader := config.NewLoader(dir)

	want := &models.StackSpec{
		Name:     "test-homelab",
		StackKit: "base-kit",
		Mode:     "simple",
		Domain:   "homelab.local",
		Owner: models.OwnerConfig{
			Source:                 "local",
			Email:                  "owner@example.com",
			Username:               "owner",
			DisplayName:            "Test Owner",
			RecoveryPassphraseHash: "$argon2id$v=19$m=65536,t=3,p=4$dGVzdHNhbHQ$dGVzdGhhc2g",
		},
	}

	specPath := filepath.Join(dir, "stack-spec.yaml")
	if err := loader.SaveStackSpec(want, specPath); err != nil {
		t.Fatalf("SaveStackSpec: %v", err)
	}

	got, err := loader.LoadStackSpec(specPath)
	if err != nil {
		t.Fatalf("LoadStackSpec: %v", err)
	}

	if got.Owner.Source != want.Owner.Source {
		t.Errorf("Owner.Source: got %q, want %q", got.Owner.Source, want.Owner.Source)
	}
	if got.Owner.Email != want.Owner.Email {
		t.Errorf("Owner.Email: got %q, want %q", got.Owner.Email, want.Owner.Email)
	}
	if got.Owner.Username != want.Owner.Username {
		t.Errorf("Owner.Username: got %q, want %q", got.Owner.Username, want.Owner.Username)
	}
	if got.Owner.DisplayName != want.Owner.DisplayName {
		t.Errorf("Owner.DisplayName: got %q, want %q", got.Owner.DisplayName, want.Owner.DisplayName)
	}
	if got.Owner.RecoveryPassphraseHash != want.Owner.RecoveryPassphraseHash {
		t.Errorf("Owner.RecoveryPassphraseHash: got %q, want %q",
			got.Owner.RecoveryPassphraseHash, want.Owner.RecoveryPassphraseHash)
	}

	// Spot-check the YAML on disk uses the documented field names — if these
	// drift, the spec stops being human-readable for ops who edit it manually.
	raw, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("read spec file: %v", err)
	}
	yamlStr := string(raw)
	for _, want := range []string{
		"owner:",
		"source: local",
		"email: owner@example.com",
		"username: owner",
		"recoveryPassphraseHash:",
	} {
		if !strings.Contains(yamlStr, want) {
			t.Errorf("YAML missing %q. Full content:\n%s", want, yamlStr)
		}
	}
}

// TestStackSpec_OwnerOmitEmpty confirms that older specs (without owner data)
// don't pick up an empty owner block on save. This matters because operators
// who upgrade to a Phase-1-aware binary but never opt into owner provisioning
// shouldn't see noisy diffs in their stack-spec.yaml.
func TestStackSpec_OwnerOmitEmpty(t *testing.T) {
	dir := t.TempDir()
	loader := config.NewLoader(dir)

	spec := &models.StackSpec{
		Name:     "no-owner-homelab",
		StackKit: "base-kit",
		Mode:     "simple",
		Domain:   "homelab.local",
		// No Owner block — every field zero-value.
	}

	specPath := filepath.Join(dir, "stack-spec.yaml")
	if err := loader.SaveStackSpec(spec, specPath); err != nil {
		t.Fatalf("SaveStackSpec: %v", err)
	}

	raw, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("read spec file: %v", err)
	}
	yamlStr := string(raw)
	if strings.Contains(yamlStr, "owner:") {
		t.Errorf("zero-value Owner should be omitted from YAML; got:\n%s", yamlStr)
	}
}
