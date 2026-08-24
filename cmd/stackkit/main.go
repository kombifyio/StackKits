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
		// A host refused by preflight exits distinctly, so an installer or
		// orchestrator can tell an unusable device from a broken rollout.
		os.Exit(commands.ExitCode(err))
	}
}
