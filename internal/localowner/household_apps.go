package localowner

import (
	"context"
	"errors"
	"slices"
	"sort"
	"strings"

	"github.com/kombifyio/stackkits/internal/householdapps"
	"github.com/kombifyio/stackkits/internal/pocketid"
)

const (
	householdOpenGroupName  = householdapps.OpenGroup
	householdAppGroupPrefix = householdapps.AppGroupPrefix
)

// HouseholdAppAccess is a member's app grant. The zero value is open: every
// user-tier app, as before per-app grants. Scoped admits only Apps.
type HouseholdAppAccess struct {
	Scoped bool
	Apps   []string
}

func normalizeHouseholdAccess(access HouseholdAppAccess) (HouseholdAppAccess, error) {
	if !access.Scoped {
		if len(access.Apps) != 0 {
			return HouseholdAppAccess{}, errors.New("localowner: app grants require scoped access")
		}
		return HouseholdAppAccess{}, nil
	}
	apps := make([]string, 0, len(access.Apps))
	for _, raw := range access.Apps {
		app := strings.TrimSpace(raw)
		if !householdapps.IsGrantable(app) {
			return HouseholdAppAccess{}, errors.New("localowner: app is not grantable to a household member: " + app)
		}
		apps = append(apps, app)
	}
	sort.Strings(apps)
	return HouseholdAppAccess{Scoped: true, Apps: slices.Compact(apps)}, nil
}

func householdAccessOf(user pocketid.User) HouseholdAppAccess {
	names := pocketIDGroupNames(user.UserGroups)
	if slices.Contains(names, householdOpenGroupName) {
		return HouseholdAppAccess{}
	}
	apps := []string{}
	for _, name := range names {
		if app, ok := strings.CutPrefix(name, householdAppGroupPrefix); ok {
			apps = append(apps, app)
		}
	}
	return HouseholdAppAccess{Scoped: true, Apps: apps}
}

// ensureNamedGroup returns the ID of a group, creating it when absent.
func ensureNamedGroup(ctx context.Context, client pocketIDOwnerClient, name, friendly string) (id string, created bool, err error) {
	id, err = client.GetGroupIDByName(ctx, name)
	if err != nil {
		return "", false, errors.New("localowner: PocketID group lookup failed")
	}
	if strings.TrimSpace(id) != "" {
		return id, false, nil
	}
	group, createErr := client.CreateUserGroup(ctx, pocketid.CreateUserGroupRequest{Name: name, FriendlyName: friendly})
	if errors.Is(createErr, pocketid.ErrAlreadyExists) {
		id, createErr = client.GetGroupIDByName(ctx, name)
		if createErr == nil && strings.TrimSpace(id) != "" {
			return id, false, nil
		}
	}
	if createErr != nil || group == nil || strings.TrimSpace(group.ID) == "" || group.Name != name {
		return "", false, errors.New("localowner: PocketID group creation failed")
	}
	return group.ID, true, nil
}

// ensureHouseholdOpenGroup creates the open-access group on first use and
// seeds it with every existing household member, so households that predate
// per-app grants keep access to every app.
func ensureHouseholdOpenGroup(ctx context.Context, client pocketIDOwnerClient) (string, error) {
	id, created, err := ensureNamedGroup(ctx, client, householdOpenGroupName, "Household (all apps)")
	if err != nil || !created {
		return id, err
	}
	users, err := client.ListUsers(ctx)
	if err != nil {
		return "", errors.New("localowner: household user list failed")
	}
	for _, user := range users {
		if user.IsAdmin || !householdMember(user) || hasRequiredGroups(user.UserGroups) {
			continue
		}
		if fenced, ok := client.(pocketIDMembershipClient); ok {
			if e := fenced.RequireMembershipCAS(ctx); e == nil {
				snapshot, e := fenced.GetMembership(ctx, user.ID)
				if e != nil {
					return "", e
				}
				if snapshot.ConditionalRequired {
					continue
				}
				if _, e = fenced.UpdateMembership(ctx, *snapshot, requiredAndExistingGroupIDs([]string{id}, snapshot.User.UserGroups)); e != nil {
					return "", e
				}
				continue
			} else if !errors.Is(e, pocketid.ErrNotFound) {
				return "", e
			}
		}
		if _, err := client.UpdateUserGroups(ctx, user.ID, requiredAndExistingGroupIDs([]string{id}, user.UserGroups)); err != nil {
			return "", errors.New("localowner: household open-group seeding failed")
		}
	}
	return id, nil
}

