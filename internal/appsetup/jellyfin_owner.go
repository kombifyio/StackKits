package appsetup

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	skerrors "github.com/kombifyio/stackkits/internal/errors"
)

const (
	JellyfinPinnedVersion              = "10.10.7"
	jellyfinOwnerSessionCleanupTimeout = 5 * time.Second
)

// JellyfinOwnerRequest contains only explicit operator credentials and the
// requested startup transition. The password is used in memory for this
// bounded operation and is never copied into the result or evidence.
type JellyfinOwnerRequest struct {
	Username           string
	Password           string
	ExpectedVersion    string
	CompleteOnboarding bool
}

// JellyfinOwnerResult is the credential-free readback of the Jellyfin owner
// setup. The temporary access token remains private for cleanup and is never
// serialized or handed to another client.
type JellyfinOwnerResult struct {
	Version                string `json:"version"`
	UserID                 string `json:"userId"`
	UserName               string `json:"userName"`
	UserIsAdmin            bool   `json:"userIsAdmin"`
	StartupWizardCompleted bool   `json:"startupWizardCompleted"`

	accessToken string
}

// Cleanup invalidates the temporary session created by BootstrapJellyfinOwner.
// The token is cleared before the bounded logout request is made.
func (r *JellyfinOwnerResult) Cleanup(ctx context.Context, client *http.Client, baseURL string) *skerrors.StackKitError {
	if r == nil {
		return nil
	}
	token := strings.TrimSpace(r.accessToken)
	r.accessToken = ""
	return LogoutJellyfinSession(ctx, client, baseURL, token)
}

type jellyfinSystemInfo struct {
	Version                string `json:"Version"`
	StartupWizardCompleted *bool  `json:"StartupWizardCompleted"`
}

type jellyfinStartupUser struct {
	Name     string `json:"Name"`
	Password string `json:"Password"`
}

type jellyfinAuthenticationResult struct {
	AccessToken string            `json:"AccessToken"`
	User        jellyfinLoginUser `json:"User"`
}

type jellyfinLoginUser struct {
	ID   string `json:"Id"`
	Name string `json:"Name"`
}

type jellyfinUserReadback struct {
	ID     string              `json:"Id"`
	Name   string              `json:"Name"`
	Policy *jellyfinUserPolicy `json:"Policy"`
}

type jellyfinUserPolicy struct {
	IsAdministrator bool `json:"IsAdministrator"`
}

