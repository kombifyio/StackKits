package localevidence

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
)

const (
	TinyAuthPocketIDBindingAPIVersion = "stackkit.tinyauth-pocketid-binding/v1"
	tinyAuthPocketIDBindingRelDir     = ".stackkit/custody/tinyauth-pocketid"
	tinyAuthPocketIDEnvRelPath        = "tinyauth.env"
	tinyAuthPocketIDRecordRelPath     = "binding.json"
	tinyAuthPocketIDBindingDomain     = "stackkit.tinyauth-pocketid-binding/v1\x00"
	TinyAuthPocketIDClientID          = "stackkit-tinyauth"
)

func TinyAuthPocketIDCallbackURL(domain string) string {
	return "http://auth." + domain + "/api/oauth/callback/pocketid"
}

var ErrTinyAuthPocketIDBindingMissing = errors.New("localevidence: no TinyAuth PocketID binding")

type TinyAuthPocketIDBindingRequest struct {
	ClientID     string
	ClientSecret string
	GroupIDs     []string
}

// TinyAuthPocketIDBinding is the owner-signed, secret-free receipt for the
// private TinyAuth OAuth environment installed after PocketID creates the
// client secret. The secret itself is only represented by an owner-keyed MAC.
type TinyAuthPocketIDBinding struct {
	APIVersion  string    `json:"apiVersion"`
	Kind        string    `json:"kind"`
	OwnerRef    string    `json:"ownerRef"`
	KeyID       string    `json:"keyId"`
	ClientID    string    `json:"clientId"`
	CallbackURL string    `json:"callbackUrl"`
	GroupIDs    []string  `json:"groupIds"`
	EnvMAC      string    `json:"envMac"`
	BoundAt     time.Time `json:"boundAt"`
	Signature   string    `json:"signature"`
}

func BindBasementTinyAuthPocketID(
	workspaceRoot string,
	request TinyAuthPocketIDBindingRequest,
) (TinyAuthPocketIDBinding, error) {
	runtimeCustody, err := LoadBasementRuntimeCustody(workspaceRoot)
	if err != nil {
		return TinyAuthPocketIDBinding{}, err
	}
	owner, err := LoadOwnerCustody(workspaceRoot)
	if err != nil {
		return TinyAuthPocketIDBinding{}, err
	}
	request.ClientID = strings.TrimSpace(request.ClientID)
	request.ClientSecret = strings.TrimSpace(request.ClientSecret)
	request.GroupIDs = normalizeTinyAuthGroupIDs(request.GroupIDs)
	if request.ClientID != TinyAuthPocketIDClientID || len(request.ClientSecret) < 32 ||
		len(request.GroupIDs) != 2 {
		return TinyAuthPocketIDBinding{}, errors.New("localevidence: TinyAuth PocketID binding input is incomplete")
	}
	if existing, loadErr := LoadBasementTinyAuthPocketIDBinding(workspaceRoot); loadErr == nil {
		if existing.ClientID != request.ClientID || !slices.Equal(existing.GroupIDs, request.GroupIDs) {
			return TinyAuthPocketIDBinding{}, errors.New("localevidence: TinyAuth PocketID binding differs from established custody")
		}
		return existing, nil
	} else if !errors.Is(loadErr, ErrTinyAuthPocketIDBindingMissing) {
		return TinyAuthPocketIDBinding{}, loadErr
	}

	key, err := LoadOwnerKey(workspaceRoot)
	if err != nil {
		return TinyAuthPocketIDBinding{}, err
	}
	callbackURL := TinyAuthPocketIDCallbackURL(runtimeCustody.Domain)
	env := renderTinyAuthPocketIDEnvironment(runtimeCustody.Domain, owner.PocketID.Email, request.ClientID, request.ClientSecret)
	record := TinyAuthPocketIDBinding{
		APIVersion:  TinyAuthPocketIDBindingAPIVersion,
		Kind:        "TinyAuthPocketIDBinding",
		OwnerRef:    owner.OwnerRef,
		KeyID:       owner.KeyID,
		ClientID:    request.ClientID,
		CallbackURL: callbackURL,
		GroupIDs:    append([]string(nil), request.GroupIDs...),
		EnvMAC:      basementRuntimeFileMAC(key, tinyAuthPocketIDEnvRelPath, env),
		BoundAt:     time.Now().UTC().Truncate(time.Second),
	}
	signingBytes, err := tinyAuthPocketIDSigningBytes(record)
	if err != nil {
		return TinyAuthPocketIDBinding{}, err
	}
	record.Signature = base64.RawStdEncoding.EncodeToString(ed25519.Sign(key.private, signingBytes))
	raw, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return TinyAuthPocketIDBinding{}, fmt.Errorf("localevidence: encode TinyAuth PocketID binding: %w", err)
	}

	finalDirectory, err := confinedCustodyPath(workspaceRoot, tinyAuthPocketIDBindingRelDir)
	if err != nil {
		return TinyAuthPocketIDBinding{}, err
	}
	if _, statErr := os.Lstat(finalDirectory); statErr == nil {
		return TinyAuthPocketIDBinding{}, errors.New("localevidence: TinyAuth PocketID custody exists without a valid binding")
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return TinyAuthPocketIDBinding{}, fmt.Errorf("localevidence: inspect TinyAuth PocketID custody: %w", statErr)
	}
	parent := filepath.Dir(finalDirectory)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return TinyAuthPocketIDBinding{}, fmt.Errorf("localevidence: create TinyAuth PocketID custody parent: %w", err)
	}
	stage, err := os.MkdirTemp(parent, ".tinyauth-pocketid-*")
	if err != nil {
		return TinyAuthPocketIDBinding{}, fmt.Errorf("localevidence: create TinyAuth PocketID transaction: %w", err)
	}
	defer func() { _ = os.RemoveAll(stage) }()
	if err := os.Chmod(stage, 0o700); err != nil {
		return TinyAuthPocketIDBinding{}, fmt.Errorf("localevidence: restrict TinyAuth PocketID transaction: %w", err)
	}
	if err := writePrivateRuntimeFile(filepath.Join(stage, tinyAuthPocketIDEnvRelPath), env); err != nil {
		return TinyAuthPocketIDBinding{}, fmt.Errorf("localevidence: persist private TinyAuth PocketID environment: %w", err)
	}
	if err := writePrivateRuntimeFile(filepath.Join(stage, tinyAuthPocketIDRecordRelPath), raw); err != nil {
		return TinyAuthPocketIDBinding{}, fmt.Errorf("localevidence: persist TinyAuth PocketID binding: %w", err)
	}
	if err := os.Rename(stage, finalDirectory); err != nil {
		return TinyAuthPocketIDBinding{}, fmt.Errorf("localevidence: atomically install TinyAuth PocketID custody: %w", err)
	}
	return LoadBasementTinyAuthPocketIDBinding(workspaceRoot)
}

