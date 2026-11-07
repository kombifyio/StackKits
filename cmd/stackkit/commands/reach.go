package commands

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/backupcustody"
	"github.com/kombifyio/stackkits/internal/hostcustody"
	"github.com/kombifyio/stackkits/internal/lancert"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/mereach"
	"github.com/kombifyio/stackkits/internal/remotereach"
	"github.com/spf13/cobra"
)

// stackkit reach is the Owner's console of the remote reach paths of a home
// server (homelab identity target §8): kombify.me tunnel mode with the
// homelab's own Me registration, a Cloudflare Tunnel in the Owner's own
// account (a token delivered from the kombify Wallet, or one the Owner
// supplies in Standard Mode), and the LAN-name certificate. A local-only
// homelab has no reach path.
func newReachCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "reach",
		Short: "Remote reach of this home server: kombify.me tunnel mode, the Owner's own Cloudflare Tunnel and the LAN-name certificate",
		Long: "Runs the Cloudflare Tunnel connector of this home server in its own network, which reaches only the remote " +
			"listener (stackkit-remote-listener.internal:8000): every request through it passes the Access Gate's remote " +
			"checks and is never home. TLS ends at the Cloudflare edge, which can read the traffic; device proof and " +
			"session are verified here. A local-only homelab (lab.home) has no reach path.",
		Annotations: map[string]string{noDeployObservabilityAnnotation: "true"},
	}
	command.AddCommand(newReachRegistrationCommand(), newReachEnableCommand(), newReachDisableCommand(), newReachStatusCommand(), newReachLANCertificateCommand())
	return command
}

func newReachRegistrationCommand() *cobra.Command {
	command := &cobra.Command{Use: "registration", Short: "The homelab's own kombify Me registration (no kombify account needed)"}
	var base, keyFile, api string
	set := &cobra.Command{
		Use:   "set",
		Short: "Store the Me registration key of this homelab's base in Owner custody",
		Long: "Reads the kbi_ registration key Me answered once from a file (never an argument) and keeps it 0600 in Owner " +
			"custody. The base is the label of this homelab's name <base>.kombify.me.",
		Args: cobra.NoArgs, SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			raw, err := os.ReadFile(keyFile)
			if err != nil {
				return fmt.Errorf("reach: read the registration key file: %w", err)
			}
			key := strings.TrimSpace(string(raw))
			clear(raw)
			if err := mereach.SaveRegistration(getWorkDir(), mereach.Registration{Base: strings.ToLower(strings.TrimSpace(base)), API: api, APIKey: key}); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Me registration of %s.%s stored in Owner custody.\n", base, mereach.Zone)
			return nil
		},
	}
	set.Flags().StringVar(&base, "base", "", "the base label of <base>.kombify.me")
	set.Flags().StringVar(&keyFile, "key-file", "", "file holding the kbi_ registration key")
	set.Flags().StringVar(&api, "api", mereach.DefaultAPI, "kombify Me API root")
	_ = set.MarkFlagRequired("base")
	_ = set.MarkFlagRequired("key-file")
	remove := &cobra.Command{
		Use: "remove", Short: "Delete the Me registration key from Owner custody", Args: cobra.NoArgs, SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := mereach.RemoveRegistration(getWorkDir()); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Me registration removed from Owner custody.")
			return nil
		},
	}
	command.AddCommand(set, remove)
	return command
}

type reachEnableOptions struct {
	kombifyMe      bool
	walletItem     string
	ownerTokenFile string
}

func newReachEnableCommand() *cobra.Command {
	options := reachEnableOptions{}
	command := &cobra.Command{
		Use:   "enable",
		Short: "Start the reach connector (requires root on the home server)",
		Long: "Exactly one source of the tunnel token:\n" +
			"  --kombify-me                  tunnel mode of this homelab's kombify.me base with its Me registration; the token is\n" +
			"                                rotated and sealed to a fresh key of this node's local custody\n" +
			"  --wallet-item <tenant>/<item> a token the kombify Wallet delivered to this node (kombify-account tunnel mode, or a\n" +
			"                                tunnel Techstack provisioned in the Owner's Cloudflare account)\n" +
			"  --owner-token-file <file>     a tunnel token of the Owner's own Cloudflare account (Standard Mode); its ingress stays\n" +
			"                                in the Owner's dashboard and must lead every public hostname to " + remotereach.ListenerService,
		Args: cobra.NoArgs, SilenceUsage: true, SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runReachEnable(commandContext(cmd), cmd, getWorkDir(), options)
		},
	}
	command.Flags().BoolVar(&options.kombifyMe, "kombify-me", false, "kombify.me tunnel mode with this homelab's Me registration")
	command.Flags().StringVar(&options.walletItem, "wallet-item", "", "Wallet item in this node's custody holding the tunnel token, as <tenant-ref>/<item-ref>")
	command.Flags().StringVar(&options.ownerTokenFile, "owner-token-file", "", "file holding a tunnel token of the Owner's own Cloudflare account")
	return command
}

