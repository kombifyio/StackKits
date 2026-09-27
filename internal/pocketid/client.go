// Package pocketid is a thin HTTP client for the PocketID admin API.
//
// API surface notes (verified against pocket-id/pocket-id v2.16.0,
// commit bb05e6922444c33eec87466919f1bcf105967825):
//
//   - Auth header is `X-API-Key` (case-insensitive). PocketID does NOT use
//     `Authorization: Bearer`. JWT cookies and API keys are accepted by the
//     same admin endpoints.
//   - Bootstrap is solved by the `STATIC_API_KEY` env variable on the
//     PocketID container (added in v1229, ships in v2+). When set, PocketID
//     auto-creates a "Static API User" admin on first request and accepts
//     that env value as a valid `X-API-Key` indefinitely. There is no
//     `/api/setup` endpoint and no first-run wizard exposed via the JSON API
//     in the WebAuthn-only v2 line; the UI flow instead requires registering
//     a passkey through the browser.
//   - Health endpoint is `/healthz` (returns 204), NOT `/api/health`.
//   - Group membership is keyed by group ID, not name:
//     `PUT /api/user-groups/:id/users` with `{"userIds":[...]}`.
//   - `CreateUser` requires `firstName` plus a valid email if email is set.
//   - OIDC client registration accepts `callbackURLs` (camelCase). The
//     client secret is created in a separate call: `POST /api/oidc/clients/:id/secrets`.
//     StackKits supplies the secret value so it can place the same one-time
//     response into owner custody without depending on server-side generation.
//
// Given those findings, BootstrapInitialAdmin is implemented as a
// verification call against `GET /api/users` using the configured
// admin token. If the token works, bootstrap is considered already
// complete and we return ErrAlreadyBootstrapped (the canonical
// "static-api-key path is already provisioned" signal). Callers
// (Tasks 6-9) are expected to render the static API key into the
// PocketID container env at deploy time, so the fact that the token
// works is the bootstrap success criterion.
package pocketid

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	apiKeyHeader    = "X-API-Key"
	maxResponseSize = 1 << 20 // 1 MiB cap on response bodies
	healthzPath     = "/healthz"
)

// ErrAlreadyBootstrapped is returned by BootstrapInitialAdmin when PocketID
// already accepts the configured admin token (typically because the
// STATIC_API_KEY env var is set and the static admin user has been
// materialized on a previous call).
var ErrAlreadyBootstrapped = errors.New("pocketid: instance already bootstrapped")

// ErrAlreadyExists is returned by create-style methods (e.g. CreateUserGroup)
// when the resource is already present at the API. Callers can use errors.Is
// to detect this and treat it as success (idempotent semantics).
var ErrAlreadyExists = errors.New("pocketid: resource already exists")

// ErrNotFound identifies an exact PocketID resource lookup miss.
var ErrNotFound = errors.New("pocketid: resource not found")

// Client is a thin HTTP client for the PocketID admin API.
type Client struct {
	BaseURL    string
	AdminToken string
	HTTP       *http.Client
}

// NewClient creates a PocketID admin client. baseURL must NOT end in a slash;
// it is the public origin (e.g. "https://id.stack.local"). adminToken is the
// value rendered into the PocketID container as STATIC_API_KEY.
func NewClient(baseURL, adminToken string) *Client {
	return &Client{
		BaseURL:    baseURL,
		AdminToken: adminToken,
		HTTP:       &http.Client{Timeout: 30 * time.Second},
	}
}

// CreateUserRequest is the payload for CreateUser. Email is optional but
// when set must be a valid address. FirstName is recommended (PocketID
// stores it as a non-nullable column). IsAdmin grants admin scope.
type CreateUserRequest struct {
	Username      string   `json:"username"`
	Email         string   `json:"email,omitempty"`
	FirstName     string   `json:"firstName,omitempty"`
	LastName      string   `json:"lastName,omitempty"`
	DisplayName   string   `json:"displayName,omitempty"`
	IsAdmin       bool     `json:"isAdmin"`
	EmailVerified bool     `json:"emailVerified,omitempty"`
	Disabled      bool     `json:"disabled,omitempty"`
	UserGroupIDs  []string `json:"userGroupIds,omitempty"`
}

