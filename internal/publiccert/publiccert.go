// Package publiccert gives the router of a public identity origin its publicly
// trusted certificate (homelab identity target §5, §14 decision 2): one
// certificate for *.<name> and <name> (id.<name>, base.<name>, ...) obtained by
// ACME DNS-01 with the same client that obtains the LAN-name certificate
// (internal/lancert, profile Public). kombify.me names answer DNS-01 through
// kombify Me's responder; an own domain answers it in the owner's Cloudflare
// zone with the owner's DNS token from Owner custody (a token file copied into
// custody, or a Wallet item delivered to the node). A local-only homelab keeps
// the step-ca certificates and never issues one.
package publiccert

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/backupcustody"
	"github.com/kombifyio/stackkits/internal/hostcustody"
	"github.com/kombifyio/stackkits/internal/lancert"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/mereach"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
	"golang.org/x/crypto/acme"
)

// BaseTLSOptions is the router's TLS policy (the Komodo control listener's
// minimum version); the public certificate is added to it.
const BaseTLSOptions = "tls:\n  options:\n    stackkit-komodo:\n      minVersion: VersionTLS13\n      sniStrict: true\n"

// routerPolicyRelDir holds tls-options.yaml; the router mounts the directory
// read-only and its file provider watches it.
const (
	routerPolicyRelDir = ".stackkit/runtime/basement-core/router-tls"
	routerPolicyName   = "tls-options.yaml"
)

// DNS providers of the DNS-01 challenge.
const (
	ProviderMe         = "kombify-me"
	ProviderCloudflare = "cloudflare"
)

// DNSToken names where the Cloudflare DNS token comes from: exactly one of a
// token read from the Owner's file or a Wallet item in this node's custody
// (stackkit wallet-item import), the pattern of reach enable. Either is copied
// into the public-tls custody, where the identity signer renews with it; a
// rotated Wallet item takes effect with the next issue.
type DNSToken struct {
	Token        []byte
	WalletTenant string
	WalletItem   string
}

type providerRecord struct {
	Schema       string `json:"schema"`
	Provider     string `json:"provider"`
	WalletTenant string `json:"walletTenant,omitempty"`
	WalletItem   string `json:"walletItem,omitempty"`
}

const providerSchema = "stackkit.public-certificate-provider/v1"

// The provider record and the DNS token copy in the public-tls custody. The
// custody belongs to the identity signer's account after the first issue, so
// root writes and removes them only confined to it (lancert.WriteConfined).
const (
	providerName = "provider.json"
	tokenName    = "dns-token"
)

func providerPath(workspace string) string {
	return filepath.Join(lancert.CustodyDirOf(workspace, lancert.Public), providerName)
}

func tokenPath(workspace string) string {
	return filepath.Join(lancert.CustodyDirOf(workspace, lancert.Public), tokenName)
}

// Domain is the workspace's public identity domain, or an error for a
// local-only one.
func Domain(workspace string) (string, error) {
	address, err := localevidence.LocalIdentityRuntimeAddress(workspace)
	if err != nil {
		return "", fmt.Errorf("publiccert: no identity runtime in this workspace: %w", err)
	}
	domain := strings.ToLower(address.Domain)
	if _, source := resolvedplan.IdentityOriginOf(domain); source != "kombify-me" && source != "own-domain" || address.SubdomainPrefix != "" {
		return "", fmt.Errorf("publiccert: %s is local-only; it keeps its step-ca certificates", domain)
	}
	return domain, nil
}

// DefaultProvider is the DNS-01 provider for a domain: kombify Me for a
// kombify.me name, Cloudflare for an own domain.
func DefaultProvider(domain string) string {
	if _, ok := mereach.BaseOf(domain); ok {
		return ProviderMe
	}
	return ProviderCloudflare
}

// Configured reports whether a DNS-01 provider is already recorded.
func Configured(workspace string) bool {
	_, err := os.Stat(providerPath(workspace))
	return err == nil
}

