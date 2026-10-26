package commands

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/kombifyio/stackkits/internal/lifecyclemutation"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"
	"github.com/spf13/cobra"
)

// Qualification is an explicit hidden native operation. Backup configuration
// and execution can only read an existing qualification; neither can issue it.
func newRuntipiTerminalQualificationCommand() *cobra.Command {
	var request nativehost.RuntipiTerminalQualificationRequest
	var workload string
	var ownerApproved bool
	cmd := &cobra.Command{
		Use: "qualify-terminal-control", Hidden: true, Args: cobra.NoArgs,
		Annotations: map[string]string{noDeployObservabilityAnnotation: "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !ownerApproved {
				return errors.New("Runtipi terminal qualification requires --owner-approve")
			}
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			workspace := getWorkDir()
			join, err := currentLifecycleMutationJoin("runtipi-runtime qualify-terminal-control")
			if err != nil {
				return err
			}
			var qualified nativehost.RuntipiQualifiedInstallation
			err = lifecyclemutation.WithIdleMutationContext(ctx, workspace, join, func(held context.Context) error {
				// Read signed Apply directly. Backup admission deliberately requires
				// a qualification already, so it cannot bootstrap this operation.
				authority, err := inspectNativeV2AppliedAuthority(held, workspace, specFile)
				if err != nil {
					return err
				}
				deployment, err := nativeAppliedWorkloadDeploymentForBackup(authority, workload)
				if err != nil {
					return err
				}
				if deployment.RuntimeAdapter.ID != "runtipi" || deployment.ExecutionChannelRef != authority.Owner.Binding.ChannelRef {
					return errors.New("qualification requires the current signed Runtipi node workload and Owner channel")
				}
				request.SiteRef, request.NodeRef = deployment.SiteRef, deployment.NodeRef
				request.ExpectedSourceDigest = nativeRuntipiTerminalSourceDigest
				qualified, err = nativehost.QualifyRuntipiTerminalControl(held, workspace, request)
				return err
			})
			if err != nil {
				return err
			}
			// The result type contains only public qualification identities.
			return json.NewEncoder(cmd.OutOrStdout()).Encode(qualified)
		},
	}
	cmd.Flags().StringVar(&workload, "workload", "", "Exact current signed Runtipi workload")
	cmd.Flags().StringVar(&request.ManagerID, "manager-id", "", "Expected private native manager UUID")
	cmd.Flags().StringVar(&request.ManagerContainerID, "manager-container-id", "", "Expected exact local manager container ID")
	cmd.Flags().StringVar(&request.ManagerImage, "manager-image", "", "Expected digest-pinned qualified manager image")
	cmd.Flags().IntVar(&request.PrincipalID, "principal-id", 0, "Expected existing native operator ID")
	cmd.Flags().StringVar(&request.InputTransportReceiptDigest, "input-transport-receipt-digest", "", "Expected attached-input qualification evidence digest")
	cmd.Flags().BoolVar(&ownerApproved, "owner-approve", false, "Authorize qualification in this established node's encrypted Owner custody")
	for _, name := range []string{"workload", "manager-id", "manager-container-id", "manager-image", "principal-id", "input-transport-receipt-digest"} {
		_ = cmd.MarkFlagRequired(name)
	}
	return cmd
}
