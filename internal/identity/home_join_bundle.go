package identity

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/subtle"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io/fs"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/kombifyio/stackkits/internal/localevidence"
)

// HomeJoinDeviceIssuer is the Authority's device credential issuer as a
// joined server verifies it: the credentials the Authority mints and the
// verification state it signs.
type HomeJoinDeviceIssuer struct {
	Issuer   string          `json:"issuer"`
	Audience string          `json:"audience"`
	KeyID    string          `json:"keyId"`
	Key      jose.JSONWebKey `json:"key"`
}

// HomeTrustBundle is the Authority-signed trust a joined server installs:
// issuer and keys (Pocket ID and the device credential issuer), the Owner
// subject, the root, the revocation feed (verification state), the joined
// server and its intermediate CA. The Owner key signs it; the Owner
// certificate, issued by the root, binds that key to the root the Owner
// confirms on the joining server.
type HomeTrustBundle struct {
	Schema                     string                                  `json:"schema"`
	AuthoritySiteRef           string                                  `json:"authoritySiteRef"`
	OwnerRef                   string                                  `json:"ownerRef"`
	OwnerSubject               string                                  `json:"ownerSubject"`
	RootCertificatePEM         string                                  `json:"rootCertificatePem"`
	OwnerCertificatePEM        string                                  `json:"ownerCertificatePem"`
	PocketID                   HomePocketIDTrust                       `json:"pocketId"`
	RevokedSubjects            []string                                `json:"revokedSubjects"`
	DeviceIssuer               HomeJoinDeviceIssuer                    `json:"deviceIssuer"`
	IdentityDomain             string                                  `json:"identityDomain"`
	IdentitySubdomainPrefix    string                                  `json:"identitySubdomainPrefix,omitempty"`
	AuthorityLANAddress        string                                  `json:"authorityLanAddress,omitempty"`
	VerificationState          string                                  `json:"verificationState"`
	Server                     HomeJoinServer                          `json:"server"`
	ClientKeyThumbprint        string                                  `json:"clientKeyThumbprint"`
	RequestNonce               string                                  `json:"requestNonce"`
	IntermediateCertificatePEM string                                  `json:"intermediateCertificatePem"`
	IssuedAt                   time.Time                               `json:"issuedAt"`
	ExpiresAt                  time.Time                               `json:"expiresAt"`
	Signature                  localevidence.OwnerPolicyStateSignature `json:"signature"`
}

func homeTrustBundleBytes(bundle HomeTrustBundle) []byte {
	bundle.Signature = localevidence.OwnerPolicyStateSignature{}
	raw, _ := json.Marshal(bundle)
	return raw
}

// ParseHomeTrustBundle decodes a carried trust bundle strictly.
func ParseHomeTrustBundle(raw []byte) (HomeTrustBundle, error) {
	var bundle HomeTrustBundle
	if len(raw) == 0 || len(raw) > homeJoinMaxBytes || decodeHomeJoinStrict(raw, &bundle) != nil || bundle.Schema != HomeTrustBundleSchema {
		return HomeTrustBundle{}, ErrHomeJoinRefused
	}
	return bundle, nil
}

// HomeTrustBundleRootFingerprint is the fingerprint the joining server's
// console shows the Owner before anything is installed. It authenticates
// nothing by itself.
func HomeTrustBundleRootFingerprint(bundle HomeTrustBundle) (string, error) {
	root, err := localevidence.ParseHomeJoinRoot(bundle.RootCertificatePEM)
	if err != nil {
		return "", ErrHomeJoinRefused
	}
	return localevidence.HomeJoinRootFingerprint(root), nil
}

