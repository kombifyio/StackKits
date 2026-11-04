package commands

import (
	"fmt"
	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/spf13/cobra"
)

// This declaration is part of the released executable. Consumers inspect the
// exact authenticated binary, rather than trusting a mutable sidecar.
const binaryCapabilityManifest = `stackkit.binary-capabilities/v1:{"capabilities":["stackkit.verify.expected-plan-hash.v1"]}:end-stackkit.binary-capabilities`

const rilPrimitiveCapabilityManifest = `stackkit.binary-capabilities/v1:{"capabilities":["stackkit.verify.ril-primitive-hash.v1"]}:end-stackkit.binary-capabilities`

func init() {
	rootCmd.AddCommand(&cobra.Command{Use: "binary-capabilities", Short: "Print capabilities implemented by this executable", Args: cobra.NoArgs, Run: func(cmd *cobra.Command, _ []string) {
		fmt.Fprintln(cmd.OutOrStdout(), binaryCapabilityManifest)
		fmt.Fprintln(cmd.OutOrStdout(), rilPrimitiveCapabilityManifest)
		fmt.Fprintln(cmd.OutOrStdout(), architecturev2.ProductRILVerifyMarkerV1)
	}})
}
