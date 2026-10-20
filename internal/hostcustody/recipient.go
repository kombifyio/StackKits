package hostcustody

import (
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"slices"
	"time"

	stackcrypto "github.com/kombifyio/stackkits/internal/crypto"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/pkg/hostdelivery"
)

// statementMaxChallenge bounds the freshness value a delivering authority supplies.
const statementMaxChallenge = 128

// ErrDetached reports that this node was detached from host delivery and must
// be re-enrolled (a recipient rotation) before it accepts a delivery again.
var ErrDetached = errors.New("hostcustody: this node is detached from host delivery; re-enroll with a recipient rotation")

// AuthorityKey is a delivering authority's Ed25519 public key pinned by the node.
type AuthorityKey struct {
	KeyID     string `json:"keyId"`
	PublicKey string `json:"publicKey"`
	PinnedAt  string `json:"pinnedAt,omitempty"`
}

// ParseAuthorityKey validates a base64 (raw or padded) Ed25519 public key.
func ParseAuthorityKey(keyID, encoded string) (AuthorityKey, error) {
	if keyID == "" || len(keyID) > 128 {
		return AuthorityKey{}, errors.New("hostcustody: authority key ID is empty or oversized")
	}
	public, err := base64.RawStdEncoding.Strict().DecodeString(encoded)
	if err != nil {
		public, err = base64.StdEncoding.Strict().DecodeString(encoded)
	}
	if err != nil || len(public) != ed25519.PublicKeySize {
		return AuthorityKey{}, errors.New("hostcustody: authority key is not an Ed25519 public key")
	}
	return AuthorityKey{KeyID: keyID, PublicKey: base64.RawStdEncoding.EncodeToString(public)}, nil
}

func (a AuthorityKey) public() (ed25519.PublicKey, error) {
	public, err := base64.RawStdEncoding.Strict().DecodeString(a.PublicKey)
	if err != nil || len(public) != ed25519.PublicKeySize {
		return nil, errors.New("hostcustody: pinned authority key is invalid")
	}
	return public, nil
}

func (a AuthorityKey) same(other AuthorityKey) bool {
	return a.KeyID == other.KeyID && a.PublicKey == other.PublicKey
}

// Fingerprint names the authority key in statements and status.
func (a AuthorityKey) Fingerprint() string {
	public, err := a.public()
	if err != nil {
		return ""
	}
	return hostdelivery.Fingerprint(public)
}

type recipientRecord struct {
	APIVersion string                                  `json:"apiVersion"`
	Generation uint64                                  `json:"generation"`
	PublicKey  string                                  `json:"publicKey"`
	Ciphertext []byte                                  `json:"ciphertext"`
	Signature  localevidence.OwnerPolicyStateSignature `json:"signature"`
}

type retiredRecord struct {
	APIVersion string                                  `json:"apiVersion"`
	Retired    []retiredRecipient                      `json:"retired"`
	Signature  localevidence.OwnerPolicyStateSignature `json:"signature"`
}

// retiredRecipient stays openable only for a delivery issued before RetiredAt,
// and only until OpenUntil (RetiredAt plus the maximum delivery lifetime).
// Then its key is destroyed.
type retiredRecipient struct {
	Generation uint64        `json:"generation"`
	PublicKey  string        `json:"publicKey"`
	Ciphertext []byte        `json:"ciphertext"`
	Authority  *AuthorityKey `json:"authority,omitempty"`
	RetiredAt  string        `json:"retiredAt"`
	OpenUntil  string        `json:"openUntil"`
}

// deliveryState is the per-node delivery policy: the authority key pinned for
// the current generation, detachment, and the registry of delivered secrets.
// Authority and DetachedAt belong to Generation; a state left from an earlier
// generation (an interrupted rotation) carries only its registry forward.
type deliveryState struct {
	APIVersion string                                  `json:"apiVersion"`
	Generation uint64                                  `json:"generation"`
	Authority  *AuthorityKey                           `json:"authority,omitempty"`
	DetachedAt string                                  `json:"detachedAt,omitempty"`
	Delivered  []DeliveredItem                         `json:"delivered"`
	Signature  localevidence.OwnerPolicyStateSignature `json:"signature"`
}

// DeliveredItem names one secret that reached this node. It never carries the value.
type DeliveredItem struct {
	TenantRef     string `json:"tenantRef"`
	DeliveryRef   string `json:"deliveryRef"`
	ItemVersion   uint64 `json:"itemVersion"`
	Operation     string `json:"operation"`
	DeliveredAt   string `json:"deliveredAt"`
	NeedsRotation bool   `json:"needsRotation,omitempty"`
}

