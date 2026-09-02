package appsetup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

const (
	// HomeAssistantPinnedVersion is the Home Assistant release used by the
	// native Architecture v2 workload. Callers must bind setup to the version
	// admitted by their applied workload.
	HomeAssistantPinnedVersion = "2026.7.2"

	homeAssistantClientID         = "http://stackkit"
	homeAssistantRedirectURI      = "http://stackkit"
	homeAssistantBodyLimit        = 1 << 20
	homeAssistantWSLimit          = 64 << 10
	homeAssistantSetupTimeout     = 3 * time.Minute
	homeAssistantWebSocketTimeout = 20 * time.Second
	homeAssistantCleanupTimeout   = 5 * time.Second
)

// HomeAssistantOwnerRequest contains the credentials used for the local
// Home Assistant owner setup. Passwords and tokens are used only in memory.
type HomeAssistantOwnerRequest struct {
	Username        string
	Password        string
	DisplayName     string
	Language        string
	ExpectedVersion string
}

// HomeAssistantOwnerResult is the typed, post-login readback of the owner
// setup. A successful return proves the current authenticated user is both an
// owner and an administrator; onboarding completion is reported separately.
type HomeAssistantOwnerResult struct {
	UserID             string `json:"userId"`
	UserIsOwner        bool   `json:"userIsOwner"`
	UserIsAdmin        bool   `json:"userIsAdmin"`
	ServerInitialized  bool   `json:"serverInitialized"`
	OnboardingComplete bool   `json:"onboardingComplete"`
	Version            string `json:"version"`
}

