package appsetup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const GuacamoleOwnerLoginAction = "guacamole-owner-login"

// Guacamole 1.6.0's default XML provider returns dataSource, not provider.
// The temporary token is revoked before an observation is admitted. Errors
// never retain response bodies, passwords, tokens or credential-bearing URLs.
func verifyGuacamoleOwnerLogin(ctx context.Context, session *http.Client, base *url.URL, password []byte) (result CustodiedOwnerLoginResult, resultErr error) {
	endpoint := strings.TrimRight(base.String(), "/") + "/api/tokens"
	form := url.Values{"username": {"owner"}, "password": {string(password)}}
	body := []byte(form.Encode())
	defer clear(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return result, errors.New("cannot construct Guacamole owner login")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := session.Do(req)
	if err != nil {
		return result, errors.New("Guacamole owner authentication request failed")
	}
	defer resp.Body.Close()
	// Register revocation as soon as a complete authToken field is read,
	// before requiring the rest of the object, trailing EOF or size boundary.
	// A later read failure must not turn an issued session into leaked custody.
	revokeToken := func(token string) {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		revoke, err := http.NewRequestWithContext(cleanup, http.MethodDelete, endpoint+"/"+url.PathEscape(token), nil)
		if err != nil {
			result = CustodiedOwnerLoginResult{}
			resultErr = errors.New("Guacamole temporary owner session could not be revoked")
			return
		}
		revoked, err := session.Do(revoke)
		if err != nil {
			result = CustodiedOwnerLoginResult{}
			resultErr = errors.New("Guacamole temporary owner session could not be revoked")
			return
		}
		defer revoked.Body.Close()
		// DELETE destroys the session. A missing session is already revoked.
		if revoked.StatusCode != http.StatusNoContent && revoked.StatusCode != http.StatusNotFound {
			result = CustodiedOwnerLoginResult{}
			resultErr = errors.New("Guacamole temporary owner session revocation was refused")
		}
	}
	var observed struct{ Token, Username, DataSource string }
	limited := &io.LimitedReader{R: resp.Body, N: (1 << 20) + 1}
	decoder := json.NewDecoder(limited)
	opening, decodeErr := decoder.Token()
	validObject := opening == json.Delim('{')
	seen := map[string]bool{}
	duplicate := false
	for decodeErr == nil && validObject && decoder.More() {
		var field any
		field, decodeErr = decoder.Token()
		if decodeErr != nil {
			break
		}
		name, ok := field.(string)
		if !ok {
			decodeErr = errors.New("invalid field")
			break
		}
		duplicate = duplicate || seen[name]
		seen[name] = true
		switch name {
		case "authToken":
			var token string
			decodeErr = decoder.Decode(&token)
			if decodeErr == nil && token != "" {
				defer revokeToken(token)
			}
			observed.Token = token
		case "username":
			decodeErr = decoder.Decode(&observed.Username)
		case "dataSource":
			decodeErr = decoder.Decode(&observed.DataSource)
		default:
			var ignored json.RawMessage
			decodeErr = decoder.Decode(&ignored)
			clear(ignored)
		}
	}
	if decodeErr == nil && validObject {
		var closing any
		closing, decodeErr = decoder.Token()
		validObject = closing == json.Delim('}')
	}
	if decodeErr == nil && validObject {
		var trailing json.RawMessage
		decodeErr = decoder.Decode(&trailing)
		clear(trailing)
		if decodeErr == io.EOF {
			decodeErr = nil
		} else if decodeErr == nil {
			decodeErr = errors.New("unexpected trailing response")
		}
	}
	if limited.N == 0 || decodeErr != nil || !validObject || duplicate {
		return result, errors.New("Guacamole owner response exceeds its boundary or is invalid")
	}
	if resp.StatusCode != http.StatusOK || observed.Token == "" || observed.Username != "owner" || observed.DataSource != "default" {
		return result, errors.New("Guacamole did not authenticate the exact existing owner in its default provider")
	}
	return CustodiedOwnerLoginResult{AccountRef: "owner"}, nil
}
