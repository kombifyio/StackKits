// Package lcrypt is a byte-for-byte compatible implementation of Laravel's
// Illuminate\Encryption\Encrypter for the AES-256-CBC cipher that Coolify uses.
//
// Coolify stores secrets (environment variables, private keys, database
// passwords, ...) encrypted with the instance APP_KEY. Moving a single resource
// to another Coolify instance therefore means decrypting with the source key and
// re-encrypting with the target key. Working at the byte level keeps both
// encryptString() payloads and serialize()d encrypt() payloads intact.
package lcrypt

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Encrypter holds the current key first, followed by previous keys
// (APP_PREVIOUS_KEYS). Encryption always uses the first key.
type Encrypter struct {
	keys [][]byte
}

// ParseKey decodes an APP_KEY value ("base64:..." or a raw 32 byte string).
func ParseKey(appKey string) ([]byte, error) {
	appKey = strings.TrimSpace(appKey)
	appKey = strings.Trim(appKey, `"'`)
	if appKey == "" {
		return nil, errors.New("empty APP_KEY")
	}
	var raw []byte
	if strings.HasPrefix(appKey, "base64:") {
		b, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(appKey, "base64:"))
		if err != nil {
			return nil, fmt.Errorf("APP_KEY is not valid base64: %w", err)
		}
		raw = b
	} else {
		raw = []byte(appKey)
	}
	if len(raw) != 32 {
		return nil, fmt.Errorf("APP_KEY must be 32 bytes for AES-256-CBC, got %d", len(raw))
	}
	return raw, nil
}

// New builds an encrypter from APP_KEY and an optional comma separated
// APP_PREVIOUS_KEYS list. Invalid previous keys are skipped.
func New(appKey, previousKeys string) (*Encrypter, error) {
	k, err := ParseKey(appKey)
	if err != nil {
		return nil, err
	}
	e := &Encrypter{keys: [][]byte{k}}
	for _, p := range strings.Split(previousKeys, ",") {
		if strings.TrimSpace(p) == "" {
			continue
		}
		if pk, err := ParseKey(p); err == nil {
			e.keys = append(e.keys, pk)
		}
	}
	return e, nil
}

type payload struct {
	IV    string `json:"iv"`
	Value string `json:"value"`
	Mac   string `json:"mac"`
	Tag   string `json:"tag"`
}

// EncryptString mirrors Crypt::encryptString() (no PHP serialization).
func (e *Encrypter) EncryptString(plain []byte) (string, error) {
	key := e.keys[0]
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	iv := make([]byte, aes.BlockSize)
	if _, err := rand.Read(iv); err != nil {
		return "", err
	}
	padded := pkcs7Pad(plain, aes.BlockSize)
	ct := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ct, padded)

	ivB64 := base64.StdEncoding.EncodeToString(iv)
	valueB64 := base64.StdEncoding.EncodeToString(ct)
	mac := macFor(key, ivB64, valueB64)
	// Same field order and escaping as PHP json_encode(..., JSON_UNESCAPED_SLASHES).
	js := `{"iv":"` + ivB64 + `","value":"` + valueB64 + `","mac":"` + mac + `","tag":""}`
	return base64.StdEncoding.EncodeToString([]byte(js)), nil
}

// ErrNotEncrypted means the value is not a Laravel payload at all.
var ErrNotEncrypted = errors.New("not a Laravel encrypted payload")

// ErrWrongKey means the value is a Laravel payload but none of the keys can open it.
var ErrWrongKey = errors.New("payload was encrypted with a different APP_KEY")

// DecryptString mirrors Crypt::decryptString(): it returns the raw plaintext
// bytes, trying every configured key.
func (e *Encrypter) DecryptString(s string) ([]byte, error) {
	p, ok := parsePayload(s)
	if !ok {
		return nil, ErrNotEncrypted
	}
	if p.Tag != "" {
		return nil, errors.New("AEAD (GCM) payloads are not supported; Coolify uses AES-256-CBC")
	}
	iv, err := base64.StdEncoding.DecodeString(p.IV)
	if err != nil || len(iv) != aes.BlockSize {
		return nil, ErrNotEncrypted
	}
	ct, err := base64.StdEncoding.DecodeString(p.Value)
	if err != nil || len(ct) == 0 || len(ct)%aes.BlockSize != 0 {
		return nil, ErrNotEncrypted
	}
	for _, key := range e.keys {
		if !hmac.Equal([]byte(macFor(key, p.IV, p.Value)), []byte(p.Mac)) {
			continue
		}
		block, err := aes.NewCipher(key)
		if err != nil {
			return nil, err
		}
		out := make([]byte, len(ct))
		cipher.NewCBCDecrypter(block, iv).CryptBlocks(out, ct)
		plain, err := pkcs7Unpad(out, aes.BlockSize)
		if err != nil {
			return nil, err
		}
		return plain, nil
	}
	return nil, ErrWrongKey
}

// LooksEncrypted is a cheap structural check (no key needed).
func LooksEncrypted(s string) bool {
	_, ok := parsePayload(s)
	return ok
}

func parsePayload(s string) (payload, bool) {
	var p payload
	// The smallest possible payload is ~200 characters; anything shorter is plain text.
	if len(s) < 150 || !strings.HasPrefix(s, "eyJ") {
		return p, false
	}
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		// Laravel always pads, but be lenient with stripped padding.
		raw, err = base64.RawStdEncoding.DecodeString(strings.TrimRight(s, "="))
		if err != nil {
			return p, false
		}
	}
	if !bytes.HasPrefix(raw, []byte("{")) {
		return p, false
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return p, false
	}
	if p.IV == "" || p.Value == "" || (p.Mac == "" && p.Tag == "") {
		return p, false
	}
	return p, true
}

func macFor(key []byte, ivB64, valueB64 string) string {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(ivB64 + valueB64))
	return hex.EncodeToString(h.Sum(nil))
}

func pkcs7Pad(b []byte, size int) []byte {
	n := size - len(b)%size
	out := make([]byte, len(b)+n)
	copy(out, b)
	for i := len(b); i < len(out); i++ {
		out[i] = byte(n)
	}
	return out
}

func pkcs7Unpad(b []byte, size int) ([]byte, error) {
	if len(b) == 0 || len(b)%size != 0 {
		return nil, errors.New("invalid padding length")
	}
	n := int(b[len(b)-1])
	if n == 0 || n > size || n > len(b) {
		return nil, errors.New("invalid padding")
	}
	for _, c := range b[len(b)-n:] {
		if int(c) != n {
			return nil, errors.New("invalid padding")
		}
	}
	return b[:len(b)-n], nil
}
