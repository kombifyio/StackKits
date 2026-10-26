package commands

import (
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/opentofu"
	"github.com/spf13/cobra"
)

func init() {
	var project, stackID string
	command := &cobra.Command{
		Use: "komodo-workload-action deploy|destroy|verify-destroy", Hidden: true, Args: cobra.ExactArgs(1),
		// This signed, node-local subprocess is already inside the admitted
		// Apply. Do not start a second rollout, logger or telemetry lifecycle.
		PersistentPreRunE: func(*cobra.Command, []string) error { return nil },
		RunE: func(cmd *cobra.Command, args []string) error {
			return opentofu.RunKomodoWorkloadAction(cmd.Context(), args[0], project, stackID)
		},
	}
	command.Flags().StringVar(&project, "project", "", "Exact node workload project")
	command.Flags().StringVar(&stackID, "stack-id", "", "Provider-held stack ObjectId")
	rootCmd.AddCommand(command)
}
