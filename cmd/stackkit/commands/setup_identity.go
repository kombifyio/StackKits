package commands

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/kombifyio/stackkits/internal/identity"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/localowner"
	"github.com/spf13/cobra"
)

// The identity bootstrap order of the homelab identity target (§5, §9,
// §14.2): init fixed the identity origin before any passkey exists; this
// command then takes the applied Authority through the Owner passkey on that
// origin, the Home trust, the recovery key and code (shown once) with the
// first recovery set, and the Owner's first device enrolled at home in the
// Hub, so later away enrollments and custody changes have an enrolled device
// to confirm them. Each step is skipped once it is done.
type identitySetupResult struct {
	IdentityOrigin   string            `json:"identityOrigin"`
	OwnerPasskey     string            `json:"ownerPasskey"`
	Trust            string            `json:"homeTrust"`
	Recovery         string            `json:"recovery"`
	RecoveryKey      string            `json:"recoveryKey,omitempty"`
	RecoveryCode     string            `json:"recoveryCode,omitempty"`
	RecoverySet      *recoverySetWrite `json:"recoverySet,omitempty"`
	RecoverySetErr   string            `json:"recoverySetError,omitempty"`
	HubURL           string            `json:"hubUrl"`
	FirstOwnerDevice string            `json:"firstOwnerDevice"`
	NextSteps        []string          `json:"nextSteps"`
	Error            string            `json:"error,omitempty"`
}

func newSetupIdentityCommand() *cobra.Command {
	var approved, asJSON bool
	var wait time.Duration
	var destinations recoverySetDestinations
	command := &cobra.Command{
		Use:   "identity",
		Short: "Finish the identity bootstrap: Owner passkey, recovery key and code, first Owner device",
		Long: `Finish the homelab identity bootstrap on the applied Authority, in order:

  1. the Owner passkey on the identity origin init fixed (the activation link
     is printed and the command waits for the passkey);
  2. the Owner-signed Home trust that every passkey and device check uses;
  3. the recovery key and recovery code, shown exactly once, and the first
     recovery set (local copy, --target directory, --backup-target upload);
  4. the Owner's first device: open the printed Hub address on a phone or
     laptop at home and connect it with the passkey; it is enrolled at once.

Every step that is already done is skipped, so the command can be run again
after an interruption. The recovery key and code are never shown again.`,
		Example: `  # After the first apply, with a copy of the recovery set on a backup disk
  stackkit setup identity --owner-approve --target /mnt/backup`,
		Args:        cobra.NoArgs,
		Annotations: map[string]string{noDeployObservabilityAnnotation: "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !approved {
				return errors.New("setup identity requires --owner-approve")
			}
			result, err := runIdentitySetup(cmd, getWorkDir(), destinations, wait, asJSON)
			if asJSON {
				// A failed step after the recovery key and code were created
				// still hands them over (they are shown only once), with the
				// result marked as failed.
				status := "success"
				if err != nil {
					status = "failed"
					result.Error = err.Error()
				}
				if writeErr := writeCommandResultStatus(cmd, cmd.CommandPath(), status, result); writeErr != nil {
					return writeErr
				}
			}
			return err
		},
	}
	command.Flags().BoolVar(&approved, "owner-approve", false, "Authorize the identity bootstrap of this Authority")
	command.Flags().BoolVar(&asJSON, "json", false, "Print a JSON result (it contains the recovery key and code when they are created)")
	command.Flags().DurationVar(&wait, "wait", 15*time.Minute, "How long to wait for the Owner passkey and for the first Owner device")
	destinations.flags(command)
	return command
}