// SaveProvider records which DNS answers the DNS-01 challenge. A Cloudflare
// token file goes to Owner custody (0600) and is never printed; a Wallet item
// is recorded by reference and must already be in this node's custody.
func SaveProvider(workspace, provider string, token DNSToken) error {
	dir := lancert.CustodyDirOf(workspace, lancert.Public)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	record := providerRecord{Schema: providerSchema, Provider: provider}
	switch provider {
	case ProviderMe:
		if len(token.Token) > 0 || token.WalletItem != "" {
			return errors.New("publiccert: kombify Me answers DNS-01 with the Me registration; it takes no DNS token")
		}
		_ = lancert.RemoveConfined(dir, tokenName)
	case ProviderCloudflare:
		switch {
		case len(token.Token) > 0 && token.WalletItem == "":
			value := bytes.TrimSpace(token.Token)
			if err := validToken(value); err != nil {
				return err
			}
			if err := lancert.WriteConfined(dir, tokenName, value, 0o600, -1, -1); err != nil {
				return err
			}
		case len(token.Token) == 0 && token.WalletTenant != "" && token.WalletItem != "":
			value, err := walletToken(workspace, token.WalletTenant, token.WalletItem)
			if err != nil {
				return err
			}
			err = lancert.WriteConfined(dir, tokenName, value, 0o600, -1, -1)
			clear(value)
			if err != nil {
				return err
			}
			record.WalletTenant, record.WalletItem = token.WalletTenant, token.WalletItem
		default:
			return errors.New("publiccert: an own domain needs exactly one Cloudflare DNS token: a token file or a Wallet item")
		}
	default:
		return fmt.Errorf("publiccert: unknown DNS provider %q (%s, %s)", provider, ProviderMe, ProviderCloudflare)
	}
	raw, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return lancert.WriteConfined(dir, providerName, raw, 0o600, -1, -1)
}

func validToken(token []byte) error {
	if len(token) < 20 || len(token) > 512 || bytes.ContainsAny(token, " \r\n\t") {
		return errors.New("publiccert: the Cloudflare DNS token is not a token")
	}
	return nil
}

func walletToken(workspace, tenant, item string) ([]byte, error) {
	_, value, err := hostcustody.LoadWalletItem(backupcustody.HostStore(workspace), tenant, item)
	if err != nil {
		return nil, fmt.Errorf("publiccert: the Wallet item holding the Cloudflare DNS token is not in this node's custody (deliver it with 'stackkit wallet-item import'): %w", err)
	}
	token := bytes.TrimSpace(value)
	if err := validToken(token); err != nil {
		clear(value)
		return nil, err
	}
	out := bytes.Clone(token)
	clear(value)
	return out, nil
}

func publisher(workspace, domain string) (lancert.Publisher, error) {
	raw, err := os.ReadFile(providerPath(workspace))
	if err != nil {
		return nil, fmt.Errorf("publiccert: no DNS provider is configured (run reach public-certificate issue first): %w", err)
	}
	var record providerRecord
	if json.Unmarshal(raw, &record) != nil || record.Schema != providerSchema {
		return nil, errors.New("publiccert: the DNS provider record is invalid")
	}
	switch record.Provider {
	case ProviderMe:
		if _, ok := mereach.BaseOf(domain); !ok {
			return nil, fmt.Errorf("publiccert: %s is not a kombify.me name", domain)
		}
		registration, err := mereach.LoadRegistration(workspace)
		if err != nil {
			return nil, err
		}
		return mereach.Client{Registration: registration}, nil
	case ProviderCloudflare:
		token, err := os.ReadFile(tokenPath(workspace))
		if err != nil {
			return nil, fmt.Errorf("publiccert: the Cloudflare DNS token is not in custody: %w", err)
		}
		cloudflare := &lancert.Cloudflare{Token: strings.TrimSpace(string(token))}
		clear(token)
		return cloudflare, nil
	}
	return nil, errors.New("publiccert: the DNS provider record names an unknown provider")
}

