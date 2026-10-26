package localowner

import (
	"context"
	"errors"
	"strings"
	"time"

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
	if err != nil || len(matches) != 0 {
		// Even matching mutable profile data cannot claim an existing local
		// subject. Legacy adoption needs separate explicit ID-bound evidence.
		return nil, errors.New("localowner: projected household username already exists or cannot be checked")
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
