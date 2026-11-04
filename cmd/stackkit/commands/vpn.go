package commands

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/kombifyio/stackkits/internal/hostsecurity"
	"github.com/kombifyio/stackkits/internal/identity"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/spf13/cobra"
)

// stackkit vpn is the Owner's console of the optional VPN add-on at the
// Authority (foundation #VPNAddOnV1, addons/vpn, homelab identity target §8).
// The add-on is never a default: it exists only after "vpn enable".
const (
	vpnServiceUnit = "stackkit-vpn.service"
	vpnSystemdDir  = "/etc/systemd/system"
)

func newVPNCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "vpn",
		Short: "The optional VPN add-on: switch it on with WireGuard, Headscale, Tailscale or Pangolin, list its peers, switch it off",
		Long: "The VPN add-on is never a default in any mode or kit: the Owner switches it on at the Authority, in addition " +
			"to the standard reach paths, with one of four tools. Every enrolled, unrevoked device then receives its peer " +
			"from the peer service on the node site address (port 9444) with its device credential, in the same enrollment; " +
			"revoking the device or removing its person removes the peer, and the reconciler applies it to the live " +
			"interface or the tool's coordination server within seconds. A peer reaches only the node site address, and " +
			"every request through it still carries the sign-in and device proof its route asks for.",
		Annotations: map[string]string{noDeployObservabilityAnnotation: "true"},
	}
	command.AddCommand(newVPNEnableCommand(), newVPNDisableCommand(), newVPNStatusCommand(), newVPNReconcileCommand(), newVPNServeCommand())
	return command
}

type vpnEnableOptions struct {
	tool, endpoint                             string
	tailnet, oauthClientFile                   string
	pangolinEndpoint, pangolinAPI, pangolinOrg string
	pangolinSite                               int
	newtID, newtSecretFile, apiKeyFile, caFile string
}

func newVPNEnableCommand() *cobra.Command {
	options := vpnEnableOptions{}
	command := &cobra.Command{
		Use:   "enable",
		Short: "Switch the VPN add-on on with one tool (requires root at the Authority)",
		Long: "Records the Owner's opt-in, starts the tool (WireGuard on the host's wireguard-tools; Headscale, the Tailscale " +
			"node and Newt as their pinned upstream images), registers the Authority as the tool's node that routes only " +
			"its site address, and starts stackkit-vpn.service: the peer service on port 9444 and the reconciler. " +
			"Tool secrets are read from files into the add-on's custody and never printed.\n\n" +
			"  wireguard  --endpoint host:port (UDP, forwarded to 51820; the peer service is host:9444)\n" +
			"  headscale  --endpoint host:port (TCP, forwarded to 9444: the peer service carries the control endpoint; UDP 3478 for STUN)\n" +
			"  tailscale  --tailnet <name> --oauth-client-file <json with clientId, clientSecret>\n" +
			"  pangolin   --pangolin-endpoint https://… --pangolin-api https://…/v1 --pangolin-org <id> --pangolin-site <n> --newt-id <id> --newt-secret-file f --api-key-file f [--ca-file pem]",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runVPNEnable(commandContext(cmd), cmd, getWorkDir(), options)
		},
	}
	flags := command.Flags()
	flags.StringVar(&options.tool, "tool", "", "wireguard, headscale, tailscale or pangolin (no default)")
	flags.StringVar(&options.endpoint, "endpoint", "", "public host:port devices dial from away (wireguard, headscale)")
	flags.StringVar(&options.tailnet, "tailnet", "", "the Owner's tailnet name (tailscale)")
	flags.StringVar(&options.oauthClientFile, "oauth-client-file", "", "JSON file with the tailnet OAuth client's clientId and clientSecret (tailscale)")
	flags.StringVar(&options.pangolinEndpoint, "pangolin-endpoint", "", "https origin of the Owner's Pangolin server (pangolin)")
	flags.StringVar(&options.pangolinAPI, "pangolin-api", "", "https URL of the Pangolin Integration API, ending in /v1 (pangolin)")
	flags.StringVar(&options.pangolinOrg, "pangolin-org", "", "Pangolin organization ID (pangolin)")
	flags.IntVar(&options.pangolinSite, "pangolin-site", 0, "numeric ID of this Authority's Newt site (pangolin)")
	flags.StringVar(&options.newtID, "newt-id", "", "the site's Newt ID (pangolin)")
	flags.StringVar(&options.newtSecretFile, "newt-secret-file", "", "file with the site's Newt secret (pangolin)")
	flags.StringVar(&options.apiKeyFile, "api-key-file", "", "file with an organization API key that manages clients and site resources (pangolin)")
	flags.StringVar(&options.caFile, "ca-file", "", "PEM of the CA of a self-hosted Pangolin server with a private certificate (pangolin)")
	_ = command.MarkFlagRequired("tool")
	return command
}