// Issue obtains or renews the public-names certificate and rewrites the
// router's policy file.
func Issue(ctx context.Context, workspace, directory string, eab *acme.ExternalAccountBinding, contact string) (lancert.State, error) {
	domain, err := Domain(workspace)
	if err != nil {
		return lancert.State{}, err
	}
	pub, err := publisher(workspace, domain)
	if err != nil {
		return lancert.State{}, err
	}
	uid, gid := signerOwner(workspace)
	state, err := lancert.Issuer{
		Profile: lancert.Public, Workspace: workspace, Domain: domain, Directory: directory, EAB: eab, Contact: contact,
		Publisher: pub, KeyUID: uid, KeyGID: gid,
	}.Issue(ctx)
	if err != nil {
		return lancert.State{}, err
	}
	if err := handOverCustody(workspace, uid, gid); err != nil {
		return lancert.State{}, err
	}
	return state, RefreshRouter(ctx, workspace)
}

// signerOwner is the account the identity signer runs as (the stackkit-server
// credential owner), which renews the certificate. Only root hands files
// over; any other caller already is that account.
func signerOwner(workspace string) (int, int) {
	if os.Geteuid() != 0 {
		return -1, -1
	}
	uid, gid, err := localevidence.StackKitServerOwner(workspace)
	if err != nil {
		return -1, -1
	}
	return uid, gid
}

// handOverCustody gives the public-tls custody (ACME account, provider record,
// DNS token, live certificate) to the identity signer's account, which renews
// it; the router reads it as root.
func handOverCustody(workspace string, uid, gid int) error {
	if uid < 0 || gid < 0 {
		return nil
	}
	return filepath.WalkDir(lancert.CustodyDirOf(workspace, lancert.Public), func(path string, _ os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return os.Lchown(path, uid, gid)
	})
}

// Renew renews the certificate within 30 days of its expiry with the ACME
// account in custody, on the identity signer's cadence. A node that never
// issued one, or a local-only one, renews nothing. The router policy is
// refreshed after every attempt, not only after a renewal: a certificate that
// stops serving (it expires after failed renewals) leaves the policy here,
// and the routes it covered fall back to step-ca, instead of at the next
// apply.
func Renew(ctx context.Context, workspace string) (bool, error) {
	if _, _, err := lancert.LoadPublic(workspace); errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	domain, err := Domain(workspace)
	if err != nil {
		return false, nil
	}
	renewed, err := renew(ctx, workspace, domain)
	return renewed, errors.Join(err, RefreshRouter(ctx, workspace))
}

// RenewalFailing is the installed certificate's expiry when it is within
// window of it although the node renews 30 days ahead: renewals kept failing.
// A node without one, or a local-only one, reports nothing.
func RenewalFailing(workspace string, now time.Time, window time.Duration) (time.Time, bool) {
	if _, err := Domain(workspace); err != nil {
		return time.Time{}, false
	}
	_, certificate, err := lancert.LoadPublic(workspace)
	if err != nil || certificate == nil || certificate.NotAfter.Sub(now) > window {
		return time.Time{}, false
	}
	return certificate.NotAfter.UTC(), true
}

func renew(ctx context.Context, workspace, domain string) (bool, error) {
	pub, err := publisher(workspace, domain)
	if err != nil {
		return false, err
	}
	return lancert.RenewProfile(ctx, workspace, lancert.Public, domain, pub, -1, -1)
}

// Remove deletes the certificate, its key, the ACME account and the DNS token
// copy and returns the router policy to its step-ca certificates: the routes
// the certificate covered fall back to step-ca at once. A Wallet item stays
// in the node's Wallet custody.
func Remove(ctx context.Context, workspace string) error {
	if err := lancert.RemoveProfile(workspace, lancert.Public); err != nil {
		return err
	}
	if err := lancert.RemoveConfined(lancert.CustodyDirOf(workspace, lancert.Public), providerName, tokenName); err != nil {
		return err
	}
	return RefreshRouter(ctx, workspace)
}

