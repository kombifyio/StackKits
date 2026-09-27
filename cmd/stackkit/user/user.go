// Package user implements stackkit user add/list/remove for PocketID household members.
package user

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

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
	OwnerActivationStatus(context.Context) (localowner.OwnerActivation, error)
	IssueOwnerActivation(context.Context) (localowner.OwnerActivation, error)
}

// FilesAccount is the secret-free result of app-local Cloudreve household
// provisioning. Selected is false when the current Plan has no Files workload.
type FilesAccount struct {
	Selected   bool
	AccountRef string
	Email      string
}

// FilesProvisioner is implemented by the root command package, which owns the
// admitted applied-workload execution channel. This package retains the public
// household CLI and never derives a runtime endpoint itself.
type FilesProvisioner interface {
	EnsureFilesAccount(context.Context, string, localowner.HouseholdUser) (FilesAccount, error)
	RevealFilesCredential(context.Context, string, localowner.HouseholdUser) ([]byte, error)
}

type commandResult struct {
	SchemaVersion string `json:"schemaVersion"`
	Command       string `json:"command"`
	Status        string `json:"status"`
	Data          any    `json:"data"`
}

// NewCommand returns the public `stackkit user` command tree.
func NewCommand() *cobra.Command {
	return newCommandWithFiles(nil, nil)
}

func newCommand(api householdAPI) *cobra.Command {
	return newCommandWithFiles(api, nil)
}

// NewCommandWithFilesProvisioner returns the public user tree with the native
// Files account hook supplied by the root command package.
func NewCommandWithFilesProvisioner(provisioner FilesProvisioner) *cobra.Command {
	return newCommandWithFiles(nil, provisioner)
}

func newCommandWithFiles(api householdAPI, provisioner FilesProvisioner) *cobra.Command {
	command := &cobra.Command{
		Use:   "user",
		Short: "Invite and manage household users in local PocketID",
		Long: `Create, list, and remove Homelab users in the PocketID household group.

Add prints a one-time passkey setup URL. TinyAuth continues to admit the
owners, admins, and household groups already bound on the OIDC client.
Owner and admin identities cannot be created or removed here.`,
		Example: `  # Invite a household member; share the printed one-time URL only with them
  stackkit user add alex --email alex@example.com --owner-approve

  # List household users
  stackkit user list

  # Remove a household user
  stackkit user remove alex --owner-approve`,
		Annotations: map[string]string{
			noDeployObservabilityAnnotation: "true",
		},
	}
	command.PersistentFlags().Bool("json", false, "Emit stackkit.command-result/v1 JSON")

	add := &cobra.Command{
		Use:   "add <username>",
		Short: "Invite a household user with a one-time passkey setup URL",
		Example: `  # Invite a household member and print their one-time passkey setup URL
  stackkit user add alex --email alex@example.com --owner-approve

  # Set a display name and return the invitation as JSON
  stackkit user add sam --email sam@example.com --display-name "Sam Doe" --owner-approve --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUserAdd(cmd, api, provisioner, args[0])
		},
	}
	add.Flags().String("email", "", "Household member email")
	add.Flags().String("display-name", "", "Optional display name")
	add.Flags().Bool("owner-approve", false, "Explicitly approve creating this household user")
	_ = add.MarkFlagRequired("email")

	list := &cobra.Command{
		Use:   "list",
		Short: "List household users in local PocketID",
		Example: `  # List household users
  stackkit user list

  # The same list as JSON
  stackkit user list --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runUserList(cmd, api)
		},
	}

	remove := &cobra.Command{
		Use:   "remove <username>",
		Short: "Remove a household user from local PocketID",
		Example: `  # Remove a household user
  stackkit user remove alex --owner-approve`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUserRemove(cmd, api, args[0])
		},
	}
	remove.Flags().Bool("owner-approve", false, "Explicitly approve removing this household user")

	owner := &cobra.Command{Use: "owner", Short: "Inspect and resume the PocketID owner passkey activation"}
	ownerStatus := &cobra.Command{
		Use: "status", Short: "Read the owner passkey activation status", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			service, err := resolveHousehold(cmd, api)
			if err != nil {
				return err
			}
			activation, err := service.OwnerActivationStatus(cmd.Context())
			if err != nil {
				return err
			}
			return writeOwnerActivation(cmd, activation, false)
		},
	}
	ownerActivate := &cobra.Command{
		Use: "activate", Short: "Reissue the owner-bound one-time passkey activation link and retire the previous one", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			approved, err := cmd.Flags().GetBool("owner-approve")
			if err != nil {
				return err
			}
			if !approved {
				return errors.New("owner activation requires explicit --owner-approve")
			}
			service, err := resolveHousehold(cmd, api)
			if err != nil {
				return err
			}
			activation, err := service.IssueOwnerActivation(cmd.Context())
			if err != nil {
				return err
			}
			return writeOwnerActivation(cmd, activation, true)
		},
	}
	ownerActivate.Flags().Bool("owner-approve", false, "Explicitly approve reissuing the owner activation link")
	owner.AddCommand(ownerStatus, ownerActivate, newDomainMigrationCommand(api))

	command.AddCommand(add, list, remove, owner, newHouseholdActivateCommand(api), newFilesCommand(api, provisioner))
	return command
}

