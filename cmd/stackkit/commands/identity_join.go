package commands

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/identity"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/spf13/cobra"
)

// identity join: one Authority across servers (homelab identity target §4,
// P2). Techstack runs request on the joining server, approve on the
// Authority and install on the joining server again, carrying only the two
// documents between them.
func init() {
	join := &cobra.Command{
		Use:   "join",
		Short: "Join this server to the homelab's Authority, or approve and manage joined servers at the Authority",
		Long: "Several servers of one homelab share one Authority, the server that runs Pocket ID. A further server " +
			"prepares a join request, the Owner approves it with a passkey step-up at the Authority, which signs a trust " +
			"bundle and an intermediate CA for the server's hostnames, and the joining server installs the bundle after " +
			"the Owner confirmed its root fingerprint there. Techstack only carries the request and the bundle.",
		Annotations: map[string]string{noDeployObservabilityAnnotation: "true"},
	}
	client := &cobra.Command{
		Use:   "client",
		Short: "Register a joined server's sign-in client at the Authority and bind it on the joined server",
		Long: "A joined server's TinyAuth signs in against the Authority's Pocket ID. The Authority registers its OIDC client " +
			"under an Owner step-up and seals the secret to a key the carrier generated; the secret then reaches the joined " +
			"server only as a Wallet item by host delivery (stackkit wallet-item import), and client bind points TinyAuth at it.",
		Annotations: map[string]string{noDeployObservabilityAnnotation: "true"},
	}
	client.AddCommand(newIdentityJoinClientRegisterCommand(), newIdentityJoinClientBindCommand())
	zone := &cobra.Command{
		Use:   "zone",
		Short: "Report this server's place in the homelab DNS zone, or install the zone Techstack assembled",
		Long: "Techstack assembles the homelab zone from every server's zone site and carries it to each server. A server " +
			"installs only what its join trust vouches for: the Authority the names the Owner approved for each joined " +
			"server, a joined server the Authority at its bundle's LAN address and other servers as the Authority's signed " +
			"verification state lists them. DNS answers are never identity: TLS verifies each name under the root.",
		Annotations: map[string]string{noDeployObservabilityAnnotation: "true"},
	}
	zone.AddCommand(newIdentityJoinZoneSiteCommand(), newIdentityJoinZoneInstallCommand())
	join.AddCommand(newIdentityJoinRequestCommand(), newIdentityJoinApproveCommand(), newIdentityJoinInstallCommand(),
		newIdentityJoinStatusCommand(), newIdentityJoinRemoveCommand(), newIdentityJoinLeaveCommand(), client, zone)
	identityCmd.AddCommand(join)
}

func newIdentityJoinRequestCommand() *cobra.Command {
	var hostnames []string
	var out, authorityRoot string
	command := &cobra.Command{
		Use:   "request",
		Short: "On the joining server: pin the Authority's root and create the join request (public keys only)",
		Long: "Enter the Authority's root fingerprint as the Authority shows it (stackkit device bootstrap there, or the Hub): " +
			"only a trust bundle under that root ever installs here. Generate this server's intermediate CA key (kept in host " +
			"custody) and its verification-state key, and write the join request: this server's owner, site and node " +
			"references, the hostnames it serves, the pinned root and the two public keys. The request holds no secret. " +
			"The request code it prints is the one the Owner sees again in the Hub before approving.",
		SilenceUsage: true, SilenceErrors: true, Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			request, err := identity.PrepareHomeJoin(getWorkDir(), hostnames, authorityRoot, time.Now())
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Join request %s for %s. Approve it at the Authority only when the Hub shows this same code.\n",
				identity.HomeJoinRequestCode(request), strings.Join(request.Server.Hostnames, ", "))
			return writeIdentityJoinDocument(cmd, out, request)
		},
	}
	command.Flags().StringSliceVar(&hostnames, "hostname", nil, "A hostname this server serves inside the homelab domain (repeatable); its intermediate CA may name only these DNS subtrees")
	command.Flags().StringVar(&authorityRoot, "authority-root", "", "The Authority's root fingerprint (SHA-256, hex) the Owner read at the Authority")
	command.Flags().StringVar(&out, "out", "", "Write the request to this file instead of standard output")
	_ = command.MarkFlagRequired("hostname")
	_ = command.MarkFlagRequired("authority-root")
	return command
}

