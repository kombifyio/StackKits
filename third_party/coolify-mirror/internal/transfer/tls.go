package transfer

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"strings"
	"time"
)

// Shares are served over TLS only. The certificate is self-signed and made
// for each share; the link carries its public-key fingerprint ("pin", in the
// #fragment next to the key), and the downloader accepts exactly that key.
// So the transfer is encrypted and authenticated without a CA or a domain.
//
// Behind Coolify's Traefik the connection is passed through untouched (TCP
// router with TLS passthrough, selected by SNI), so TLS still ends in our own
// process. The SNI name is derived from the share token.

// SNIName is the TLS server name used for a share token.
func SNIName(token string) string { return token + ".cm.invalid" }

// Cert is a share certificate.
type Cert struct {
	TLS     tls.Certificate
	CertPEM []byte
	KeyPEM  []byte
	Pin     string // base64 sha256 of the SubjectPublicKeyInfo (curl --pinnedpubkey format)
}

// NewCert creates a self-signed certificate valid for a few days.
func NewCert(token string) (*Cert, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
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
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	kder, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	c := &Cert{
		CertPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		KeyPEM:  pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kder}),
	}
	if c.TLS, err = tls.X509KeyPair(c.CertPEM, c.KeyPEM); err != nil {
		return nil, err
	}
	leaf, _ := x509.ParseCertificate(der)
	c.Pin = spkiPin(leaf)
	return c, nil
}

// LoadCert reads a certificate written by NewCert.
func LoadCert(certPEM, keyPEM []byte) (*Cert, error) {
	t, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, err
	}
	leaf, err := x509.ParseCertificate(t.Certificate[0])
	if err != nil {
		return nil, err
	}
	return &Cert{TLS: t, CertPEM: certPEM, KeyPEM: keyPEM, Pin: spkiPin(leaf)}, nil
}

func spkiPin(c *x509.Certificate) string {
	sum := sha256.Sum256(c.RawSubjectPublicKeyInfo)
	return base64.StdEncoding.EncodeToString(sum[:])
}

func (c *Cert) serverConfig() *tls.Config {
	return &tls.Config{Certificates: []tls.Certificate{c.TLS}, MinVersion: tls.VersionTLS12}
}

// pinnedConfig accepts only a server presenting the pinned public key.
func pinnedConfig(pin, sni string) (*tls.Config, error) {
	want, err := decodePin(pin)
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: sni,
		// Chain/name checks are replaced by the exact public-key pin below.
		InsecureSkipVerify: true, //nolint:gosec
		VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
			if len(raw) == 0 {
				return errors.New("the source server sent no certificate")
			}
			c, err := x509.ParseCertificate(raw[0])
			if err != nil {
				return err
			}
			got := sha256.Sum256(c.RawSubjectPublicKeyInfo)
			if subtle.ConstantTimeCompare(got[:], want) != 1 {
				return errors.New("the server's certificate does not match the link (pin mismatch) - someone may be intercepting the connection, or the link is from another share")
			}
			return nil
		},
	}, nil
}

func decodePin(p string) ([]byte, error) {
	// Query parsing turns an unescaped "+" into a space.
	p = strings.ReplaceAll(strings.TrimSpace(p), " ", "+")
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.URLEncoding, base64.RawURLEncoding, base64.RawStdEncoding} {
		if b, err := enc.DecodeString(p); err == nil && len(b) == sha256.Size {
			return b, nil
		}
	}
	return nil, errors.New("invalid certificate pin in the link")
}

// Link builds the share link: https, token path, and in the fragment (never
// sent over the network) the decryption key and the certificate pin.
func Link(hostPort, token, key, pin string) string {
	return fmt.Sprintf("https://%s/cm/%s/%s#key=%s&pin=%s", hostPort, token, BackupName, key, url.QueryEscape(pin))
}

// ToolCommand returns a one-liner that downloads the tool binary from the
// share over the pinned TLS connection and restores the share code with it.
func ToolCommand(hostPort, token, pin, code string) string {
	host, port, err := net.SplitHostPort(hostPort)
	if err != nil {
		host, port = strings.Trim(hostPort, "[]"), "443"
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	sni := SNIName(token)
	u := "https://" + sni
	if port != "443" {
		u += ":" + port
	}
	return fmt.Sprintf("curl -fsSLk --pinnedpubkey 'sha256//%s' --resolve '%s:%s:%s' '%s/cm/%s/%s' -o coolify-mirror && chmod +x coolify-mirror && sudo ./coolify-mirror restore %s",
		pin, sni, port, host, u, token, ToolName, code)
}

// tokenFromURL extracts the share token from a /cm/<token>/... URL.
func tokenFromURL(u *url.URL) string {
	parts := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
	if len(parts) >= 2 && parts[0] == "cm" {
		return parts[1]
	}
	return ""
}