// InstallHomeJoin runs on the joining server's host. The bundle must answer
// this server's pending request exactly, carry the root whose fingerprint
// the Owner entered here at request, be signed by the Owner of that root, and
// carry an intermediate the root issued for the pending key with exactly the
// requested DNS subtrees, server authentication only, the identity names and
// every IP range excluded, path length 0 and at most 90 days. Only then is
// the trust installed into verification custody; the intermediate stays
// beside its key in host custody, and the pending request is used up.
func InstallHomeJoin(root string, bundle HomeTrustBundle, now time.Time) error {
	now = now.UTC()
	request, err := loadPendingHomeJoin(root)
	if err != nil {
		return errors.New("identity: this server has no pending join request; run 'stackkit identity join request' first")
	}
	if homeActsAsAuthority(root) || homeJoined(root) {
		return errors.New("identity: this server already is an Authority or joined one; leave first")
	}
	if err := verifyHomeTrustBundle(bundle, request, request.AuthorityRoot, now); err != nil {
		return err
	}
	key, err := loadHomeJoinKey(root, homeJoinKeyPath, "intermediate-ca")
	if err != nil {
		return err
	}
	intermediate, _ := parseHomeJoinCertificate(bundle.IntermediateCertificatePEM)
	private, ok := key.(*ecdsa.PrivateKey)
	if !ok || !private.PublicKey.Equal(intermediate.PublicKey) {
		return ErrHomeJoinRefused
	}
	// The joined core: step-ca moves onto the intermediate and the core
	// records where the Authority's Pocket ID answers (the bundle's Owner-signed
	// LAN address). It refuses an intermediate that does not cover this
	// server's own core names.
	authority := localevidence.IdentityRuntimeAddress{Domain: bundle.IdentityDomain, SubdomainPrefix: bundle.IdentitySubdomainPrefix}
	if _, err := localevidence.BindJoinedCore(root, localevidence.JoinedCoreInput{
		AuthoritySiteRef: bundle.AuthoritySiteRef, AuthorityOwnerSubject: bundle.OwnerSubject,
		AuthorityRootPEM: bundle.RootCertificatePEM, IntermediatePEM: bundle.IntermediateCertificatePEM, IntermediateKey: private,
		AuthorityIdentityHost: authority.ServiceHost("id"), AuthorityLANAddress: bundle.AuthorityLANAddress,
	}); err != nil {
		return err
	}
	if err := writeHomePrivate(root, homeJoinIntermediate, []byte(bundle.IntermediateCertificatePEM), false); err != nil {
		return err
	}
	raw, err := json.Marshal(bundle)
	if err != nil {
		return err
	}
	if err := writeHomePrivate(root, homeJoinedTrustPath, raw, false); err != nil {
		return err
	}
	if err := removeHomePrivate(root, homeJoinRequestPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	invalidateHomeVerifier(root)
	return nil
}

// verifyHomeTrustBundle is the whole check of a bundle against the pending
// request and the confirmed root, in order: the pending request, the root
// fingerprint, the Owner signature, the intermediate's scope, the issuer
// keys and the Authority's origins.
//
//nolint:gocyclo // One ordered list of independent refusals.
func verifyHomeTrustBundle(bundle HomeTrustBundle, request HomeJoinRequest, confirmedRoot string, now time.Time) error {
	if bundle.Schema != HomeTrustBundleSchema || !slices.Equal(bundle.Server.Hostnames, request.Server.Hostnames) ||
		bundle.Server.OwnerRef != request.Server.OwnerRef || bundle.Server.SiteRef != request.Server.SiteRef || bundle.Server.NodeRef != request.Server.NodeRef ||
		bundle.Server.LANAddress != request.Server.LANAddress ||
		bundle.ClientKeyThumbprint != request.ClientKey.KeyID || subtle.ConstantTimeCompare([]byte(bundle.RequestNonce), []byte(request.Nonce)) != 1 {
		return ErrHomeJoinRefused
	}
	rootCert, err := localevidence.ParseHomeJoinRoot(bundle.RootCertificatePEM)
	confirmed := strings.ToLower(strings.TrimSpace(confirmedRoot))
	if err != nil || !validHomeDigest(confirmed) || subtle.ConstantTimeCompare([]byte(localevidence.HomeJoinRootFingerprint(rootCert)), []byte(confirmed)) != 1 {
		return errors.New("identity: the bundle's root is not the root you confirmed")
	}
	if localevidence.VerifyHomeTrustBundle(rootCert, bundle.OwnerCertificatePEM, bundle.OwnerRef, homeTrustBundleBytes(bundle), bundle.Signature, now) != nil {
		return ErrHomeJoinRefused
	}
	if bundle.IssuedAt.Location() != time.UTC || bundle.ExpiresAt.Location() != time.UTC || bundle.IssuedAt.After(now.Add(HomeClockLeeway)) ||
		!now.Before(bundle.ExpiresAt) || bundle.OwnerRef == request.Server.OwnerRef {
		return ErrHomeJoinRefused
	}
	address := localevidence.IdentityRuntimeAddress{Domain: bundle.IdentityDomain, SubdomainPrefix: bundle.IdentitySubdomainPrefix}
	intermediate, err := parseHomeJoinCertificate(bundle.IntermediateCertificatePEM)
	if err != nil || verifyHomeJoinIntermediate(intermediate, rootCert, request, homeJoinIdentityNames(address), bundle.ExpiresAt, now) != nil {
		return ErrHomeJoinRefused
	}
	if err := verifyHomeJoinIssuer(bundle); err != nil {
		return err
	}
	hub := "https://" + address.ServiceHost("base")
	if validateHomeOrigin(hub) != nil || bundle.VerificationState != hub+HomeJoinedStatePath || bundle.PocketID.Issuer != address.PocketIDOrigin() {
		return ErrHomeJoinRefused
	}
	if bundle.AuthorityLANAddress != "" {
		if address, parseErr := netip.ParseAddr(bundle.AuthorityLANAddress); parseErr != nil || address.String() != bundle.AuthorityLANAddress || !ObservedLANAddress(bundle.AuthorityLANAddress) {
			return ErrHomeJoinRefused
		}
	}
	return nil
}

// verifyHomeJoinIntermediate checks the intermediate the Authority issued:
// signed by the confirmed root for the pending key, a CA with path length 0,
// server authentication as its only extended key usage (so no client
// certificate chains to it), critical permitted DNS subtrees equal to the
// requested hostnames, the identity names and every IP range excluded, no other constraint, and a
// lifetime of at most 90 days ending at the bundle's expiry.
func verifyHomeJoinIntermediate(intermediate, rootCert *x509.Certificate, request HomeJoinRequest, identityNames []string, expires, now time.Time) error {
	public, err := parseHomeJoinIntermediateKey(request.IntermediatePublicKey)
	if err != nil || intermediate.CheckSignatureFrom(rootCert) != nil || !public.Equal(intermediate.PublicKey) {
		return ErrHomeJoinRefused
	}
	if !intermediate.IsCA || !intermediate.BasicConstraintsValid || intermediate.MaxPathLen != 0 || !intermediate.MaxPathLenZero ||
		intermediate.KeyUsage&x509.KeyUsageCertSign == 0 || !intermediate.PermittedDNSDomainsCritical ||
		!slices.Equal(intermediate.ExtKeyUsage, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}) || len(intermediate.UnknownExtKeyUsage) != 0 {
		return ErrHomeJoinRefused
	}
	permitted := slices.Clone(intermediate.PermittedDNSDomains)
	slices.Sort(permitted)
	if !slices.Equal(permitted, request.Server.Hostnames) || len(intermediate.PermittedIPRanges) != 0 ||
		len(intermediate.PermittedEmailAddresses) != 0 || len(intermediate.PermittedURIDomains) != 0 {
		return ErrHomeJoinRefused
	}
	for _, name := range identityNames {
		if !slices.Contains(intermediate.ExcludedDNSDomains, name) {
			return ErrHomeJoinRefused
		}
	}
	excludedV4, excludedV6 := false, false
	for _, network := range intermediate.ExcludedIPRanges {
		ones, bits := network.Mask.Size()
		excludedV4 = excludedV4 || (ones == 0 && bits == 32)
		excludedV6 = excludedV6 || (ones == 0 && bits == 128)
	}
	if !excludedV4 || !excludedV6 {
		return ErrHomeJoinRefused
	}
	lifetime := intermediate.NotAfter.Sub(intermediate.NotBefore)
	if lifetime <= 0 || lifetime > localevidence.HomeJoinIntermediateLifetime+10*time.Minute || !intermediate.NotAfter.UTC().Equal(expires) ||
		now.Before(intermediate.NotBefore) || !now.Before(intermediate.NotAfter) {
		return ErrHomeJoinRefused
	}
	return nil
}