func LoadBasementTinyAuthPocketIDBinding(workspaceRoot string) (TinyAuthPocketIDBinding, error) {
	directory, err := confinedCustodyPath(workspaceRoot, tinyAuthPocketIDBindingRelDir)
	if err != nil {
		return TinyAuthPocketIDBinding{}, err
	}
	recordPath := filepath.Join(directory, tinyAuthPocketIDRecordRelPath)
	raw, err := os.ReadFile(recordPath) //nolint:gosec // fixed custody path
	if errors.Is(err, os.ErrNotExist) {
		return TinyAuthPocketIDBinding{}, ErrTinyAuthPocketIDBindingMissing
	}
	if err != nil {
		return TinyAuthPocketIDBinding{}, fmt.Errorf("localevidence: read TinyAuth PocketID binding: %w", err)
	}
	if err := requirePrivateRuntimeFile(recordPath); err != nil {
		return TinyAuthPocketIDBinding{}, err
	}
	var record TinyAuthPocketIDBinding
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		return TinyAuthPocketIDBinding{}, errors.New("localevidence: TinyAuth PocketID binding is malformed")
	}
	canonical, err := json.MarshalIndent(record, "", "  ")
	if err != nil || !bytes.Equal(raw, canonical) {
		return TinyAuthPocketIDBinding{}, errors.New("localevidence: TinyAuth PocketID binding is not canonical")
	}
	runtimeCustody, err := LoadBasementRuntimeCustody(workspaceRoot)
	if err != nil {
		return TinyAuthPocketIDBinding{}, err
	}
	owner, err := LoadOwnerCustody(workspaceRoot)
	if err != nil {
		return TinyAuthPocketIDBinding{}, err
	}
	key, err := LoadOwnerKey(workspaceRoot)
	if err != nil {
		return TinyAuthPocketIDBinding{}, err
	}
	record.GroupIDs = normalizeTinyAuthGroupIDs(record.GroupIDs)
	if record.APIVersion != TinyAuthPocketIDBindingAPIVersion ||
		record.Kind != "TinyAuthPocketIDBinding" ||
		record.OwnerRef != owner.OwnerRef || record.KeyID != owner.KeyID ||
		record.ClientID != TinyAuthPocketIDClientID ||
		record.CallbackURL != TinyAuthPocketIDCallbackURL(runtimeCustody.Domain) ||
		len(record.GroupIDs) != 2 || record.BoundAt.IsZero() ||
		record.BoundAt.Location() != time.UTC {
		return TinyAuthPocketIDBinding{}, errors.New("localevidence: TinyAuth PocketID binding is not bound to established custody")
	}
	signature, err := base64.RawStdEncoding.Strict().DecodeString(record.Signature)
	signingBytes, signingErr := tinyAuthPocketIDSigningBytes(record)
	if err != nil || signingErr != nil || len(signature) != ed25519.SignatureSize ||
		!ed25519.Verify(key.Public(), signingBytes, signature) {
		return TinyAuthPocketIDBinding{}, errors.New("localevidence: TinyAuth PocketID binding signature does not verify")
	}
	envPath := filepath.Join(directory, tinyAuthPocketIDEnvRelPath)
	env, err := os.ReadFile(envPath) //nolint:gosec // fixed custody path
	if err != nil || requirePrivateRuntimeFile(envPath) != nil ||
		record.EnvMAC != basementRuntimeFileMAC(key, tinyAuthPocketIDEnvRelPath, env) ||
		!validTinyAuthPocketIDEnvironment(env, runtimeCustody.Domain, owner.PocketID.Email, record.ClientID) {
		return TinyAuthPocketIDBinding{}, errors.New("localevidence: private TinyAuth PocketID environment does not verify")
	}
	return record, nil
}

