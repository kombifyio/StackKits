package hostcustody

import (
	"bytes"
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	josecipher "github.com/go-jose/go-jose/v4/cipher"
)

// The kombify Wallet transport (kombify.wallet-transport.v1, Gateway
// services/wallet-store/src/transport.mjs) seals one value to one P-256
// recipient: ECDH-ES+A256KW key agreement, A256GCM content encryption and a
// critical wallet_ctx header that binds the exact delivery context. kombify Me
// answers a homelab's tunnel connector token in this format (homelab identity
// target §10, §11). A homelab without a kombify account (Standard Mode,
// Techstack Local) receives it sealed to a key of its own local custody: the
// node generates that key for one delivery, keeps it in memory only and opens
// the envelope here.
const (
	WalletTransportFormat = "kombify.wallet-transport.v1"
	// LocalCustodyAudience is the audience a local-custody recipient names.
	LocalCustodyAudience = "urn:stackkit:local-custody"

	walletTransportAlg = "ECDH-ES+A256KW"
	walletTransportEnc = "A256GCM"
	// The sender refuses a context with less than 60 s or more than 300 s left.
	walletTransportLifetime = 240 * time.Second
	maxWalletTransportBytes = 64 << 10
)

// walletContextFields is the fixed field order of wallet_ctx.
var walletContextFields = []string{
	"custodyNamespace", "ownerSubject", "vaultRef", "itemVersion", "keyGeneration",
	"operation", "operationId", "audience", "recipientKeyThumbprint", "expiresAt",
}

