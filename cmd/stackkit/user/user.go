// Package user implements stackkit user add/list/remove for PocketID household members.
package user

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/kombifyio/stackkits/internal/localowner"
	"github.com/spf13/cobra"
)

const (
	noDeployObservabilityAnnotation = "stackkit.io/no-deploy-observability"
	commandResultSchemaVersion      = "stackkit.command-result/v1"
)

type householdAPI interface {
	AddHouseholdUser(context.Context, localowner.HouseholdUserSpec) (localowner.HouseholdUser, error)
	ListHouseholdUsers(context.Context) ([]localowner.HouseholdUser, error)
	RemoveHouseholdUser(context.Context, string) error
}

type commandResult struct {
	SchemaVersion string `json:"schemaVersion"`
	Command       string `json:"command"`
	Status        string `json:"status"`
	Data          any    `json:"data"`
}

// NewCommand returns the public `stackkit user` command tree.
func NewCommand() *cobra.Command {
	return newCommand(nil)
}

func newCommand(api householdAPI) *cobra.Command {
	command := &cobra.Command{
		Use:   "user",
		Short: "Invite and manage household users in local PocketID",
		Long: `Create, list, and remove Homelab users in the PocketID household group.

Add prints a one-time passkey setup URL. TinyAuth continues to admit the
owners, admins, and household groups already bound on the OIDC client.
Owner and admin identities cannot be created or removed here.

Examples:
  stackkit user add alex --email alex@home.test --owner-approve
  stackkit user list
  stackkit user remove alex --owner-approve`,
		Annotations: map[string]string{
			noDeployObservabilityAnnotation: "true",
		},
	}
	command.PersistentFlags().Bool("json", false, "Emit stackkit.command-result/v1 JSON")

	add := &cobra.Command{
		Use:   "add <username>",
		Short: "Invite a household user with a one-time passkey setup URL",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUserAdd(cmd, api, args[0])
		},
	}
	add.Flags().String("email", "", "Household member email")
	add.Flags().String("display-name", "", "Optional display name")
	add.Flags().Bool("owner-approve", false, "Explicitly approve creating this household user")
	_ = add.MarkFlagRequired("email")

	list := &cobra.Command{
		Use:   "list",
		Short: "List household users in local PocketID",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runUserList(cmd, api)
		},
	}

	remove := &cobra.Command{
		Use:   "remove <username>",
		Short: "Remove a household user from local PocketID",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUserRemove(cmd, api, args[0])
		},
	}
	remove.Flags().Bool("owner-approve", false, "Explicitly approve removing this household user")

	command.AddCommand(add, list, remove)
	return command
}

func runUserAdd(cmd *cobra.Command, api householdAPI, username string) error {
	approved, err := cmd.Flags().GetBool("owner-approve")
	if err != nil {
		return err
	}
	if !approved {
		return errors.New("user add requires explicit --owner-approve")
	}
	email, err := cmd.Flags().GetString("email")
	if err != nil {
		return err
	}
	displayName, err := cmd.Flags().GetString("display-name")
	if err != nil {
		return err
	}
	service, err := resolveHousehold(cmd, api)
	if err != nil {
		return err
	}
	invited, err := service.AddHouseholdUser(cmd.Context(), localowner.HouseholdUserSpec{
		Username:    strings.TrimSpace(username),
		Email:       strings.TrimSpace(email),
		DisplayName: strings.TrimSpace(displayName),
	})
	if err != nil {
		return err
	}
	if jsonOutput(cmd) {
		return writeJSON(cmd, map[string]string{
			"username":    invited.Username,
			"email":       invited.Email,
			"displayName": invited.DisplayName,
			"setupURL":    invited.SetupURL,
		})
	}
	_, err = fmt.Fprintf(
		cmd.OutOrStdout(),
		"Invited household user %s\nOne-time setup URL: %s\nShare this URL only with that person so they can enroll a passkey.\n",
		invited.Username,
		invited.SetupURL,
	)
	return err
}

func runUserList(cmd *cobra.Command, api householdAPI) error {
	service, err := resolveHousehold(cmd, api)
	if err != nil {
		return err
	}
	users, err := service.ListHouseholdUsers(cmd.Context())
	if err != nil {
		return err
	}
	if jsonOutput(cmd) {
		items := make([]map[string]string, 0, len(users))
		for _, user := range users {
			items = append(items, map[string]string{
				"username":    user.Username,
				"email":       user.Email,
				"displayName": user.DisplayName,
			})
		}
		return writeJSON(cmd, map[string]any{"users": items})
	}
	if len(users) == 0 {
		_, err = fmt.Fprintln(cmd.OutOrStdout(), "No household users.")
		return err
	}
	for _, user := range users {
		if _, err = fmt.Fprintf(
			cmd.OutOrStdout(),
			"%s\t%s\t%s\n",
			user.Username,
			user.Email,
			user.DisplayName,
		); err != nil {
			return err
		}
	}
	return nil
}

func runUserRemove(cmd *cobra.Command, api householdAPI, username string) error {
	approved, err := cmd.Flags().GetBool("owner-approve")
	if err != nil {
		return err
	}
	if !approved {
		return errors.New("user remove requires explicit --owner-approve")
	}
	service, err := resolveHousehold(cmd, api)
	if err != nil {
		return err
	}
	username = strings.TrimSpace(username)
	if err := service.RemoveHouseholdUser(cmd.Context(), username); err != nil {
		return err
	}
	if jsonOutput(cmd) {
		return writeJSON(cmd, map[string]string{"username": username})
	}
	_, err = fmt.Fprintf(cmd.OutOrStdout(), "Removed household user %s\n", username)
	return err
}

func resolveHousehold(cmd *cobra.Command, api householdAPI) (householdAPI, error) {
	if api != nil {
		return api, nil
	}
	root, err := workspaceRoot(cmd)
	if err != nil {
		return nil, err
	}
	return liveHousehold{workspace: root}, nil
}

func workspaceRoot(cmd *cobra.Command) (string, error) {
	raw := "."
	if cmd != nil {
		if flag := cmd.Flag("chdir"); flag != nil {
			if value := strings.TrimSpace(flag.Value.String()); value != "" {
				raw = value
			}
		}
	}
	return filepath.Abs(raw)
}

func jsonOutput(cmd *cobra.Command) bool {
	if cmd == nil {
		return false
	}
	value, err := cmd.Flags().GetBool("json")
	return err == nil && value
}

func writeJSON(cmd *cobra.Command, data any) error {
	encoder := json.NewEncoder(cmd.OutOrStdout())
	encoder.SetIndent("", "  ")
	return encoder.Encode(commandResult{
		SchemaVersion: commandResultSchemaVersion,
		Command:       cmd.CommandPath(),
		Status:        "success",
		Data:          data,
	})
}