// User is the subset of the PocketID user DTO we care about.
type User struct {
	ID            string        `json:"id"`
	Username      string        `json:"username"`
	Email         string        `json:"email,omitempty"`
	FirstName     string        `json:"firstName,omitempty"`
	LastName      string        `json:"lastName,omitempty"`
	DisplayName   string        `json:"displayName,omitempty"`
	IsAdmin       bool          `json:"isAdmin"`
	EmailVerified bool          `json:"emailVerified"`
	Disabled      bool          `json:"disabled,omitempty"`
	UserGroups    []UserGroup   `json:"userGroups,omitempty"`
	CustomClaims  []CustomClaim `json:"customClaims,omitempty"`
}

// SMTPConfiguration is the owner-approved PocketID delivery configuration
// used for email verification. StackKits deliberately does not expose
// PocketID's plaintext or skip-verification modes.
type SMTPConfiguration struct {
	Host     string
	Port     int
	From     string
	User     string
	Password string
	TLS      string
}

type appConfigVariable struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// pocketID216ApplicationConfigKeys is the full replacement document exposed
// by the pinned v2.16 API. Requiring every key prevents a partial or masked
// read from resetting unrelated identity settings on PUT.
var pocketID216ApplicationConfigKeys = []string{
	"appName", "sessionDuration", "homePageUrl", "emailsVerified", "disableAnimations",
	"allowOwnAccountEdit", "allowUserSignups", "signupDefaultUserGroupIDs", "signupDefaultCustomClaims",
	"accentColor", "requireUserEmail", "smtpHost", "smtpPort", "smtpFrom", "smtpUser", "smtpPassword",
	"smtpTls", "smtpSkipCertVerify", "ldapEnabled", "ldapUrl", "ldapBindDn", "ldapBindPassword", "ldapBase",
	"ldapUserSearchFilter", "ldapUserGroupSearchFilter", "ldapSkipCertVerify", "ldapAttributeUserUniqueIdentifier",
	"ldapAttributeUserUsername", "ldapAttributeUserEmail", "ldapAttributeUserFirstName", "ldapAttributeUserLastName",
	"ldapAttributeUserDisplayName", "ldapAttributeUserProfilePicture", "ldapAttributeGroupMember",
	"ldapAttributeGroupUniqueIdentifier", "ldapAttributeGroupName", "ldapAdminGroupName", "ldapSoftDeleteUsers",
	"webauthnUserVerification", "webauthnAllowSyncedPasskeys", "webauthnAuthenticatorAttachment",
	"emailOneTimeAccessAsAdminEnabled", "emailOneTimeAccessAsUnauthenticatedEnabled", "emailLoginNotificationEnabled",
	"emailApiKeyExpirationEnabled", "emailVerificationEnabled", "cimdUrlAllowlist",
}

// ConfigureEmailVerificationSMTP preserves PocketID's complete current
// application configuration while replacing the bounded SMTP and email
// verification values. PocketID v2.16 exposes a full-replacement API, so a
// partial request would reset unrelated identity policy to defaults.
func (c *Client) ConfigureEmailVerificationSMTP(ctx context.Context, smtp SMTPConfiguration) error {
	host := strings.TrimSpace(smtp.Host)
	from := strings.TrimSpace(smtp.From)
	user := strings.TrimSpace(smtp.User)
	password := smtp.Password
	tls := strings.TrimSpace(smtp.TLS)
	if host == "" || strings.ContainsAny(host, "/?#@\r\n\t ") || smtp.Port < 1 || smtp.Port > 65535 {
		return errors.New("configure email verification: SMTP host and port are invalid")
	}
	address, err := mail.ParseAddress(from)
	if err != nil || address.Address != from {
		return errors.New("configure email verification: SMTP from address is invalid")
	}
	if (user == "") != (password == "") {
		return errors.New("configure email verification: SMTP user and password must be supplied together")
	}
	if tls != "starttls" && tls != "tls" {
		return errors.New("configure email verification: SMTP TLS must be starttls or tls")
	}

	current, err := c.readApplicationConfiguration(ctx)
	if err != nil {
		return err
	}
	// v2.16 masks sensitive values as XXXXXXXXXX when UI configuration is
	// disabled. Its PUT endpoint replaces the whole document; replaying that
	// marker would destroy unrelated LDAP custody. Refuse before mutation.
	if current["ldapBindPassword"] == "XXXXXXXXXX" {
		return errors.New("configure email verification: PocketID masked unrelated secret configuration")
	}
	current["smtpHost"] = host
	current["smtpPort"] = strconv.Itoa(smtp.Port)
	current["smtpFrom"] = from
	current["smtpUser"] = user
	current["smtpPassword"] = password
	current["smtpTls"] = tls
	current["smtpSkipCertVerify"] = "false"
	current["requireUserEmail"] = "true"
	current["emailsVerified"] = "false"
	current["emailVerificationEnabled"] = "true"

	var saved []appConfigVariable
	if err := c.do(ctx, http.MethodPut, "/api/application-configuration", current, &saved); err != nil {
		return fmt.Errorf("configure email verification: %w", err)
	}
	if err := verifyEmailConfiguration(saved, current); err != nil {
		return err
	}
	readback, err := c.readApplicationConfiguration(ctx)
	if err != nil {
		return err
	}
	return verifyEmailConfigurationMap(readback, current)
}