// BootstrapHomeAssistantOwner creates the local owner only when the user
// onboarding step is still open, then always authenticates through the normal
// Home Assistant login flow and verifies the owner over WebSocket. The
// refresh token is revoked before returning, including on failure.
func BootstrapHomeAssistantOwner(
	ctx context.Context,
	client *http.Client,
	baseURL string,
	request HomeAssistantOwnerRequest,
) (result HomeAssistantOwnerResult, returnErr error) {
	username := strings.TrimSpace(request.Username)
	displayName := strings.TrimSpace(request.DisplayName)
	language := strings.TrimSpace(request.Language)
	expectedVersion := strings.TrimSpace(request.ExpectedVersion)
	password := request.Password
	if username == "" || strings.TrimSpace(password) == "" {
		return result, errors.New("Home Assistant owner setup requires a username and password")
	}
	if displayName == "" {
		return result, errors.New("Home Assistant owner setup requires a display name")
	}
	if language == "" {
		language = "en"
	}
	if expectedVersion == "" {
		return result, errors.New("Home Assistant owner setup requires an expected application version")
	}
	baseURL, err := normalizeHomeAssistantBaseURL(baseURL)
	if err != nil {
		return result, err
	}
	setupCtx, cancelSetup := context.WithTimeout(ctx, homeAssistantSetupTimeout)
	defer cancelSetup()
	ctx = setupCtx
	if client == nil {
		client = NewImmichHTTPClient()
		defer client.CloseIdleConnections()
	}
	client = cloneHomeAssistantHTTPClient(client)

	var accessToken string
	var onboardingAuthCode string
	var authCode string
	var refreshTokens []string
	var transientAccessTokens []string
	defer func() {
		if cleanupErr := revokeHomeAssistantRefreshTokens(client, baseURL, refreshTokens); cleanupErr != nil {
			cleanupFailure := fmt.Errorf("Home Assistant session cleanup failed: %w", cleanupErr)
			if returnErr == nil {
				returnErr = cleanupFailure
			} else {
				returnErr = errors.Join(returnErr, cleanupFailure)
			}
		}
		accessToken = ""
		onboardingAuthCode = ""
		authCode = ""
		for index := range refreshTokens {
			refreshTokens[index] = ""
		}
		for index := range transientAccessTokens {
			transientAccessTokens[index] = ""
		}
		password = ""
	}()

	// The unauthenticated WebSocket greeting is the version gate. It runs
	// before any onboarding or login mutation.
	if err := verifyHomeAssistantWebSocketVersion(ctx, client, baseURL, expectedVersion); err != nil {
		return result, err
	}

	onboarding, err := readHomeAssistantOnboarding(ctx, client, baseURL, "")
	if err != nil {
		return result, err
	}
	if onboarding.available && !onboarding.userDone {
		var created homeAssistantOnboardingCreate
		err := homeAssistantJSONRequest(ctx, client, baseURL, http.MethodPost, "/api/onboarding/users", map[string]string{
			"client_id": homeAssistantClientID,
			"name":      displayName,
			"username":  username,
			"password":  password,
			"language":  language,
		}, "", &created)
		if err != nil {
			// Another actor may have completed the one-shot user step between
			// the status read and this request. Continue with credential login;
			// a 403 alone never becomes setup evidence.
			var statusErr *homeAssistantHTTPError
			if !errors.As(err, &statusErr) || statusErr.status != http.StatusForbidden {
				return result, fmt.Errorf("Home Assistant owner onboarding failed: %w", err)
			}
		} else {
			onboardingAuthCode = strings.TrimSpace(created.AuthCode)
			created.AuthCode = ""
			if onboardingAuthCode == "" {
				return result, errors.New("Home Assistant owner onboarding returned no authorization code")
			}
			onboardingTokens, exchangeErr := exchangeHomeAssistantAuthCode(ctx, client, baseURL, onboardingAuthCode)
			onboardingAuthCode = ""
			onboardingAccessToken := strings.TrimSpace(onboardingTokens.AccessToken)
			onboardingRefreshToken := strings.TrimSpace(onboardingTokens.RefreshToken)
			onboardingTokenType := strings.TrimSpace(onboardingTokens.TokenType)
			refreshTokens = rememberHomeAssistantRefreshToken(refreshTokens, onboardingRefreshToken)
			if onboardingAccessToken != "" {
				transientAccessTokens = append(transientAccessTokens, onboardingAccessToken)
			}
			onboardingTokens.AccessToken = ""
			onboardingTokens.RefreshToken = ""
			onboardingTokens.TokenType = ""
			if exchangeErr != nil {
				return result, fmt.Errorf("Home Assistant onboarding authorization exchange failed: %w", exchangeErr)
			}
			if onboardingAccessToken == "" || onboardingRefreshToken == "" || !strings.EqualFold(onboardingTokenType, "bearer") {
				return result, errors.New("Home Assistant onboarding authorization exchange returned incomplete session credentials")
			}
		}
		created.AuthCode = ""
	}

	authCode, err = homeAssistantCredentialLogin(ctx, client, baseURL, username, password)
	if err != nil {
		return result, err
	}
	authCode = strings.TrimSpace(authCode)
	if authCode == "" {
		return result, errors.New("Home Assistant credential login returned no authorization code")
	}

	tokens, exchangeErr := exchangeHomeAssistantAuthCode(ctx, client, baseURL, authCode)
	authCode = ""
	accessToken = strings.TrimSpace(tokens.AccessToken)
	refreshToken := strings.TrimSpace(tokens.RefreshToken)
	tokenType := strings.TrimSpace(tokens.TokenType)
	refreshTokens = rememberHomeAssistantRefreshToken(refreshTokens, refreshToken)
	tokens.AccessToken = ""
	tokens.RefreshToken = ""
	tokens.TokenType = ""
	if exchangeErr != nil {
		return result, exchangeErr
	}
	if accessToken == "" || refreshToken == "" || !strings.EqualFold(tokenType, "bearer") {
		return result, errors.New("Home Assistant token exchange returned incomplete session credentials")
	}

	user, observedVersion, err := readHomeAssistantCurrentUser(ctx, client, baseURL, accessToken, expectedVersion)
	if err != nil {
		return result, err
	}
	if strings.TrimSpace(user.ID) == "" || user.IsOwner == nil || user.IsAdmin == nil || !*user.IsOwner || !*user.IsAdmin {
		return result, errors.New("Home Assistant login did not resolve to an owner administrator")
	}

	var config homeAssistantConfig
	if err := homeAssistantJSONRequest(ctx, client, baseURL, http.MethodGet, "/api/config", nil, accessToken, &config); err != nil {
		return result, fmt.Errorf("Home Assistant config readback failed: %w", err)
	}
	if strings.TrimSpace(config.Version) == "" || config.Version != expectedVersion || observedVersion != expectedVersion {
		return result, fmt.Errorf("Home Assistant version readback did not match expected version %q", expectedVersion)
	}
	if !strings.EqualFold(strings.TrimSpace(config.State), "RUNNING") {
		return result, fmt.Errorf("Home Assistant server is not running after owner login")
	}

	finalOnboarding, err := readHomeAssistantOnboarding(ctx, client, baseURL, accessToken)
	if err != nil {
		return result, err
	}
	if finalOnboarding.notFound && expectedVersion == HomeAssistantPinnedVersion && containsHomeAssistantComponent(config.Components, "onboarding") {
		// HA 2026.7.2 stops registering onboarding views after every step is
		// complete. An authenticated running config with the loaded component
		// is the required second signal for that 404 startup state.
		finalOnboarding.complete = true
	}

	return HomeAssistantOwnerResult{
		UserID:             strings.TrimSpace(user.ID),
		UserIsOwner:        *user.IsOwner,
		UserIsAdmin:        *user.IsAdmin,
		ServerInitialized:  true,
		OnboardingComplete: finalOnboarding.complete,
		Version:            expectedVersion,
	}, nil
}

