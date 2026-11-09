package commands

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/identity"
	"github.com/spf13/cobra"
)

func newDeviceCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "device",
		Short: "Enrollment bootstrap, devices, notices and revocation at the Authority (the Owner's local console)",
		Long: "Each person enrolls their own devices: the device client signs them in with their own passkey and enrolls " +
			"its own key, automatically at home through the LAN enrollment listener and confirmed away by their own " +
			"enrolled device or the Owner. These commands are the Owner's console at the Authority: show the enrollment " +
			"bootstrap, bind the Home trust, list devices and notices, undo an enrollment and revoke any device.",
		Annotations: map[string]string{noDeployObservabilityAnnotation: "true"},
	}
	command.AddCommand(newDeviceBootstrapCommand(), newDeviceTrustCommand(), newDeviceListCommand(), newDeviceNoticesCommand(),
		newDeviceUndoCommand(), newDeviceRevokeCommand(), newDeviceOverlayCommand(), newDeviceSignerCommand())
	return command
}

func newDeviceBootstrapCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "bootstrap",
		Short: "Print the LAN enrollment bootstrap a device client scans (listener URL, TLS name, root fingerprint)",
		Long: "Print where the LAN enrollment listener is, which server name its certificate carries and the step-ca root " +
			"fingerprint a device pins, as JSON and as the stackkit-enroll URI, and the Companion enrollment address: the " +
			"Hub origin with the Owner key fingerprint (#okf=), which a person enters in the Companion so it trusts the " +
			"Owner-signed listener and root even when the Hub is reached through an edge. It contains no secret: a device " +
			"enrolls only with its person's own passkey sign-in.",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			bootstrap, err := identity.LoadHomeEnrollmentBootstrap(getWorkDir())
			if err != nil {
				return err
			}
			result := map[string]any{"bootstrap": bootstrap}
			if address, addressErr := identity.HomeCompanionEnrollmentAddress(getWorkDir()); addressErr == nil {
				result["companionAddress"] = address
			}
			return writeDeviceJSON(cmd.OutOrStdout(), result)
		},
	}
}

func newDeviceTrustCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "trust",
		Short: "Bind the Owner-signed Home trust to the live PocketID and register the device sign-in client",
		Long: "Register the stackkit-home-device PocketID client on first use (a public PKCE client for the Homelab groups " +
			"that requires a passkey on every sign-in), read the live issuer, signing keys and Owner subject, and bind them " +
			"as the Owner-signed Home trust for 24 hours, keeping every revoked and removed account revoked. The LAN " +
			"enrollment listener refreshes it every hour; run this once after setup or to repair it.",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			root, now := getWorkDir(), time.Now()
			trust, err := identity.RefreshHomePublicTrust(commandContext(cmd), root, now)
			if err != nil {
				return err
			}
			// Revoked accounts lose their VPN add-on peers with the binding.
			overlay, err := resyncHomeOverlayIfActive(commandContext(cmd), root, now)
			if err != nil {
				return fmt.Errorf("Home trust bound, but the running overlay was not updated: %w", err)
			}
			return writeDeviceJSON(cmd.OutOrStdout(), map[string]any{
				"issuer": trust.PocketID.Issuer, "clientId": trust.PocketID.ClientID, "ownerSubject": trust.PocketID.Subject,
				"revokedAccounts": trust.RevokedSubjects, "issuedAt": trust.IssuedAt, "expiresAt": trust.ExpiresAt, "overlay": overlay,
			})
		},
	}
}

func newDeviceListCommand() *cobra.Command {
	return &cobra.Command{
		Use:           "list",
		Short:         "List every device of every Homelab account: enrolled, pending and revoked",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			devices, err := identity.ListHomeDevices(getWorkDir())
			if err != nil {
				return err
			}
			return writeDeviceJSON(cmd.OutOrStdout(), map[string]any{"devices": devices})
		},
	}
}

func newDeviceNoticesCommand() *cobra.Command {
	return &cobra.Command{
		Use:           "notices",
		Short:         "List enrollment notices: at-home enrollments with their 24 hour undo and pending away enrollments",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			notices, err := identity.ListHomeEnrollmentNotices(getWorkDir(), identity.HomeActor{Owner: true}, time.Now())
			if err != nil {
				return err
			}
			return writeDeviceJSON(cmd.OutOrStdout(), map[string]any{"notices": notices})
		},
	}
}

func newDeviceUndoCommand() *cobra.Command {
	return &cobra.Command{
		Use:           "undo <notice-id>",
		Short:         "Undo an at-home enrollment within 24 hours, or decline a pending one, by revoking its device",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, now := getWorkDir(), time.Now()
			revocation, err := identity.UndoHomeEnrollment(root, strings.TrimSpace(args[0]), identity.HomeActor{Owner: true}, now)
			if err != nil {
				return err
			}
			return writeDeviceRevocation(cmd, root, revocation, now)
		},
	}
}

func newDeviceRevokeCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "revoke <device-id>",
		Short: "Permanently revoke any device key and everything it holds",
		Long: "Revoke a device key as the Owner: its renewals stop at once, verifiers refuse its credentials within the " +
			"revocation refresh, its VPN add-on peer is removed (and a running overlay resynced) and the output lists " +
			"each revocation-registry artifact. The key can never enroll again.",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, now := getWorkDir(), time.Now()
			revocation, err := identity.RevokeHomeDevice(root, strings.TrimSpace(args[0]), identity.HomeActor{Owner: true}, now)
			if err != nil {
				return err
			}
			return writeDeviceRevocation(cmd, root, revocation, now)
		},
	}
}

// writeDeviceRevocation resyncs a running VPN add-on overlay, so the revoked
// device loses its peer now rather than at the next manual render, and prints
// the revocation.
func writeDeviceRevocation(cmd *cobra.Command, root string, revocation identity.HomeRevocation, now time.Time) error {
	overlay, err := resyncHomeOverlayIfActive(commandContext(cmd), root, now)
	if err != nil {
		return fmt.Errorf("device revoked, but the running overlay still admits it: %w", err)
	}
	return writeDeviceJSON(cmd.OutOrStdout(), map[string]any{"revocation": revocation, "overlay": overlay})
}

func newDeviceOverlayCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "overlay",
		Short: "Join enrolled devices to the optional WireGuard VPN add-on",
		Long: "The VPN add-on is never the default: the Owner switches it on in addition to the standard reach paths, " +
			"and every request through it still carries the sign-in and device proof its route asks for. " +
			"The node generates its WireGuard private key into local custody; it is never printed or rendered. " +
			"Each device generates its own key pair and registers only its public key. Revoked or expired devices " +
			"drop out of the rendered interface configuration. No kombify account is involved.",
	}
	var outFile string
	render := &cobra.Command{
		Use:           "render",
		Short:         "Render the node's wg-quick configuration (public keys only; the private key loads from custody)",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			config, err := identity.RenderHomeOverlayConfig(getWorkDir(), time.Now())
			if err != nil {
				return err
			}
			if outFile == "" {
				_, err = cmd.OutOrStdout().Write(config)
				return err
			}
			if err := os.WriteFile(outFile, config, 0o640); err != nil {
				return fmt.Errorf("device overlay render: write config: %w", err)
			}
			return writeDeviceJSON(cmd.OutOrStdout(), map[string]any{"configFile": outFile})
		},
	}
	render.Flags().StringVar(&outFile, "out", "", "write the configuration to this file instead of stdout")
	var publicKey string
	bind := &cobra.Command{
		Use:           "bind <device-id>",
		Short:         "Register an enrolled device's WireGuard public key and assign its overlay address",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root := getWorkDir()
			peer, err := identity.BindHomeOverlayPeer(root, strings.TrimSpace(args[0]), publicKey, time.Now())
			if err != nil {
				return err
			}
			node, err := identity.EnsureHomeOverlayNodeKey(root)
			if err != nil {
				return err
			}
			overlay, err := resyncHomeOverlayIfActive(commandContext(cmd), root, time.Now())
			if err != nil {
				return fmt.Errorf("device bound, but the running overlay was not updated: %w", err)
			}
			return writeDeviceJSON(cmd.OutOrStdout(), map[string]any{"peer": peer, "node": node, "overlay": overlay})
		},
	}
	up := &cobra.Command{
		Use:   "up",
		Short: "Create or resync the node's WireGuard overlay interface and enable it at boot (requires root)",
		Long: "Installs the rendered configuration as /etc/wireguard/" + identity.HomeOverlayInterfaceName + ".conf, " +
			"starts and enables wg-quick@" + identity.HomeOverlayInterfaceName + " so the interface returns after a reboot, " +
			"or resyncs the peers of a running interface in place. Fails closed when the kernel module, " +
			"wireguard-tools or systemd are missing. Safe to repeat.",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			result, err := ensureHomeOverlayInterface(commandContext(cmd), getWorkDir(), time.Now())
			if err != nil {
				return err
			}
			return writeDeviceJSON(cmd.OutOrStdout(), map[string]any{"overlay": result})
		},
	}
	bind.Flags().StringVar(&publicKey, "public-key", "", "the device's base64 WireGuard public key (generated on the device)")
	_ = bind.MarkFlagRequired("public-key")
	command.AddCommand(bind, render, up)
	return command
}

func writeDeviceJSON(out io.Writer, value any) error {
	encoder := json.NewEncoder(out)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}
