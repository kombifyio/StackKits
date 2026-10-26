package commands

import "github.com/spf13/cobra"

func init() {
	useCasesCmd.AddCommand(newUseCasesAddCommand(), newUseCasesConfigureCommand())
	rootCmd.AddCommand(newCapabilitiesConfigureCommand())
}

func newUseCasesAddCommand() *cobra.Command {
	o := &useCaseChangeOptions{}
	cmd := &cobra.Command{
		Use: "add <slug>", Short: "Add one catalog use case while retaining the Foundation and applications",
		Long: `Add one use case using CUE-owned defaults or explicit alternatives and profiles.
Existing workloads, routes, placement, storage and custody are retained.
Standard Mode persists by current spec hash and prepares only this workload's
local secret slots. Advanced intent requires --output: submit that candidate
through the existing capability-gated Advanced change-set lifecycle.
No services are deployed; use the normal plan/apply lifecycle.`,
		Args: cobra.ExactArgs(1), Annotations: map[string]string{noDeployObservabilityAnnotation: "true"},
		RunE: func(cmd *cobra.Command, args []string) error { return runUseCaseChange(cmd, args[0], *o, true) },
	}
	bindUseCaseChangeFlags(cmd, o)
	return cmd
}

func runUseCasesAdd(cmd *cobra.Command, slug, expectedHash string) error {
	return runUseCaseChange(cmd, slug, useCaseChangeOptions{intentChangeOptions: intentChangeOptions{expectedHash: expectedHash}}, true)
}
