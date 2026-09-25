package fleetmember

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/applyevidencev2"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

// Integrator decision 2026-09-25 (reversible): a member signs its own Apply
// and Verify evidence with a member evidence key. The member generates the key
// at join and sends one request file to the Foundation Node; the Home owner
// returns one signed certificate file. Verifiers accept member evidence only
// under a valid certificate for the exact StackInstance, plan, and member
// Site/node/channel tuple. The certificate grants no enrollment, identity
// signing, credential issuance, ControlAuthority, or Owner authority.
const (
	EvidenceKeyRequestAPIVersion = "stackkit.member-evidence-key-request/v1"
	EvidenceKeyRequestKind       = "MemberEvidenceKeyRequest"
	EvidenceKeyAPIVersion        = "stackkit.member-evidence-key/v1"
	EvidenceKeyKind              = "MemberEvidenceKeyCertificate"

	// DefaultEvidenceKeyValidity and MaxEvidenceKeyValidity bound a
	// certificate. An expired certificate stops member Apply and Verify until
	// the Home owner certifies the key again.
	DefaultEvidenceKeyValidity = 30 * 24 * time.Hour
	MaxEvidenceKeyValidity     = 90 * 24 * time.Hour
	// MaxEvidenceKeyBytes bounds a request or certificate document.
	MaxEvidenceKeyBytes = 64 << 10

	// IssuedEvidenceKeysRoot is where the Foundation Node records every
	// certificate it issued. A record makes the Foundation Node leave that
	// member's local runtime targets to the member.
	IssuedEvidenceKeysRoot = ".stackkit/fleet/member-evidence-keys"
)

// EvidenceKey is the public member evidence key.
type EvidenceKey struct {
	KeyID     string `json:"keyId"`
	PublicKey string `json:"publicKey"`
}

// EvidenceKeyRequest travels from the member to the Foundation Node.
type EvidenceKeyRequest struct {
	APIVersion      string                                `json:"apiVersion"`
	Kind            string                                `json:"kind"`
	StackID         string                                `json:"stackId"`
	PlanHash        string                                `json:"planHash"`
	AdmissionDigest string                                `json:"admissionDigest"`
	Authority       ExecutionBinding                      `json:"authority"`
	Member          ExecutionBinding                      `json:"member"`
	HomeKeyID       string                                `json:"homeKeyId"`
	Capability      string                                `json:"capability"`
	Key             EvidenceKey                           `json:"key"`
	RequestedAt     time.Time                             `json:"requestedAt"`
	Proof           localevidence.MemberEvidenceSignature `json:"proof"`
}

// SigningBytes are the canonical bytes covered by the proof of possession.
func (r EvidenceKeyRequest) SigningBytes() ([]byte, error) {
	r.Proof = localevidence.MemberEvidenceSignature{}
	return resolvedplan.CanonicalJSON(r)
}

// EvidenceKeyCertificate travels from the Foundation Node to the member.
type EvidenceKeyCertificate struct {
	APIVersion      string                                  `json:"apiVersion"`
	Kind            string                                  `json:"kind"`
	StackID         string                                  `json:"stackId"`
	PlanHash        string                                  `json:"planHash"`
	AdmissionDigest string                                  `json:"admissionDigest"`
	Authority       ExecutionBinding                        `json:"authority"`
	Member          ExecutionBinding                        `json:"member"`
	Capability      string                                  `json:"capability"`
	Grants          Grants                                  `json:"grants"`
	Key             EvidenceKey                             `json:"key"`
	Issuer          HomeVerifier                            `json:"issuer"`
	IssuedAt        time.Time                               `json:"issuedAt"`
	ValidUntil      time.Time                               `json:"validUntil"`
	Signature       localevidence.OwnerPolicyStateSignature `json:"signature"`
}

// SigningBytes are the canonical bytes covered by the Home owner signature.
func (c EvidenceKeyCertificate) SigningBytes() ([]byte, error) {
	c.Signature = localevidence.OwnerPolicyStateSignature{}
	return resolvedplan.CanonicalJSON(c)
}

// NewEvidenceKeyRequest builds the proof-of-possession request for the member
// evidence key of one joined member.
func NewEvidenceKeyRequest(custody Custody, key localevidence.MemberEvidenceKey, now time.Time) (EvidenceKeyRequest, error) {
	if key.StackID != custody.StackID || key.Binding != custody.Binding {
		return EvidenceKeyRequest{}, errors.New("member evidence key does not belong to this member custody")
	}
	request := EvidenceKeyRequest{
		APIVersion: EvidenceKeyRequestAPIVersion, Kind: EvidenceKeyRequestKind,
		StackID: custody.StackID, PlanHash: custody.PlanHash, AdmissionDigest: custody.AdmissionDigest,
		Authority: custody.Admission.Authority, Member: custody.Admission.Member,
		HomeKeyID: custody.Verifier.KeyID, Capability: localevidence.MemberEvidenceCapability,
		Key:         EvidenceKey{KeyID: key.KeyID, PublicKey: base64.RawStdEncoding.EncodeToString(key.Public())},
		RequestedAt: now.UTC().Truncate(time.Second),
	}
	signing, err := request.SigningBytes()
	if err != nil {
		return EvidenceKeyRequest{}, err
	}
	request.Proof, err = localevidence.SignMemberEvidenceKeyRequest(key, signing)
	return request, err
}

