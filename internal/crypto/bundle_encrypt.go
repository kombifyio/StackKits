package crypto

import (
	"bytes"
	"fmt"
	"io"

	"filippo.io/age"
)

// EncryptWithPassphrase encrypts plaintext using age with a scrypt-passphrase recipient.
func EncryptWithPassphrase(plaintext []byte, pass string) ([]byte, error) {
	recipient, err := age.NewScryptRecipient(pass)
	if err != nil {
		return nil, fmt.Errorf("scrypt recipient: %w", err)
	}
	var buf bytes.Buffer
	w, err := age.Encrypt(&buf, recipient)
	if err != nil {
		return nil, fmt.Errorf("age encrypt: %w", err)
	}
	if _, err := w.Write(plaintext); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// DecryptWithPassphrase decrypts age-encrypted ciphertext using the given passphrase.
func DecryptWithPassphrase(ciphertext []byte, pass string) ([]byte, error) {
	identity, err := age.NewScryptIdentity(pass)
	if err != nil {
		return nil, fmt.Errorf("scrypt identity: %w", err)
	}
	r, err := age.Decrypt(bytes.NewReader(ciphertext), identity)
	if err != nil {
		return nil, fmt.Errorf("age decrypt: %w", err)
	}
	return io.ReadAll(r)
}
