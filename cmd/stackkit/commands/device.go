package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/identity"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/spf13/cobra"
)

func newDeviceCommand() *cobra.Command {
	command := &cobra.Command{
		Use:         "device",
		Short:       "Enroll, list and revoke household devices (CLI token fallback of the State Console ceremony)",
		Annotations: map[string]string{noDeployObservabilityAnnotation: "true"},
	}
	command.AddCommand(newDeviceEnrollCommand(), newDeviceListCommand(), newDeviceRevokeCommand(), newDeviceOverlayCommand())
	return command
}

type deviceEnrollOptions struct {
	ownerSessionFile string
	code             string
	csrFile          string
	outFile          string
}

func newDeviceEnrollCommand() *cobra.Command {
	options := &deviceEnrollOptions{}
	command := &cobra.Command{
		Use:   "enroll",
		Short: "Issue a single-use pairing token after Owner step-up, or redeem one with a device CSR",
		Long: "With --owner-session-file, issue a single-use 300-second pairing token; the file holds a PocketID " +
			"Owner session authenticated within the last 60 seconds ('-' reads stdin). With --code and --csr, redeem " +
			"the token for the device-generated key in the CSR and receive a client certificate from the node-local " +
			"step-ca device provisioner. Both steps also combine in one call. Enrollment is refused from outside the LAN.",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDeviceEnroll(cmd, getWorkDir(), options, time.Now())
		},
	}
	command.Flags().StringVar(&options.ownerSessionFile, "owner-session-file", "", "file with a fresh PocketID Owner session token ('-' for stdin); issues a pairing token")
	command.Flags().StringVar(&options.code, "code", "", "single-use pairing token to redeem")
	command.Flags().StringVar(&options.csrFile, "csr", "", "PEM certificate request generated on the device")
	command.Flags().StringVar(&options.outFile, "out", "", "write the issued certificate chain to this file instead of stdout")
	return command
}

func runDeviceEnroll(cmd *cobra.Command, root string, options *deviceEnrollOptions, now time.Time) error {
	if options.ownerSessionFile == "" && (options.code == "" || options.csrFile == "") {
		return errors.New("device enroll: pass --owner-session-file to issue a pairing token, or --code with --csr to redeem one")
	}
	if options.ownerSessionFile != "" && options.code != "" {
		return errors.New("device enroll: --code redeems an existing token; omit --owner-session-file")
	}
	result := map[string]any{}
	code := options.code
	if options.ownerSessionFile != "" {
		session, err := readDeviceSecretInput(cmd.InOrStdin(), options.ownerSessionFile)
		if err != nil {
			return err
		}
		pairing, err := identity.IssueHomePairing(root, session, now)
		if err != nil {
			return err
		}
		code = pairing.Code
		if options.csrFile == "" {
			result["pairing"] = pairing
			return writeDeviceJSON(cmd.OutOrStdout(), result)
		}
	}
	csr, err := os.ReadFile(options.csrFile)
	if err != nil {
		return fmt.Errorf("device enroll: read CSR: %w", err)
	}
	issuer := func(ctx context.Context, csrPEM []byte, ttl int) ([]byte, error) {
		return localevidence.IssueHomeDeviceCertificate(ctx, root, csrPEM, ttl)
	}
	device, chain, err := identity.EnrollHomeDeviceWithPairing(commandContext(cmd), root,
		identity.HomeEnrollmentRequest{Code: strings.TrimSpace(code), CSRPEM: csr, LAN: deviceCLIObservedLAN()}, issuer, now)
	if err != nil {
		return err
	}
	result["device"] = device
	if options.outFile != "" {
		if err := os.WriteFile(options.outFile, chain, 0o644); err != nil {
			return fmt.Errorf("device enroll: write certificate: %w", err)
		}
		result["certificateFile"] = options.outFile
	} else {
		result["certificatePEM"] = string(chain)
	}
	return writeDeviceJSON(cmd.OutOrStdout(), result)
}

func newDeviceListCommand() *cobra.Command {
	return &cobra.Command{
		Use:           "list",
		Short:         "List enrolled and revoked household devices",
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

func newDeviceRevokeCommand() *cobra.Command {
	return &cobra.Command{
		Use:           "revoke <device-id>",
		Short:         "Permanently revoke a household device key",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, now := getWorkDir(), time.Now()
			device, err := identity.RevokeHomeDevice(root, strings.TrimSpace(args[0]), now)
			if err != nil {
				return err
			}
			// A revoked device must lose overlay access now, not at the next manual render.
			overlay, err := resyncHomeOverlayIfActive(commandContext(cmd), root, now)
			if err != nil {
				return fmt.Errorf("device revoked, but the running overlay still admits it: %w", err)
			}
			return writeDeviceJSON(cmd.OutOrStdout(), map[string]any{"device": device, "overlay": overlay})
		},
	}
}

func newDeviceOverlayCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "overlay",
		Short: "Join enrolled devices to the kit-owned WireGuard overlay for remote-private access",
		Long: "The node generates its WireGuard private key into local custody; it is never printed or rendered. " +
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

// deviceCLIObservedLAN admits a node-local console, or an SSH session whose
// client the transport reports as a LAN peer. A remote SSH session is refused
// like any other off-LAN enrollment.
func deviceCLIObservedLAN() bool {
	connection := strings.Fields(os.Getenv("SSH_CONNECTION"))
	if len(connection) == 0 {
		return true
	}
	return identity.ObservedLANAddress(connection[0])
}

func readDeviceSecretInput(stdin io.Reader, path string) (string, error) {
	var raw []byte
	var err error
	if path == "-" {
		raw, err = io.ReadAll(io.LimitReader(stdin, 64<<10))
	} else {
		raw, err = os.ReadFile(path)
	}
	if err != nil {
		return "", fmt.Errorf("device enroll: read Owner session: %w", err)
	}
	return strings.TrimSpace(string(raw)), nil
}

func writeDeviceJSON(out io.Writer, value any) error {
	encoder := json.NewEncoder(out)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}