func (c *Client) readApplicationConfiguration(ctx context.Context) (map[string]string, error) {
	var variables []appConfigVariable
	if err := c.do(ctx, http.MethodGet, "/api/application-configuration/all", nil, &variables); err != nil {
		return nil, fmt.Errorf("read PocketID application configuration: %w", err)
	}
	configuration := make(map[string]string, len(variables))
	for _, variable := range variables {
		if variable.Key == "" {
			return nil, errors.New("read PocketID application configuration: empty key")
		}
		if _, duplicate := configuration[variable.Key]; duplicate {
			return nil, errors.New("read PocketID application configuration: duplicate key")
		}
		configuration[variable.Key] = variable.Value
	}
	if len(configuration) != len(pocketID216ApplicationConfigKeys) {
		return nil, errors.New("read PocketID application configuration: pinned field set differs")
	}
	for _, required := range pocketID216ApplicationConfigKeys {
		if _, ok := configuration[required]; !ok {
			return nil, errors.New("read PocketID application configuration: pinned field set is incomplete")
		}
	}
	return configuration, nil
}

func verifyEmailConfiguration(variables []appConfigVariable, expected map[string]string) error {
	actual := make(map[string]string, len(variables))
	for _, variable := range variables {
		if variable.Key == "" {
			return errors.New("configure email verification: readback is malformed")
		}
		if _, duplicate := actual[variable.Key]; duplicate {
			return errors.New("configure email verification: readback contains a duplicate key")
		}
		actual[variable.Key] = variable.Value
	}
	return verifyEmailConfigurationMap(actual, expected)
}

func verifyEmailConfigurationMap(actual, expected map[string]string) error {
	for _, key := range []string{"smtpHost", "smtpPort", "smtpFrom", "smtpUser", "smtpPassword", "smtpTls", "smtpSkipCertVerify", "requireUserEmail", "emailsVerified", "emailVerificationEnabled"} {
		if actual[key] != expected[key] {
			return errors.New("configure email verification: PocketID readback differs from the owner-approved settings")
		}
	}
	return nil
}

// WebAuthnCredential is the secret-free registration metadata returned by the
// admin user credential endpoint.
type WebAuthnCredential struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

// ListUserWebAuthnCredentials returns passkeys registered to one exact user.
func (c *Client) ListUserWebAuthnCredentials(ctx context.Context, userID string) ([]WebAuthnCredential, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" || strings.ContainsAny(userID, "/?#") {
		return nil, errors.New("list user WebAuthn credentials: subject is invalid")
	}
	var credentials []WebAuthnCredential
	if err := c.do(ctx, http.MethodGet, "/api/users/"+userID+"/webauthn-credentials", nil, &credentials); err != nil {
		return nil, fmt.Errorf("list user WebAuthn credentials: %w", err)
	}
	return credentials, nil
}

