package appsetup

import (
	"context"
	"net/http"
	"strings"
	"time"

	skerrors "github.com/kombifyio/stackkits/internal/errors"
)

const immichOwnerSessionCleanupTimeout = 5 * time.Second

// ImmichOwnerRequest contains the technical setup choices supplied by
// the caller. The password is used only for the bounded bootstrap requests and
// is never copied into setup evidence.
type ImmichOwnerRequest struct {
	Email              string
	Password           string
	DisplayName        string
	CompleteOnboarding bool
}

type ImmichOwnerResult struct {
	// accessToken is kept in memory for the legacy handoff that follows the
	// technical bootstrap. It is deliberately unexported and is never evidence.
	accessToken        string
	ServerInitialized  bool   `json:"serverInitialized"`
	UserID             string `json:"userId"`
	UserEmail          string `json:"userEmail"`
	UserName           string `json:"userName"`
	UserIsAdmin        bool   `json:"userIsAdmin"`
	OnboardingComplete bool   `json:"onboardingComplete"`
}

type immichOwnerReadback struct {
	ID      string `json:"id"`
	Email   string `json:"email"`
	Name    string `json:"name"`
	IsAdmin bool   `json:"isAdmin"`
}

// BootstrapImmichOwner performs the shared, app-local Immich
// owner bootstrap. It owns only technical Immich setup and readback; callers
// remain responsible for any separate identity or demo-data handoff.
func BootstrapImmichOwner(
	ctx context.Context,
	client *http.Client,
	baseURL string,
	request ImmichOwnerRequest,
) (result ImmichOwnerResult, returnErr *skerrors.StackKitError) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	email := strings.TrimSpace(request.Email)
	password := request.Password
	displayName := strings.TrimSpace(request.DisplayName)
	if email == "" || strings.TrimSpace(password) == "" {
		return ImmichOwnerResult{}, skerrors.NewValidationError(
			"setup_credentials_missing",
			"Immich owner bootstrap requires StackKit admin credentials",
			skerrors.WithSuggestion("Provide the Immich owner email and password through the selected setup configuration"),
		)
	}
	if displayName == "" {
		return ImmichOwnerResult{}, skerrors.NewValidationError(
			"setup_display_name_missing",
			"Immich owner bootstrap requires an owner display name",
		)
	}
	if baseURL == "" {
		return ImmichOwnerResult{}, skerrors.NewValidationError(
			"setup_immich_url_missing",
			"Immich owner bootstrap requires an Immich URL",
		)
	}
	if client == nil {
		client = NewImmichHTTPClient()
		defer client.CloseIdleConnections()
	}
	var token string
	var sessionToken string
	defer func() {
		if returnErr == nil || sessionToken == "" {
			return
		}
		cleanupErr := LogoutImmichSession(context.Background(), client, baseURL, sessionToken)
		sessionToken = ""
		if cleanupErr != nil {
			if returnErr.Fields == nil {
				returnErr.Fields = make(map[string]interface{})
			}
			returnErr.Fields["sessionCleanup"] = "failed"
		}
		token = ""
	}()

	var initialConfig struct {
		IsInitialized bool `json:"isInitialized"`
	}
	if err := ImmichRequest(ctx, client, baseURL, http.MethodGet, "/api/server/config", nil, "", &initialConfig); err != nil {
		return ImmichOwnerResult{}, skerrors.NewDependencyError(
			"immich_config_failed",
			"failed to read Immich server config",
			skerrors.WithField("operation", "GET /api/server/config"),
		)
	}
	if !initialConfig.IsInitialized {
		if err := ImmichRequest(ctx, client, baseURL, http.MethodPost, "/api/auth/admin-sign-up", map[string]string{
			"email":    email,
			"password": password,
			"name":     displayName,
		}, "", nil); err != nil {
			return ImmichOwnerResult{}, skerrors.NewDependencyError(
				"immich_admin_signup_failed",
				"failed to create Immich owner",
				skerrors.WithField("operation", "POST /api/auth/admin-sign-up"),
			)
		}
	}

	var login struct {
		AccessToken string `json:"accessToken"`
	}
	loginErr := ImmichRequest(ctx, client, baseURL, http.MethodPost, "/api/auth/login", map[string]string{
		"email":    email,
		"password": password,
	}, "", &login)
	token = strings.TrimSpace(login.AccessToken)
	login.AccessToken = ""
	if token != "" {
		sessionToken = token
	}
	if loginErr != nil {
		return ImmichOwnerResult{}, skerrors.NewAuthError(
			"immich_login_failed",
			"failed to log in to Immich with StackKit admin credentials",
			skerrors.WithField("operation", "POST /api/auth/login"),
		)
	}
	if token == "" {
		return ImmichOwnerResult{}, skerrors.NewAuthError(
			"immich_login_missing_token",
			"Immich login did not return an access token",
		)
	}

	var loggedInUser immichOwnerReadback
	if err := ImmichRequest(ctx, client, baseURL, http.MethodGet, "/api/users/me", nil, token, &loggedInUser); err != nil {
		return ImmichOwnerResult{}, skerrors.NewDependencyError(
			"immich_owner_identity_readback_failed",
			"failed to verify the Immich administrator identity before profile setup",
			skerrors.WithField("operation", "GET /api/users/me"),
		)
	}
	if strings.TrimSpace(loggedInUser.ID) == "" || !strings.EqualFold(strings.TrimSpace(loggedInUser.Email), email) || !loggedInUser.IsAdmin {
		return ImmichOwnerResult{}, skerrors.NewDependencyError(
			"immich_owner_not_admin",
			"Immich login did not resolve to the requested administrator",
		)
	}

	if err := ImmichRequest(ctx, client, baseURL, http.MethodPut, "/api/users/me", map[string]string{
		"name":     displayName,
		"password": password,
	}, token, nil); err != nil {
		return ImmichOwnerResult{}, skerrors.NewDependencyError(
			"immich_profile_update_failed",
			"failed to update Immich owner profile",
			skerrors.WithField("operation", "PUT /api/users/me"),
		)
	}
	// Omitting completion preserves the application's current onboarding
	// flags. A retry must never reopen onboarding that the owner finished.
	if request.CompleteOnboarding {
		if err := ImmichRequest(ctx, client, baseURL, http.MethodPut, "/api/users/me/onboarding", map[string]bool{
			"isOnboarded": request.CompleteOnboarding,
		}, token, nil); err != nil {
			return ImmichOwnerResult{}, skerrors.NewDependencyError(
				"immich_user_onboarding_failed",
				"failed to configure Immich user onboarding",
				skerrors.WithField("operation", "PUT /api/users/me/onboarding"),
			)
		}
		if err := ImmichRequest(ctx, client, baseURL, http.MethodPost, "/api/system-metadata/admin-onboarding", map[string]bool{
			"isOnboarded": request.CompleteOnboarding,
		}, token, nil); err != nil {
			return ImmichOwnerResult{}, skerrors.NewDependencyError(
				"immich_admin_onboarding_failed",
				"failed to configure Immich admin onboarding",
				skerrors.WithField("operation", "POST /api/system-metadata/admin-onboarding"),
			)
		}
	}

	var user immichOwnerReadback
	if err := ImmichRequest(ctx, client, baseURL, http.MethodGet, "/api/users/me", nil, token, &user); err != nil {
		return ImmichOwnerResult{}, skerrors.NewDependencyError(
			"immich_owner_readback_failed",
			"failed to verify the Immich owner readback",
			skerrors.WithField("operation", "GET /api/users/me"),
		)
	}
	if strings.TrimSpace(user.ID) == "" || !strings.EqualFold(strings.TrimSpace(user.Email), email) || strings.TrimSpace(user.Name) != displayName || !user.IsAdmin {
		return ImmichOwnerResult{}, skerrors.NewDependencyError(
			"immich_owner_readback_mismatch",
			"Immich owner readback did not match the requested administrator",
		)
	}

	var finalConfig struct {
		IsInitialized bool `json:"isInitialized"`
	}
	if err := ImmichRequest(ctx, client, baseURL, http.MethodGet, "/api/server/config", nil, token, &finalConfig); err != nil {
		return ImmichOwnerResult{}, skerrors.NewDependencyError(
			"immich_config_readback_failed",
			"failed to verify Immich server initialization",
			skerrors.WithField("operation", "GET /api/server/config"),
		)
	}
	if !finalConfig.IsInitialized {
		return ImmichOwnerResult{}, skerrors.NewDependencyError(
			"immich_server_not_initialized",
			"Immich server did not report initialized after owner bootstrap",
		)
	}
	// The v2.7 user object has no onboarding field. Verify the two actual
	// metadata endpoints instead of inferring completion from a successful PUT.
	onboardingComplete := true
	for _, endpoint := range []string{"/api/users/me/onboarding", "/api/system-metadata/admin-onboarding"} {
		var observed struct {
			IsOnboarded *bool `json:"isOnboarded"`
		}
		if err := ImmichRequest(ctx, client, baseURL, http.MethodGet, endpoint, nil, token, &observed); err != nil || observed.IsOnboarded == nil || (request.CompleteOnboarding && !*observed.IsOnboarded) {
			return ImmichOwnerResult{}, skerrors.NewDependencyError("immich_onboarding_readback_mismatch", "Immich onboarding readback did not match the requested state", skerrors.WithField("operation", "GET "+endpoint))
		}
		onboardingComplete = onboardingComplete && *observed.IsOnboarded
	}

	result = ImmichOwnerResult{
		accessToken:        token,
		ServerInitialized:  finalConfig.IsInitialized,
		UserID:             strings.TrimSpace(user.ID),
		UserEmail:          strings.TrimSpace(user.Email),
		UserName:           strings.TrimSpace(user.Name),
		UserIsAdmin:        user.IsAdmin,
		OnboardingComplete: onboardingComplete,
	}
	sessionToken = ""
	token = ""
	return result, nil
}