// BootstrapJellyfinOwner performs the bounded Jellyfin owner sequence:
// version/startup readback, explicit first-user preparation when startup is
// incomplete, optional startup completion, password login, administrator and
// startup readback, and caller-owned bounded cleanup. It never reads the
// FirstUser endpoint or invents default credentials.
func BootstrapJellyfinOwner(
	ctx context.Context,
	client *http.Client,
	baseURL string,
	request JellyfinOwnerRequest,
) (result JellyfinOwnerResult, returnErr *skerrors.StackKitError) {
	if ctx == nil {
		ctx = context.Background()
	}
	normalizedBaseURL, err := normalizeJellyfinBaseURL(baseURL)
	if err != nil {
		return JellyfinOwnerResult{}, skerrors.NewValidationError(
			"setup_jellyfin_url_invalid",
			"Jellyfin owner bootstrap requires an absolute HTTP(S) URL",
		)
	}
	username := strings.TrimSpace(request.Username)
	password := request.Password
	if username == "" || strings.TrimSpace(password) == "" {
		return JellyfinOwnerResult{}, skerrors.NewValidationError(
			"setup_credentials_missing",
			"Jellyfin owner bootstrap requires explicit owner credentials",
			skerrors.WithSuggestion("Provide the Jellyfin owner username and password through the selected local setup input"),
		)
	}
	expectedVersion := strings.TrimSpace(request.ExpectedVersion)
	if expectedVersion == "" {
		expectedVersion = JellyfinPinnedVersion
	}
	if expectedVersion != JellyfinPinnedVersion {
		return JellyfinOwnerResult{}, skerrors.NewValidationError(
			"setup_jellyfin_version_unsupported",
			"Jellyfin owner bootstrap is bound to the pinned Media workload version",
			skerrors.WithField("expectedVersion", JellyfinPinnedVersion),
		)
	}
	client = cloneJellyfinHTTPClient(client)
	defer client.CloseIdleConnections()

	var token string
	defer func() {
		if returnErr == nil || token == "" {
			return
		}
		cleanupErr := LogoutJellyfinSession(context.Background(), client, normalizedBaseURL, token)
		token = ""
		if cleanupErr != nil {
			if returnErr.Fields == nil {
				returnErr.Fields = make(map[string]interface{})
			}
			returnErr.Fields["sessionCleanup"] = "failed"
		}
	}()

	var initialInfo jellyfinSystemInfo
	if err := jellyfinJSONRequest(ctx, client, normalizedBaseURL, http.MethodGet, "/System/Info/Public", "", nil, &initialInfo); err != nil {
		return JellyfinOwnerResult{}, jellyfinDependencyError(
			"jellyfin_server_info_failed",
			"failed to read the Jellyfin server information",
			"GET /System/Info/Public",
			err,
		)
	}
	if strings.TrimSpace(initialInfo.Version) != expectedVersion {
		return JellyfinOwnerResult{}, skerrors.NewDependencyError(
			"jellyfin_server_version_mismatch",
			"Jellyfin server version does not match the pinned Media workload",
			skerrors.WithField("expectedVersion", expectedVersion),
		)
	}
	if initialInfo.StartupWizardCompleted == nil {
		return JellyfinOwnerResult{}, skerrors.NewDependencyError(
			"jellyfin_startup_state_unavailable",
			"Jellyfin server information omitted startup completion state",
			skerrors.WithField("operation", "GET /System/Info/Public"),
		)
	}
	if !*initialInfo.StartupWizardCompleted {
		if err := jellyfinJSONRequest(ctx, client, normalizedBaseURL, http.MethodPost, "/Startup/User", "", jellyfinStartupUser{Name: username, Password: password}, nil); err != nil {
			return JellyfinOwnerResult{}, jellyfinDependencyError(
				"jellyfin_owner_prepare_failed",
				"failed to prepare the explicit Jellyfin owner credentials",
				"POST /Startup/User",
				err,
			)
		}
		if request.CompleteOnboarding {
			if err := jellyfinJSONRequest(ctx, client, normalizedBaseURL, http.MethodPost, "/Startup/Complete", "", nil, nil); err != nil {
				return JellyfinOwnerResult{}, jellyfinDependencyError(
					"jellyfin_startup_completion_failed",
					"failed to complete the Jellyfin startup wizard",
					"POST /Startup/Complete",
					err,
				)
			}
		}
	}

	var login jellyfinAuthenticationResult
	loginErr := jellyfinJSONRequest(ctx, client, normalizedBaseURL, http.MethodPost, "/Users/AuthenticateByName", "", map[string]string{
		"Username": username,
		"Pw":       password,
	}, &login)
	token = strings.TrimSpace(login.AccessToken)
	if loginErr != nil {
		return JellyfinOwnerResult{}, jellyfinAuthError(
			"jellyfin_login_failed",
			"Jellyfin rejected the supplied owner credentials",
			"POST /Users/AuthenticateByName",
			loginErr,
		)
	}
	if token == "" {
		return JellyfinOwnerResult{}, skerrors.NewAuthError(
			"jellyfin_login_missing_token",
			"Jellyfin login did not return an access token",
			skerrors.WithField("operation", "POST /Users/AuthenticateByName"),
		)
	}
	loginUserID := strings.TrimSpace(login.User.ID)
	loginIdentity, identityErr := uuid.Parse(loginUserID)
	if identityErr != nil || loginIdentity == uuid.Nil {
		return JellyfinOwnerResult{}, skerrors.NewAuthError(
			"jellyfin_login_missing_user",
			"Jellyfin login did not return the authenticated user identity",
			skerrors.WithField("operation", "POST /Users/AuthenticateByName"),
		)
	}

	var user jellyfinUserReadback
	userPath := "/Users/" + loginIdentity.String()
	if err := jellyfinJSONRequest(ctx, client, normalizedBaseURL, http.MethodGet, userPath, token, nil, &user); err != nil {
		return JellyfinOwnerResult{}, jellyfinAuthOrDependencyError(
			"jellyfin_owner_readback_failed",
			"failed to read back the authenticated Jellyfin owner",
			"GET /Users/{id}",
			err,
		)
	}
	readbackIdentity, identityErr := uuid.Parse(strings.TrimSpace(user.ID))
	if identityErr != nil || readbackIdentity != loginIdentity || !strings.EqualFold(strings.TrimSpace(user.Name), username) {
		return JellyfinOwnerResult{}, skerrors.NewAuthError(
			"jellyfin_owner_identity_mismatch",
			"Jellyfin login did not resolve to the requested owner",
		)
	}
	if user.Policy == nil || !user.Policy.IsAdministrator {
		return JellyfinOwnerResult{}, skerrors.NewAuthError(
			"jellyfin_owner_not_admin",
			"Jellyfin login did not resolve to an administrator",
			skerrors.WithField("operation", "GET /Users/{id}"),
		)
	}

	var finalInfo jellyfinSystemInfo
	if err := jellyfinJSONRequest(ctx, client, normalizedBaseURL, http.MethodGet, "/System/Info", token, nil, &finalInfo); err != nil {
		return JellyfinOwnerResult{}, jellyfinDependencyError(
			"jellyfin_startup_readback_failed",
			"failed to read back the Jellyfin startup state",
			"GET /System/Info",
			err,
		)
	}
	if strings.TrimSpace(finalInfo.Version) != expectedVersion {
		return JellyfinOwnerResult{}, skerrors.NewDependencyError(
			"jellyfin_server_version_mismatch",
			"Jellyfin server version changed during owner setup",
			skerrors.WithField("expectedVersion", expectedVersion),
		)
	}
	if finalInfo.StartupWizardCompleted == nil {
		return JellyfinOwnerResult{}, skerrors.NewDependencyError(
			"jellyfin_startup_readback_invalid",
			"Jellyfin startup readback omitted completion state",
			skerrors.WithField("operation", "GET /System/Info"),
		)
	}
	if request.CompleteOnboarding && !*finalInfo.StartupWizardCompleted {
		return JellyfinOwnerResult{}, skerrors.NewDependencyError(
			"jellyfin_startup_completion_unconfirmed",
			"Jellyfin did not confirm startup completion",
			skerrors.WithField("operation", "GET /System/Info"),
		)
	}

	result = JellyfinOwnerResult{
		Version:                strings.TrimSpace(finalInfo.Version),
		UserID:                 readbackIdentity.String(),
		UserName:               strings.TrimSpace(user.Name),
		UserIsAdmin:            user.Policy != nil && user.Policy.IsAdministrator,
		StartupWizardCompleted: *finalInfo.StartupWizardCompleted,
		accessToken:            token,
	}
	token = ""
	return result, nil
}

