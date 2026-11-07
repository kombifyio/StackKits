package identity

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/kombifyio/stackkits/internal/localevidence"
)

// One Authority across servers (homelab identity target §4, §14.1, P2).
// Several servers exist only under Techstack; the server that runs Pocket ID
// is the Authority and every further server joins it:
//
//  1. The joining server prepares a join request (PrepareHomeJoin): its own
//     server identity, the hostnames it serves, a fresh P-256 key for its
//     intermediate CA (host custody) and an Ed25519 key that authenticates
//     its verification-state reads (verification custody).
//  2. Techstack carries the request to the Authority, whose console raises
//     an Owner step-up of class join for exactly this request; the Owner
//     approves with a passkey in the Hub (ApproveHomeJoin). The Authority's
//     step-ca root then signs the intermediate (DNS subtrees of the server's
//     hostnames, every IP range and the identity names excluded, path length
//     0, 90 days) and its Owner key signs the trust bundle: issuer, keys,
//     Owner subject, root, revocation feed.
//  3. Techstack carries the bundle back; the joining server's console shows
//     the root fingerprint, the Owner confirms it, and InstallHomeJoin
//     verifies every signature and constraint before it installs the trust.
//
// Techstack only carries both documents: it can neither forge nor widen one.
const (
	HomeJoinRequestSchema = "stackkit.homelab-join-request/v1"
	HomeTrustBundleSchema = "stackkit.homelab-trust-bundle/v1"

	// HomeJoinedStatePath is the Authority's verification-state route on the
	// Hub host for joined servers.
	HomeJoinedStatePath = "/api/v1/identity/home/joined/state"

	homeJoinDir            = ".stackkit/custody/home-join"
	homeJoinKeyPath        = homeJoinDir + "/intermediate-key.json"
	homeJoinRequestPath    = homeJoinDir + "/request.json"
	homeJoinIntermediate   = homeJoinDir + "/intermediate.crt"
	homeJoinClientKeyPath  = ".stackkit/custody/home-verifier/join-client-key.json"
	homeJoinedTrustPath    = ".stackkit/custody/home-verifier/joined-trust.json"
	homeJoinedServersPath  = ".stackkit/custody/home-verifier/joined-servers.json"
	homeJoinKeySchema      = "stackkit.homelab-join-key/v1"
	homeJoinedServerSchema = "stackkit.homelab-joined-servers/v1"
	homeJoinMaxHostnames   = 32
	homeJoinMaxBytes       = 64 << 10
	homeJoinMaxServers     = 64
	homeJoinRequestMaxAge  = 24 * time.Hour
)

var (
	// ErrHomeJoinRefused is the one answer to a join request or trust bundle
	// that does not verify, does not match or is not allowed.
	ErrHomeJoinRefused = errors.New("identity: the join request or trust bundle was refused")
	homeJoinRefPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,127}$`)
	homeJoinHostname   = regexp.MustCompile(`^(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
)

// HomeJoinServer is the joining server as it identifies itself: its own
// lifecycle identity (ownerRef, site and node) and the names it serves.
type HomeJoinServer struct {
	OwnerRef  string   `json:"ownerRef"`
	SiteRef   string   `json:"siteRef"`
	NodeRef   string   `json:"nodeRef"`
	Hostnames []string `json:"hostnames"`
	// LANAddress is where the server's own LAN resolver says it answers. The
	// Owner approves it with the request; the homelab zone points the
	// server's names only there.
	LANAddress string `json:"lanAddress,omitempty"`
}

// HomeJoinRequest is what the joining server sends to the Authority. It holds
// public keys only, and the Authority root fingerprint the Owner entered on
// the joining server.
type HomeJoinRequest struct {
	Schema                string          `json:"schema"`
	Server                HomeJoinServer  `json:"server"`
	AuthorityRoot         string          `json:"authorityRoot"`
	IntermediatePublicKey string          `json:"intermediatePublicKey"`
	ClientKey             jose.JSONWebKey `json:"clientKey"`
	RequestedAt           time.Time       `json:"requestedAt"`
	Nonce                 string          `json:"nonce"`
}

type homeJoinPrivateKey struct {
	Schema string `json:"schema"`
	Kind   string `json:"kind"`
	PKCS8  string `json:"pkcs8"`
}

