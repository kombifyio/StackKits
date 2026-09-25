package localevidence

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/applyevidencev2"
)

// Integrator decision 2026-09-25 (reversible): a Fleet member signs its own
// Apply and Verify evidence with a member evidence key. The key is generated on
// the member, certified by the Home owner, and limited to evidence attestation
// for the member's own Site/node/channel tuple. It grants no enrollment,
// identity signing, credential issuance, ControlAuthority, or Owner authority.
const (
	memberEvidenceKeyRelPath         = ".stackkit/custody/member-evidence-key.json"
	memberEvidenceCertificateRelPath = ".stackkit/custody/member-evidence-certificate.json"
	memberEvidenceKeyAPIVersion      = "stackkit.local-member-evidence-key/v1"

	// MemberEvidenceCapability is the only capability a member evidence key has.
	MemberEvidenceCapability = "evidence-attestation"

	// memberProducerID identifies member-signed Apply evidence receipts.
	memberProducerID = "stackkit-local-member"

	memberEvidenceKeyRequestDomain = "stackkit.member-evidence-key-request/v1\x00"
	ownerMemberEvidenceKeyDomain   = "stackkit.owner-member-evidence-key/v1\x00"
	memberApplyResultDomain        = "stackkit.member-signed-apply-result/v1\x00"
)

// ErrMemberEvidenceKeyMissing reports that this member has no evidence key.
var ErrMemberEvidenceKeyMissing = errors.New("localevidence: no member evidence key")

type memberEvidenceKeyFile struct {
	APIVersion string       `json:"apiVersion"`
	Capability string       `json:"capability"`
	StackID    string       `json:"stackId"`
	Binding    LocalBinding `json:"localBinding"`
	Seed       string       `json:"seed"`
}

// MemberEvidenceKey is a member host's evidence-attestation signing identity.
type MemberEvidenceKey struct {
	StackID string
	Binding LocalBinding
	KeyID   string
	private ed25519.PrivateKey
}

// Public returns the public half the Home owner certifies.
func (k MemberEvidenceKey) Public() ed25519.PublicKey {
	if len(k.private) != ed25519.PrivateKeySize {
		return nil
	}
	return k.private.Public().(ed25519.PublicKey)
}

// EstablishMemberEvidenceKey creates the member evidence key exactly once for
// one joined member tuple. The workspace must hold member custody and no Home
// owner key; an existing key for the same tuple is returned unchanged.
func EstablishMemberEvidenceKey(workspaceRoot, stackID string, binding LocalBinding) (MemberEvidenceKey, error) {
	if strings.TrimSpace(stackID) == "" || binding.SiteRef == "" || binding.NodeRef == "" || binding.ChannelRef == "" {
		return MemberEvidenceKey{}, errors.New("localevidence: member evidence key requires the StackInstance and member tuple")
	}
	if _, err := LoadOwnerKey(workspaceRoot); !errors.Is(err, ErrOwnerKeyMissing) {
		if err != nil {
			return MemberEvidenceKey{}, err
		}
		return MemberEvidenceKey{}, errors.New("localevidence: a workspace with Home owner signing custody holds no member evidence key")
	}
	if _, err := ReadMemberCustody(workspaceRoot); err != nil {
		return MemberEvidenceKey{}, fmt.Errorf("localevidence: member evidence key requires member custody: %w", err)
	}
	existing, err := LoadMemberEvidenceKey(workspaceRoot)
	switch {
	case err == nil:
		if existing.StackID != stackID || existing.Binding != binding {
			return MemberEvidenceKey{}, errors.New("localevidence: the member evidence key belongs to another StackInstance or member tuple")
		}
		return existing, nil
	case !errors.Is(err, ErrMemberEvidenceKeyMissing):
		return MemberEvidenceKey{}, err
	}
	seed := make([]byte, ed25519.SeedSize)
	if _, err := rand.Read(seed); err != nil {
		return MemberEvidenceKey{}, fmt.Errorf("localevidence: generate member evidence key: %w", err)
	}
	path, err := confinedCustodyPath(workspaceRoot, memberEvidenceKeyRelPath)
	if err != nil {
		return MemberEvidenceKey{}, err
	}
	if err := writePrivateJSON(path, memberEvidenceKeyFile{
		APIVersion: memberEvidenceKeyAPIVersion, Capability: MemberEvidenceCapability,
		StackID: stackID, Binding: binding, Seed: base64.RawStdEncoding.EncodeToString(seed),
	}); err != nil {
		return MemberEvidenceKey{}, fmt.Errorf("localevidence: persist member evidence key: %w", err)
	}
	return LoadMemberEvidenceKey(workspaceRoot)
}

