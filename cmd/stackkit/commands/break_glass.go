package commands

// stackkit break-glass — operator surface for managing per-node recovery
// bundles. The bundles themselves are written by `stackkit apply` (Phase 1)
// from internal/identity. This sub-command does not generate bundles; it
// inspects the bundle directory on the local node so an operator can locate
// the artifact during a recovery exercise.
//
// Sub-commands:
//   list                    list bundles in the recovery dir
//   show-bundle <node>      print a single node's bundle path
//   rotate                  Phase-5 stub
//
// The bundle directory defaults to /var/lib/stackkit/recovery (matching the
// internal/identity default) but is overrideable via --dir or the
// STACKKIT_BREAK_GLASS_DIR environment variable. The env var is the
// hook that tests use to redirect into a temp dir.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// defaultBreakGlassDir mirrors internal/identity.defaultBundleDir. We keep
// a local copy rather than importing the constant so the CLI can render
// an empty-dir hint without dragging the whole identity package in.
const defaultBreakGlassDir = "/var/lib/stackkit/recovery"

// breakGlassDir is the value supplied via --dir (empty = "use default
// or the env-var override"). Resolved by breakGlassDirOrDefault.
var breakGlassDir string

var breakGlassCmd = &cobra.Command{
	Use:   "break-glass",
	Short: "Manage break-glass recovery accounts and bundles",
	Long: `Manage per-node break-glass recovery accounts and the encrypted bundles
that hold their credentials.

Bundles are written by 'stackkit apply' (Phase 1) into /var/lib/stackkit/recovery/.
They are encrypted with the recovery passphrase you set during init.

To recover a node, decrypt the bundle:
    age -d -o break-glass.txt break-glass-<node>.age

Then follow the RESTORE INSTRUCTIONS section inside the decrypted YAML.`,
}

var breakGlassListCmd = &cobra.Command{
	Use:   "list",
	Short: "List break-glass bundles on this host",
	RunE: func(cmd *cobra.Command, args []string) error {
		dir := breakGlassDirOrDefault()
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				cmd.Printf("No break-glass bundles found (directory %s does not exist).\n", dir)
				cmd.Println("Run 'stackkit apply' to provision a node and generate a bundle.")
				return nil
			}
			return fmt.Errorf("read %s: %w", dir, err)
		}

		bundles := []string{}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			// Only the encrypted artifact is the disaster-recovery item.
			// Plaintext .txt convenience copies are intentionally ignored
			// here so `list` mirrors what is safe to back up off-host.
			if strings.HasPrefix(name, "break-glass-") && strings.HasSuffix(name, ".age") {
				bundles = append(bundles, name)
			}
		}

		if len(bundles) == 0 {
			cmd.Printf("No break-glass bundles found in %s.\n", dir)
			return nil
		}

		cmd.Printf("Break-glass bundles in %s:\n", dir)
		for _, name := range bundles {
			node := strings.TrimSuffix(strings.TrimPrefix(name, "break-glass-"), ".age")
			cmd.Printf("  %s  (node: %s)\n", name, node)
		}
		return nil
	},
}

var breakGlassShowBundleCmd = &cobra.Command{
	Use:   "show-bundle <node>",
	Short: "Print the path to a node's encrypted bundle",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		node := args[0]
		// Reject anything that could traverse out of the bundle dir or
		// embed a path separator. We only want simple node names here;
		// callers that need to read an arbitrary file should just `ls`.
		if node == "" || strings.ContainsAny(node, "/\\") {
			return fmt.Errorf("invalid node name %q", node)
		}

		dir := breakGlassDirOrDefault()
		path := filepath.Join(dir, "break-glass-"+node+".age")
		info, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("no bundle found for node %q at %s", node, path)
			}
			return fmt.Errorf("stat %s: %w", path, err)
		}

		// Path on stdout (so the caller can pipe it into age -d / cp / etc.),
		// metadata on stderr so it does not pollute the pipe.
		cmd.Printf("%s\n", path)
		fmt.Fprintf(cmd.ErrOrStderr(), "Size: %d bytes\nModified: %s\n",
			info.Size(),
			info.ModTime().Format("2006-01-02 15:04:05"))
		return nil
	},
}

var breakGlassRotateCmd = &cobra.Command{
	Use:   "rotate",
	Short: "Generate a new break-glass account on this node and re-issue the bundle (Phase 5)",
	Long: `Rotate the per-node break-glass credentials and re-issue the bundle.

This is part of Phase 5 of the owner & break-glass-admin roadmap and is
not yet implemented. Until it lands, rotate manually by re-running
'stackkit apply' on the node — the apply orchestrator will regenerate
the per-node break-glass admin and write a fresh bundle.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return fmt.Errorf("break-glass rotate is Phase 5, not yet implemented (see docs/superpowers/plans/2026-04-28-phase-1-standalone-firstnode-localowner.md for the current roadmap)")
	},
}

func init() {
	breakGlassCmd.PersistentFlags().StringVar(&breakGlassDir, "dir", "",
		"Override the bundle directory (default: /var/lib/stackkit/recovery)")
	breakGlassCmd.AddCommand(breakGlassListCmd, breakGlassShowBundleCmd, breakGlassRotateCmd)
	rootCmd.AddCommand(breakGlassCmd)
}

// breakGlassDirOrDefault resolves the bundle directory in priority order:
// explicit --dir flag, then STACKKIT_BREAK_GLASS_DIR env var (used by tests
// to redirect into a temp dir), then the system default.
func breakGlassDirOrDefault() string {
	if breakGlassDir != "" {
		return breakGlassDir
	}
	if envDir := os.Getenv("STACKKIT_BREAK_GLASS_DIR"); envDir != "" {
		return envDir
	}
	return defaultBreakGlassDir
}
