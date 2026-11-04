package user

import (
	"context"
	"time"

	"github.com/kombifyio/stackkits/internal/identity"
	"github.com/kombifyio/stackkits/internal/lifecyclemutation"
	"github.com/kombifyio/stackkits/internal/localowner"
)

type liveHousehold struct {
	workspace string
}

func (h liveHousehold) ProjectHouseholdUser(ctx context.Context, request localowner.HouseholdProjection) (localowner.HouseholdUser, error) {
	var member localowner.HouseholdUser
	err := lifecyclemutation.WithIdleMutation(h.workspace, lifecyclemutation.JoinRequest{Command: "user project"}, func() error {
		service, err := localowner.NewService(h.workspace)
		if err != nil {
			return err
		}
		member, err = service.ProjectHouseholdUser(ctx, request, func(_ context.Context, subject string) error {
			_, revokeErr := identity.RevokeHomeAccount(h.workspace, subject, time.Now())
			return revokeErr
		})
		return err
	})
	return member, err
}

func (h liveHousehold) AddHouseholdUser(ctx context.Context, spec localowner.HouseholdUserSpec) (localowner.HouseholdUser, error) {
	var invited localowner.HouseholdUser
	err := lifecyclemutation.WithIdleMutation(h.workspace, lifecyclemutation.JoinRequest{Command: "user add"}, func() error {
		service, err := localowner.NewService(h.workspace)
		if err != nil {
			return err
		}
		invited, err = service.AddHouseholdUser(ctx, spec)
		return err
	})
	return invited, err
}

func (h liveHousehold) SetHouseholdUserApps(ctx context.Context, username string, access localowner.HouseholdAppAccess) (localowner.HouseholdUser, error) {
	var member localowner.HouseholdUser
	err := lifecyclemutation.WithIdleMutation(h.workspace, lifecyclemutation.JoinRequest{Command: "user apps"}, func() error {
		service, err := localowner.NewService(h.workspace)
		if err != nil {
			return err
		}
		member, err = service.SetHouseholdUserApps(ctx, username, access)
		return err
	})
	return member, err
}

func (h liveHousehold) ListHouseholdUsers(ctx context.Context) ([]localowner.HouseholdUser, error) {
	service, err := localowner.NewService(h.workspace)
	if err != nil {
		return nil, err
	}
	return service.ListHouseholdUsers(ctx)
}

// RemoveHouseholdUser runs the local removal chain (target §10): the
// member's devices, credentials, VPN peers and account revocation first, then
// the PocketID user, whose deletion ends its passkeys and PocketID sessions.
func (h liveHousehold) RemoveHouseholdUser(ctx context.Context, username string) (identity.HomeRevocation, error) {
	var removal identity.HomeRevocation
	err := lifecyclemutation.WithIdleMutation(h.workspace, lifecyclemutation.JoinRequest{Command: "user remove"}, func() error {
		service, err := localowner.NewService(h.workspace)
		if err != nil {
			return err
		}
		_, err = service.RemoveHouseholdUser(ctx, username, func(_ context.Context, subject string) error {
			var revokeErr error
			removal, revokeErr = identity.RevokeHomeAccount(h.workspace, subject, time.Now())
			return revokeErr
		})
		return err
	})
	if err != nil {
		return identity.HomeRevocation{}, err
	}
	for index := range removal.Artifacts {
		if removal.Artifacts[index].ID == identity.HomeArtifactAccount {
			removal.Artifacts[index].Detail = "PocketID user deleted (passkeys and PocketID sessions end); " + removal.Artifacts[index].Detail
		}
	}
	return removal, nil
}

func (h liveHousehold) OwnerActivationStatus(ctx context.Context) (localowner.OwnerActivation, error) {
	service, err := localowner.NewService(h.workspace)
	if err != nil {
		return localowner.OwnerActivation{}, err
	}
	return service.OwnerActivationStatus(ctx)
}

func (h liveHousehold) IssueOwnerActivation(ctx context.Context) (localowner.OwnerActivation, error) {
	var activation localowner.OwnerActivation
	err := lifecyclemutation.WithIdleMutation(h.workspace, lifecyclemutation.JoinRequest{Command: "user owner activate"}, func() error {
		service, err := localowner.NewService(h.workspace)
		if err != nil {
			return err
		}
		activation, err = service.IssueOwnerActivation(ctx)
		return err
	})
	return activation, err
}
