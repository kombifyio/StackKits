// Package lancert holds the publicly trusted certificate of a homelab's LAN
// name under its kombify.me base (homelab identity target §5, §14 decision
// 3): one certificate for lan.<base>.kombify.me only, with which the
// Authority's LAN enrollment listener serves the LAN name the installed Hub
// PWA opens from home.
//
// The node generates the certificate key and its ACME account key and keeps
// both in Owner custody; it runs its own ACME client against a second issuer
// (ZeroSSL or Google Trust Services with external account binding, until
// kombify.me is on the Public Suffix List). kombify Me publishes only the
// DNS-01 value and, as the LAN name's one DNS-only record, the node's private
// site address. Neither key, the CSR's key nor the EAB key is printed, logged
// or written to a receipt.
package lancert

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/kombifyio/stackkits/internal/localevidence"
	"golang.org/x/crypto/acme"
)

const (
	// LANLabel is the label of the LAN name under the homelab's domain.
	LANLabel = "lan"

	custodyDirName  = "lan-tls"
	liveDirName     = "live"
	certName        = "cert.pem"
	keyName         = "key.pem"
	accountName     = "account.json"
	accountVersion  = "stackkit.lan-certificate-account/v1"
	renewBefore     = 30 * 24 * time.Hour
	minimumLifetime = time.Hour
	maxPEMBytes     = 64 << 10
)

// Issuer directories. ZeroSSL and Google Trust Services need external account
// binding. Let's Encrypt production is not offered: its 50 certificates per
// week for the registered domain kombify.me are shared by every homelab until
// the Public Suffix List entry takes effect (target §14 decision 3). Let's
// Encrypt staging serves journeys only; its chain is not trusted.
var Issuers = map[string]string{
	"zerossl":             "https://acme.zerossl.com/v2/DV90",
	"gts":                 "https://dv.acme-v02.api.pki.goog/directory",
	"letsencrypt-staging": "https://acme-staging-v02.api.letsencrypt.org/directory",
}

// untrustedDirectories issue chains no browser trusts.
var untrustedDirectories = map[string]bool{Issuers["letsencrypt-staging"]: true}

// accountRelPath is the ACME account record; its custody lock serializes
// every issuance and renewal (the root CLI and the identity signer).
const accountRelPath = ".stackkit/custody/" + custodyDirName + "/" + accountName

// LANName is the LAN name of a homelab domain.
func LANName(domain string) string { return LANLabel + "." + domain }

// CustodyDir is the Owner custody directory of the LAN certificate.
func CustodyDir(workspace string) string {
	return filepath.Join(workspace, ".stackkit", "custody", custodyDirName)
}

// LiveDir holds the certificate and its key; the Basement core mounts it.
func LiveDir(workspace string) string { return filepath.Join(CustodyDir(workspace), liveDirName) }

// State is the public identity of the installed certificate.
type State struct {
	Names             []string `json:"names"`
	Issuer            string   `json:"issuer"`
	NotBefore         string   `json:"notBefore"`
	NotAfter          string   `json:"notAfter"`
	SerialHex         string   `json:"serialHex"`
	CertificateSHA256 string   `json:"certificateSha256"`
}

// Load parses the installed certificate; os.ErrNotExist when none is.
func Load(workspace string) (State, *x509.Certificate, error) {
	raw, err := readLimited(filepath.Join(LiveDir(workspace), certName))
	if err != nil {
		return State{}, nil, err
	}
	block, _ := pem.Decode(raw)
	if block == nil || block.Type != "CERTIFICATE" {
		return State{}, nil, errors.New("lancert: the installed certificate is not PEM")
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return State{}, nil, err
	}
	sum := sha256.Sum256(block.Bytes)
	return State{
		Names: slices.Clone(leaf.DNSNames), Issuer: leaf.Issuer.CommonName,
		NotBefore: leaf.NotBefore.UTC().Format(time.RFC3339), NotAfter: leaf.NotAfter.UTC().Format(time.RFC3339),
		SerialHex: leaf.SerialNumber.Text(16), CertificateSHA256: hex.EncodeToString(sum[:]),
	}, leaf, nil
}

