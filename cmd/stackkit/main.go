// StackKit CLI - Infrastructure deployment from declarative blueprints
package main

import (
	"context"
	"os"

	"github.com/kombifyio/stackkits/cmd/stackkit/commands"
	"github.com/kombifyio/stackkits/internal/secretexec"
)

// Version information (set by build)
var (
	Version   = "dev"
	GitCommit = "unknown"
	BuildDate = "unknown"
)

func main() {
	// Started as a native container's governed entrypoint shim: read the
	// mounted secret files and exec the image's own entrypoint.
	if secretexec.Invoked(os.Args[0]) {
		os.Exit(secretexec.Main(os.Args[1:]))
	}
	commands.SetVersionInfo(Version, GitCommit, BuildDate)

	if isAPIMode(os.Args) {
		os.Exit(runAPIMode(context.Background(), os.Args[2:]))
	}

	if err := commands.Execute(); err != nil {
		// A host refused by preflight exits distinctly, so an installer or
		// orchestrator can tell an unusable device from a broken rollout.
		os.Exit(commands.ExitCode(err))
	}
}
