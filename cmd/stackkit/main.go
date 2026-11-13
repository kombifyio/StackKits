// StackKit CLI - Infrastructure deployment from declarative blueprints
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/kombifyio/stackkits/cmd/stackkit/commands"
	"github.com/kombifyio/stackkits/internal/ownappfirewall"
	"github.com/kombifyio/stackkits/internal/ownapplication"
	"github.com/kombifyio/stackkits/internal/runtimeprofile"
	"github.com/kombifyio/stackkits/internal/secretexec"
)

// Version information (set by build)
var (
	Version   = "dev"
	GitCommit = "unknown"
	BuildDate = "unknown"
)

func main() {
	if len(os.Args) == 3 && os.Args[1] == "--own-egress-watch" {
		if err := ownappfirewall.WatchMain(os.Args[2]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) == 3 && os.Args[1] == "--own-egress-restore" {
		// A permanent refusal exits with RestorePermanentExit, which the unit's
		// RestartPreventExitStatus keeps from retrying every 30 seconds.
		if err := ownappfirewall.RestoreMain(os.Args[2], ownapplication.ReviewedFirewall); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(ownappfirewall.RestoreExitCode(err))
		}
		return
	}
	// Started as a native container's governed entrypoint shim: read the
	// mounted secret files and exec the image's own entrypoint.
	if secretexec.Invoked(os.Args[0]) {
		os.Exit(secretexec.Main(os.Args[1:]))
	}
	commands.SetVersionInfo(Version, GitCommit, BuildDate)

	if isAPIMode(os.Args) {
		if err := runtimeprofile.Boot(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(runAPIMode(context.Background(), os.Args[2:]))
	}

	if err := commands.Execute(); err != nil {
		// A host refused by preflight exits distinctly, so an installer or
		// orchestrator can tell an unusable device from a broken rollout.
		os.Exit(commands.ExitCode(err))
	}
}
