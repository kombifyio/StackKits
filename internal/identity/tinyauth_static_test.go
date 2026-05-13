package identity

import (
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

// TestGenerateTinyAuthStatic verifies the happy path: username pattern,
// bcrypt round-trip, env-format, and password entropy.
func TestGenerateTinyAuthStatic(t *testing.T) {
	// Cost 10 keeps the test fast; the default-cost test below verifies
	// the production default is 12.
	g := &TinyAuthStaticGenerator{NodeName: "homelab-pi-01", BcryptCost: 10}
	cred, err := g.Generate()
	if err != nil {
		t.Fatal(err)
	}

	if cred.Username != "bg-homelab-pi-01-static" {
		t.Errorf("username pattern wrong: %q", cred.Username)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(cred.PasswordBcrypt), []byte(cred.PasswordPlain)); err != nil {
		t.Errorf("bcrypt verify failed: %v", err)
	}
	envVal := cred.ToEnvValue()
	if !strings.HasPrefix(envVal, cred.Username+":") {
		t.Errorf("env value malformed: %q", envVal)
	}
	if !strings.Contains(envVal, cred.PasswordBcrypt) {
		t.Errorf("env value missing bcrypt hash: %q", envVal)
	}
	// 32 bytes base64-encoded (RawStdEncoding) is ~43 characters; require
	// at least 40 to catch silent regressions in RandomPassword.
	if len(cred.PasswordPlain) < 40 {
		t.Errorf("password plain too short: %d", len(cred.PasswordPlain))
	}
}

// TestGenerateTinyAuthStaticRequiresNodeName asserts NodeName is mandatory.
func TestGenerateTinyAuthStaticRequiresNodeName(t *testing.T) {
	g := &TinyAuthStaticGenerator{NodeName: ""}
	if _, err := g.Generate(); err == nil {
		t.Error("expected error for empty NodeName")
	}
}

// TestGenerateTinyAuthStaticDefaultCost verifies BcryptCost=0 defaults to
// cost 12. The bcrypt hash format is `$2a$<cost>$<salt><digest>`, so a
// "$2a$12$" prefix is the canonical signal.
func TestGenerateTinyAuthStaticDefaultCost(t *testing.T) {
	g := &TinyAuthStaticGenerator{NodeName: "x"} // BcryptCost not set
	cred, err := g.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(cred.PasswordBcrypt, "$2a$12$") {
		t.Errorf("expected default cost 12, got hash: %q", cred.PasswordBcrypt)
	}
}

// TestGenerateTinyAuthStaticUniqueness verifies two consecutive Generate()
// calls produce different passwords (RandomPassword is, well, random).
func TestGenerateTinyAuthStaticUniqueness(t *testing.T) {
	g := &TinyAuthStaticGenerator{NodeName: "x", BcryptCost: 10}
	a, err := g.Generate()
	if err != nil {
		t.Fatal(err)
	}
	b, err := g.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if a.PasswordPlain == b.PasswordPlain {
		t.Error("two consecutive Generate() calls produced the same password")
	}
}
