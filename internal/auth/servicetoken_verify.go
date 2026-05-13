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

// VerifyOptions configures service-auth JWT verification for inbound calls.
type VerifyOptions struct {
	Target         string
	Secrets        []string
	AllowedCallers []string
	Now            func() time.Time
	Leeway         time.Duration
}

// VerifyServiceToken verifies an HS256 service-auth JWT minted by
// SignServiceToken or kombify-go-common/servicecall.IssueToken.
func VerifyServiceToken(token string, opts VerifyOptions) (*Claims, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, fmt.Errorf("auth: service-auth token is empty")
	}
	target := strings.TrimSpace(opts.Target)
	if target == "" {
		return nil, fmt.Errorf("auth: target audience is empty")
	}

	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("auth: malformed service-auth token")
	}

	if err := verifyJWTHeader(parts[0]); err != nil {
		return nil, err
	}
	if err := verifyJWTSignature(parts[0]+"."+parts[1], parts[2], opts.Secrets); err != nil {
		return nil, err
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("auth: decode claims: %w", err)
	}
	var claims Claims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, fmt.Errorf("auth: decode claims: %w", err)
	}

	expectedAud := "kombify-" + target
	if claims.Aud != expectedAud {
		return nil, fmt.Errorf("auth: invalid audience %q, want %q", claims.Aud, expectedAud)
	}
	if claims.Svc == "" {
		return nil, fmt.Errorf("auth: caller service claim is empty")
	}
	if claims.Iss != "kombify-"+claims.Svc {
		return nil, fmt.Errorf("auth: issuer %q does not match caller %q", claims.Iss, claims.Svc)
	}
	if !callerAllowed(claims.Svc, opts.AllowedCallers) {
		return nil, fmt.Errorf("auth: caller %q is not allowed", claims.Svc)
	}

	nowFn := opts.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	leeway := opts.Leeway
	if leeway <= 0 {
		leeway = 30 * time.Second
	}
	now := nowFn()
	if claims.Exp <= now.Add(-leeway).Unix() {
		return nil, fmt.Errorf("auth: service-auth token expired")
	}
	if claims.Iat > now.Add(leeway).Unix() {
		return nil, fmt.Errorf("auth: service-auth token issued in the future")
	}

	return &claims, nil
}

func verifyJWTHeader(encoded string) error {
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return fmt.Errorf("auth: decode header: %w", err)
	}
	var header struct {
		Alg string `json:"alg"`
		Typ string `json:"typ"`
	}
	if err := json.Unmarshal(raw, &header); err != nil {
		return fmt.Errorf("auth: decode header: %w", err)
	}
	if header.Alg != "HS256" || header.Typ != "JWT" {
		return fmt.Errorf("auth: unsupported JWT header")
	}
	return nil
}

func verifyJWTSignature(signingInput, encodedSig string, secrets []string) error {
	got, err := base64.RawURLEncoding.DecodeString(encodedSig)
	if err != nil {
		return fmt.Errorf("auth: decode signature: %w", err)
	}
	for _, secret := range secrets {
		secret = strings.TrimSpace(secret)
		if secret == "" {
			continue
		}
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(signingInput))
		if hmac.Equal(got, mac.Sum(nil)) {
			return nil
		}
	}
	return fmt.Errorf("auth: invalid service-auth signature")
}

func callerAllowed(caller string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, candidate := range allowed {
		if strings.TrimSpace(candidate) == caller {
			return true
		}
	}
	return false
}
