package appsetup

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	skerrors "github.com/kombifyio/stackkits/internal/errors"
)

const (
	// CloudrevePinnedVersion is the version admitted by the native Files
	// workload. The server ping is checked before any owner mutation.
	CloudrevePinnedVersion              = "4.18.0"
	cloudreveOwnerSessionCleanupTimeout = 5 * time.Second
)

// CloudreveOwnerRequest contains only owner choices supplied by the caller.
// Password is used during this bounded operation and is never copied into
// CloudreveOwnerResult or setup evidence.
type CloudreveOwnerRequest struct {
	Email           string
	Password        string
	Language        string
	ExpectedVersion string
	// AllowFirstOwnerRegistration must be explicitly selected by the caller
	// before a missing login can cause POST /user. Cloudreve's public API does
	// not expose a user-count/initialized flag, so the adapter never treats an
	// unknown owner as permission to create a normal account by default.
	AllowFirstOwnerRegistration bool
}

// CloudreveOwnerResult is the credential-free readback of a Cloudreve owner
// bootstrap. The temporary login material is private and can only be handed
// to a caller that explicitly owns a legacy session handoff.
type CloudreveOwnerResult struct {
	ServerInitialized bool   `json:"serverInitialized"`
	Version           string `json:"version"`
	UserID            string `json:"userId"`
	UserEmail         string `json:"userEmail"`
	UserGroup         string `json:"userGroup,omitempty"`
	UserIsAdmin       bool   `json:"userIsAdmin"`

	accessToken  string
	refreshToken string
	loginPayload json.RawMessage
}

// CloudreveLoginResponse is the v4 password-login response used by the
// legacy Files session bridge and the native adapter's in-memory handoff.
// The token fields never appear in CloudreveOwnerResult.
type CloudreveLoginResponse struct {
	User struct {
		ID    json.RawMessage     `json:"id"`
		Email string              `json:"email"`
		Group *CloudreveUserGroup `json:"group,omitempty"`
	} `json:"user"`
	Token struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	} `json:"token"`
}

type CloudreveUserGroup struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type cloudreveOwnerReadback struct {
	ID    json.RawMessage     `json:"id"`
	Email string              `json:"email"`
	Group *CloudreveUserGroup `json:"group"`
}