// TLSOptions is the router's policy file: the base policy plus the public
// certificate while it serves the identity host (the same decision that moves
// the router's ACME store), or, once it no longer serves, the step-ca
// fallback router for the hosts whose running routes still name no resolver.
// A certificate the router cannot load is never listed. The digest line makes
// a renewal (same paths, new certificate) a content change the router reloads
// on.
func TLSOptions(workspace string, fallback []string) []byte {
	if !localevidence.PublicRouterCertificateServes(workspace, time.Now()) {
		return []byte(fallbackPolicy(fallback) + BaseTLSOptions)
	}
	certPEM, err := os.ReadFile(filepath.Join(lancert.LiveDirOf(workspace, lancert.Public), "cert.pem"))
	if err != nil {
		return []byte(BaseTLSOptions)
	}
	sum := sha256.Sum256(certPEM)
	mount := localevidence.PublicRouterCertificateMount
	stanza := "  certificates:\n    - certFile: " + mount + "/cert.pem\n      keyFile: " + mount + "/key.pem\n"
	return []byte(strings.Replace(BaseTLSOptions, "tls:\n", "# public certificate sha256:"+hex.EncodeToString(sum[:])+"\ntls:\n"+stanza, 1))
}

// RefreshRouter rewrites the router's policy file once the Basement core was
// applied; before that the apply writes it. When the certificate no longer
// serves, it reads the running routes from the router's API and adds the
// step-ca fallback for the covered hosts whose routes name no resolver. When
// the API cannot be read (the router is stopped or restarting), the existing
// policy stays as it is, so an installed fallback router is never dropped
// without its replacement, and the error carries ErrRouterNotRefreshed, so
// the signer retries soon.
func RefreshRouter(ctx context.Context, workspace string) error {
	dir := filepath.Join(workspace, filepath.FromSlash(routerPolicyRelDir))
	if _, err := readRouterPolicy(dir); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	hosts, err := fallbackHosts(ctx, workspace)
	if err != nil {
		return err
	}
	uid, gid := signerOwner(workspace)
	if len(hosts) > 0 {
		if err := retireRouterStore(workspace, uid, gid); err != nil {
			return err
		}
	}
	return writeRouterPolicy(workspace, false, TLSOptions(workspace, hosts))
}

// WriteRouterPolicy writes the policy the apply renders with: the apply names
// the step-ca resolver on every route the certificate does not serve, so it
// needs no fallback router.
func WriteRouterPolicy(workspace string, create bool) error {
	return writeRouterPolicy(workspace, create, TLSOptions(workspace, nil))
}

// writeRouterPolicy replaces tls-options.yaml atomically (a temporary file in
// the same directory, fsync, rename), so the router's watcher never reads a
// partial policy. The router mounts the directory, not the file, and so sees
// each replacement. The directory and the file belong to the identity signer,
// which rewrites the policy after a renewal; the write is confined to the
// directory, whose entry stays in the root-owned runtime directory. With
// create the directory and file are created when missing (Apply); otherwise a
// missing policy is left to the next Apply.
func writeRouterPolicy(workspace string, create bool, content []byte) error {
	dir := filepath.Join(workspace, filepath.FromSlash(routerPolicyRelDir))
	current, err := readRouterPolicy(dir)
	if errors.Is(err, os.ErrNotExist) && !create {
		return nil
	}
	if err == nil && bytes.Equal(current, content) {
		return nil
	}
	uid, gid := signerOwner(workspace)
	if create {
		if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		if uid >= 0 {
			if err := os.Lchown(dir, uid, gid); err != nil {
				return err
			}
		}
	}
	return lancert.WriteConfined(dir, routerPolicyName, content, 0o600, uid, gid)
}

func readRouterPolicy(dir string) ([]byte, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	file, err := root.Open(routerPolicyName)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return io.ReadAll(io.LimitReader(file, 64<<10))
}
