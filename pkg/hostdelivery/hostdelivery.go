// Package hostdelivery is the public wire contract for sealed host delivery of
// one secret input to the StackKit node that owns it. A delivering authority
// such as Techstack seals the value to a recipient key that the node generated
// and proved; only that node opens it into its own encrypted custody. The
// authority and every transport between them carry ciphertext and opaque
// references only.
//
// The sealing scheme is HPKE (RFC 9180): DHKEM(X25519, HKDF-SHA256) with
// HKDF-SHA256 and ChaCha20-Poly1305. The canonical Context is the HPKE info,
// so a ciphertext opens only under the exact delivery, tenant, server, binding
// generation, operation, recipient key and expiry it was sealed for.
package hostdelivery

import (
	"bytes"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/hpke"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"time"
)

const (
	EnvelopeAPIVersion  = "stackkit.sealed-host-delivery/v1"
	StatementAPIVersion = "stackkit.host-recipient-statement/v1"

	// OperationBackupTargetImport delivers the owner S3 target material that
	// `stackkit backup target import` writes into encrypted local custody.
	OperationBackupTargetImport = "backup-target-import"
	// OperationBackupTargetRebind renews Plan authority over identical custody
	// when a node-issued binding has expired.
	OperationBackupTargetRebind = "backup-target-rebind"

	// MaxLifetime bounds how long one sealed delivery stays openable.
	MaxLifetime = 15 * time.Minute
	// MaxPlaintextBytes bounds the sealed material.
	MaxPlaintextBytes = 16 << 10

	infoDomain      = "stackkit.sealed-host-delivery/v1\x00"
	statementDomain = "stackkit.owner-bound-policy-state/v1\x00"
)

// Context is the exact scope a delivery is sealed for. Every field is bound
// into the ciphertext; none is secret.
type Context struct {
	TenantRef            string `json:"tenantRef"`
	DeliveryRef          string `json:"deliveryRef"`
	ItemVersion          uint64 `json:"itemVersion"`
	SiteRef              string `json:"siteRef"`
	NodeRef              string `json:"nodeRef"`
	BindingGeneration    uint64 `json:"bindingGeneration"`
	Operation            string `json:"operation"`
	RecipientFingerprint string `json:"recipientFingerprint"`
	OperationID          string `json:"operationId"`
	IssuedAt             string `json:"issuedAt"`
	NotAfter             string `json:"notAfter"`
}

// Envelope is the only thing that crosses the control plane and transport.
type Envelope struct {
	APIVersion string  `json:"apiVersion"`
	Context    Context `json:"context"`
	Sealed     []byte  `json:"sealed"`
}

// Signature is the node owner's signature over a recipient statement.
type Signature struct {
	OwnerRef string `json:"ownerRef"`
	KeyID    string `json:"keyId"`
	Value    string `json:"value"`
}

// Statement is the node's proof that a recipient key belongs to it. The
// Challenge makes it fresh: the delivering authority supplies it and rejects a
// statement that does not echo it.
type Statement struct {
	APIVersion   string    `json:"apiVersion"`
	SiteRef      string    `json:"siteRef"`
	NodeRef      string    `json:"nodeRef"`
	RecipientKey string    `json:"recipientKey"`
	Fingerprint  string    `json:"fingerprint"`
	Generation   uint64    `json:"generation"`
	Challenge    string    `json:"challenge"`
	IssuedAt     string    `json:"issuedAt"`
	OwnerKeyID   string    `json:"ownerKeyId"`
	OwnerPublic  string    `json:"ownerPublicKey"`
	Signature    Signature `json:"signature"`
}

