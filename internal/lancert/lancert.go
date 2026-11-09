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

// Profile selects which certificate an Issuer obtains and where it lives. The
// LAN profile is the lan.<domain> certificate the Authority's LAN listener
// serves. The Public profile is the certificate of a public identity origin's
// router: *.<domain> and <domain> (id.<name>, base.<name>, ...), obtained by
// DNS-01 through kombify Me (a kombify.me name) or the owner's DNS provider (an
// own domain); the Basement core router mounts its live directory read-only.
type Profile struct {
	// Custody is the directory under .stackkit/custody that holds the ACME
	// account and the live directory.
	Custody string
	// Names are the certificate's DNS names for a domain.
	Names func(domain string) []string
	// PublishAddress publishes the node's private site address for the LAN name.
	PublishAddress bool
	agent          string
}

// LAN is the lan.<domain> profile (the zero Issuer profile).
var LAN = Profile{Custody: custodyDirName, Names: func(domain string) []string { return []string{LANName(domain)} }, PublishAddress: true, agent: "stackkit-lan-certificate"}

// Public is the public-names profile.
var Public = Profile{
	Custody: "public-tls",
	Names:   func(domain string) []string { return []string{"*." + domain, domain} }, agent: "stackkit-public-certificate",
}

func (p Profile) orLAN() Profile {
	if p.Names == nil {
		return LAN
	}
	return p
}

func (p Profile) custodyDir(workspace string) string {
	return filepath.Join(workspace, ".stackkit", "custody", p.orLAN().Custody)
}

func (p Profile) liveDir(workspace string) string {
	return filepath.Join(p.custodyDir(workspace), liveDirName)
}

// accountRel is the ACME account record; its custody lock serializes every
// issuance and renewal (the root CLI and the identity signer).
func (p Profile) accountRel() string {
	return ".stackkit/custody/" + p.orLAN().Custody + "/" + accountName
}

// checkName is a host the certificate must cover for the profile to count as installed.
func (p Profile) checkName(domain string) string {
	if p.orLAN().PublishAddress {
		return LANName(domain)
	}
	return "id." + domain
}

// LiveDirOf and CustodyDirOf expose a profile's directories.
func LiveDirOf(workspace string, p Profile) string    { return p.liveDir(workspace) }
func CustodyDirOf(workspace string, p Profile) string { return p.custodyDir(workspace) }

// LANName is the LAN name of a homelab domain.
func LANName(domain string) string { return LANLabel + "." + domain }

// CustodyDir is the Owner custody directory of the LAN certificate.
func CustodyDir(workspace string) string { return LAN.custodyDir(workspace) }

// LiveDir holds the certificate and its key; the Basement core mounts it.
func LiveDir(workspace string) string { return LAN.liveDir(workspace) }

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
func Load(workspace string) (State, *x509.Certificate, error) { return loadProfile(workspace, LAN) }

// LoadPublic parses the installed public-names certificate.
func LoadPublic(workspace string) (State, *x509.Certificate, error) {
	return loadProfile(workspace, Public)
}

func loadProfile(workspace string, profile Profile) (State, *x509.Certificate, error) {
	raw, err := readLimited(filepath.Join(profile.liveDir(workspace), certName))
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
	return CoversProfile(workspace, LAN, host, now)
}

// CoversProfile is Covers for a profile.
func CoversProfile(workspace string, profile Profile, host string, now time.Time) bool {
	_, leaf, err := loadProfile(workspace, profile)
	if err != nil || leaf.VerifyHostname(strings.ToLower(host)) != nil ||
		!now.After(leaf.NotBefore) || !now.Add(minimumLifetime).Before(leaf.NotAfter) {
		return false
	}
	_, err = tls.LoadX509KeyPair(filepath.Join(profile.liveDir(workspace), certName), filepath.Join(profile.liveDir(workspace), keyName))
	return err == nil
}

// NeedsRenewal reports a missing certificate, one that does not cover the
// domain's names, and one within 30 days of expiry.
func NeedsRenewal(workspace, domain string, now time.Time) bool {
	return NeedsRenewalProfile(workspace, LAN, domain, now)
}