func containsHomeAssistantComponent(components []string, wanted string) bool {
	for _, component := range components {
		if strings.TrimSpace(component) == wanted {
			return true
		}
	}
	return false
}

type homeAssistantHTTPError struct {
	method string
	path   string
	status int
}

func (e *homeAssistantHTTPError) Error() string {
	return fmt.Sprintf("Home Assistant %s %s returned HTTP %d", e.method, e.path, e.status)
}

type homeAssistantOnboarding struct {
	available bool
	userDone  bool
	complete  bool
	notFound  bool
}

type homeAssistantOnboardingStep struct {
	Step string `json:"step"`
	Done bool   `json:"done"`
}

type homeAssistantOnboardingCreate struct {
	AuthCode string `json:"auth_code"`
}

func readHomeAssistantOnboarding(ctx context.Context, client *http.Client, baseURL, token string) (homeAssistantOnboarding, error) {
	var steps []homeAssistantOnboardingStep
	err := homeAssistantJSONRequest(ctx, client, baseURL, http.MethodGet, "/api/onboarding", nil, token, &steps)
	if err != nil {
		var statusErr *homeAssistantHTTPError
		if errors.As(err, &statusErr) && statusErr.status == http.StatusNotFound {
			// A missing endpoint is unknown state. It never proves completion;
			// credential login and owner readback still have to succeed.
			return homeAssistantOnboarding{notFound: true}, nil
		}
		return homeAssistantOnboarding{}, fmt.Errorf("Home Assistant onboarding status readback failed: %w", err)
	}
	if len(steps) == 0 {
		return homeAssistantOnboarding{}, errors.New("Home Assistant onboarding status was empty")
	}
	status := homeAssistantOnboarding{available: true}
	sawUser := false
	status.complete = true
	for _, step := range steps {
		if step.Step == "user" {
			sawUser = true
			status.userDone = step.Done
		}
		status.complete = status.complete && step.Done
	}
	if !sawUser {
		return homeAssistantOnboarding{}, errors.New("Home Assistant onboarding status omitted the user step")
	}
	return status, nil
}

