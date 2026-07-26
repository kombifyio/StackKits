package commands

import "github.com/spf13/cobra"

var kitCmd = &cobra.Command{
	Use:   "kit",
	Short: "Published StackKit release operations",
	Long:  "Discover, verify, and install published StackKit release artifacts.",
}

func init() {
	rootCmd.AddCommand(kitCmd)
}