func readRecipient(store Store) (recipientRecord, error) {
	var record recipientRecord
	if err := store.Read(recipientName, maxRecordBytes, &record, &record.Signature); err != nil {
		return recipientRecord{}, err
	}
	if record.APIVersion != recipientAPIVersion || record.Generation == 0 || record.PublicKey == "" || len(record.Ciphertext) == 0 {
		return recipientRecord{}, errors.New("hostcustody: recipient record is invalid")
	}
	return record, nil
}

func readRetired(store Store, current uint64) (retiredRecord, bool, error) {
	record := retiredRecord{APIVersion: retiredAPIVersion}
	if err := store.Read(retiredName, maxRecordBytes, &record, &record.Signature); errors.Is(err, ErrMissing) {
		return retiredRecord{APIVersion: retiredAPIVersion}, false, nil
	} else if err != nil {
		return retiredRecord{}, false, err
	}
	if record.APIVersion != retiredAPIVersion {
		return retiredRecord{}, false, errors.New("hostcustody: retired recipient record is invalid")
	}
	// An entry for the current generation or later is a rotation that never
	// completed; the current record still holds that key.
	record.Retired = slices.DeleteFunc(record.Retired, func(entry retiredRecipient) bool { return entry.Generation >= current })
	return record, true, nil
}

func readState(store Store, current uint64) (deliveryState, bool, error) {
	state := deliveryState{APIVersion: stateAPIVersion, Generation: current}
	if err := store.Read(stateName, maxRecordBytes, &state, &state.Signature); errors.Is(err, ErrMissing) {
		return deliveryState{APIVersion: stateAPIVersion, Generation: current}, false, nil
	} else if err != nil {
		return deliveryState{}, false, err
	}
	switch {
	case state.APIVersion != stateAPIVersion || state.Generation == 0:
		return deliveryState{}, false, errors.New("hostcustody: delivery state is invalid")
	case state.Generation > current:
		return deliveryState{}, false, errors.New("hostcustody: delivery state belongs to a later recipient generation than the recipient record")
	case state.Generation < current:
		state = deliveryState{APIVersion: stateAPIVersion, Generation: current, Delivered: state.Delivered}
	}
	return state, true, nil
}

func writeState(store Store, state deliveryState, existing bool) error {
	if state.Delivered == nil {
		state.Delivered = []DeliveredItem{}
	}
	return store.Write(stateName, &state, &state.Signature, !existing)
}

func writeRetired(store Store, record retiredRecord, existing bool) error {
	if record.Retired == nil {
		record.Retired = []retiredRecipient{}
	}
	return store.Write(retiredName, &record, &record.Signature, !existing)
}

func newRecipientKey(store Store, generation uint64) (recipientRecord, error) {
	wrapping, err := store.WrappingKey()
	if err != nil {
		return recipientRecord{}, err
	}
	defer clearBytes(wrapping)
	private, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return recipientRecord{}, errors.New("hostcustody: generate recipient key")
	}
	seed := private.Bytes()
	defer clearBytes(seed)
	ciphertext, err := stackcrypto.EncryptWithPassphrase(seed, string(wrapping))
	if err != nil {
		return recipientRecord{}, errors.New("hostcustody: encrypt recipient key")
	}
	return recipientRecord{APIVersion: recipientAPIVersion, Generation: generation, PublicKey: base64.RawStdEncoding.EncodeToString(private.PublicKey().Bytes()), Ciphertext: ciphertext}, nil
}

// EstablishRecipient creates the node's encryption recipient key (generation
// 1) once. The private half is encrypted under the local custody key and never
// leaves this node; no signing key is ever used to decrypt.
func EstablishRecipient(store Store) error {
	if _, err := readRecipient(store); err == nil {
		return nil
	} else if !errors.Is(err, ErrMissing) {
		return err
	}
	record, err := newRecipientKey(store, 1)
	if err != nil {
		return err
	}
	return store.Write(recipientName, &record, &record.Signature, true)
}

// PruneRetired destroys every retired recipient key whose window has closed.
func PruneRetired(store Store, now time.Time) error {
	current, err := readRecipient(store)
	if err != nil {
		return err
	}
	return pruneRetired(store, current.Generation, now)
}

