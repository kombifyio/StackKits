package localowner

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/pocketid"
)

// Creation uses the pinned provider's caller-chosen ID, not a username upsert:
// https://github.com/pocket-id/pocket-id/blob/v2.16.0/backend/internal/dto/user_dto.go
// https://github.com/pocket-id/pocket-id/blob/v2.16.0/backend/internal/service/user_service.go
// The signed intent already reserves subject before this function is called.
func (s *Service) createProjectedSubject(ctx context.Context, client pocketIDOwnerClient, spec HouseholdUserSpec, subject, ownerUsername, ownerEmail string, beforeCreate func() error) (*pocketid.User, error) {
	username, email := strings.TrimSpace(spec.Username), strings.TrimSpace(spec.Email)
	if username == "" || email == "" || strings.ContainsAny(username, "/?#") || username == ownerUsername || email == ownerEmail {
		return nil, errors.New("localowner: invalid projected household identity")
	}
	matches, err := client.FindUsersByUsername(ctx, username)
	if err != nil {
		return nil, errors.New("localowner: projected household username cannot be checked")
	}
	if len(matches) != 0 {
		// Even matching mutable profile data cannot claim an existing local
		// subject: an unused legacy account is replaced, never adopted.
		if err := s.replaceUnusedLegacySubject(ctx, client, matches, username, email, subject); err != nil {
			return nil, err
		}
	}
	if _, err := ensureRequiredGroups(ctx, client); err != nil {
		return nil, err
	}
	if _, err := ensureHouseholdOpenGroup(ctx, client); err != nil {
		return nil, err
	}
	householdID, err := client.GetGroupIDByName(ctx, householdGroupName)
	if err != nil || strings.TrimSpace(householdID) == "" {
		return nil, errors.New("localowner: household group is missing")
	}
	accessIDs, err := householdAccessGroupIDs(ctx, client, spec.Access)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(spec.DisplayName)
	if name == "" {
		name = username
	}
	if err := beforeCreate(); err != nil {
		return nil, err
	}
	created, err := client.CreateUser(ctx, pocketid.CreateUserRequest{
		ID: subject, Username: username, Email: email, FirstName: name, DisplayName: name,
		IsAdmin: false, UserGroupIDs: append([]string{householdID}, accessIDs...),
	})
	if err != nil || created == nil || created.ID != subject || created.Username != username || created.IsAdmin {
		// The response may have been lost after commit. Only GET of the
		// reserved ID on a subsequent attempt may recover it; never adopt
		// the username or guess the ID of a partially completed creation.
		return nil, errors.New("localowner: projected household creation not confirmed")
	}
	return client.GetUser(ctx, subject)
}

// replaceUnusedLegacySubject deletes the one account that the old unrevisioned
// household add left under the projected username (platform-cztj4.5), so the
// bound subject can be created. Pocket ID keeps username and email unique, so
// the bound create cannot coexist with it. Its UUID never becomes custody. It
// is deleted only when nothing on it belongs to a person yet: a non-admin
// household account with the member's address, no passkey, and no projection
// custody naming it. An account with a passkey is refused, because replacing it
// would cut off whoever holds that passkey; it needs ID-bound adoption.
func (s *Service) replaceUnusedLegacySubject(ctx context.Context, client pocketIDOwnerClient, matches []pocketid.User, username, email, subject string) error {
	refuse := func(reason string) error {
		return errors.New("localowner: projected household username is held by " + reason)
	}
	if len(matches) != 1 || !strings.EqualFold(matches[0].Username, username) {
		return refuse("several or differently named accounts")
	}
	legacyID := matches[0].ID
	if legacyID == "" || legacyID == subject {
		return refuse("an unidentified account")
	}
	fenced, ok := client.(pocketIDMembershipClient)
	if !ok {
		return errors.New("localowner: provider conditional membership contract unavailable")
	}
	bound, err := s.householdCustodyNames(legacyID)
	if err != nil {
		return err
	}
	if bound {
		return refuse("a bound household subject")
	}
	// The caller already refused the owner's username and address, and both are
	// unique in Pocket ID, so a match on them is never the owner. Any admin or
	// owner group still refuses: the delete below cannot be undone.
	eligible := func(user *pocketid.User) bool {
		groups := pocketIDGroupNames(user.UserGroups)
		return user.ID == legacyID && !user.IsAdmin && !slices.Contains(groups, "admins") &&
			!slices.Contains(groups, "owners") && householdMember(*user) &&
			strings.EqualFold(user.Username, username) && strings.EqualFold(strings.TrimSpace(user.Email), email)
	}
	user, err := client.GetUser(ctx, legacyID)
	if err != nil || user == nil {
		return errors.New("localowner: legacy household account readback failed")
	}
	if !eligible(user) {
		return refuse("an account that is not this member's legacy household account")
	}
	snapshot, err := fenced.GetMembership(ctx, legacyID)
	if err != nil {
		return err
	}
	// The conditional delete is fenced on this snapshot, so judge the
	// snapshot itself, not the earlier readback.
	if !eligible(&snapshot.User) {
		return refuse("an account that is not this member's legacy household account")
	}
	// The membership version may not cover passkeys: read them last, right
	// before the delete.
	credentials, err := client.ListUserWebAuthnCredentials(ctx, legacyID)
	if err != nil {
		return errors.New("localowner: legacy household passkey readback failed")
	}
	if len(credentials) != 0 {
		return refuse("a legacy account with a passkey; it needs ID-bound adoption")
	}
	if err := fenced.DeleteMembership(ctx, *snapshot); err != nil && !errors.Is(err, pocketid.ErrNotFound) {
		return errors.New("localowner: legacy household account deletion failed")
	}
	if _, err := client.GetUser(ctx, legacyID); !errors.Is(err, pocketid.ErrNotFound) {
		return errors.New("localowner: legacy household account deletion is not confirmed")
	}
	remaining, err := client.FindUsersByUsername(ctx, username)
	if err != nil || len(remaining) != 0 {
		return errors.New("localowner: projected household username already exists or cannot be checked")
	}
	return nil
}

