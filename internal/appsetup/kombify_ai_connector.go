package appsetup

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"regexp"
)

// maxKombifyAIConnectorTokenBytes bounds the connector token; Cloudflare
// tunnel tokens are a few hundred bytes.
const maxKombifyAIConnectorTokenBytes = 4096

var (
	cloudflareAccountTagPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)
	cloudflareTunnelIDPattern   = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
)

// ErrKombifyAIConnectorToken reports a value that is not a connector token.
// The message never contains the value or any part of it.
var ErrKombifyAIConnectorToken = errors.New("the value is not a kombify AI connector token; copy the connector token of the model endpoint from Companion Studio (Settings, AI access, Model endpoints)")

// NormalizeKombifyAIConnectorToken returns the connector token of a kombify AI
// model endpoint without surrounding whitespace, after checking its shape: a
// remotely managed Cloudflare Tunnel token is standard base64 of a JSON object
// with the account tag "a", the tunnel ID "t" and the tunnel secret "s". The
// shape check keeps a pasted wrong value out of custody; only the connector's
// sign-in at Cloudflare proves the token.
func NormalizeKombifyAIConnectorToken(raw []byte) ([]byte, error) {
	token := bytes.TrimSpace(raw)
	if len(token) == 0 || len(token) > maxKombifyAIConnectorTokenBytes {
		return nil, ErrKombifyAIConnectorToken
	}
	decoded := make([]byte, base64.StdEncoding.DecodedLen(len(token)))
	defer clear(decoded)
	n, err := base64.StdEncoding.Strict().Decode(decoded, token)
	if err != nil {
		return nil, ErrKombifyAIConnectorToken
	}
	var fields struct {
		AccountTag   string `json:"a"`
		TunnelID     string `json:"t"`
		TunnelSecret string `json:"s"`
	}
	if json.Unmarshal(decoded[:n], &fields) != nil {
		return nil, ErrKombifyAIConnectorToken
	}
	defer func() { fields.TunnelSecret = "" }()
	secret, err := base64.StdEncoding.DecodeString(fields.TunnelSecret)
	defer clear(secret)
	if err != nil || len(secret) < 32 ||
		!cloudflareAccountTagPattern.MatchString(fields.AccountTag) || !cloudflareTunnelIDPattern.MatchString(fields.TunnelID) {
		return nil, ErrKombifyAIConnectorToken
	}
	return append([]byte(nil), token...), nil
}