func requireVPNHost(needDocker bool) error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("vpn: the VPN add-on runs on a Linux Authority (this is %s)", runtime.GOOS)
	}
	if os.Geteuid() != 0 {
		return errors.New("vpn: the VPN add-on changes interfaces and services; rerun with sudo")
	}
	tools := []string{"systemctl"}
	if needDocker {
		tools = append(tools, "docker")
	}
	for _, tool := range tools {
		if _, err := homeOverlayLookPath(tool); err != nil {
			return fmt.Errorf("vpn: %s is required and was not found", tool)
		}
	}
	return nil
}

func runVPNEnable(ctx context.Context, cmd *cobra.Command, root string, options vpnEnableOptions) error {
	tool := strings.TrimSpace(options.tool)
	if err := requireVPNHost(tool != identity.HomeVPNToolWireGuard); err != nil {
		return err
	}
	current, err := identity.LoadHomeVPNSelection(root)
	switch {
	case err == nil && current.Tool != tool:
		return fmt.Errorf("vpn: the add-on runs the %s tool; run 'sudo stackkit vpn disable' first, which removes its peers", current.Tool)
	case err != nil && !errors.Is(err, identity.ErrHomeVPNOff):
		return err
	}
	selection := identity.HomeVPNSelection{Tool: tool, Endpoint: strings.TrimSpace(options.endpoint), EnabledAt: time.Now().UTC().Truncate(time.Second)}
	if err == nil {
		selection.EnabledAt = current.EnabledAt
		selection.Pangolin = current.Pangolin
	}
	secrets := map[string]string{}
	switch tool {
	case identity.HomeVPNToolTailscale:
		selection.Tailscale = &identity.HomeVPNTailscaleSelection{Tailnet: strings.TrimSpace(options.tailnet)}
		secrets["tailscale-oauth-client.json"] = options.oauthClientFile
	case identity.HomeVPNToolPangolin:
		resource := 0
		if selection.Pangolin != nil && selection.Pangolin.SiteID == options.pangolinSite {
			resource = selection.Pangolin.ResourceID
		}
		selection.Pangolin = &identity.HomeVPNPangolinSelection{
			Endpoint: strings.TrimSuffix(strings.TrimSpace(options.pangolinEndpoint), "/"), API: strings.TrimSpace(options.pangolinAPI),
			OrgID: strings.TrimSpace(options.pangolinOrg), SiteID: options.pangolinSite, NewtID: strings.TrimSpace(options.newtID), ResourceID: resource,
		}
		secrets["pangolin-newt-secret"] = options.newtSecretFile
		secrets["pangolin-api-key"] = options.apiKeyFile
		if options.caFile != "" {
			secrets["pangolin-ca.pem"] = options.caFile
		}
	default:
		selection.Pangolin = nil
	}
	if err := identity.ValidateHomeVPNSelection(selection); err != nil {
		return err
	}
	host, err := loadVPNHostFor(root, selection)
	if err != nil {
		return err
	}
	for name, file := range secrets {
		if file == "" {
			if _, readErr := identity.ReadHomeVPNSecret(root, name); readErr == nil {
				continue // kept from an earlier enable
			}
			return fmt.Errorf("vpn: the %s tool needs its secret file for %s", tool, name)
		}
		raw, readErr := os.ReadFile(file) //nolint:gosec // the Owner names the file
		if readErr != nil {
			return fmt.Errorf("vpn: read %s: %w", file, readErr)
		}
		if err := identity.WriteHomeVPNSecret(root, name, []byte(strings.TrimSpace(string(raw))+"\n")); err != nil {
			return err
		}
	}
	if err := identity.SaveHomeVPNSelection(root, selection); err != nil {
		return err
	}
	driver, err := newVPNDriver(host)
	if err != nil {
		return err
	}
	// A WireGuard interface left from P0 or an earlier opt-in never runs
	// beside another tool.
	if tool != identity.HomeVPNToolWireGuard && (homeOverlayLinkExists(ctx) || homeOverlayUnitInstalled()) {
		if err := teardownHomeOverlayInterface(ctx); err != nil {
			return err
		}
	}
	if err := driver.up(ctx); err != nil {
		if current.Tool != "" {
			return fmt.Errorf("%w (the add-on stays on with %s; fix the cause and run enable again, or disable)", err, current.Tool)
		}
		// A first opt-in that cannot start leaves nothing behind: the add-on
		// stays off, fail closed. If the tool cannot be stopped cleanly the
		// opt-in and custody stay, so disable can finish the job.
		note := ""
		var leftovers vpnLeftoversError
		if downErr := driver.down(ctx); errors.As(downErr, &leftovers) {
			note = "; " + leftovers.Error()
		} else if downErr != nil {
			return fmt.Errorf("%w; the tool did not stop cleanly (%v): run 'sudo stackkit vpn disable'", err, downErr)
		}
		if clearErr := errors.Join(os.RemoveAll(filepath.Join(root, filepath.FromSlash(identity.HomeVPNCustodyDir))), identity.ClearHomeVPNSelection(root)); clearErr != nil {
			return fmt.Errorf("%w; switching it off again failed too: %v", err, clearErr)
		}
		return fmt.Errorf("%w (the VPN add-on stays off%s)", err, note)
	}
	if err := installVPNService(ctx, root); err != nil {
		return err
	}
	firewall, err := setVPNPublicListeners(ctx, tool)
	if err != nil {
		return fmt.Errorf("vpn: the tool runs, but the host firewall does not admit its public ports yet: %w", err)
	}
	host, err = loadVPNHost(root)
	if err != nil {
		return err
	}
	return writeDeviceJSON(cmd.OutOrStdout(), map[string]any{
		"tool": host.selection.Tool, "enabledAt": host.selection.EnabledAt, "endpoint": host.selection.Endpoint,
		"peerService": host.peerServiceOrigins(), "caRootSha256": vpnCARootSHA256(root), "serverName": identity.HomeVPNServerName,
		"systemdUnit": vpnServiceUnit, "hostFirewall": firewall,
	})
}