func reachHost(root string) remotereach.Host {
	return remotereach.Host{Workspace: root, Image: architecturev2renderer.RemoteReachConnectorImage(), Run: homeOverlayRun}
}

// reachDomain is the homelab's public name, or an error for a local-only one.
func reachDomain(root string) (string, error) {
	address, err := localevidence.LocalIdentityRuntimeAddress(root)
	if err != nil {
		return "", fmt.Errorf("reach: no identity runtime in this workspace (apply the StackKit first): %w", err)
	}
	if _, err := localevidence.LoadBasementRuntimeCustody(root); err != nil {
		return "", errors.New("reach: remote reach runs on a home server's Basement core; a Cloud Kit is served by its public edge")
	}
	domain := strings.ToLower(address.Domain)
	if address.SubdomainPrefix != "" || reservedHomelabName(domain) {
		return "", fmt.Errorf("reach: %s is local-only; a local-only homelab has no remote reach path (use the VPN add-on)", domain)
	}
	return domain, nil
}

// reservedHomelabName is a special-use name no public DNS, certificate or
// reach path carries (lab.home included).
func reservedHomelabName(name string) bool {
	for _, suffix := range []string{"localhost", "local", "internal", "arpa", "test", "invalid", "example", "home", "lan"} {
		if name == suffix || strings.HasSuffix(name, "."+suffix) {
			return true
		}
	}
	return false
}

func requireReachHost() error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("reach: the connector runs on a Linux home server (this is %s)", runtime.GOOS)
	}
	if os.Geteuid() != 0 {
		return errors.New("reach: the connector is a container with a custody file owned by its account; rerun with sudo")
	}
	if _, err := homeOverlayLookPath("docker"); err != nil {
		return errors.New("reach: docker is required and was not found")
	}
	return nil
}

func runReachEnable(ctx context.Context, cmd *cobra.Command, root string, options reachEnableOptions) error {
	sources := 0
	for _, set := range []bool{options.kombifyMe, options.walletItem != "", options.ownerTokenFile != ""} {
		if set {
			sources++
		}
	}
	if sources != 1 {
		return errors.New("reach: name exactly one of --kombify-me, --wallet-item or --owner-token-file")
	}
	domain, err := reachDomain(root)
	if err != nil {
		return err
	}
	if err := requireReachHost(); err != nil {
		return err
	}
	host := reachHost(root)
	if err := host.RequireConnectorNetwork(ctx); err != nil {
		return err
	}
	var token []byte
	defer func() { clear(token) }()
	state := remotereach.State{}
	var me mereach.Client
	switch {
	case options.kombifyMe:
		base, ok := mereach.BaseOf(domain)
		if !ok {
			return fmt.Errorf("reach: %s is not a kombify.me name; --kombify-me needs the homelab domain <base>.kombify.me", domain)
		}
		registration, err := mereach.LoadRegistration(root)
		if err != nil {
			return err
		}
		if registration.Base != base {
			return fmt.Errorf("reach: the Me registration is for %s.%s, this homelab is %s", registration.Base, mereach.Zone, domain)
		}
		client := mereach.Client{Registration: registration}
		me = client
		if _, err := client.EnableTunnel(ctx); err != nil {
			return fmt.Errorf("reach: enable tunnel mode at kombify Me: %w", err)
		}
		view, delivered, err := client.DeliverToken(ctx, time.Now)
		if err != nil {
			return fmt.Errorf("reach: receive the connector token: %w", err)
		}
		token, state = delivered, remotereach.State{Mode: remotereach.ModeKombifyMe, Base: base, TunnelID: view.TunnelID}
	case options.walletItem != "":
		tenant, item, ok := strings.Cut(options.walletItem, "/")
		if !ok || tenant == "" || item == "" {
			return errors.New("reach: --wallet-item is <tenant-ref>/<item-ref>")
		}
		_, value, err := hostcustody.LoadWalletItem(backupcustody.HostStore(root), tenant, item)
		if err != nil {
			return fmt.Errorf("reach: the Wallet item is not in this node's custody (deliver it with 'stackkit wallet-item import'): %w", err)
		}
		token, state = []byte(strings.TrimSpace(string(value))), remotereach.State{Mode: remotereach.ModeWalletItem, WalletTenant: tenant, WalletItem: item}
		clear(value)
	default:
		raw, err := os.ReadFile(options.ownerTokenFile)
		if err != nil {
			return fmt.Errorf("reach: read the tunnel token file: %w", err)
		}
		token, state = []byte(strings.TrimSpace(string(raw))), remotereach.State{Mode: remotereach.ModeOwnerToken}
		clear(raw)
	}
	if err := host.Install(ctx, state, token); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Reach connector %s is running (%s) on %s, reaching only %s.\n",
		remotereach.ConnectorContainer, state.Mode, remotereach.ConnectorNetwork, remotereach.ListenerService)
	if state.Mode != remotereach.ModeKombifyMe {
		fmt.Fprintf(cmd.OutOrStdout(), "The tunnel's ingress must lead every public hostname of %s to %s.\n", domain, remotereach.ListenerService)
		return nil
	}
	republishLANAddress(ctx, cmd, root, domain, me)
	return nil
}