// WalletTransportJWK is a public P-256 key in JWK form.
type WalletTransportJWK struct {
	Kty string `json:"kty"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

// WalletTransportContext is the delivery context bound into wallet_ctx.
// ExpiresAt is in Unix milliseconds.
type WalletTransportContext struct {
	CustodyNamespace       string `json:"custodyNamespace"`
	OwnerSubject           string `json:"ownerSubject"`
	VaultRef               string `json:"vaultRef"`
	ItemVersion            string `json:"itemVersion"`
	KeyGeneration          string `json:"keyGeneration"`
	Operation              string `json:"operation"`
	OperationID            string `json:"operationId"`
	Audience               string `json:"audience"`
	RecipientKeyThumbprint string `json:"recipientKeyThumbprint"`
	ExpiresAt              int64  `json:"expiresAt"`
}

// canonical is the context JSON in the fixed field order.
func (c WalletTransportContext) canonical() []byte {
	values := []any{
		c.CustodyNamespace, c.OwnerSubject, c.VaultRef, c.ItemVersion, c.KeyGeneration,
		c.Operation, c.OperationID, c.Audience, c.RecipientKeyThumbprint, c.ExpiresAt,
	}
	var out bytes.Buffer
	out.WriteByte('{')
	for i, field := range walletContextFields {
		if i > 0 {
			out.WriteByte(',')
		}
		key, _ := json.Marshal(field)
		value, _ := json.Marshal(values[i])
		out.Write(key)
		out.WriteByte(':')
		out.Write(value)
	}
	out.WriteByte('}')
	return out.Bytes()
}

// WalletTransportRecipient is what the sender needs: {kid, jwk, context}.
type WalletTransportRecipient struct {
	KID     string                 `json:"kid"`
	JWK     WalletTransportJWK     `json:"jwk"`
	Context WalletTransportContext `json:"context"`
}

// LocalCustodyRecipient is one delivery's recipient key of the node's local
// custody. The private key exists only in this value; Close clears it.
type LocalCustodyRecipient struct {
	Recipient WalletTransportRecipient
	private   *ecdh.PrivateKey
}

// NewLocalCustodyRecipient generates a fresh P-256 key for one sealed
// delivery to this node's local custody. ownerSubject and vaultRef name the
// homelab and the item; they are bound into the envelope and checked on open.
func NewLocalCustodyRecipient(ownerSubject, vaultRef string, now time.Time) (*LocalCustodyRecipient, error) {
	for _, value := range []string{ownerSubject, vaultRef} {
		if value == "" || len(value) > 256 || strings.ContainsFunc(value, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
			return nil, errors.New("hostcustody: invalid local custody recipient context")
		}
	}
	private, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	jwk := jwkOfP256(private.PublicKey())
	thumbprint, err := WalletTransportThumbprint(jwk)
	if err != nil {
		return nil, err
	}
	operation := make([]byte, 16)
	if _, err := rand.Read(operation); err != nil {
		return nil, err
	}
	return &LocalCustodyRecipient{
		Recipient: WalletTransportRecipient{
			KID: "stackkit-local-" + thumbprint[:16],
			JWK: jwk,
			Context: WalletTransportContext{
				CustodyNamespace: "stackkit-local", OwnerSubject: ownerSubject, VaultRef: vaultRef,
				ItemVersion: "1", KeyGeneration: "1", Operation: "capture", OperationID: hex.EncodeToString(operation),
				Audience: LocalCustodyAudience, RecipientKeyThumbprint: thumbprint,
				ExpiresAt: now.Add(walletTransportLifetime).UnixMilli(),
			},
		},
		private: private,
	}, nil
}

// Close drops the private key. The recipient opens nothing afterwards.
func (r *LocalCustodyRecipient) Close() { r.private = nil }

type walletTransportHeader struct {
	Alg       string             `json:"alg"`
	Enc       string             `json:"enc"`
	Typ       string             `json:"typ"`
	Kid       string             `json:"kid"`
	Epk       WalletTransportJWK `json:"epk"`
	Crit      []string           `json:"crit"`
	WalletCtx string             `json:"wallet_ctx"`
}

// Open opens one compact JWE sealed to this recipient. It refuses any other
// algorithm, type, key id, critical header set or context than the one this
// recipient issued, an expired context and every ciphertext that does not
// authenticate. The caller owns and clears the returned value.
func (r *LocalCustodyRecipient) Open(compact string, now time.Time) ([]byte, error) {
	if r == nil || r.private == nil {
		return nil, errors.New("hostcustody: the local custody recipient is closed")
	}
	if len(compact) == 0 || len(compact) > maxWalletTransportBytes {
		return nil, errors.New("hostcustody: wallet transport envelope is empty or oversized")
	}
	parts := strings.Split(compact, ".")
	if len(parts) != 5 {
		return nil, errors.New("hostcustody: wallet transport envelope is not a compact JWE")
	}
	decoded := make([][]byte, 5)
	for i, part := range parts {
		value, err := base64.RawURLEncoding.DecodeString(part)
		if err != nil {
			return nil, errors.New("hostcustody: wallet transport envelope is not base64url")
		}
		decoded[i] = value
	}
	var header walletTransportHeader
	decoder := json.NewDecoder(bytes.NewReader(decoded[0]))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&header); err != nil {
		return nil, errors.New("hostcustody: wallet transport header is invalid")
	}
	if header.Alg != walletTransportAlg || header.Enc != walletTransportEnc || header.Typ != WalletTransportFormat ||
		header.Kid != r.Recipient.KID || len(header.Crit) != 1 || header.Crit[0] != "wallet_ctx" ||
		header.Epk.Kty != "EC" || header.Epk.Crv != "P-256" {
		return nil, errors.New("hostcustody: wallet transport header differs from this recipient")
	}
	want := base64.RawURLEncoding.EncodeToString(r.Recipient.Context.canonical())
	if subtle.ConstantTimeCompare([]byte(header.WalletCtx), []byte(want)) != 1 {
		return nil, errors.New("hostcustody: wallet transport context differs from this recipient's")
	}
	if now.UnixMilli() >= r.Recipient.Context.ExpiresAt {
		return nil, errors.New("hostcustody: wallet transport context expired")
	}
	ephemeral, err := publicP256(header.Epk)
	if err != nil {
		return nil, err
	}
	shared, err := r.private.ECDH(ephemeral)
	if err != nil {
		return nil, errors.New("hostcustody: wallet transport key agreement failed")
	}
	kek := make([]byte, 32)
	supPubInfo := []byte{0, 0, 1, 0}
	kdf := josecipher.NewConcatKDF(crypto.SHA256, shared, lengthPrefixed([]byte(walletTransportAlg)), lengthPrefixed(nil), lengthPrefixed(nil), supPubInfo, nil)
	if _, err := kdf.Read(kek); err != nil {
		return nil, err
	}
	clear(shared)
	block, err := aes.NewCipher(kek)
	clear(kek)
	if err != nil {
		return nil, err
	}
	cek, err := josecipher.KeyUnwrap(block, decoded[1])
	if err != nil || len(cek) != 32 {
		return nil, errors.New("hostcustody: wallet transport key unwrap failed")
	}
	content, err := aes.NewCipher(cek)
	clear(cek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(content)
	if err != nil || len(decoded[2]) != gcm.NonceSize() || len(decoded[4]) != gcm.Overhead() {
		return nil, errors.New("hostcustody: wallet transport content parameters are invalid")
	}
	plaintext, err := gcm.Open(nil, decoded[2], append(bytes.Clone(decoded[3]), decoded[4]...), []byte(parts[0]))
	if err != nil {
		return nil, errors.New("hostcustody: wallet transport ciphertext does not authenticate")
	}
	return plaintext, nil
}

// WalletTransportThumbprint is the RFC 7638 thumbprint of a public P-256 JWK.
func WalletTransportThumbprint(jwk WalletTransportJWK) (string, error) {
	if _, err := publicP256(jwk); err != nil {
		return "", err
	}
	canonical := fmt.Sprintf(`{"crv":%s,"kty":%s,"x":%s,"y":%s}`, strconv.Quote(jwk.Crv), strconv.Quote(jwk.Kty), strconv.Quote(jwk.X), strconv.Quote(jwk.Y))
	sum := sha256.Sum256([]byte(canonical))
	return base64.RawURLEncoding.EncodeToString(sum[:]), nil
}

func jwkOfP256(key *ecdh.PublicKey) WalletTransportJWK {
	raw := key.Bytes()
	return WalletTransportJWK{
		Kty: "EC", Crv: "P-256",
		X: base64.RawURLEncoding.EncodeToString(raw[1:33]),
		Y: base64.RawURLEncoding.EncodeToString(raw[33:65]),
	}
}

func publicP256(jwk WalletTransportJWK) (*ecdh.PublicKey, error) {
	x, errX := base64.RawURLEncoding.DecodeString(jwk.X)
	y, errY := base64.RawURLEncoding.DecodeString(jwk.Y)
	if jwk.Kty != "EC" || jwk.Crv != "P-256" || errX != nil || errY != nil || len(x) != 32 || len(y) != 32 {
		return nil, errors.New("hostcustody: not a public P-256 JWK")
	}
	key, err := ecdh.P256().NewPublicKey(append(append([]byte{0x04}, x...), y...))
	if err != nil {
		return nil, errors.New("hostcustody: the JWK is not a point on P-256")
	}
	return key, nil
}

func lengthPrefixed(data []byte) []byte {
	out := make([]byte, 4+len(data))
	out[0], out[1], out[2], out[3] = byte(len(data)>>24), byte(len(data)>>16), byte(len(data)>>8), byte(len(data))
	copy(out[4:], data)
	return out
}