// seedHouseholdOpenGroupForExistingMembers creates the open group only when
// household members already exist, so a household without members is left
// untouched until its first invitation.
func seedHouseholdOpenGroupForExistingMembers(ctx context.Context, client pocketIDOwnerClient) error {
	id, err := client.GetGroupIDByName(ctx, householdOpenGroupName)
	if err != nil {
		return errors.New("localowner: PocketID group lookup failed")
	}
	if strings.TrimSpace(id) != "" {
		return nil
	}
	users, err := client.ListUsers(ctx)
	if err != nil {
		return errors.New("localowner: household user list failed")
	}
	for _, user := range users {
		if !user.IsAdmin && householdMember(user) && !hasRequiredGroups(user.UserGroups) {
			_, err := ensureHouseholdOpenGroup(ctx, client)
			return err
		}
	}
	return nil
}

// householdAccessGroupIDs returns the group IDs an access state needs on top
// of the household group, creating app groups on demand.
func householdAccessGroupIDs(ctx context.Context, client pocketIDOwnerClient, access HouseholdAppAccess) ([]string, error) {
	if !access.Scoped {
		id, err := ensureHouseholdOpenGroup(ctx, client)
		return []string{id}, err
	}
	ids := make([]string, 0, len(access.Apps))
	for _, app := range access.Apps {
		id, _, err := ensureNamedGroup(ctx, client, householdapps.AppGroup(app), "Household app "+app)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// applyHouseholdAccess replaces a member's open and app groups with the
// desired state and keeps every other group, then re-binds the existing
// user-tier app clients so a legacy client cannot still admit the whole
// household.
func (s *Service) applyHouseholdAccess(ctx context.Context, client pocketIDOwnerClient, user pocketid.User, access HouseholdAppAccess) error {
	return s.applyHouseholdAccessWithFence(ctx, client, user, access, false)
}

// requireCAS is chosen by the projection entrypoint, never a request flag.
func (s *Service) applyHouseholdAccessWithFence(ctx context.Context, client pocketIDOwnerClient, user pocketid.User, access HouseholdAppAccess, requireCAS bool) error {
	fenced, hasFence := client.(pocketIDMembershipClient)
	if hasFence {
		err := fenced.RequireMembershipCAS(ctx)
		if errors.Is(err, pocketid.ErrNotFound) && !requireCAS {
			// Unmodified official providers support the established manual lane.
			// Every other capability failure remains fail closed.
			hasFence = false
		} else if err != nil {
			return errors.New("localowner: conditional membership provider unavailable")
		}
	}
	if requireCAS && !hasFence {
		return errors.New("localowner: conditional membership provider required")
	}
	desired, err := householdAccessGroupIDs(ctx, client, access)
	if err != nil {
		return err
	}
	for attempt := 0; attempt < 3; attempt++ {
		var snapshot *pocketid.MembershipSnapshot
		if hasFence {
			snapshot, err = fenced.GetMembership(ctx, user.ID)
			if err != nil {
				return err
			}
			user = snapshot.User
		}
		ids := append([]string(nil), desired...)
		for _, group := range user.UserGroups {
			if group.Name != householdOpenGroupName && !strings.HasPrefix(group.Name, householdAppGroupPrefix) {
				ids = append(ids, group.ID)
			}
		}
		sort.Strings(ids)
		ids = slices.Compact(ids)
		var updated *pocketid.User
		if hasFence {
			var committed *pocketid.MembershipSnapshot
			committed, err = fenced.UpdateMembership(ctx, *snapshot, ids)
			if errors.Is(err, pocketid.ErrMembershipConflict) {
				continue
			}
			if err == nil {
				updated = &committed.User
			}
		} else {
			updated, err = client.UpdateUserGroups(ctx, user.ID, ids)
		}
		if err != nil || updated == nil || !householdMember(*updated) || !sameAccess(householdAccessOf(*updated), access) {
			return errors.New("localowner: household app grant remains unconfirmed")
		}
		return s.convergeHouseholdAppClients(ctx, client)
	}
	return errors.New("localowner: household membership contention; retry latest projection")
}

func sameAccess(a, b HouseholdAppAccess) bool {
	return a.Scoped == b.Scoped && slices.Equal(a.Apps, b.Apps)
}

// convergeHouseholdAppClients rebinds every existing user-tier app client that
// still admits the flat household to the per-app admission set.
func (s *Service) convergeHouseholdAppClients(ctx context.Context, client pocketIDOwnerClient) error {
	for _, app := range householdapps.Grantable {
		clientID := ApplicationOIDCClientID(app)
		existing, err := client.GetOIDCClient(ctx, clientID)
		if errors.Is(err, pocketid.ErrNotFound) {
			continue
		}
		if err != nil || existing == nil {
			return errors.New("localowner: Pocket ID application client readback failed")
		}
		names := pocketIDGroupNames(existing.AllowedUserGroups)
		if !slices.Contains(names, "household") && !slices.Contains(names, householdOpenGroupName) {
			continue
		}
		ids, err := applicationOIDCGroupIDs(ctx, client, ApplicationOIDCClientRequest{ClientID: clientID, RequiredPrivilege: "user"})
		if err != nil {
			return err
		}
		if samePocketIDGroupIDs(existing.AllowedUserGroups, ids) {
			continue
		}
		if _, err := client.UpdateOIDCClientAllowedUserGroups(ctx, clientID, ids); err != nil {
			return errors.New("localowner: Pocket ID application client group binding failed")
		}
	}
	return nil
}

// SetHouseholdUserApps changes which node apps one existing household member
// may sign into. It is idempotent.
func (s *Service) SetHouseholdUserApps(ctx context.Context, username string, access HouseholdAppAccess) (HouseholdUser, error) {
	access, err := normalizeHouseholdAccess(access)
	if err != nil {
		return HouseholdUser{}, err
	}
	owner, client, err := s.ready(ctx)
	if err != nil {
		return HouseholdUser{}, err
	}
	if err := verifyPocketIDAdmin(ctx, client); err != nil {
		return HouseholdUser{}, err
	}
	if _, err := ensureHouseholdOpenGroup(ctx, client); err != nil {
		return HouseholdUser{}, err
	}
	username = strings.TrimSpace(username)
	if username == "" || username == owner.PocketID.Username {
		return HouseholdUser{}, errors.New("localowner: app grants require a non-owner household username")
	}
	found, err := client.FindUsersByUsername(ctx, username)
	if err != nil || len(found) != 1 {
		return HouseholdUser{}, errors.New("localowner: household subject is absent or ambiguous")
	}
	user := found[0]
	if user.IsAdmin || user.Disabled || !householdMember(user) || hasRequiredGroups(user.UserGroups) {
		return HouseholdUser{}, errors.New("localowner: app grants require an enabled non-owner household subject")
	}
	if err := s.applyHouseholdAccess(ctx, client, user, access); err != nil {
		return HouseholdUser{}, err
	}
	return HouseholdUser{Username: user.Username, Email: user.Email, DisplayName: effectiveDisplayName(user), Access: access}, nil
}
