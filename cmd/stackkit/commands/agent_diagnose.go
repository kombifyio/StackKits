package commands

import (
	"fmt"

	"github.com/kombifyio/stackkits/internal/actionableerror"
	"github.com/spf13/cobra"
)

var agentDiagnoseJSON bool

var agentDiagnoseCmd = &cobra.Command{
	Use: "diagnose <error-id>", Short: "Explain a stable error ID and its recovery action",
	Long:        "Read the same recovery guidance used by CLI failures, the MCP self-check plan and docs/TROUBLESHOOTING.md. This lookup does not observe or change the host.",
	Args:        cobra.ExactArgs(1),
	Annotations: map[string]string{noDeployObservabilityAnnotation: "true"},
	RunE: func(cmd *cobra.Command, args []string) error {
		detail, known := actionableerror.Diagnostic(args[0])
		if !known {
			return machineAwareCommandError(cmd, fmt.Errorf("unknown terminal diagnosis ID %q", args[0]))
		}
		if agentDiagnoseJSON {
			return writeCommandResult(cmd, cmd.CommandPath(), detail)
		}
		_, err := fmt.Fprintf(cmd.OutOrStdout(), "[%s] %s Next: %s\n", detail.ReasonCode, detail.Message, detail.UserGuidance[0])
		return err
	},
}

func init() {
	agentDiagnoseCmd.Flags().BoolVar(&agentDiagnoseJSON, "json", false, "Emit the shared recovery contract as JSON; this is guidance, not an observed failure")
	agentCmd.AddCommand(agentDiagnoseCmd)
}