// verifyHomeJoinIssuer checks the issuer keys the bundle carries: the
// Authority's device credential issuer and the Pocket ID pin of its Owner.
func verifyHomeJoinIssuer(bundle HomeTrustBundle) error {
	issuer := bundle.DeviceIssuer
	public, ok := issuer.Key.Key.(ed25519.PublicKey)
	prefix := "urn:stackkit:" + bundle.AuthoritySiteRef + ":"
	if !ok || len(public) != ed25519.PublicKeySize || !issuer.Key.IsPublic() || issuer.Key.KeyID != issuer.KeyID || bundle.AuthoritySiteRef == "" ||
		!strings.HasPrefix(issuer.Issuer, prefix) || !strings.HasPrefix(issuer.Audience, prefix) || issuer.Issuer == issuer.Audience {
		return ErrHomeJoinRefused
	}
	if thumbprint, err := homeKeyThumbprint(public); err != nil || thumbprint != issuer.KeyID {
		return ErrHomeJoinRefused
	}
	pin := bundle.PocketID
	if pin.Subject == "" || pin.Subject != bundle.OwnerSubject || pin.ClientID != HomeDeviceClientID || !validHomePublicKeys(pin.Keys) ||
		slices.Contains(bundle.RevokedSubjects, bundle.OwnerSubject) {
		return ErrHomeJoinRefused
	}
	return nil
}

