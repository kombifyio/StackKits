package appsetup

import (
	"context"
	"net/http"
	"strings"

	skerrors "github.com/kombifyio/stackkits/internal/errors"
)

// ImmichAPIKeyResult carries the issued key only in memory; callers custody
// it and never copy it into setup evidence.
type ImmichAPIKeyResult struct {
	Secret      string `json:"-"`
	KeyID       string `json:"keyId"`
	OwnerUserID string `json:"ownerUserId"`
	Replaced    int    `json:"replaced"`
}

// IssueImmichAddOnAPIKey signs in as the Immich owner, deletes an earlier key
// with the same name (one key per add-on, so a rotation never leaves an older
// StackKits key active), issues a new one and signs out again.
func IssueImmichAddOnAPIKey(ctx context.Context, client *http.Client, baseURL, email, password, keyName string) (result ImmichAPIKeyResult, returnErr *skerrors.StackKitError) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" || strings.TrimSpace(email) == "" || strings.TrimSpace(password) == "" || strings.TrimSpace(keyName) == "" {
		return ImmichAPIKeyResult{}, skerrors.NewValidationError(
			"setup_credentials_missing",
			"Immich API key issuance requires the Immich URL and the owner credentials",
		)
	}
	if client == nil {
		client = NewImmichHTTPClient()
		defer client.CloseIdleConnections()
	}
	var login struct {
		AccessToken string `json:"accessToken"`
		UserID      string `json:"userId"`
		IsAdmin     bool   `json:"isAdmin"`
	}
	if err := ImmichRequest(ctx, client, baseURL, http.MethodPost, "/api/auth/login", map[string]string{
		"email": email, "password": password,
	}, "", &login); err != nil {
		return ImmichAPIKeyResult{}, skerrors.NewValidationError("immich_owner_login_failed", "the Immich owner could not sign in to issue the add-on API key")
	}
	token := strings.TrimSpace(login.AccessToken)
	if token == "" || !login.IsAdmin {
		return ImmichAPIKeyResult{}, skerrors.NewValidationError("immich_owner_not_admin", "the add-on API key must be issued by the Immich administrator")
	}
	defer func() {
		if cleanupErr := LogoutImmichSession(context.Background(), client, baseURL, token); cleanupErr != nil && returnErr == nil {
			result, returnErr = ImmichAPIKeyResult{}, cleanupErr
		}
	}()
	var existing []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := ImmichRequest(ctx, client, baseURL, http.MethodGet, "/api/api-keys", nil, token, &existing); err != nil {
		return ImmichAPIKeyResult{}, skerrors.NewValidationError("immich_api_keys_unreadable", "Immich did not list the owner's API keys")
	}
	for _, key := range existing {
		if key.Name != keyName {
			continue
		}
		if err := ImmichRequest(ctx, client, baseURL, http.MethodDelete, "/api/api-keys/"+key.ID, nil, token, nil); err != nil {
			return ImmichAPIKeyResult{}, skerrors.NewValidationError("immich_api_key_rotation_failed", "Immich did not delete the previous add-on API key")
		}
		result.Replaced++
	}
	var created struct {
		Secret string `json:"secret"`
		APIKey struct {
			ID string `json:"id"`
		} `json:"apiKey"`
	}
	if err := ImmichRequest(ctx, client, baseURL, http.MethodPost, "/api/api-keys", map[string]any{
		"name": keyName, "permissions": []string{"all"},
	}, token, &created); err != nil || strings.TrimSpace(created.Secret) == "" || created.APIKey.ID == "" {
		return ImmichAPIKeyResult{}, skerrors.NewValidationError("immich_api_key_issue_failed", "Immich did not issue the add-on API key")
	}
	result.Secret, result.KeyID, result.OwnerUserID = created.Secret, created.APIKey.ID, login.UserID
	return result, nil
}
