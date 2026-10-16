package user

import (
	"context"
	"errors"
	"fmt"

	"github.com/kombifyio/stackkits/internal/actionableerror"
	"github.com/kombifyio/stackkits/internal/lifecyclemutation"
	"github.com/kombifyio/stackkits/internal/localowner"
	"github.com/spf13/cobra"
)

type ownerReconciliationAPI interface {
	ReconcileTinyAuthPKCE(context.Context, bool) (localowner.TinyAuthReconciliation, error)
}

func newOwnerReconcileCommand(api householdAPI) *cobra.Command {
	command := &cobra.Command{
		Use: "reconcile", Short: "Enable legacy TinyAuth PKCE only against the verified existing Owner binding",
		Long:        "Requires explicit Owner approval and an idle native lifecycle. Verifies every existing Owner, client, callback and group binding before enabling PKCE, then verifies again. Keeps Owner keys, users, groups, client secret, enrollment links, Plan and release pin. Other identity drift fails closed. On a managed server invoke through authorized Techstack server access; this performs no Advanced apply or provider execution.",
		Args:        cobra.NoArgs,
		Annotations: map[string]string{noDeployObservabilityAnnotation: "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			approved, err := cmd.Flags().GetBool("owner-approve")
			if err != nil {
				return err
			}
			if !approved {
				return ownerReconcileError(cmd, actionableerror.WithDiagnostic(
					"pocketid_tinyauth_reconcile_owner_approval_required", errors.New("owner approval required")))
			}
			service, err := resolveHousehold(cmd, api)
			if err != nil {
				return err
			}
			reconciler, ok := service.(ownerReconciliationAPI)
			if !ok {
				return errors.New("owner identity reconciliation is unavailable")
			}
			result, err := reconciler.ReconcileTinyAuthPKCE(cmd.Context(), approved)
			if err != nil {
				return ownerReconcileError(cmd, err)
			}
			if jsonOutput(cmd) {
				return writeJSON(cmd, result)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "TinyAuth binding verified; PKCE enabled (changed: %t).\n", result.Changed)
			return err
		},
	}
	command.Flags().Bool("owner-approve", false, "Explicitly approve PKCE-only convergence of the existing verified identity binding")
	return command
}

func ownerReconcileError(cmd *cobra.Command, err error) error {
	if detail, known := actionableerror.FromError(err); known && jsonOutput(cmd) {
		if writeErr := writeJSONStatus(cmd, detail, "failed"); writeErr != nil {
			return errors.Join(err, writeErr)
		}
	}
	return err
}

func (h liveHousehold) ReconcileTinyAuthPKCE(ctx context.Context, approved bool) (localowner.TinyAuthReconciliation, error) {
	var result localowner.TinyAuthReconciliation
	err := lifecyclemutation.WithIdleMutation(h.workspace, lifecyclemutation.JoinRequest{Command: "user owner reconcile"}, func() error {
		service, err := localowner.NewService(h.workspace)
		if err != nil {
			return err
		}
		result, err = service.ReconcileTinyAuthPKCE(ctx, approved)
		return err
	})
	return result, err
}