func homeAssistantCredentialLogin(ctx context.Context, client *http.Client, baseURL, username, password string) (string, error) {
	var flow homeAssistantLoginFlow
	if err := homeAssistantJSONRequest(ctx, client, baseURL, http.MethodPost, "/auth/login_flow", map[string]any{
		"client_id":    homeAssistantClientID,
		"handler":      []any{"homeassistant", nil},
		"redirect_uri": homeAssistantRedirectURI,
	}, "", &flow); err != nil {
		return "", fmt.Errorf("Home Assistant login flow could not be started: %w", err)
	}
	if err := validateHomeAssistantLoginForm(flow); err != nil {
		return "", err
	}

	var next homeAssistantLoginFlow
	if err := homeAssistantJSONRequest(ctx, client, baseURL, http.MethodPost, "/auth/login_flow/"+url.PathEscape(flow.FlowID), map[string]any{
		"client_id": homeAssistantClientID,
		"username":  username,
		"password":  password,
	}, "", &next); err != nil {
		return "", fmt.Errorf("Home Assistant credential login failed: %w", err)
	}
	if next.Type == "create_entry" {
		if strings.TrimSpace(next.Result) == "" {
			return "", errors.New("Home Assistant login completed without an authorization code")
		}
		return next.Result, nil
	}
	if next.Type == "form" {
		if strings.EqualFold(strings.TrimSpace(next.Errors["base"]), "invalid_auth") {
			return "", errors.New("Home Assistant rejected the supplied username or password; verify the credentials and retry")
		}
		return "", errors.New("Home Assistant login requires another form step; MFA or an additional challenge cannot be completed by native setup")
	}
	return "", errors.New("Home Assistant login returned an unsupported flow step")
}

type homeAssistantLoginFlow struct {
	Type       string                     `json:"type"`
	FlowID     string                     `json:"flow_id"`
	StepID     string                     `json:"step_id"`
	Result     string                     `json:"result"`
	Errors     map[string]string          `json:"errors"`
	DataSchema []homeAssistantSchemaField `json:"data_schema"`
}

type homeAssistantSchemaField struct {
	Name string `json:"name"`
}

func validateHomeAssistantLoginForm(flow homeAssistantLoginFlow) error {
	if flow.Type != "form" || strings.TrimSpace(flow.FlowID) == "" {
		return errors.New("Home Assistant login returned an unsupported initial flow step")
	}
	hasUsername := false
	hasPassword := false
	for _, field := range flow.DataSchema {
		switch field.Name {
		case "username":
			hasUsername = true
		case "password":
			hasPassword = true
		default:
			return errors.New("Home Assistant login requires an unsupported field; complete the extra challenge manually and retry")
		}
	}
	if !hasUsername || !hasPassword {
		return errors.New("Home Assistant login did not request the expected username and password fields")
	}
	return nil
}

type homeAssistantTokens struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
}

func exchangeHomeAssistantAuthCode(ctx context.Context, client *http.Client, baseURL, authCode string) (homeAssistantTokens, error) {
	values := url.Values{
		"client_id":  {homeAssistantClientID},
		"grant_type": {"authorization_code"},
		"code":       {authCode},
	}
	var tokens homeAssistantTokens
	if err := homeAssistantFormRequest(ctx, client, baseURL, "/auth/token", values, &tokens); err != nil {
		return tokens, fmt.Errorf("Home Assistant authorization code exchange failed: %w", err)
	}
	return tokens, nil
}

func revokeHomeAssistantRefreshToken(ctx context.Context, client *http.Client, baseURL, refreshToken string) error {
	values := url.Values{"token": {refreshToken}}
	return homeAssistantFormRequest(ctx, client, baseURL, "/auth/revoke", values, nil)
}

func rememberHomeAssistantRefreshToken(tokens []string, token string) []string {
	token = strings.TrimSpace(token)
	if token == "" {
		return tokens
	}
	for _, existing := range tokens {
		if existing == token {
			return tokens
		}
	}
	return append(tokens, token)
}

func revokeHomeAssistantRefreshTokens(client *http.Client, baseURL string, tokens []string) error {
	var cleanupErr error
	for _, token := range tokens {
		if token == "" {
			continue
		}
		cleanupCtx, cancel := context.WithTimeout(context.Background(), homeAssistantCleanupTimeout)
		err := revokeHomeAssistantRefreshToken(cleanupCtx, client, baseURL, token)
		cancel()
		if err != nil {
			cleanupErr = errors.Join(cleanupErr, err)
		}
	}
	return cleanupErr
}

type homeAssistantConfig struct {
	State      string   `json:"state"`
	Version    string   `json:"version"`
	Components []string `json:"components"`
}

type homeAssistantWSGreeting struct {
	Type      string `json:"type"`
	HAVersion string `json:"ha_version"`
}

