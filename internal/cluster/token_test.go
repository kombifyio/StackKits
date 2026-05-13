package cluster

import (
	"strings"
	"testing"
	"time"
)

func TestGenerateJoinToken(t *testing.T) {
	token, err := GenerateJoinToken("lab", MainNode{
		ID:       "main-1",
		Endpoint: "https://base.stack.home",
	}, time.Hour)
	if err != nil {
		t.Fatalf("GenerateJoinToken() error = %v", err)
	}
	if token.Version != JoinTokenVersion {
		t.Fatalf("Version = %q", token.Version)
	}
	if token.MainNode.Name != "main-1" {
		t.Fatalf("MainNode.Name = %q, want fallback to main id", token.MainNode.Name)
	}
	if !strings.HasPrefix(token.Token, "skj_") {
		t.Fatalf("Token = %q, want skj_ prefix", token.Token)
	}
	if time.Until(token.ExpiresAt) <= 0 {
		t.Fatal("ExpiresAt should be in the future")
	}
}

func TestGenerateJoinTokenRequiresMainEndpoint(t *testing.T) {
	if _, err := GenerateJoinToken("lab", MainNode{ID: "main-1"}, time.Hour); err == nil {
		t.Fatal("GenerateJoinToken() error = nil, want missing endpoint error")
	}
}
