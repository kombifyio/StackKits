package commands

import (
	"fmt"

	"github.com/kombifyio/stackkits/internal/runtimeexecutorlocal"
	"github.com/spf13/cobra"
)

func newHostPrepareCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "prepare",
		Short: "Prepare this host before Cloud Kit apply",
		Long: `Provision the Cloud execution-channel account and SSH key custody
required before host-security disables root login.

Cloud Kit installers and apply call this automatically. The command exists
for diagnostics and for converging a workspace after manual interruption.
It mints or reuses the workspace-custodied key under
.stackkit/cloud-host-security/execution-channel/ and installs the matching
public key on the execution-channel Linux account.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			workspace := getWorkDir()
			kit := workspaceKitSlug(workspace)
			if kit != "cloud-kit" {
				if kit == "" {
					return fmt.Errorf("host prepare requires a Cloud Kit workspace with stack-spec.yaml")
				}
				return fmt.Errorf("host prepare is for Cloud Kit workspaces; this workspace selected %q", kit)
			}
			if err := runtimeexecutorlocal.PrepareCloudExecutionChannel(cmd.Context(), workspace); err != nil {
				return err
			}
			if !humanOutputSuppressed() {
				printSuccess("Cloud execution-channel account is ready for apply")
			}
			return nil
		},
	}
	return cmd
}