// BootstrapCloudreveOwner performs the single technical Cloudreve owner
// sequence shared by native setup and the legacy Files handoff:
// version/initialization readback, first-user registration when the requested
// owner is absent, password login, current-user readback, and an admin-only
// readback. It does not seed demo content or consult PocketID.
func BootstrapCloudreveOwner(
	ctx context.Context,
	client *http.Client,
	baseURL string,
	request CloudreveOwnerRequest,
) (result CloudreveOwnerResult, returnErr *skerrors.StackKitError) {
	if ctx == nil {
		ctx = context.Background()
	}
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if _, err := cloudreveEndpointURL(baseURL, "/site/ping"); err != nil {
		return CloudreveOwnerResult{}, skerrors.NewValidationError(
			"setup_cloudreve_url_invalid",
			"Cloudreve owner bootstrap requires an absolute HTTP(S) URL",
		)
	}
	email := strings.TrimSpace(request.Email)
	password := request.Password
	if email == "" || strings.TrimSpace(password) == "" {
		return CloudreveOwnerResult{}, skerrors.NewValidationError(
			"setup_credentials_missing",
			"Cloudreve owner bootstrap requires StackKit owner credentials",
			skerrors.WithSuggestion("Provide the Files owner email and password through the selected local setup input"),
		)
	}
	expectedVersion := strings.TrimSpace(request.ExpectedVersion)
	if expectedVersion == "" {
		expectedVersion = CloudrevePinnedVersion
	}
	if expectedVersion != CloudrevePinnedVersion {
		return CloudreveOwnerResult{}, skerrors.NewValidationError(
			"setup_cloudreve_version_unsupported",
			"Cloudreve owner bootstrap is bound to the pinned Files workload version",
			skerrors.WithField("expectedVersion", CloudrevePinnedVersion),
		)
	}
	language := strings.TrimSpace(request.Language)
	if language == "" {
		language = "en-US"
	}
	if client == nil {
		client = NewCloudreveHTTPClient()
		defer client.CloseIdleConnections()
	}

	var accessToken string
	var refreshToken string
	var sessionStarted bool
	defer func() {
		if returnErr == nil || !sessionStarted {
			return
		}
		if refreshToken == "" {
			if returnErr.Fields == nil {
				returnErr.Fields = make(map[string]interface{})
			}
			returnErr.Fields["sessionCleanup"] = "unavailable"
			accessToken = ""
			refreshToken = ""
			return
		}
		cleanupErr := LogoutCloudreveSession(context.Background(), client, baseURL, refreshToken)
		accessToken = ""
		refreshToken = ""
		if cleanupErr != nil {
			if returnErr.Fields == nil {
				returnErr.Fields = make(map[string]interface{})
			}
			returnErr.Fields["sessionCleanup"] = "failed"
		}
	}()

	var version string
	versionData, apiErr := CloudreveJSON(ctx, client, http.MethodGet, baseURL, "/site/ping", "", nil)
	if apiErr != nil {
		return CloudreveOwnerResult{}, cloudreveOwnerDependencyError(
			"cloudreve_server_ping_failed",
			"failed to read the Cloudreve server version",
			"GET /site/ping",
			apiErr,
		)
	}
	if err := json.Unmarshal(versionData, &version); err != nil || strings.TrimSpace(version) == "" {
		return CloudreveOwnerResult{}, skerrors.NewDependencyError(
			"cloudreve_server_version_readback_invalid",
			"Cloudreve server version readback was not a string",
			skerrors.WithField("operation", "GET /site/ping"),
		)
	}
	version = strings.TrimSpace(version)
	if version != expectedVersion {
		return CloudreveOwnerResult{}, skerrors.NewDependencyError(
			"cloudreve_server_version_mismatch",
			"Cloudreve server version does not match the pinned Files workload",
			skerrors.WithField("expectedVersion", expectedVersion),
			skerrors.WithField("observedVersion", version),
		)
	}

	var loginConfig struct {
		RegisterEnabled *bool `json:"register_enabled"`
	}
	configData, apiErr := CloudreveJSON(ctx, client, http.MethodGet, baseURL, "/site/config/login", "", nil)
	if apiErr != nil {
		return CloudreveOwnerResult{}, cloudreveOwnerDependencyError(
			"cloudreve_initialization_readback_failed",
			"failed to read the initialized Cloudreve login configuration",
			"GET /site/config/login",
			apiErr,
		)
	}
	if err := json.Unmarshal(configData, &loginConfig); err != nil || loginConfig.RegisterEnabled == nil {
		return CloudreveOwnerResult{}, skerrors.NewDependencyError(
			"cloudreve_initialization_readback_invalid",
			"Cloudreve initialization readback omitted the login configuration",
			skerrors.WithField("operation", "GET /site/config/login"),
		)
	}

	loginPayload, parsed, loginErr := CloudreveLogin(ctx, client, baseURL, email, password)
	accessToken = strings.TrimSpace(parsed.Token.AccessToken)
	refreshToken = strings.TrimSpace(parsed.Token.RefreshToken)
	sessionStarted = accessToken != "" || refreshToken != ""
	if loginErr != nil {
		if !CloudreveOwnerNotFound(loginErr) {
			return CloudreveOwnerResult{}, skerrors.NewAuthError(
				"cloudreve_owner_login_failed",
				"Cloudreve rejected the supplied Files owner credentials",
				skerrors.WithField("operation", "POST /session/token"),
			)
		}
		if !*loginConfig.RegisterEnabled {
			return CloudreveOwnerResult{}, skerrors.NewDependencyError(
				"cloudreve_owner_registration_disabled",
				"Cloudreve rejected the requested owner and public registration is disabled; refusing to create another account",
				skerrors.WithField("operation", "GET /site/config/login"),
			)
		}
		if !request.AllowFirstOwnerRegistration {
			return CloudreveOwnerResult{}, skerrors.NewDependencyError(
				"cloudreve_owner_registration_requires_explicit_authorization",
				"Cloudreve did not expose initialization state; refusing first-owner registration without explicit caller authorization",
				skerrors.WithField("operation", "POST /user"),
			)
		}
		if createErr := EnsureCloudreveOwnerAccount(ctx, client, baseURL, email, password, language); createErr != nil {
			return CloudreveOwnerResult{}, cloudreveOwnerDependencyError(
				"cloudreve_owner_create_failed",
				"failed to create the first Cloudreve owner account",
				"POST /user",
				createErr,
			)
		}
		loginPayload, parsed, loginErr = CloudreveLogin(ctx, client, baseURL, email, password)
		accessToken = strings.TrimSpace(parsed.Token.AccessToken)
		refreshToken = strings.TrimSpace(parsed.Token.RefreshToken)
		sessionStarted = accessToken != "" || refreshToken != ""
		if loginErr != nil {
			return CloudreveOwnerResult{}, skerrors.NewAuthError(
				"cloudreve_owner_login_failed",
				"Cloudreve did not accept the newly prepared Files owner credentials",
				skerrors.WithField("operation", "POST /session/token"),
			)
		}
	}
	if accessToken == "" {
		return CloudreveOwnerResult{}, skerrors.NewAuthError(
			"cloudreve_owner_session_incomplete",
			"Cloudreve login did not return a usable access token",
			skerrors.WithField("operation", "POST /session/token"),
		)
	}

	var user cloudreveOwnerReadback
	userData, apiErr := CloudreveJSON(ctx, client, http.MethodGet, baseURL, "/user/me", accessToken, nil)
	if apiErr != nil {
		return CloudreveOwnerResult{}, cloudreveOwnerDependencyError(
			"cloudreve_owner_identity_readback_failed",
			"failed to read the logged-in Cloudreve owner",
			"GET /user/me",
			apiErr,
		)
	}
	if err := json.Unmarshal(userData, &user); err != nil {
		return CloudreveOwnerResult{}, skerrors.NewDependencyError(
			"cloudreve_owner_identity_readback_invalid",
			"Cloudreve owner identity readback was invalid",
			skerrors.WithField("operation", "GET /user/me"),
		)
	}
	userID := cloudreveScalarString(user.ID)
	if userID == "" || !strings.EqualFold(strings.TrimSpace(user.Email), email) {
		return CloudreveOwnerResult{}, skerrors.NewAuthError(
			"cloudreve_owner_identity_mismatch",
			"Cloudreve login did not resolve to the requested Files owner",
		)
	}

	// The pinned API exposes the current user group in /user/me and protects
	// this read-only endpoint with IsAdmin. Requiring both facts prevents a
	// merely authenticated account from being reported as the first admin.
	if user.Group == nil || strings.TrimSpace(user.Group.Name) == "" {
		return CloudreveOwnerResult{}, skerrors.NewAuthError(
			"cloudreve_owner_role_readback_missing",
			"Cloudreve owner identity readback omitted the user role",
		)
	}
	if _, apiErr := CloudreveJSON(ctx, client, http.MethodGet, baseURL, "/admin/summary", accessToken, nil); apiErr != nil {
		return CloudreveOwnerResult{}, skerrors.NewAuthError(
			"cloudreve_owner_not_admin",
			"Cloudreve login did not resolve to an administrator",
			skerrors.WithField("operation", "GET /admin/summary"),
		)
	}

	result = CloudreveOwnerResult{
		ServerInitialized: true,
		Version:           version,
		UserID:            userID,
		UserEmail:         strings.TrimSpace(user.Email),
		UserGroup:         strings.TrimSpace(user.Group.Name),
		UserIsAdmin:       true,
		accessToken:       accessToken,
		refreshToken:      refreshToken,
		loginPayload:      append(json.RawMessage(nil), loginPayload...),
	}
	return result, nil
}

