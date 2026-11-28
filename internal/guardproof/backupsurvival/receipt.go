package backupsurvival

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kombifyio/stackkits/contracts/canonicaljson"
)

// ReceiptVersion identifies the receipt wire shape.
const ReceiptVersion = "stackkit.guard-backup-survival-receipt/v1"

const (
	genesisDomain   = "backupsurvival/genesis/v1\x00"
	entryDomain     = "backupsurvival/entry/v1\x00"
	receiptDomain   = "backupsurvival/receipt/v1\x00"
	signatureDomain = "stackkit.guard-backup-survival-receipt/v1\x00"
)

// Verdict is the whole proof's outcome. There is no partial pass.
type Verdict string

// Verdicts.
const (
	VerdictSurvives Verdict = "survives"
	VerdictFailed   Verdict = "failed"
)

// Step outcomes.
const (
	OutcomeOK        = "ok"
	OutcomeDenied    = "denied"
	OutcomeAbsorbed  = "absorbed"
	OutcomeBreached  = "breached"
	OutcomeFailed    = "failed"
	OutcomeWarn      = "warn"
	OutcomeSkipped   = "skipped"
	StepBinding      = "binding"
	StepCommit       = "commit-canary"
	StepRetention    = "verify-retention"
	StepAttackPrefix = "attack:"
	StepSurvival     = "verify-survival"
	StepRestore      = "clean-room-restore"
	StepPrune        = "maintenance-prune"
	StepCapacity     = "capacity"
)

// RequiredNotClaimed is the closed set of bounds every receipt must state. A
// receipt missing one is invalid, so a verifier can never read silence as a
// stronger claim.
var RequiredNotClaimed = []string{
	"backup-admin-compromise: whoever holds the maintenance or bypass authority can delete retained points; this proof does not protect against that identity",
	"provider-account-compromise: the cloud account owner and provider staff are outside the proof",
	"real-recovery-points: no deletion was attempted against any real retained recovery point",
	"lock-renewal: retention was verified at proof time only; renewal before expiry is not proven",
	"application-recovery: restored bytes are verified, application behaviour on them is not",
	"live-target: the proof ran against the bound backend only, no other target or provider is covered",
}

// Entry is one hash-chained step.
type Entry struct {
	Seq      int       `json:"seq"`
	Step     string    `json:"step"`
	Outcome  string    `json:"outcome"`
	Detail   string    `json:"detail"`
	At       time.Time `json:"at"`
	PrevHash string    `json:"prevHash"`
	Hash     string    `json:"hash"`
}

// Signature is the Ed25519 signature over the canonical sealed receipt.
type Signature struct {
	KeyID string `json:"keyId"`
	Value string `json:"value"`
}

// Receipt is the proof's sealed result. It holds identity IDs and credential
// fingerprints, never credential values.
type Receipt struct {
	APIVersion    string     `json:"apiVersion"`
	BindingDigest string     `json:"bindingDigest"`
	BackendRef    string     `json:"backendRef"`
	NodeID        string     `json:"nodeId,omitempty"`
	WorkloadID    string     `json:"workloadId,omitempty"`
	Canary        ObjectRef  `json:"canary"`
	Verdict       Verdict    `json:"verdict"`
	Genesis       string     `json:"genesis"`
	Entries       []Entry    `json:"entries"`
	HeadHash      string     `json:"headHash"`
	NotClaimed    []string   `json:"notClaimed"`
	CompletedAt   time.Time  `json:"completedAt"`
	ReceiptDigest string     `json:"receiptDigest"`
	Signature     *Signature `json:"signature,omitempty"`
}

// KeyIDOf derives the canonical ed25519://sha256/<hex> identifier of an
// Ed25519 public key, matching applyevidence.ProducerKeyID and localevidence.
func KeyIDOf(publicKey ed25519.PublicKey) string {
	sum := sha256.Sum256(publicKey)
	return "ed25519://sha256/" + hex.EncodeToString(sum[:])
}

type receiptGenesisBinding struct {
	APIVersion    string `json:"apiVersion"`
	BindingDigest string `json:"bindingDigest"`
	BackendRef    string `json:"backendRef"`
	NodeID        string `json:"nodeId,omitempty"`
	WorkloadID    string `json:"workloadId,omitempty"`
}