// FindUsersByUsername searches PocketID and returns only exact username
// matches. PocketID's server-side search is deliberately fuzzy, so the local
// owner binder must never accept a near-match as identity evidence.
func (c *Client) FindUsersByUsername(ctx context.Context, username string) ([]User, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return nil, errors.New("find users: username is required")
	}
	var response struct {
		Data []User `json:"data"`
	}
	path := "/api/users?search=" + url.QueryEscape(username) + "&pagination%5Blimit%5D=100"
	if err := c.do(ctx, http.MethodGet, path, nil, &response); err != nil {
		return nil, fmt.Errorf("find user %q: %w", username, err)
	}
	result := make([]User, 0, 1)
	for _, user := range response.Data {
		if user.Username == username {
			result = append(result, user)
		}
	}
	return result, nil
}

// ListUsers returns every current user page. Pocket ID v2.16 clamps requests
// past the end back to the final page, so pagination metadata determines when
// to stop. Duplicate subjects or a changing collection fail closed.
// https://github.com/pocket-id/pocket-id/blob/v2.16.0/backend/internal/utils/list_request_util.go
func (c *Client) ListUsers(ctx context.Context) ([]User, error) {
	result := make([]User, 0)
	seen := map[string]bool{}
	var expectedTotal int64 = -1
	for page := 1; ; page++ {
		var response struct {
			Data       []User `json:"data"`
			Pagination struct {
				CurrentPage int   `json:"currentPage"`
				TotalPages  int   `json:"totalPages"`
				TotalItems  int64 `json:"totalItems"`
			} `json:"pagination"`
		}
		path := fmt.Sprintf("/api/users?pagination%%5Blimit%%5D=100&pagination%%5Bpage%%5D=%d&sort%%5Bcolumn%%5D=username&sort%%5Bdirection%%5D=asc", page)
		if err := c.do(ctx, http.MethodGet, path, nil, &response); err != nil {
			return nil, fmt.Errorf("list users: %w", err)
		}
		pagination := response.Pagination
		if pagination.CurrentPage != page || pagination.TotalPages < page || pagination.TotalItems < 0 {
			return nil, errors.New("list users: invalid pagination response")
		}
		if expectedTotal == -1 {
			expectedTotal = pagination.TotalItems
		}
		if expectedTotal != pagination.TotalItems {
			return nil, errors.New("list users: collection changed during pagination")
		}
		for _, user := range response.Data {
			if user.ID == "" || seen[user.ID] {
				return nil, errors.New("list users: invalid or repeated subject in paginated response")
			}
			seen[user.ID] = true
			result = append(result, user)
		}
		if page == pagination.TotalPages {
			if int64(len(result)) != expectedTotal {
				return nil, errors.New("list users: incomplete paginated response")
			}
			return result, nil
		}
	}
}

// DeleteUser removes one PocketID subject. Callers must refuse owner and
// break-glass identities before invoking this.
func (c *Client) DeleteUser(ctx context.Context, userID string) error {
	userID = strings.TrimSpace(userID)
	if userID == "" || strings.ContainsAny(userID, "/?#") {
		return errors.New("delete user: subject is invalid")
	}
	if err := c.do(ctx, http.MethodDelete, "/api/users/"+userID, nil, nil); err != nil {
		return fmt.Errorf("delete user %q: %w", userID, err)
	}
	return nil
}

// GetUser reads the exact PocketID subject including its current groups.
func (c *Client) GetUser(ctx context.Context, userID string) (*User, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" || strings.ContainsAny(userID, "/?#") {
		return nil, errors.New("get user: subject is invalid")
	}
	var user User
	if err := c.do(ctx, http.MethodGet, "/api/users/"+userID, nil, &user); err != nil {
		return nil, fmt.Errorf("get user %q: %w", userID, err)
	}
	return &user, nil
}

// UpdateUserGroups replaces PocketID's group-ID set in one request and
// returns the server readback. Callers must pass the complete desired set.
func (c *Client) UpdateUserGroups(ctx context.Context, userID string, groupIDs []string) (*User, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" || strings.ContainsAny(userID, "/?#") || len(groupIDs) == 0 {
		return nil, errors.New("update user groups: subject and group IDs are required")
	}
	normalized := append([]string(nil), groupIDs...)
	slices.Sort(normalized)
	normalized = slices.Compact(normalized)
	for _, groupID := range normalized {
		if strings.TrimSpace(groupID) == "" {
			return nil, errors.New("update user groups: group ID is invalid")
		}
	}
	body := struct {
		UserGroupIDs []string `json:"userGroupIds"`
	}{UserGroupIDs: normalized}
	var user User
	if err := c.do(ctx, http.MethodPut, "/api/users/"+userID+"/user-groups", body, &user); err != nil {
		return nil, fmt.Errorf("update user %q groups: %w", userID, err)
	}
	return &user, nil
}