// republishLANAddress publishes the LAN name's private address again after a
// tunnel change: Me removes that record with every tunnel teardown, and the
// LAN name serves home enrollment, not remote reach. Only a node that serves
// the LAN name publishes it; a failure leaves the Hub on the away flow until
// the identity signer's next attempt, never the command failed.
func republishLANAddress(ctx context.Context, cmd *cobra.Command, root, domain string, me mereach.Client) {
	if !lancert.Covers(root, lancert.LANName(domain), time.Now()) {
		return
	}
	if address, err := lancert.PublishAddress(ctx, root, domain, me); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "The LAN name was not published again; the identity signer retries within minutes: %v\n", err)
	} else {
		fmt.Fprintf(cmd.OutOrStdout(), "kombify Me publishes %s as %s (DNS-only) for home enrollment.\n", lancert.LANName(domain), address)
	}
}

func newReachDisableCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "disable",
		Short: "Stop the reach connector and, in kombify.me tunnel mode, remove the tunnel and its DNS records at Me",
		Args:  cobra.NoArgs, SilenceUsage: true, SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := requireReachHost(); err != nil {
				return err
			}
			ctx, root := commandContext(cmd), getWorkDir()
			host := reachHost(root)
			state, loadErr := host.Load()
			if loadErr != nil && !errors.Is(loadErr, os.ErrNotExist) {
				return loadErr
			}
			if err := host.Remove(ctx); err != nil {
				return err
			}
			if loadErr == nil && state.Mode == remotereach.ModeKombifyMe {
				registration, err := mereach.LoadRegistration(root)
				if err != nil {
					return err
				}
				me := mereach.Client{Registration: registration}
				receipt, err := me.DisableTunnel(ctx)
				if err != nil {
					return fmt.Errorf("reach: the connector is stopped, but kombify Me did not remove the tunnel; rerun disable: %w", err)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "kombify Me removed tunnel %s and its DNS records (read back absent).\n", receipt.TunnelID)
				if domain, err := reachDomain(root); err == nil {
					if base, ok := mereach.BaseOf(domain); ok && base == registration.Base {
						republishLANAddress(ctx, cmd, root, domain, me)
					}
				}
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Reach connector stopped; its token is deleted from custody.")
			return nil
		},
	}
}

func newReachStatusCommand() *cobra.Command {
	var asJSON bool
	command := &cobra.Command{
		Use: "status", Short: "Show the reach connector and, in kombify.me tunnel mode, Me's Cloudflare read-back", Args: cobra.NoArgs, SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, root := commandContext(cmd), getWorkDir()
			host := reachHost(root)
			report := map[string]any{"enabled": false}
			state, err := host.Load()
			if err == nil {
				report = map[string]any{"enabled": true, "mode": state.Mode, "base": state.Base, "tunnelId": state.TunnelID, "enabledAt": state.EnabledAt, "connector": host.Health(ctx)}
				if state.Mode == remotereach.ModeKombifyMe {
					if registration, regErr := mereach.LoadRegistration(root); regErr == nil {
						if view, viewErr := (mereach.Client{Registration: registration}).Tunnel(ctx); viewErr == nil {
							report["kombifyMe"] = view
						} else {
							report["kombifyMeError"] = viewErr.Error()
						}
					}
				}
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
			if asJSON {
				return writeCommandResultStatus(cmd, cmd.CommandPath(), "success", report)
			}
			if report["enabled"] != true {
				fmt.Fprintln(cmd.OutOrStdout(), "Reach connector: off.")
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Reach connector: %s (%s), since %s.\n", report["connector"], state.Mode, state.EnabledAt)
			if view, ok := report["kombifyMe"].(mereach.TunnelView); ok {
				fmt.Fprintf(cmd.OutOrStdout(), "kombify Me: tunnel %s %s, ingress %s, hostnames %s.\n", view.TunnelID, view.TunnelStatus, view.IngressService, strings.Join(view.Hostnames, ", "))
			}
			return nil
		},
	}
	command.Flags().BoolVar(&asJSON, "json", false, "Emit JSON")
	return command
}