type receiptSealBody struct {
	APIVersion    string    `json:"apiVersion"`
	BindingDigest string    `json:"bindingDigest"`
	BackendRef    string    `json:"backendRef"`
	NodeID        string    `json:"nodeId,omitempty"`
	WorkloadID    string    `json:"workloadId,omitempty"`
	Canary        ObjectRef `json:"canary"`
	Verdict       Verdict   `json:"verdict"`
	Genesis       string    `json:"genesis"`
	Entries       []Entry   `json:"entries"`
	HeadHash      string    `json:"headHash"`
	NotClaimed    []string  `json:"notClaimed"`
	CompletedAt   time.Time `json:"completedAt"`
}

func genesisFor(r Receipt) (string, error) {
	raw, err := canonicaljson.CanonicalJSON(receiptGenesisBinding{
		APIVersion:    r.APIVersion,
		BindingDigest: r.BindingDigest,
		BackendRef:    r.BackendRef,
		NodeID:        r.NodeID,
		WorkloadID:    r.WorkloadID,
	})
	if err != nil {
		return "", err
	}
	return canonicaljson.SHA256Hex([]byte(genesisDomain), raw), nil
}

func entryHash(e Entry) (string, error) {
	e.Hash = ""
	raw, err := canonicaljson.CanonicalJSON(e)
	if err != nil {
		return "", err
	}
	return canonicaljson.SHA256Hex([]byte(entryDomain), []byte(e.PrevHash), []byte{0}, raw), nil
}

// CanonicalSigningBytes returns the canonical JSON bytes covered by
// ReceiptDigest and the Ed25519 signature.
func (r Receipt) CanonicalSigningBytes() ([]byte, error) {
	body := receiptSealBody{
		APIVersion:    r.APIVersion,
		BindingDigest: r.BindingDigest,
		BackendRef:    r.BackendRef,
		NodeID:        r.NodeID,
		WorkloadID:    r.WorkloadID,
		Canary:        r.Canary,
		Verdict:       r.Verdict,
		Genesis:       r.Genesis,
		Entries:       r.Entries,
		HeadHash:      r.HeadHash,
		NotClaimed:    r.NotClaimed,
		CompletedAt:   r.CompletedAt,
	}
	return canonicaljson.CanonicalJSON(body)
}

// ComputeDigest computes the canonical SHA-256 receipt digest over all header,
// identity, chain and completion fields.
func (r Receipt) ComputeDigest() (string, error) {
	raw, err := r.CanonicalSigningBytes()
	if err != nil {
		return "", err
	}
	return canonicaljson.SHA256Hex([]byte(receiptDomain), raw), nil
}

func (r Receipt) signingDigest() ([]byte, error) {
	raw, err := r.CanonicalSigningBytes()
	if err != nil {
		return nil, err
	}
	h := sha256.New()
	h.Write([]byte(signatureDomain))
	h.Write(raw)
	return h.Sum(nil), nil
}

// Sign computes the canonical receipt digest if unset, verifies structural
// integrity, and signs the receipt with the given Ed25519 private key.
func (r *Receipt) Sign(privateKey ed25519.PrivateKey) error {
	if r == nil {
		return errors.New("backupsurvival: receipt is required")
	}
	if len(privateKey) != ed25519.PrivateKeySize {
		return errors.New("backupsurvival: signing key must be an Ed25519 private key")
	}
	publicKey, ok := privateKey.Public().(ed25519.PublicKey)
	if !ok || len(publicKey) != ed25519.PublicKeySize {
		return errors.New("backupsurvival: invalid Ed25519 public key")
	}
	return r.SignWith(KeyIDOf(publicKey), func(digest []byte) ([]byte, error) {
		return ed25519.Sign(privateKey, digest), nil
	})
}

// SignWith seals and signs the receipt using a caller-supplied signing callback
// (e.g. a local custody holder that never exports private key bytes).
func (r *Receipt) SignWith(keyID string, sign func(digest []byte) ([]byte, error)) error {
	if r == nil {
		return errors.New("backupsurvival: receipt is required")
	}
	if !validKeyID(keyID) {
		return errors.New("backupsurvival: invalid signer keyId")
	}
	if sign == nil {
		return errors.New("backupsurvival: signing callback is required")
	}
	if r.ReceiptDigest == "" {
		digest, err := r.ComputeDigest()
		if err != nil {
			return err
		}
		r.ReceiptDigest = digest
	}
	if err := r.Verify(); err != nil {
		return err
	}
	signingDigest, err := r.signingDigest()
	if err != nil {
		return err
	}
	sigBytes, err := sign(signingDigest)
	if err != nil {
		return err
	}
	if len(sigBytes) != ed25519.SignatureSize {
		return errors.New("backupsurvival: signature must be 64 Ed25519 bytes")
	}
	r.Signature = &Signature{
		KeyID: keyID,
		Value: base64.RawStdEncoding.EncodeToString(sigBytes),
	}
	return nil
}

