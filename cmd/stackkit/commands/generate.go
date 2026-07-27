package commands

import (
	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/rollout"
	"github.com/spf13/cobra"
)

var (
	genOutputDir               string
	genForce                   bool
	genFragments               bool
	generateV2ExecutionOptions architectureV2ExecutionCLIOptions
)

var generateCmd = &cobra.Command{
	Use:     "generate",
	Aliases: []string{"gen"},
	Short:   "Generate governed deployment artifacts from a stack specification",
	Long: `Resolve canonical StackSpec v2 intent through the embedded CUE authority,
persist the exact ResolvedPlan atomically, and render its governed artifacts
transactionally beneath the plan-owned outputRoot.

StackSpec v1 generation is retired. Use a published exact-v0.6 binary for a
historical workspace or migrate the intent to StackSpec v2 before generating.
Legacy --force and --fragments switches are rejected by the native v2 path.

Examples:
  stackkit generate
  stackkit generate --output ./deploy`,
	RunE: runGenerate,
}

func init() {
	generateCmd.Flags().StringVarP(&genOutputDir, "output", "o", "deploy", "Override the plan-owned output root")
	generateCmd.Flags().BoolVarP(&genForce, "force", "f", false, "Retired legacy overwrite switch; rejected for StackSpec v2")
	generateCmd.Flags().BoolVar(&genFragments, "fragments", false, "Retired legacy fragment switch; rejected for StackSpec v2")
	generateCmd.Flags().StringVar(&generateV2ExecutionOptions.inventoryPath, "inventory", "", "Architecture v2 observed Inventory (otherwise one conventional inventory file is selected)")
	generateCmd.Flags().StringVar(&generateV2ExecutionOptions.planPath, "resolved-plan", "", "Architecture v2 canonical ResolvedPlan (default: <outputRoot>/.stackkit/resolved-plan.json)")
}

func runGenerate(cmd *cobra.Command, args []string) (retErr error) {
	wd := getWorkDir()
	rolloutEvent("generate", "started", "generate started", nil)
	defer func() {
		if retErr != nil {
			rolloutFailure("generate", retErr)
			closeRolloutRecorder(rollout.Summary{
				Status:  "failed",
				Message: retErr.Error(),
			})
			return
		}
		rolloutEvent("generate", "succeeded", "generate succeeded", nil)
	}()

	options := generateV2ExecutionOptions
	if flag := cmd.Flags().Lookup("output"); flag != nil && flag.Changed {
		options.outputRoot = genOutputDir
	}
	if flag := cmd.Flags().Lookup("fragments"); flag != nil && flag.Changed {
		options.fragments = genFragments
	}
	if flag := cmd.Flags().Lookup("force"); flag != nil && flag.Changed {
		options.force = genForce
	}
	options.context = cmd.Context()

	gate := newArchitectureV2ExecutionGate()
	gate.rejectV1 = true
	if handled, err := gate.preflight(wd, specFile, architectureV2Generate, options); handled {
		return err
	}
	return &architecturev2.ResolveError{
		Code:    architecturev2.ErrOperationalUnavailable,
		Message: "generate requires a classifiable canonical StackSpec v2; legacy fallback generation is retired",
	}
}
