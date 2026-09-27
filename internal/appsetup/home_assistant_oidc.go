package appsetup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
)

// HomeAssistantOIDCBinding contains the immutable subject from signed custody.
// Only the authenticated HA owner may attach it to that same existing account.
type HomeAssistantOIDCBinding struct {
	Issuer  string
	Subject string
}

func bindHomeAssistantOIDCOwner(ctx context.Context, client *http.Client, baseURL, token, userID string, binding HomeAssistantOIDCBinding) error {
	if binding.Issuer == "" || binding.Subject == "" || userID == "" {
		return errors.New("Home Assistant OIDC owner binding is incomplete")
	}
	payload := map[string]string{"issuer": binding.Issuer, "subject": binding.Subject, "userId": userID}
	if err := homeAssistantJSONRequest(ctx, client, baseURL, http.MethodPost, "/api/stackkit/auth_oidc/owner", payload, token, nil); err != nil {
		var statusErr *homeAssistantHTTPError
		if errors.As(err, &statusErr) && statusErr.status == http.StatusNotFound {
			return errors.New("Home Assistant's packaged auth_oidc component is unavailable; preserve the existing configuration, enable auth_oidc: !include stackkit-oidc.yaml in configuration.yaml, restart Home Assistant and retry setup; if already enabled, inspect its startup dependency/configuration error")
		}
		return errors.New("Home Assistant rejected the custodied OIDC owner binding")
	}
	var readback struct {
		UserID      string `json:"userId"`
		SubjectHash string `json:"subjectHash"`
		Linked      bool   `json:"linked"`
	}
	if err := homeAssistantJSONRequest(ctx, client, baseURL, http.MethodGet, "/api/stackkit/auth_oidc/owner", nil, token, &readback); err != nil {
		return errors.New("Home Assistant OIDC owner binding readback failed")
	}
	sum := sha256.Sum256([]byte(binding.Issuer + "." + binding.Subject))
	if !readback.Linked || readback.UserID != userID || readback.SubjectHash != hex.EncodeToString(sum[:]) {
		return errors.New("Home Assistant OIDC owner identity differs from custody")
	}
	return nil
}