// LoadMemberEvidenceKey reads the member evidence key. It never creates one.
func LoadMemberEvidenceKey(workspaceRoot string) (MemberEvidenceKey, error) {
	path, err := confinedCustodyPath(workspaceRoot, memberEvidenceKeyRelPath)
	if err != nil {
		return MemberEvidenceKey{}, err
	}
	raw, err := os.ReadFile(path) //nolint:gosec // fixed path below the explicit workspace
	if errors.Is(err, os.ErrNotExist) {
		return MemberEvidenceKey{}, ErrMemberEvidenceKeyMissing
	}
	if err != nil {
		return MemberEvidenceKey{}, fmt.Errorf("localevidence: read member evidence key: %w", err)
	}
	if err := requireFilePrivateToCurrentUser(path); err != nil {
		return MemberEvidenceKey{}, err
	}
	var file memberEvidenceKeyFile
	if err := json.Unmarshal(raw, &file); err != nil {
		return MemberEvidenceKey{}, fmt.Errorf("localevidence: decode member evidence key: %w", err)
	}
	if file.APIVersion != memberEvidenceKeyAPIVersion || file.Capability != MemberEvidenceCapability ||
		file.StackID == "" || file.Binding.SiteRef == "" || file.Binding.NodeRef == "" || file.Binding.ChannelRef == "" {
		return MemberEvidenceKey{}, errors.New("localevidence: member evidence key is not a recognized evidence-attestation record")
	}
	seed, err := base64.RawStdEncoding.DecodeString(file.Seed)
	if err != nil || len(seed) != ed25519.SeedSize {
		return MemberEvidenceKey{}, errors.New("localevidence: member evidence key seed is malformed")
	}
	private := ed25519.NewKeyFromSeed(seed)
	return MemberEvidenceKey{
		StackID: file.StackID, Binding: file.Binding,
		KeyID: applyevidence.ProducerKeyID(private.Public().(ed25519.PublicKey)), private: private,
	}, nil
}

// MemberEvidenceSignature is one member evidence-key signature.
type MemberEvidenceSignature struct {
	KeyID string `json:"keyId"`
	Value string `json:"value"`
}

// SignMemberEvidenceKeyRequest proves possession of the member key over the
// exact request the Foundation Node certifies.
func SignMemberEvidenceKeyRequest(key MemberEvidenceKey, canonical []byte) (MemberEvidenceSignature, error) {
	return signMember(key, memberEvidenceKeyRequestDomain, canonical)
}

// VerifyMemberEvidenceKeyRequest verifies a proof of possession.
func VerifyMemberEvidenceKeyRequest(canonical []byte, signature MemberEvidenceSignature, public ed25519.PublicKey) error {
	return verifyMember(memberEvidenceKeyRequestDomain, canonical, signature, public)
}

// SignMemberApplyResult authenticates one complete canonical Apply result
// produced on this member host.
func SignMemberApplyResult(key MemberEvidenceKey, canonical []byte) (MemberEvidenceSignature, error) {
	return signMember(key, memberApplyResultDomain, canonical)
}

// VerifyMemberApplyResult verifies a member-signed Apply result against the
// public key of a verified Home-issued certificate.
func VerifyMemberApplyResult(canonical []byte, signature MemberEvidenceSignature, public ed25519.PublicKey) error {
	return verifyMember(memberApplyResultDomain, canonical, signature, public)
}

// SignOwnerMemberEvidenceKey signs one member evidence-key certificate with the
// Home owner key under a dedicated domain.
func SignOwnerMemberEvidenceKey(workspaceRoot string, canonical []byte) (OwnerPolicyStateSignature, error) {
	value, ownerRef, keyID, err := signOwnerRestore(workspaceRoot, canonical, ownerMemberEvidenceKeyDomain, "member evidence key certificate")
	return OwnerPolicyStateSignature{OwnerRef: ownerRef, KeyID: keyID, Value: value}, err
}

