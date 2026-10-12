// Package hostdelivery is the public wire contract for sealed host delivery of
// one input to the StackKit node that owns it. A delivering authority such as
// Techstack or the kombify Wallet seals the value to a recipient key that the
// node generated and proved; only that node opens it into its own custody. The
// authority and every transport between them carry ciphertext and opaque
// references only.
//
// The sealing scheme is HPKE (RFC 9180): DHKEM(X25519, HKDF-SHA256) with
// HKDF-SHA256 and ChaCha20-Poly1305. The canonical Context is the HPKE info,
// so a ciphertext opens only under the exact delivery, tenant, server, binding
// generation, operation, recipient key and expiry it was sealed for.
//
// HPKE base mode does not authenticate the sender: anyone holding the public
// recipient key can seal to it. Operations whose effect is integrity-critical
// (Wallet items, SSH CA trust and host certificates) therefore also carry an
// Ed25519 authority signature over the whole envelope, made with a key the node
// pinned at enrollment and echoes in its recipient statement. Each operation
// declares its own plaintext limit and whether that signature is required; see
// LookupOperation.
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
	"sort"
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
	// OperationWalletItemImport delivers one Wallet item version (for example a
	// stack secret input) into the node's encrypted local custody. The
	// plaintext is the item value itself; DeliveryRef names the item and
	// ItemVersion its immutable version.
	OperationWalletItemImport = "wallet-item-import"
	// OperationWalletItemRotate delivers a newer version of an item already in
	// custody; the node removes every older version once it holds the new one.
	OperationWalletItemRotate = "wallet-item-rotate"
	// OperationSSHCATrust delivers the owner's SSH user CA public keys and the
	// principals each local user accepts (SSHCATrust). An empty key list
	// revokes the trust like OperationSSHCARevoke.
	OperationSSHCATrust = "ssh-ca-trust"
	// OperationSSHCARevoke removes the owner's SSH CA trust (SSHCARevoke).
	OperationSSHCARevoke = "ssh-ca-revoke"
	// OperationSSHHostCertificate delivers an SSH host certificate for one of
	// this server's host keys (SSHHostCertificate).
	OperationSSHHostCertificate = "ssh-host-certificate"

	// MaxLifetime bounds how long one sealed delivery stays openable.
	MaxLifetime = 15 * time.Minute
	// MaxPlaintextBytes bounds the sealed material of the backup-target and SSH
	// operations.
	MaxPlaintextBytes = 16 << 10
	// MaxWalletItemPlaintextBytes bounds one delivered Wallet item value.
	MaxWalletItemPlaintextBytes = 64 << 10
	// MaxEnvelopeBytes bounds one encoded envelope, the largest operation's
	// sealed material and its context included.
	MaxEnvelopeBytes = 128 << 10

	infoDomain      = "stackkit.sealed-host-delivery/v1\x00"
	statementDomain = "stackkit.owner-bound-policy-state/v1\x00"
	authorityDomain = "stackkit.sealed-host-delivery-authority/v1\x00"
)

// OperationSpec is what the contract fixes for one operation.
type OperationSpec struct {
	// MaxPlaintextBytes bounds the sealed material of this operation.
	MaxPlaintextBytes int
	// AuthoritySigned requires an envelope signature by the delivering
	// authority key the node pinned for its recipient generation.
	AuthoritySigned bool
	// Secret marks material the node must treat as a delivered secret: it is
	// listed for rotation when the node is detached.
	Secret bool
}

