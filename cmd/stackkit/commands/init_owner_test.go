package commands

import (
	"strings"
	"testing"

	"github.com/kombifyio/stackkits/internal/identity"
	"github.com/kombifyio/stackkits/pkg/models"
)

func TestResolveOwnerSpec_LocalNonInteractive(t *testing.T) {
	s, has, err := resolveOwnerSpec(ownerFlags{
		Source:      "local",
		Email:       "owner@example.com",
		Username:    "owner",
		DisplayName: "Example Owner",
	}, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if !has {
		t.Fatal("expected hasOwner=true when --owner-source=local provided")
	}
	if s.Source != "local" {
		t.Errorf("source %q", s.Source)
	}
	if s.Email != "owner@example.com" {
		t.Errorf("email %q", s.Email)
	}
	if s.Username != "owner" {
		t.Errorf("username %q", s.Username)
	}
	if s.DisplayName != "Example Owner" {
		t.Errorf("displayName %q", s.DisplayName)
	}
}

func TestResolveOwnerSpec_DisplayNameFallsBack(t *testing.T) {
	s, has, err := resolveOwnerSpec(ownerFlags{
		Source: "local", Email: "x@y.com", Username: "joe",
	}, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if !has {
		t.Fatal("expected hasOwner=true")
	}
	if s.DisplayName != "joe" {
		t.Errorf("displayName %q", s.DisplayName)
	}
}

func TestResolveOwnerSpec_CloudRequiresAutoBootstrap(t *testing.T) {
	_, has, err := resolveOwnerSpec(ownerFlags{Source: "cloud"}, nil, true)
	if err == nil || !strings.Contains(err.Error(), "owner-bootstrap-mode=auto") {
		t.Errorf("expected auto-bootstrap error, got: %v", err)
	}
	if has {
		t.Error("hasOwner must be false on error")
	}
}

func TestResolveOwnerSpec_InvalidSource(t *testing.T) {
	_, has, err := resolveOwnerSpec(ownerFlags{Source: "ldap"}, nil, true)
	if err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Errorf("expected invalid-source error, got: %v", err)
	}
	if has {
		t.Error("hasOwner must be false on error")
	}
}

func TestResolveOwnerSpec_NonInteractiveNoSourceIsSkip(t *testing.T) {
	// Missing --owner-source in non-interactive mode is the documented
	// "skip owner provisioning" path. It must NOT error — older specs and
	// users who don't want owner bootstrap need this.
	s, has, err := resolveOwnerSpec(ownerFlags{}, nil, true)
	if err != nil {
		t.Fatalf("expected no error for missing source in non-interactive: %v", err)
	}
	if has {
		t.Error("hasOwner must be false when no source provided")
	}
	if s != (identity.OwnerSpec{}) {
		t.Errorf("expected zero-value spec, got %+v", s)
	}
}

func TestResolveOwnerSpec_NonInteractiveMissingFields(t *testing.T) {
	// When --owner-source IS provided in non-interactive mode but required
	// fields are missing, that's a real misconfiguration and must error.
	cases := map[string]ownerFlags{
		"no email":    {Source: "local", Username: "x"},
		"no username": {Source: "local", Email: "x@y.com"},
	}
	for name, f := range cases {
		t.Run(name, func(t *testing.T) {
			_, has, err := resolveOwnerSpec(f, nil, true)
			if err == nil {
				t.Error("expected error in non-interactive mode with partial flags")
			}
			if has {
				t.Error("hasOwner must be false on error")
			}
		})
	}
}

func TestResolveOwnerSpec_SourceCaseInsensitive(t *testing.T) {
	// "LOCAL" should be normalized to "local" before validation; this
	// guards against pipelines that emit upper-case env vars.
	s, has, err := resolveOwnerSpec(ownerFlags{
		Source: "LOCAL", Email: "a@b.com", Username: "u",
	}, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if !has {
		t.Fatal("expected hasOwner=true")
	}
	if s.Source != "local" {
		t.Errorf("source %q", s.Source)
	}
}

func TestResolveOwnerSpec_TrimsWhitespace(t *testing.T) {
	s, has, err := resolveOwnerSpec(ownerFlags{
		Source:      "local",
		Email:       "  a@b.com  ",
		Username:    "  joe  ",
		DisplayName: "  Joe Friday  ",
	}, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if !has {
		t.Fatal("expected hasOwner=true")
	}
	if s.Email != "a@b.com" {
		t.Errorf("email %q (whitespace not trimmed)", s.Email)
	}
	if s.Username != "joe" {
		t.Errorf("username %q (whitespace not trimmed)", s.Username)
	}
	if s.DisplayName != "Joe Friday" {
		t.Errorf("displayName %q (whitespace not trimmed)", s.DisplayName)
	}
}

func TestResolveOwnerBootstrapConfig_AutoDoesNotRequireOwnerIdentityOrRecoveryHash(t *testing.T) {
	cfg, has, err := resolveOwnerBootstrapConfig(ownerFlags{
		BootstrapMode:       "auto",
		Source:              "cloud",
		RecoveryMaterialRef: "techstack://recovery/stacks/stack-123",
	}, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if !has {
		t.Fatal("expected hasOwner=true for explicit auto bootstrap")
	}
	if cfg.BootstrapMode != models.OwnerBootstrapModeAuto {
		t.Errorf("BootstrapMode %q", cfg.BootstrapMode)
	}
	if cfg.Source != models.OwnerSourceCloud {
		t.Errorf("Source %q", cfg.Source)
	}
	if cfg.Email != "" || cfg.Username != "" {
		t.Errorf("auto owner must not require or invent owner identity, got email=%q username=%q", cfg.Email, cfg.Username)
	}
	if cfg.RecoveryMaterialRef != "techstack://recovery/stacks/stack-123" {
		t.Errorf("RecoveryMaterialRef %q", cfg.RecoveryMaterialRef)
	}
}

func TestResolveOwnerBootstrapConfig_AutoRequiresRecoveryReferenceOrHash(t *testing.T) {
	_, has, err := resolveOwnerBootstrapConfig(ownerFlags{
		BootstrapMode: "auto",
		Source:        "cloud",
	}, nil, true)
	if err == nil || !strings.Contains(err.Error(), "recovery") {
		t.Fatalf("expected recovery material error, got has=%v err=%v", has, err)
	}
	if has {
		t.Error("hasOwner must be false on auto recovery validation error")
	}
}

func TestResolveOwnerBootstrapConfig_NoneIsExplicitNoop(t *testing.T) {
	cfg, has, err := resolveOwnerBootstrapConfig(ownerFlags{BootstrapMode: "none"}, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if !has {
		t.Fatal("expected hasOwner=true so explicit none persists")
	}
	if cfg.BootstrapMode != models.OwnerBootstrapModeNone {
		t.Errorf("BootstrapMode %q", cfg.BootstrapMode)
	}
	if cfg.Source != "" || cfg.Email != "" || cfg.Username != "" {
		t.Errorf("none lane must not carry owner fields: %+v", cfg)
	}
}

func TestResolveOwnerBootstrapConfig_CustomKeepsLegacyRequirements(t *testing.T) {
	_, has, err := resolveOwnerBootstrapConfig(ownerFlags{
		BootstrapMode: "custom",
		Source:        "local",
		Email:         "owner@example.com",
	}, nil, true)
	if err == nil || !strings.Contains(err.Error(), "--owner-username required") {
		t.Fatalf("expected missing username error, got has=%v err=%v", has, err)
	}
	if has {
		t.Error("hasOwner must be false on custom validation error")
	}
}

func TestResolveRecoveryPassphrase_FlagHash(t *testing.T) {
	// Valid PHC string passes through verbatim. Plaintext stays empty —
	// The apply path is responsible for reprompting when it needs
	// the actual symmetric key material.
	phc := "$argon2id$v=19$m=65536,t=3,p=4$dGVzdHNhbHQ$dGVzdGhhc2g"
	h, plain, err := resolveRecoveryPassphrase(phc, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if h != phc {
		t.Errorf("hash %q", h)
	}
	if plain != "" {
		t.Errorf("plaintext should be empty when only flag-hash provided, got %q", plain)
	}
}

func TestResolveRecoveryPassphrase_InvalidFlag(t *testing.T) {
	if _, _, err := resolveRecoveryPassphrase("not-argon2id", nil, true); err == nil {
		t.Error("expected error for non-PHC string")
	}
}

func TestResolveRecoveryPassphrase_NonInteractiveRequiresFlag(t *testing.T) {
	if _, _, err := resolveRecoveryPassphrase("", nil, true); err == nil {
		t.Error("expected error when no flag and non-interactive")
	}
}

func TestResolveRecoveryPassphrase_InteractiveRequiresPrompter(t *testing.T) {
	// Defensive guard: if a caller forgets to wire a prompter when
	// interactive resolution is needed, fail clearly rather than nil-panic.
	if _, _, err := resolveRecoveryPassphrase("", nil, false); err == nil {
		t.Error("expected error when interactive but prompter is nil")
	}
}
