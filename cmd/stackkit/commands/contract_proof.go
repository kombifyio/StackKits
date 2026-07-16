package commands

import (
	"fmt"

	"github.com/kombifyio/stackkits/internal/architecturecontractproof"
	"github.com/spf13/cobra"
)

var contractProofRepoRoot string

var contractProofCmd = &cobra.Command{
	Use:    "contract-proof",
	Short:  "Verify the packaged Architecture v2 contract fixture",
	Hidden: true,
	Args:   cobra.NoArgs,
	RunE: func(_ *cobra.Command, _ []string) error {
		if err := architecturecontractproof.VerifyRepository(contractProofRepoRoot); err != nil {
			return fmt.Errorf("architecture v2 contract proof failed: %w", err)
		}
		printSuccess("Architecture v2 contract fixture was reproduced under the embedded authority")
		return nil
	},
}

func init() {
	contractProofCmd.Flags().StringVar(&contractProofRepoRoot, "repo-root", ".", "Root of the packaged StackKits archive")
}
