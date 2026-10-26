package user

import (
	"context"
	"errors"
	"strings"

	"github.com/kombifyio/stackkits/internal/localowner"
	"github.com/spf13/cobra"
)

type householdProjector interface {
	ProjectHouseholdUser(context.Context, localowner.HouseholdProjection) (localowner.HouseholdUser, error)
}

func newProjectionCommand(api householdAPI) *cobra.Command {
	command := &cobra.Command{
		Use: "project <username>", Short: "Apply a revision-bound household identity projection", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireOwnerApproval(cmd); err != nil {
				return err
			}
			service, err := resolveHousehold(cmd, api)
			if err != nil {
				return err
			}
			projector, ok := service.(householdProjector)
			if !ok {
				return errors.New("household identity projection is unavailable")
			}
			key, _ := cmd.Flags().GetString("projection-key")
			revision, _ := cmd.Flags().GetUint64("revision")
			action, _ := cmd.Flags().GetString("action")
			email, _ := cmd.Flags().GetString("email")
			name, _ := cmd.Flags().GetString("display-name")
			apps, _ := cmd.Flags().GetStringArray("app")
			all, _ := cmd.Flags().GetBool("all")
			none, _ := cmd.Flags().GetBool("none")
			if action == "remove" {
				if email != "" || name != "" || len(apps) > 0 || all || none {
					return errors.New("household removal carries no profile or app grants")
				}
			} else {
				chosen := 0
				for _, set := range []bool{all, none, len(apps) > 0} {
					if set {
						chosen++
					}
				}
				if action != "add" || chosen != 1 {
					return errors.New("household add requires exactly one of --all, --none or --app")
				}
			}
			member, err := projector.ProjectHouseholdUser(cmd.Context(), localowner.HouseholdProjection{
				Key: key, Revision: revision, Action: action,
				User: localowner.HouseholdUserSpec{Username: strings.TrimSpace(args[0]), Email: email, DisplayName: name,
					Access: localowner.HouseholdAppAccess{Scoped: action == "add" && !all, Apps: apps}},
			})
			if err != nil {
				return err
			}
			return writeHouseholdInvitation(cmd, member, FilesAccount{}, "success")
		},
	}
	command.Flags().String("projection-key", "", "Owner/person projection digest")
	command.Flags().Uint64("revision", 0, "Monotonic Cloud generation")
	command.Flags().String("action", "", "Desired identity action: add or remove")
	command.Flags().String("email", "", "Household member email")
	command.Flags().String("display-name", "", "Household member display name")
	command.Flags().StringArray("app", nil, "App this member may sign into (repeatable)")
	command.Flags().Bool("all", false, "Allow every app")
	command.Flags().Bool("none", false, "Allow no app")
	command.Flags().Bool("owner-approve", false, "Approve the exact household identity projection")
	return command
}