func newIdentityJoinApproveCommand() *cobra.Command {
	var requestFile, out string
	command := &cobra.Command{
		Use:   "approve",
		Short: "At the Authority: approve a join request with the Owner's passkey and write the trust bundle",
		Long: "Raise an Owner step-up of class join for exactly this request; the Hub shows the server, every hostname and " +
			"the request code the joining server printed, and the Owner approves it there with a passkey on an enrolled Owner " +
			"device within five minutes. Then the root signs the server's intermediate CA (its hostnames only, server " +
			"authentication only, every IP range and the identity names excluded, path length 0, 90 days) and the Owner key " +
			"signs the trust bundle the joining server installs.",
		SilenceUsage: true, SilenceErrors: true, Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			raw, err := readIdentityJoinFile(requestFile)
			if err != nil {
				return err
			}
			request, err := identity.ParseHomeJoinRequest(raw)
			if err != nil {
				return err
			}
			root := getWorkDir()
			bundle, err := identity.ApproveHomeJoin(commandContext(cmd), root, request, func(ctx context.Context, action identity.HomeStepUpAction, summary string) error {
				return awaitIdentityOwnerStepUp(ctx, cmd, root, action, summary)
			}, time.Now())
			if err != nil {
				return err
			}
			return writeIdentityJoinDocument(cmd, out, bundle)
		},
	}
	command.Flags().StringVar(&requestFile, "request", "", "The join request file the joining server wrote")
	command.Flags().StringVar(&out, "out", "", "Write the trust bundle to this file instead of standard output")
	_ = command.MarkFlagRequired("request")
	return command
}

func newIdentityJoinInstallCommand() *cobra.Command {
	var bundleFile string
	command := &cobra.Command{
		Use:   "install",
		Short: "On the joining server: install the Authority's trust bundle under the root pinned at request",
		Long: "Install the bundle only when it answers this server's pending request, carries the root the Owner pinned " +
			"with join request, is signed by the Owner of that root and its intermediate CA has exactly the requested scope. " +
			"Then read the Authority's verification state once.",
		SilenceUsage: true, SilenceErrors: true, Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			raw, err := readIdentityJoinFile(bundleFile)
			if err != nil {
				return err
			}
			bundle, err := identity.ParseHomeTrustBundle(raw)
			if err != nil {
				return err
			}
			root, now := getWorkDir(), time.Now()
			if err := identity.InstallHomeJoin(root, bundle, now); err != nil {
				return err
			}
			shown, _ := identity.HomeTrustBundleRootFingerprint(bundle)
			state := "current"
			read, cancel := context.WithTimeout(commandContext(cmd), 20*time.Second)
			defer cancel()
			if err := identity.RefreshHomeJoinedState(read, root, now); err != nil {
				state = "not read yet: " + err.Error()
			}
			return writeDeviceJSON(cmd.OutOrStdout(), map[string]any{
				"joined": true, "authoritySiteRef": bundle.AuthoritySiteRef, "rootFingerprint": shown,
				"hostnames": bundle.Server.Hostnames, "expiresAt": bundle.ExpiresAt, "verificationState": state,
			})
		},
	}
	command.Flags().StringVar(&bundleFile, "bundle", "", "The trust bundle file the Authority wrote")
	_ = command.MarkFlagRequired("bundle")
	return command
}

func newIdentityJoinStatusCommand() *cobra.Command {
	return &cobra.Command{
		Use:          "status",
		Short:        "Show the joined Authority (on a joined server) or the joined servers (at the Authority)",
		SilenceUsage: true, SilenceErrors: true, Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			root := getWorkDir()
			if status, err := identity.LoadHomeJoinedStatus(root, time.Now()); err == nil {
				return writeDeviceJSON(cmd.OutOrStdout(), map[string]any{"joined": status})
			}
			servers, err := identity.ListHomeJoinedServers(root)
			if err != nil {
				return err
			}
			return writeDeviceJSON(cmd.OutOrStdout(), map[string]any{"joinedServers": servers})
		},
	}
}

func newIdentityJoinRemoveCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <server-ref>",
		Short: "At the Authority: end a joined server's trust",
		Long: "Refuse the server's verification-state reads at once: it opens no new browser session, and every " +
			"credential it still accepts expires within the one hour credential lifetime.",
		SilenceUsage: true, SilenceErrors: true, Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := identity.RemoveHomeJoinedServer(getWorkDir(), strings.TrimSpace(args[0]), time.Now()); err != nil {
				return err
			}
			return writeDeviceJSON(cmd.OutOrStdout(), map[string]any{"removed": strings.TrimSpace(args[0])})
		},
	}
}

func newIdentityJoinLeaveCommand() *cobra.Command {
	return &cobra.Command{
		Use:          "leave",
		Short:        "On a joined server: remove the Authority's trust; its credentials stop verifying here at once",
		SilenceUsage: true, SilenceErrors: true, Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := identity.LeaveHomeJoin(getWorkDir()); err != nil {
				return err
			}
			return writeDeviceJSON(cmd.OutOrStdout(), map[string]any{"joined": false})
		},
	}
}

func newIdentityJoinClientRegisterCommand() *cobra.Command {
	var requestFile, out string
	command := &cobra.Command{
		Use:   "register",
		Short: "At the Authority: register a joined server's TinyAuth client with the Owner's passkey; the secret leaves sealed only",
		Long: "Raise an Owner step-up of class client-registration for exactly this server, TinyAuth host and carrier key. " +
			"After the Owner approved it in the Hub, the Authority's Pocket ID registers (or converges) the server's " +
			"confidential PKCE client, restricted to the household groups, sets a fresh secret and writes it sealed with " +
			"HPKE to the carrier key. The document holds no readable secret.",
		SilenceUsage: true, SilenceErrors: true, Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			raw, err := readIdentityJoinFile(requestFile)
			if err != nil {
				return err
			}
			root := getWorkDir()
			client, err := identity.RegisterHomeJoinedClient(commandContext(cmd), root, raw, func(ctx context.Context, action identity.HomeStepUpAction, summary string) error {
				return awaitIdentityOwnerStepUp(ctx, cmd, root, action, summary)
			}, time.Now())
			if err != nil {
				return err
			}
			return writeIdentityJoinDocument(cmd, out, client)
		},
	}
	command.Flags().StringVar(&requestFile, "request", "", "The client request ("+identity.HomeJoinClientRequestSchema+") the carrier wrote")
	command.Flags().StringVar(&out, "out", "", "Write the sealed client to this file instead of standard output")
	_ = command.MarkFlagRequired("request")
	return command
}

func newIdentityJoinClientBindCommand() *cobra.Command {
	var bindingFile string
	command := &cobra.Command{
		Use:   "bind",
		Short: "On a joined server: bind TinyAuth to the Authority's client with the secret of an imported Wallet item",
		Long: "The binding names the client, the Authority's Pocket ID issuer and the Wallet item this server imported " +
			"(stackkit wallet-item import). TinyAuth's private environment is written from the item in encrypted custody; " +
			"the record keeps only an Owner-signed reference.",
		SilenceUsage: true, SilenceErrors: true, Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			raw, err := readIdentityJoinFile(bindingFile)
			if err != nil {
				return err
			}
			binding, err := identity.ParseHomeJoinClientBinding(raw)
			if err != nil {
				return err
			}
			bound, err := identity.BindHomeJoinedClient(getWorkDir(), binding)
			if err != nil {
				return err
			}
			return writeDeviceJSON(cmd.OutOrStdout(), map[string]any{
				"bound": true, "clientId": bound.ClientID, "issuer": bound.Issuer, "itemRef": bound.ItemRef, "boundAt": bound.BoundAt,
			})
		},
	}
	command.Flags().StringVar(&bindingFile, "binding", "", "The client binding ("+identity.HomeJoinClientBindingSchema+")")
	_ = command.MarkFlagRequired("binding")
	return command
}

func newIdentityJoinZoneSiteCommand() *cobra.Command {
	return &cobra.Command{
		Use:          "site",
		Short:        "Show this server's role, domain and LAN address for the homelab zone",
		SilenceUsage: true, SilenceErrors: true, Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			site, err := identity.LoadHomeDNSZoneSite(getWorkDir())
			if err != nil {
				return err
			}
			return writeDeviceJSON(cmd.OutOrStdout(), site)
		},
	}
}