// vpnPublicListeners are the ports each tool serves to every source, which
// the Owner's router forwards: WireGuard's endpoint and peer service,
// Headscale's peer service (control and DERP), STUN and the node's direct
// port, the Tailscale node's direct port. Newt only dials out.
func vpnPublicListeners(tool string) (tcp, udp []int) {
	switch tool {
	case identity.HomeVPNToolWireGuard:
		return []int{identity.HomeVPNPeerServicePort}, []int{identity.HomeOverlayListenPort}
	case identity.HomeVPNToolHeadscale:
		return []int{identity.HomeVPNPeerServicePort}, []int{3478, 41641}
	case identity.HomeVPNToolTailscale:
		return nil, []int{41641}
	}
	return nil, nil
}

// setVPNPublicListeners opens the tool's public ports in the StackKits Home
// firewall, or closes them for "" (the add-on is off).
func setVPNPublicListeners(ctx context.Context, tool string) (map[string]any, error) {
	tcp, udp := vpnPublicListeners(tool)
	managed, err := hostsecurity.Engine{Host: hostsecurity.LocalHost{}}.SetAddOnPublicListeners(ctx, tcp, udp)
	if err != nil {
		return nil, err
	}
	if !managed {
		return map[string]any{"managed": false, "note": "no StackKits Home firewall on this host; admit the public ports in the host's own firewall"}, nil
	}
	return map[string]any{"managed": true, "publicTcp": tcp, "publicUdp": udp}, nil
}

