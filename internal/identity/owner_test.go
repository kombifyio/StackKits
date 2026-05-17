package identity

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kombifyio/stackkits/internal/pocketid"
)

// fakePocketID is an in-memory implementation of PocketIDClient used by the
// owner-provisioner tests. It records every call so assertions can verify
// what the provisioner actually sent (e.g. that no password was leaked).
type fakePocketID struct {
	createdUsers      []pocketid.CreateUserRequest
	addedToGroups     map[string][]string
	knownGroups       map[string]string // group-name -> group-id
	issuedTokens      map[string]string // user-id -> token
	tokenTTLs         []time.Duration   // recorded TTLs for each CreateOneTimeAccessToken call
	failOnGroupLookup bool
	groupIDForName    string // when set, returned regardless of name
}

func (f *fakePocketID) CreateUser(ctx context.Context, req pocketid.CreateUserRequest) (*pocketid.User, error) {
	f.createdUsers = append(f.createdUsers, req)
	return &pocketid.User{
		ID:       "user-" + req.Username,
		Username: req.Username,
		Email:    req.Email,
	}, nil
}

func (f *fakePocketID) AddUserToGroup(ctx context.Context, userID, groupID string) error {
	if f.addedToGroups == nil {
		f.addedToGroups = map[string][]string{}
	}
	f.addedToGroups[groupID] = append(f.addedToGroups[groupID], userID)
	return nil
}

func (f *fakePocketID) GetGroupIDByName(ctx context.Context, name string) (string, error) {
	if f.failOnGroupLookup {
		return "", &pocketid.HTTPError{StatusCode: 404, Method: "GET", Path: "/api/user-groups", Body: "not found"}
	}
	if f.groupIDForName != "" {
		return f.groupIDForName, nil
	}
	if f.knownGroups == nil {
		f.knownGroups = map[string]string{"owners": "grp-owners-uuid"}
	}
	id, ok := f.knownGroups[name]
	if !ok {
		// PocketID's real client returns ("", nil) when no group matches;
		// match that contract here so the provisioner exercises its own
		// "no group by that name exists" branch.
		return "", nil
	}
	return id, nil
}

func (f *fakePocketID) CreateOneTimeAccessToken(ctx context.Context, userID string, ttl time.Duration) (string, error) {
	if f.issuedTokens == nil {
		f.issuedTokens = map[string]string{}
	}
	tok := "ott-" + userID
	f.issuedTokens[userID] = tok
	f.tokenTTLs = append(f.tokenTTLs, ttl)
	return tok, nil
}

// Compile-time guarantee that fakePocketID satisfies the production interface.
var _ PocketIDClient = (*fakePocketID)(nil)