func newIdentityJoinZoneInstallCommand() *cobra.Command {
	var zoneFile string
	command := &cobra.Command{
		Use:   "install",
		Short: "Install the homelab zone into this server's LAN resolver, as far as its join trust vouches for it",
		Long: "Refuses a zone that names another homelab domain or Authority address, names for a server the Owner did not " +
			"approve, or an identity name. The LAN resolver restarts when it already runs.",
		SilenceUsage: true, SilenceErrors: true, Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			raw, err := readIdentityJoinFile(zoneFile)
			if err != nil {
				return err
			}
			root := getWorkDir()
			var installed identity.HomeDNSZoneInstalled
			if err := withLifecycleMutation(root, "identity join zone install", func() error {
				var installErr error
				installed, installErr = identity.InstallHomeDNSZone(commandContext(cmd), root, raw)
				return installErr
			}); err != nil {
				return err
			}
			restarted := false
			if installed.Changed {
				restarted = restartBasementLANDNS(commandContext(cmd), root)
			}
			return writeDeviceJSON(cmd.OutOrStdout(), map[string]any{
				"role": installed.Role, "records": installed.Records, "skipped": installed.Skipped, "changed": installed.Changed, "resolverRestarted": restarted,
			})
		},
	}
	command.Flags().StringVar(&zoneFile, "zone", "", "The homelab zone ("+identity.HomeDNSZoneSchema+") Techstack assembled")
	_ = command.MarkFlagRequired("zone")
	return command
}

// restartBasementLANDNS restarts a running Basement core LAN resolver so it
// reads the new zone; before the first apply there is nothing to restart.
func restartBasementLANDNS(ctx context.Context, root string) bool {
	compose := filepath.Join(root, ".stackkit", "runtime", "basement-core", "compose.yaml")
	if _, err := os.Stat(compose); err != nil {
		return false
	}
	environment, err := localevidence.ComposeInterpolationEnvironment(root)
	if err != nil {
		return false
	}
	restart := exec.CommandContext(ctx, "docker", "compose", "--project-name", "stackkit-basement-core", "-f", compose, "restart", "lan-dns") //nolint:gosec // fixed arguments
	restart.Dir, restart.Env = filepath.Dir(compose), append(os.Environ(), environment...)
	return restart.Run() == nil
}

// awaitIdentityOwnerStepUp raises an Owner step-up request for action and
// waits until the Owner approved it in the Hub; the console verifies the
// step-up itself.
func awaitIdentityOwnerStepUp(ctx context.Context, cmd *cobra.Command, root string, action identity.HomeStepUpAction, summary string) error {
	request, err := identity.RequestHomeOwnerStepUp(root, action, summary, time.Now())
	if err != nil {
		return err
	}
	hub := "the Hub"
	if address, addressErr := localevidence.LocalIdentityRuntimeAddress(root); addressErr == nil {
		hub = "https://" + address.ServiceHost("base") + identity.HomeHubPathPrefix
	}
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Approve \"%s\" with your passkey on an enrolled Owner device: open %s and choose Approve (request %s, valid for 5 minutes).\n", summary, hub, request.ID)
	if _, err := identity.AwaitHomeOwnerStepUp(ctx, root, request, time.Second); err != nil {
		return fmt.Errorf("the Owner step-up for %q was not accepted: %w", summary, err)
	}
	return nil
}

func readIdentityJoinFile(path string) ([]byte, error) {
	file, err := os.Open(path) //nolint:gosec // an operator-named public document
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	raw, err := io.ReadAll(io.LimitReader(file, 64<<10+1))
	if err != nil || len(raw) > 64<<10 {
		return nil, errors.New("the join document is unreadable or larger than 64 KiB")
	}
	return raw, nil
}

func writeIdentityJoinDocument(cmd *cobra.Command, out string, document any) error {
	if out == "" {
		return writeDeviceJSON(cmd.OutOrStdout(), document)
	}
	file, err := os.OpenFile(out, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644) //nolint:gosec // an operator-named public document
	if err != nil {
		return err
	}
	if err := writeDeviceJSON(file, document); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}