// userCreateResp tolerates email being either string, null, or absent.
type userCreateResp struct {
	ID          string  `json:"id"`
	Username    string  `json:"username"`
	Email       *string `json:"email"`
	FirstName   string  `json:"firstName"`
	LastName    *string `json:"lastName"`
	DisplayName string  `json:"displayName"`
	IsAdmin     bool    `json:"isAdmin"`
}

// CreateUser registers a new PocketID account. Returns the created user
// (including server-assigned ID).
func (c *Client) CreateUser(ctx context.Context, req CreateUserRequest) (*User, error) {
	var resp userCreateResp
	if err := c.do(ctx, http.MethodPost, "/api/users", req, &resp); err != nil {
		return nil, fmt.Errorf("create user %q: %w", req.Username, err)
	}
	user := &User{
		ID:          resp.ID,
		Username:    resp.Username,
		FirstName:   resp.FirstName,
		DisplayName: resp.DisplayName,
		IsAdmin:     resp.IsAdmin,
	}
	if resp.Email != nil {
		user.Email = *resp.Email
	}
	if resp.LastName != nil {
		user.LastName = *resp.LastName
	}
	return user, nil
}

// AddUserToGroup adds a user to a group identified by ID. PocketID's API
// uses group IDs (UUIDs), not names, so callers must resolve the name to
// an ID first via GetGroupIDByName.
func (c *Client) AddUserToGroup(ctx context.Context, userID, groupID string) error {
	body := struct {
		UserGroupIDs []string `json:"userGroupIds"`
	}{UserGroupIDs: []string{groupID}}
	path := fmt.Sprintf("/api/users/%s/user-groups", userID)
	if err := c.do(ctx, http.MethodPut, path, body, nil); err != nil {
		return fmt.Errorf("add user %s to group %s: %w", userID, groupID, err)
	}
	return nil
}

// CreateUserGroupRequest is the body for POST /api/user-groups. PocketID
// expects both `name` (machine identifier, must be unique) and `friendlyName`
// (display label).
type CreateUserGroupRequest struct {
	Name         string `json:"name"`
	FriendlyName string `json:"friendlyName"`
}

// UserGroup is the subset of the PocketID user-group DTO we care about.
type UserGroup struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	FriendlyName string `json:"friendlyName"`
}

// CreateUserGroup creates a new user group in PocketID and returns the
// created object. A 409 Conflict (group already exists) maps to
// ErrAlreadyExists so callers can treat re-runs as idempotent — chain it with
// errors.Is(err, pocketid.ErrAlreadyExists) at the call site.
//
// Wire format empirically verified against PocketID v2.6.x by Task 14's
// integration test:
//
//	POST /api/user-groups
//	body: {"name":"...","friendlyName":"..."}
//	201 Created -> {"id":"...","name":"...","friendlyName":"...",...}
//	409 Conflict -> already exists (idempotent for callers)
func (c *Client) CreateUserGroup(ctx context.Context, req CreateUserGroupRequest) (*UserGroup, error) {
	var resp UserGroup
	if err := c.do(ctx, http.MethodPost, "/api/user-groups", req, &resp); err != nil {
		var httpErr *HTTPError
		if errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusConflict {
			return nil, ErrAlreadyExists
		}
		return nil, fmt.Errorf("create user-group %q: %w", req.Name, err)
	}
	return &resp, nil
}

// GetGroupIDByName resolves a user-group name to its server-side ID.
// Returns "" with a nil error if no group matches.
func (c *Client) GetGroupIDByName(ctx context.Context, name string) (string, error) {
	type groupItem struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	type page struct {
		Data []groupItem `json:"data"`
	}
	var resp page
	path := "/api/user-groups?search=" + name
	if err := c.do(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return "", fmt.Errorf("list user-groups: %w", err)
	}
	for _, g := range resp.Data {
		if g.Name == name {
			return g.ID, nil
		}
	}
	return "", nil
}