func TestProvisionLocalOwner(t *testing.T) {
	fake := &fakePocketID{}
	p := &OwnerProvisioner{Client: fake, PocketIDURL: "https://id.test.local"}

	result, err := p.Provision(t.Context(), OwnerSpec{
		Source:      "local",
		Email:       "mako@kombify.io",
		Username:    "mako",
		DisplayName: "Marcel Kombify",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Verify user was created with correct fields.
	if len(fake.createdUsers) != 1 {
		t.Fatalf("want 1 user, got %d", len(fake.createdUsers))
	}
	cu := fake.createdUsers[0]
	if cu.Email != "mako@kombify.io" {
		t.Errorf("wrong email: %q", cu.Email)
	}
	if cu.Username != "mako" {
		t.Errorf("wrong username: %q", cu.Username)
	}
	if cu.FirstName != "Marcel Kombify" {
		t.Errorf("wrong firstName: %q", cu.FirstName)
	}
	if !cu.IsAdmin {
		t.Error("owner must be admin")
	}

	// Verify added to owners group via the resolved UUID.
	members, ok := fake.addedToGroups["grp-owners-uuid"]
	if !ok || len(members) != 1 || members[0] != "user-mako" {
		t.Errorf("owner not added to owners group: %v", fake.addedToGroups)
	}

	// Verify a single one-time-access token was issued for the new user.
	if got := fake.issuedTokens["user-mako"]; got != "ott-user-mako" {
		t.Errorf("expected token to be issued for user-mako, got %q", got)
	}

	// Verify setup URL was generated correctly.
	wantPrefix := "https://id.test.local/setup-account?token=ott-user-mako"
	if !strings.Contains(result.SetupURL, wantPrefix) {
		t.Errorf("setup URL malformed: got %q, want prefix %q", result.SetupURL, wantPrefix)
	}
	if result.UserID != "user-mako" {
		t.Errorf("wrong UserID: %q", result.UserID)
	}
}

func TestProvisionDoesNotSendPassword(t *testing.T) {
	// PocketID v2 has no password field. Verify the provisioner doesn't
	// somehow smuggle a password-shaped value through the email/firstName
	// fields, and that the CreateUserRequest struct surface (which has no
	// Password member) is what we're sending.
	fake := &fakePocketID{}
	p := &OwnerProvisioner{Client: fake, PocketIDURL: "https://id.test.local"}

	_, err := p.Provision(t.Context(), OwnerSpec{
		Source:   "local",
		Email:    "x@y.com",
		Username: "x",
	})
	if err != nil {
		t.Fatal(err)
	}
	cu := fake.createdUsers[0]
	if cu.Email != "x@y.com" {
		t.Errorf("Email tampered: %q", cu.Email)
	}
	// The CreateUserRequest type itself has no Password field; this test
	// is here as living documentation that the v2 contract is intentional.
}

func TestProvisionDisplayNameFallsBackToUsername(t *testing.T) {
	fake := &fakePocketID{}
	p := &OwnerProvisioner{Client: fake, PocketIDURL: "https://id.test.local"}

	_, err := p.Provision(t.Context(), OwnerSpec{
		Source:   "local",
		Email:    "x@y.com",
		Username: "fallback",
	})
	if err != nil {
		t.Fatal(err)
	}
	if fake.createdUsers[0].FirstName != "fallback" {
		t.Errorf("displayName should fall back to username: got %q", fake.createdUsers[0].FirstName)
	}
}

func TestProvisionRejectsCloudAsOrchestratorManaged(t *testing.T) {
	fake := &fakePocketID{}
	p := &OwnerProvisioner{Client: fake, PocketIDURL: "https://id.test.local"}
	_, err := p.Provision(t.Context(), OwnerSpec{Source: "cloud", Email: "x@y.com", Username: "x"})
	if err == nil || !strings.Contains(err.Error(), "orchestrator-managed") {
		t.Errorf("expected orchestrator-managed error, got: %v", err)
	}
	// No PocketID calls should have been made.
	if len(fake.createdUsers) != 0 {
		t.Errorf("cloud-source must not call CreateUser: %v", fake.createdUsers)
	}
}

func TestProvisionRejectsUnknownSource(t *testing.T) {
	fake := &fakePocketID{}
	p := &OwnerProvisioner{Client: fake, PocketIDURL: "https://id.test.local"}
	_, err := p.Provision(t.Context(), OwnerSpec{Source: "magic", Email: "x@y.com", Username: "x"})
	if err == nil || !strings.Contains(err.Error(), "invalid owner source") {
		t.Errorf("expected 'invalid owner source' error, got: %v", err)
	}
}

func TestProvisionMissingFields(t *testing.T) {
	cases := []struct {
		name string
		spec OwnerSpec
	}{
		{"missing email", OwnerSpec{Source: "local", Username: "x"}},
		{"missing username", OwnerSpec{Source: "local", Email: "x@y.com"}},
		{"missing both", OwnerSpec{Source: "local"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fake := &fakePocketID{}
			p := &OwnerProvisioner{Client: fake, PocketIDURL: "https://id.test.local"}
			if _, err := p.Provision(t.Context(), c.spec); err == nil {
				t.Error("expected error, got nil")
			}
			if len(fake.createdUsers) != 0 {
				t.Errorf("invalid spec must not call CreateUser: %v", fake.createdUsers)
			}
		})
	}
}

func TestProvisionGroupLookupFailure(t *testing.T) {
	fake := &fakePocketID{failOnGroupLookup: true}
	p := &OwnerProvisioner{Client: fake, PocketIDURL: "https://id.test.local"}
	_, err := p.Provision(t.Context(), OwnerSpec{Source: "local", Email: "x@y.com", Username: "x"})
	if err == nil {
		t.Fatal("expected error when group lookup fails")
	}
	// Ensure the underlying HTTPError is preserved through the wrapping
	// chain, so callers can match on it.
	var herr *pocketid.HTTPError
	if !errors.As(err, &herr) {
		t.Errorf("expected wrapped HTTPError, got %T: %v", err, err)
	}
}

func TestProvisionGroupNotFound(t *testing.T) {
	// GetGroupIDByName returns ("", nil) when the named group doesn't exist
	// (per the real pocketid client contract). The provisioner must surface
	// this as an error with a clear message rather than silently passing
	// the empty string to AddUserToGroup.
	fake := &fakePocketID{knownGroups: map[string]string{}} // no "owners" entry
	p := &OwnerProvisioner{Client: fake, PocketIDURL: "https://id.test.local"}
	_, err := p.Provision(t.Context(), OwnerSpec{Source: "local", Email: "x@y.com", Username: "x"})
	if err == nil || !strings.Contains(err.Error(), "no group by that name exists") {
		t.Errorf("expected 'no group by that name' error, got: %v", err)
	}
	if len(fake.addedToGroups) != 0 {
		t.Errorf("AddUserToGroup must not be called when group is missing: %v", fake.addedToGroups)
	}
}

func TestProvisionCustomOwnersGroup(t *testing.T) {
	fake := &fakePocketID{
		knownGroups: map[string]string{"admins": "grp-admins-uuid"},
	}
	p := &OwnerProvisioner{
		Client:      fake,
		PocketIDURL: "https://id.test.local",
		OwnersGroup: "admins",
	}
	_, err := p.Provision(t.Context(), OwnerSpec{
		Source: "local", Email: "x@y.com", Username: "x",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := fake.addedToGroups["grp-admins-uuid"]; !ok {
		t.Errorf("user not added to custom 'admins' group: %v", fake.addedToGroups)
	}
}
