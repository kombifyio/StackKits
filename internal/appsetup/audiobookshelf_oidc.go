package appsetup

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"time"
)

// AudiobookshelfOIDCRequest contains only verified home identity custody.
// Pocket ID restricts this client to the application's household grants.
type AudiobookshelfOIDCRequest struct {
	Issuer, ClientID, ClientSecret string
}

type audiobookshelfSetupCall func(context.Context, string, string, string, string, any, any) error

func configureAudiobookshelfOIDC(ctx context.Context, call audiobookshelfSetupCall, token string, request AudiobookshelfOIDCRequest) (returnErr error) {
	issuer, err := url.Parse(request.Issuer)
	if err != nil || issuer.Scheme != "https" || issuer.Host == "" || issuer.User != nil || issuer.Path != "" || issuer.RawQuery != "" || issuer.Fragment != "" ||
		request.ClientID != "stackkit-media-audiobooks" || strings.TrimSpace(request.ClientSecret) == "" || strings.ContainsAny(request.ClientSecret, "\r\n\x00") {
		return errors.New("Audiobookshelf OIDC requires the exact home issuer and custodied application client")
	}
	read := func() (map[string]json.RawMessage, error) {
		var settings map[string]json.RawMessage
		if err := call(ctx, http.MethodGet, "/api/auth-settings", token, "", nil, &settings); err != nil {
			return nil, err
		}
		if settings == nil {
			return nil, errors.New("Audiobookshelf authentication settings are absent")
		}
		return settings, nil
	}
	current, err := read()
	if err != nil {
		return err
	}
	// Keep existing exact native-client callbacks, including owner-approved
	// third-party readers. A wildcard must be resolved before enabling SSO.
	var mobileURIs []string
	if raw := current["authOpenIDMobileRedirectURIs"]; len(raw) != 0 {
		if json.Unmarshal(raw, &mobileURIs) != nil {
			return errors.New("Audiobookshelf mobile callback policy is invalid")
		}
		for _, uri := range mobileURIs {
			if uri == "*" {
				return errors.New("Audiobookshelf has an unrestricted mobile callback; select exact native-client callbacks before enabling OIDC")
			}
		}
	}
	for name, expected := range map[string]string{"authOpenIDIssuerURL": request.Issuer, "authOpenIDClientID": request.ClientID} {
		var value string
		if raw := current[name]; len(raw) != 0 && string(raw) != "null" && json.Unmarshal(raw, &value) != nil {
			return errors.New("Audiobookshelf authentication settings are invalid")
		}
		if value != "" && value != expected {
			return errors.New("Audiobookshelf already belongs to a different OIDC provider or client; preserve its account links")
		}
	}
	// Ask the application to discover the home issuer using its governed CA
	// mount. This also verifies server-side reach before any setting changes.
	var discovery struct {
		Issuer        string   `json:"issuer"`
		Authorization string   `json:"authorization_endpoint"`
		Token         string   `json:"token_endpoint"`
		UserInfo      string   `json:"userinfo_endpoint"`
		JWKS          string   `json:"jwks_uri"`
		Logout        string   `json:"end_session_endpoint"`
		Algorithms    []string `json:"id_token_signing_alg_values_supported"`
	}
	if err := call(ctx, http.MethodGet, "/auth/openid/config?issuer="+url.QueryEscape(request.Issuer), token, "", nil, &discovery); err != nil {
		return err
	}
	if discovery.Issuer != request.Issuer {
		return errors.New("Audiobookshelf discovered a different identity issuer")
	}
	for _, endpoint := range []string{discovery.Authorization, discovery.Token, discovery.UserInfo, discovery.JWKS, discovery.Logout} {
		u, err := url.Parse(endpoint)
		if err != nil || u.Scheme != issuer.Scheme || u.Host != issuer.Host || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path == "" {
			return errors.New("Audiobookshelf discovery contains an endpoint outside the home issuer")
		}
	}
	algorithm := ""
	for _, candidate := range discovery.Algorithms {
		if candidate == "RS256" || candidate == "ES256" || candidate == "EdDSA" {
			algorithm = candidate
			break
		}
	}
	if algorithm == "" {
		return errors.New("Audiobookshelf discovery has no supported asymmetric signing algorithm")
	}
	// Never match by mutable email or username: a new subject must not acquire
	// an existing account's library state or root/admin permissions. Upstream
	// auto-registration creates ordinary users; local recovery stays enabled.
	desired := map[string]any{
		"authActiveAuthMethods": []string{"local", "openid"},
		"authOpenIDIssuerURL":   request.Issuer, "authOpenIDClientID": request.ClientID, "authOpenIDClientSecret": request.ClientSecret,
		"authOpenIDAuthorizationURL": discovery.Authorization, "authOpenIDTokenURL": discovery.Token,
		"authOpenIDUserInfoURL": discovery.UserInfo, "authOpenIDJwksURL": discovery.JWKS, "authOpenIDLogoutURL": discovery.Logout,
		"authOpenIDTokenSigningAlgorithm": algorithm, "authOpenIDButtonText": "Pocket ID",
		"authOpenIDAutoLaunch": false, "authOpenIDAutoRegister": true, "authOpenIDMatchExistingBy": nil,
		"authOpenIDGroupClaim": nil, "authOpenIDAdvancedPermsClaim": nil, "authOpenIDSubfolderForRedirectURLs": "",
	}
	matches := func(settings map[string]json.RawMessage) bool {
		for name, value := range desired {
			raw, _ := json.Marshal(value)
			var expected any
			_ = json.Unmarshal(raw, &expected)
			var observed any
			if json.Unmarshal(settings[name], &observed) != nil {
				return false
			}
			// The upstream controller treats empty optional claims as null
			// without rewriting them; construction also restores empty claims.
			if text, ok := observed.(string); value == nil && ok && text == "" &&
				(name == "authOpenIDGroupClaim" || name == "authOpenIDAdvancedPermsClaim" || name == "authOpenIDMatchExistingBy") {
				continue
			}
			if !reflect.DeepEqual(observed, expected) {
				return false
			}
		}
		return true
	}
	if !matches(current) {
		// The pinned controller logs previous/new secret values at DEBUG.
		// Change only its supported log-level setting and restore it even if
		// the settings write fails. Other auth/log edits must remain quiesced.
		restoreLogging, err := quietAudiobookshelfSecretLogging(ctx, call, token)
		if restoreLogging != nil {
			defer func() { returnErr = errors.Join(returnErr, restoreLogging()) }()
		}
		if err != nil {
			return err
		}
		var methods []string
		if json.Unmarshal(current["authActiveAuthMethods"], &methods) != nil {
			return errors.New("Audiobookshelf active authentication methods are invalid")
		}
		if slices.Contains(methods, "openid") {
			// Updating settings alone leaves the upstream cached OIDC client
			// alive. Removing the strategy clears both client and strategy;
			// re-enabling after the write uses the current secret/endpoints.
			if err := call(ctx, http.MethodPatch, "/api/auth-settings", token, "", map[string]any{"authActiveAuthMethods": []string{"local"}}, nil); err != nil {
				return err
			}
			disabled, err := read()
			if err != nil {
				return err
			}
			if json.Unmarshal(disabled["authActiveAuthMethods"], &methods) != nil || !slices.Equal(methods, []string{"local"}) {
				return errors.New("Audiobookshelf did not disable its cached OIDC strategy while retaining local recovery")
			}
		}
		if err := call(ctx, http.MethodPatch, "/api/auth-settings", token, "", desired, nil); err != nil {
			return err
		}
	}
	observed, err := read()
	if err != nil {
		return err
	}
	if !matches(observed) {
		return errors.New("Audiobookshelf did not confirm its subject-bound OIDC policy")
	}
	return nil
}