// PrepareHomeJoin runs on the joining server's host. authorityRoot is the
// root fingerprint the Owner read at the Authority and entered here; only a
// bundle under that root ever installs. It generates the intermediate CA key
// into host custody and the state-read key into verification custody and
// returns the request Techstack carries. A server that already holds an
// Authority's device registry, or already joined, cannot request.
func PrepareHomeJoin(root string, hostnames []string, authorityRoot string, now time.Time) (HomeJoinRequest, error) {
	owner, err := localevidence.LoadOwnerCustody(root)
	if err != nil {
		return HomeJoinRequest{}, err
	}
	if homeActsAsAuthority(root) {
		return HomeJoinRequest{}, errors.New("identity: this server already is an Authority with enrolled devices; a further server joins without its own registry")
	}
	if homeJoined(root) {
		return HomeJoinRequest{}, errors.New("identity: this server already joined an Authority; run 'stackkit identity join leave' first")
	}
	// A server joins before its first apply: its core then starts on the
	// joined intermediate, and no ingress certificate of its own root remains.
	if _, err := os.Stat(filepath.Join(root, ".stackkit", "runtime", "basement-core", "compose.yaml")); err == nil {
		return HomeJoinRequest{}, errors.New("identity: this server's core is already applied; a server joins after stackkit init and before its first apply")
	}
	intermediate, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return HomeJoinRequest{}, err
	}
	client, clientPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return HomeJoinRequest{}, err
	}
	publicDER, err := x509.MarshalPKIXPublicKey(&intermediate.PublicKey)
	if err != nil {
		return HomeJoinRequest{}, err
	}
	first, err := homeRandomID()
	if err != nil {
		return HomeJoinRequest{}, err
	}
	second, err := homeRandomID()
	if err != nil {
		return HomeJoinRequest{}, err
	}
	thumbprint, err := homeKeyThumbprint(client)
	if err != nil {
		return HomeJoinRequest{}, err
	}
	request := HomeJoinRequest{
		Schema: HomeJoinRequestSchema, AuthorityRoot: strings.ToLower(strings.TrimSpace(authorityRoot)),
		Server:                HomeJoinServer{OwnerRef: owner.OwnerRef, SiteRef: owner.Binding.SiteRef, NodeRef: owner.Binding.NodeRef, Hostnames: normalizeHomeJoinHostnames(hostnames)},
		IntermediatePublicKey: string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER})),
		ClientKey:             jose.JSONWebKey{Key: client, KeyID: thumbprint, Algorithm: string(jose.EdDSA), Use: "sig"},
		RequestedAt:           now.UTC().Truncate(time.Second), Nonce: first + second,
	}
	if !validHomeJoinRequest(request) || len(HomeJoinSummary(request)) > homeStepUpSummaryMax {
		return HomeJoinRequest{}, errors.New("identity: a join request needs the Authority's root fingerprint (64 hex characters), this server's owner, site and node references and 1 to 32 lowercase hostnames that fit the Owner's approval text")
	}
	// The joined core serves step-ca and TinyAuth under this server's own
	// domain, so the intermediate must cover those names.
	if own, ownErr := localevidence.LocalIdentityRuntimeAddress(root); ownErr == nil {
		if _, address, siteErr := localevidence.BasementLANDNSSite(root); siteErr == nil {
			request.Server.LANAddress = address.String()
			if !validHomeJoinRequest(request) || len(HomeJoinSummary(request)) > homeStepUpSummaryMax {
				return HomeJoinRequest{}, errors.New("identity: this server's LAN address is not a private address the homelab zone can name")
			}
		}
		for _, name := range localevidence.JoinedCoreNames(own.Domain) {
			if !slices.ContainsFunc(request.Server.Hostnames, func(tree string) bool { return name == tree || strings.HasSuffix(name, "."+tree) }) {
				return HomeJoinRequest{}, errors.New("identity: include this server's own domain " + own.Domain + " as a hostname: its joined core serves " + name)
			}
		}
	}
	if err := writeHomeJoinKey(root, homeJoinKeyPath, "intermediate-ca", intermediate); err != nil {
		return HomeJoinRequest{}, err
	}
	if err := writeHomeJoinKey(root, homeJoinClientKeyPath, "state-read", clientPrivate); err != nil {
		return HomeJoinRequest{}, err
	}
	raw, err := json.Marshal(request)
	if err != nil {
		return HomeJoinRequest{}, err
	}
	return request, writeHomePrivate(root, homeJoinRequestPath, raw, false)
}

// HomeJoinRequestCode is the short digest of a request both consoles show:
// the joining server when it writes the request and the Hub in the Owner's
// step-up, so a carrier that swapped a key or a hostname is seen.
func HomeJoinRequestCode(request HomeJoinRequest) string {
	raw, _ := json.Marshal(request)
	sum := sha256.Sum256(raw)
	code := hex.EncodeToString(sum[:8])
	return code[0:4] + "-" + code[4:8] + "-" + code[8:12] + "-" + code[12:16]
}

