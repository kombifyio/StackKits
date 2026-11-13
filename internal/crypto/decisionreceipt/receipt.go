package decisionreceipt

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
)

// The refusal names the platform binds for every repository in this work
// package. A consumer answers with one of these names and nothing else.
const (
	// RefusalInvalid covers the signature, the shape, the outcome and the
	// answering party.
	RefusalInvalid = "stackkit_receipt_invalid"
	// RefusalExpired covers a receipt outside its time window.
	RefusalExpired = "stackkit_receipt_expired"
	// RefusalSpent covers a receipt that was already used on this node.
	RefusalSpent = "stackkit_receipt_spent"
	// RefusalTargetMismatch covers a receipt that binds another operation,
	// target, digest, revision or acting principal.
	RefusalTargetMismatch = "stackkit_receipt_target_mismatch"
)

// The receipt contract the platform's ledger signs.
const (
	// ReceiptType is the JWS protected header type of a decision receipt.
	ReceiptType = "kombify.decision-receipt.v1"
	// SubjectStore is the only ledger subject this consumer admits. A RIL
	// action receipt names another store and is refused here; that check is
	// what keeps one receipt from passing for the other.
	SubjectStore = "stackkit_operation"
	// IntentDigestDomain separates the intent digest from every other digest.
	IntentDigestDomain = "kombify.decision-intent.digest.v1"
	// RiskDestructive is the risk the ledger records for a StackKits operation.
	RiskDestructive = "destructive"
	// OutcomeAllowed and OptionAllowOnce are the only answer profile a
	// consumer admits.
	OutcomeAllowed  = "allowed"
	OptionAllowOnce = "allow_once"
	// AnsweredByPerson is the only answering party.
	AnsweredByPerson = "person"
	// SurfaceEquipment is the surface a person's answer may never carry.
	SurfaceEquipment = "equipment"
	// LifetimeOnce is the only lifetime scope.
	LifetimeOnce = "once"
)

// The claim shapes the ledger signs, in the ledger's own vocabulary.
var (
	decisionIDPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{3,127}$`)
	surfacePattern     = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,39}$`)
	intentDigestFormat = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
	hexDigestFormat    = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

// Refusal is a named refusal. The name is the contract; the message only
// explains it.
type Refusal struct {
	Name    string
	Message string
}

func (r *Refusal) Error() string { return r.Name + ": " + r.Message }

func refuse(name, format string, args ...any) *Refusal {
	return &Refusal{Name: name, Message: fmt.Sprintf(format, args...)}
}

// Receipt is one verified decision receipt.
type Receipt struct {
	ReceiptID     string
	DecisionID    string
	OperationID   string
	SubjectStore  string
	IssuedAt      time.Time
	ExpiresAt     time.Time
	Outcome       string
	OptionKind    string
	AnsweredBy    Principal
	Surface       string
	Assurance     string
	IntentDigest  string
	LifetimeScope string
}

type receiptClaims struct {
	ReceiptID    string          `json:"receiptId"`
	DecisionID   string          `json:"decisionId"`
	OperationID  string          `json:"operationId"`
	SubjectStore string          `json:"subjectStore"`
	IssuedAt     string          `json:"issuedAt"`
	ExpiresAt    string          `json:"expiresAt"`
	Outcome      string          `json:"outcome"`
	OptionKind   string          `json:"optionKind"`
	AnsweredBy   principalClaims `json:"answeredBy"`
	Surface      string          `json:"surface"`
	Assurance    string          `json:"assurance"`
	IntentDigest string          `json:"intentDigest"`
	Lifetime     lifetimeClaims  `json:"lifetime"`
}

type principalClaims struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

type lifetimeClaims struct {
	Scope string `json:"scope"`
}

// MaxReceiptLifetime is the longest window a receipt may carry: the ledger
// admits a stackkit_operation decision for at most 900 seconds, and a
// consumer refuses a longer window rather than trusting the producer for it.
const MaxReceiptLifetime = 900 * time.Second