// Fingerprint names a recipient public key.
func Fingerprint(public []byte) string {
	sum := sha256.Sum256(public)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// SigningBytes is the canonical payload the owner signs, without the signature.
func (s Statement) SigningBytes() ([]byte, error) {
	s.Signature = Signature{}
	return json.Marshal(s)
}

// StatementDigest is the digest the owner key signs for a statement payload.
func StatementDigest(payload []byte) []byte {
	digest := sha256.New()
	_, _ = digest.Write([]byte(statementDomain))
	_, _ = digest.Write(payload)
	return digest.Sum(nil)
}

// VerifyStatement checks the owner signature, key binding and freshness of a
// statement against the owner public key the caller pinned at enrollment. It
// proves the recipient key belongs to the holder of that owner key; it does not
// establish that the pinned owner key is genuine.
func VerifyStatement(statement Statement, pinnedOwnerPublic ed25519.PublicKey, challenge string, now time.Time, maxAge time.Duration) error {
	if statement.APIVersion != StatementAPIVersion || statement.Challenge == "" || statement.Challenge != challenge {
		return errors.New("hostdelivery: recipient statement does not answer the challenge")
	}
	public, err := base64.RawStdEncoding.Strict().DecodeString(statement.RecipientKey)
	if err != nil || len(public) != 32 || Fingerprint(public) != statement.Fingerprint {
		return errors.New("hostdelivery: recipient statement key is invalid")
	}
	owner, err := base64.RawStdEncoding.Strict().DecodeString(statement.OwnerPublic)
	if err != nil || len(owner) != ed25519.PublicKeySize || !bytes.Equal(owner, pinnedOwnerPublic) {
		return errors.New("hostdelivery: recipient statement owner key differs from the pinned owner key")
	}
	issued, err := time.Parse(time.RFC3339Nano, statement.IssuedAt)
	if err != nil || issued.After(now.Add(time.Minute)) || now.Sub(issued) > maxAge {
		return errors.New("hostdelivery: recipient statement is not fresh")
	}
	payload, err := statement.SigningBytes()
	if err != nil {
		return err
	}
	signature, err := base64.RawStdEncoding.Strict().DecodeString(statement.Signature.Value)
	if err != nil || len(signature) != ed25519.SignatureSize || !ed25519.Verify(pinnedOwnerPublic, StatementDigest(payload), signature) {
		return errors.New("hostdelivery: recipient statement signature does not verify")
	}
	return nil
}

func info(context Context) ([]byte, error) {
	encoded, err := json.Marshal(context)
	if err != nil {
		return nil, err
	}
	return append([]byte(infoDomain), encoded...), nil
}

func validateContext(context Context, now time.Time) error {
	for _, value := range []string{context.TenantRef, context.DeliveryRef, context.SiteRef, context.NodeRef, context.Operation, context.RecipientFingerprint, context.OperationID} {
		if value == "" {
			return errors.New("hostdelivery: context is incomplete")
		}
	}
	issued, issuedErr := time.Parse(time.RFC3339Nano, context.IssuedAt)
	until, untilErr := time.Parse(time.RFC3339Nano, context.NotAfter)
	if issuedErr != nil || untilErr != nil || !until.After(issued) || until.Sub(issued) > MaxLifetime {
		return errors.New("hostdelivery: context lifetime is invalid")
	}
	if !now.Before(until) {
		return errors.New("hostdelivery: delivery expired")
	}
	return nil
}

// Seal encrypts plaintext to a recipient public key under the exact context.
func Seal(recipientPublic []byte, context Context, plaintext []byte, now time.Time) ([]byte, error) {
	if len(plaintext) == 0 || len(plaintext) > MaxPlaintextBytes {
		return nil, errors.New("hostdelivery: sealed material is empty or oversized")
	}
	if context.RecipientFingerprint != Fingerprint(recipientPublic) {
		return nil, errors.New("hostdelivery: context names a different recipient key")
	}
	if err := validateContext(context, now); err != nil {
		return nil, err
	}
	public, err := ecdh.X25519().NewPublicKey(recipientPublic)
	if err != nil {
		return nil, errors.New("hostdelivery: recipient key is invalid")
	}
	key, err := hpke.NewDHKEMPublicKey(public)
	if err != nil {
		return nil, errors.New("hostdelivery: recipient key is invalid")
	}
	bound, err := info(context)
	if err != nil {
		return nil, err
	}
	sealed, err := hpke.Seal(key, hpke.HKDFSHA256(), hpke.ChaCha20Poly1305(), bound, plaintext)
	if err != nil {
		return nil, errors.New("hostdelivery: seal failed")
	}
	return json.Marshal(Envelope{APIVersion: EnvelopeAPIVersion, Context: context, Sealed: sealed})
}

// Open decrypts an envelope with the recipient private key. Callers must have
// checked the context against their own expectations first; Open re-binds the
// context cryptographically, so a changed field fails to decrypt.
func Open(recipientPrivate []byte, raw []byte, now time.Time) (Context, []byte, error) {
	var envelope Envelope
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil || envelope.APIVersion != EnvelopeAPIVersion {
		return Context{}, nil, errors.New("hostdelivery: envelope is invalid")
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return Context{}, nil, errors.New("hostdelivery: envelope contains trailing content")
	}
	if err := validateContext(envelope.Context, now); err != nil {
		return Context{}, nil, err
	}
	private, err := ecdh.X25519().NewPrivateKey(recipientPrivate)
	if err != nil {
		return Context{}, nil, errors.New("hostdelivery: recipient key is invalid")
	}
	if envelope.Context.RecipientFingerprint != Fingerprint(private.PublicKey().Bytes()) {
		return Context{}, nil, errors.New("hostdelivery: delivery is sealed to a different recipient")
	}
	key, err := hpke.NewDHKEMPrivateKey(private)
	if err != nil {
		return Context{}, nil, errors.New("hostdelivery: recipient key is invalid")
	}
	bound, err := info(envelope.Context)
	if err != nil {
		return Context{}, nil, err
	}
	plaintext, err := hpke.Open(key, hpke.HKDFSHA256(), hpke.ChaCha20Poly1305(), bound, envelope.Sealed)
	if err != nil {
		return Context{}, nil, errors.New("hostdelivery: delivery does not open for this recipient and context")
	}
	return envelope.Context, plaintext, nil
}
