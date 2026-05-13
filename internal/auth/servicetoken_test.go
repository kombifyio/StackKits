package auth

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestSignServiceToken_HappyPath(t *testing.T) {
	tok, err := SignServiceToken("stackkits", "administration", "secret-x", 0)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("expected 3 parts, got %d in %q", len(parts), tok)
	}

	// Header round-trip
	hdr, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatalf("decode header: %v", err)
	}
	if string(hdr) != `{"alg":"HS256","typ":"JWT"}` {
		t.Errorf("header mismatch: %q", string(hdr))
	}

	// Payload round-trip
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	var c Claims
	if err := json.Unmarshal(payload, &c); err != nil {
		t.Fatalf("decode claims: %v", err)
	}
	if c.Iss != "kombify-stackkits" {
		t.Errorf("iss = %q, want kombify-stackkits", c.Iss)
	}
	if c.Aud != "kombify-administration" {
		t.Errorf("aud = %q, want kombify-administration", c.Aud)
	}
	if c.Svc != "stackkits" {
		t.Errorf("svc = %q, want stackkits", c.Svc)
	}
	if c.Exp <= c.Iat {
		t.Errorf("exp (%d) must be after iat (%d)", c.Exp, c.Iat)
	}
}

func TestSignServiceToken_RejectsEmptyArgs(t *testing.T) {
	cases := []struct {
		name, svc, target, secret string
	}{
		{"empty secret", "stackkits", "administration", ""},
		{"blank secret", "stackkits", "administration", " \n\t"},
		{"empty svc", "", "administration", "secret"},
		{"empty target", "stackkits", "", "secret"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := SignServiceToken(tc.svc, tc.target, tc.secret, 0); err == nil {
				t.Errorf("expected error for %s", tc.name)
			}
		})
	}
}

func TestSignServiceToken_TrimsSecretWhitespace(t *testing.T) {
	tok, err := SignServiceToken("stackkits", "administration", "test-secret-12345\n", 0)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("malformed token")
	}

	signingInput := parts[0] + "." + parts[1]
	gotSig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatalf("decode sig: %v", err)
	}
	expected := hmacSHA256([]byte(signingInput), []byte("test-secret-12345"))
	if !equalBytes(gotSig, expected) {
		t.Error("signature should use the trimmed service-auth secret")
	}
}

func TestSignServiceToken_TTLApplied(t *testing.T) {
	short, _ := SignServiceToken("stackkits", "administration", "secret", time.Second)
	long, _ := SignServiceToken("stackkits", "administration", "secret", time.Hour)

	parseExp := func(tok string) int64 {
		parts := strings.Split(tok, ".")
		payload, _ := base64.RawURLEncoding.DecodeString(parts[1])
		var c Claims
		_ = json.Unmarshal(payload, &c)
		return c.Exp - c.Iat
	}

	if parseExp(short) >= parseExp(long) {
		t.Errorf("short TTL token (%ds) should expire before long (%ds)", parseExp(short), parseExp(long))
	}
}

// TestSignServiceToken_DefaultTTL ensures ttl<=0 falls back to DefaultTokenTTL.
func TestSignServiceToken_DefaultTTL(t *testing.T) {
	tok, err := SignServiceToken("stackkits", "administration", "secret", 0)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(tok, ".")
	payload, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var c Claims
	_ = json.Unmarshal(payload, &c)

	expectedTTL := int64(DefaultTokenTTL.Seconds())
	actualTTL := c.Exp - c.Iat
	if actualTTL != expectedTTL {
		t.Errorf("default TTL: expected %ds, got %ds", expectedTTL, actualTTL)
	}
}

// TestSignatureIsHMACSHA256 verifies the signing algorithm matches the wire
// contract with kombify-Administration's tryServiceAuth verifier.
func TestSignatureIsHMACSHA256(t *testing.T) {
	tok, _ := SignServiceToken("stackkits", "administration", "test-secret-12345", 0)
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatal("malformed token")
	}
	signingInput := parts[0] + "." + parts[1]
	gotSig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatalf("decode sig: %v", err)
	}
	if len(gotSig) != 32 {
		t.Errorf("HMAC-SHA256 produces 32 bytes; got %d", len(gotSig))
	}
	// Verify-friendly check: produce the same MAC and assert equality
	expected := hmacSHA256([]byte(signingInput), []byte("test-secret-12345"))
	if !equalBytes(gotSig, expected) {
		t.Error("signature does not match HMAC-SHA256 of header.payload")
	}
}

// minimal helpers — kept inline so this test file does not depend on stdlib
// crypto in a way that masks bugs in the production signer.
func hmacSHA256(data, key []byte) []byte {
	// crypto/hmac is OK in test, mirrors what production does
	h := hmacNew(key)
	h.Write(data)
	return h.Sum(nil)
}