func newVPNDisableCommand() *cobra.Command {
	return &cobra.Command{
		Use:           "disable",
		Short:         "Switch the VPN add-on off: remove every peer, the tool's runtime and the peer service (requires root)",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, root := commandContext(cmd), getWorkDir()
			selection, err := identity.LoadHomeVPNSelection(root)
			if errors.Is(err, identity.ErrHomeVPNOff) {
				return writeDeviceJSON(cmd.OutOrStdout(), map[string]any{"vpn": "off"})
			}
			if err != nil {
				return err
			}
			if err := requireVPNHost(selection.Tool != identity.HomeVPNToolWireGuard); err != nil {
				return err
			}
			host, err := loadVPNHostFor(root, selection)
			if err != nil {
				return err
			}
			driver, err := newVPNDriver(host)
			if err != nil {
				return err
			}
			// The peers go first while the reconciler still runs; down then
			// stops the runtime whatever the tool answers and cleans the
			// rest. The opt-in stays until everything is gone, so a failed
			// disable can be repeated.
			var removeErr error
			if selection.Tool != identity.HomeVPNToolWireGuard {
				if _, err := identity.RemoveAllHomeVPNPeers(root, selection.Tool); err != nil {
					return err
				}
				_, removeErr = reconcileVPNPeers(ctx, root, selection.Tool, driver)
			}
			if err := removeVPNService(ctx); err != nil {
				return err
			}
			result := map[string]any{"vpn": "off", "removedTool": selection.Tool}
			var leftovers vpnLeftoversError
			if err := driver.down(ctx); errors.As(err, &leftovers) {
				result["leftovers"] = leftovers.Error()
			} else if err != nil {
				return fmt.Errorf("vpn disable: the tool was not fully stopped (%w); run disable again", errors.Join(removeErr, err))
			}
			if selection.Tool == identity.HomeVPNToolWireGuard {
				// Off removes every peer: a later enable starts with none.
				if err := clearHomeOverlayPeers(root); err != nil {
					return fmt.Errorf("vpn disable: clear the WireGuard peer registry: %w", err)
				}
			}
			if _, err := setVPNPublicListeners(ctx, ""); err != nil {
				return fmt.Errorf("vpn disable: close the public ports in the host firewall: %w", err)
			}
			if err := identity.ClearHomeVPNSelection(root); err != nil {
				return err
			}
			return writeDeviceJSON(cmd.OutOrStdout(), result)
		},
	}
}

func newVPNStatusCommand() *cobra.Command {
	return &cobra.Command{
		Use:           "status",
		Short:         "Show the selected tool, the peer ledger and the tool's live device nodes",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, root := commandContext(cmd), getWorkDir()
			host, err := loadVPNHost(root)
			if errors.Is(err, identity.ErrHomeVPNOff) {
				return writeDeviceJSON(cmd.OutOrStdout(), map[string]any{"vpn": "off"})
			}
			if err != nil {
				return err
			}
			status := map[string]any{"tool": host.selection.Tool, "enabledAt": host.selection.EnabledAt, "peerService": host.peerServiceOrigins()}
			if host.selection.Tool == identity.HomeVPNToolWireGuard {
				config, renderErr := identity.RenderHomeOverlayConfig(root, time.Now())
				if renderErr != nil {
					return renderErr
				}
				status["peers"] = strings.Count(string(config), "[Peer]")
				status["interfaceUp"] = homeOverlayLinkExists(ctx)
				return writeDeviceJSON(cmd.OutOrStdout(), status)
			}
			ledger, err := identity.ListHomeVPNPeers(root)
			if err != nil {
				return err
			}
			status["ledger"] = ledger
			driver, err := newVPNDriver(host)
			if err != nil {
				return err
			}
			if os.Geteuid() == 0 {
				live, liveErr := driver.live(ctx)
				if liveErr != nil {
					status["live"] = "unavailable: " + liveErr.Error()
				} else {
					status["live"] = live
				}
			}
			return writeDeviceJSON(cmd.OutOrStdout(), status)
		},
	}
}

