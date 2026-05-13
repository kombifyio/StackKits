// Package auth provides shared authentication primitives for the stackkit
// CLI and supporting libraries. The single primitive today is the HS256
// service-auth JWT signer used for service-to-service calls into
// kombify-Administration.
//
// Wire-format mirrors kombify-go-common/servicecall.IssueToken byte-for-byte
// so admin's tryServiceAuth verifier accepts our tokens. Kept inline (not
// pulled from go-common) because servicekit cannot import go-common as a
// module without changing the repo's external-dependency posture.
package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// HeaderServiceAuth is the HTTP header that carries the signed token.
// Must equal kombify-Administration's HEADER_SERVICE_AUTH constant.
const HeaderServiceAuth = "X-Kombify-Service-Auth"

// DefaultTokenTTL is the default expiry duration for a minted token.
// Short by design: replays are bounded to 5 minutes.
const DefaultTokenTTL = 5 * time.Minute

// jwtHeaderJSON is the fixed HS256 JWT header. Bytes must match the
// admin verifier exactly (see servicecall.jwtHeader).
var jwtHeaderJSON = []byte(`{"alg":"HS256","typ":"JWT"}`)

// Claims is the JSON shape inside the JWT payload. Kept minimal — admin
// only inspects iss/aud/iat/exp/svc.
type Claims struct {
	Iss       string `json:"iss"`
	Aud       string `json:"aud"`
	Iat       int64  `json:"iat"`
	Exp       int64  `json:"exp"`
	Svc       string `json:"svc"`
	RequestID string `json:"reqId,omitempty"`
}

// SignServiceToken mints an HS256 service-auth JWT.
//
//	svc      service slug (the caller; e.g. "stackkits")
//	target   service slug being called (e.g. "administration")
//	secret   shared signing secret (SERVICE_AUTH_SECRET in Doppler)
//	ttl      token lifetime; <=0 falls back to DefaultTokenTTL
//
// Returns "header.payload.signature" with each part base64url-encoded
// without padding. Admin's tryServiceAuth verifies signature + audience
// (kombify-<target>) + caller-allowlist (svc).
func SignServiceToken(svc, target, secret string, ttl time.Duration) (string, error) {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return "", fmt.Errorf("auth: service-auth secret is empty")
	}
	if svc == "" {
		return "", fmt.Errorf("auth: svc claim is empty")
	}
	if target == "" {
		return "", fmt.Errorf("auth: target audience is empty")
	}
	if ttl <= 0 {
		ttl = DefaultTokenTTL
	}
	now := time.Now()
	claims := Claims{
		Iss: "kombify-" + svc,
		Aud: "kombify-" + target,
		Iat: now.Unix(),
		Exp: now.Add(ttl).Unix(),
		Svc: svc,
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("auth: marshal claims: %w", err)
	}
	signingInput := base64.RawURLEncoding.EncodeToString(jwtHeaderJSON) + "." +
		base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signingInput))
	sig := mac.Sum(nil)
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}