// Verify checks one compact JWS receipt against the consumer's own resolved
// facts and the published purpose keys. It returns the receipt when every
// condition holds, and a named refusal otherwise.
//
// The refusals come in the contract's order (kombify-workspace
// scripts/check-decision-contract.mjs, stackkitReceiptConsumerSemantics):
// stackkit_receipt_invalid first (the signature against a published purpose
// key for decision receipts, the type, the store, the expiry member, the
// answer allowing this call once, a person answering on a surface other than
// equipment, a window of at most 900 seconds, and a clock not before
// issuedAt), then stackkit_receipt_target_mismatch (the intent digest
// recomputed here from the consumer's own operation, target, arguments, plan,
// revision and acting principal), then stackkit_receipt_expired (the clock at
// or after expiresAt). stackkit_receipt_spent comes last, from the spent
// record (Admit).
func Verify(token string, facts Facts, keys *KeySet, now time.Time) (*Receipt, error) {
	claims, err := parseReceipt(token, keys)
	if err != nil {
		return nil, err
	}
	if claims.SubjectStore != SubjectStore {
		return nil, refuse(RefusalInvalid, "the receipt is for subject store %q, not %q", claims.SubjectStore, SubjectStore)
	}
	if claims.Outcome != OutcomeAllowed || claims.OptionKind != OptionAllowOnce {
		return nil, refuse(RefusalInvalid, "the receipt does not allow this call once")
	}
	if claims.AnsweredBy.Kind != AnsweredByPerson {
		return nil, refuse(RefusalInvalid, "the receipt was not answered by a person")
	}
	if claims.Surface == SurfaceEquipment {
		return nil, refuse(RefusalInvalid, "the receipt was answered on the equipment surface")
	}
	if claims.Lifetime.Scope != LifetimeOnce {
		return nil, refuse(RefusalInvalid, "the receipt does not cover a single use")
	}
	issuedAt, err := parseInstant(claims.IssuedAt)
	if err != nil {
		return nil, refuse(RefusalInvalid, "the receipt issue time is invalid")
	}
	expiresAt, err := parseInstant(claims.ExpiresAt)
	if err != nil {
		return nil, refuse(RefusalInvalid, "the receipt expiry time is invalid")
	}
	if !issuedAt.Before(expiresAt) {
		return nil, refuse(RefusalInvalid, "the receipt time window is empty")
	}
	if expiresAt.Sub(issuedAt) > MaxReceiptLifetime {
		return nil, refuse(RefusalInvalid, "the receipt window is longer than %s", MaxReceiptLifetime)
	}
	if now.Before(issuedAt) {
		return nil, refuse(RefusalInvalid, "the receipt was issued after this clock's time")
	}
	expected, err := IntentDigest(facts)
	if err != nil {
		return nil, refuse(RefusalInvalid, "the intent could not be recomputed")
	}
	if claims.IntentDigest != expected {
		return nil, refuse(RefusalTargetMismatch, "the receipt binds another operation, target, digest, revision or principal")
	}
	if !now.Before(expiresAt) {
		return nil, refuse(RefusalExpired, "the receipt is outside its time window")
	}
	return &Receipt{
		ReceiptID:     claims.ReceiptID,
		DecisionID:    claims.DecisionID,
		OperationID:   claims.OperationID,
		SubjectStore:  claims.SubjectStore,
		IssuedAt:      issuedAt,
		ExpiresAt:     expiresAt,
		Outcome:       claims.Outcome,
		OptionKind:    claims.OptionKind,
		AnsweredBy:    Principal{Kind: claims.AnsweredBy.Kind, ID: claims.AnsweredBy.ID},
		Surface:       claims.Surface,
		Assurance:     claims.Assurance,
		IntentDigest:  claims.IntentDigest,
		LifetimeScope: claims.Lifetime.Scope,
	}, nil
}

// Admit verifies one receipt for one first-slice call on this node and spends
// it before the call's first effect.
//
// Everything it trusts comes from node custody (custody.go): the purpose keys
// the privileged refresher writes, inside their window; the node's enrolled
// identity, which every target reference carries; and the node-level spent
// record. A process that cannot read the keys or the identity cannot verify
// and refuses the receipt; a process that cannot write the spent record
// refuses the call.
func Admit(token string, request Request, now time.Time) (*Receipt, error) {
	return nodeCustody().admit(token, request, now)
}