func pruneRetired(store Store, current uint64, now time.Time) error {
	retired, existing, err := readRetired(store, current)
	if err != nil || !existing {
		return err
	}
	before := len(retired.Retired)
	retired.Retired = slices.DeleteFunc(retired.Retired, func(entry retiredRecipient) bool { return !retiredOpen(entry, now) })
	if len(retired.Retired) == before {
		return nil
	}
	if len(retired.Retired) == 0 {
		return store.Remove(retiredName)
	}
	return writeRetired(store, retired, true)
}

func retiredOpen(entry retiredRecipient, now time.Time) bool {
	until, err := time.Parse(time.RFC3339Nano, entry.OpenUntil)
	return err == nil && now.Before(until)
}

// RotateRecipient re-enrolls the node under recipient generation n+1. The
// previous key stays openable for deliveries issued before this moment, for
// at most the maximum delivery lifetime, and is then destroyed; a detached
// node destroys it at once. A rotation ends detachment. A non-nil authority
// replaces the pinned delivering authority key for the new generation;
// otherwise the current pin carries over. It returns the new generation.
func RotateRecipient(store Store, authority *AuthorityKey, now time.Time) (uint64, error) {
	if err := EstablishRecipient(store); err != nil {
		return 0, err
	}
	current, err := readRecipient(store)
	if err != nil {
		return 0, err
	}
	state, stateExisting, err := readState(store, current.Generation)
	if err != nil {
		return 0, err
	}
	retired, retiredExisting, err := readRetired(store, current.Generation)
	if err != nil {
		return 0, err
	}
	retired.Retired = slices.DeleteFunc(retired.Retired, func(entry retiredRecipient) bool { return !retiredOpen(entry, now) })
	if state.DetachedAt != "" {
		retired.Retired = nil
	} else {
		retired.Retired = append(retired.Retired, retiredRecipient{
			Generation: current.Generation, PublicKey: current.PublicKey, Ciphertext: current.Ciphertext, Authority: state.Authority,
			RetiredAt: now.UTC().Format(time.RFC3339Nano), OpenUntil: now.UTC().Add(hostdelivery.MaxLifetime).Format(time.RFC3339Nano),
		})
	}
	next, err := newRecipientKey(store, current.Generation+1)
	if err != nil {
		return 0, err
	}
	// The retired key is kept before the current record is replaced, so a
	// crash between the writes never loses a key an in-flight delivery needs.
	if len(retired.Retired) > 0 || retiredExisting {
		if err := writeRetired(store, retired, retiredExisting); err != nil {
			return 0, err
		}
	}
	if err := store.Write(recipientName, &next, &next.Signature, false); err != nil {
		return 0, err
	}
	pinned := state.Authority
	if authority != nil {
		pin := *authority
		pin.PinnedAt = now.UTC().Format(time.RFC3339Nano)
		pinned = &pin
	}
	nextState := deliveryState{APIVersion: stateAPIVersion, Generation: next.Generation, Authority: pinned, Delivered: state.Delivered}
	if err := writeState(store, nextState, stateExisting); err != nil {
		return 0, err
	}
	return next.Generation, nil
}

// PinAuthority pins the delivering authority key for the current generation.
// Pinning the same key again changes nothing; a different key is refused, and
// only a recipient rotation (re-enrollment) can replace it.
func PinAuthority(store Store, authority AuthorityKey, now time.Time) error {
	if _, err := authority.public(); err != nil || authority.KeyID == "" {
		return errors.New("hostcustody: authority key is invalid")
	}
	if err := EstablishRecipient(store); err != nil {
		return err
	}
	current, err := readRecipient(store)
	if err != nil {
		return err
	}
	state, existing, err := readState(store, current.Generation)
	if err != nil {
		return err
	}
	if state.DetachedAt != "" {
		return ErrDetached
	}
	if state.Authority != nil {
		if state.Authority.same(authority) {
			return nil
		}
		return errors.New("hostcustody: a different delivering authority key is pinned for this recipient generation; re-enroll with a recipient rotation to replace it")
	}
	authority.PinnedAt = now.UTC().Format(time.RFC3339Nano)
	state.Authority = &authority
	return writeState(store, state, existing)
}