// ParseHomeJoinRequest decodes a carried join request strictly.
func ParseHomeJoinRequest(raw []byte) (HomeJoinRequest, error) {
	var request HomeJoinRequest
	if len(raw) == 0 || len(raw) > homeJoinMaxBytes || decodeHomeJoinStrict(raw, &request) != nil || !validHomeJoinRequest(request) {
		return HomeJoinRequest{}, ErrHomeJoinRefused
	}
	return request, nil
}

// HomeJoinAction is the step-up action of class join for exactly this
// request on this Authority: the Owner's passkey approves these hostnames,
// these keys and this server, nothing else.
func HomeJoinAction(root string, request HomeJoinRequest) (HomeStepUpAction, error) {
	authority, err := homeAuthority(root)
	if err != nil {
		return HomeStepUpAction{}, err
	}
	return NewHomeStepUpAction(HomeStepUpJoin, struct {
		AuthoritySiteRef string          `json:"authoritySiteRef"`
		Request          HomeJoinRequest `json:"request"`
	}{authority.SiteRef, request})
}

// homeStepUpSummaryMax is the longest text a step-up request carries.
const homeStepUpSummaryMax = 200

// HomeJoinSummary is the text the Hub shows the Owner for the step-up: the
// server, every hostname and the request code. A request whose text does
// not fit is refused, never shortened.
func HomeJoinSummary(request HomeJoinRequest) string {
	server := request.Server.NodeRef
	if request.Server.LANAddress != "" {
		server += " at " + request.Server.LANAddress
	}
	return "Join " + server + " as " + strings.Join(request.Server.Hostnames, ", ") + " (request " + HomeJoinRequestCode(request) + ")"
}

