package localevidence

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"

	stackcrypto "github.com/kombifyio/stackkits/internal/crypto"
)

const localEncryptedIssuedSecretCustodyKind = "LocalEncryptedIssuedSecretCustody"

// StoreEncryptedLocalIssuedSecret retains the existing owner-bound secret://
// custody and resolver, with encrypted material. It creates no owner/root key.
// A lifecycle caller must serialize updates and retain its own durable intent.
func StoreEncryptedLocalIssuedSecret(workspace, ref string, material []byte) error {
	return storeLocalIssuedSecret(workspace, ref, material, localEncryptedIssuedSecretCustodyKind)
}

// ResolveEncryptedLocalIssuedSecret refuses a legacy plaintext record rather
// than silently downgrading a consumer that requires encryption at rest.
func ResolveEncryptedLocalIssuedSecret(workspace, ref string) ([]byte, error) {
	digest, err := localSecretRefDigest(ref)
	if err != nil {
		return nil, err
	}
	record, err := loadLocalSecretCustody(workspace, digest)
	if err != nil {
		return nil, err
	}
	if record.Kind != localEncryptedIssuedSecretCustodyKind {
		return nil, errors.New("localevidence: encrypted issued custody required")
	}
	return decryptLocalIssuedSecret(workspace, record)
}

// This follows the established owner-derived OpenTofu wrapping-key pattern,
// with a separate domain and the full current node binding plus opaque ref.
func withIssuedSecretKey(workspace, refDigest string, use func([]byte) ([]byte, error)) ([]byte, error) {
	owner, err := LoadOwnerCustody(workspace)
	if err != nil {
		return nil, err
	}
	key, err := LoadOwnerKey(workspace)
	if err != nil {
		return nil, err
	}
	if key.OwnerRef != owner.OwnerRef || key.KeyID != owner.KeyID {
		return nil, errors.New("localevidence: issued secret owner mismatch")
	}
	seed := key.private.Seed()
	defer clear(seed)
	mac := hmac.New(sha256.New, seed)
	_, _ = mac.Write([]byte("stackkit.owner-encrypted-issued-secret/v1\x00"))
	identity, _ := json.Marshal(struct {
		Owner, Key, Ref string
		Binding         LocalBinding
	}{owner.OwnerRef, owner.KeyID, refDigest, owner.Binding})
	_, _ = mac.Write(identity)
	wrapping := mac.Sum(nil)
	defer clear(wrapping)
	return use(wrapping)
}

func encryptLocalIssuedSecret(workspace, ref string, plaintext []byte) ([]byte, error) {
	return withIssuedSecretKey(workspace, ref, func(key []byte) ([]byte, error) {
		return stackcrypto.EncryptWithPassphrase(plaintext, base64.RawStdEncoding.EncodeToString(key))
	})
}

func decryptLocalIssuedSecret(workspace string, record localSecretCustody) ([]byte, error) {
	ciphertext, err := base64.RawStdEncoding.Strict().DecodeString(record.Material)
	if err != nil {
		return nil, errors.New("localevidence: malformed encrypted issued secret")
	}
	plaintext, err := withIssuedSecretKey(workspace, record.RefDigest, func(key []byte) ([]byte, error) {
		return stackcrypto.DecryptWithPassphrase(ciphertext, base64.RawStdEncoding.EncodeToString(key))
	})
	if err != nil || len(plaintext) == 0 || len(plaintext) > maxIssuedSecretBytes {
		clear(plaintext)
		return nil, errors.New("localevidence: encrypted issued secret cannot be opened")
	}
	return plaintext, nil
}
