package user

import (
	"context"

	"github.com/kombifyio/stackkits/internal/lifecyclemutation"
	"github.com/kombifyio/stackkits/internal/localowner"
)

type liveHousehold struct {
	workspace string
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

func (h liveHousehold) ListHouseholdUsers(ctx context.Context) ([]localowner.HouseholdUser, error) {
	service, err := localowner.NewService(h.workspace)
	if err != nil {
		return nil, err
	}
	return service.ListHouseholdUsers(ctx)
}

func (h liveHousehold) RemoveHouseholdUser(ctx context.Context, username string) error {
	return lifecyclemutation.WithIdleMutation(h.workspace, lifecyclemutation.JoinRequest{Command: "user remove"}, func() error {
		service, err := localowner.NewService(h.workspace)
		if err != nil {
			return err
		}
		return service.RemoveHouseholdUser(ctx, username)
	})
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
