package appsetup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	// VaultwardenPinnedVersion is the release admitted by the native
	// Vaultwarden workload. The adapter refuses to mutate another release.
	VaultwardenPinnedVersion = "1.35.4"

	vaultwardenCompatibilityVersion = "2025.12.0"
	vaultwardenResponseBodyLimit    = 256 << 10
	vaultwardenSetupTimeout         = 3 * time.Minute
	vaultwardenCleanupTimeout       = 5 * time.Second
)

// VaultwardenOwnerRequest contains only the explicit owner invitation input.
// AdminToken is resolved from the signed Apply/deployment custody by the
// caller, used for this bounded operation, and never copied into a result.
type VaultwardenOwnerRequest struct {
	Email           string
	AdminToken      []byte
	ExpectedVersion string
}

// VaultwardenOwnerResult is a secret-free observation of the administrator
// invitation state. It deliberately does not claim personal login,
// encryption-key setup, or client usability.
type VaultwardenOwnerResult struct {
	UserID             string `json:"userId"`
	UserEnabled        bool   `json:"userEnabled"`
	AdminLoginVerified bool   `json:"adminLoginVerified"`
	Preparation        string `json:"preparation"`
	Version            string `json:"version"`

	session *vaultwardenAdminSession
}

// BootstrapVaultwardenOwner authenticates the exact owner-custodied admin
// session, checks the pinned API/config identity, and invites the requested
// email only when no server-side user exists. Existing invited or registered
// users are read back and never reinvited. The admin session must be cleaned up
// by the caller through Cleanup before setup evidence is persisted.
func BootstrapVaultwardenOwner(
	ctx context.Context,
	client *http.Client,
	baseURL string,
	request VaultwardenOwnerRequest,
) (result VaultwardenOwnerResult, returnErr error) {
	if ctx == nil {
		ctx = context.Background()
	}
	setupCtx, cancel := context.WithTimeout(ctx, vaultwardenSetupTimeout)
	defer cancel()
	ctx = setupCtx

	baseURL, err := normalizeVaultwardenBaseURL(baseURL)
	if err != nil {
		return result, err
	}
	email, err := normalizeVaultwardenEmail(request.Email)
	if err != nil {
		return result, err
	}
	if len(request.AdminToken) == 0 {
		return result, errors.New("Vaultwarden owner invitation requires the owner-custodied admin token")
	}
	expectedVersion := strings.TrimSpace(request.ExpectedVersion)
	if expectedVersion == "" {
		expectedVersion = VaultwardenPinnedVersion
	}
	if expectedVersion != VaultwardenPinnedVersion {
		return result, fmt.Errorf("Vaultwarden owner invitation is bound to application version %q", VaultwardenPinnedVersion)
	}

	if client == nil || client.Transport == nil {
		return result, errors.New("Vaultwarden setup requires the already-admitted application HTTP client")
	}
	client = cloneVaultwardenHTTPClient(client)

	var session *vaultwardenAdminSession
	defer func() {
		if returnErr == nil || session == nil {
			return
		}
		cleanupErr := session.cleanup(context.Background())
		session.clear()
		session = nil
		if cleanupErr != nil {
			returnErr = errors.Join(returnErr, errors.New("Vaultwarden admin session cleanup failed"))
		}
	}()

	var version string
	if err := vaultwardenJSONRequest(ctx, client, baseURL, http.MethodGet, "/api/version", nil, nil, &version); err != nil {
		return result, fmt.Errorf("read Vaultwarden release: %w", err)
	}
	version = strings.TrimSpace(version)
	if version != expectedVersion {
		return result, fmt.Errorf("Vaultwarden release differs from the admitted version %q", expectedVersion)
	}
	var config vaultwardenConfigReadback
	if err := vaultwardenJSONRequest(ctx, client, baseURL, http.MethodGet, "/api/config", nil, nil, &config); err != nil {
		return result, fmt.Errorf("read Vaultwarden API configuration: %w", err)
	}
	if config.Version != vaultwardenCompatibilityVersion || config.Server.Name != "Vaultwarden" ||
		config.Server.URL != "https://github.com/dani-garcia/vaultwarden" || config.Settings.DisableUserRegistration == nil ||
		!*config.Settings.DisableUserRegistration {
		return result, errors.New("Vaultwarden API configuration is not the pinned source with closed public signups")
	}

	cookies, loginErr := vaultwardenAdminLogin(ctx, client, baseURL, request.AdminToken)
	if len(cookies) > 0 {
		// Claim the allowlisted session cookie before inspecting the response
		// body or status so every issued session is cleaned up on failure.
		session = &vaultwardenAdminSession{client: client, baseURL: baseURL, cookies: cookies}
	}
	if loginErr != nil {
		return result, loginErr
	}
	if session == nil {
		return result, errors.New("Vaultwarden admin login did not return its authenticated session cookie")
	}

	user, found, err := vaultwardenReadUser(ctx, session, email)
	if err != nil {
		return result, err
	}
	if !found {
		if err := vaultwardenInviteUser(ctx, session, email); err != nil {
			if !errors.Is(err, errVaultwardenUserAlreadyExists) {
				return result, err
			}
		}
		user, found, err = vaultwardenReadUser(ctx, session, email)
		if err != nil {
			return result, err
		}
		if !found {
			return result, errors.New("Vaultwarden accepted the invitation but did not expose the invited owner in admin readback")
		}
	}
	if user.UserEnabled == nil || !*user.UserEnabled {
		return result, errors.New("the requested Vaultwarden owner exists but is disabled")
	}
	identity, err := uuid.Parse(strings.TrimSpace(user.ID))
	if err != nil || identity == uuid.Nil || user.Status == nil {
		return result, errors.New("Vaultwarden owner readback omitted the bounded identity state")
	}
	if user.Email != nil && !strings.EqualFold(strings.TrimSpace(*user.Email), email) {
		return result, errors.New("Vaultwarden owner readback differs from the requested owner")
	}
	preparation, err := vaultwardenPreparation(*user.Status)
	if err != nil {
		return result, err
	}

	result = VaultwardenOwnerResult{
		UserID:             identity.String(),
		UserEnabled:        *user.UserEnabled,
		AdminLoginVerified: true,
		Preparation:        preparation,
		Version:            version,
		session:            session,
	}
	session = nil
	return result, nil
}

