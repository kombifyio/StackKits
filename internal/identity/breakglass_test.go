package identity

import (
	"strings"
	"testing"
	"time"
)

// TestGenerateBreakGlass verifies the happy path: a per-node break-glass
// admin is created with the expected username pattern, no password is sent
// (PocketID v2 contract), and the SetupURL is correctly composed from the
// returned token.
func TestGenerateBreakGlass(t *testing.T) {
	fake := &fakePocketID{}
	g := &BreakGlassGenerator{
		Client:      fake,
		NodeName:    "homelab-pi-01",
		PocketIDURL: "https://id.test.local",
	}

	cred, err := g.Generate(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if cred.Username != "bg-homelab-pi-01@local" {
		t.Errorf("unexpected username: %q", cred.Username)
	}
	if !strings.Contains(cred.SetupURL, "https://id.test.local/setup-account?token=") {
		t.Errorf("SetupURL malformed: %q", cred.SetupURL)
	}
	if cred.SetupToken == "" {
		t.Error("SetupToken empty")
	}
	if !strings.Contains(cred.SetupURL, cred.SetupToken) {
		t.Errorf("SetupURL doesn't contain SetupToken")
	}
	if cred.Group != "owners" {
		t.Errorf("Group %q, want owners", cred.Group)
	}
	if cred.UserID == "" {
		t.Error("UserID empty (needed for later rotation/revoke)")
	}

	// Verify user was created without a password field set.
	// CreateUserRequest doesn't even expose a Password member in v2, but
	// keep this assertion as living documentation of the contract.
	if len(fake.createdUsers) != 1 {
		t.Fatalf("want 1 user, got %d", len(fake.createdUsers))
	}
	if !fake.createdUsers[0].IsAdmin {
		t.Error("break-glass must be admin")
	}

	// Verify added to owners group via the resolved UUID.
	members, ok := fake.addedToGroups["grp-owners-uuid"]
	if !ok || len(members) != 1 {
		t.Errorf("break-glass not added to owners group: %v", fake.addedToGroups)
	}
}

// TestGenerateBreakGlassRequiresNodeName asserts NodeName is mandatory and
// no PocketID calls happen on the validation-failure path.
func TestGenerateBreakGlassRequiresNodeName(t *testing.T) {
	fake := &fakePocketID{}
	g := &BreakGlassGenerator{Client: fake, PocketIDURL: "https://id.test.local"}
	if _, err := g.Generate(t.Context()); err == nil {
		t.Error("expected error for empty NodeName")
	}
	if len(fake.createdUsers) != 0 {
		t.Errorf("missing NodeName must not call CreateUser: %v", fake.createdUsers)
	}
}

// TestGenerateBreakGlassDefaultTTL verifies the default token TTL is ~365
// days (allow ±1 day skew for any time-of-day arithmetic).
func TestGenerateBreakGlassDefaultTTL(t *testing.T) {
	fake := &fakePocketID{}
	g := &BreakGlassGenerator{Client: fake, NodeName: "x", PocketIDURL: "https://id.x"}
	if _, err := g.Generate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(fake.tokenTTLs) != 1 {
		t.Fatalf("want 1 token-ttl call, got %d", len(fake.tokenTTLs))
	}
	got := fake.tokenTTLs[0]
	want := 365 * 24 * time.Hour
	if got < want-24*time.Hour || got > want+24*time.Hour {
		t.Errorf("default TTL = %v, want ~365 days", got)
	}
}

// TestGenerateBreakGlassCustomTTL verifies an explicit TokenTTL is honored.
func TestGenerateBreakGlassCustomTTL(t *testing.T) {
	fake := &fakePocketID{}
	g := &BreakGlassGenerator{
		Client:      fake,
		NodeName:    "x",
		PocketIDURL: "https://id.x",
		TokenTTL:    7 * 24 * time.Hour,
	}
	if _, err := g.Generate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(fake.tokenTTLs) != 1 || fake.tokenTTLs[0] != 7*24*time.Hour {
		t.Errorf("custom TTL not honored: %v", fake.tokenTTLs)
	}
}

// TestGenerateBreakGlassCustomGroup verifies OwnersGroup overrides the
// default "owners".
func TestGenerateBreakGlassCustomGroup(t *testing.T) {
	fake := &fakePocketID{
		knownGroups: map[string]string{"admins": "grp-admins-uuid"},
	}
	g := &BreakGlassGenerator{
		Client:      fake,
		NodeName:    "x",
		PocketIDURL: "https://id.x",
		OwnersGroup: "admins",
	}
	cred, err := g.Generate(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if cred.Group != "admins" {
		t.Errorf("Group = %q, want admins", cred.Group)
	}
	if _, ok := fake.addedToGroups["grp-admins-uuid"]; !ok {
		t.Errorf("user not added to custom 'admins' group: %v", fake.addedToGroups)
	}
}

// TestGenerateBreakGlassGroupNotFound asserts that a missing owners group
// is reported as an error (not silently passed as "" to AddUserToGroup).
func TestGenerateBreakGlassGroupNotFound(t *testing.T) {
	fake := &fakePocketID{knownGroups: map[string]string{}} // no "owners"
	g := &BreakGlassGenerator{
		Client:      fake,
		NodeName:    "x",
		PocketIDURL: "https://id.x",
	}
	_, err := g.Generate(t.Context())
	if err == nil || !strings.Contains(err.Error(), "no group by that name exists") {
		t.Errorf("expected 'no group by that name' error, got: %v", err)
	}
	if len(fake.addedToGroups) != 0 {
		t.Errorf("AddUserToGroup must not be called when group is missing: %v", fake.addedToGroups)
	}
}
