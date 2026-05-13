package commands

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Long:  `Print the version, git commit, and build date of stackkit.`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("stackkit version %s\n", version)
		fmt.Printf("  Git commit: %s\n", gitCommit)
		fmt.Printf("  Build date: %s\n", buildDate)
		fmt.Printf("  Go version: %s\n", runtime.Version())
		fmt.Printf("  OS/Arch:    %s/%s\n", runtime.GOOS, runtime.GOARCH)
	},
}
