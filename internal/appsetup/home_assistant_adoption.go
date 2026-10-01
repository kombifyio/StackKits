package appsetup

import (
	"context"
	"errors"
	"net/http"
	"strings"
)

// VerifyHomeAssistantExistingOwner reads an existing grant. It deliberately
// never invokes bootstrap, login-flow, token issuance or onboarding endpoints.
func VerifyHomeAssistantExistingOwner(ctx context.Context, client *http.Client, baseURL, token, version string) (HomeAssistantOwnerResult, error) {
	if strings.TrimSpace(token) == "" {
		return HomeAssistantOwnerResult{}, errors.New("missing_native_grant: existing Home Assistant owner access is required")
	}
	baseURL, err := normalizeHomeAssistantBaseURL(baseURL)
	if err != nil {
		return HomeAssistantOwnerResult{}, err
	}
	if client == nil {
		client = NewImmichHTTPClient()
		defer client.CloseIdleConnections()
	}
	client = cloneHomeAssistantHTTPClient(client)
	user, observed, err := readHomeAssistantCurrentUser(ctx, client, baseURL, token, version)
	if err != nil {
		return HomeAssistantOwnerResult{}, err
	}
	if user.IsOwner == nil || !*user.IsOwner || user.IsAdmin == nil || !*user.IsAdmin || user.ID == "" {
		return HomeAssistantOwnerResult{}, errors.New("missing_native_grant: existing account is not the Home Assistant owner")
	}
	var config homeAssistantConfig
	if err := homeAssistantJSONRequest(ctx, client, baseURL, http.MethodGet, "/api/config", nil, token, &config); err != nil {
		return HomeAssistantOwnerResult{}, err
	}
	if config.Version != version || observed != version || !strings.EqualFold(config.State, "RUNNING") {
		return HomeAssistantOwnerResult{}, errors.New("source_identity_conflict: authenticated Home Assistant version/state differs from the admitted module")
	}
	return HomeAssistantOwnerResult{UserID: user.ID, UserIsOwner: true, UserIsAdmin: true, ServerInitialized: true, Version: version}, nil
}