func writeOwnerActivation(cmd *cobra.Command, activation localowner.OwnerActivation, includeURL bool) error {
	data := map[string]string{"status": activation.Status, "origin": activation.Origin}
	if !activation.ExpiresAt.IsZero() {
		data["expiresAt"] = activation.ExpiresAt.UTC().Format(time.RFC3339)
	}
	if includeURL && activation.SetupURL != "" {
		data["activationURL"] = activation.SetupURL
	}
	if jsonOutput(cmd) {
		return writeJSON(cmd, data)
	}
	_, err := fmt.Fprintln(cmd.OutOrStdout(), activation.Status)
	if err == nil && includeURL && activation.SetupURL != "" {
		_, err = fmt.Fprintln(cmd.OutOrStdout(), activation.SetupURL)
	}
	return err
}

func runUserAdd(cmd *cobra.Command, api householdAPI, provisioner FilesProvisioner, username string) error {
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
	var files FilesAccount
	if provisioner != nil {
		workspace, workspaceErr := workspaceRoot(cmd)
		if workspaceErr != nil {
			return workspaceErr
		}
		files, err = provisioner.EnsureFilesAccount(cmd.Context(), workspace, invited)
		if err != nil {
			if outputErr := writeHouseholdInvitation(cmd, invited, FilesAccount{}, "failed"); outputErr != nil {
				return errors.Join(err, outputErr)
			}
			return fmt.Errorf("Files account remains pending; retry with `stackkit user files ensure %s --owner-approve`: %w", invited.Username, err)
		}
	}
	return writeHouseholdInvitation(cmd, invited, files, "success")
}

func writeHouseholdInvitation(cmd *cobra.Command, invited localowner.HouseholdUser, files FilesAccount, status string) error {
	if jsonOutput(cmd) {
		data := map[string]any{
			"username":    invited.Username,
			"email":       invited.Email,
			"displayName": invited.DisplayName,
			"status":      invited.Status,
		}
		if invited.SetupURL != "" {
			data["setupURL"] = invited.SetupURL
		}
		if !invited.ExpiresAt.IsZero() {
			data["expiresAt"] = invited.ExpiresAt.UTC().Format(time.RFC3339)
		}
		if files.Selected {
			data["files"] = map[string]string{
				"status": "ready", "accountRef": files.AccountRef,
				"credentialCommand": "stackkit user files credential " + invited.Username + " --owner-approve",
			}
		} else if status != "success" {
			data["partial"] = true
			data["files"] = map[string]string{
				"status": "pending", "retryCommand": "stackkit user files ensure " + invited.Username + " --owner-approve",
			}
		}
		return writeJSONStatus(cmd, data, status)
	}
	_, err := fmt.Fprintf(
		cmd.OutOrStdout(),
		"Invited household user %s\nOne-time setup URL: %s\nShare this URL only with that person so they can enroll a passkey.\n",
		invited.Username,
		invited.SetupURL,
	)
	if err == nil && files.Selected {
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "Files account ready. Reveal its password intentionally with: stackkit user files credential %s --owner-approve\n", invited.Username)
	} else if err == nil && status != "success" {
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "Files account pending. Retry with: stackkit user files ensure %s --owner-approve\n", invited.Username)
	}
	return err
}

