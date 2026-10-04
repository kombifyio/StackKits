package commands

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/kombifyio/stackkits/internal/netmove"
	"github.com/spf13/cobra"
)

func newNetworkWifiCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "wifi",
		Short: "Pre-stage the destination WiFi before moving the server",
		Long: `Stage WiFi networks in the host's own network configuration so the server
joins the destination WiFi by itself after the move. NetworkManager is used
when it is running, otherwise netplan. The wired DHCP connection is not touched
and passphrases are written only to the system network configuration (root
only, mode 0600); they are never printed or stored by StackKits.`,
	}
	command.AddCommand(newNetworkWifiAddCommand(), newNetworkWifiListCommand(), newNetworkWifiRemoveCommand())
	return command
}

func newNetworkWifiAddCommand() *cobra.Command {
	var (
		ssid          string
		password      string
		passwordStdin bool
		open          bool
		priority      int
		applyNow      bool
		force         bool
	)
	command := &cobra.Command{
		Use:   "add",
		Short: "Stage a WiFi network that is joined automatically when in range",
		Example: `  # Read the passphrase from stdin so it never appears in the process list
  printf '%s' "$WIFI_PASSPHRASE" | sudo stackkit network wifi add --ssid OfficeNet --password-stdin

  # Prefer this network over others staged on the host
  sudo stackkit network wifi add --ssid OfficeNet --password-stdin --priority 20 < passphrase.txt`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.TrimSpace(ssid) == "" {
				return errors.New("--ssid is required")
			}
			switch {
			case passwordStdin && password != "":
				return errors.New("use either --password-stdin or --password, not both")
			case passwordStdin:
				line, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
				if err != nil && line == "" {
					return errors.New("--password-stdin: no passphrase on stdin")
				}
				password = strings.TrimRight(line, "\r\n")
			case password == "" && !open:
				return errors.New("a passphrase is required: pass --password-stdin (preferred) or --password; use --open for a network without one")
			}
			credential := netmove.Credential{SSID: ssid, Password: password, Priority: priority}
			if err := credential.Validate(); err != nil {
				return err
			}
			sys := netmove.DefaultSys()
			if len(netmove.WirelessInterfaces(sys)) == 0 && !force {
				return netmove.ErrNoWirelessInterface
			}
			backend, err := netmove.DetectBackend(cmd.Context(), sys)
			if err != nil {
				return err
			}
			if err := backend.Add(cmd.Context(), credential, applyNow); err != nil {
				return err
			}
			printSuccess("Staged WiFi %q through %s.", ssid, backend.Name())
			if backend.Name() == "netplan" && !applyNow {
				printInfo("The change is generated but not applied, so the current connection is undisturbed. It takes effect at the next boot or with --apply-now.")
			}
			printInfo("After the move the server joins it automatically and re-binds itself.")
			return nil
		},
	}
	command.Flags().StringVar(&ssid, "ssid", "", "Name of the WiFi network")
	command.Flags().BoolVar(&passwordStdin, "password-stdin", false, "Read the passphrase from the first line of stdin")
	command.Flags().StringVar(&password, "password", "", "Passphrase (visible in the process list; prefer --password-stdin)")
	command.Flags().BoolVar(&open, "open", false, "Stage a network without a passphrase")
	command.Flags().IntVar(&priority, "priority", 0, "Connection priority; higher wins (NetworkManager only)")
	command.Flags().BoolVar(&applyNow, "apply-now", false, "Apply the change immediately (netplan apply) instead of at the next boot")
	command.Flags().BoolVar(&force, "force", false, "Stage the network even when this host has no wireless interface")
	return command
}

func newNetworkWifiListCommand() *cobra.Command {
	var asJSON bool
	command := &cobra.Command{
		Use:           "list",
		Short:         "List the staged WiFi networks (never their passphrases)",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			networks, backendName, err := listStagedWifi(cmd.Context())
			if err != nil {
				return err
			}
			if asJSON {
				encoded, err := json.Marshal(map[string]any{"backend": backendName, "networks": networks})
				if err != nil {
					return err
				}
				_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s\n", encoded)
				return err
			}
			if len(networks) == 0 {
				printInfo("No WiFi network is staged (%s).", backendName)
				return nil
			}
			for _, network := range networks {
				printInfo("%s (%s, priority %d)", network.SSID, network.Backend, network.Priority)
			}
			return nil
		},
	}
	command.Flags().BoolVar(&asJSON, "json", false, "Emit the list as JSON")
	return command
}

func listStagedWifi(ctx context.Context) ([]netmove.Network, string, error) {
	backend, err := netmove.DetectBackend(ctx, netmove.DefaultSys())
	if err != nil {
		return nil, "", err
	}
	networks, err := backend.List()
	return networks, backend.Name(), err
}

func newNetworkWifiRemoveCommand() *cobra.Command {
	var (
		ssid     string
		applyNow bool
	)
	command := &cobra.Command{
		Use:           "remove",
		Short:         "Remove a staged WiFi network",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.TrimSpace(ssid) == "" {
				return errors.New("--ssid is required")
			}
			backend, err := netmove.DetectBackend(cmd.Context(), netmove.DefaultSys())
			if err != nil {
				return err
			}
			removed, err := backend.Remove(cmd.Context(), ssid, applyNow)
			if err != nil {
				return err
			}
			if !removed {
				printInfo("WiFi %q is not staged.", ssid)
				return nil
			}
			printSuccess("Removed staged WiFi %q.", ssid)
			return nil
		},
	}
	command.Flags().StringVar(&ssid, "ssid", "", "Name of the WiFi network to remove")
	command.Flags().BoolVar(&applyNow, "apply-now", false, "Apply the change immediately (netplan apply)")
	return command
}
