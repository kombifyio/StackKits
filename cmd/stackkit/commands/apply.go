package commands

import (
	"fmt"
	"strconv"

	"github.com/kombifyio/stackkits/internal/rollout"
	"github.com/spf13/cobra"
)

var (
	applyAutoApprove        bool
	applyVerify             bool
	applyVerifyHTTP         bool
	applyVerifyStrict       bool
	applySkipPlatformApps   bool
	applyJSON               bool
	applyV2ExecutionOptions architectureV2ExecutionCLIOptions
)

var applyCmd = &cobra.Command{
	Use:         "apply [plan-file]",
	Short:       "Apply infrastructure changes",
	Annotations: map[string]string{noDeployObservabilityAnnotation: "true"},
	Long: `Apply the planned changes to the infrastructure.

Product Apply executes the canonical ResolvedPlan through its Runtime Owners.
Standard Mode uses the persisted CUE-owned local Site/node/channel binding
automatically; Advanced Mode may provide an authenticated service/device
execution channel. Both paths use the same ResolvedPlan, evidence, and runtime
validation, and neither prompts: the plan is approved when it is generated.

Examples:
  stackkit apply                              Apply the canonical ResolvedPlan
  stackkit apply --json                       Emit the versioned Apply result
  stackkit apply --expected-plan-hash <sha>   Refuse to mutate a changed plan`,
	Args: cobra.MaximumNArgs(1),
	RunE: runApply,
}

func init() {
	applyCmd.Flags().BoolVar(&applyAutoApprove, "auto-approve", false, "Accepted for compatibility; native Apply never prompts")
	applyCmd.Flags().BoolVar(&applyVerify, "verify", false, "No effect: run stackkit verify after Apply instead")
	applyCmd.Flags().BoolVar(&applyVerifyHTTP, "verify-http", false, "No effect: run stackkit verify --http after Apply instead")
	applyCmd.Flags().BoolVar(&applyVerifyStrict, "verify-strict", false, "No effect: run stackkit verify after Apply instead")
	applyCmd.Flags().BoolVar(&applySkipPlatformApps, "skip-platform-apps", false, "No effect: native Apply has no separate platform-app stage")
	applyCmd.Flags().BoolVar(&applyJSON, "json", false, "Emit the versioned Apply result and runtime observations as machine-readable JSON")
	applyCmd.Flags().StringVar(&applyV2ExecutionOptions.inventoryPath, "inventory", "", "Architecture v2 observed Inventory (otherwise one conventional inventory file is selected)")
	applyCmd.Flags().StringVar(&applyV2ExecutionOptions.planPath, "resolved-plan", "", "Architecture v2 canonical ResolvedPlan (default: <outputRoot>/.stackkit/resolved-plan.json)")
	applyCmd.Flags().StringVar(&applyV2ExecutionOptions.manifestPath, "artifact-manifest", "", "Architecture v2 generation manifest (default: <outputRoot>/.stackkit/generation-manifest.json)")
	applyCmd.Flags().StringVar(&applyV2ExecutionOptions.receiptPath, "generation-receipt", "", "Architecture v2 generation receipt (default: <outputRoot>/.stackkit/generation-receipt.json)")
	applyCmd.Flags().StringVar(&applyV2ExecutionOptions.expectedPlanHash, "expected-plan-hash", "", "Require the canonical ResolvedPlan to match this sha256 digest immediately before Apply")
	// Local execution binding. Apply never infers that a planned target is this
	// machine: the owner names the exact Site, node, and channel this process
	// owns, and anything else stays unadmitted.
	applyCmd.Flags().StringVar(&applyV2ExecutionOptions.preflightPolicy, "preflight", "", "Host admission policy before mutation: strict, warn (default), or skip")
	applyCmd.Flags().StringVar(&applyV2ExecutionOptions.localSiteRef, "local-site", "", "Architecture v2 Site explicitly owned by this local execution process")
	applyCmd.Flags().StringVar(&applyV2ExecutionOptions.localNodeRef, "local-node", "", "Architecture v2 node explicitly owned by this local execution process")
	applyCmd.Flags().StringVar(&applyV2ExecutionOptions.localChannelRef, "local-execution-channel", "", "Architecture v2 execution channel explicitly owned by this local process")
}

