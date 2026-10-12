package commands

import (
	"errors"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/spf13/cobra"
)

func init() {
	var approved, complete bool
	cmd := &cobra.Command{
		Use:         "migrate-browser-ca",
		Short:       "Migrate this node's authenticated legacy CA to the browser-capable policy",
		Long:        "Explicitly migrate legacy Basement CA signing keys while preserving the Owner signing key, PocketID identities, runtime secrets, ingress leaf keys, addresses and volumes. Retain an already current root. Apply and verify the current native plan before --complete. Clients must approve a changed public root through the existing client-trust handoff.",
		Args:        cobra.NoArgs,
		Annotations: map[string]string{noDeployObservabilityAnnotation: "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !approved {
				return machineAwareCommandError(cmd, errors.New("browser CA migration requires --owner-approve"))
			}
			workspace := getWorkDir()
			var result localevidence.BrowserCAMigrationObservation
			err := withLifecycleMutation(workspace, "host migrate-browser-ca", func() error {
				var err error
				if complete {
					if _, err = inspectNativeV2AppliedAuthority(commandContext(cmd), workspace, specFile); err != nil {
						return err
					}
					if err = localevidence.VerifyBasementOriginProvisioner(commandContext(cmd), workspace); err != nil {
						return err
					}
					result, err = localevidence.CompleteBasementBrowserCAMigration(workspace)
				} else {
					result, err = localevidence.MigrateBasementBrowserCA(workspace)
				}
				return err
			})
			if err != nil {
				return machineAwareCommandError(cmd, err)
			}
			return writeCommandResultStatus(cmd, cmd.CommandPath(), "success", result)
		},
	}
	cmd.Flags().BoolVar(&approved, "owner-approve", false, "Authorize this node's explicit CA transition")
	cmd.Flags().BoolVar(&complete, "complete", false, "Retire the recovery journal after current native Apply and ordinary TLS verification")
	hostCmd.AddCommand(cmd)
}
