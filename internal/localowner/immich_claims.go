package localowner

import (
	"context"
	"errors"
	"maps"
	"sort"

	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/pocketid"
)

const immichRoleClaim = "immich_role"

type userClaimsUpdater interface {
	UpdateUserCustomClaims(context.Context, string, []pocketid.CustomClaim) error
}

// EnsureImmichUserClaims converges the application role of the exact signed
// owner and every household subject before Photos is applied. It preserves
// unrelated per-user claims, memberships, credentials and Pocket ID admin flags.
// Verify never calls this mutating operation. Callers hold the lifecycle lock.
func (s *Service) EnsureImmichUserClaims(ctx context.Context) error {
	binding, err := s.Verify(ctx)
	if err != nil {
		return err
	}
	owner, client, err := s.ready(ctx)
	if err != nil {
		return err
	}
	photos, err := client.GetOIDCClient(ctx, ApplicationOIDCClientID("photos"))
	if err != nil {
		return err
	}
	if photos == nil || photos.ID != ApplicationOIDCClientID("photos") || photos.IsPublic || !photos.IsGroupRestricted {
		return errors.New("localowner: Immich claims require the confidential restricted Photos client")
	}
	updater, ok := client.(userClaimsUpdater)
	if !ok {
		return errors.New("localowner: Pocket ID client cannot reconcile per-user claims")
	}
	users, err := client.ListUsers(ctx)
	if err != nil {
		return err
	}
	if err := reconcileImmichUserRole(ctx, client, updater, owner, binding.PocketIDSubject, binding.PocketIDSubject); err != nil {
		return err
	}
	for _, user := range users {
		if user.ID == binding.PocketIDSubject || !householdMember(user) {
			continue
		}
		if err := reconcileImmichUserRole(ctx, client, updater, owner, binding.PocketIDSubject, user.ID); err != nil {
			return err
		}
	}
	return nil
}

// Existing installations gain claims on their next Apply/Realize. New or
// resumed invitations reconcile after creation but before exposing enrollment.
// An installation without Photos performs no custom-claim mutations.
func (s *Service) ensureConfiguredImmichUserClaims(ctx context.Context, client pocketIDOwnerClient) error {
	_, err := client.GetOIDCClient(ctx, ApplicationOIDCClientID("photos"))
	if errors.Is(err, pocketid.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return s.EnsureImmichUserClaims(ctx)
}

func reconcileImmichUserRole(ctx context.Context, client pocketIDOwnerClient, updater userClaimsUpdater, owner localevidence.OwnerCustody, ownerSubject, subject string) error {
	current, err := client.GetUser(ctx, subject)
	if err != nil || current == nil || current.ID != subject {
		return errors.New("localowner: claim reconciliation could not read the exact subject")
	}
	desiredRole := "user"
	if subject == ownerSubject {
		if err := validatePocketIDOwner(owner, *current); err != nil {
			return err
		}
		desiredRole = "admin"
	} else if !householdMember(*current) {
		return errors.New("localowner: household membership changed during claim reconciliation")
	}
	claims, err := customClaimMap(current.CustomClaims)
	if err != nil {
		return err
	}
	if claims[immichRoleClaim] == desiredRole {
		return nil
	}
	claims[immichRoleClaim] = desiredRole
	keys := make([]string, 0, len(claims))
	for key := range claims {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	merged := make([]pocketid.CustomClaim, 0, len(keys))
	for _, key := range keys {
		merged = append(merged, pocketid.CustomClaim{Key: key, Value: claims[key]})
	}
	if err := updater.UpdateUserCustomClaims(ctx, subject, merged); err != nil {
		return err
	}
	readback, err := client.GetUser(ctx, subject)
	if err != nil || readback == nil || readback.ID != subject {
		return errors.New("localowner: custom claim readback failed")
	}
	actual, err := customClaimMap(readback.CustomClaims)
	if err != nil {
		return err
	}
	if !maps.Equal(actual, claims) {
		return errors.New("localowner: custom claim readback differs from the merged per-user claims")
	}
	if readback.IsAdmin != current.IsAdmin || readback.Disabled != current.Disabled || !samePocketIDGroupIDs(readback.UserGroups, requiredAndExistingGroupIDs(nil, current.UserGroups)) {
		return errors.New("localowner: subject privileges changed during custom claim reconciliation")
	}
	return nil
}

func customClaimMap(claims []pocketid.CustomClaim) (map[string]string, error) {
	result := make(map[string]string, len(claims))
	for _, claim := range claims {
		if _, exists := result[claim.Key]; exists || claim.Key == "" || claim.Value == "" {
			return nil, errors.New("localowner: existing custom claims are ambiguous or invalid")
		}
		result[claim.Key] = claim.Value
	}
	return result, nil
}
