package localevidence

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/contracts/applyevidence"
)

const (
	// HomeAuthorityCustodyRelDir is the Authority's verification-only custody:
	// what the Basement core's identity workspace verifies against, written
	// only on the host (Apply, the host-side identity signer, the console) and
	// mounted read-only into the core. It holds no private key.
	HomeAuthorityCustodyRelDir = ".stackkit/custody/home-authority"
	// HomeAuthorityPublicRelPath is the public Authority record.
	HomeAuthorityPublicRelPath = HomeAuthorityCustodyRelDir + "/authority.json"

	homeAuthorityPublicAPIVersion = "stackkit.home-authority-public/v1"
	homeAuthorityPublicMaxBytes   = 64 << 10
)

// ErrHomeAuthorityPublicMissing reports that the host has not written the
// public Authority record yet (stackkit apply writes it).
var ErrHomeAuthorityPublicMissing = errors.New("localevidence: no public Authority record; run stackkit apply on the Authority")

// HomeAuthorityPublic is the verification-only projection of the Authority's
// Owner custody: the Owner's public evidence key, the step-ca root
// certificate, the bound Owner Pocket ID subject, the identity runtime address
// and the LAN site address. It verifies the Owner-signed Home trust and names
// the Authority's own origins and listener names; it can sign nothing.
type HomeAuthorityPublic struct {
	APIVersion               string    `json:"apiVersion"`
	OwnerRef                 string    `json:"ownerRef"`
	KeyID                    string    `json:"keyId"`
	OwnerPublicKey           string    `json:"ownerPublicKey"`
	SiteRef                  string    `json:"siteRef"`
	OwnerPocketIDSubject     string    `json:"ownerPocketIdSubject"`
	StepCARootCertificatePEM string    `json:"stepCaRootCertificatePem"`
	IdentityDomain           string    `json:"identityDomain,omitempty"`
	IdentitySubdomainPrefix  string    `json:"identitySubdomainPrefix,omitempty"`
	LANSiteAddress           string    `json:"lanSiteAddress,omitempty"`
	WrittenAt                time.Time `json:"writtenAt"`
}

// WriteHomeAuthorityPublic derives the public Authority record from the
// verified Owner custody of this workspace and writes it (host only). The
// identity runtime address and the LAN site address are included once their
// owner-signed custody exists.
func WriteHomeAuthorityPublic(workspaceRoot string, now time.Time) (HomeAuthorityPublic, error) {
	owner, err := LoadOwnerCustody(workspaceRoot)
	if err != nil {
		return HomeAuthorityPublic{}, err
	}
	key, err := LoadOwnerKey(workspaceRoot)
	if err != nil {
		return HomeAuthorityPublic{}, err
	}
	if key.OwnerRef != owner.OwnerRef || key.KeyID != owner.KeyID {
		return HomeAuthorityPublic{}, errors.New("localevidence: owner key differs from established owner custody")
	}
	subject := owner.PocketID.Subject
	binding, err := LoadOwnerRuntimeBinding(workspaceRoot)
	switch {
	case err == nil && binding.OwnerRef == owner.OwnerRef && binding.PocketIDSubject != "":
		subject = binding.PocketIDSubject
	case err != nil && !errors.Is(err, ErrOwnerRuntimeBindingMissing):
		return HomeAuthorityPublic{}, err
	}
	record := HomeAuthorityPublic{
		APIVersion: homeAuthorityPublicAPIVersion, OwnerRef: owner.OwnerRef, KeyID: owner.KeyID,
		OwnerPublicKey: base64.RawStdEncoding.EncodeToString(key.Public()), SiteRef: owner.Binding.SiteRef,
		OwnerPocketIDSubject: subject, StepCARootCertificatePEM: owner.StepCARootCertificatePEM,
		WrittenAt: now.UTC().Truncate(time.Second),
	}
	if address, addressErr := LocalIdentityRuntimeAddress(workspaceRoot); addressErr == nil {
		record.IdentityDomain, record.IdentitySubdomainPrefix = address.Domain, address.SubdomainPrefix
	}
	if site, siteErr := BasementLANDNSResolverAddress(workspaceRoot); siteErr == nil {
		record.LANSiteAddress = site
	}
	if err := record.validate(); err != nil {
		return HomeAuthorityPublic{}, err
	}
	if prior, priorErr := LoadHomeAuthorityPublic(workspaceRoot); priorErr == nil && prior.sameExceptTime(record) {
		return prior, nil
	}
	path, err := confinedCustodyPath(workspaceRoot, HomeAuthorityPublicRelPath)
	if err != nil {
		return HomeAuthorityPublic{}, err
	}
	if err := writePrivateJSON(path, record); err != nil {
		return HomeAuthorityPublic{}, fmt.Errorf("localevidence: persist the public Authority record: %w", err)
	}
	return record, nil
}

// CurrentHomeAuthority is the one source of the Authority's public identity
// facts. Where this process holds the Owner custody (the host: Apply, the
// console, the host-side identity signer) it derives the record from that
// custody and keeps the public copy current; where it holds only the
// verification custody (the Basement core's identity workspace) it reads the
// public copy the host wrote. A workspace with neither fails closed.
func CurrentHomeAuthority(workspaceRoot string) (HomeAuthorityPublic, error) {
	record, err := WriteHomeAuthorityPublic(workspaceRoot, time.Now())
	if err == nil {
		return record, nil
	}
	if !errors.Is(err, ErrOwnerCustodyMissing) {
		return HomeAuthorityPublic{}, err
	}
	return LoadHomeAuthorityPublic(workspaceRoot)
}

