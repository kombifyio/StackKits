package commands

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/lancert"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/publiccert"
	"github.com/spf13/cobra"
	"golang.org/x/crypto/acme"
)

// stackkit reach public-certificate gives the router of a public identity
// origin its publicly trusted certificate (homelab identity target §5):
// *.<name> and <name> by ACME DNS-01 with the lan-certificate client. A
// kombify.me name answers DNS-01 through kombify Me's responder; an own domain
// answers it in the owner's Cloudflare zone with the owner's DNS token from
// Owner custody (a token file, or a Wallet item delivered to the node). A
// local-only homelab keeps its step-ca certificates.
func newReachPublicCertificateCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "public-certificate",
		Short: "The publicly trusted certificate of a public identity origin (*.<name>) the router serves",
		Long: "Without it, the router of a public identity origin serves certificates of the node's own step-ca, which only a " +
			"device with the Owner root trusts. With it, id.<name>, base.<name> and every other host are served with a publicly " +
			"trusted certificate, so a browser on the LAN needs no installed root. The identity signer renews it. A local-only " +
			"homelab (lab.home) keeps step-ca.",
	}
	var issuer, eabKID, eabHMACFile, contactEmail, provider, tokenFile, walletItem string
	issue := &cobra.Command{
		Use:   "issue",
		Short: "Obtain or renew the public-names certificate through ACME DNS-01 (requires root)",
		Long: "kombify.me names use kombify Me's responder (the Me registration is in custody). An own domain uses Cloudflare " +
			"with a token limited to the domain's zone, Zone:Read and DNS:Edit (Zone:Read finds the zone): --dns-token-file copies it into Owner custody, --dns-wallet-item " +
			"copies a Wallet item already delivered to this node (stackkit wallet-item import) the same way. A later issue without a token flag " +
			"reuses the recorded provider. Issuers: zerossl and gts need external account binding (--eab-kid and --eab-hmac-file) " +
			"for a new ACME account; letsencrypt-staging serves tests only (its chain is not trusted). The key stays on this node.",
		Example: "  sudo stackkit reach public-certificate issue --issuer gts --email owner@example.org --eab-kid KID --eab-hmac-file eab.key\n" +
			"  sudo stackkit reach public-certificate issue --dns-token-file cf-dns.token --issuer zerossl --eab-kid KID --eab-hmac-file eab.key",
		Args: cobra.NoArgs, SilenceUsage: true, SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if os.Geteuid() != 0 {
				return errors.New("reach: the public certificate is Owner custody the router reads; rerun with sudo")
			}
			root := getWorkDir()
			domain, err := publiccert.Domain(root)
			if err != nil {
				return err
			}
			directory, ok := lancert.Issuers[issuer]
			if !ok {
				return fmt.Errorf("reach: unknown issuer %q (zerossl, gts, letsencrypt-staging)", issuer)
			}
			if tokenFile != "" && walletItem != "" {
				return errors.New("reach: name at most one of --dns-token-file and --dns-wallet-item")
			}
			if provider == "" {
				provider = publiccert.DefaultProvider(domain)
			}
			var token publiccert.DNSToken
			if tokenFile != "" {
				if token.Token, err = os.ReadFile(tokenFile); err != nil {
					return fmt.Errorf("reach: read the DNS token file: %w", err)
				}
			}
			if walletItem != "" {
				tenant, item, ok := strings.Cut(walletItem, "/")
				if !ok || tenant == "" || item == "" {
					return errors.New("reach: --dns-wallet-item is <tenant-ref>/<item-ref>")
				}
				token.WalletTenant, token.WalletItem = tenant, item
			}
			switch {
			case provider == publiccert.ProviderCloudflare && tokenFile == "" && walletItem == "":
				if !publiccert.Configured(root) {
					return errors.New("reach: an own domain answers DNS-01 in Cloudflare: pass --dns-token-file or --dns-wallet-item (a token with Zone:Read and DNS:Edit on the zone)")
				}
			default:
				err = publiccert.SaveProvider(root, provider, token)
			}
			clear(token.Token)
			if err != nil {
				return err
			}
			var eab *acme.ExternalAccountBinding
			if eabKID != "" || eabHMACFile != "" {
				raw, err := os.ReadFile(eabHMACFile)
				if err != nil {
					return fmt.Errorf("reach: read the EAB HMAC key file: %w", err)
				}
				key, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(strings.TrimSpace(string(raw)), "="))
				clear(raw)
				if err != nil || eabKID == "" {
					return errors.New("reach: external account binding needs --eab-kid and a base64url HMAC key file")
				}
				eab = &acme.ExternalAccountBinding{KID: eabKID, Key: key}
			}
			state, err := publiccert.Issue(commandContext(cmd), root, directory, eab, contactEmail)
			if eab != nil {
				clear(eab.Key)
			}
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Public certificate for %s issued by %s, valid until %s (sha256 %s).\n",
				strings.Join(state.Names, ", "), state.Issuer, state.NotAfter, state.CertificateSHA256)
			if localevidence.PublicRouterCertificateServes(root, time.Now()) {
				fmt.Fprintln(cmd.OutOrStdout(), "Run stackkit apply: the router then serves it for id."+domain+" and every other host (the identity signer renews it).")
			}
			return nil
		},
	}
	issue.Flags().StringVar(&issuer, "issuer", "zerossl", "ACME issuer: zerossl, gts or letsencrypt-staging (tests only)")
	issue.Flags().StringVar(&contactEmail, "email", "", "ACME account contact address (Google Trust Services and ZeroSSL refuse an account without one)")
	issue.Flags().StringVar(&eabKID, "eab-kid", "", "external account binding key id (zerossl, gts)")
	issue.Flags().StringVar(&eabHMACFile, "eab-hmac-file", "", "file holding the external account binding HMAC key (base64url)")
	issue.Flags().StringVar(&provider, "dns-provider", "", "DNS-01 provider: kombify-me (the default for a kombify.me name) or cloudflare (the default for an own domain)")
	issue.Flags().StringVar(&tokenFile, "dns-token-file", "", "file holding the Cloudflare API token (Zone:Read and DNS:Edit on the zone); copied into Owner custody")
	issue.Flags().StringVar(&walletItem, "dns-wallet-item", "", "Wallet item in this node's custody holding the Cloudflare API token, as <tenant-ref>/<item-ref>; copied into Owner custody")
	status := &cobra.Command{
		Use: "status", Short: "Show the installed public-names certificate (names, issuer, expiry; never the key)", Args: cobra.NoArgs, SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			state, _, err := lancert.LoadPublic(getWorkDir())
			if errors.Is(err, os.ErrNotExist) {
				fmt.Fprintln(cmd.OutOrStdout(), "No public certificate is installed; the router serves its step-ca certificates.")
				return nil
			}
			if err != nil {
				return err
			}
			return writeCommandResultStatus(cmd, cmd.CommandPath(), "success", state)
		},
	}
	remove := &cobra.Command{
		Use: "remove", Short: "Delete the public certificate, its key, the ACME account and the DNS token copy (requires root)",
		Long: "The routes the certificate covered fall back to the router's step-ca certificates at once, without an apply. A Wallet item holding the DNS token " +
			"stays in the node's Wallet custody.",
		Args: cobra.NoArgs, SilenceUsage: true, SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if os.Geteuid() != 0 {
				return errors.New("reach: the public certificate is Owner custody; rerun with sudo")
			}
			err := publiccert.Remove(commandContext(cmd), getWorkDir())
			if errors.Is(err, publiccert.ErrRouterNotRefreshed) {
				// The certificate is gone; a stopped router names the step-ca
				// resolver again from its next stackkit apply.
				fmt.Fprintln(cmd.OutOrStdout(), "Public certificate removed; the router is not running, and the next stackkit apply starts it with its step-ca certificates.")
				return nil
			}
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Public certificate removed; the router serves its step-ca certificates for the routes it covered.")
			return nil
		},
	}
	command.AddCommand(issue, status, remove)
	return command
}