// householdCustodyNames reports whether any household projection custody file
// names the Pocket ID subject. Any mention refuses; it never grants.
func (s *Service) householdCustodyNames(pocketIDSubject string) (bool, error) {
	root, err := confinedfs.Open(s.workspaceRoot)
	if err != nil {
		return false, err
	}
	defer root.Close()
	view, err := root.View(".")
	if err != nil {
		return false, err
	}
	dir, err := view.Open(".stackkit/custody")
	if err != nil {
		return false, err
	}
	names, err := dir.Readdirnames(-1)
	_ = dir.Close()
	if err != nil {
		return false, err
	}
	tx, err := root.BeginTransaction()
	if err != nil {
		return false, err
	}
	defer tx.Close()
	for _, name := range names {
		if !strings.HasPrefix(name, "household-projection-") || !strings.HasSuffix(name, ".json") {
			continue
		}
		body, _, err := tx.ReadStableBounded(".stackkit/custody/"+name, 4096)
		if err != nil {
			return false, err
		}
		if strings.Contains(string(body), pocketIDSubject) {
			return true, nil
		}
	}
	return false, nil
}

func validateProjectedSubject(user *pocketid.User, subject, ownerSubject string) error {
	if user == nil || subject == "" || subject == ownerSubject || user.ID != subject ||
		user.IsAdmin || user.Disabled || !householdMember(*user) || hasRequiredGroups(user.UserGroups) {
		return errors.New("localowner: projection requires its bound enabled non-owner household subject")
	}
	return nil
}

// projectedSubjectEnrollment never resolves a username to select its target.
// A migrated passkey is not active; pending and reenrollment-required both
// issue enrollment for the exact same custody-bound subject.
func (s *Service) projectedSubjectEnrollment(ctx context.Context, client pocketIDOwnerClient, user pocketid.User) (HouseholdUser, error) {
	owner, _, err := s.ready(ctx)
	if err != nil {
		return HouseholdUser{}, err
	}
	if err := s.ensureKombifyLinkClient(ctx); err != nil {
		return HouseholdUser{}, err
	}
	tinyAuthIDs, err := exactRequiredGroupIDs(ctx, client)
	if err != nil {
		return HouseholdUser{}, err
	}
	if err := s.ensureTinyAuthPocketIDBinding(ctx, client, owner, tinyAuthIDs); err != nil {
		return HouseholdUser{}, err
	}
	if err := s.ensureConfiguredImmichUserClaims(ctx, client); err != nil {
		return HouseholdUser{}, err
	}
	credentials, err := client.ListUserWebAuthnCredentials(ctx, user.ID)
	if err != nil {
		return HouseholdUser{}, errors.New("localowner: projected household passkey readback failed")
	}
	active, _, err := s.passkeyActive(user.ID, credentials)
	if err != nil {
		return HouseholdUser{}, err
	}
	member := HouseholdUser{Username: user.Username, Email: user.Email, DisplayName: effectiveDisplayName(user),
		Status: "active", Access: householdAccessOf(user)}
	if active {
		return member, nil
	}
	if err := s.beginDomainReenrollment(ctx, client, user.ID); err != nil {
		return HouseholdUser{}, err
	}
	address, err := localevidence.LocalIdentityRuntimeAddress(s.workspaceRoot)
	if err != nil {
		return HouseholdUser{}, err
	}
	token, err := client.CreateOneTimeAccessToken(ctx, user.ID, ownerEnrollmentTTL)
	if err != nil || strings.TrimSpace(token) == "" {
		return HouseholdUser{}, errors.New("localowner: projected household enrollment creation failed")
	}
	member.Status, member.SetupURL = "pending", pocketid.ActivationURL(address.PocketIDOrigin(), token)
	member.ExpiresAt = s.now().UTC().Add(ownerEnrollmentTTL).Truncate(time.Second)
	return member, nil
}