// Cleanup requests Vaultwarden's admin logout, then clears the temporary
// cookie value held in memory. The admin JWT is stateless in Vaultwarden, so
// this does not claim server-side token revocation. The extra arguments
// preserve the existing appsetup adapter shape; the authenticated session is
// bound to the client and URL admitted above.
func (r *VaultwardenOwnerResult) Cleanup(ctx context.Context, _ *http.Client, _ string) error {
	if r == nil || r.session == nil {
		return nil
	}
	session := r.session
	r.session = nil
	defer session.clear()
	return session.cleanup(ctx)
}

type vaultwardenAdminSession struct {
	client  *http.Client
	baseURL string
	cookies []vaultwardenCookie
}

type vaultwardenCookie struct {
	name  string
	value string
}

func (s *vaultwardenAdminSession) cleanup(ctx context.Context) error {
	if s == nil || s.client == nil || s.baseURL == "" || len(s.cookies) == 0 {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	cleanupCtx, cancel := context.WithTimeout(ctx, vaultwardenCleanupTimeout)
	defer cancel()
	endpoint, err := vaultwardenEndpointURL(s.baseURL, "/admin/logout")
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(cleanupCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	for _, cookie := range s.cookies {
		req.AddCookie(&http.Cookie{Name: cookie.name, Value: cookie.value})
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := discardVaultwardenBody(resp.Body); err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return fmt.Errorf("Vaultwarden admin logout returned HTTP %d", resp.StatusCode)
	}
	return nil
}

func (s *vaultwardenAdminSession) clear() {
	if s == nil {
		return
	}
	for index := range s.cookies {
		s.cookies[index].name = ""
		s.cookies[index].value = ""
	}
	s.cookies = nil
	s.client = nil
	s.baseURL = ""
}

type vaultwardenConfigReadback struct {
	Version string `json:"version"`
	Server  struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	} `json:"server"`
	Settings struct {
		DisableUserRegistration *bool `json:"disableUserRegistration"`
	} `json:"settings"`
}

type vaultwardenUserReadback struct {
	ID          string  `json:"id"`
	Email       *string `json:"email,omitempty"`
	Status      *int    `json:"_status"`
	UserEnabled *bool   `json:"userEnabled"`
}

var errVaultwardenUserAlreadyExists = errors.New("Vaultwarden owner already exists")

func vaultwardenAdminLogin(ctx context.Context, client *http.Client, baseURL string, token []byte) ([]vaultwardenCookie, error) {
	values := url.Values{"token": {string(token)}}
	resp, err := vaultwardenRequest(ctx, client, baseURL, http.MethodPost, "/admin", strings.NewReader(values.Encode()), "application/x-www-form-urlencoded", nil)
	if err != nil {
		if resp != nil {
			cookies := vaultwardenAdminCookies(resp)
			_ = discardVaultwardenBody(resp.Body)
			_ = resp.Body.Close()
			return cookies, errors.New("Vaultwarden admin login request failed")
		}
		return nil, errors.New("Vaultwarden admin login request failed")
	}
	defer resp.Body.Close()
	cookies := vaultwardenAdminCookies(resp)
	if err := discardVaultwardenBody(resp.Body); err != nil {
		return cookies, errors.New("Vaultwarden admin login response was not bounded")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return cookies, fmt.Errorf("Vaultwarden admin login returned HTTP %d", resp.StatusCode)
	}
	if len(cookies) == 0 {
		return cookies, errors.New("Vaultwarden admin login did not return its authenticated session cookie")
	}
	return cookies, nil
}

func vaultwardenAdminCookies(resp *http.Response) []vaultwardenCookie {
	if resp == nil {
		return nil
	}
	var cookies []vaultwardenCookie
	for _, cookie := range resp.Cookies() {
		if cookie.Name == "VW_ADMIN" && cookie.Value != "" {
			cookies = append(cookies, vaultwardenCookie{name: cookie.Name, value: cookie.Value})
		}
	}
	return cookies
}

func vaultwardenReadUser(ctx context.Context, session *vaultwardenAdminSession, email string) (vaultwardenUserReadback, bool, error) {
	path := "/admin/users/by-mail/" + email
	resp, err := session.request(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return vaultwardenUserReadback{}, false, errors.New("read Vaultwarden owner invitation state failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		if err := discardVaultwardenBody(resp.Body); err != nil {
			return vaultwardenUserReadback{}, false, err
		}
		return vaultwardenUserReadback{}, false, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_ = discardVaultwardenBody(resp.Body)
		return vaultwardenUserReadback{}, false, fmt.Errorf("Vaultwarden owner readback returned HTTP %d", resp.StatusCode)
	}
	var user vaultwardenUserReadback
	if err := decodeVaultwardenJSON(resp.Body, &user); err != nil {
		return vaultwardenUserReadback{}, false, errors.New("Vaultwarden owner readback was not a bounded allowlisted JSON result")
	}
	return user, true, nil
}

func vaultwardenInviteUser(ctx context.Context, session *vaultwardenAdminSession, email string) error {
	payload, err := json.Marshal(struct {
		Email string `json:"email"`
	}{Email: email})
	if err != nil {
		return err
	}
	resp, err := session.request(ctx, http.MethodPost, "/admin/invite", bytes.NewReader(payload), "application/json")
	if err != nil {
		return errors.New("Vaultwarden owner invitation request failed")
	}
	defer resp.Body.Close()
	if err := discardVaultwardenBody(resp.Body); err != nil {
		return err
	}
	if resp.StatusCode == http.StatusConflict {
		return errVaultwardenUserAlreadyExists
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("Vaultwarden owner invitation returned HTTP %d", resp.StatusCode)
	}
	return nil
}

func (s *vaultwardenAdminSession) request(ctx context.Context, method, path string, body io.Reader, contentType string) (*http.Response, error) {
	return vaultwardenRequest(ctx, s.client, s.baseURL, method, path, body, contentType, s.cookies)
}

func vaultwardenJSONRequest(ctx context.Context, client *http.Client, baseURL, method, path string, body io.Reader, cookies []vaultwardenCookie, out any) error {
	resp, err := vaultwardenRequest(ctx, client, baseURL, method, path, body, "", cookies)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_ = discardVaultwardenBody(resp.Body)
		return fmt.Errorf("Vaultwarden %s returned HTTP %d", method, resp.StatusCode)
	}
	if out == nil {
		return discardVaultwardenBody(resp.Body)
	}
	return decodeVaultwardenJSON(resp.Body, out)
}