// VerifyOwnerMemberEvidenceKey verifies a certificate against the public Home
// owner key a member pinned at join.
func VerifyOwnerMemberEvidenceKey(canonical []byte, signature OwnerPolicyStateSignature, ownerRef, keyID string, public ed25519.PublicKey) error {
	if !verifyPinnedOwnerSignature(ownerMemberEvidenceKeyDomain, canonical, signature, ownerRef, keyID, public) {
		return errors.New("localevidence: member evidence key certificate does not verify against the pinned Home owner key")
	}
	return nil
}

// verifyPinnedOwnerSignature checks one domain-separated Owner signature
// against a pinned public Home key whose key ID is derived from that key.
func verifyPinnedOwnerSignature(domain string, canonical []byte, signature OwnerPolicyStateSignature, ownerRef, keyID string, public ed25519.PublicKey) bool {
	value, err := base64.RawStdEncoding.Strict().DecodeString(signature.Value)
	return len(canonical) != 0 && len(public) == ed25519.PublicKeySize && err == nil &&
		strings.TrimSpace(ownerRef) != "" && signature.OwnerRef == ownerRef && signature.KeyID == keyID &&
		applyevidence.ProducerKeyID(public) == keyID &&
		ed25519.Verify(public, ownerRestoreDigest(domain, canonical), value)
}

// NewMemberCollector builds the Apply evidence collector of a member host. Its
// receipts carry the member producer identity and verify only under a trust
// anchor derived from a valid Home-issued certificate for this exact tuple.
func NewMemberCollector(key MemberEvidenceKey, version string, observers map[string]Observer, now func() time.Time) (*OwnerCollector, error) {
	if key.KeyID == "" || len(key.private) != ed25519.PrivateKeySize {
		return nil, errors.New("localevidence: member collector requires an established member evidence key")
	}
	collector, err := NewOwnerCollector(CollectorConfig{
		Key:     OwnerKey{OwnerRef: "member:" + key.Binding.NodeRef, KeyID: key.KeyID, private: key.private},
		Version: version, Observers: observers, Now: now,
	})
	if err != nil {
		return nil, err
	}
	collector.producerID = memberProducerID
	return collector, nil
}

// PersistMemberEvidenceCertificate stores the imported Home-issued certificate
// of a member host. Its integrity is re-verified by its owning package on use.
func PersistMemberEvidenceCertificate(workspaceRoot string, certificate any) error {
	if _, err := ReadMemberCustody(workspaceRoot); err != nil {
		return fmt.Errorf("localevidence: a member evidence certificate requires member custody: %w", err)
	}
	path, err := confinedCustodyPath(workspaceRoot, memberEvidenceCertificateRelPath)
	if err != nil {
		return err
	}
	return writePrivateJSON(path, certificate)
}

// ReadMemberEvidenceCertificate returns the stored certificate, or
// os.ErrNotExist when the member has not imported one.
func ReadMemberEvidenceCertificate(workspaceRoot string) ([]byte, error) {
	path, err := confinedCustodyPath(workspaceRoot, memberEvidenceCertificateRelPath)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(path) //nolint:gosec // fixed path below the explicit workspace
}

func signMember(key MemberEvidenceKey, domain string, canonical []byte) (MemberEvidenceSignature, error) {
	if len(canonical) == 0 || key.KeyID == "" || len(key.private) != ed25519.PrivateKeySize {
		return MemberEvidenceSignature{}, errors.New("localevidence: member evidence signature requires an established key and content")
	}
	return MemberEvidenceSignature{
		KeyID: key.KeyID, Value: base64.RawStdEncoding.EncodeToString(ed25519.Sign(key.private, ownerRestoreDigest(domain, canonical))),
	}, nil
}

func verifyMember(domain string, canonical []byte, signature MemberEvidenceSignature, public ed25519.PublicKey) error {
	value, err := base64.RawStdEncoding.Strict().DecodeString(signature.Value)
	if len(canonical) == 0 || len(public) != ed25519.PublicKeySize || err != nil ||
		applyevidence.ProducerKeyID(public) != signature.KeyID ||
		!ed25519.Verify(public, ownerRestoreDigest(domain, canonical), value) {
		return errors.New("localevidence: member evidence signature does not verify against the certified member key")
	}
	return nil
}
