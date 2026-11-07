package user

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/kombifyio/stackkits/internal/lifecyclemutation"
	"github.com/kombifyio/stackkits/internal/localowner"
	"github.com/kombifyio/stackkits/internal/stackspecintent"
	"github.com/spf13/cobra"
)

type domainMigrationAPI interface {
	MigrateBasementDomain(ctx context.Context, specPath, to string, quiesceRuntime bool) (localowner.DomainMigration, error)
}
type householdActivationAPI interface {
	ActivateHouseholdUser(context.Context, string) (localowner.HouseholdUser, error)
}
type householdPasskeyResetAPI interface {
	ResetHouseholdPasskey(context.Context, string) (localowner.HouseholdUser, error)
}

func newDomainMigrationCommand(api householdAPI) *cobra.Command {
	command := &cobra.Command{
		Use: "migrate-domain", Short: "Prepare the owner-approved identity domain migration (home to lab.home, or lab.home to a public id.<name>)", Args: cobra.NoArgs,
		Long: "Preserve the existing owner, CA, service secrets and users while migrating Basement intent and runtime custody from home to lab.home. Preserve the prior-release CLI, packaged tools and provider mirror before using an isolated target CLI for this command. --quiesce-runtime authorizes the command to capture the exact source Core container identities, converge the identity callback while PocketID is available, then gracefully stop and remove only those containers without deleting volumes, networks or images. Use the prior CLI for generate, apply and verify, then use the target CLI for upgrade --to <exact-release>. The upgrade checkpoint covers the migrated prior release on lab.home; this command creates no original-domain rollback checkpoint. The v0.47.4 bridge awaits real-host qualification. After rollout, enroll client DNS and new passkeys at id.lab.home.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			approved, _ := cmd.Flags().GetBool("owner-approve")
			if !approved {
				return errors.New("domain migration requires explicit --owner-approve")
			}
			quiesceRuntime, _ := cmd.Flags().GetBool("quiesce-runtime")
			if !quiesceRuntime {
				return errors.New("domain migration requires explicit --quiesce-runtime")
			}
			specPath, _ := cmd.Flags().GetString("spec")
			service, err := resolveHousehold(cmd, api)
			if err != nil {
				return err
			}
			migrator, ok := service.(domainMigrationAPI)
			if !ok {
				return errors.New("domain migration is unavailable")
			}
			to, _ := cmd.Flags().GetString("to")
			result, err := migrator.MigrateBasementDomain(cmd.Context(), specPath, strings.ToLower(strings.TrimSpace(to)), quiesceRuntime)
			if err != nil {
				return err
			}
			if jsonOutput(cmd) {
				return writeJSON(cmd, result)
			}
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Prepared %s -> %s and quiesced the authenticated source Core runtime. Rollout and passkey reenrollment are still required.\n", result.From, result.To); err != nil {
				return err
			}
			for _, step := range result.Next {
				if _, err := fmt.Fprintln(cmd.OutOrStdout(), step); err != nil {
					return err
				}
			}
			return nil
		},
	}
	command.Flags().Bool("owner-approve", false, "Explicitly approve domain migration and new passkey enrollment")
	command.Flags().Bool("quiesce-runtime", false, "Gracefully stop and remove the authenticated source Core containers without deleting persistent resources")
	command.Flags().String("spec", "stack-spec.yaml", "Workspace-relative StackSpec to migrate")
	command.Flags().String("to", "lab.home", "Target identity domain: lab.home for the retired home default, or a public name (a kombify.me name or an own domain, giving https://id.<name>) for a local-only install")
	return command
}

func newHouseholdActivateCommand(api householdAPI) *cobra.Command {
	command := &cobra.Command{
		Use: "activate <username>", Short: "Reissue passkey enrollment for an existing household member", Args: cobra.ExactArgs(1),
		Long: "Reissue a one-time passkey registration link for a household member who has no passkey yet. With --reset the Owner resets the passkey of a member who lost every usable passkey: the link is issued although Pocket ID still lists the lost ones, which the member then removes in their account settings. The Owner recovers their own account with the recovery code (stackkit break-glass redeem).",
		Example: `  # Reset the passkey of a member who lost their phone
  stackkit user activate alex --reset --owner-approve`,
		RunE: func(cmd *cobra.Command, args []string) error {
			approved, _ := cmd.Flags().GetBool("owner-approve")
			if !approved {
				return errors.New("household activation requires explicit --owner-approve")
			}
			service, err := resolveHousehold(cmd, api)
			if err != nil {
				return err
			}
			var user localowner.HouseholdUser
			if reset, _ := cmd.Flags().GetBool("reset"); reset {
				resetter, ok := service.(householdPasskeyResetAPI)
				if !ok {
					return errors.New("household passkey reset is unavailable")
				}
				user, err = resetter.ResetHouseholdPasskey(cmd.Context(), args[0])
			} else {
				activator, ok := service.(householdActivationAPI)
				if !ok {
					return errors.New("household activation is unavailable")
				}
				user, err = activator.ActivateHouseholdUser(cmd.Context(), args[0])
			}
			if err != nil {
				return err
			}
			if jsonOutput(cmd) {
				return writeJSON(cmd, map[string]any{"username": user.Username, "status": user.Status, "setupURL": user.SetupURL, "expiresAt": user.ExpiresAt})
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n%s\n", user.Username, user.Status, user.SetupURL)
			return err
		},
	}
	command.Flags().Bool("owner-approve", false, "Explicitly approve reissuing household passkey enrollment")
	command.Flags().Bool("reset", false, "Reset the passkey of a member who lost every usable passkey")
	return command
}

func (h liveHousehold) MigrateBasementDomain(ctx context.Context, specPath, to string, quiesceRuntime bool) (localowner.DomainMigration, error) {
	var result localowner.DomainMigration
	err := stackspecintent.WithBasementDomainMigrator(h.workspace, lifecyclemutation.JoinRequest{Command: "user owner migrate-domain"}, func(intent stackspecintent.BasementDomainMigrator) error {
		service, err := localowner.NewService(h.workspace)
		if err != nil {
			return err
		}
		result, err = service.MigrateBasementDomain(ctx, specPath, to, intent, quiesceRuntime)
		return err
	})
	return result, err
}

func (h liveHousehold) ActivateHouseholdUser(ctx context.Context, username string) (localowner.HouseholdUser, error) {
	var result localowner.HouseholdUser
	err := lifecyclemutation.WithIdleMutation(h.workspace, lifecyclemutation.JoinRequest{Command: "user activate"}, func() error {
		service, err := localowner.NewService(h.workspace)
		if err != nil {
			return err
		}
		result, err = service.ActivateHouseholdUser(ctx, username)
		return err
	})
	return result, err
}

func (h liveHousehold) ResetHouseholdPasskey(ctx context.Context, username string) (localowner.HouseholdUser, error) {
	var result localowner.HouseholdUser
	err := lifecyclemutation.WithIdleMutation(h.workspace, lifecyclemutation.JoinRequest{Command: "user activate --reset"}, func() error {
		service, err := localowner.NewService(h.workspace)
		if err != nil {
			return err
		}
		result, err = service.ResetHouseholdPasskey(ctx, username)
		return err
	})
	return result, err
}