func runIdentitySetup(cmd *cobra.Command, root string, destinations recoverySetDestinations, wait time.Duration, quiet bool) (identitySetupResult, error) {
	ctx := cmd.Context()
	out := cmd.OutOrStdout()
	if quiet {
		out = cmd.ErrOrStderr()
	}
	result := identitySetupResult{}
	address, err := localevidence.LocalIdentityRuntimeAddress(root)
	if err != nil {
		return result, fmt.Errorf("the identity bootstrap needs an applied Authority: %w", err)
	}
	result.IdentityOrigin = address.PocketIDOrigin()
	result.HubURL = "https://" + address.ServiceHost("base") + identity.HomeHubPathPrefix
	_, _ = fmt.Fprintf(out, "Identity origin: %s (passkeys bind to it)\n", result.IdentityOrigin)

	service, err := localowner.NewService(root)
	if err != nil {
		return result, err
	}
	if result.OwnerPasskey, err = awaitOwnerPasskey(ctx, service, out, wait); err != nil {
		return result, err
	}

	trust, err := identity.RefreshHomePublicTrust(ctx, root, time.Now())
	if err != nil {
		return result, fmt.Errorf("bind the Home trust: %w", err)
	}
	result.Trust = "bound until " + trust.ExpiresAt.Format(time.RFC3339)

	status, err := identity.LoadRecoveryStatus(root)
	if err != nil {
		return result, err
	}
	if status.Configured {
		result.Recovery = "configured"
	} else {
		var secrets identity.RecoverySecrets
		if err := withLifecycleMutation(root, "setup identity", func() error {
			var setupErr error
			secrets, setupErr = identity.SetupRecovery(root, false, time.Now())
			return setupErr
		}); err != nil {
			return result, err
		}
		result.Recovery, result.RecoveryKey, result.RecoveryCode = "created", secrets.RecoveryKey, secrets.RecoveryCode
		written, writeErr := writeRecoverySetEverywhere(ctx, root, destinations)
		if writeErr != nil {
			result.RecoverySetErr = writeErr.Error()
		} else {
			result.RecoverySet = &written
		}
		if !quiet {
			_, _ = fmt.Fprintln(out, "RECOVERY KEY (shown once; keep it offline and never with the recovery set):")
			_, _ = fmt.Fprintln(out, "  "+secrets.RecoveryKey)
			_, _ = fmt.Fprintln(out, "RECOVERY CODE (shown once; registers a new Owner passkey once):")
			_, _ = fmt.Fprintln(out, "  "+secrets.RecoveryCode)
			if writeErr != nil {
				_, _ = fmt.Fprintf(out, "The recovery set was not written: %v\nRetry with: stackkit break-glass write\n", writeErr)
			} else {
				printRecoverySetWrite(cmd, written)
			}
		}
	}

	if result.FirstOwnerDevice, err = awaitFirstOwnerDevice(ctx, root, trust.PocketID.Subject, result.HubURL, out, wait); err != nil {
		return result, err
	}
	result.NextSteps = []string{
		"stackkit backup schedule enable --owner-approve (scheduled backups also write the recovery set)",
		"stackkit user add <name> --owner-approve for each household member; members enroll their own devices in the Hub",
	}
	_, _ = fmt.Fprintln(out, "Identity bootstrap complete. Next: "+result.NextSteps[0])
	return result, nil
}

func awaitOwnerPasskey(ctx context.Context, service *localowner.Service, out io.Writer, wait time.Duration) (string, error) {
	status, err := service.OwnerActivationStatus(ctx)
	if err != nil {
		return "", err
	}
	if status.Status == "active" {
		return "active", nil
	}
	activation, err := service.IssueOwnerActivation(ctx)
	if err != nil {
		return "", err
	}
	if activation.Status == "active" {
		return "active", nil
	}
	_, _ = fmt.Fprintf(out, "Register the Owner passkey (one-time link, valid until %s):\n  %s\n", activation.ExpiresAt.Format(time.RFC3339), activation.SetupURL)
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(5 * time.Second):
		}
		if current, statusErr := service.OwnerActivationStatus(ctx); statusErr == nil && current.Status == "active" {
			_, _ = fmt.Fprintln(out, "Owner passkey registered.")
			return "active", nil
		}
	}
	return "", errors.New("the Owner passkey was not registered in time; run 'stackkit setup identity --owner-approve' again")
}

func awaitFirstOwnerDevice(ctx context.Context, root, owner, hub string, out io.Writer, wait time.Duration) (string, error) {
	find := func() string {
		devices, err := identity.ListHomeDevices(root)
		if err != nil {
			return ""
		}
		for _, device := range devices {
			if device.AccountSubject == owner && device.Active(time.Now()) {
				return device.Name
			}
		}
		return ""
	}
	if name := find(); name != "" {
		return name, nil
	}
	_, _ = fmt.Fprintf(out, "Connect your first device at home: open %s on your phone or laptop on the home network, install it, and connect it with your passkey.\n", hub)
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(3 * time.Second):
		}
		if name := find(); name != "" {
			_, _ = fmt.Fprintf(out, "First Owner device enrolled: %s\n", name)
			return name, nil
		}
	}
	return "", errors.New("no Owner device was enrolled in time; open the Hub at home and run 'stackkit setup identity --owner-approve' again")
}
