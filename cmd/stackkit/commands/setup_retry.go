package commands

import (
	"fmt"
	"strings"

	"github.com/kombifyio/stackkits/internal/stackspecintent"
	"github.com/spf13/cobra"
)

type retryAppSetupOptions struct {
	all        bool
	outputJSON bool
}

type retryAppSetupResult struct {
	SchemaVersion  string                       `json:"schemaVersion"`
	Status         string                       `json:"status"`
	WorkloadFilter string                       `json:"workloadFilter,omitempty"`
	Outcomes       []automaticOwnerSetupOutcome `json:"outcomes"`
	SucceededCount int                          `json:"succeededCount"`
	DegradedCount  int                          `json:"degradedCount"`
}

func newRetryAppSetupCommand() *cobra.Command {
	opts := &retryAppSetupOptions{}
	cmd := &cobra.Command{
		Use:         "retry-appsetup [workload]",
		Short:       "Retry degraded Tier-2 automatic app personalizations",
		Args:        cobra.MaximumNArgs(1),
		Annotations: map[string]string{noDeployObservabilityAnnotation: "true"},
		Long: `Retry Tier-2 automatic application personalization for workloads in
bootstrap_degraded or failed state without re-running Core/OpenTofu apply.

Without a workload argument, retries every workload currently recorded as
bootstrap_degraded (or failed) for the active Plan. Pass a workload name to
retry one specific application, or pass --all to run Tier-2 personalization
for every pending application (including those skipped during Clean Rollout).`,
		Example: `  # Retry every application currently in bootstrap_degraded state
  stackkit retry-appsetup

  # Retry one specific application
  stackkit retry-appsetup photos

  # Run Tier-2 personalization for all pending applications (e.g. after a Clean Rollout)
  stackkit retry-appsetup --all`,
		RunE: func(cmd *cobra.Command, args []string) error {
			workload := ""
			if len(args) == 1 {
				workload = strings.TrimSpace(args[0])
			}
			return runRetryAppSetup(cmd, workload, *opts)
		},
	}
	cmd.Flags().BoolVar(&opts.all, "all", false, "Retry all workloads declaring a Tier-2 setup action, including those skipped by Clean Rollout")
	cmd.Flags().BoolVar(&opts.outputJSON, "json", false, "Emit machine-readable secret-free retry outcomes as JSON")
	return cmd
}

func init() { rootCmd.AddCommand(newRetryAppSetupCommand()) }

func runRetryAppSetup(cmd *cobra.Command, workload string, opts retryAppSetupOptions) error {
	ctx := commandContext(cmd)
	wd := getWorkDir()
	setup, err := newAutomaticOwnerSetup(ctx, wd)
	if err != nil {
		return machineAwareCommandError(cmd, fmt.Errorf("retry-appsetup could not inspect applied authority: %w", err))
	}
	if workload != "" {
		found := false
		for _, contract := range setup.contracts {
			if contract.WorkloadRef == workload {
				if _, _, supported := setup.action(contract); supported {
					found = true
				}
				break
			}
		}
		if !found {
			return machineAwareCommandError(cmd, fmt.Errorf("workload %q has no declared executable Tier-2 app setup action in the current Plan", workload))
		}
	}

	onlyDegraded := workload == "" && !opts.all
	outcomes := runAutomaticOwnerSetupWithOptions(ctx, wd, automaticOwnerSetupOptions{
		SetupStyleOverride: stackspecintent.SetupStyleBootstrapped,
		WorkloadFilter:     workload,
		OnlyDegraded:       onlyDegraded,
	})
	result := retryAppSetupResult{
		SchemaVersion:  "stackkit.retry-appsetup/v1",
		WorkloadFilter: workload,
		Outcomes:       outcomes,
	}
	if result.Outcomes == nil {
		result.Outcomes = []automaticOwnerSetupOutcome{}
	}
	for _, outcome := range outcomes {
		switch outcome.Status {
		case automaticSetupSucceeded, automaticSetupAlreadyDone:
			result.SucceededCount++
		case automaticSetupBootstrapDegraded, automaticSetupFailed:
			result.DegradedCount++
		}
	}

	result.Status = "passed"
	if result.DegradedCount > 0 {
		result.Status = automaticSetupBootstrapDegraded
	}

	if opts.outputJSON {
		if err := writeCommandResult(cmd, cmd.CommandPath(), result); err != nil {
			return err
		}
	} else if len(outcomes) == 0 {
		if onlyDegraded {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "No applications are currently in bootstrap_degraded state.")
		} else {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "No applications declare a Tier-2 automatic setup action.")
		}
	} else {
		for _, outcome := range outcomes {
			if outcome.Detail != "" {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s: %s (%s)\n", outcome.WorkloadRef, outcome.Status, outcome.Detail)
			} else {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", outcome.WorkloadRef, outcome.Status)
			}
		}
	}

	if result.DegradedCount > 0 {
		return fmt.Errorf("%d application Tier-2 setup(s) remain in %s state; apps stay private behind Access Gate until resolved", result.DegradedCount, automaticSetupBootstrapDegraded)
	}
	return nil
}