func runApply(cmd *cobra.Command, args []string) (retErr error) {
	machineResultWritten := false
	applyV2ExecutionOptions.applySink = nil
	if applyJSON {
		applyV2ExecutionOptions.applySink = func(result architectureV2ApplyCommandResult) error {
			err := writeCommandResult(cmd, cmd.CommandPath(), result)
			machineResultWritten = err == nil
			return err
		}
	}
	applyV2ExecutionOptions.legacyPlanFile = ""
	if len(args) > 0 {
		applyV2ExecutionOptions.legacyPlanFile = args[0]
	}
	defer func() {
		if retErr != nil {
			if applyJSON && !machineResultWritten {
				retErr = writeMachineCommandFailure(cmd, retErr, applyFailureGuidance()...)
			} else {
				printApplyFailureEnvelope()
			}
			rolloutFailure("apply", retErr)
			closeRolloutRecorder(rollout.Summary{
				Status:  "failed",
				Message: retErr.Error(),
			})
			return
		}
		rolloutEvent("apply", "succeeded", "apply succeeded", nil)
		closeRolloutRecorder(rollout.Summary{Status: "success"})
	}()
	// Say this before admission: whether a flag is inert does not depend on the
	// workspace being valid, and an operator who passed one deserves to know
	// even when Apply stops earlier for another reason.
	noteInertApplyFlags(cmd)
	wd := getWorkDir()
	if err := admitApplyBeforeDeployObservability(wd, specFile); err != nil {
		return err
	}
	if !noLog {
		initDeployLogger()
	}
	rolloutEvent("apply", "started", "apply started", map[string]string{
		"auto_approve": strconv.FormatBool(applyAutoApprove),
	})
	if handled, err := newArchitectureV2ExecutionGate().preflight(wd, specFile, architectureV2Apply, applyV2ExecutionOptions); handled {
		return err
	}
	if err := requireNativeV2StackSpec(wd, specFile, architectureV2Apply); err != nil {
		return err
	}
	// The Architecture v2 gate owns execution for every StackSpec it can
	// classify, and the admission above proves the file exists. Reaching this
	// point therefore means the spec is present but unreadable as either
	// canonical v2 or bounded v1 compatibility input, so no execution
	// authority admits it. Fail closed instead of inferring one.
	return fmt.Errorf(
		"apply: StackSpec %s exists but could not be classified for execution; repair the file or recreate it with stackkit init",
		specFile,
	)
}

// noteInertApplyFlags reports the flags the native Architecture v2 lifecycle
// does not act on.
//
// They are still accepted because released installers pass them: removing them
// would break an older `base.stackkit.cc` script against a newer CLI. Silently
// ignoring them is worse than saying so, because the two-phase installer shape
// they imply — core services first, applications second — does not exist. The
// canonical ResolvedPlan and its Runtime Owners decide execution order, so a
// second Apply repeats the first rather than adding a stage.
func noteInertApplyFlags(cmd *cobra.Command) {
	if cmd == nil {
		return
	}
	for flag, note := range map[string]string{
		"skip-platform-apps": "native Apply has no separate platform-app stage; the ResolvedPlan owns execution order",
		"verify":             "run `stackkit verify` after Apply instead",
		"verify-http":        "run `stackkit verify --http` after Apply instead",
		"verify-strict":      "run `stackkit verify` after Apply instead",
	} {
		if cmd.Flags().Changed(flag) {
			printWarning("--%s has no effect on the native Apply lifecycle: %s", flag, note)
		}
	}
}