type chain struct {
	genesis string
	entries []Entry
	prev    string
	now     func() time.Time
	failed  bool
}

func newChain(genesis string, now func() time.Time) *chain {
	return &chain{genesis: genesis, prev: genesis, now: now}
}

func (c *chain) add(step, outcome, detail string) {
	if outcome == OutcomeFailed || outcome == OutcomeBreached {
		c.failed = true
	}
	detail = strings.ToValidUTF8(detail, "\uFFFD")
	e := Entry{Seq: len(c.entries) + 1, Step: step, Outcome: outcome, Detail: detail, At: c.now().UTC(), PrevHash: c.prev}
	e.Hash, _ = entryHash(e)
	c.entries = append(c.entries, e)
	c.prev = e.Hash
}

// Verify recomputes the genesis binding, chain, canonical receipt digest and
// every structural rule. It trusts nothing in the receipt: a tampered header,
// verdict, timestamp, head hash or missing bound fails closed.
func (r Receipt) Verify() error {
	if r.APIVersion != ReceiptVersion {
		return errors.New("backupsurvival: unknown receipt version")
	}
	if !validDigest(r.BindingDigest) {
		return errors.New("backupsurvival: receipt bindingDigest is missing or malformed")
	}
	if !validIdentifier(r.BackendRef) {
		return errors.New("backupsurvival: receipt backendRef is missing or malformed")
	}
	if (r.NodeID != "" || r.WorkloadID != "") && (!validIdentifier(r.NodeID) || !validIdentifier(r.WorkloadID)) {
		return errors.New("backupsurvival: receipt nodeId and workloadId must both be valid identifiers when bound")
	}
	if r.CompletedAt.IsZero() || r.CompletedAt.Location() != time.UTC {
		return errors.New("backupsurvival: receipt completedAt must be a non-zero UTC timestamp")
	}
	wantGenesis, err := genesisFor(r)
	if err != nil {
		return fmt.Errorf("backupsurvival: compute genesis: %w", err)
	}
	if !validDigest(r.Genesis) || r.Genesis != wantGenesis {
		return errors.New("backupsurvival: receipt genesis does not match its header binding")
	}
	if len(r.Entries) == 0 {
		return errors.New("backupsurvival: receipt has no entries")
	}
	prev := wantGenesis
	var lastAt time.Time
	failed := false
	for i, e := range r.Entries {
		if e.Seq != i+1 || e.PrevHash != prev {
			return fmt.Errorf("backupsurvival: receipt chain broken at entry %d", i+1)
		}
		if e.At.IsZero() || e.At.Location() != time.UTC {
			return fmt.Errorf("backupsurvival: receipt entry %d timestamp must be non-zero UTC", i+1)
		}
		if i > 0 && e.At.Before(lastAt) {
			return fmt.Errorf("backupsurvival: receipt entry %d timestamp moved backwards", i+1)
		}
		if !validOutcome(e.Outcome) {
			return fmt.Errorf("backupsurvival: receipt entry %d has invalid outcome %q", i+1, e.Outcome)
		}
		want, err := entryHash(e)
		if err != nil || want != e.Hash {
			return fmt.Errorf("backupsurvival: receipt entry %d was altered", i+1)
		}
		if e.Outcome == OutcomeFailed || e.Outcome == OutcomeBreached {
			failed = true
		}
		prev = e.Hash
		lastAt = e.At
	}
	if r.CompletedAt.Before(lastAt) {
		return errors.New("backupsurvival: receipt completedAt precedes its last entry")
	}
	if r.HeadHash != prev {
		return errors.New("backupsurvival: receipt head hash does not match the chain")
	}
	if want := VerdictSurvives; !failed && r.Verdict != want {
		return errors.New("backupsurvival: verdict understates the recorded steps")
	}
	if failed && r.Verdict != VerdictFailed {
		return errors.New("backupsurvival: verdict overstates a failed proof")
	}
	if r.Verdict == VerdictSurvives {
		if !strings.HasPrefix(r.Canary.Key, canaryPrefixMarker) || len(r.Canary.Key) <= len(canaryPrefixMarker) || !validIdentifier(r.Canary.VersionID) {
			return errors.New("backupsurvival: survives verdict lacks valid canary reference")
		}
		if err := r.requireSteps(); err != nil {
			return err
		}
	}
	have := map[string]bool{}
	for _, n := range r.NotClaimed {
		if strings.TrimSpace(n) == "" {
			return errors.New("backupsurvival: receipt contains an empty not-claimed bound")
		}
		if have[n] {
			return errors.New("backupsurvival: receipt duplicates a not-claimed bound")
		}
		have[n] = true
	}
	for _, n := range RequiredNotClaimed {
		if !have[n] {
			return errors.New("backupsurvival: receipt omits a required not-claimed bound")
		}
	}
	wantDigest, err := r.ComputeDigest()
	if err != nil {
		return fmt.Errorf("backupsurvival: compute receipt digest: %w", err)
	}
	if !validDigest(r.ReceiptDigest) || r.ReceiptDigest != wantDigest {
		return errors.New("backupsurvival: receipt digest does not match its canonical body")
	}
	if r.Signature != nil {
		if _, err := decodeSignature(*r.Signature); err != nil {
			return err
		}
	}
	return nil
}