// CloudreveLogin performs the pinned password login without exposing token
// fields through any credential-free result type.
func CloudreveLogin(ctx context.Context, client *http.Client, baseURL, email, password string) (json.RawMessage, CloudreveLoginResponse, *CloudreveAPIError) {
	payload := map[string]string{"email": email, "password": password}
	raw, apiErr := CloudreveJSON(ctx, client, http.MethodPost, baseURL, "/session/token", "", payload)
	if apiErr != nil {
		return nil, CloudreveLoginResponse{}, apiErr
	}
	var parsed CloudreveLoginResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return raw, parsed, &CloudreveAPIError{Message: "failed to parse Cloudreve session response", Cause: err}
	}
	return raw, parsed, nil
}

// EnsureCloudreveOwnerAccount performs the official v4 first-user
// registration request. The server assigns administrator privileges to its
// first user; the caller still verifies that role through /admin/summary.
func EnsureCloudreveOwnerAccount(ctx context.Context, client *http.Client, baseURL, email, password, language string) *CloudreveAPIError {
	if strings.TrimSpace(language) == "" {
		language = "en-US"
	}
	_, apiErr := CloudreveJSON(ctx, client, http.MethodPost, baseURL, "/user", "", map[string]string{
		"email":    email,
		"password": password,
		"language": language,
	})
	if apiErr == nil || CloudreveAlreadyExists(apiErr) {
		return nil
	}
	return apiErr
}

