package commands

import (
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/kombifyio/stackkits/internal/identity"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/spf13/cobra"
)

func newDeviceSignerCommand() *cobra.Command {
	var socket string
	peerUID := -1
	command := &cobra.Command{
		Use:   "signer",
		Short: "Run the Authority's host-side identity signer for the Basement core (Apply installs it as a systemd service)",
		Long: "The Basement core's identity workspace holds verification custody only. This process keeps the Owner custody, " +
			"the step-ca root and the Pocket ID admin key on the host: it keeps the Owner-signed Home trust, the public " +
			"Authority record, the household groups and the recovery notices current, and answers the core over a Unix " +
			"socket that only the core server's account may use: a certificate for the LAN enrollment listener's own names, " +
			"and the Owner device recovery routes. stackkit apply installs and starts it as stackkit-identity-signer.service.",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			root := getWorkDir()
			if socket == "" {
				socket = filepath.Join(root, ".stackkit", "identity-signer", "signer.sock")
			}
			if peerUID < 0 {
				uid, _, err := localevidence.StackKitServerOwner(root)
				if err != nil {
					return err
				}
				peerUID = uid
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return identity.ServeHomeAuthoritySigner(ctx, root, socket, peerUID)
		},
	}
	command.Flags().StringVar(&socket, "socket", "", "Unix socket path (default: .stackkit/identity-signer/signer.sock in the workspace)")
	command.Flags().IntVar(&peerUID, "peer-uid", -1, "The only account besides root that may call the signer (default: the core stackkit-server's uid)")
	return command
}