// VerifyWithPublicKey verifies the full receipt integrity and fails closed
// unless the receipt carries a valid Ed25519 signature made by publicKey.
func (r Receipt) VerifyWithPublicKey(publicKey ed25519.PublicKey) error {
	if len(publicKey) != ed25519.PublicKeySize {
		return errors.New("backupsurvival: trusted public key must be an Ed25519 public key")
	}
	if err := r.Verify(); err != nil {
		return err
	}
	if r.Signature == nil {
		return errors.New("backupsurvival: receipt is unsigned")
	}
	if r.Signature.KeyID != KeyIDOf(publicKey) {
		return errors.New("backupsurvival: receipt signature keyId does not match the trusted public key")
	}
	sigBytes, err := decodeSignature(*r.Signature)
	if err != nil {
		return err
	}
	signingDigest, err := r.signingDigest()
	if err != nil {
		return err
	}
	if !ed25519.Verify(publicKey, signingDigest, sigBytes) {
		return errors.New("backupsurvival: receipt signature does not verify")
	}
	return nil
}

func decodeSignature(sig Signature) ([]byte, error) {
	if !validKeyID(sig.KeyID) {
		return nil, errors.New("backupsurvival: receipt signature keyId is malformed")
	}
	raw, err := base64.RawStdEncoding.Strict().DecodeString(sig.Value)
	if err != nil || len(raw) != ed25519.SignatureSize {
		return nil, errors.New("backupsurvival: receipt signature is malformed")
	}
	return raw, nil
}

// requireSteps makes a "survives" verdict depend on every proof leg being
// present with an admissible outcome, so a truncated or altered chain cannot pass.
func (r Receipt) requireSteps() error {
	seen := map[string]string{}
	for _, e := range r.Entries {
		seen[e.Step] = e.Outcome
	}
	for _, step := range []string{StepBinding, StepCommit, StepRetention, StepSurvival, StepRestore, StepPrune, StepCapacity} {
		if o, ok := seen[step]; !ok || o == OutcomeSkipped {
			return fmt.Errorf("backupsurvival: survives verdict lacks step %q", step)
		}
	}
	for _, a := range attacks {
		o, ok := seen[StepAttackPrefix+string(a.attack)]
		if !ok {
			return fmt.Errorf("backupsurvival: survives verdict lacks attack %q", a.attack)
		}
		if a.absorb {
			if o != OutcomeDenied && o != OutcomeAbsorbed {
				return fmt.Errorf("backupsurvival: attack %q had unexpected outcome %q", a.attack, o)
			}
		} else if o != OutcomeDenied {
			return fmt.Errorf("backupsurvival: attack %q was not denied (%q)", a.attack, o)
		}
	}
	return nil
}

func validOutcome(outcome string) bool {
	switch outcome {
	case OutcomeOK, OutcomeDenied, OutcomeAbsorbed, OutcomeBreached, OutcomeFailed, OutcomeWarn, OutcomeSkipped:
		return true
	default:
		return false
	}
}

func validIdentifier(value string) bool {
	if value == "" || len(value) > 200 || value != strings.TrimSpace(value) || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if r < 0x21 || r == 0x7f {
			return false
		}
	}
	return true
}

func validDigest(v string) bool {
	hexPart, ok := strings.CutPrefix(v, "sha256:")
	if !ok || len(hexPart) != 64 {
		return false
	}
	for _, c := range hexPart {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return strings.Trim(hexPart, "0") != ""
}

func validKeyID(v string) bool {
	hexPart, ok := strings.CutPrefix(v, "ed25519://sha256/")
	if !ok || len(hexPart) != 64 {
		return false
	}
	for _, c := range hexPart {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
