package api

import (
	"context"
	"fmt"
	"strings"
	"time"

	stackaction "github.com/kombifyio/stackkits/internal/stackaction"
)

const (
	stackActionScopeRuntimeSSH       = "runtime:ssh"
	stackActionScopeRuntimeEnroll    = "runtime:enroll"
	stackActionScopeNodeOnboard      = "platform:node-onboard"
	stackActionScopeBackupRepository = "backup:repository"
	stackActionScopeOwnerSpecRead    = "owner-spec:read"
)

// StackActionReferenceResolver is the internal custody boundary for resolving
// opaque StackAction references. Implementations must revalidate ref identity,
// version, scope, expiry, tenant/subject binding, grant state, and revocation
// against their authoritative store before returning short-lived material.
// Returned material must never be logged or persisted in action evidence.
type StackActionReferenceResolver interface {
	ResolveAccessProfile(context.Context, stackaction.ScopedReference) (ResolvedAccessProfile, error)
	ResolveNodeOnboarding(context.Context, stackaction.ScopedReference) (ResolvedNodeOnboarding, error)
	ResolveBackupCredential(context.Context, stackaction.ScopedReference) (ResolvedBackupCredential, error)
}

// ResolvedAccessProfile is ephemeral internal SSH material.
type ResolvedAccessProfile struct {
	PrivateKey []byte
}

// ResolvedNodeOnboarding is ephemeral internal provider bootstrap material.
type ResolvedNodeOnboarding struct {
	OnboardingKey string
}

// ResolvedBackupCredential is ephemeral internal backup repository material.
type ResolvedBackupCredential struct {
	AccessKeyID        string
	SecretAccessKey    string
	RepositoryPassword string
}

func normalizeStackActionReference(ref *stackaction.ScopedReference, requiredScope string, now time.Time) (*stackaction.ScopedReference, error) {
	if ref == nil {
		return nil, fmt.Errorf("reference is required")
	}
	normalized := *ref
	normalized.Ref = strings.TrimSpace(normalized.Ref)
	normalized.Version = strings.TrimSpace(normalized.Version)
	normalized.ExpiresAt = normalized.ExpiresAt.UTC()
	normalized.Scopes = normalizeStackActionScopes(normalized.Scopes)
	if normalized.Ref == "" {
		return nil, fmt.Errorf("ref is required")
	}
	if normalized.Version == "" {
		return nil, fmt.Errorf("version is required")
	}
	if normalized.ExpiresAt.IsZero() || !normalized.ExpiresAt.After(now.UTC()) {
		return nil, fmt.Errorf("reference is expired")
	}
	if !containsStackActionScope(normalized.Scopes, requiredScope) {
		return nil, fmt.Errorf("required scope %q is missing", requiredScope)
	}
	return &normalized, nil
}

func normalizeStackActionScopes(scopes []string) []string {
	out := make([]string, 0, len(scopes))
	seen := make(map[string]bool, len(scopes))
	for _, scope := range scopes {
		scope = strings.ToLower(strings.TrimSpace(scope))
		if scope == "" || seen[scope] {
			continue
		}
		seen[scope] = true
		out = append(out, scope)
	}
	return out
}

func containsStackActionScope(scopes []string, required string) bool {
	for _, scope := range scopes {
		if scope == required {
			return true
		}
	}
	return false
}
