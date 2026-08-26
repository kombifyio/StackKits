package commands

import (
	"fmt"
	"os"
	"strings"

	"github.com/kombifyio/stackkits/internal/logging"
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
		"Inspect `stackkit status --json` for executionReadiness and apply state.",
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