// RecipientStatement proves the current recipient key belongs to this node's
// owner, names its generation and pinned authority, and answers the delivering
// authority's challenge. A detached node issues no statement.
func RecipientStatement(store Store, challenge string, now time.Time) (hostdelivery.Statement, error) {
	if challenge == "" || len(challenge) > statementMaxChallenge {
		return hostdelivery.Statement{}, errors.New("hostcustody: recipient challenge is empty or oversized")
	}
	if err := EstablishRecipient(store); err != nil {
		return hostdelivery.Statement{}, err
	}
	record, err := readRecipient(store)
	if err != nil {
		return hostdelivery.Statement{}, err
	}
	if err := pruneRetired(store, record.Generation, now); err != nil {
		return hostdelivery.Statement{}, err
	}
	state, _, err := readState(store, record.Generation)
	if err != nil {
		return hostdelivery.Statement{}, err
	}
	if state.DetachedAt != "" {
		return hostdelivery.Statement{}, ErrDetached
	}
	workspace := store.Workspace()
	owner, err := localevidence.LoadOwnerCustody(workspace)
	if err != nil {
		return hostdelivery.Statement{}, err
	}
	key, err := localevidence.LoadOwnerKey(workspace)
	if err != nil {
		return hostdelivery.Statement{}, err
	}
	public, err := base64.RawStdEncoding.DecodeString(record.PublicKey)
	if err != nil {
		return hostdelivery.Statement{}, errors.New("hostcustody: recipient record is invalid")
	}
	statement := hostdelivery.Statement{
		APIVersion: hostdelivery.StatementAPIVersion, SiteRef: owner.Binding.SiteRef, NodeRef: hostdelivery.NodeIdentityRef(owner.Binding.NodeRef, key.Public()),
		RecipientKey: record.PublicKey, Fingerprint: hostdelivery.Fingerprint(public), Generation: record.Generation,
		Challenge: challenge, IssuedAt: now.UTC().Format(time.RFC3339Nano),
		OwnerKeyID: owner.KeyID, OwnerPublic: base64.RawStdEncoding.EncodeToString(key.Public()),
	}
	if state.Authority != nil {
		statement.AuthorityKeyID, statement.AuthorityFingerprint = state.Authority.KeyID, state.Authority.Fingerprint()
	}
	payload, err := statement.SigningBytes()
	if err != nil {
		return hostdelivery.Statement{}, err
	}
	signature, err := localevidence.SignOwnerPolicyState(workspace, payload)
	if err != nil {
		return hostdelivery.Statement{}, err
	}
	statement.Signature = hostdelivery.Signature{OwnerRef: signature.OwnerRef, KeyID: signature.KeyID, Value: signature.Value}
	return statement, nil
}

// RetiredGeneration is one previous recipient generation still openable.
type RetiredGeneration struct {
	Generation uint64 `json:"generation"`
	RetiredAt  string `json:"retiredAt"`
	OpenUntil  string `json:"openUntil"`
}

// Status is the secret-free state of this node's host delivery custody.
type Status struct {
	Generation           uint64              `json:"generation"`
	Fingerprint          string              `json:"fingerprint"`
	AuthorityKeyID       string              `json:"authorityKeyId,omitempty"`
	AuthorityFingerprint string              `json:"authorityFingerprint,omitempty"`
	Detached             bool                `json:"detached"`
	DetachedAt           string              `json:"detachedAt,omitempty"`
	Retired              []RetiredGeneration `json:"retired"`
	Delivered            []DeliveredItem     `json:"delivered"`
}

// ReadStatus reports the recipient generation, retired generations still
// openable, the pinned authority, detachment and every delivered secret by
// reference. It changes nothing; a node without a recipient is ErrMissing.
func ReadStatus(store Store, now time.Time) (Status, error) {
	record, err := readRecipient(store)
	if err != nil {
		return Status{}, err
	}
	public, err := base64.RawStdEncoding.DecodeString(record.PublicKey)
	if err != nil {
		return Status{}, errors.New("hostcustody: recipient record is invalid")
	}
	state, _, err := readState(store, record.Generation)
	if err != nil {
		return Status{}, err
	}
	retired, _, err := readRetired(store, record.Generation)
	if err != nil {
		return Status{}, err
	}
	status := Status{
		Generation: record.Generation, Fingerprint: hostdelivery.Fingerprint(public), Detached: state.DetachedAt != "", DetachedAt: state.DetachedAt,
		Retired: []RetiredGeneration{}, Delivered: append([]DeliveredItem{}, state.Delivered...),
	}
	if state.Authority != nil {
		status.AuthorityKeyID, status.AuthorityFingerprint = state.Authority.KeyID, state.Authority.Fingerprint()
	}
	for _, entry := range retired.Retired {
		if retiredOpen(entry, now) && state.DetachedAt == "" {
			status.Retired = append(status.Retired, RetiredGeneration{Generation: entry.Generation, RetiredAt: entry.RetiredAt, OpenUntil: entry.OpenUntil})
		}
	}
	return status, nil
}