// AccessTokenForHandoff exposes only the in-memory access token to a caller
// that owns a separate legacy browser/session handoff.
func (r CloudreveOwnerResult) AccessTokenForHandoff() string {
	return r.accessToken
}

// LoginResponseForHandoff returns a defensive copy of the login payload for
// the legacy browser bridge. Native evidence must use CloudreveOwnerResult,
// which has no token fields.
func (r CloudreveOwnerResult) LoginResponseForHandoff() json.RawMessage {
	return append(json.RawMessage(nil), r.loginPayload...)
}

// Cleanup invalidates the temporary Cloudreve session through the official
// refresh-token logout endpoint and then clears all in-memory token material.
func (r *CloudreveOwnerResult) Cleanup(ctx context.Context, client *http.Client, baseURL string) *skerrors.StackKitError {
	if r == nil {
		return nil
	}
	accessToken := strings.TrimSpace(r.accessToken)
	refreshToken := strings.TrimSpace(r.refreshToken)
	r.accessToken = ""
	r.refreshToken = ""
	r.loginPayload = nil
	if accessToken == "" && refreshToken == "" {
		return nil
	}
	if refreshToken == "" {
		return skerrors.NewDependencyError(
			"cloudreve_session_cleanup_unavailable",
			"Cloudreve did not return a refresh token for temporary session cleanup",
		)
	}
	return LogoutCloudreveSession(ctx, client, baseURL, refreshToken)
}

// LogoutCloudreveSession uses Cloudreve 4.18's DELETE /session/token
// refresh-token endpoint. The endpoint is deliberately called without an
// Authorization header: Cloudreve parses the refresh token from the JSON
// request body and revokes its root session.
func LogoutCloudreveSession(ctx context.Context, client *http.Client, baseURL, refreshToken string) *skerrors.StackKitError {
	refreshToken = strings.TrimSpace(refreshToken)
	if refreshToken == "" {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	cleanupCtx, cancel := context.WithTimeout(ctx, cloudreveOwnerSessionCleanupTimeout)
	defer cancel()
	if _, apiErr := CloudreveJSON(cleanupCtx, client, http.MethodDelete, baseURL, "/session/token", "", map[string]string{
		"refresh_token": refreshToken,
	}); apiErr != nil {
		return cloudreveOwnerDependencyError(
			"cloudreve_session_logout_failed",
			"failed to invalidate the temporary Cloudreve setup session",
			"DELETE /session/token",
			apiErr,
		)
	}
	return nil
}

func cloudreveOwnerDependencyError(code, message, operation string, apiErr *CloudreveAPIError) *skerrors.StackKitError {
	options := []skerrors.ErrorOption{skerrors.WithField("operation", operation)}
	if apiErr != nil {
		if apiErr.HTTPStatus != 0 {
			options = append(options, skerrors.WithField("status", apiErr.HTTPStatus))
		}
		if apiErr.Code != 0 {
			options = append(options, skerrors.WithField("cloudreveCode", apiErr.Code))
		}
	}
	return skerrors.NewDependencyError(code, message, options...)
}

func cloudreveScalarString(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return ""
	}
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return strings.TrimSpace(value)
	}
	if len(raw) > 0 && raw[0] != '{' && raw[0] != '[' {
		return strings.TrimSpace(string(raw))
	}
	return ""
}