var operations = map[string]OperationSpec{
	OperationBackupTargetImport: {MaxPlaintextBytes: MaxPlaintextBytes, Secret: true},
	OperationBackupTargetRebind: {MaxPlaintextBytes: MaxPlaintextBytes, Secret: true},
	OperationWalletItemImport:   {MaxPlaintextBytes: MaxWalletItemPlaintextBytes, AuthoritySigned: true, Secret: true},
	OperationWalletItemRotate:   {MaxPlaintextBytes: MaxWalletItemPlaintextBytes, AuthoritySigned: true, Secret: true},
	OperationSSHCATrust:         {MaxPlaintextBytes: MaxPlaintextBytes, AuthoritySigned: true},
	OperationSSHCARevoke:        {MaxPlaintextBytes: MaxPlaintextBytes, AuthoritySigned: true},
	OperationSSHHostCertificate: {MaxPlaintextBytes: MaxPlaintextBytes, AuthoritySigned: true},
}

// LookupOperation returns the contract of a known operation. An unknown
// operation is neither sealed nor opened.
func LookupOperation(name string) (OperationSpec, bool) {
	spec, ok := operations[name]
	return spec, ok
}

// Operations lists every known operation in a stable order.
func Operations() []string {
	names := make([]string, 0, len(operations))
	for name := range operations {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// SSHCATrust is the plaintext of OperationSSHCATrust. CAKeys are OpenSSH
// authorized-key lines of the owner's user CA (several during a CA rotation);
// Principals maps each local user to the certificate principals it accepts.
type SSHCATrust struct {
	CAKeys     []string            `json:"caKeys"`
	Principals map[string][]string `json:"principals"`
}

// SSHCARevoke is the plaintext of OperationSSHCARevoke, encoded as {}.
type SSHCARevoke struct{}

// SSHHostCertificate is the plaintext of OperationSSHHostCertificate: one
// OpenSSH host certificate line for a key this server already holds.
type SSHHostCertificate struct {
	Certificate string `json:"certificate"`
}

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
	// Authority is the delivering authority's signature over the envelope. It
	// is required for operations whose spec is AuthoritySigned.
	Authority *AuthoritySignature `json:"authority,omitempty"`
}

// AuthoritySignature is an Ed25519 signature by the delivering authority over
// the envelope without this field.
type AuthoritySignature struct {
	KeyID string `json:"keyId"`
	Value string `json:"value"`
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
	APIVersion   string `json:"apiVersion"`
	SiteRef      string `json:"siteRef"`
	NodeRef      string `json:"nodeRef"`
	RecipientKey string `json:"recipientKey"`
	Fingerprint  string `json:"fingerprint"`
	Generation   uint64 `json:"generation"`
	Challenge    string `json:"challenge"`
	IssuedAt     string `json:"issuedAt"`
	OwnerKeyID   string `json:"ownerKeyId"`
	OwnerPublic  string `json:"ownerPublicKey"`
	// AuthorityKeyID and AuthorityFingerprint name the delivering authority
	// key the node pinned for this generation. They are empty until one is
	// pinned, so a statement without them is byte-identical to a v1 statement
	// from before authority signatures existed.
	AuthorityKeyID       string    `json:"authorityKeyId,omitempty"`
	AuthorityFingerprint string    `json:"authorityFingerprint,omitempty"`
	Signature            Signature `json:"signature"`
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

// VerifyStatementAuthority checks that a statement, already verified with
// VerifyStatement, names exactly the authority key the caller signs with. An
// authority seals AuthoritySigned operations only to a node that pinned it.
func VerifyStatementAuthority(statement Statement, authority ed25519.PublicKey, keyID string) error {
	if len(authority) != ed25519.PublicKeySize || keyID == "" || statement.AuthorityKeyID != keyID || statement.AuthorityFingerprint != Fingerprint(authority) {
		return errors.New("hostdelivery: recipient statement does not pin this delivering authority key")
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
	if _, known := LookupOperation(context.Operation); !known {
		return errors.New("hostdelivery: context names an unknown operation")
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
// The plaintext must fit the operation's limit. An AuthoritySigned operation
// additionally needs SignEnvelope before it is delivered.
func Seal(recipientPublic []byte, context Context, plaintext []byte, now time.Time) ([]byte, error) {
	if err := validateContext(context, now); err != nil {
		return nil, err
	}
	spec, _ := LookupOperation(context.Operation)
	if len(plaintext) == 0 || len(plaintext) > spec.MaxPlaintextBytes {
		return nil, errors.New("hostdelivery: sealed material is empty or exceeds the operation limit")
	}
	if context.RecipientFingerprint != Fingerprint(recipientPublic) {
		return nil, errors.New("hostdelivery: context names a different recipient key")
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

// authoritySigningBytes is what the authority signs: the envelope without its
// authority signature, under a domain separator.
func (e Envelope) authoritySigningBytes() ([]byte, error) {
	e.Authority = nil
	encoded, err := json.Marshal(e)
	if err != nil {
		return nil, err
	}
	return append([]byte(authorityDomain), encoded...), nil
}

// SignEnvelope adds the delivering authority's signature to a sealed envelope.
func SignEnvelope(raw []byte, authority ed25519.PrivateKey, keyID string) ([]byte, error) {
	if len(authority) != ed25519.PrivateKeySize || keyID == "" {
		return nil, errors.New("hostdelivery: authority key is invalid")
	}
	envelope, err := ParseEnvelope(raw)
	if err != nil {
		return nil, err
	}
	payload, err := envelope.authoritySigningBytes()
	if err != nil {
		return nil, err
	}
	envelope.Authority = &AuthoritySignature{KeyID: keyID, Value: base64.RawStdEncoding.EncodeToString(ed25519.Sign(authority, payload))}
	return json.Marshal(envelope)
}

// VerifyAuthority checks the envelope's authority signature against the
// authority key the node pinned.
func (e Envelope) VerifyAuthority(pinned ed25519.PublicKey, keyID string) error {
	if e.Authority == nil {
		return errors.New("hostdelivery: delivery carries no authority signature")
	}
	if len(pinned) != ed25519.PublicKeySize || keyID == "" || e.Authority.KeyID != keyID {
		return errors.New("hostdelivery: delivery is signed by a different authority key")
	}
	signature, err := base64.RawStdEncoding.Strict().DecodeString(e.Authority.Value)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return errors.New("hostdelivery: delivery authority signature is malformed")
	}
	payload, err := e.authoritySigningBytes()
	if err != nil {
		return err
	}
	if !ed25519.Verify(pinned, payload, signature) {
		return errors.New("hostdelivery: delivery authority signature does not verify")
	}
	return nil
}

// ParseEnvelope decodes one envelope strictly and checks its bounds. It does
// not open the envelope or judge its context.
func ParseEnvelope(raw []byte) (Envelope, error) {
	if len(raw) == 0 || len(raw) > MaxEnvelopeBytes {
		return Envelope{}, errors.New("hostdelivery: envelope is empty or oversized")
	}
	var envelope Envelope
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil || envelope.APIVersion != EnvelopeAPIVersion {
		return Envelope{}, errors.New("hostdelivery: envelope is invalid")
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return Envelope{}, errors.New("hostdelivery: envelope contains trailing content")
	}
	return envelope, nil
}

// Open decrypts an envelope with the recipient private key. Callers must have
// checked the context, and for an AuthoritySigned operation the authority
// signature (Envelope.VerifyAuthority), against their own expectations first;
// Open re-binds the context cryptographically, so a changed field fails to
// decrypt.
func Open(recipientPrivate []byte, raw []byte, now time.Time) (Context, []byte, error) {
	envelope, err := ParseEnvelope(raw)
	if err != nil {
		return Context{}, nil, err
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
	spec, _ := LookupOperation(envelope.Context.Operation)
	if len(plaintext) == 0 || len(plaintext) > spec.MaxPlaintextBytes {
		clear(plaintext)
		return Context{}, nil, errors.New("hostdelivery: sealed material is empty or exceeds the operation limit")
	}
	return envelope.Context, plaintext, nil
}