func vaultwardenRequest(ctx context.Context, client *http.Client, baseURL, method, path string, body io.Reader, contentType string, cookies []vaultwardenCookie) (*http.Response, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	endpoint, err := vaultwardenEndpointURL(baseURL, path)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for _, cookie := range cookies {
		req.AddCookie(&http.Cookie{Name: cookie.name, Value: cookie.value})
	}
	return client.Do(req)
}

func decodeVaultwardenJSON(body io.Reader, out any) error {
	limited := &io.LimitedReader{R: body, N: vaultwardenResponseBodyLimit + 1}
	decoder := json.NewDecoder(limited)
	if err := decoder.Decode(out); err != nil {
		return err
	}
	var trailing struct{}
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("Vaultwarden response had trailing JSON")
	}
	if limited.N <= 0 {
		return errors.New("Vaultwarden response exceeded the bounded setup limit")
	}
	return nil
}

func discardVaultwardenBody(body io.Reader) error {
	read, err := io.Copy(io.Discard, io.LimitReader(body, vaultwardenResponseBodyLimit+1))
	if err != nil {
		return err
	}
	if read > vaultwardenResponseBodyLimit {
		return errors.New("Vaultwarden response exceeded the bounded setup limit")
	}
	return nil
}

func vaultwardenPreparation(status int) (string, error) {
	switch status {
	case 0:
		return "owner-registered", nil
	case 1:
		return "owner-invited", nil
	default:
		return "", errors.New("Vaultwarden owner readback returned an unsupported account state")
	}
}

