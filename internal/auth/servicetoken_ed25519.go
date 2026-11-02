package auth

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
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

// SignServiceTokenEd25519 mints an EdDSA service token with the caller's own
// seed. kid and the claim shape match kombify-go-common/servicecall. The
// seed is 32 bytes; the header carries kid. HS256 stays on SignServiceToken
// for callers that do not have a key yet.
func SignServiceTokenEd25519(svc, target, kid string, seed []byte, ttl time.Duration) (string, error) {
	if svc == "" || target == "" {
		return "", fmt.Errorf("auth: svc and target are required")
	}
	if !validKeyID(kid) || len(seed) != ed25519.SeedSize {
		return "", ErrInvalidKeyMaterial
	}
	if ttl <= 0 {
		ttl = DefaultTokenTTL
	}
	if ttl > 5*time.Minute {
		ttl = 5 * time.Minute
	}
	now := time.Now()
	payload, err := json.Marshal(Claims{
		Iss: "kombify-" + svc,
		Aud: "kombify-" + target,
		Iat: now.Unix(),
		Exp: now.Add(ttl).Unix(),
		Svc: svc,
	})
	if err != nil {
		return "", fmt.Errorf("auth: marshal claims: %w", err)
	}
	header, err := json.Marshal(map[string]string{"alg": "EdDSA", "kid": kid, "typ": "JWT"})
	if err != nil {
		return "", fmt.Errorf("auth: marshal header: %w", err)
	}
	enc := base64.RawURLEncoding
	signingInput := enc.EncodeToString(header) + "." + enc.EncodeToString(payload)
	signature := ed25519.Sign(ed25519.NewKeyFromSeed(seed), []byte(signingInput))
	return signingInput + "." + enc.EncodeToString(signature), nil
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
