package commands

import (
	"errors"

	"github.com/kombifyio/stackkits/internal/runtimeexecutor/opentofu"
	"github.com/spf13/cobra"
)

func newAdoptCommand() *cobra.Command {
	group := &cobra.Command{Use: "adopt", Short: "Bring existing workloads under StackKit custody"}
	var directory, target string
	var preserve bool
	command := &cobra.Command{Use: "compose", Short: "Adopt one running Compose project without replacing containers", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if target != "opentofu" || !preserve {
			return errors.New("Compose adoption requires --to opentofu --no-recreate")
		}
		workspace := getWorkDir()
		return withLifecycleMutation(workspace, "adopt compose", func() error {
			ev, err := (opentofu.Runtime{WorkspaceRoot: workspace}).AdoptCompose(cmd.Context(), directory)
			if err != nil {
				return err
			}
			return writeCommandResult(cmd, "adopt compose", ev)
		})
	}}
	command.Flags().StringVar(&directory, "project-dir", "", "Directory of the running Compose project")
	_ = command.MarkFlagRequired("project-dir")
	command.Flags().StringVar(&target, "to", "", "Target runtime")
	command.Flags().BoolVar(&preserve, "no-recreate", false, "Preserve container identity")
	command.Flags().Bool("json", false, "Emit machine-readable evidence")
	group.AddCommand(command)
	return group
}