func (c custody) admit(token string, request Request, now time.Time) (*Receipt, error) {
	keys, err := c.loadPurposeKeys(now)
	if err != nil {
		return nil, refuse(RefusalInvalid, "this node cannot verify a receipt: %v", err)
	}
	node, err := c.loadNodeIdentity()
	if err != nil {
		return nil, refuse(RefusalInvalid, "this node holds no enrolled identity a receipt can bind: %v", err)
	}
	facts, err := request.Facts(node)
	if err != nil {
		return nil, refuse(RefusalInvalid, "the call cannot be bound: %v", err)
	}
	receipt, err := Verify(token, facts, keys, now)
	if err != nil {
		return nil, err
	}
	// A person confirms an operation on this node only as the node's enrolled
	// Owner. The intent digest binds the principal the filer named; this binds
	// that principal to the node, so a receipt correctly filed for another
	// person is refused here.
	if facts.Principal.Kind == PrincipalPerson && (node.OwnerID == "" || facts.Principal.ID != node.OwnerID) {
		return nil, refuse(RefusalTargetMismatch, "the acting person is not this node's enrolled Owner")
	}
	if err := c.spend(Record{
		ReceiptID:  receipt.ReceiptID,
		DecisionID: receipt.DecisionID,
		Operation:  facts.Operation,
		Resource:   facts.Resource,
		SpentAt:    now.UTC(),
	}); err != nil {
		var refusal *Refusal
		if errors.As(err, &refusal) {
			return nil, refusal
		}
		return nil, refuse(RefusalInvalid, "this node cannot record the receipt as spent: %v", err)
	}
	return receipt, nil
}

// parseReceipt verifies the signature and the shape of one compact JWS.
func parseReceipt(token string, keys *KeySet) (*receiptClaims, error) {
	trimmed := strings.TrimSpace(token)
	if trimmed == "" || len(trimmed) > 16384 {
		return nil, refuse(RefusalInvalid, "the receipt is empty or too long")
	}
	header, payload, err := verifySignature(trimmed, keys)
	if err != nil {
		return nil, err
	}
	if header.Type != ReceiptType {
		return nil, refuse(RefusalInvalid, "the receipt type is %q, not %q", header.Type, ReceiptType)
	}
	var claims receiptClaims
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&claims); err != nil {
		return nil, refuse(RefusalInvalid, "the receipt shape is invalid")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, refuse(RefusalInvalid, "the receipt carries trailing data")
	}
	var generic map[string]any
	if err := json.Unmarshal(payload, &generic); err != nil {
		return nil, refuse(RefusalInvalid, "the receipt payload is invalid")
	}
	canonical, err := canonicalJSON(generic)
	if err != nil || canonical != string(payload) {
		return nil, refuse(RefusalInvalid, "the receipt payload is not canonical")
	}
	if err := validateClaims(claims); err != nil {
		return nil, err
	}
	return &claims, nil
}

func validateClaims(claims receiptClaims) error {
	for _, member := range []struct{ name, value string }{
		{"receiptId", claims.ReceiptID},
		{"decisionId", claims.DecisionID},
		{"operationId", claims.OperationID},
	} {
		if !decisionIDPattern.MatchString(member.value) {
			return refuse(RefusalInvalid, "the receipt %s is invalid", member.name)
		}
	}
	if !surfacePattern.MatchString(claims.Surface) || claims.Surface == SurfaceEquipment {
		return refuse(RefusalInvalid, "the receipt surface is invalid")
	}
	if claims.Assurance != "L1" && claims.Assurance != "L2" {
		return refuse(RefusalInvalid, "the receipt assurance is invalid")
	}
	if !intentDigestFormat.MatchString(claims.IntentDigest) {
		return refuse(RefusalInvalid, "the receipt intent digest is invalid")
	}
	if claims.AnsweredBy.ID == "" || len(claims.AnsweredBy.ID) > 256 {
		return refuse(RefusalInvalid, "the answering party is invalid")
	}
	return nil
}

// parseInstant reads one ISO-8601 instant with an offset.
func parseInstant(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, err
	}
	return parsed.UTC(), nil
}

// ValidDigest reports whether a value is a hex SHA-256 digest.
func ValidDigest(value string) bool { return hexDigestFormat.MatchString(value) }
