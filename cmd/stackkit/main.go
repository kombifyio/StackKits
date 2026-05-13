// StackKit CLI - Infrastructure deployment from declarative blueprints
package main

import (
	"os"

	"github.com/kombifyio/stackkits/cmd/stackkit/commands"
)

// Version information (set by build)
var (
	Version   = "dev"
	GitCommit = "unknown"
	BuildDate = "unknown"
)

func main() {
	commands.SetVersionInfo(Version, GitCommit, BuildDate)

	if err := commands.Execute(); err != nil {
		os.Exit(1)
	}
}