func quietAudiobookshelfSecretLogging(ctx context.Context, call audiobookshelfSetupCall, token string) (restore func() error, returnErr error) {
	type settingsResponse struct {
		ServerSettings struct {
			LogLevel *int `json:"logLevel"`
		} `json:"serverSettings"`
	}
	var current settingsResponse
	if err := call(ctx, http.MethodPost, "/api/authorize", token, "", nil, &current); err != nil {
		return nil, err
	}
	if current.ServerSettings.LogLevel == nil || *current.ServerSettings.LogLevel < 0 || *current.ServerSettings.LogLevel > 6 {
		return nil, errors.New("Audiobookshelf did not disclose a valid logging level before the secret write")
	}
	original := *current.ServerSettings.LogLevel
	if original >= 2 {
		return nil, nil
	} // INFO or quieter suppresses secret-bearing DEBUG messages.
	set := func(ctx context.Context, level int) error {
		var observed settingsResponse
		if err := call(ctx, http.MethodPatch, "/api/settings", token, "", map[string]int{"logLevel": level}, &observed); err != nil {
			return err
		}
		if observed.ServerSettings.LogLevel == nil || *observed.ServerSettings.LogLevel != level {
			return errors.New("Audiobookshelf did not confirm the requested safe logging level")
		}
		return nil
	}
	// Return restoration even on an uncertain PATCH result: it may have committed.
	restore = func() error {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		return set(cleanup, original)
	}
	return restore, set(ctx, 2)
}
