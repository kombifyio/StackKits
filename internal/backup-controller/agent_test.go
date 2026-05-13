package backupcontroller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEnrollHost_MintsTokenAndPersists is the happy path for the
// helper that the SaaS controller calls when an operator clicks
// "enroll host" in the kombify-TechStack dashboard. Three guarantees:
//  1. Token is non-empty (a missing token would render the agent
//     route unauthenticatable forever).
//  2. The host row is persisted with the same token (so subsequent
//     heartbeats authenticate).
//  3. An audit entry is appended (compliance — no silent enrolments).
func TestEnrollHost_MintsTokenAndPersists(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	audit := &AuditLog{Store: store}

	host := &Host{Hostname: "alpha", FleetID: "fleet-1", StackKitKind: HostKindBaseKit}
	enroll, err := EnrollHost(ctx, store, audit, host, "operator:apikey")
	require.NoError(t, err)
	assert.NotEmpty(t, enroll.Token, "token must be minted")
	assert.NotEmpty(t, enroll.HostID, "host id must be assigned")

	// The host row must hold the same token so agent middleware can
	// resolve it.
	got, err := store.GetHostByToken(ctx, enroll.Token)
	require.NoError(t, err)
	assert.Equal(t, host.ID, got.ID)
	assert.Equal(t, "alpha", got.Hostname)

	// An audit entry was written.
	entries, err := store.ListAuditByTenant(ctx, "", 10)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "host.enroll", entries[0].Action)
}

// TestEnrollHost_TokensAreUnique guards against a regression where the
// token generator returns a constant or near-constant value (the kind
// of bug a refactor could plausibly introduce). 256 bits of entropy in
// 32 random bytes — collisions across two calls are astronomical, so
// inequality is a correctness invariant.
func TestEnrollHost_TokensAreUnique(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()

	a := &Host{Hostname: "a", FleetID: "f"}
	b := &Host{Hostname: "b", FleetID: "f"}
	ea, err := EnrollHost(ctx, store, nil, a, "op")
	require.NoError(t, err)
	eb, err := EnrollHost(ctx, store, nil, b, "op")
	require.NoError(t, err)

	assert.NotEqual(t, ea.Token, eb.Token, "two enrolments must yield distinct tokens")
}

// TestEnrollHost_AuditOptional documents the audit parameter contract:
// nil is allowed (test code, controller-internal callers) and skips
// the audit write without erroring. We don't want to force every
// caller to construct an AuditLog when none is needed.
func TestEnrollHost_AuditOptional(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()

	host := &Host{Hostname: "x", FleetID: "f"}
	_, err := EnrollHost(ctx, store, nil, host, "op")
	require.NoError(t, err, "nil audit must not error")

	entries, _ := store.ListAuditByTenant(ctx, "", 10)
	assert.Empty(t, entries, "no audit log expected when audit is nil")
}