// CreateOneTimeAccessToken issues a one-time-access token for the given user
// that the holder redeems through ActivationURL (`/lc/<token>`) to enroll a
// WebAuthn credential. PocketID v2 is passkey-only, so this is the only way
// to bootstrap a freshly-provisioned owner account into a usable state.
//
// PocketID v2.7 accepts the TTL as a Go-duration string in the `ttl` field.
// The returned string is the raw token (not a full URL); callers compose the
// setup URL themselves.
//
// Endpoint: `POST /api/users/:id/one-time-access-token`.
func (c *Client) CreateOneTimeAccessToken(ctx context.Context, userID string, ttl time.Duration) (string, error) {
	if userID == "" {
		return "", fmt.Errorf("create one-time-access-token: userID is required")
	}
	if ttl <= 0 {
		return "", fmt.Errorf("create one-time-access-token: ttl must be positive, got %s", ttl)
	}
	body := struct {
		TTL string `json:"ttl"`
	}{
		TTL: ttl.String(),
	}
	var resp struct {
		Token string `json:"token"`
	}
	path := fmt.Sprintf("/api/users/%s/one-time-access-token", userID)
	if err := c.do(ctx, http.MethodPost, path, body, &resp); err != nil {
		return "", fmt.Errorf("create one-time-access-token for %s: %w", userID, err)
	}
	if resp.Token == "" {
		return "", fmt.Errorf("create one-time-access-token for %s: empty token in response", userID)
	}
	return resp.Token, nil
}

// RetireOneTimeAccessToken makes a previously issued one-time-access token
// unusable. PocketID v2.16 has no revocation endpoint and keeps a token
// redeemable until it is exchanged or its TTL ends, so the only way to retire
// a superseded link is to redeem it here: the exchange atomically deletes the
// token, and the session it returns is discarded unread. PocketID records the
// exchange as a one-time-access sign-in in its audit log.
//
// A 401 means the token was already consumed or has expired, which is the
// wanted end state. The token is a secret: it never appears in an error.
//
// Endpoint: `POST /api/one-time-access-token/:token` (unauthenticated).
func (c *Client) RetireOneTimeAccessToken(ctx context.Context, token string) error {
	if strings.TrimSpace(token) == "" {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.BaseURL+"/api/one-time-access-token/"+url.PathEscape(token), nil)
	if err != nil {
		return errors.New("retire one-time-access-token: build request failed")
	}
	req.Header.Set("Accept", "application/json")
	httpClient := *c.HTTP
	httpClient.Jar = nil
	resp, err := httpClient.Do(req)
	if err != nil {
		return errors.New("retire one-time-access-token: PocketID is unreachable")
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseSize))
	_ = resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK, http.StatusUnauthorized:
		return nil
	default:
		return fmt.Errorf("retire one-time-access-token: PocketID answered HTTP %d", resp.StatusCode)
	}
}

// RegisterClientRequest is the payload for RegisterOIDCClient.
type RegisterClientRequest struct {
	RequiresReauthentication bool     `json:"requiresReauthentication"`
	ID                       string   `json:"id,omitempty"`
	Name                     string   `json:"name"`
	CallbackURLs             []string `json:"callbackURLs"`
	IsPublic                 bool     `json:"isPublic"`
	PkceEnabled              bool     `json:"pkceEnabled,omitempty"`
	IsGroupRestricted        bool     `json:"isGroupRestricted"`
}

// OIDCClient describes a registered OIDC client. Secret is only populated
// after CreateClientSecret returns.
type OIDCClient struct {
	Credentials struct {
		FederatedIdentities []json.RawMessage `json:"federatedIdentities"`
	} `json:"credentials"`
	RequiresReauthentication bool        `json:"requiresReauthentication"`
	PkceEnabled              bool        `json:"pkceEnabled"`
	ID                       string      `json:"id"`
	Name                     string      `json:"name"`
	CallbackURLs             []string    `json:"callbackURLs"`
	IsPublic                 bool        `json:"isPublic"`
	IsGroupRestricted        bool        `json:"isGroupRestricted"`
	AllowedUserGroups        []UserGroup `json:"allowedUserGroups,omitempty"`
	Secret                   string      `json:"-"`
}