// AccessTokenForHandoff exposes the in-memory session only to a follow-up
// identity handoff. JSON serialization never includes it.
func (r ImmichOwnerResult) AccessTokenForHandoff() string { return r.accessToken }

// Cleanup invalidates the temporary session created by BootstrapImmichOwner.
// The operation is idempotent and clears the in-memory token before making the
// bounded logout request.
func (r *ImmichOwnerResult) Cleanup(ctx context.Context, client *http.Client, baseURL string) *skerrors.StackKitError {
	if r == nil {
		return nil
	}
	token := strings.TrimSpace(r.accessToken)
	r.accessToken = ""
	return LogoutImmichSession(ctx, client, baseURL, token)
}

// LogoutImmichSession invalidates one in-memory Immich session through the
// pinned v2 API. Callers that own a handoff must pass context.Background() (or
// another live context) so cleanup still runs after the setup operation fails.
func LogoutImmichSession(ctx context.Context, client *http.Client, baseURL, token string) *skerrors.StackKitError {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil
	}
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return skerrors.NewValidationError("setup_immich_url_missing", "Immich session cleanup requires an Immich URL")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if client == nil {
		client = NewImmichHTTPClient()
		defer client.CloseIdleConnections()
	}
	cleanupCtx, cancel := context.WithTimeout(ctx, immichOwnerSessionCleanupTimeout)
	defer cancel()
	var response struct {
		Successful bool `json:"successful"`
	}
	if err := ImmichRequest(cleanupCtx, client, baseURL, http.MethodPost, "/api/auth/logout", nil, token, &response); err != nil {
		return skerrors.NewDependencyError(
			"immich_session_logout_failed",
			"failed to invalidate the temporary Immich setup session",
			skerrors.WithField("operation", "POST /api/auth/logout"),
			skerrors.WithCause(err),
		)
	}
	if !response.Successful {
		return skerrors.NewDependencyError(
			"immich_session_logout_unconfirmed",
			"Immich did not confirm invalidation of the temporary setup session",
			skerrors.WithField("operation", "POST /api/auth/logout"),
		)
	}
	return nil
}