// DecodeEvidenceKeyRequest strictly decodes a request and verifies its proof
// of possession.
func DecodeEvidenceKeyRequest(raw []byte) (EvidenceKeyRequest, error) {
	var request EvidenceKeyRequest
	if err := decodeStrictBounded(raw, &request, "member evidence key request"); err != nil {
		return EvidenceKeyRequest{}, err
	}
	if request.APIVersion != EvidenceKeyRequestAPIVersion || request.Kind != EvidenceKeyRequestKind ||
		request.Capability != localevidence.MemberEvidenceCapability {
		return EvidenceKeyRequest{}, errors.New("member evidence key request has an unsupported contract identity or capability")
	}
	public, err := decodeEvidenceKey(request.Key)
	if err != nil {
		return EvidenceKeyRequest{}, err
	}
	signing, err := request.SigningBytes()
	if err != nil {
		return EvidenceKeyRequest{}, err
	}
	if request.Proof.KeyID != request.Key.KeyID {
		return EvidenceKeyRequest{}, errors.New("member evidence key request proof names another key")
	}
	if err := localevidence.VerifyMemberEvidenceKeyRequest(signing, request.Proof, public); err != nil {
		return EvidenceKeyRequest{}, fmt.Errorf("member evidence key request proof of possession: %w", err)
	}
	return request, nil
}

// CertifyRequest is the Foundation Node input for one certificate.
type CertifyRequest struct {
	Request   []byte
	Plan      resolvedplan.ResolvedPlan
	Authority localevidence.LocalBinding
	OwnerRef  string
	KeyID     string
	PublicKey ed25519.PublicKey
	Now       time.Time
	ValidFor  time.Duration
}

// Certify derives the unsigned certificate. The member tuple must be the one
// the current plan declares under the same admission rules as a join, and the
// member must hold exactly the current plan. The caller signs SigningBytes
// with the Home owner key.
func Certify(input CertifyRequest) (EvidenceKeyCertificate, error) {
	if input.ValidFor <= 0 || input.ValidFor > MaxEvidenceKeyValidity {
		return EvidenceKeyCertificate{}, fmt.Errorf("member evidence key validity must be within (0, %s]", MaxEvidenceKeyValidity)
	}
	if len(input.PublicKey) != ed25519.PublicKeySize || applyevidence.ProducerKeyID(input.PublicKey) != input.KeyID ||
		strings.TrimSpace(input.OwnerRef) == "" {
		return EvidenceKeyCertificate{}, errors.New("member evidence key certification requires the exact Home owner verification key")
	}
	request, err := DecodeEvidenceKeyRequest(input.Request)
	if err != nil {
		return EvidenceKeyCertificate{}, err
	}
	if request.HomeKeyID != input.KeyID {
		return EvidenceKeyCertificate{}, errors.New("member evidence key request pins another Home owner key")
	}
	derived, err := derive(input.Plan, input.Authority, request.Member.NodeRef)
	if err != nil {
		return EvidenceKeyCertificate{}, err
	}
	if derived.member != request.Member || derived.authority != request.Authority || derived.stackID != request.StackID {
		return EvidenceKeyCertificate{}, errors.New("member evidence key request names a tuple the current plan does not admit")
	}
	if derived.planHash != request.PlanHash {
		return EvidenceKeyCertificate{}, fmt.Errorf(
			"member joined plan %s but the Foundation Node plan is %s; admit and join the member again before certifying its key",
			request.PlanHash, derived.planHash,
		)
	}
	issuedAt := input.Now.UTC().Truncate(time.Second)
	return EvidenceKeyCertificate{
		APIVersion: EvidenceKeyAPIVersion, Kind: EvidenceKeyKind,
		StackID: request.StackID, PlanHash: request.PlanHash, AdmissionDigest: request.AdmissionDigest,
		Authority: request.Authority, Member: request.Member,
		Capability: localevidence.MemberEvidenceCapability, Grants: Grants{}, Key: request.Key,
		Issuer: HomeVerifier{
			OwnerRef: input.OwnerRef, KeyID: input.KeyID,
			PublicKey: base64.RawStdEncoding.EncodeToString(input.PublicKey),
		},
		IssuedAt: issuedAt, ValidUntil: issuedAt.Add(input.ValidFor),
	}, nil
}

