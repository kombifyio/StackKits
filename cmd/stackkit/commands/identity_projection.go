package commands

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/identityprojection"
	"github.com/spf13/cobra"
)

const maxIdentityProjectionBytes = 64 * 1024

var (
	identityProjectionFile    string
	identityProjectionDigest  string
	identityProjectionOwnerOK bool
	identityProjectionJSON    bool
)

var identityCmd = &cobra.Command{
	Use:   "identity",
	Short: "Manage the standalone local identity plane",
	Annotations: map[string]string{
		noDeployObservabilityAnnotation: "true",
	},
}

var identityProjectionCmd = &cobra.Command{
	Use:   "projection",
	Short: "Inspect and locally approve credential-free desired identity projections",
}

var identityProjectionInspectCmd = &cobra.Command{
	Use:   "inspect",
	Short: "Verify one signed projection without mutation",
	Args:  cobra.NoArgs,
	RunE:  runIdentityProjectionInspect,
}

var identityProjectionApproveCmd = &cobra.Command{
	Use:   "approve",
	Short: "Create the local Owner-signed approval without PocketID mutation",
	Args:  cobra.NoArgs,
	RunE:  runIdentityProjectionApprove,
}

var identityProjectionApplyCmd = &cobra.Command{
	Use:   "apply",
	Short: "Apply one previously approved projection to local PocketID",
	Args:  cobra.NoArgs,
	RunE:  runIdentityProjectionApply,
}

var identityProjectionUnlinkCmd = &cobra.Command{
	Use:   "unlink",
	Short: "Detach optional sync without deleting any local identity",
	Args:  cobra.NoArgs,
	RunE:  runIdentityProjectionUnlink,
}

func init() {
	for _, command := range []*cobra.Command{
		identityProjectionInspectCmd,
		identityProjectionApproveCmd,
	} {
		command.Flags().StringVar(
			&identityProjectionFile,
			"file",
			"",
			"Path to a canonical stackkit.desired-identity-projection/v1 document",
		)
		command.Flags().BoolVar(
			&identityProjectionJSON, "json", false,
			"Emit stackkit.command-result/v1 JSON",
		)
	}
	identityProjectionApproveCmd.Flags().BoolVar(
		&identityProjectionOwnerOK,
		"owner-approve",
		false,
		"Explicitly approve importing this desired projection into local custody",
	)
	for _, command := range []*cobra.Command{
		identityProjectionApplyCmd,
		identityProjectionUnlinkCmd,
	} {
		command.Flags().StringVar(
			&identityProjectionDigest,
			"projection-sha256",
			"",
			"Exact sha256:<hex> of a locally approved projection",
		)
		command.Flags().BoolVar(
			&identityProjectionOwnerOK,
			"owner-approve",
			false,
			"Explicitly approve this local identity operation",
		)
		command.Flags().BoolVar(
			&identityProjectionJSON, "json", false,
			"Emit stackkit.command-result/v1 JSON",
		)
	}
	identityProjectionCmd.AddCommand(
		identityProjectionInspectCmd,
		identityProjectionApproveCmd,
		identityProjectionApplyCmd,
		identityProjectionUnlinkCmd,
	)
	identityCmd.AddCommand(identityProjectionCmd)
	rootCmd.AddCommand(identityCmd)
}

func runIdentityProjectionInspect(cmd *cobra.Command, _ []string) error {
	raw, service, err := readIdentityProjectionInput()
	if err != nil {
		return err
	}
	result, err := service.Inspect(raw, time.Now().UTC().Truncate(time.Second))
	if err != nil {
		return err
	}
	if identityProjectionJSON {
		return writeCommandResult(cmd, cmd.CommandPath(), result)
	}
	_, err = fmt.Fprintf(
		cmd.OutOrStdout(),
		"Projection: %s\nDigest: %s\nIssuer: %s\nExpires: %s\nCredential-free: true\n",
		result.ProjectionID,
		result.ProjectionSHA256,
		result.IssuerID,
		result.ExpiresAt.Format(time.RFC3339),
	)
	return err
}