// ApproveHomeJoin runs on the Authority's host, which holds the Owner
// custody. approve must prove the Owner's passkey step-up for exactly
// HomeJoinAction(request) (VerifyHomeStepUp through the console's request);
// only then does the root sign the intermediate and the Owner key the
// bundle. The joined server is recorded for the verification-state route
// and the Hub's launch hosts.
func ApproveHomeJoin(ctx context.Context, root string, request HomeJoinRequest, approve func(context.Context, HomeStepUpAction, string) error, now time.Time) (HomeTrustBundle, error) {
	now = now.UTC()
	if !validHomeJoinRequest(request) || now.Sub(request.RequestedAt) > homeJoinRequestMaxAge || request.RequestedAt.After(now.Add(HomeClockLeeway)) ||
		len(HomeJoinSummary(request)) > homeStepUpSummaryMax {
		return HomeTrustBundle{}, ErrHomeJoinRefused
	}
	material, err := localevidence.LoadHomeJoinIssuerMaterial(root)
	if err != nil {
		return HomeTrustBundle{}, err
	}
	if request.Server.OwnerRef == material.OwnerRef {
		return HomeTrustBundle{}, errors.New("identity: the Authority cannot join itself")
	}
	rootCert, err := localevidence.ParseHomeJoinRoot(material.RootCertificatePEM)
	if err != nil || localevidence.HomeJoinRootFingerprint(rootCert) != request.AuthorityRoot {
		return HomeTrustBundle{}, errors.New("identity: the joining server pinned another Authority's root; the Owner enters this Authority's root fingerprint there")
	}
	trust, err := loadCurrentHomeTrust(root, now)
	if err != nil {
		return HomeTrustBundle{}, errors.New("identity: no current Home trust; run 'stackkit device trust' on the Authority first")
	}
	address, err := homeIdentityAddress(root)
	if err != nil {
		return HomeTrustBundle{}, err
	}
	authority, err := homeAuthority(root)
	if err != nil {
		return HomeTrustBundle{}, err
	}
	issuer, err := loadHomeDeviceIssuer(root)
	if err != nil {
		return HomeTrustBundle{}, errors.New("identity: the Authority has no device credential issuer yet; enroll a device first")
	}
	public, err := parseHomeJoinIntermediateKey(request.IntermediatePublicKey)
	if err != nil {
		return HomeTrustBundle{}, err
	}
	excluded := homeJoinIdentityNames(address)
	reserved := append(slices.Clone(excluded), loadHomeAuthorityHosts(root)...)
	check := func() error {
		servers, loadErr := loadHomeJoinedServers(root)
		if loadErr != nil && !errors.Is(loadErr, fs.ErrNotExist) {
			return loadErr
		}
		return homeJoinHostnamesAllowed(request, address, reserved, servers, now)
	}
	if err := check(); err != nil {
		return HomeTrustBundle{}, err
	}
	action, err := HomeJoinAction(root, request)
	if err != nil {
		return HomeTrustBundle{}, err
	}
	if approve == nil {
		return HomeTrustBundle{}, ErrHomeStepUpDenied
	}
	if err := approve(ctx, action, HomeJoinSummary(request)); err != nil {
		return HomeTrustBundle{}, err
	}
	// The approval took up to five minutes; another join may have taken a
	// name meanwhile, so the names are checked again under the registry lock.
	release, err := localevidence.AcquireCustodyLock(root, homeJoinedServersPath, 1)
	if err != nil {
		return HomeTrustBundle{}, err
	}
	defer release()
	if err := check(); err != nil {
		return HomeTrustBundle{}, err
	}
	der, err := localevidence.IssueHomeJoinIntermediate(root, localevidence.HomeJoinIntermediateRequest{
		CommonName: "StackKits intermediate " + request.Server.NodeRef, PublicKey: public,
		Permitted: request.Server.Hostnames, Excluded: excluded,
	}, now)
	if err != nil {
		return HomeTrustBundle{}, err
	}
	intermediate, err := x509.ParseCertificate(der)
	if err != nil {
		return HomeTrustBundle{}, err
	}
	bundle := HomeTrustBundle{
		Schema: HomeTrustBundleSchema, AuthoritySiteRef: authority.SiteRef, OwnerRef: material.OwnerRef, OwnerSubject: trust.PocketID.Subject,
		RootCertificatePEM: material.RootCertificatePEM, OwnerCertificatePEM: material.OwnerCertificatePEM,
		PocketID: trust.PocketID, RevokedSubjects: append([]string{}, trust.RevokedSubjects...),
		DeviceIssuer: HomeJoinDeviceIssuer{
			Issuer: issuer.issuer, Audience: issuer.audience, KeyID: issuer.keyID,
			Key: jose.JSONWebKey{Key: issuer.key, KeyID: issuer.keyID, Algorithm: string(jose.EdDSA), Use: "sig"},
		},
		IdentityDomain: address.Domain, IdentitySubdomainPrefix: address.SubdomainPrefix, AuthorityLANAddress: authority.LANSiteAddress,
		VerificationState: "https://" + address.ServiceHost("base") + HomeJoinedStatePath,
		Server:            request.Server, ClientKeyThumbprint: request.ClientKey.KeyID, RequestNonce: request.Nonce,
		IntermediateCertificatePEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		IssuedAt:                   now.Truncate(time.Second), ExpiresAt: intermediate.NotAfter.UTC(),
	}
	if bundle.Signature, err = localevidence.SignHomeTrustBundle(root, homeTrustBundleBytes(bundle)); err != nil {
		return HomeTrustBundle{}, err
	}
	if err := recordHomeJoinedServer(root, request, intermediate, now); err != nil {
		return HomeTrustBundle{}, err
	}
	return bundle, nil
}

// homeJoinIdentityNames are the Authority's identity names a further
// server's intermediate never covers: Pocket ID, the Hub, TinyAuth and the
// LAN enrollment listener.
func homeJoinIdentityNames(address localevidence.IdentityRuntimeAddress) []string {
	names := []string{address.ServiceHost("id"), address.ServiceHost("base"), address.ServiceHost("auth")}
	if homeJoinHostname.MatchString(HomeEnrollmentServerName) {
		names = append(names, HomeEnrollmentServerName)
	}
	return names
}

// homeJoinHostnamesAllowed admits only names inside the homelab domain that
// cover no identity name or Authority host and no name of another joined
// server whose intermediate is still valid (a removed server's intermediate
// stays valid to its expiry, so its names stay reserved until then).
func homeJoinHostnamesAllowed(request HomeJoinRequest, address localevidence.IdentityRuntimeAddress, reserved []string, servers homeJoinedServers, now time.Time) error {
	overlaps := func(a, b string) bool { return a == b || strings.HasSuffix(a, "."+b) || strings.HasSuffix(b, "."+a) }
	for _, host := range request.Server.Hostnames {
		if !strings.HasSuffix(host, "."+address.Domain) {
			return errors.New("identity: a joined server's hostname must lie inside the homelab domain " + address.Domain)
		}
		for _, name := range reserved {
			if overlaps(host, name) {
				return errors.New("identity: a joined server's hostname cannot cover the Authority's name " + name)
			}
		}
		for _, other := range servers.Servers {
			if (other.Removed && !now.Before(other.ExpiresAt)) || other.Server.OwnerRef == request.Server.OwnerRef {
				continue
			}
			for _, held := range other.Server.Hostnames {
				if overlaps(host, held) {
					return errors.New("identity: the hostname " + host + " overlaps a name of joined server " + other.Server.NodeRef)
				}
			}
		}
	}
	return nil
}