func normalizeTinyAuthGroupIDs(input []string) []string {
	result := make([]string, 0, len(input))
	for _, value := range input {
		if value = strings.TrimSpace(value); value != "" && !strings.ContainsAny(value, "/?#\\\x00") {
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return slices.Compact(result)
}

func renderTinyAuthPocketIDEnvironment(domain, email, clientID, clientSecret string) []byte {
	return []byte(strings.Join([]string{
		"TINYAUTH_OAUTH_PROVIDERS_POCKETID_CLIENTID=" + clientID,
		"TINYAUTH_OAUTH_PROVIDERS_POCKETID_CLIENTSECRET=" + clientSecret,
		"TINYAUTH_OAUTH_PROVIDERS_POCKETID_AUTHURL=http://id." + domain + "/authorize",
		"TINYAUTH_OAUTH_PROVIDERS_POCKETID_TOKENURL=http://pocketid:1411/api/oidc/token",
		"TINYAUTH_OAUTH_PROVIDERS_POCKETID_USERINFOURL=http://pocketid:1411/api/oidc/userinfo",
		"TINYAUTH_OAUTH_PROVIDERS_POCKETID_REDIRECTURL=" + TinyAuthPocketIDCallbackURL(domain),
		"TINYAUTH_OAUTH_PROVIDERS_POCKETID_SCOPES=openid email profile groups",
		"TINYAUTH_OAUTH_PROVIDERS_POCKETID_NAME=Pocket ID",
		"TINYAUTH_OAUTH_PROVIDERS_POCKETID_INSECURE=true",
		"TINYAUTH_OAUTH_AUTOREDIRECT=pocketid",
		"TINYAUTH_OAUTH_WHITELIST=" + email,
	}, "\n") + "\n")
}

func validTinyAuthPocketIDEnvironment(raw []byte, domain, email, clientID string) bool {
	prefix := renderTinyAuthPocketIDEnvironment(domain, email, clientID, "")
	secretLine := []byte("TINYAUTH_OAUTH_PROVIDERS_POCKETID_CLIENTSECRET=")
	lines := bytes.Split(raw, []byte("\n"))
	if len(lines) != 12 {
		return false
	}
	if !bytes.HasPrefix(lines[1], secretLine) || len(bytes.TrimPrefix(lines[1], secretLine)) < 32 {
		return false
	}
	lines[1] = secretLine
	return bytes.Equal(bytes.Join(lines, []byte("\n")), prefix)
}

func tinyAuthPocketIDSigningBytes(record TinyAuthPocketIDBinding) ([]byte, error) {
	record.Signature = ""
	raw, err := json.Marshal(record)
	if err != nil {
		return nil, err
	}
	return domainDigest(tinyAuthPocketIDBindingDomain, raw), nil
}
