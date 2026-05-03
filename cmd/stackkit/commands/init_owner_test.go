package commands

import (
	"strings"
	"testing"

	"github.com/kombifyio/stackkits/internal/identity"
)

func TestResolveOwnerSpec_LocalNonInteractive(t *testing.T) {
	s, has, err := resolveOwnerSpec(ownerFlags{
		Source:      "local",
		Email:       "mako@kombify.io",
		Username:    "mako",
		DisplayName: "Marcel",
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
	if s.Email != "mako@kombify.io" {
		t.Errorf("email %q", s.Email)
	}
	if s.Username != "mako" {
		t.Errorf("username %q", s.Username)
	}
	if s.DisplayName != "Marcel" {
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

func TestResolveOwnerSpec_CloudRejected(t *testing.T) {
	_, has, err := resolveOwnerSpec(ownerFlags{Source: "cloud"}, nil, true)
	if err == nil || !strings.Contains(err.Error(), "Phase 2") {
		t.Errorf("expected Phase-2 error, got: %v", err)
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

func TestResolveRecoveryPassphrase_FlagHash(t *testing.T) {
	// Valid PHC string passes through verbatim. Plaintext stays empty —
	// Task 11's apply path is responsible for reprompting when it needs
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
