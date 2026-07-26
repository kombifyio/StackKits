package commands

import (
	"github.com/spf13/cobra"
)

var (
	kitVerifyJSON bool
)

var kitVerifyCmd = &cobra.Command{
	Use:         "verify",
	Short:       "Verify cached release receipts and attestations",
	Long:        "Deprecated alias for 'stackkit verify --offline'. No network request is performed.",
	Deprecated:  "use stackkit verify --offline",
	Annotations: map[string]string{noDeployObservabilityAnnotation: "true"},
	RunE:        runKitVerify,
}

func init() {
	kitVerifyCmd.Flags().BoolVar(&kitVerifyJSON, "json", false, "Emit stackkit.command-result/v1 JSON.")
	kitCmd.AddCommand(kitVerifyCmd)
}

func runKitVerify(cmd *cobra.Command, args []string) error {
	return runOfflineReleaseVerification(cmd, getWorkDir(), kitVerifyJSON)
}
