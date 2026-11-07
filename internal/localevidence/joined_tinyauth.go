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
	"regexp"
	"strings"
	"time"
)

// A joined server's TinyAuth signs in against the Authority's Pocket ID
// (homelab identity target §4). The Authority registers the OIDC client under
// an Owner step-up; its secret reaches this server only as a Wallet item by
// host delivery (stackkit wallet-item import). Binding reads the delivered
// item from encrypted Wallet item custody and writes TinyAuth's private
// environment; the Owner-signed record holds the client, the issuer and the
// Wallet item reference, never the secret.
const joinedTinyAuthBindingKind = "JoinedTinyAuthPocketIDBinding"

var joinedTinyAuthClientID = regexp.MustCompile(`^stackkit-tinyauth-[a-z0-9][a-z0-9-]{0,62}$`)

// JoinedTinyAuthRequest names the Authority's client for this server's
// TinyAuth and the Wallet item holding its secret.
type JoinedTinyAuthRequest struct {
	ClientID  string
	Issuer    string
	TenantRef string
	ItemRef   string
}

// BindJoinedTinyAuthPocketID binds this joined server's TinyAuth to the
// Authority's Pocket ID (host only). The issuer must be the Authority's Pocket
// ID origin the joined-core anchor names, and the Wallet item must already be
// in this server's custody.
func BindJoinedTinyAuthPocketID(workspaceRoot string, request JoinedTinyAuthRequest) (TinyAuthPocketIDBinding, error) {
	joined, err := LoadJoinedCore(workspaceRoot)
	if err != nil {
		return TinyAuthPocketIDBinding{}, err
	}
	address, err := LocalIdentityRuntimeAddress(workspaceRoot)
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
	if !joinedTinyAuthClientID.MatchString(request.ClientID) || request.Issuer != "https://"+joined.AuthorityIdentityHost ||
		request.TenantRef == "" || request.ItemRef == "" {
		return TinyAuthPocketIDBinding{}, errors.New("localevidence: a joined TinyAuth binding needs the Authority's client, its Pocket ID issuer and the delivered Wallet item")
	}
	binding, err := json.Marshal(walletSecretBinding{TenantRef: request.TenantRef, ItemRef: request.ItemRef})
	if err != nil {
		return TinyAuthPocketIDBinding{}, err
	}
	secret, err := resolveWalletBinding(workspaceRoot, binding)
	if err != nil {
		return TinyAuthPocketIDBinding{}, err
	}
	defer clear(secret)
	if len(secret) < 32 {
		return TinyAuthPocketIDBinding{}, errors.New("localevidence: the delivered client secret is too short")
	}
	env := renderJoinedTinyAuthEnvironment(address, request.Issuer, request.ClientID, string(secret))
	defer clear(env)
	record := TinyAuthPocketIDBinding{
		APIVersion: TinyAuthPocketIDBindingAPIVersion, Kind: joinedTinyAuthBindingKind,
		OwnerRef: owner.OwnerRef, KeyID: owner.KeyID, ClientID: request.ClientID,
		CallbackURL: TinyAuthPocketIDCallbackURL(address), GroupIDs: []string{},
		Issuer: request.Issuer, TenantRef: request.TenantRef, ItemRef: request.ItemRef,
		EnvMAC:  basementRuntimeFileMAC(key, tinyAuthPocketIDEnvRelPath, env),
		BoundAt: time.Now().UTC().Truncate(time.Second),
	}
	signingBytes, err := tinyAuthPocketIDSigningBytes(record)
	if err != nil {
		return TinyAuthPocketIDBinding{}, err
	}
	record.Signature = base64.RawStdEncoding.EncodeToString(ed25519.Sign(key.private, signingBytes))
	raw, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return TinyAuthPocketIDBinding{}, err
	}
	directory, err := confinedCustodyPath(workspaceRoot, tinyAuthPocketIDBindingRelDir)
	if err != nil {
		return TinyAuthPocketIDBinding{}, err
	}
	// A server that ran its own Pocket ID before it joined loses that binding:
	// its own Pocket ID no longer runs once the core is joined.
	parent := filepath.Dir(directory)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return TinyAuthPocketIDBinding{}, err
	}
	stage, err := os.MkdirTemp(parent, ".tinyauth-joined-*")
	if err != nil {
		return TinyAuthPocketIDBinding{}, err
	}
	defer func() { _ = os.RemoveAll(stage) }()
	if err := os.Chmod(stage, 0o700); err != nil {
		return TinyAuthPocketIDBinding{}, err
	}
	if err := writePrivateRuntimeFile(filepath.Join(stage, tinyAuthPocketIDEnvRelPath), env); err != nil {
		return TinyAuthPocketIDBinding{}, err
	}
	if err := writePrivateRuntimeFile(filepath.Join(stage, tinyAuthPocketIDRecordRelPath), raw); err != nil {
		return TinyAuthPocketIDBinding{}, err
	}
	// A rotation replaces the earlier joined binding as one directory.
	retired := directory + ".retired"
	_ = os.RemoveAll(retired)
	if err := os.Rename(directory, retired); err != nil && !errors.Is(err, os.ErrNotExist) {
		return TinyAuthPocketIDBinding{}, err
	}
	if err := os.Rename(stage, directory); err != nil {
		_ = os.Rename(retired, directory)
		return TinyAuthPocketIDBinding{}, fmt.Errorf("localevidence: install the joined TinyAuth binding: %w", err)
	}
	_ = os.RemoveAll(retired)
	return LoadJoinedTinyAuthPocketIDBinding(workspaceRoot)
}

