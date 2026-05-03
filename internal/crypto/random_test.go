package crypto

import (
	"encoding/base64"
	"regexp"
	"testing"
)

func TestRandomPasswordLength(t *testing.T) {
	pwd, err := RandomPassword(32)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.RawStdEncoding.DecodeString(pwd)
	if err != nil {
		t.Fatalf("not valid base64: %v", err)
	}
	if len(raw) != 32 {
		t.Errorf("want 32 raw bytes, got %d", len(raw))
	}
}

func TestRandomPasswordCharset(t *testing.T) {
	pwd, err := RandomPassword(24)
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[A-Za-z0-9+/]+$`).MatchString(pwd) {
		t.Errorf("password contains unexpected chars: %q", pwd)
	}
}

func TestRandomPasswordUniqueness(t *testing.T) {
	seen := map[string]struct{}{}
	for i := 0; i < 100; i++ {
		pwd, err := RandomPassword(32)
		if err != nil {
			t.Fatal(err)
		}
		if _, dup := seen[pwd]; dup {
			t.Fatal("collision in 100 generations")
		}
		seen[pwd] = struct{}{}
	}
}

func TestRandomPasswordMinLength(t *testing.T) {
	if _, err := RandomPassword(15); err == nil {
		t.Error("expected error for length < 16, got none")
	}
}