// Covers reports whether the installed key pair matches and its certificate
// is valid for host for at least another hour.
func Covers(workspace, host string, now time.Time) bool {
	_, leaf, err := Load(workspace)
	if err != nil || leaf.VerifyHostname(strings.ToLower(host)) != nil ||
		!now.After(leaf.NotBefore) || !now.Add(minimumLifetime).Before(leaf.NotAfter) {
		return false
	}
	_, err = tls.LoadX509KeyPair(filepath.Join(LiveDir(workspace), certName), filepath.Join(LiveDir(workspace), keyName))
	return err == nil
}

// NeedsRenewal reports a missing certificate, one that does not cover the
// domain's names, and one within 30 days of expiry.
func NeedsRenewal(workspace, domain string, now time.Time) bool {
	_, leaf, err := Load(workspace)
	if err != nil || leaf.VerifyHostname(LANName(domain)) != nil {
		return true
	}
	return now.Add(renewBefore).After(leaf.NotAfter)
}

// Source serves the installed certificate to a TLS listener, reloading it when
// the file changes.
type Source struct {
	Workspace string
	mu        sync.Mutex
	modified  time.Time
	cached    *tls.Certificate
}

// Certificate answers the installed key pair, or nil when none is installed.
func (s *Source) Certificate() *tls.Certificate {
	info, err := os.Stat(filepath.Join(LiveDir(s.Workspace), certName))
	if err != nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cached != nil && info.ModTime().Equal(s.modified) {
		return s.cached
	}
	pair, err := tls.LoadX509KeyPair(filepath.Join(LiveDir(s.Workspace), certName), filepath.Join(LiveDir(s.Workspace), keyName))
	if err != nil {
		return nil
	}
	s.cached, s.modified = &pair, info.ModTime()
	return s.cached
}

// Publisher is kombify Me's side of the LAN name: it publishes and removes one
// DNS-01 value, and publishes the node's private address as the DNS-only
// record of the LAN name, answering nil only after Me's read-back shows that
// name, DNS-only, with exactly that address.
type Publisher interface {
	SetChallenge(ctx context.Context, identifier, value string) error
	RemoveChallenge(ctx context.Context, identifier, value string) error
	SetLANAddress(ctx context.Context, name string, address netip.Addr) error
}

// ErrAddressNotPublished reports that kombify Me did not publish the LAN
// name's address (or the node had none to publish). Nothing was ordered: a
// caller may retry soon without touching the ACME issuer.
var ErrAddressNotPublished = errors.New("lancert: the LAN address is not published")

// SiteAddress is the address lan.<domain> must resolve to: the node site
// address the owner-signed LAN resolver custody binds, which the LAN
// enrollment listener (9443) is published on and announces. Only an RFC 1918
// or IPv6 ULA address is answered (narrower than Me, which also admits
// 100.64.0.0/10); a public, loopback, link-local, IPv4-mapped or zoned address
// never leaves the node.
func SiteAddress(workspace string) (netip.Addr, error) {
	raw, err := localevidence.BasementLANDNSResolverAddress(workspace)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("lancert: no LAN site address is bound (apply the StackKit first): %w", err)
	}
	address, err := netip.ParseAddr(raw)
	if err != nil || !address.IsPrivate() || address.Is4In6() || address.Zone() != "" {
		return netip.Addr{}, fmt.Errorf("lancert: the LAN site address %s is not a private address (RFC 1918 or IPv6 ULA); kombify Me publishes no other", raw)
	}
	return address, nil
}

// PublishAddress publishes the node site address for lan.<domain> through
// kombify Me before any ACME order: without that record the base's proxied
// wildcard answers the LAN name with Cloudflare addresses. A router that drops
// private answers (DNS rebinding protection) still leaves the Hub on the away
// flow.
// Every failure wraps ErrAddressNotPublished.
func PublishAddress(ctx context.Context, workspace, domain string, publisher Publisher) (netip.Addr, error) {
	if publisher == nil || domain == "" {
		return netip.Addr{}, fmt.Errorf("%w: publishing it needs a domain and kombify Me", ErrAddressNotPublished)
	}
	address, err := SiteAddress(workspace)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("%w: %w", ErrAddressNotPublished, err)
	}
	if err := publisher.SetLANAddress(ctx, LANName(domain), address); err != nil {
		return netip.Addr{}, fmt.Errorf("%w: kombify Me did not publish %s as %s: %w", ErrAddressNotPublished, LANName(domain), address, err)
	}
	return address, nil
}

