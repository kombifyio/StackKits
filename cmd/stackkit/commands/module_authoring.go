package commands

import "github.com/spf13/cobra"

// Local authoring is part of the public CLI. Publisher builds attach their
// release operations to this same command without making authoring depend on
// private publisher capabilities.
var moduleCmd = &cobra.Command{
	Use:   "module",
	Short: "Author and validate local CUE modules",
	Long:  "Render module facts into CUE contracts and validate their deployment configuration.",
}

func init() {
	rootCmd.AddCommand(moduleCmd)
}