func normalizeHomeJoinHostnames(hostnames []string) []string {
	out := []string{}
	for _, host := range hostnames {
		host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
		if host != "" && !slices.Contains(out, host) {
			out = append(out, host)
		}
	}
	slices.Sort(out)
	return out
}

func validHomeJoinServer(server HomeJoinServer) bool {
	if !homeJoinRefPattern.MatchString(server.OwnerRef) || !homeJoinRefPattern.MatchString(server.SiteRef) || !homeJoinRefPattern.MatchString(server.NodeRef) ||
		len(server.Hostnames) == 0 || len(server.Hostnames) > homeJoinMaxHostnames || !slices.IsSorted(server.Hostnames) {
		return false
	}
	for index, host := range server.Hostnames {
		if len(host) > 253 || !homeJoinHostname.MatchString(host) || (index > 0 && server.Hostnames[index-1] == host) {
			return false
		}
	}
	return server.LANAddress == "" || homeZoneAddress(server.LANAddress)
}

func validHomeJoinRequest(request HomeJoinRequest) bool {
	if request.Schema != HomeJoinRequestSchema || !validHomeJoinServer(request.Server) || !validHomeStepUpChallenge(request.Nonce) ||
		request.RequestedAt.IsZero() || request.RequestedAt.Location() != time.UTC || !validHomeDigest(request.AuthorityRoot) {
		return false
	}
	if _, err := parseHomeJoinIntermediateKey(request.IntermediatePublicKey); err != nil {
		return false
	}
	return validHomeJoinClientKey(request.ClientKey)
}

func validHomeJoinClientKey(key jose.JSONWebKey) bool {
	public, ok := key.Key.(ed25519.PublicKey)
	if !ok || len(public) != ed25519.PublicKeySize || !key.IsPublic() || key.Algorithm != string(jose.EdDSA) || key.Use != "sig" {
		return false
	}
	thumbprint, err := homeKeyThumbprint(public)
	return err == nil && thumbprint == key.KeyID
}

func parseHomeJoinIntermediateKey(raw string) (*ecdsa.PublicKey, error) {
	block, rest := pem.Decode([]byte(raw))
	if block == nil || block.Type != "PUBLIC KEY" || len(bytes.TrimSpace(rest)) != 0 {
		return nil, ErrHomeJoinRefused
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	public, ok := parsed.(*ecdsa.PublicKey)
	if err != nil || !ok || public.Curve != elliptic.P256() {
		return nil, ErrHomeJoinRefused
	}
	return public, nil
}

// homeActsAsAuthority reports a server that holds a device credential issuer
// or registry entries of its own.
func homeActsAsAuthority(root string) bool {
	if _, err := loadHomeDeviceIssuer(root); err == nil {
		return true
	}
	registry, err := loadHomeDeviceRegistry(root)
	return err == nil && len(registry.Devices) > 0
}

func writeHomeJoinKey(root, path, kind string, key any) error {
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(homeJoinPrivateKey{Schema: homeJoinKeySchema, Kind: kind, PKCS8: base64.RawStdEncoding.EncodeToString(der)})
	if err != nil {
		return err
	}
	return writeHomePrivate(root, path, raw, false)
}

func loadHomeJoinKey(root, path, kind string) (any, error) {
	raw, err := readHomePrivate(root, path, 8<<10)
	if err != nil {
		return nil, err
	}
	var file homeJoinPrivateKey
	if decodeHomeJoinStrict(raw, &file) != nil || file.Schema != homeJoinKeySchema || file.Kind != kind {
		return nil, errors.New("identity: unreadable join key custody")
	}
	der, err := base64.RawStdEncoding.Strict().DecodeString(file.PKCS8)
	if err != nil {
		return nil, errors.New("identity: unreadable join key custody")
	}
	return x509.ParsePKCS8PrivateKey(der)
}

func decodeHomeJoinStrict(raw []byte, into any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(into); err != nil {
		return err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return errors.New("identity: trailing data")
	}
	return nil
}