func runIdentityProjectionApprove(cmd *cobra.Command, _ []string) error {
	if !identityProjectionOwnerOK {
		return errors.New("identity projection approve requires explicit --owner-approve")
	}
	raw, service, err := readIdentityProjectionInput()
	if err != nil {
		return err
	}
	now := time.Now().UTC().Truncate(time.Second)
	if _, err := service.Inspect(raw, now); err != nil {
		return err
	}
	var approval identityprojection.Approval
	err = withLifecycleMutation(getWorkDir(), "identity projection approve", func() error {
		var approveErr error
		approval, approveErr = service.Approve(raw, now)
		return approveErr
	})
	if err != nil {
		return err
	}
	result := map[string]any{
		"schemaVersion":              "stackkit.identity-projection-approve-result/v1",
		"projectionId":               approval.Projection.ProjectionID,
		"projectionSHA256":           approval.ProjectionSHA256,
		"ownerRef":                   approval.OwnerRef,
		"approvedAt":                 approval.ApprovedAt,
		"pocketIdMutation":           false,
		"credentialMaterialExported": false,
	}
	if identityProjectionJSON {
		return writeCommandResult(cmd, cmd.CommandPath(), result)
	}
	_, err = fmt.Fprintf(
		cmd.OutOrStdout(),
		"Locally approved identity projection %s (%s); PocketID unchanged.\n",
		approval.Projection.ProjectionID,
		approval.ProjectionSHA256,
	)
	return err
}

func runIdentityProjectionApply(cmd *cobra.Command, _ []string) error {
	if !identityProjectionOwnerOK {
		return errors.New("identity projection apply requires explicit --owner-approve")
	}
	digest := strings.TrimSpace(identityProjectionDigest)
	if digest == "" {
		return errors.New("identity projection apply requires --projection-sha256")
	}
	service, err := identityprojection.NewService(getWorkDir())
	if err != nil {
		return err
	}
	now := time.Now().UTC().Truncate(time.Second)
	var receipt identityprojection.Receipt
	err = withLifecycleMutation(getWorkDir(), "identity projection apply", func() error {
		var applyErr error
		receipt, applyErr = service.Apply(cmd.Context(), digest, now)
		return applyErr
	})
	if err != nil {
		return err
	}
	if identityProjectionJSON {
		return writeCommandResult(cmd, cmd.CommandPath(), receipt)
	}
	_, err = fmt.Fprintf(
		cmd.OutOrStdout(),
		"Applied identity projection %s to local PocketID subject %s.\n",
		receipt.ProjectionID,
		receipt.PocketIDSubject,
	)
	return err
}

func runIdentityProjectionUnlink(cmd *cobra.Command, _ []string) error {
	if !identityProjectionOwnerOK {
		return errors.New("identity projection unlink requires explicit --owner-approve")
	}
	digest := strings.TrimSpace(identityProjectionDigest)
	if digest == "" {
		return errors.New("identity projection unlink requires --projection-sha256")
	}
	service, err := identityprojection.NewService(getWorkDir())
	if err != nil {
		return err
	}
	now := time.Now().UTC().Truncate(time.Second)
	var receipt identityprojection.Receipt
	err = withLifecycleMutation(getWorkDir(), "identity projection unlink", func() error {
		var unlinkErr error
		receipt, unlinkErr = service.Unlink(digest, now)
		return unlinkErr
	})
	if err != nil {
		return err
	}
	if identityProjectionJSON {
		return writeCommandResult(cmd, cmd.CommandPath(), receipt)
	}
	_, err = fmt.Fprintf(
		cmd.OutOrStdout(),
		"Detached identity projection %s; no local identity was deleted.\n",
		receipt.ProjectionID,
	)
	return err
}

func readIdentityProjectionInput() ([]byte, *identityprojection.Service, error) {
	path := strings.TrimSpace(identityProjectionFile)
	if path == "" {
		return nil, nil, errors.New("identity projection command requires --file")
	}
	raw, err := os.ReadFile(resolvePathFromWorkDir(getWorkDir(), path))
	if err != nil {
		return nil, nil, fmt.Errorf("read desired identity projection: %w", err)
	}
	if len(raw) == 0 || len(raw) > maxIdentityProjectionBytes {
		return nil, nil, errors.New("desired identity projection must contain 1 to 65536 bytes")
	}
	service, err := identityprojection.NewService(getWorkDir())
	if err != nil {
		return nil, nil, err
	}
	return raw, service, nil
}
