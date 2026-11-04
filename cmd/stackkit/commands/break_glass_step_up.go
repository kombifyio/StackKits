package commands

import (
	"errors"
	"fmt"
	"time"

	"github.com/kombifyio/stackkits/internal/identity"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/spf13/cobra"
)

// requireCustodyChangeStepUp gates a console change of identity custody
// (homelab identity target §9, §12) on the Owner's passkey: the command
// raises a custody-change step-up request with the action digest, the Owner
// approves it in the Hub on an enrolled Owner device, and the console verifies
// the step-up itself before it acts. The first recovery setup, before any
// Owner device exists, runs without it.
func requireCustodyChangeStepUp(cmd *cobra.Command, root, operation, summary string) error {
	status, err := identity.LoadRecoveryStatus(root)
	if err != nil {
		return err
	}
	owner, err := localevidence.LoadOwnerCustody(root)
	if err != nil {
		return err
	}
	action, err := identity.NewHomeStepUpAction(identity.HomeStepUpCustodyChange, struct {
		Operation    string    `json:"operation"`
		OwnerRef     string    `json:"ownerRef"`
		SiteRef      string    `json:"siteRef"`
		Recipient    string    `json:"recipient"`
		CodeIssuedAt time.Time `json:"codeIssuedAt"`
	}{operation, owner.OwnerRef, owner.Binding.SiteRef, status.Recipient, status.CodeIssuedAt.UTC()})
	if err != nil {
		return err
	}
	request, err := identity.RequestHomeOwnerStepUp(root, action, summary, time.Now())
	if err != nil {
		return err
	}
	address, addressErr := localevidence.LocalIdentityRuntimeAddress(root)
	hub := "the Hub"
	if addressErr == nil {
		hub = "https://" + address.ServiceHost("base") + identity.HomeHubPathPrefix
	}
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Approve \"%s\" with your passkey on an enrolled Owner device: open %s and choose Approve (request %s, valid for 5 minutes).\n", summary, hub, request.ID)
	if _, err := identity.AwaitHomeOwnerStepUp(cmd.Context(), root, request, time.Second); err != nil {
		if errors.Is(err, identity.ErrHomeStepUpRequestExpired) {
			return fmt.Errorf("%w; enroll an Owner device in the Hub at home first if you have none, then run the command again", err)
		}
		return fmt.Errorf("the Owner step-up for %q was not accepted: %w", summary, err)
	}
	return nil
}