// LogoutJellyfinSession invalidates one temporary Jellyfin session. The
// pinned endpoint returns 204 with no JSON success envelope; any other status
// is treated as an unconfirmed cleanup.
func LogoutJellyfinSession(ctx context.Context, client *http.Client, baseURL, token string) *skerrors.StackKitError {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil
	}
	normalizedBaseURL, err := normalizeJellyfinBaseURL(baseURL)
	if err != nil {
		return skerrors.NewValidationError("setup_jellyfin_url_invalid", "Jellyfin session cleanup requires an absolute HTTP(S) URL")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	client = cloneJellyfinHTTPClient(client)
	defer client.CloseIdleConnections()
	cleanupCtx, cancel := context.WithTimeout(ctx, jellyfinOwnerSessionCleanupTimeout)
	defer cancel()
	status, err := jellyfinJSONRequestStatus(cleanupCtx, client, normalizedBaseURL, http.MethodPost, "/Sessions/Logout", token, nil, nil)
	if err != nil {
		return jellyfinDependencyError("jellyfin_session_logout_failed", "failed to invalidate the temporary Jellyfin setup session", "POST /Sessions/Logout", err)
	}
	if status != http.StatusNoContent {
		return skerrors.NewDependencyError(
			"jellyfin_session_logout_unconfirmed",
			"Jellyfin did not confirm invalidation of the temporary setup session",
			skerrors.WithField("operation", "POST /Sessions/Logout"),
		)
	}
	return nil
}

func normalizeJellyfinBaseURL(raw string) (string, error) {
	trimmed := strings.TrimRight(strings.TrimSpace(raw), "/")
	if _, err := jellyfinEndpointURL(trimmed, "/System/Info/Public"); err != nil {
		return "", err
	}
	return trimmed, nil
}

func jellyfinDependencyError(code, message, operation string, err error) *skerrors.StackKitError {
	options := []skerrors.ErrorOption{skerrors.WithField("operation", operation)}
	if status := jellyfinHTTPStatus(err); status != 0 {
		options = append(options, skerrors.WithField("httpStatus", status))
	}
	return skerrors.NewDependencyError(code, message, options...)
}

func jellyfinAuthError(code, message, operation string, err error) *skerrors.StackKitError {
	options := []skerrors.ErrorOption{skerrors.WithField("operation", operation)}
	if status := jellyfinHTTPStatus(err); status != 0 {
		options = append(options, skerrors.WithField("httpStatus", status))
	}
	return skerrors.NewAuthError(code, message, options...)
}

func jellyfinAuthOrDependencyError(code, message, operation string, err error) *skerrors.StackKitError {
	if status := jellyfinHTTPStatus(err); status == http.StatusUnauthorized || status == http.StatusForbidden {
		return jellyfinAuthError(code, message, operation, err)
	}
	return jellyfinDependencyError(code, message, operation, err)
}