// GetOIDCClient reads the exact client including its allowed group projection.
func (c *Client) GetOIDCClient(ctx context.Context, clientID string) (*OIDCClient, error) {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" || strings.ContainsAny(clientID, "/?#") {
		return nil, errors.New("get oidc client: id is invalid")
	}
	var client OIDCClient
	if err := c.do(ctx, http.MethodGet, "/api/oidc/clients/"+clientID, nil, &client); err != nil {
		var httpErr *HTTPError
		if errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusNotFound {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get oidc client %q: %w", clientID, err)
	}
	return &client, nil
}

// CreateOIDCClientSecret adds a secret to the confidential client. The raw
// value is chosen locally, returned once, and must remain in local private
// custody. Callers invoke this only when no custodied secret is available.
func (c *Client) CreateOIDCClientSecret(ctx context.Context, clientID string) (string, error) {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" || strings.ContainsAny(clientID, "/?#") {
		return "", errors.New("create oidc client secret: id is invalid")
	}
	material := make([]byte, 32)
	if _, err := rand.Read(material); err != nil {
		return "", fmt.Errorf("create oidc client secret: generate material: %w", err)
	}
	secret := base64.RawURLEncoding.EncodeToString(material)
	clear(material)
	request := struct {
		Secret string `json:"secret"`
	}{Secret: secret}
	var response struct {
		Secret string `json:"secret"`
	}
	if err := c.do(ctx, http.MethodPost, "/api/oidc/clients/"+clientID+"/secrets", request, &response); err != nil {
		return "", fmt.Errorf("create secret for oidc client %s: %w", clientID, err)
	}
	if response.Secret != secret {
		return "", errors.New("create oidc client secret: PocketID did not return the chosen secret")
	}
	return response.Secret, nil
}

// UpdateOIDCClientAllowedUserGroups binds the client to the complete desired
// PocketID group-ID set.
func (c *Client) UpdateOIDCClientAllowedUserGroups(
	ctx context.Context,
	clientID string,
	groupIDs []string,
) (*OIDCClient, error) {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" || strings.ContainsAny(clientID, "/?#") || len(groupIDs) == 0 {
		return nil, errors.New("update oidc client groups: input is invalid")
	}
	body := struct {
		UserGroupIDs []string `json:"userGroupIds"`
	}{UserGroupIDs: append([]string(nil), groupIDs...)}
	var client OIDCClient
	if err := c.do(ctx, http.MethodPut, "/api/oidc/clients/"+clientID+"/allowed-user-groups", body, &client); err != nil {
		return nil, fmt.Errorf("update oidc client %s groups: %w", clientID, err)
	}
	return &client, nil
}

// RegisterOIDCClient creates an OIDC client (e.g. TinyAuth) and immediately
// generates a secret only for a confidential client. Public PKCE clients never
// create or expose a shared secret. The returned OIDCClient.Secret is the
// raw value — record it; PocketID will not return it again.
func (c *Client) RegisterOIDCClient(ctx context.Context, req RegisterClientRequest) (*OIDCClient, error) {
	var client OIDCClient
	if err := c.do(ctx, http.MethodPost, "/api/oidc/clients", req, &client); err != nil {
		return nil, fmt.Errorf("register oidc client %q: %w", req.Name, err)
	}
	if req.ID != "" && client.ID != req.ID {
		return nil, errors.New("register oidc client: PocketID did not preserve the chosen client ID")
	}
	if req.IsPublic {
		if !client.IsPublic || !client.PkceEnabled {
			return nil, errors.New("register oidc client: public PKCE policy was not preserved")
		}
		return &client, nil
	}
	secret, err := c.CreateOIDCClientSecret(ctx, client.ID)
	if err != nil {
		return nil, err
	}
	client.Secret = secret
	return &client, nil
}