// EvidenceKeyExpectation is what a verifier requires of a certificate.
type EvidenceKeyExpectation struct {
	StackID         string
	PlanHash        string
	AdmissionDigest string
	Member          localevidence.LocalBinding
	Issuer          HomeVerifier
}

// VerifyEvidenceKeyCertificate is the single verification helper for member
// evidence. It accepts a certificate only when the pinned Home owner key signed
// it for the exact StackInstance, plan, admission, and member tuple, with only
// the evidence-attestation capability, and when at lies inside its validity.
func VerifyEvidenceKeyCertificate(raw []byte, expect EvidenceKeyExpectation, at time.Time) (EvidenceKeyCertificate, ed25519.PublicKey, error) {
	certificate, public, err := verifyEvidenceKeyCertificateIntegrity(raw, expect.Issuer)
	if err != nil {
		return EvidenceKeyCertificate{}, nil, err
	}
	if certificate.StackID != expect.StackID || certificate.PlanHash != expect.PlanHash ||
		certificate.AdmissionDigest != expect.AdmissionDigest || certificate.Member.LocalBinding() != expect.Member {
		return EvidenceKeyCertificate{}, nil, errors.New("member evidence key certificate is bound to another StackInstance, plan, or member tuple")
	}
	at = at.UTC()
	if at.IsZero() || at.Add(admissionClockSkew).Before(certificate.IssuedAt) || !at.Before(certificate.ValidUntil) {
		return EvidenceKeyCertificate{}, nil, errors.New("member evidence key certificate is expired or not yet valid")
	}
	return certificate, public, nil
}

func verifyEvidenceKeyCertificateIntegrity(raw []byte, issuer HomeVerifier) (EvidenceKeyCertificate, ed25519.PublicKey, error) {
	var certificate EvidenceKeyCertificate
	if err := decodeStrictBounded(raw, &certificate, "member evidence key certificate"); err != nil {
		return EvidenceKeyCertificate{}, nil, err
	}
	if certificate.APIVersion != EvidenceKeyAPIVersion || certificate.Kind != EvidenceKeyKind ||
		certificate.Capability != localevidence.MemberEvidenceCapability || certificate.Grants != (Grants{}) {
		return EvidenceKeyCertificate{}, nil, errors.New("member evidence key certificate widens evidence attestation or has an unsupported contract identity")
	}
	if certificate.Issuer != issuer {
		return EvidenceKeyCertificate{}, nil, errors.New("member evidence key certificate is not issued by the pinned Home owner key")
	}
	issuerKey, err := base64.RawStdEncoding.Strict().DecodeString(issuer.PublicKey)
	if err != nil {
		return EvidenceKeyCertificate{}, nil, errors.New("member evidence key certificate issuer key is malformed")
	}
	signing, err := certificate.SigningBytes()
	if err != nil {
		return EvidenceKeyCertificate{}, nil, err
	}
	if err := localevidence.VerifyOwnerMemberEvidenceKey(
		signing, certificate.Signature, issuer.OwnerRef, issuer.KeyID, ed25519.PublicKey(issuerKey),
	); err != nil {
		return EvidenceKeyCertificate{}, nil, err
	}
	if certificate.IssuedAt.Location() != time.UTC || certificate.ValidUntil.Location() != time.UTC ||
		!certificate.ValidUntil.After(certificate.IssuedAt) || certificate.ValidUntil.Sub(certificate.IssuedAt) > MaxEvidenceKeyValidity {
		return EvidenceKeyCertificate{}, nil, errors.New("member evidence key certificate validity is outside its bound")
	}
	public, err := decodeEvidenceKey(certificate.Key)
	if err != nil {
		return EvidenceKeyCertificate{}, nil, err
	}
	return certificate, public, nil
}

// MemberEvidence is the verified evidence-attestation custody of a member.
type MemberEvidence struct {
	Custody     Custody
	Key         localevidence.MemberEvidenceKey
	Certificate EvidenceKeyCertificate
	Public      ed25519.PublicKey
}

// ImportEvidenceKeyCertificate verifies a Home-issued certificate against the
// member custody and the member's own key and stores it.
func ImportEvidenceKeyCertificate(workspaceRoot string, raw []byte, now time.Time) (MemberEvidence, error) {
	evidence, err := verifyMemberEvidence(workspaceRoot, raw, now)
	if err != nil {
		return MemberEvidence{}, err
	}
	if err := localevidence.PersistMemberEvidenceCertificate(workspaceRoot, evidence.Certificate); err != nil {
		return MemberEvidence{}, err
	}
	return evidence, nil
}