// publishHeld republishes the LAN name's address under the custody lock while
// a certificate is installed, so it never races Remove.
func publishHeld(ctx context.Context, workspace, domain string, publisher Publisher) error {
	unlock, err := localevidence.AcquireCustodyLock(workspace, accountRelPath, 6000)
	if err != nil {
		return fmt.Errorf("lancert: another issuance holds the LAN certificate custody: %w", err)
	}
	defer unlock()
	if _, _, err := Load(workspace); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	_, err = PublishAddress(ctx, workspace, domain, publisher)
	return err
}

// Issuer obtains the LAN certificate.
type Issuer struct {
	Workspace string
	Domain    string
	// Directory is the ACME directory URL of the issuer.
	Directory string
	// EAB is required for a new account at ZeroSSL and Google Trust Services.
	EAB *acme.ExternalAccountBinding
	// Contact is the optional ACME account contact address; Google Trust
	// Services and ZeroSSL refuse an account without one.
	Contact   string
	Publisher Publisher
	// KeyUID and KeyGID own the certificate key file: the core server's
	// account, which serves the LAN enrollment listener.
	KeyUID, KeyGID int
	// Propagated waits until the authoritative servers answer the value.
	Propagated func(ctx context.Context, name, value string) error
}

type account struct {
	APIVersion string `json:"apiVersion"`
	Directory  string `json:"directory"`
	URI        string `json:"uri"`
	KeyPEM     string `json:"keyPem"`
}

// Issue publishes the LAN name's address, registers or reuses the node's ACME
// account, proves control of the LAN name lan.<domain> (and nothing else: the
// key lives in the core, so it must not be able to impersonate the identity
// origin or any service) through the publisher and installs the certificate.
// Nothing is ordered unless Me read the address back. One issuance runs at a
// time.
func (i Issuer) Issue(ctx context.Context) (State, error) {
	if i.Publisher == nil || i.Domain == "" || !slices.Contains(slices.Collect(maps.Values(Issuers)), i.Directory) {
		return State{}, errors.New("lancert: issuance needs a domain, a listed ACME issuer and a DNS-01 publisher")
	}
	if err := os.MkdirAll(CustodyDir(i.Workspace), 0o700); err != nil {
		return State{}, err
	}
	unlock, err := localevidence.AcquireCustodyLock(i.Workspace, accountRelPath, 6000)
	if err != nil {
		return State{}, fmt.Errorf("lancert: another issuance holds the LAN certificate custody: %w", err)
	}
	defer unlock()
	if _, err := PublishAddress(ctx, i.Workspace, i.Domain, i.Publisher); err != nil {
		return State{}, err
	}
	client, err := i.client(ctx)
	if err != nil {
		return State{}, err
	}
	name := LANName(i.Domain)
	order, err := client.AuthorizeOrder(ctx, acme.DomainIDs(name))
	if err != nil {
		return State{}, fmt.Errorf("lancert: the issuer refused the order: %w", err)
	}
	for _, authzURL := range order.AuthzURLs {
		if err := i.authorize(ctx, client, authzURL); err != nil {
			return State{}, err
		}
	}
	if order, err = client.WaitOrder(ctx, order.URI); err != nil {
		return State{}, fmt.Errorf("lancert: the order did not become ready: %w", err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return State{}, err
	}
	csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{DNSNames: []string{name}}, key)
	if err != nil {
		return State{}, err
	}
	chain, _, err := client.CreateOrderCert(ctx, order.FinalizeURL, csr, true)
	if err != nil {
		return State{}, fmt.Errorf("lancert: finalization failed: %w", err)
	}
	if err := i.install(chain, key); err != nil {
		return State{}, err
	}
	state, _, err := Load(i.Workspace)
	return state, err
}