func newVPNReconcileCommand() *cobra.Command {
	return &cobra.Command{
		Use:           "reconcile",
		Short:         "Apply the device registry to the tool once: drop the peers of revoked, expired and removed devices (requires root)",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, root := commandContext(cmd), getWorkDir()
			host, err := loadVPNHost(root)
			if err != nil {
				return err
			}
			if err := requireVPNHost(host.selection.Tool != identity.HomeVPNToolWireGuard); err != nil {
				return err
			}
			driver, err := newVPNDriver(host)
			if err != nil {
				return err
			}
			removals, err := newVPNPeerService(host, driver).reconcile(ctx)
			if err != nil {
				return err
			}
			return writeDeviceJSON(cmd.OutOrStdout(), map[string]any{"tool": host.selection.Tool, "removedNodes": removals.Nodes, "removedKeys": removals.Keys})
		},
	}
}

func newVPNServeCommand() *cobra.Command {
	return &cobra.Command{
		Use:           "serve",
		Short:         "Run the peer service and the reconciler (stackkit-vpn.service runs this as root)",
		Hidden:        true,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			root := getWorkDir()
			host, err := loadVPNHost(root)
			if errors.Is(err, identity.ErrHomeVPNOff) {
				return nil
			}
			if err != nil {
				return err
			}
			driver, err := newVPNDriver(host)
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(commandContext(cmd), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return serveVPNPeerService(ctx, host, driver)
		},
	}
}

// clearHomeOverlayPeers removes the WireGuard peer registry under the same
// custody lock every bind and revocation takes.
func clearHomeOverlayPeers(root string) error {
	release, err := localevidence.AcquireCustodyLock(root, identity.HomeOverlayPeerRegistryFile, 101)
	if err != nil {
		return err
	}
	defer release()
	err = os.Remove(filepath.Join(root, filepath.FromSlash(identity.HomeOverlayPeerRegistryFile)))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func installVPNService(ctx context.Context, root string) error {
	binary, err := os.Executable()
	if err != nil {
		return fmt.Errorf("vpn: locate the stackkit executable: %w", err)
	}
	if resolved, resolveErr := filepath.EvalSymlinks(binary); resolveErr == nil {
		binary = resolved
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	if strings.ContainsAny(absolute+binary, " \t\n\"'%$") {
		return errors.New("vpn: the workspace or stackkit path holds characters a systemd unit cannot carry; move it to a plain path")
	}
	unit := "[Unit]\nDescription=StackKits VPN add-on peer service and reconciler\nAfter=network-online.target docker.service\nWants=network-online.target\n\n" +
		"[Service]\nType=simple\nWorkingDirectory=" + absolute + "\nExecStart=" + binary + " --chdir " + absolute + " vpn serve\n" +
		"Restart=always\nRestartSec=5\n\n[Install]\nWantedBy=multi-user.target\n"
	file := filepath.Join(vpnSystemdDir, vpnServiceUnit)
	if current, readErr := os.ReadFile(file); readErr != nil || string(current) != unit {
		if err := os.WriteFile(file, []byte(unit), 0o644); err != nil {
			return fmt.Errorf("vpn: write %s: %w", file, err)
		}
		if _, err := homeOverlayRun(ctx, "systemctl", "daemon-reload"); err != nil {
			return err
		}
	}
	if _, err := homeOverlayRun(ctx, "systemctl", "enable", vpnServiceUnit); err != nil {
		return err
	}
	if _, err := homeOverlayRun(ctx, "systemctl", "restart", vpnServiceUnit); err != nil {
		return fmt.Errorf("vpn: start %s (see journalctl -u %s): %w", vpnServiceUnit, vpnServiceUnit, err)
	}
	return nil
}

func removeVPNService(ctx context.Context) error {
	file := filepath.Join(vpnSystemdDir, vpnServiceUnit)
	if _, err := os.Stat(file); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if _, err := homeOverlayRun(ctx, "systemctl", "disable", "--now", vpnServiceUnit); err != nil {
		return err
	}
	if err := os.Remove(file); err != nil {
		return err
	}
	_, err := homeOverlayRun(ctx, "systemctl", "daemon-reload")
	return err
}