func newFilesCommand(api householdAPI, provisioner FilesProvisioner) *cobra.Command {
	files := &cobra.Command{
		Use: "files", Short: "Resume and hand off app-local Files household accounts",
		Long: "Cloudreve CE keeps an app-local account behind TinyAuth. These commands converge that account from an approved PocketID household identity and reveal its owner-custodied password only on explicit request.",
	}
	ensure := &cobra.Command{
		Use: "ensure <username>", Short: "Create or verify one non-admin Files account", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireOwnerApproval(cmd); err != nil {
				return err
			}
			if provisioner == nil {
				return errors.New("Files household provisioning is unavailable in this command host")
			}
			service, err := resolveHousehold(cmd, api)
			if err != nil {
				return err
			}
			member, err := exactHouseholdUser(cmd.Context(), service, args[0])
			if err != nil {
				return err
			}
			workspace, err := workspaceRoot(cmd)
			if err != nil {
				return err
			}
			account, err := provisioner.EnsureFilesAccount(cmd.Context(), workspace, member)
			if err != nil {
				return err
			}
			if !account.Selected {
				return errors.New("the current Plan does not select Cloudreve Files")
			}
			if jsonOutput(cmd) {
				return writeJSON(cmd, map[string]string{
					"username": member.Username, "email": account.Email, "accountRef": account.AccountRef,
					"credentialCommand": "stackkit user files credential " + member.Username + " --owner-approve",
				})
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Files account ready for %s. Reveal its password intentionally with: stackkit user files credential %s --owner-approve\n", member.Username, member.Username)
			return err
		},
	}
	ensure.Flags().Bool("owner-approve", false, "Explicitly approve Files account provisioning")

	credential := &cobra.Command{
		Use: "credential <username>", Short: "Print one owner-custodied Files household password", Args: cobra.ExactArgs(1),
		Long: "Verify the approved PocketID household identity and its non-admin Cloudreve account, then print the existing password from signed local-owner custody. Output contains secret material; use it only in a private terminal or intentional pipe.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireOwnerApproval(cmd); err != nil {
				return err
			}
			if jsonOutput(cmd) {
				return errors.New("Files credential reveal emits only the raw password; omit --json")
			}
			if provisioner == nil {
				return errors.New("Files household credential reveal is unavailable in this command host")
			}
			service, err := resolveHousehold(cmd, api)
			if err != nil {
				return err
			}
			member, err := exactHouseholdUser(cmd.Context(), service, args[0])
			if err != nil {
				return err
			}
			workspace, err := workspaceRoot(cmd)
			if err != nil {
				return err
			}
			material, err := provisioner.RevealFilesCredential(cmd.Context(), workspace, member)
			if err != nil {
				return err
			}
			defer clear(material)
			if _, err := cmd.OutOrStdout().Write(material); err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout())
			return err
		},
	}
	credential.Flags().Bool("owner-approve", false, "Explicitly approve revealing this Files password")
	files.AddCommand(ensure, credential)
	return files
}

func requireOwnerApproval(cmd *cobra.Command) error {
	approved, err := cmd.Flags().GetBool("owner-approve")
	if err != nil {
		return err
	}
	if !approved {
		return errors.New("Files household account action requires explicit --owner-approve")
	}
	return nil
}

func exactHouseholdUser(ctx context.Context, api householdAPI, username string) (localowner.HouseholdUser, error) {
	username = strings.TrimSpace(username)
	users, err := api.ListHouseholdUsers(ctx)
	if err != nil {
		return localowner.HouseholdUser{}, err
	}
	var matched *localowner.HouseholdUser
	for i := range users {
		if users[i].Username != username {
			continue
		}
		if matched != nil {
			return localowner.HouseholdUser{}, errors.New("PocketID household identity is ambiguous")
		}
		candidate := users[i]
		matched = &candidate
	}
	if matched == nil || strings.TrimSpace(matched.Email) == "" {
		return localowner.HouseholdUser{}, errors.New("PocketID household identity is absent")
	}
	return *matched, nil
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
				"status":      user.Status,
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
	return writeJSONStatus(cmd, data, "success")
}

func writeJSONStatus(cmd *cobra.Command, data any, status string) error {
	encoder := json.NewEncoder(cmd.OutOrStdout())
	encoder.SetIndent("", "  ")
	return encoder.Encode(commandResult{
		SchemaVersion: commandResultSchemaVersion,
		Command:       cmd.CommandPath(),
		Status:        status,
		Data:          data,
	})
}
