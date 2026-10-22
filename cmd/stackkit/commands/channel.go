package commands

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/kombifyio/stackkits/internal/releasechannel"
	"github.com/spf13/cobra"
)

// channelDir is the per-user directory holding the channel choice; the shell
// installer reads the same file.
var channelDir = func() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home directory: %w", err)
	}
	return filepath.Join(home, ".stackkits"), nil
}

func newChannelCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "channel",
		Short: "Show or set the release channel (stable or early-access)",
		Long: `Show which release channel this machine follows.

stable is the default. early-access is the opt-in channel: it gets builds the
StackKits team switched live before they become the next stable release, and
the newest stable release whenever that is newer. Early Access builds are
unsigned prereleases. The choice is stored in ~/.stackkits/channel and read by
the installer (install.sh), which fetches the newest release of that channel.`,
		Example: `  # Show the current channel
  stackkit channel

  # Follow Early Access, then re-run the installer
  stackkit channel set early-access`,
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			noDeployObservabilityAnnotation: "true",
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir, err := channelDir()
			if err != nil {
				return err
			}
			current, err := releasechannel.Read(dir)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Release channel: %s\n", current.Label())
			return err
		},
	}
	command.AddCommand(&cobra.Command{
		Use:   "set <stable|early-access>",
		Short: "Choose the release channel",
		Args:  cobra.ExactArgs(1),
		Annotations: map[string]string{
			noDeployObservabilityAnnotation: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			selected, err := releasechannel.Parse(args[0])
			if err != nil {
				return err
			}
			dir, err := channelDir()
			if err != nil {
				return err
			}
			if err := releasechannel.Write(dir, selected); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			_, _ = fmt.Fprintf(out, "Release channel: %s\n", selected.Label())
			if selected == releasechannel.Stable {
				_, _ = fmt.Fprintln(out, "A running Early Access build stays installed until a stable release at least as new is published; the installer never downgrades it on its own.")
			} else {
				_, _ = fmt.Fprintln(out, "Run the installer again to fetch the newest build of this channel: curl -sSL https://install.stackkit.cc | sh")
			}
			return nil
		},
	})
	return command
}
