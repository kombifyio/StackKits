package transfer

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// A share code is short enough to type: "HOST[:PORT]/xxxx-xxxx-…". Its secret
// (128 bits) is the only thing that has to travel; everything else is derived
// from it:
//
//   - the share token (the URL path and the TLS server name),
//   - the TLS key of the share (Ed25519), so the downloader knows exactly
//     which certificate to accept - the code works like the pin of a link,
//   - the key that encrypts the backup's own key, which the share serves at
//     /cm/<token>/key (only someone holding the code can read it).
//
// Without a running share the code is useless: the backup key is not in it.

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// codeKeyPrefix marks, in the key returned by ParseSource, a key that still
// has to be fetched from the share with the code's secret (see FetchKey).
const codeKeyPrefix = "code:"

// NewSecret returns a random share secret (base32, lower case).
func NewSecret() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return strings.ToLower(b32.EncodeToString(b))
}

func secretBytes(secret string) ([]byte, error) {
	s := strings.ToUpper(strings.NewReplacer("-", "", " ", "", "_", "").Replace(strings.TrimSpace(secret)))
	b, err := b32.DecodeString(s)
	if err != nil || len(b) != 16 {
		return nil, errors.New("invalid share code - copy it exactly as shown on the source server")
	}
	return b, nil
}

func derive(secret []byte, label string) []byte {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte("coolify-mirror/" + label))
	return m.Sum(nil)
}

// CodeToken is the share token of a secret.
func CodeToken(secret string) (string, error) {
	b, err := secretBytes(secret)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(derive(b, "token"))[:32], nil
}

func codeKey(secret []byte) ed25519.PrivateKey {
	return ed25519.NewKeyFromSeed(derive(secret, "tls"))
}

// CodePin is the pin (base64 SHA-256 of the public key) of a secret's share.
func CodePin(secret string) (string, error) {
	b, err := secretBytes(secret)
	if err != nil {
		return "", err
	}
	spki, err := x509.MarshalPKIXPublicKey(codeKey(b).Public())
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(spki)
	return base64.StdEncoding.EncodeToString(sum[:]), nil
}

// NewCodeCert creates the share certificate of a secret (its key is derived
// from the secret; the certificate itself is fresh).
func NewCodeCert(secret string) (*Cert, error) {
	b, err := secretBytes(secret)
	if err != nil {
		return nil, err
	}
	token, _ := CodeToken(secret)
	key := codeKey(b)
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "coolify-mirror share"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(8 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{SNIName(token)},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, key.Public(), key)
	if err != nil {
		return nil, err
	}
	kder, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, err
	}
	return LoadCert(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kder}))
}

// WrapKey encrypts a backup key for /cm/<token>/key.
func WrapKey(secret, backupKey string) ([]byte, error) {
	b, err := secretBytes(secret)
	if err != nil {
		return nil, err
	}
	gcm, err := keyCipher(b)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	_, _ = rand.Read(nonce)
	out := gcm.Seal(nonce, nonce, []byte(backupKey), []byte("backup-key"))
	return []byte(base64.StdEncoding.EncodeToString(out)), nil
}

func unwrapKey(secret []byte, blob []byte) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(blob)))
	if err != nil {
		return "", err
	}
	gcm, err := keyCipher(secret)
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", errors.New("short key")
	}
	plain, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], []byte("backup-key"))
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func keyCipher(secret []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(derive(secret, "backup-key"))
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// FormatCode is the code shown to the user: host (port only when not 443)
// and the secret in groups of four.
func FormatCode(hostPort, secret string) string {
	host := strings.TrimSuffix(hostPort, ":443")
	s := strings.ToLower(strings.ReplaceAll(secret, "-", ""))
	var g []string
	for len(s) > 4 {
		g = append(g, s[:4])
		s = s[4:]
	}
	g = append(g, s)
	return host + "/" + strings.Join(g, "-")
}

var reCode = regexp.MustCompile(`^(\[[0-9A-Fa-f:.]+\]|[A-Za-z0-9.-]+)(:\d{1,5})?/([A-Za-z2-7]{4}(-?[A-Za-z2-7]{1,4}){5,7})$`)

// parseCode turns "HOST[:PORT]/SECRET" into the share URL (with its pin, like
// a link) and the secret. ok is false when s is not a code.
func parseCode(s string) (location, secret string, ok bool, err error) {
	m := reCode.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return "", "", false, nil
	}
	secret = m[3]
	token, err := CodeToken(secret)
	if err != nil {
		return "", "", true, err
	}
	pin, err := CodePin(secret)
	if err != nil {
		return "", "", true, err
	}
	host, port := m[1], strings.TrimPrefix(m[2], ":")
	if port == "" {
		port = "443"
	}
	hp := net.JoinHostPort(strings.Trim(host, "[]"), port)
	return fmt.Sprintf("https://%s/cm/%s/%s#pin=%s", hp, token, BackupName, url.QueryEscape(pin)), secret, true, nil
}

// FetchKey resolves a key returned by ParseSource for a share code: it reads
// the encrypted backup key from the share and decrypts it with the code. Other
// keys are returned unchanged.
func FetchKey(ctx context.Context, link, key string) (string, error) {
	secret, isCode := strings.CutPrefix(key, codeKeyPrefix)
	if !isCode {
		return key, nil
	}
	sb, err := secretBytes(secret)
	if err != nil {
		return "", err
	}
	client, loc, err := newClient(link)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(loc, BackupName)+"key", nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", explainNetErr(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone {
		return "", errors.New("the source server does not share this backup anymore - share it again there and use the new code")
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("reading the key from the source server: HTTP %s", resp.Status)
	}
	blob, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return "", err
	}
	k, err := unwrapKey(sb, blob)
	if err != nil {
		return "", errors.New("the share code does not match this share - copy it again from the source server")
	}
	return k, nil
}
