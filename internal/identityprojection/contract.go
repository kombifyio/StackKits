// Package identityprojection owns the credential-free desired identity
// projection accepted by a standalone StackKits installation. Cloud or
// Techstack may sign a proposal, but only current local Owner custody can
// approve it for a later PocketID mutation.
package identityprojection

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/mail"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kombifyio/stackkits/internal/advancedcapability"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

const (
	SchemaVersion          = "stackkit.desired-identity-projection/v1"
	Audience               = "stackkit-local-identity"
	Kind                   = "DesiredIdentityProjection"
	maxDocumentBytes       = 64 * 1024
	maxLifetime            = 7 * 24 * time.Hour
	futureTolerance        = 5 * time.Minute
	signatureDomain        = SchemaVersion + "\x00"
	ProjectionActionUpsert = "upsert"
)

var (
	projectionIDPattern = regexp.MustCompile(`^identity-projection/[0-9a-f]{32}$`)
	ownerRefPattern     = regexp.MustCompile(`^owner/local/[0-9a-f]{32}$`)
	issuerPattern       = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)
	keyIDPattern        = regexp.MustCompile(`^ed25519://sha256/[0-9a-f]{64}$`)
	subjectPattern      = regexp.MustCompile(`^identity-source/[a-z0-9][a-z0-9._-]{0,127}$`)
	usernamePattern     = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	groupPattern        = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
)

type Profile struct {
	Username    string `json:"username"`
	Email       string `json:"email"`
	DisplayName string `json:"displayName"`
}

// Projection contains only declarative identity data and public signature
// metadata. Exact-field decoding rejects credentials, sessions, passkeys,
// private keys, admin secrets, URLs, and provider endpoints.
type Projection struct {
	Audience        string   `json:"audience"`
	ExpiresAt       string   `json:"expiresAt"`
	Groups          []string `json:"groups"`
	IssuedAt        string   `json:"issuedAt"`
	IssuerID        string   `json:"issuerId"`
	KeyID           string   `json:"keyId"`
	Kind            string   `json:"kind"`
	OwnerRef        string   `json:"ownerRef"`
	Profile         Profile  `json:"profile"`
	ProjectionID    string   `json:"projectionId"`
	RequestedAction string   `json:"requestedAction"`
	SchemaVersion   string   `json:"schemaVersion"`
	Signature       string   `json:"signature"`
	SubjectRef      string   `json:"subjectRef"`
}

type Verified struct {
	Projection Projection
	SHA256     string
	IssuedAt   time.Time
	ExpiresAt  time.Time
}

type Inspection struct {
	SchemaVersion    string    `json:"schemaVersion"`
	ProjectionID     string    `json:"projectionId"`
	ProjectionSHA256 string    `json:"projectionSHA256"`
	IssuerID         string    `json:"issuerId"`
	KeyID            string    `json:"keyId"`
	OwnerRef         string    `json:"ownerRef"`
	SubjectRef       string    `json:"subjectRef"`
	Profile          Profile   `json:"profile"`
	Groups           []string  `json:"groups"`
	IssuedAt         time.Time `json:"issuedAt"`
	ExpiresAt        time.Time `json:"expiresAt"`
	CredentialFree   bool      `json:"credentialFree"`
}

func Verify(
	raw []byte,
	trust advancedcapability.TrustBundle,
	expectedOwnerRef string,
	now time.Time,
	allowExpired bool,
) (Verified, error) {
	document, err := decodeStrict(raw)
	if err != nil {
		return Verified{}, err
	}
	if err := validateProjection(document, expectedOwnerRef); err != nil {
		return Verified{}, err
	}
	issuedAt, err := parseCanonicalTime(document.IssuedAt)
	if err != nil {
		return Verified{}, fmt.Errorf("identityprojection: issuedAt %w", err)
	}
	expiresAt, err := parseCanonicalTime(document.ExpiresAt)
	if err != nil {
		return Verified{}, fmt.Errorf("identityprojection: expiresAt %w", err)
	}
	if !expiresAt.After(issuedAt) || expiresAt.Sub(issuedAt) > maxLifetime {
		return Verified{}, errors.New("identityprojection: projection lifetime must be positive and at most seven days")
	}
	if now.IsZero() {
		return Verified{}, errors.New("identityprojection: trusted verification time is required")
	}
	now = now.UTC()
	if issuedAt.After(now.Add(futureTolerance)) {
		return Verified{}, errors.New("identityprojection: projection is not yet valid")
	}
	if !allowExpired && !expiresAt.After(now) {
		return Verified{}, errors.New("identityprojection: projection has expired")
	}
	signature, err := base64.RawStdEncoding.Strict().DecodeString(document.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return Verified{}, errors.New("identityprojection: projection signature is malformed")
	}
	unsigned, err := document.canonical(false)
	if err != nil {
		return Verified{}, err
	}
	digest := sha256.Sum256(append([]byte(signatureDomain), unsigned...))
	if err := advancedcapability.VerifyIdentityProjectionDigest(
		&trust, document.IssuerID, document.KeyID, digest[:], signature,
	); err != nil {
		return Verified{}, err
	}
	canonical, err := document.canonical(true)
	if err != nil || !bytes.Equal(raw, canonical) {
		return Verified{}, errors.New("identityprojection: projection must be exact canonical JSON")
	}
	rawDigest := sha256.Sum256(raw)
	return Verified{
		Projection: document,
		SHA256:     "sha256:" + hex.EncodeToString(rawDigest[:]),
		IssuedAt:   issuedAt,
		ExpiresAt:  expiresAt,
	}, nil
}

