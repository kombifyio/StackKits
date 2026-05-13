// stackkit-backup-agent is the per-host orchestrator for the kombify
// Backup-Controller (Phase 4 of the backup rollout — see
// docs/plans/2026-05-01-backup-rollout.md).
//
// In production this binary:
//  1. authenticates against the controller using a per-host token
//     minted at enrollment time;
//  2. pulls JobMessages from the controller's queue;
//  3. executes them by exec'ing the local kopia client (the same
//     binary the addon already runs); and
//  4. reports status back to the controller.
//
// This file is the SCAFFOLD entry point. It defines the CLI surface and
// the configuration shape but does not yet talk to a controller because
// the controller's network listener is not stood up. When the
// follow-up PR wires the controller to a port, this binary will work
// end-to-end without any CLI changes.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
)

const usage = `stackkit-backup-agent

Usage:
  stackkit-backup-agent enroll --token <t> --endpoint <url>
  stackkit-backup-agent run    --config <path>
  stackkit-backup-agent status

Flags:
  --token      Per-host agent token, issued by the kombify-Backup-Controller.
               Shown ONCE in the kombify-TechStack dashboard at enrollment time.
  --endpoint   Controller base URL (e.g. https://backup.kombify.io).
  --config     Path to agent config (default: /etc/stackkit/backup-agent.yaml).

Notes:
  This is a Phase-4 scaffold. The controller endpoint is not operational
  yet; the binary parses flags and validates them, then exits with a
  clear "not implemented" message. Track progress in
  docs/plans/2026-05-01-backup-rollout.md.
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run is the testable dispatcher. It returns the process exit code so
// unit tests can verify both the contract (what the binary does for
// each subcommand today) and its evolution (what it does after the
// controller wire-up lands).
//
// Exit-code conventions:
//
//	0  — handled cleanly (status, --help)
//	1  — known scaffold limitation hit (enroll/run before Phase-4 lands)
//	2  — usage error (missing flag, unknown subcommand)
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) < 1 {
		fmt.Fprintf(stderr, "%s", usage)
		return 2
	}
	switch args[0] {
	case "enroll":
		return runEnroll(args[1:], stdout, stderr)
	case "run":
		return runAgent(args[1:], stdout, stderr)
	case "status":
		return runStatus(stdout)
	case "-h", "--help", "help":
		fmt.Fprintf(stdout, "%s", usage)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown subcommand %q\n\n%s", args[0], usage)
		return 2
	}
}

func runEnroll(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("enroll", flag.ContinueOnError)
	fs.SetOutput(stderr)
	token := fs.String("token", "", "Per-host agent token from kombify-TechStack")
	endpoint := fs.String("endpoint", "", "Controller base URL")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *token == "" {
		fmt.Fprintf(stderr, "✗ enroll: --token is required\n")
		return 2
	}
	if *endpoint == "" {
		fmt.Fprintf(stderr, "✗ enroll: --endpoint is required\n")
		return 2
	}
	fmt.Fprintf(stdout, "→ would enroll against %s with token %s…\n", *endpoint, truncate(*token, 12))
	fmt.Fprintf(stdout, "✗ not implemented yet (Phase 4 of the backup rollout)\n")
	return 1
}

func runAgent(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfg := fs.String("config", "/etc/stackkit/backup-agent.yaml", "Path to agent config")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	fmt.Fprintf(stdout, "→ would load config from %s and start pulling jobs from controller…\n", *cfg)
	fmt.Fprintf(stdout, "✗ not implemented yet (Phase 4 of the backup rollout)\n")
	return 1
}

func runStatus(stdout io.Writer) int {
	fmt.Fprintf(stdout, "stackkit-backup-agent: scaffold (Phase 4 of the backup rollout)\n")
	fmt.Fprintf(stdout, "  controller endpoint: <not configured>\n")
	fmt.Fprintf(stdout, "  enrolled:            no\n")
	fmt.Fprintf(stdout, "  see docs/plans/2026-05-01-backup-rollout.md for status\n")
	return 0
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