func verifyHomeAssistantWebSocketVersion(ctx context.Context, client *http.Client, baseURL, expectedVersion string) error {
	wsURL, err := homeAssistantWebSocketURL(baseURL)
	if err != nil {
		return err
	}
	wsCtx, cancel := context.WithTimeout(ctx, homeAssistantWebSocketTimeout)
	defer cancel()
	conn, _, err := websocket.Dial(wsCtx, wsURL, &websocket.DialOptions{HTTPClient: client})
	if err != nil {
		return homeAssistantWebSocketError(wsCtx, "Home Assistant WebSocket version probe failed", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "version probe complete")
	conn.SetReadLimit(homeAssistantWSLimit)
	var greeting homeAssistantWSGreeting
	if err := wsjson.Read(wsCtx, conn, &greeting); err != nil {
		return homeAssistantWebSocketError(wsCtx, "Home Assistant WebSocket greeting failed", err)
	}
	if greeting.Type != "auth_required" {
		return errors.New("Home Assistant WebSocket returned an unsupported greeting")
	}
	if strings.TrimSpace(greeting.HAVersion) == "" || greeting.HAVersion != expectedVersion {
		return fmt.Errorf("Home Assistant WebSocket version did not match expected version %q", expectedVersion)
	}
	return nil
}

type homeAssistantCurrentUser struct {
	ID      string `json:"id"`
	IsOwner *bool  `json:"is_owner"`
	IsAdmin *bool  `json:"is_admin"`
}

type homeAssistantWSAuthResult struct {
	Type      string `json:"type"`
	HAVersion string `json:"ha_version"`
}

type homeAssistantWSResult struct {
	ID      int64           `json:"id"`
	Type    string          `json:"type"`
	Success bool            `json:"success"`
	Result  json.RawMessage `json:"result"`
}

func readHomeAssistantCurrentUser(ctx context.Context, client *http.Client, baseURL, accessToken, expectedVersion string) (homeAssistantCurrentUser, string, error) {
	wsURL, err := homeAssistantWebSocketURL(baseURL)
	if err != nil {
		return homeAssistantCurrentUser{}, "", err
	}
	wsCtx, cancel := context.WithTimeout(ctx, homeAssistantWebSocketTimeout)
	defer cancel()
	conn, _, err := websocket.Dial(wsCtx, wsURL, &websocket.DialOptions{HTTPClient: client})
	if err != nil {
		return homeAssistantCurrentUser{}, "", homeAssistantWebSocketError(wsCtx, "Home Assistant authenticated WebSocket connection failed", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "owner readback complete")
	conn.SetReadLimit(homeAssistantWSLimit)

	var greeting homeAssistantWSGreeting
	if err := wsjson.Read(wsCtx, conn, &greeting); err != nil {
		return homeAssistantCurrentUser{}, "", homeAssistantWebSocketError(wsCtx, "Home Assistant WebSocket greeting failed", err)
	}
	if greeting.Type != "auth_required" || greeting.HAVersion != expectedVersion {
		return homeAssistantCurrentUser{}, "", fmt.Errorf("Home Assistant WebSocket version changed before owner readback")
	}
	if err := wsjson.Write(wsCtx, conn, map[string]string{"type": "auth", "access_token": accessToken}); err != nil {
		return homeAssistantCurrentUser{}, "", homeAssistantWebSocketError(wsCtx, "Home Assistant WebSocket authentication write failed", err)
	}
	var authResult homeAssistantWSAuthResult
	if err := wsjson.Read(wsCtx, conn, &authResult); err != nil {
		return homeAssistantCurrentUser{}, "", homeAssistantWebSocketError(wsCtx, "Home Assistant WebSocket authentication readback failed", err)
	}
	if authResult.Type != "auth_ok" || authResult.HAVersion != expectedVersion {
		return homeAssistantCurrentUser{}, "", errors.New("Home Assistant rejected the authenticated WebSocket session or changed version")
	}

	const commandID int64 = 1
	if err := wsjson.Write(wsCtx, conn, map[string]any{"id": commandID, "type": "auth/current_user"}); err != nil {
		return homeAssistantCurrentUser{}, "", homeAssistantWebSocketError(wsCtx, "Home Assistant current-user request failed", err)
	}
	var response homeAssistantWSResult
	if err := wsjson.Read(wsCtx, conn, &response); err != nil {
		return homeAssistantCurrentUser{}, "", homeAssistantWebSocketError(wsCtx, "Home Assistant current-user readback failed", err)
	}
	if response.ID != commandID || response.Type != "result" || !response.Success || len(response.Result) == 0 {
		return homeAssistantCurrentUser{}, "", errors.New("Home Assistant current-user readback was unsuccessful")
	}
	var user homeAssistantCurrentUser
	if err := json.Unmarshal(response.Result, &user); err != nil {
		return homeAssistantCurrentUser{}, "", fmt.Errorf("Home Assistant current-user payload was invalid: %w", err)
	}
	return user, strings.TrimSpace(greeting.HAVersion), nil
}

func homeAssistantWebSocketError(ctx context.Context, phase string, err error) error {
	failure := errors.New(phase)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return errors.Join(failure, ctxErr)
	}
	if errors.Is(err, context.Canceled) {
		return errors.Join(failure, context.Canceled)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return errors.Join(failure, context.DeadlineExceeded)
	}
	return failure
}

func homeAssistantJSONRequest(ctx context.Context, client *http.Client, baseURL, method, path string, payload any, token string, out any) error {
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	endpoint, err := homeAssistantEndpointURL(baseURL, path)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := readHomeAssistantBody(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &homeAssistantHTTPError{method: method, path: path, status: resp.StatusCode}
	}
	if out == nil {
		return nil
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return errors.New("Home Assistant returned an empty JSON response")
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("Home Assistant returned invalid JSON: %w", err)
	}
	return nil
}

func homeAssistantFormRequest(ctx context.Context, client *http.Client, baseURL, path string, values url.Values, out any) error {
	endpoint, err := homeAssistantEndpointURL(baseURL, path)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := readHomeAssistantBody(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &homeAssistantHTTPError{method: http.MethodPost, path: path, status: resp.StatusCode}
	}
	if out == nil {
		return nil
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return errors.New("Home Assistant returned an empty JSON response")
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("Home Assistant returned invalid JSON: %w", err)
	}
	return nil
}

func readHomeAssistantBody(body io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(body, homeAssistantBodyLimit+1))
	if err != nil {
		return nil, err
	}
	if len(data) > homeAssistantBodyLimit {
		return nil, errors.New("Home Assistant response exceeded the bounded setup limit")
	}
	return data, nil
}

