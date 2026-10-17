package commands

import (
	"fmt"
	"os"
	"strings"

	"github.com/kombifyio/stackkits/internal/logging"
	"github.com/kombifyio/stackkits/internal/ownernotify"
)

func currentApplyRunID() string {
	if deployLog == nil {
		return ""
	}
	return strings.TrimSpace(deployLog.RunID())
}

func applyFailureGuidance() []string {
	guidance := []string{
		"Retry `stackkit apply` on the same workspace; the apply journal resumes succeeded owners.",
		"Inspect `stackkit plan --json` for readiness blockers and `stackkit drift detect --json` for the applied state.",
	}
	if runID := currentApplyRunID(); runID != "" && logging.IsValidRunID(runID) {
		return append(guidance, fmt.Sprintf("Inspect run %s with `stackkit logs latest --json`.", runID))
	}
	return append(guidance, "Inspect `stackkit logs latest --json` when the failure created a local rollout run.")
}

func printApplyFailureEnvelope() {
	if humanOutputSuppressed() {
		return
	}
	for _, line := range applyFailureGuidance() {
		_, _ = fmt.Fprintf(os.Stderr, "  %s\n", line)
	}
}

// applyFailedEventInput is the owner message of a failed Apply: the exact
// remedy and the run to inspect, never the error text, which can name paths.
func applyFailedEventInput() ownernotify.LifecycleInput {
	input := ownernotify.LifecycleInput{
		Event:  ownernotify.EventApplyFailed,
		Facts:  []string{"The apply stopped before the runtime converged."},
		Remedy: "stackkit apply (resumes succeeded owners)",
	}
	if runID := currentApplyRunID(); runID != "" && logging.IsValidRunID(runID) {
		input.Facts = append(input.Facts, "Run "+runID+": stackkit logs latest --json")
	}
	return input
}