// WaitHealthy polls the PocketID `/healthz` endpoint until it returns 2xx
// or the context/timeout fires.
func (c *Client) WaitHealthy(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()

	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	for {
		if err := c.healthzOnce(ctx); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for pocketid healthy: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func (c *Client) healthzOnce(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+healthzPath, nil)
	if err != nil {
		return err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("pocketid healthz: status %d", resp.StatusCode)
	}
	return nil
}

// BootstrapInitialAdmin verifies that the configured AdminToken is accepted
// by PocketID. PocketID v2 has no /api/setup JSON endpoint; admin bootstrap
// is performed by setting STATIC_API_KEY on the container, after which the
// first authenticated request materializes a built-in "Static API User"
// admin. This method exercises that path and reports success or failure.
//
// The email/username/password parameters are accepted for API symmetry with
// the original Phase-1 plan but are ignored — they have no equivalent in the
// PocketID v2 setup flow. Callers should instead invoke CreateUser to
// provision the human owner account after this returns.
//
// Returns:
//   - ErrAlreadyBootstrapped when the token is already accepted (the normal
//     case once STATIC_API_KEY has been provisioned).
//   - A wrapped HTTP/network error otherwise.
//
// The plan-spec signature returned (adminToken string, err error). The
// returned token is always c.AdminToken on success — callers that need to
// persist a token should use the value they passed into NewClient.
func (c *Client) BootstrapInitialAdmin(ctx context.Context, _, _, _ string) (string, error) {
	if c.AdminToken == "" {
		return "", errors.New("pocketid: bootstrap requires AdminToken to be set " +
			"(provision STATIC_API_KEY on the PocketID container)")
	}
	// A trivial authenticated request: list users with limit=1.
	if err := c.do(ctx, http.MethodGet, "/api/users?pagination[limit]=1", nil, nil); err != nil {
		return "", fmt.Errorf("verify admin token: %w", err)
	}
	return c.AdminToken, ErrAlreadyBootstrapped
}

// do executes a JSON request and decodes the response into out (if non-nil).
// Body may be nil for requests without a payload. Non-2xx responses are
// returned as errors that include the status code so callers can match them
// when needed.
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, reader)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.AdminToken != "" {
		req.Header.Set(apiKeyHeader, c.AdminToken)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))

	if resp.StatusCode/100 != 2 {
		return &HTTPError{
			StatusCode: resp.StatusCode,
			Method:     method,
			Path:       path,
			Body:       string(respBody),
		}
	}

	if out == nil || len(respBody) == 0 {
		return nil
	}
	if err := json.Unmarshal(respBody, out); err != nil {
		return fmt.Errorf("decode response (%s %s): %w", method, path, err)
	}
	return nil
}

// HTTPError represents a non-2xx response from PocketID.
type HTTPError struct {
	StatusCode int
	Method     string
	Path       string
	Body       string
}

// Error implements error.
func (e *HTTPError) Error() string {
	return fmt.Sprintf("pocketid: %s %s -> %d: %s", e.Method, e.Path, e.StatusCode, e.Body)
}

// Unwrap exposes a lookup miss as ErrNotFound so callers can distinguish "this
// resource is gone" from "this resource answered something unexpected" on every
// endpoint, not only the ones that map the status explicitly.
func (e *HTTPError) Unwrap() error {
	if e.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	return nil
}

// UpdateOIDCClient replaces the mutable settings of an existing client (name,
// callbacks, public/PKCE and group restriction). The secret is unchanged.
func (c *Client) UpdateOIDCClient(ctx context.Context, clientID string, req RegisterClientRequest) (*OIDCClient, error) {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" || strings.ContainsAny(clientID, "/?#") {
		return nil, errors.New("update oidc client: id is invalid")
	}
	req.ID = ""
	var client OIDCClient
	if err := c.do(ctx, http.MethodPut, "/api/oidc/clients/"+clientID, req, &client); err != nil {
		return nil, fmt.Errorf("update oidc client %s: %w", clientID, err)
	}
	return &client, nil
}

// OIDCIssuer reads the running server's discovery identity, rather than local
// desired configuration. Domain migration uses it before passkey reenrollment.
func (c *Client) OIDCIssuer(ctx context.Context) (string, error) {
	var discovery struct {
		Issuer string `json:"issuer"`
	}
	if err := c.do(ctx, http.MethodGet, "/.well-known/openid-configuration", nil, &discovery); err != nil {
		return "", err
	}
	if discovery.Issuer == "" {
		return "", errors.New("pocketid: discovery issuer is absent")
	}
	return discovery.Issuer, nil
}