// LoadMemberEvidence returns the verified evidence custody of a member host at
// now. It fails closed when the certificate is missing, expired, or bound to
// another tuple or plan.
func LoadMemberEvidence(workspaceRoot string, now time.Time) (MemberEvidence, error) {
	raw, err := localevidence.ReadMemberEvidenceCertificate(workspaceRoot)
	if errors.Is(err, os.ErrNotExist) {
		return MemberEvidence{}, errors.New(
			"this member has no Home-certified evidence key; relay the member evidence key request to the Foundation Node, run `stackkit fleet certify-member-key` there, and import the certificate with `stackkit fleet import-member-key`",
		)
	}
	if err != nil {
		return MemberEvidence{}, fmt.Errorf("read member evidence key certificate: %w", err)
	}
	return verifyMemberEvidence(workspaceRoot, raw, now)
}

func verifyMemberEvidence(workspaceRoot string, raw []byte, now time.Time) (MemberEvidence, error) {
	custody, err := LoadCustody(workspaceRoot)
	if err != nil {
		return MemberEvidence{}, err
	}
	key, err := localevidence.LoadMemberEvidenceKey(workspaceRoot)
	if err != nil {
		return MemberEvidence{}, err
	}
	certificate, public, err := VerifyEvidenceKeyCertificate(raw, EvidenceKeyExpectation{
		StackID: custody.StackID, PlanHash: custody.PlanHash, AdmissionDigest: custody.AdmissionDigest,
		Member: custody.Binding, Issuer: custody.Verifier,
	}, now)
	if err != nil {
		return MemberEvidence{}, err
	}
	if certificate.Key.KeyID != key.KeyID || !bytes.Equal(public, key.Public()) {
		return MemberEvidence{}, errors.New("member evidence key certificate certifies another key than this member holds")
	}
	return MemberEvidence{Custody: custody, Key: key, Certificate: certificate, Public: public}, nil
}

// CertifiedMember is one member tuple the Foundation Node certified.
type CertifiedMember struct {
	StackID string
	Member  ExecutionBinding
}

// CertifiedMembers returns the member tuples the Foundation Node certified,
// from its own issued records, sorted by StackInstance, Site, and node. Expiry
// does not matter here: a certified member stays the executor of its own
// targets, and its evidence is rejected until the key is certified again.
func CertifiedMembers(workspaceRoot string, issuer HomeVerifier) ([]CertifiedMember, error) {
	directory := filepath.Join(workspaceRoot, filepath.FromSlash(IssuedEvidenceKeysRoot))
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read issued member evidence keys: %w", err)
	}
	seen := map[CertifiedMember]struct{}{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		raw, err := readBounded(filepath.Join(directory, entry.Name()))
		if err != nil {
			return nil, err
		}
		certificate, _, err := verifyEvidenceKeyCertificateIntegrity(raw, issuer)
		if err != nil {
			return nil, fmt.Errorf("issued member evidence key %s: %w", entry.Name(), err)
		}
		seen[CertifiedMember{StackID: certificate.StackID, Member: certificate.Member}] = struct{}{}
	}
	members := make([]CertifiedMember, 0, len(seen))
	for member := range seen {
		members = append(members, member)
	}
	sort.Slice(members, func(i, j int) bool {
		left, right := members[i], members[j]
		if left.StackID != right.StackID {
			return left.StackID < right.StackID
		}
		if left.Member.SiteRef != right.Member.SiteRef {
			return left.Member.SiteRef < right.Member.SiteRef
		}
		return left.Member.NodeRef < right.Member.NodeRef
	})
	return members, nil
}

// ReadBounded reads one operator-selected request or certificate file.
func ReadBounded(path string) ([]byte, error) {
	return readBounded(path)
}

func readBounded(path string) ([]byte, error) {
	file, err := os.Open(path) //nolint:gosec // operator-selected or fixed record, read only and bounded
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	raw, err := io.ReadAll(io.LimitReader(file, MaxEvidenceKeyBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 || len(raw) > MaxEvidenceKeyBytes {
		return nil, fmt.Errorf("%s is empty or exceeds %d bytes", filepath.Base(path), MaxEvidenceKeyBytes)
	}
	return raw, nil
}

func decodeEvidenceKey(key EvidenceKey) (ed25519.PublicKey, error) {
	public, err := base64.RawStdEncoding.Strict().DecodeString(key.PublicKey)
	if err != nil || len(public) != ed25519.PublicKeySize || applyevidence.ProducerKeyID(public) != key.KeyID {
		return nil, errors.New("member evidence key is malformed or its key ID is not derived from it")
	}
	return ed25519.PublicKey(public), nil
}

func decodeStrictBounded(raw []byte, destination any, label string) error {
	if len(raw) == 0 || len(raw) > MaxEvidenceKeyBytes {
		return fmt.Errorf("%s is empty or exceeds its bound", label)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("decode %s: %w", label, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%s has trailing input", label)
	}
	return nil
}
