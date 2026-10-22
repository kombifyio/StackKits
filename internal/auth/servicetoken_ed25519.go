package auth

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
)

// Per-caller Ed25519 service identity, wire-compatible with
// kombify-go-common/servicecall (kept inline for the same reason as
// SignServiceToken). A receiver holds only public keys, each bound to one
// caller service, so it can neither mint tokens nor let one caller speak for
// another. HS256 stays accepted until SERVICE_AUTH_REQUIRE_ED25519=true.
const (
	EnvEd25519PublicKeys = "SERVICE_AUTH_ED25519_PUBLIC_KEYS"
	EnvRequireEd25519    = "SERVICE_AUTH_REQUIRE_ED25519"
)

// ErrInvalidKeyMaterial reports a malformed public key set.
var ErrInvalidKeyMaterial = errors.New("auth: invalid Ed25519 key material")

// CallerPublicKey is a verification key bound to one caller service id.
type CallerPublicKey struct {
	Service string
	Key     ed25519.PublicKey
}

// ParseCallerPublicKeys parses "<svc>:<kid>=<base64url key>,...". A malformed
// entry or a duplicate kid fails the whole set, so a typo never silently
// disables verification.
func ParseCallerPublicKeys(raw string) (map[string]CallerPublicKey, error) {
	keys := map[string]CallerPublicKey{}
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		binding, encoded, ok := strings.Cut(entry, "=")
		service, kid, bound := strings.Cut(binding, ":")
		service, kid = strings.ToLower(strings.TrimSpace(service)), strings.TrimSpace(kid)
		key, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(strings.TrimSpace(encoded), "="))
		if !ok || !bound || service == "" || !validKeyID(kid) || err != nil || len(key) != ed25519.PublicKeySize {
			return nil, ErrInvalidKeyMaterial
		}
		if _, dup := keys[kid]; dup {
			return nil, fmt.Errorf("%w: duplicate kid %q", ErrInvalidKeyMaterial, kid)
		}
		keys[kid] = CallerPublicKey{Service: service, Key: ed25519.PublicKey(key)}
	}
	return keys, nil
}

// LoadEd25519FromEnv reads SERVICE_AUTH_ED25519_PUBLIC_KEYS and
// SERVICE_AUTH_REQUIRE_ED25519. Absent variables yield no keys and false.
func LoadEd25519FromEnv() (map[string]CallerPublicKey, bool, error) {
	var keys map[string]CallerPublicKey
	if raw := strings.TrimSpace(os.Getenv(EnvEd25519PublicKeys)); raw != "" {
		parsed, err := ParseCallerPublicKeys(raw)
		if err != nil {
			return nil, false, fmt.Errorf("%s: %w", EnvEd25519PublicKeys, err)
		}
		keys = parsed
	}
	return keys, strings.EqualFold(strings.TrimSpace(os.Getenv(EnvRequireEd25519)), "true"), nil
}

func validKeyID(kid string) bool {
	if kid == "" || len(kid) > 64 {
		return false
	}
	for i := 0; i < len(kid); i++ {
		c := kid[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_', c == '.':
		default:
			return false
		}
	}
	return true
}