// LoadHomeAuthorityPublic reads and validates the public Authority record.
func LoadHomeAuthorityPublic(workspaceRoot string) (HomeAuthorityPublic, error) {
	path, err := confinedCustodyPath(workspaceRoot, HomeAuthorityPublicRelPath)
	if err != nil {
		return HomeAuthorityPublic{}, err
	}
	file, err := os.Open(path) //nolint:gosec // fixed path below the explicit workspace
	if errors.Is(err, os.ErrNotExist) {
		return HomeAuthorityPublic{}, ErrHomeAuthorityPublicMissing
	}
	if err != nil {
		return HomeAuthorityPublic{}, fmt.Errorf("localevidence: read the public Authority record: %w", err)
	}
	defer func() { _ = file.Close() }()
	raw, err := io.ReadAll(io.LimitReader(file, homeAuthorityPublicMaxBytes+1))
	if err != nil || len(raw) > homeAuthorityPublicMaxBytes {
		return HomeAuthorityPublic{}, errors.New("localevidence: the public Authority record is unreadable")
	}
	var record HomeAuthorityPublic
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&record) != nil || decoder.Decode(new(any)) != io.EOF {
		return HomeAuthorityPublic{}, errors.New("localevidence: the public Authority record is malformed")
	}
	if err := record.validate(); err != nil {
		return HomeAuthorityPublic{}, err
	}
	return record, nil
}

func (r HomeAuthorityPublic) validate() error {
	invalid := errors.New("localevidence: the public Authority record is not a recognised record")
	if r.APIVersion != homeAuthorityPublicAPIVersion || strings.TrimSpace(r.OwnerRef) == "" || strings.TrimSpace(r.SiteRef) == "" ||
		strings.TrimSpace(r.OwnerPocketIDSubject) == "" || r.WrittenAt.IsZero() {
		return invalid
	}
	public, err := r.ownerKey()
	if err != nil || applyevidence.ProducerKeyID(public) != r.KeyID {
		return invalid
	}
	root, err := parseCertificatePEM(r.StepCARootCertificatePEM, "step-ca root")
	if err != nil || root.CheckSignatureFrom(root) != nil {
		return invalid
	}
	if r.IdentityDomain != "" && !(IdentityRuntimeAddress{Domain: r.IdentityDomain, SubdomainPrefix: r.IdentitySubdomainPrefix}).valid() &&
		r.IdentityDomain != LegacyBasementDomain {
		return invalid
	}
	if r.IdentityDomain == "" && r.IdentitySubdomainPrefix != "" {
		return invalid
	}
	if r.LANSiteAddress != "" {
		if address, err := netip.ParseAddr(r.LANSiteAddress); err != nil || address.String() != r.LANSiteAddress {
			return invalid
		}
	}
	return nil
}

func (r HomeAuthorityPublic) sameExceptTime(other HomeAuthorityPublic) bool {
	r.WrittenAt, other.WrittenAt = time.Time{}, time.Time{}
	return r == other
}

func (r HomeAuthorityPublic) ownerKey() (ed25519.PublicKey, error) {
	raw, err := base64.RawStdEncoding.Strict().DecodeString(r.OwnerPublicKey)
	if err != nil || len(raw) != ed25519.PublicKeySize {
		return nil, errors.New("localevidence: the public Owner key is malformed")
	}
	return ed25519.PublicKey(raw), nil
}

// IdentityRuntimeAddress is the address of the Authority's identity runtime
// (Pocket ID, TinyAuth, the Hub). It fails before the runtime custody exists.
func (r HomeAuthorityPublic) IdentityRuntimeAddress() (IdentityRuntimeAddress, error) {
	if r.IdentityDomain == "" {
		return IdentityRuntimeAddress{}, errors.New("localevidence: the Authority has no identity runtime address yet")
	}
	return IdentityRuntimeAddress{Domain: r.IdentityDomain, SubdomainPrefix: r.IdentitySubdomainPrefix}, nil
}

// HubHost is the Hub host (the base host of the identity runtime address).
func (r HomeAuthorityPublic) HubHost() (string, error) {
	address, err := r.IdentityRuntimeAddress()
	if err != nil {
		return "", err
	}
	return address.ServiceHost("base"), nil
}

// CARootSHA256 is the hex SHA-256 of the step-ca root certificate (DER) a
// device pins before it trusts the LAN enrollment listener.
func (r HomeAuthorityPublic) CARootSHA256() (string, error) {
	block, _ := pem.Decode([]byte(r.StepCARootCertificatePEM))
	if block == nil || block.Type != "CERTIFICATE" {
		return "", errors.New("localevidence: no established step-ca root")
	}
	sum := sha256.Sum256(block.Bytes)
	return hex.EncodeToString(sum[:]), nil
}

// VerifyHomeHumanDeviceTrust checks the Owner signature over public Home
// verifier material against this record's Owner key, without any private key.
func (r HomeAuthorityPublic) VerifyHomeHumanDeviceTrust(canonical []byte, signature OwnerPolicyStateSignature) error {
	public, err := r.ownerKey()
	if err != nil {
		return err
	}
	if !verifyPinnedOwnerSignature(homeHumanDeviceTrustDomain, canonical, signature, r.OwnerRef, r.KeyID, public) {
		return errors.New("localevidence: Home human-device trust signature does not verify against the Owner key")
	}
	return nil
}
