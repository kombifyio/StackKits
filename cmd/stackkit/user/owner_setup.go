package user

import (
	"errors"
	"fmt"
	"time"

	"github.com/kombifyio/stackkits/internal/identity"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/localowner"
	"github.com/spf13/cobra"
)

// Owner setup steps in their fixed order (homelab identity target section 5
// and 14.2): the identity origin is decided at init, before any passkey
// exists; the Owner then registers a passkey bound to it; then the recovery
// key and recovery code are shown once; then the Owner's first device is
// enrolled at home, so away confirmations have an enrolled device.
type ownerSetupStep struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

func newOwnerSetupCommand(api householdAPI) *cobra.Command {
	return &cobra.Command{
		Use:   "setup",
		Short: "Show the ordered Owner setup: identity origin, Owner passkey, recovery key and code, first Owner device",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			root, err := workspaceRoot(cmd)
			if err != nil {
				return err
			}
			service, err := resolveHousehold(cmd, api)
			if err != nil {
				return err
			}
			steps := []ownerSetupStep{{ID: "identity-origin"}, {ID: "owner-passkey"}, {ID: "recovery-key-and-code"}, {ID: "first-owner-device"}}
			blocked := false
			mark := func(step *ownerSetupStep, done bool, doneDetail, nextDetail string) {
				switch {
				case blocked:
					step.Status = "waiting"
				case done:
					step.Status, step.Detail = "done", doneDetail
				default:
					step.Status, step.Detail, blocked = "next", nextDetail, true
				}
			}
			address, originErr := localevidence.LocalIdentityRuntimeAddress(root)
			origin := ""
			if originErr == nil {
				origin = address.PocketIDOrigin()
			}
			mark(&steps[0], originErr == nil, origin, "run stackkit init: the identity origin is fixed before the Owner passkey")
			if originErr != nil {
				for index := 1; index < len(steps); index++ {
					steps[index].Status = "waiting"
				}
			} else {
				activation, activationErr := service.OwnerActivationStatus(cmd.Context())
				switch {
				case errors.Is(activationErr, localevidence.ErrOwnerRuntimeBindingMissing):
					mark(&steps[1], false, "", "run stackkit apply: Pocket ID must run before the Owner registers a passkey")
				case activationErr != nil:
					// Pocket ID is not reachable (a move quiesced the Core): the passkey
					// step is the next one, once the StackKit is applied again.
					mark(&steps[1], false, "", "Pocket ID cannot be read ("+activationErr.Error()+"); run stackkit apply")
				default:
					mark(&steps[1], activation.Status == "active" && activation.Origin == origin, activation.Origin, activation.Status+"; run stackkit user owner activate --owner-approve")
				}
				if blocked {
					steps[2].Status, steps[3].Status = "waiting", "waiting"
					return writeOwnerSetup(cmd, steps)
				}
				recovery, recoveryErr := identity.LoadRecoveryStatus(root)
				if recoveryErr != nil {
					return recoveryErr
				}
				movedAt, moved := time.Time{}, false
				if service, serviceErr := localowner.NewService(root); serviceErr == nil {
					movedAt, moved = service.IdentityOriginMovedAt()
				}
				// A recovery set binds the identity origin it was written under:
				// after a move only a set of the current origin counts.
				recoveryDone, recoveryNext := recovery.Configured, "run stackkit break-glass setup --owner-approve (the key and code are shown once)"
				recoveryDetail := "recovery key and code set up, code issued " + recovery.CodeIssuedAt.UTC().Format("2006-01-02")
				if recovery.SetIdentityOrigin != "" {
					recoveryDetail += "; recovery set for " + recovery.SetIdentityOrigin + " written " + recovery.SetWrittenAt.UTC().Format("2006-01-02")
				}
				if recovery.Configured && moved && recovery.SetIdentityOrigin != origin {
					recoveryDone = false
					recoveryNext = "the identity origin moved to " + origin + " on " + movedAt.UTC().Format("2006-01-02") + ": write a recovery set that binds it (stackkit break-glass write)"
				}
				mark(&steps[2], recoveryDone, recoveryDetail, recoveryNext)
				binding, bindingErr := localevidence.LoadOwnerRuntimeBinding(root)
				if bindingErr != nil && !errors.Is(bindingErr, localevidence.ErrOwnerRuntimeBindingMissing) {
					return bindingErr
				}
				devices, devicesErr := identity.ListHomeDevices(root)
				ownerDevice := false
				if devicesErr == nil && bindingErr == nil {
					for _, device := range devices {
						if device.AccountSubject == binding.PocketIDSubject && !device.Revoked && device.Approval.PendingUntil == nil && (!moved || device.EnrolledAt.After(movedAt)) {
							ownerDevice = true
						}
					}
				}
				mark(&steps[3], ownerDevice, "an Owner device is enrolled", "sign in to the Homelab Hub with the Owner passkey (a device enrolled before an identity origin move belongs to the old origin; stackkit device bootstrap prints where)")
			}
			return writeOwnerSetup(cmd, steps)
		},
	}
}

func writeOwnerSetup(cmd *cobra.Command, steps []ownerSetupStep) error {
	if jsonOutput(cmd) {
		return writeJSON(cmd, map[string]any{"steps": steps})
	}
	for _, step := range steps {
		if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%-24s %-8s %s\n", step.ID, step.Status, step.Detail); err != nil {
			return err
		}
	}
	return nil
}
