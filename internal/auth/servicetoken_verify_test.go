package auth

import (
	"strings"
	"testing"
	"time"
)

func TestVerifyServiceToken_AcceptsAllowedCaller(t *testing.T) {
	token, err := SignServiceToken("techstack", "stackkits", "shared-secret", time.Minute)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	claims, err := VerifyServiceToken(token, VerifyOptions{
		Target:         "stackkits",
		Secrets:        []string{"shared-secret"},
		AllowedCallers: []string{"techstack"},
	})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}

	if claims.Svc != "techstack" {
		t.Fatalf("svc = %q, want techstack", claims.Svc)
	}
	if claims.Aud != "kombify-stackkits" {
		t.Fatalf("aud = %q, want kombify-stackkits", claims.Aud)
	}
}

func TestVerifyServiceToken_AcceptsRotatedSecret(t *testing.T) {
	token, err := SignServiceToken("techstack", "stackkits", "next-secret", time.Minute)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	_, err = VerifyServiceToken(token, VerifyOptions{
		Target:         "stackkits",
		Secrets:        []string{"current-secret", "next-secret"},
		AllowedCallers: []string{"techstack"},
	})
	if err != nil {
		t.Fatalf("verify with rotated secret: %v", err)
	}
}

func TestVerifyServiceToken_RejectsWrongAudience(t *testing.T) {
	token, err := SignServiceToken("techstack", "simulate", "shared-secret", time.Minute)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	_, err = VerifyServiceToken(token, VerifyOptions{
		Target:         "stackkits",
		Secrets:        []string{"shared-secret"},
		AllowedCallers: []string{"techstack"},
	})
	if err == nil || !strings.Contains(err.Error(), "audience") {
		t.Fatalf("expected audience error, got %v", err)
	}
}

func TestVerifyServiceToken_RejectsWrongCaller(t *testing.T) {
	token, err := SignServiceToken("simulate", "stackkits", "shared-secret", time.Minute)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	_, err = VerifyServiceToken(token, VerifyOptions{
		Target:         "stackkits",
		Secrets:        []string{"shared-secret"},
		AllowedCallers: []string{"techstack"},
	})
	if err == nil || !strings.Contains(err.Error(), "caller") {
		t.Fatalf("expected caller error, got %v", err)
	}
}
