package commands

import (
	"fmt"

	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/spf13/cobra"
)

// This declaration is part of the released executable. Consumers inspect the
// exact authenticated binary, rather than trusting a mutable sidecar.
const binaryCapabilityManifest = `stackkit.binary-capabilities/v1:{"capabilities":["stackkit.verify.expected-plan-hash.v1"]}:end-stackkit.binary-capabilities`

// rilPrimitiveCapabilityManifest declares that every executor-bound command
// (verify, service start/stop/restart, application control, upgrade, backup
// status and run) refuses a different
// --expected-ril-primitive-hash for its own primitive. The per-primitive
// markers carry the hashes.
const rilPrimitiveCapabilityManifest = `stackkit.binary-capabilities/v1:{"capabilities":["stackkit.ril-primitive-hash.v2"]}:end-stackkit.binary-capabilities`

func init() {
	rootCmd.AddCommand(&cobra.Command{Use: "binary-capabilities", Short: "Print capabilities implemented by this executable", Args: cobra.NoArgs, Run: func(cmd *cobra.Command, _ []string) {
		fmt.Fprintln(cmd.OutOrStdout(), binaryCapabilityManifest)
		fmt.Fprintln(cmd.OutOrStdout(), rilPrimitiveCapabilityManifest)
		for _, marker := range architecturev2.ProductRILPrimitiveMarkersV2 {
			fmt.Fprintln(cmd.OutOrStdout(), marker)
		}
	}})
}
