package localowner

import (
	"context"
	"errors"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/pocketid"
)

const householdGroupName = "household"

// HouseholdUser is one non-owner PocketID subject in the household group.
type HouseholdUser struct {
	Username    string
	Email       string
	DisplayName string
	Status      string
	ExpiresAt   time.Time
	SetupURL    string
}

// HouseholdUserSpec is the owner-supplied identity for one household member.
type HouseholdUserSpec struct {
	Username    string
	Email       string
	DisplayName string
}

// AddHouseholdUser creates a non-admin PocketID subject in the household
// group and returns a one-time passkey enrollment URL. Owners, admins and
// break-glass identities are refused.
func (s *Service) AddHouseholdUser(ctx context.Context, spec HouseholdUserSpec) (HouseholdUser, error) {
	owner, client, err := s.ready(ctx)
	if err != nil {
		return HouseholdUser{}, err
	}
	if err := verifyPocketIDAdmin(ctx, client); err != nil {
		return HouseholdUser{}, err
	}
	username := strings.TrimSpace(spec.Username)
	email := strings.TrimSpace(spec.Email)
	if username == "" || email == "" || strings.ContainsAny(username, "/?#") {
		return HouseholdUser{}, errors.New("localowner: household user requires username and email")
	}
	if username == owner.PocketID.Username || email == owner.PocketID.Email {
		return HouseholdUser{}, errors.New("localowner: household user cannot reuse the owner identity")
	}
	displayName := strings.TrimSpace(spec.DisplayName)
	if displayName == "" {
		displayName = username
	}
	if _, err := ensureRequiredGroups(ctx, client); err != nil {
		return HouseholdUser{}, err
	}
	householdID, err := client.GetGroupIDByName(ctx, householdGroupName)
	if err != nil || strings.TrimSpace(householdID) == "" {
		return HouseholdUser{}, errors.New("localowner: household group is missing")
	}
	existing, err := client.FindUsersByUsername(ctx, username)
	if err != nil {
		return HouseholdUser{}, errors.New("localowner: household user lookup failed")
	}
	if len(existing) > 0 {
		if len(existing) != 1 || existing[0].IsAdmin || !householdMember(existing[0]) ||
			existing[0].Email != email || effectiveDisplayName(existing[0]) != displayName {
			return HouseholdUser{}, errors.New("localowner: household username already exists")
		}
		credentials, credentialErr := client.ListUserWebAuthnCredentials(ctx, existing[0].ID)
		if credentialErr != nil {
			return HouseholdUser{}, errors.New("localowner: household passkey readback failed")
		}
		status := "pending"
		if len(credentials) > 0 {
			status = "active"
		}
		return HouseholdUser{Username: username, Email: email, DisplayName: displayName, Status: status}, nil
	}
	created, err := client.CreateUser(ctx, pocketid.CreateUserRequest{
		Username: username, Email: email, FirstName: displayName, DisplayName: displayName,
		IsAdmin: false, UserGroupIDs: []string{householdID},
	})
	if err != nil || created == nil || strings.TrimSpace(created.ID) == "" {
		return HouseholdUser{}, errors.New("localowner: household user creation failed")
	}
	readback, err := client.GetUser(ctx, created.ID)
	if err != nil || readback == nil || readback.IsAdmin || householdMember(*readback) == false {
		return HouseholdUser{}, errors.New("localowner: household user readback failed")
	}
	tinyAuthIDs, err := exactRequiredGroupIDs(ctx, client)
	if err != nil {
		return HouseholdUser{}, err
	}
	if err := s.ensureTinyAuthPocketIDBinding(ctx, client, owner, tinyAuthIDs); err != nil {
		return HouseholdUser{}, err
	}
	token, err := client.CreateOneTimeAccessToken(ctx, created.ID, ownerEnrollmentTTL)
	if err != nil || strings.TrimSpace(token) == "" {
		return HouseholdUser{}, errors.New("localowner: household enrollment creation failed")
	}
	address, err := localevidence.LocalIdentityRuntimeAddress(s.workspaceRoot)
	if err != nil {
		return HouseholdUser{}, err
	}
	expiresAt := s.now().UTC().Add(ownerEnrollmentTTL).Truncate(time.Second)
	return HouseholdUser{
		Username: readback.Username, Email: readback.Email, DisplayName: effectiveDisplayName(*readback),
		Status: "pending", ExpiresAt: expiresAt,
		SetupURL: address.PocketIDOrigin() + "/setup-account?token=" + url.QueryEscape(token),
	}, nil
}

// ListHouseholdUsers returns non-admin PocketID subjects in the household group.
func (s *Service) ListHouseholdUsers(ctx context.Context) ([]HouseholdUser, error) {
	_, client, err := s.ready(ctx)
	if err != nil {
		return nil, err
	}
	if err := verifyPocketIDAdmin(ctx, client); err != nil {
		return nil, err
	}
	users, err := client.ListUsers(ctx)
	if err != nil {
		return nil, errors.New("localowner: household user list failed")
	}
	result := make([]HouseholdUser, 0)
	for _, user := range users {
		if user.IsAdmin || !householdMember(user) {
			continue
		}
		credentials, credentialErr := client.ListUserWebAuthnCredentials(ctx, user.ID)
		if credentialErr != nil {
			return nil, errors.New("localowner: household passkey readback failed")
		}
		status := "pending"
		if len(credentials) > 0 {
			status = "active"
		}
		result = append(result, HouseholdUser{
			Username: user.Username, Email: user.Email, DisplayName: effectiveDisplayName(user), Status: status,
		})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Username < result[j].Username })
	return result, nil
}

// RemoveHouseholdUser deletes one household subject. Owner and admin
// identities are refused.
func (s *Service) RemoveHouseholdUser(ctx context.Context, username string) error {
	owner, client, err := s.ready(ctx)
	if err != nil {
		return err
	}
	if err := verifyPocketIDAdmin(ctx, client); err != nil {
		return err
	}
	username = strings.TrimSpace(username)
	if username == "" || username == owner.PocketID.Username {
		return errors.New("localowner: household remove requires a non-owner username")
	}
	matches, err := client.FindUsersByUsername(ctx, username)
	if err != nil {
		return errors.New("localowner: household user lookup failed")
	}
	if len(matches) != 1 {
		return errors.New("localowner: household user is absent")
	}
	user := matches[0]
	if user.IsAdmin || !householdMember(user) || hasRequiredGroups(user.UserGroups) {
		return errors.New("localowner: refusing to remove an owner or admin identity")
	}
	if err := client.DeleteUser(ctx, user.ID); err != nil {
		return errors.New("localowner: household user deletion failed")
	}
	return nil
}

func householdMember(user pocketid.User) bool {
	return slicesContainsName(user.UserGroups, householdGroupName)
}

func slicesContainsName(groups []pocketid.UserGroup, name string) bool {
	for _, group := range groups {
		if group.Name == name {
			return true
		}
	}
	return false
}