func parseHomeJoinCertificate(raw string) (*x509.Certificate, error) {
	block, _ := pem.Decode([]byte(raw))
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, ErrHomeJoinRefused
	}
	return x509.ParseCertificate(block.Bytes)
}

func loadPendingHomeJoin(root string) (HomeJoinRequest, error) {
	raw, err := readHomePrivate(root, homeJoinRequestPath, homeJoinMaxBytes)
	if err != nil {
		return HomeJoinRequest{}, err
	}
	return ParseHomeJoinRequest(raw)
}

// LoadHomeJoinedTrust reads the installed trust bundle of a joined server.
// Its Owner signature and scope were verified at install against the root the
// Owner confirmed; the process that reads it is the verifier itself.
func LoadHomeJoinedTrust(root string) (HomeTrustBundle, error) {
	raw, err := readHomePrivate(root, homeJoinedTrustPath, homeJoinMaxBytes)
	if err != nil {
		return HomeTrustBundle{}, err
	}
	bundle, err := ParseHomeTrustBundle(raw)
	if err != nil {
		return HomeTrustBundle{}, err
	}
	if verifyHomeJoinIssuer(bundle) != nil {
		return HomeTrustBundle{}, ErrHomeJoinRefused
	}
	return bundle, nil
}

// homeJoined reports a server that holds an installed trust bundle, valid or
// not: a damaged or expired bundle keeps the server joined and verifying
// nothing, it never turns the server back into an Authority.
func homeJoined(root string) bool {
	_, err := readHomePrivate(root, homeJoinedTrustPath, homeJoinMaxBytes)
	return err == nil || !errors.Is(err, fs.ErrNotExist)
}

// LeaveHomeJoin removes the installed trust (host only): the server stops
// verifying the Authority's credentials at once, its TinyAuth loses the
// Authority's client, its LAN resolver the homelab zone, and its step-ca
// returns to a fresh intermediate under its own root. The next apply runs
// this server's own Pocket ID again.
func LeaveHomeJoin(root string) error {
	for _, path := range []string{homeJoinedTrustPath, homeJoinedStateFile, homeAccountGroupsPath, homeJoinRequestPath} {
		if err := removeHomePrivate(root, path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	invalidateHomeVerifier(root)
	if err := localevidence.DiscardJoinedTinyAuthPocketIDBinding(root); err != nil {
		return err
	}
	if _, err := localevidence.InstallBasementLANDNSZone(root, nil); err != nil && !errors.Is(err, localevidence.ErrBasementRuntimeCustodyMissing) {
		return err
	}
	return localevidence.UnbindJoinedCore(root)
}