// NeedsRenewalProfile is NeedsRenewal for a profile.
func NeedsRenewalProfile(workspace string, profile Profile, domain string, now time.Time) bool {
	_, leaf, err := loadProfile(workspace, profile)
	if err != nil || leaf.VerifyHostname(profile.checkName(domain)) != nil {
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
	unlock, err := localevidence.AcquireCustodyLock(workspace, LAN.accountRel(), 6000)
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
	// Profile is the certificate to obtain; the zero value is the LAN name.
	Profile   Profile
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
	profile := i.Profile.orLAN()
	if err := os.MkdirAll(profile.custodyDir(i.Workspace), 0o700); err != nil {
		return State{}, err
	}
	unlock, err := localevidence.AcquireCustodyLock(i.Workspace, profile.accountRel(), 6000)
	if err != nil {
		return State{}, fmt.Errorf("lancert: another issuance holds the certificate custody: %w", err)
	}
	defer unlock()
	if profile.PublishAddress {
		if _, err := PublishAddress(ctx, i.Workspace, i.Domain, i.Publisher); err != nil {
			return State{}, err
		}
	}
	client, err := i.client(ctx)
	if err != nil {
		return State{}, err
	}
	names := profile.Names(i.Domain)
	order, err := client.AuthorizeOrder(ctx, acme.DomainIDs(names...))
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
	csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{DNSNames: names}, key)
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
	state, _, err := loadProfile(i.Workspace, profile)
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
	profile := i.Profile.orLAN()
	identifier := authz.Identifier.Value
	allowed := false
	for _, name := range profile.Names(i.Domain) {
		if strings.TrimPrefix(name, "*.") == identifier && strings.HasPrefix(name, "*.") == authz.Wildcard {
			allowed = true
		}
	}
	if challenge == nil || !allowed {
		return errors.New("lancert: the issuer offered no DNS-01 challenge for the certificate's names")
	}
	value, err := client.DNS01ChallengeRecord(challenge.Token)
	if err != nil {
		return err
	}
	if err := i.Publisher.SetChallenge(ctx, identifier, value); err != nil {
		return fmt.Errorf("lancert: the DNS provider did not publish the DNS-01 value: %w", err)
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
	path := filepath.Join(i.Profile.custodyDir(i.Workspace), accountName)
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
		client := &acme.Client{Key: key, DirectoryURL: i.Directory, UserAgent: i.Profile.orLAN().agent}
		if _, err := client.GetReg(ctx, stored.URI); err != nil {
			return nil, fmt.Errorf("lancert: the ACME account in custody is not usable at the issuer: %w", err)
		}
		return client, nil
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	client := &acme.Client{Key: key, DirectoryURL: i.Directory, UserAgent: i.Profile.orLAN().agent}
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
	if err := os.MkdirAll(i.Profile.custodyDir(i.Workspace), 0o700); err != nil {
		return nil, err
	}
	return client, writeAtomic(i.Profile.custodyDir(i.Workspace), accountName, raw, 0o600, -1, -1)
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
	profile := i.Profile.orLAN()
	wantNames := slices.Clone(profile.Names(i.Domain))
	gotNames := slices.Clone(leaf.DNSNames)
	slices.Sort(wantNames)
	slices.Sort(gotNames)
	public, ok := leaf.PublicKey.(*ecdsa.PublicKey)
	if !ok || !public.Equal(key.Public()) || !slices.Equal(gotNames, wantNames) {
		return errors.New("lancert: the issued certificate does not match the node's key and names")
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
		if _, err := leaf.Verify(x509.VerifyOptions{DNSName: profile.checkName(i.Domain), Intermediates: intermediates, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
			return fmt.Errorf("lancert: the issued chain is not publicly trusted for the certificate's names: %w", err)
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
	custody := profile.custodyDir(i.Workspace)
	if err := mkdirCustody(custody, liveDirName, 0o755); err != nil {
		return err
	}
	if err := writeAtomic(custody, liveDirName+"/"+keyName, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600, i.KeyUID, i.KeyGID); err != nil {
		return err
	}
	return writeAtomic(custody, liveDirName+"/"+certName, certPEM, 0o644, -1, -1)
}

// Renew renews an issued LAN certificate within 30 days of its expiry with
// the account in custody (no external account binding is needed again), and
// otherwise republishes the LAN name's address: Me removes that record with
// every tunnel teardown, and the site address can move. A node that never
// issued one has nothing to renew or publish.
func Renew(ctx context.Context, workspace, domain string, publisher Publisher, keyUID, keyGID int) (bool, error) {
	return RenewProfile(ctx, workspace, LAN, domain, publisher, keyUID, keyGID)
}

// RenewProfile is Renew for a profile.
func RenewProfile(ctx context.Context, workspace string, profile Profile, domain string, publisher Publisher, keyUID, keyGID int) (bool, error) {
	raw, err := readLimited(filepath.Join(profile.custodyDir(workspace), accountName))
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
	if !NeedsRenewalProfile(workspace, profile, domain, time.Now()) {
		if !profile.orLAN().PublishAddress {
			return false, nil
		}
		return false, publishHeld(ctx, workspace, domain, publisher)
	}
	_, err = Issuer{Profile: profile, Workspace: workspace, Domain: domain, Directory: stored.Directory, Publisher: publisher, KeyUID: keyUID, KeyGID: keyGID}.Issue(ctx)
	return err == nil, err
}

// Remove deletes the certificate, its key and the ACME account key under the
// custody lock. The live directory itself stays: the Basement core mounts it.
func Remove(workspace string) error { return RemoveProfile(workspace, LAN) }

// RemoveProfile is Remove for a profile.
func RemoveProfile(workspace string, profile Profile) error {
	if err := os.MkdirAll(profile.custodyDir(workspace), 0o700); err != nil {
		return err
	}
	unlock, err := localevidence.AcquireCustodyLock(workspace, profile.accountRel(), 6000)
	if err != nil {
		return fmt.Errorf("lancert: another issuance holds the certificate custody: %w", err)
	}
	defer unlock()
	return RemoveConfined(profile.custodyDir(workspace), liveDirName+"/"+certName, liveDirName+"/"+keyName, accountName)
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

// A profile's custody directory (and the router policy directory) is handed to
// the identity signer's account after the first issuance, while root keeps
// writing into it (issue, remove, apply). Every write and removal there goes
// through an os.Root opened on that directory, whose own entry stays in a
// root-owned parent: a symlink planted inside (live, a file name) cannot
// redirect a root write or removal out of it.

// WriteConfined writes name under dir atomically (a new temporary file beside
// it, fsync, rename), confined to dir. A uid of -1 keeps the writer's owner.
func WriteConfined(dir, name string, raw []byte, mode os.FileMode, uid, gid int) error {
	return writeAtomic(dir, name, raw, mode, uid, gid)
}

// RemoveConfined removes the named files under dir, confined to dir; a
// missing file or directory is no error.
func RemoveConfined(dir string, names ...string) error {
	root, err := os.OpenRoot(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer root.Close()
	var errs []error
	for _, name := range names {
		if err := root.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func mkdirCustody(dir, name string, mode os.FileMode) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	return root.MkdirAll(name, mode)
}

func writeAtomic(dir, name string, raw []byte, mode os.FileMode, uid, gid int) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return err
	}
	// O_EXCL: an entry planted under the temporary name fails the write.
	temp := filepath.Join(filepath.Dir(filepath.FromSlash(name)), ".custody-"+hex.EncodeToString(suffix[:]))
	file, err := root.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	defer func() { _ = root.Remove(temp) }()
	if err = file.Chmod(mode); err == nil {
		_, err = file.Write(raw)
	}
	if err == nil && uid >= 0 {
		err = file.Chown(uid, gid)
	}
	if err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return root.Rename(temp, filepath.FromSlash(name))
}