func normalizeVaultwardenBaseURL(raw string) (string, error) {
	trimmed := strings.TrimRight(strings.TrimSpace(raw), "/")
	u, err := url.Parse(trimmed)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", errors.New("Vaultwarden setup requires an absolute HTTP(S) URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", errors.New("Vaultwarden setup accepts only HTTP(S) URLs")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("Vaultwarden setup URL cannot contain credentials, query, or fragment data")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = ""
	return strings.TrimRight(u.String(), "/"), nil
}

func vaultwardenEndpointURL(baseURL, path string) (string, error) {
	if path == "" || !strings.HasPrefix(path, "/") {
		return "", errors.New("Vaultwarden setup endpoint must be an absolute path")
	}
	u, err := url.Parse(baseURL)
	if err != nil || u.Scheme == "" || u.Host == "" || u.User != nil {
		return "", errors.New("Vaultwarden setup endpoint has an invalid base URL")
	}
	u.Path = strings.TrimRight(u.Path, "/") + path
	u.RawPath = ""
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}

func normalizeVaultwardenEmail(raw string) (string, error) {
	email := strings.ToLower(strings.TrimSpace(raw))
	parsed, err := mail.ParseAddress(email)
	if err != nil || parsed.Address != email || len(email) > 254 || strings.ContainsAny(email, "/?#%\\") ||
		strings.IndexFunc(email, func(r rune) bool { return r <= ' ' || r > 126 }) >= 0 {
		return "", errors.New("Vaultwarden owner invitation requires one valid email address")
	}
	return email, nil
}

func cloneVaultwardenHTTPClient(client *http.Client) *http.Client {
	clone := *client
	if clone.Timeout <= 0 || clone.Timeout > 20*time.Second {
		clone.Timeout = 20 * time.Second
	}
	// Keep redirects as responses so logout's documented 303 can be consumed;
	// every setup request rejects 3xx before it can become a new authority.
	clone.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	// The admitted runtime client does not need a cookie jar for this action;
	// keep the admin cookie only in the short-lived, explicitly cleaned session.
	clone.Jar = nil
	return &clone
}