func (i Issuer) authorize(ctx context.Context, client *acme.Client, authzURL string) error {
	authz, err := client.GetAuthorization(ctx, authzURL)
	if err != nil {
		return err
	}
	if authz.Status == acme.StatusValid {
		return nil
	}
	var challenge *acme.Challenge
	for _, candidate := range authz.Challenges {
		if candidate.Type == "dns-01" {
			challenge = candidate
		}
	}
	identifier := LANName(i.Domain)
	if challenge == nil || authz.Wildcard || authz.Identifier.Value != identifier {
		return errors.New("lancert: the issuer offered no DNS-01 challenge for the LAN name")
	}
	value, err := client.DNS01ChallengeRecord(challenge.Token)
	if err != nil {
		return err
	}
	if err := i.Publisher.SetChallenge(ctx, identifier, value); err != nil {
		return fmt.Errorf("lancert: kombify Me did not publish the DNS-01 value: %w", err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
		defer cancel()
		_ = i.Publisher.RemoveChallenge(cleanup, identifier, value)
	}()
	propagated := i.Propagated
	if propagated == nil {
		propagated = AuthoritativeTXT
	}
	if err := propagated(ctx, "_acme-challenge."+identifier, value); err != nil {
		return err
	}
	if _, err := client.Accept(ctx, challenge); err != nil {
		return fmt.Errorf("lancert: the issuer did not accept the challenge: %w", err)
	}
	if _, err := client.WaitAuthorization(ctx, authz.URI); err != nil {
		return fmt.Errorf("lancert: the issuer did not validate %s: %w", identifier, err)
	}
	return nil
}

// client loads the node's ACME account for this directory or registers one.
func (i Issuer) client(ctx context.Context) (*acme.Client, error) {
	path := filepath.Join(CustodyDir(i.Workspace), accountName)
	var stored account
	if raw, err := readLimited(path); err == nil {
		if json.Unmarshal(raw, &stored) != nil || stored.APIVersion != accountVersion {
			return nil, errors.New("lancert: the ACME account in custody is invalid")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if stored.Directory == i.Directory {
		block, _ := pem.Decode([]byte(stored.KeyPEM))
		if block == nil {
			return nil, errors.New("lancert: the ACME account key in custody is invalid")
		}
		key, err := x509.ParseECPrivateKey(block.Bytes)
		if err != nil {
			return nil, err
		}
		client := &acme.Client{Key: key, DirectoryURL: i.Directory, UserAgent: "stackkit-lan-certificate"}
		if _, err := client.GetReg(ctx, stored.URI); err != nil {
			return nil, fmt.Errorf("lancert: the ACME account in custody is not usable at the issuer: %w", err)
		}
		return client, nil
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	client := &acme.Client{Key: key, DirectoryURL: i.Directory, UserAgent: "stackkit-lan-certificate"}
	registration := &acme.Account{ExternalAccountBinding: i.EAB}
	if i.Contact != "" {
		if strings.ContainsAny(i.Contact, " ,?;:/\\") || !strings.Contains(i.Contact, "@") {
			return nil, errors.New("lancert: the ACME account contact must be one e-mail address")
		}
		registration.Contact = []string{"mailto:" + i.Contact}
	}
	registered, err := client.Register(ctx, registration, acme.AcceptTOS)
	if err != nil {
		return nil, fmt.Errorf("lancert: the issuer refused the account (an issuer with external account binding needs --eab-kid and --eab-hmac-file): %w", err)
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(account{
		APIVersion: accountVersion, Directory: i.Directory, URI: registered.URI,
		KeyPEM: string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})),
	})
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(CustodyDir(i.Workspace), 0o700); err != nil {
		return nil, err
	}
	return client, writeAtomic(path, raw, 0o600, -1, -1)
}

// install verifies the chain (against the system roots, for server
// authentication, except for a staging issuer), the key and the LAN name and
// writes the live directory: the key first, then the chain, under the lock.
func (i Issuer) install(chain [][]byte, key *ecdsa.PrivateKey) error {
	if len(chain) == 0 {
		return errors.New("lancert: the issuer answered no certificate")
	}
	leaf, err := x509.ParseCertificate(chain[0])
	if err != nil {
		return err
	}
	public, ok := leaf.PublicKey.(*ecdsa.PublicKey)
	if !ok || !public.Equal(key.Public()) || !slices.Equal(leaf.DNSNames, []string{LANName(i.Domain)}) {
		return errors.New("lancert: the issued certificate does not match the node's key and LAN name")
	}
	if !untrustedDirectories[i.Directory] {
		intermediates := x509.NewCertPool()
		for _, der := range chain[1:] {
			certificate, err := x509.ParseCertificate(der)
			if err != nil {
				return err
			}
			intermediates.AddCert(certificate)
		}
		if _, err := leaf.Verify(x509.VerifyOptions{DNSName: LANName(i.Domain), Intermediates: intermediates, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
			return fmt.Errorf("lancert: the issued chain is not publicly trusted for the LAN name: %w", err)
		}
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	var certPEM []byte
	for _, der := range chain {
		certPEM = append(certPEM, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)
	}
	if _, err := tls.X509KeyPair(certPEM, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})); err != nil {
		return err
	}
	live := LiveDir(i.Workspace)
	if err := os.MkdirAll(live, 0o755); err != nil {
		return err
	}
	if err := writeAtomic(filepath.Join(live, keyName), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600, i.KeyUID, i.KeyGID); err != nil {
		return err
	}
	return writeAtomic(filepath.Join(live, certName), certPEM, 0o644, -1, -1)
}

// Renew renews an issued LAN certificate within 30 days of its expiry with
// the account in custody (no external account binding is needed again), and
// otherwise republishes the LAN name's address: Me removes that record with
// every tunnel teardown, and the site address can move. A node that never
// issued one has nothing to renew or publish.
func Renew(ctx context.Context, workspace, domain string, publisher Publisher, keyUID, keyGID int) (bool, error) {
	raw, err := readLimited(filepath.Join(CustodyDir(workspace), accountName))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var stored account
	if json.Unmarshal(raw, &stored) != nil || stored.APIVersion != accountVersion ||
		!slices.Contains(slices.Collect(maps.Values(Issuers)), stored.Directory) {
		return false, errors.New("lancert: the ACME account in custody is invalid or names an unlisted issuer")
	}
	if !NeedsRenewal(workspace, domain, time.Now()) {
		return false, publishHeld(ctx, workspace, domain, publisher)
	}
	_, err = Issuer{Workspace: workspace, Domain: domain, Directory: stored.Directory, Publisher: publisher, KeyUID: keyUID, KeyGID: keyGID}.Issue(ctx)
	return err == nil, err
}

// Remove deletes the certificate, its key and the ACME account key under the
// custody lock. The live directory itself stays: the Basement core mounts it.
func Remove(workspace string) error {
	if err := os.MkdirAll(CustodyDir(workspace), 0o700); err != nil {
		return err
	}
	unlock, err := localevidence.AcquireCustodyLock(workspace, accountRelPath, 6000)
	if err != nil {
		return fmt.Errorf("lancert: another issuance holds the LAN certificate custody: %w", err)
	}
	defer unlock()
	var errs []error
	for _, path := range []string{
		filepath.Join(LiveDir(workspace), certName), filepath.Join(LiveDir(workspace), keyName),
		filepath.Join(CustodyDir(workspace), accountName),
	} {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// AuthoritativeTXT waits until every authoritative name server of the zone
// answers value for name, querying them directly: a shared recursive resolver
// asked too early caches the missing answer.
func AuthoritativeTXT(ctx context.Context, name, value string) error {
	zone := name
	for strings.Count(zone, ".") > 1 {
		zone = zone[strings.Index(zone, ".")+1:]
	}
	servers, err := net.DefaultResolver.LookupNS(ctx, zone)
	if err != nil || len(servers) == 0 {
		return fmt.Errorf("lancert: no name servers for %s: %w", zone, err)
	}
	deadline := time.Now().Add(3 * time.Minute)
	for {
		pending := 0
		for _, server := range servers {
			if !answers(ctx, server.Host, name, value) {
				pending++
			}
		}
		if pending == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("lancert: %d of %d name servers of %s do not answer the DNS-01 value yet", pending, len(servers), zone)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
}

func answers(ctx context.Context, server, name, value string) bool {
	addresses, err := net.DefaultResolver.LookupHost(ctx, server)
	if err != nil || len(addresses) == 0 {
		return false
	}
	resolver := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
		var dialer net.Dialer
		return dialer.DialContext(ctx, network, net.JoinHostPort(addresses[0], "53"))
	}}
	lookup, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	records, err := resolver.LookupTXT(lookup, name)
	return err == nil && slices.Contains(records, value)
}

func readLimited(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, maxPEMBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxPEMBytes {
		return nil, errors.New("lancert: custody file is oversized")
	}
	return raw, nil
}

func writeAtomic(path string, raw []byte, mode os.FileMode, uid, gid int) error {
	temp, err := os.CreateTemp(filepath.Dir(path), ".lan-tls-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	if err = temp.Chmod(mode); err == nil {
		_, err = temp.Write(raw)
	}
	if err == nil && uid >= 0 {
		err = temp.Chown(uid, gid)
	}
	if err == nil {
		err = temp.Sync()
	}
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(temp.Name(), path)
}