// LoadJoinedTinyAuthPocketIDBinding verifies a joined server's TinyAuth
// binding: the Owner signature, the issuer the joined-core anchor names and
// the private environment's MAC.
func LoadJoinedTinyAuthPocketIDBinding(workspaceRoot string) (TinyAuthPocketIDBinding, error) {
	joined, err := LoadJoinedCore(workspaceRoot)
	if err != nil {
		return TinyAuthPocketIDBinding{}, err
	}
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
		return TinyAuthPocketIDBinding{}, err
	}
	var record TinyAuthPocketIDBinding
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if requirePrivateRuntimeFile(recordPath) != nil || decoder.Decode(&record) != nil || record.Kind != joinedTinyAuthBindingKind {
		return TinyAuthPocketIDBinding{}, errors.New("localevidence: the joined TinyAuth binding is malformed")
	}
	address, err := LocalIdentityRuntimeAddress(workspaceRoot)
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
	signature, err := base64.RawStdEncoding.Strict().DecodeString(record.Signature)
	signingBytes, signingErr := tinyAuthPocketIDSigningBytes(record)
	if err != nil || signingErr != nil || len(signature) != ed25519.SignatureSize || !ed25519.Verify(key.Public(), signingBytes, signature) ||
		record.APIVersion != TinyAuthPocketIDBindingAPIVersion || record.OwnerRef != owner.OwnerRef || record.KeyID != owner.KeyID ||
		!joinedTinyAuthClientID.MatchString(record.ClientID) || record.Issuer != "https://"+joined.AuthorityIdentityHost ||
		record.CallbackURL != TinyAuthPocketIDCallbackURL(address) {
		return TinyAuthPocketIDBinding{}, errors.New("localevidence: the joined TinyAuth binding does not verify")
	}
	envPath := filepath.Join(directory, tinyAuthPocketIDEnvRelPath)
	env, err := os.ReadFile(envPath) //nolint:gosec // fixed custody path
	defer clear(env)
	if err != nil || requirePrivateRuntimeFile(envPath) != nil || record.EnvMAC != basementRuntimeFileMAC(key, tinyAuthPocketIDEnvRelPath, env) {
		return TinyAuthPocketIDBinding{}, errors.New("localevidence: the joined TinyAuth environment does not verify")
	}
	return record, nil
}

// renderJoinedTinyAuthEnvironment points every Pocket ID endpoint at the
// Authority's public issuer: the browser follows the authorize URL, and
// TinyAuth's token and userinfo calls reach the Authority's LAN address
// (Compose extra_hosts from the joined-core anchor) with TLS verified against
// the Authority root.
func renderJoinedTinyAuthEnvironment(address IdentityRuntimeAddress, issuer, clientID, clientSecret string) []byte {
	issuer = strings.TrimSuffix(issuer, "/")
	return []byte(strings.Join([]string{
		"TINYAUTH_OAUTH_PROVIDERS_POCKETID_CLIENTID=" + clientID,
		"TINYAUTH_OAUTH_PROVIDERS_POCKETID_CLIENTSECRET=" + clientSecret,
		"TINYAUTH_OAUTH_PROVIDERS_POCKETID_AUTHURL=" + issuer + "/authorize",
		"TINYAUTH_OAUTH_PROVIDERS_POCKETID_TOKENURL=" + issuer + "/api/oidc/token",
		"TINYAUTH_OAUTH_PROVIDERS_POCKETID_USERINFOURL=" + issuer + "/api/oidc/userinfo",
		"TINYAUTH_OAUTH_PROVIDERS_POCKETID_REDIRECTURL=" + TinyAuthPocketIDCallbackURL(address),
		"TINYAUTH_OAUTH_PROVIDERS_POCKETID_SCOPES=openid email profile groups",
		"TINYAUTH_OAUTH_PROVIDERS_POCKETID_NAME=Pocket ID",
		"TINYAUTH_OAUTH_PROVIDERS_POCKETID_INSECURE=false",
		"TINYAUTH_OAUTH_AUTOREDIRECT=pocketid",
	}, "\n") + "\n")
}

// DiscardJoinedTinyAuthPocketIDBinding removes a joined binding (leave).
// It never touches a binding of this server's own Pocket ID.
func DiscardJoinedTinyAuthPocketIDBinding(workspaceRoot string) error {
	directory, err := confinedCustodyPath(workspaceRoot, tinyAuthPocketIDBindingRelDir)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(filepath.Join(directory, tinyAuthPocketIDRecordRelPath)) //nolint:gosec // fixed custody path
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var record struct {
		Kind string `json:"kind"`
	}
	if json.Unmarshal(raw, &record) != nil || record.Kind != joinedTinyAuthBindingKind {
		return nil
	}
	return DiscardBasementTinyAuthPocketIDBinding(workspaceRoot)
}