func (verified Verified) Inspection() Inspection {
	return Inspection{
		SchemaVersion:    "stackkit.identity-projection-inspection/v1",
		ProjectionID:     verified.Projection.ProjectionID,
		ProjectionSHA256: verified.SHA256,
		IssuerID:         verified.Projection.IssuerID,
		KeyID:            verified.Projection.KeyID,
		OwnerRef:         verified.Projection.OwnerRef,
		SubjectRef:       verified.Projection.SubjectRef,
		Profile:          verified.Projection.Profile,
		Groups:           slices.Clone(verified.Projection.Groups),
		IssuedAt:         verified.IssuedAt,
		ExpiresAt:        verified.ExpiresAt,
		CredentialFree:   true,
	}
}

func (projection Projection) MarshalCanonical() ([]byte, error) {
	return projection.canonical(true)
}

func (projection Projection) canonical(includeSignature bool) ([]byte, error) {
	copy := projection
	if !includeSignature {
		copy.Signature = ""
	}
	return resolvedplan.CanonicalJSON(copy)
}

func decodeStrict(raw []byte) (Projection, error) {
	if len(raw) == 0 || len(raw) > maxDocumentBytes || !utf8.Valid(raw) {
		return Projection{}, errors.New("identityprojection: projection must be bounded UTF-8 JSON")
	}
	if err := rejectDuplicateJSONNames(raw); err != nil {
		return Projection{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var document Projection
	if err := decoder.Decode(&document); err != nil {
		return Projection{}, fmt.Errorf("identityprojection: decode projection: %w", err)
	}
	if token, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err != nil {
			return Projection{}, err
		}
		return Projection{}, fmt.Errorf("identityprojection: trailing JSON token %v", token)
	}
	return document, nil
}

func validateProjection(document Projection, expectedOwnerRef string) error {
	if document.SchemaVersion != SchemaVersion || document.Kind != Kind ||
		document.Audience != Audience || document.RequestedAction != ProjectionActionUpsert {
		return errors.New("identityprojection: projection contract identity is invalid")
	}
	if !projectionIDPattern.MatchString(document.ProjectionID) ||
		!ownerRefPattern.MatchString(document.OwnerRef) ||
		document.OwnerRef != expectedOwnerRef ||
		!issuerPattern.MatchString(document.IssuerID) ||
		!keyIDPattern.MatchString(document.KeyID) ||
		!subjectPattern.MatchString(document.SubjectRef) {
		return errors.New("identityprojection: projection scope or issuer identity is invalid")
	}
	if !usernamePattern.MatchString(document.Profile.Username) ||
		strings.TrimSpace(document.Profile.DisplayName) != document.Profile.DisplayName ||
		len(document.Profile.DisplayName) < 1 || len(document.Profile.DisplayName) > 128 ||
		strings.ContainsAny(document.Profile.DisplayName, "\x00\r\n") {
		return errors.New("identityprojection: desired PocketID profile is invalid")
	}
	address, err := mail.ParseAddress(document.Profile.Email)
	if err != nil || address.Address != document.Profile.Email ||
		address.Name != "" || len(document.Profile.Email) > 254 {
		return errors.New("identityprojection: desired PocketID email is invalid")
	}
	if len(document.Groups) < 1 || len(document.Groups) > 16 ||
		!slices.IsSorted(document.Groups) {
		return errors.New("identityprojection: desired groups must be a sorted non-empty bounded set")
	}
	for index, group := range document.Groups {
		if !groupPattern.MatchString(group) || group == "admins" || group == "owners" ||
			(index > 0 && group == document.Groups[index-1]) {
			return errors.New("identityprojection: desired groups contain a reserved, duplicate, or invalid group")
		}
	}
	return nil
}

func parseCanonicalTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil || parsed.Location() != time.UTC || parsed.Nanosecond() != 0 ||
		parsed.Format(time.RFC3339) != value {
		return time.Time{}, errors.New("must be canonical UTC RFC3339 with exact-second precision")
	}
	return parsed, nil
}

func rejectDuplicateJSONNames(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := walkUniqueJSONValue(decoder, "$"); err != nil {
		return fmt.Errorf("identityprojection: %w", err)
	}
	if token, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err != nil {
			return err
		}
		return fmt.Errorf("identityprojection: trailing token %v", token)
	}
	return nil
}

func walkUniqueJSONValue(decoder *json.Decoder, path string) error {
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("decode JSON at %s: %w", path, err)
	}
	delimiter, composite := token.(json.Delim)
	if !composite {
		return nil
	}
	switch delimiter {
	case '{':
		seen := map[string]struct{}{}
		for decoder.More() {
			nameToken, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := nameToken.(string)
			if !ok {
				return fmt.Errorf("non-string object name at %s", path)
			}
			if _, duplicate := seen[name]; duplicate {
				return fmt.Errorf("duplicate JSON name %q at %s", name, path)
			}
			seen[name] = struct{}{}
			if err := walkUniqueJSONValue(decoder, path+"."+name); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return fmt.Errorf("unterminated object at %s", path)
		}
	case '[':
		for index := 0; decoder.More(); index++ {
			if err := walkUniqueJSONValue(decoder, fmt.Sprintf("%s[%d]", path, index)); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return fmt.Errorf("unterminated array at %s", path)
		}
	default:
		return fmt.Errorf("invalid JSON delimiter at %s", path)
	}
	return nil
}
