package localevidence

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"time"
)

const (
	// HomeEnrollmentTrustRelPath is the host-written, Owner-signed statement
	// of the LAN enrollment listener (homelab identity target §5, §6.2): the
	// core serves it inside the public bootstrap but cannot make or change it.
	HomeEnrollmentTrustRelPath = HomeAuthorityCustodyRelDir + "/enrollment-trust.json"
	// HomeEnrollmentTrustSchema names the statement.
	HomeEnrollmentTrustSchema = "stackkit.home-enrollment-trust/v1"
	// HomeEnrollmentTrustLifetime bounds one statement; the host re-signs well before.
	HomeEnrollmentTrustLifetime = 24 * time.Hour

	homeEnrollmentTrustDomain   = "stackkit.home-enrollment-trust/v1\x00"
	homeEnrollmentTrustMaxBytes = 64 << 10
)

// HomeEnrollmentTrustStatement is what the Owner key signs: the LAN listener
// URL and the step-ca root a native client pins for it, bound to this Home.
type HomeEnrollmentTrustStatement struct {
	Schema           string    `json:"schema"`
	HomeSiteRef      string    `json:"homeSiteRef"`
	EnrollmentURL    string    `json:"enrollmentUrl"`
	HubEnrollmentURL string    `json:"hubEnrollmentUrl,omitempty"`
	TLSServerName    string    `json:"tlsServerName"`
	CARootSHA256     string    `json:"caRootSha256"`
	CARootPEM        string    `json:"caRootPem"`
	IssuedAt         time.Time `json:"issuedAt"`
	ExpiresAt        time.Time `json:"expiresAt"`
}

// HomeEnrollmentTrust is the signed statement as it travels in the bootstrap.
// Statement is the exact JSON text that was signed (a verifier checks these
// bytes, never a re-serialization); OwnerPublicKey is the raw Ed25519 key,
// whose Signature.KeyID is its SHA-256 identifier. A client trusts it only for
// an Owner key it holds from a source the public origin cannot rewrite.
type HomeEnrollmentTrust struct {
	Statement      string                    `json:"statement"`
	OwnerPublicKey string                    `json:"ownerPublicKey"`
	Signature      OwnerPolicyStateSignature `json:"signature"`
}

// SignHomeEnrollmentTrust signs the statement with the Owner key (host only).
func SignHomeEnrollmentTrust(workspaceRoot string, statement HomeEnrollmentTrustStatement) (HomeEnrollmentTrust, error) {
	statement.Schema = HomeEnrollmentTrustSchema
	statement.IssuedAt, statement.ExpiresAt = statement.IssuedAt.UTC(), statement.ExpiresAt.UTC()
	if !statement.ExpiresAt.After(statement.IssuedAt) || statement.ExpiresAt.Sub(statement.IssuedAt) > HomeEnrollmentTrustLifetime {
		return HomeEnrollmentTrust{}, errors.New("localevidence: the enrollment trust lifetime is out of range")
	}
	raw, err := json.Marshal(statement)
	if err != nil {
		return HomeEnrollmentTrust{}, err
	}
	value, ownerRef, keyID, err := signOwnerRestore(workspaceRoot, raw, homeEnrollmentTrustDomain, "Home enrollment trust")
	if err != nil {
		return HomeEnrollmentTrust{}, err
	}
	key, err := LoadOwnerKey(workspaceRoot)
	if err != nil {
		return HomeEnrollmentTrust{}, err
	}
	return HomeEnrollmentTrust{
		Statement: string(raw), OwnerPublicKey: base64.RawStdEncoding.EncodeToString(key.Public()),
		Signature: OwnerPolicyStateSignature{OwnerRef: ownerRef, KeyID: keyID, Value: value},
	}, nil
}

// VerifyHomeEnrollmentTrust checks the signature against this Authority's own
// Owner key and the statement against this Home, and returns the statement.
func (r HomeAuthorityPublic) VerifyHomeEnrollmentTrust(trust HomeEnrollmentTrust, now time.Time) (HomeEnrollmentTrustStatement, error) {
	invalid := errors.New("localevidence: the Home enrollment trust does not verify")
	public, err := r.ownerKey()
	if err != nil || trust.OwnerPublicKey != r.OwnerPublicKey ||
		!verifyPinnedOwnerSignature(homeEnrollmentTrustDomain, []byte(trust.Statement), trust.Signature, r.OwnerRef, r.KeyID, ed25519.PublicKey(public)) {
		return HomeEnrollmentTrustStatement{}, invalid
	}
	var statement HomeEnrollmentTrustStatement
	decoder := json.NewDecoder(bytes.NewReader([]byte(trust.Statement)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&statement) != nil || decoder.Decode(new(any)) != io.EOF ||
		statement.Schema != HomeEnrollmentTrustSchema || statement.HomeSiteRef != r.SiteRef || !statement.ExpiresAt.After(now) {
		return HomeEnrollmentTrustStatement{}, invalid
	}
	return statement, nil
}

// OwnerKeyFingerprint is the unpadded base64url SHA-256 of the raw Owner key.
// The Owner hands it to a native client through a channel no edge can rewrite
// (the enrollment address the local console prints, `#okf=<fingerprint>`); the
// client takes the Owner key from the public bootstrap only when it matches.
func (r HomeAuthorityPublic) OwnerKeyFingerprint() (string, error) {
	public, err := r.ownerKey()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(public)
	return base64.RawURLEncoding.EncodeToString(sum[:]), nil
}

// WriteHomeEnrollmentTrust stores the signed statement (host only).
func WriteHomeEnrollmentTrust(workspaceRoot string, trust HomeEnrollmentTrust) error {
	path, err := confinedCustodyPath(workspaceRoot, HomeEnrollmentTrustRelPath)
	if err != nil {
		return err
	}
	return writePrivateJSON(path, trust)
}

// LoadHomeEnrollmentTrust reads the stored statement; os.ErrNotExist when the host wrote none.
func LoadHomeEnrollmentTrust(workspaceRoot string) (HomeEnrollmentTrust, error) {
	path, err := confinedCustodyPath(workspaceRoot, HomeEnrollmentTrustRelPath)
	if err != nil {
		return HomeEnrollmentTrust{}, err
	}
	file, err := os.Open(path) //nolint:gosec // fixed path below the explicit workspace
	if err != nil {
		return HomeEnrollmentTrust{}, err
	}
	defer func() { _ = file.Close() }()
	raw, err := io.ReadAll(io.LimitReader(file, homeEnrollmentTrustMaxBytes+1))
	if err != nil || len(raw) > homeEnrollmentTrustMaxBytes {
		return HomeEnrollmentTrust{}, errors.New("localevidence: the Home enrollment trust is unreadable")
	}
	var trust HomeEnrollmentTrust
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&trust) != nil || decoder.Decode(new(any)) != io.EOF {
		return HomeEnrollmentTrust{}, errors.New("localevidence: the Home enrollment trust is malformed")
	}
	return trust, nil
}
