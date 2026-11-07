package commands

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/kombifyio/stackkits/internal/lancert"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/mereach"
	"github.com/spf13/cobra"
	"golang.org/x/crypto/acme"
)

// stackkit reach lan-certificate obtains the publicly trusted certificate of
// the homelab's LAN names (homelab identity target §5, §14 decision 3): the
// node generates its key and ACME account key, kombify Me publishes only the
// DNS-01 value for a name under the homelab's own base and the node site
// address as the DNS-only record of lan.<domain>, and the LAN enrollment
// listener serves lan.<domain> with it, so the installed Hub PWA reaches the
// Authority from home without an installed root.
func newReachLANCertificateCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "lan-certificate",
		Short: "The publicly trusted certificate of this homelab's LAN names (lan.<domain>)",
		Long: "kombify Me publishes lan.<domain> as one DNS-only record holding this node's private site address; a router that " +
			"refuses private answers (DNS rebind protection) leaves the Hub on the away flow. A local-only homelab (lab.home) " +
			"reaches the Authority through the OS trust profile instead ('stackkit verify client-trust --export').",
	}
	var issuer, eabKID, eabHMACFile, contactEmail string
	issue := &cobra.Command{
		Use:   "issue",
		Short: "Obtain or renew the LAN certificate through kombify Me's DNS-01 responder (requires root)",
		Long: "First publishes this node's site address (RFC 1918 or IPv6 ULA, never a public address) for lan.<domain> at " +
			"kombify Me and orders nothing unless Me reads it back DNS-only with that address. Issuers: zerossl and gts need " +
			"external account binding (--eab-kid and --eab-hmac-file) for a new ACME account; letsencrypt-staging serves " +
			"tests only. The key never leaves this node.",
		Args: cobra.NoArgs, SilenceUsage: true, SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if os.Geteuid() != 0 {
				return errors.New("reach: the LAN certificate is Owner custody the core reads; rerun with sudo")
			}
			root := getWorkDir()
			domain, err := reachDomain(root)
			if err != nil {
				return err
			}
			base, ok := mereach.BaseOf(domain)
			if !ok {
				return fmt.Errorf("reach: %s is not a kombify.me name; an own domain answers DNS-01 in its own DNS", domain)
			}
			registration, err := mereach.LoadRegistration(root)
			if err != nil {
				return err
			}
			if registration.Base != base {
				return fmt.Errorf("reach: the Me registration is for %s.%s, this homelab is %s", registration.Base, mereach.Zone, domain)
			}
			directory, ok := lancert.Issuers[issuer]
			if !ok {
				return fmt.Errorf("reach: unknown issuer %q (zerossl, gts, letsencrypt-staging)", issuer)
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
			uid, gid, err := localevidence.StackKitServerOwner(root)
			if err != nil {
				return err
			}
			state, err := lancert.Issuer{
				Workspace: root, Domain: domain, Directory: directory, EAB: eab, Contact: contactEmail,
				Publisher: mereach.Client{Registration: registration}, KeyUID: uid, KeyGID: gid,
			}.Issue(commandContext(cmd))
			if eab != nil {
				clear(eab.Key)
			}
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "LAN certificate for %s issued by %s, valid until %s (sha256 %s).\n",
				strings.Join(state.Names, ", "), state.Issuer, state.NotAfter, state.CertificateSHA256)
			if address, err := lancert.SiteAddress(root); err == nil {
				fmt.Fprintf(cmd.OutOrStdout(), "kombify Me publishes %s as %s (DNS-only); a router that drops private answers leaves the Hub on the away flow.\n",
					lancert.LANName(domain), address)
			}
			return nil
		},
	}
	issue.Flags().StringVar(&issuer, "issuer", "zerossl", "ACME issuer: zerossl, gts or letsencrypt-staging (tests only)")
	issue.Flags().StringVar(&contactEmail, "email", "", "ACME account contact address (Google Trust Services and ZeroSSL refuse an account without one)")
	issue.Flags().StringVar(&eabKID, "eab-kid", "", "external account binding key id (zerossl, gts)")
	issue.Flags().StringVar(&eabHMACFile, "eab-hmac-file", "", "file holding the external account binding HMAC key (base64url)")
	status := &cobra.Command{
		Use: "status", Short: "Show the installed LAN certificate (names, issuer, expiry; never the key)", Args: cobra.NoArgs, SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			state, _, err := lancert.Load(getWorkDir())
			if errors.Is(err, os.ErrNotExist) {
				fmt.Fprintln(cmd.OutOrStdout(), "No LAN certificate is installed.")
				return nil
			}
			if err != nil {
				return err
			}
			return writeCommandResultStatus(cmd, cmd.CommandPath(), "success", state)
		},
	}
	remove := &cobra.Command{
		Use: "remove", Short: "Delete the LAN certificate, its key and the ACME account key, and have kombify Me remove the LAN address",
		Args: cobra.NoArgs, SilenceUsage: true, SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			root := getWorkDir()
			if err := lancert.Remove(root); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "LAN certificate removed; the Hub falls back to the away flow.")
			// The node stops serving its LAN name, so Me's record of it goes
			// too. Tunnel teardown and base removal need no call: Me removes
			// the record itself. A local-only homelab or an own domain has no
			// Me record.
			domain, err := reachDomain(root)
			if err != nil {
				return nil
			}
			base, ok := mereach.BaseOf(domain)
			if !ok {
				return nil
			}
			registration, err := mereach.LoadRegistration(root)
			if errors.Is(err, mereach.ErrNoRegistration) {
				fmt.Fprintf(cmd.OutOrStdout(), "No Me registration in custody: a record of %s at kombify Me goes with the base, or after 'reach registration set' and another remove.\n", lancert.LANName(domain))
				return nil
			}
			if err != nil {
				return err
			}
			if registration.Base != base {
				return fmt.Errorf("reach: the Me registration is for %s.%s, this homelab is %s", registration.Base, mereach.Zone, domain)
			}
			client := mereach.Client{Registration: registration}
			var apiErr *mereach.APIError
			switch err := client.RemoveLANAddress(commandContext(cmd)); {
			case errors.As(err, &apiErr) && apiErr.Status == 404 && apiErr.Code == "base_not_found":
				// Me removed the record with the base.
				fmt.Fprintf(cmd.OutOrStdout(), "kombify Me holds no base %s.%s any more; its LAN name went with it.\n", base, mereach.Zone)
			case err != nil:
				return fmt.Errorf("reach: the certificate is removed, but kombify Me did not remove %s; rerun remove: %w", client.LANName(), err)
			default:
				fmt.Fprintf(cmd.OutOrStdout(), "kombify Me removed %s (read back absent).\n", client.LANName())
			}
			return nil
		},
	}
	command.AddCommand(issue, status, remove)
	return command
}