func normalizeHomeAssistantBaseURL(raw string) (string, error) {
	trimmed := strings.TrimRight(strings.TrimSpace(raw), "/")
	u, err := url.Parse(trimmed)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", errors.New("Home Assistant setup requires an absolute HTTP(S) URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", errors.New("Home Assistant setup accepts only HTTP(S) URLs")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("Home Assistant setup URL cannot contain credentials, query, or fragment data")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = ""
	return strings.TrimRight(u.String(), "/"), nil
}

func homeAssistantEndpointURL(baseURL, path string) (string, error) {
	if path == "" || !strings.HasPrefix(path, "/") {
		return "", errors.New("Home Assistant setup endpoint must be an absolute path")
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	u.Path = strings.TrimRight(u.Path, "/") + path
	u.RawPath = ""
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}

func homeAssistantWebSocketURL(baseURL string) (string, error) {
	endpoint, err := homeAssistantEndpointURL(baseURL, "/api/websocket")
	if err != nil {
		return "", err
	}
	return endpoint, nil
}

func cloneHomeAssistantHTTPClient(client *http.Client) *http.Client {
	clone := *client
	if clone.Timeout <= 0 {
		clone.Timeout = 20 * time.Second
	}
	oldCheckRedirect := clone.CheckRedirect
	clone.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if oldCheckRedirect != nil {
			if err := oldCheckRedirect(req, via); err != nil {
				return err
			}
		}
		return errors.New("Home Assistant setup refuses HTTP redirects")
	}
	if clone.Transport == nil {
		clone.Transport = &http.Transport{Proxy: nil}
	}
	return &clone
}
