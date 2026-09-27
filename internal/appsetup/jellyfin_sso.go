package appsetup

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"reflect"
	"strings"

	"github.com/google/uuid"
	"github.com/kombifyio/stackkits/internal/jellyfinsso"
)

// JellyfinSSORequest is populated from verified local identity custody. Its
// secret is sent only to the admitted Jellyfin setup endpoint, never evidence.
type JellyfinSSORequest struct {
	Issuer       string
	ClientID     string
	ClientSecret string
	OwnerSubject string
}

// EnsureSSO configures the packaged plugin during explicit owner setup. It
// preserves account links and app-local passwords. Verify/backup paths never
// call it. The plugin has no conditional-update API: run owner setup with
// Media sign-in quiesced when changing an established provider configuration.
func (r *JellyfinOwnerResult) EnsureSSO(ctx context.Context, client *http.Client, baseURL string, request JellyfinSSORequest) error {
	if r == nil {
		return errors.New("Jellyfin SSO owner session is missing")
	}
	owner, err := uuid.Parse(r.UserID)
	issuer, issuerErr := url.Parse(request.Issuer)
	if !r.UserIsAdmin || !r.StartupWizardCompleted || r.accessToken == "" || err != nil || owner == uuid.Nil || issuerErr != nil || issuer.Scheme != "https" || issuer.Host == "" || issuer.User != nil || issuer.RawQuery != "" || issuer.Fragment != "" || request.ClientID != "stackkit-media" || request.ClientSecret == "" || strings.TrimSpace(request.OwnerSubject) == "" {
		return errors.New("Jellyfin SSO requires a verified owner session and exact home identity custody")
	}
	client = cloneJellyfinHTTPClient(client)
	defer client.CloseIdleConnections()
	var plugins []struct {
		ID      string `json:"Id"`
		Version string `json:"Version"`
		Status  string `json:"Status"`
	}
	if err := jellyfinJSONRequest(ctx, client, baseURL, http.MethodGet, "/Plugins", r.accessToken, nil, &plugins); err != nil {
		return errors.New("read the installed Jellyfin SSO plugin failed")
	}
	admitted := false
	for _, plugin := range plugins {
		id, _ := uuid.Parse(plugin.ID)
		if id.String() != jellyfinsso.GUID || plugin.Status != "Active" {
			continue
		}
		if admitted || (plugin.Version != jellyfinsso.Version && plugin.Version != jellyfinsso.Version+".0") {
			return errors.New("Jellyfin has conflicting active SSO plugin versions")
		}
		admitted = true
	}
	if !admitted {
		return errors.New("the exact governed Jellyfin SSO plugin is not active")
	}
	read := func() (map[string]json.RawMessage, error) {
		var providers map[string]map[string]json.RawMessage
		if err := jellyfinJSONRequest(ctx, client, baseURL, http.MethodGet, "/sso/OID/Get", r.accessToken, nil, &providers); err != nil {
			return nil, errors.New("read Jellyfin SSO configuration failed")
		}
		config := providers["pocketid"]
		if config == nil {
			config = map[string]json.RawMessage{}
		}
		return config, nil
	}
	current, err := read()
	if err != nil {
		return err
	}
	links := map[string]string{}
	if raw := jellyfinSSOField(current, "CanonicalLinks"); len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &links); err != nil || links == nil {
			return errors.New("Jellyfin SSO account links are invalid")
		}
	}
	for subject, linked := range links {
		id, err := uuid.Parse(linked)
		if err != nil || id == uuid.Nil || (subject == request.OwnerSubject && id != owner) || (subject != request.OwnerSubject && id == owner) {
			return errors.New("Jellyfin SSO account links conflict with the verified owner")
		}
	}
	links[request.OwnerSubject] = owner.String()
	governed := map[string]any{
		"OidEndpoint": request.Issuer, "OidClientId": request.ClientID, "OidSecret": request.ClientSecret,
		"Enabled": true, "EnableAuthorization": true, "EnableAllFolders": true,
		"AdminRoles": []string{"owners", "admins"}, "PreserveAdminPermissions": false,
		"Roles": []string{"owners", "admins", "household"}, "RoleClaim": "groups",
		"OidScopes": []string{"openid", "profile", "email", "groups"}, "OverrideDefaultScopes": true,
		"OidAuthorizationParameters": "", "DefaultUsernameClaim": "sub", "CanonicalLinks": links,
		"DefaultProvider": "", "SchemeOverride": "https", "PortOverride": nil, "NewPath": true,
		"DisableHttps": false, "DoNotValidateEndpoints": false, "DoNotValidateIssuerName": false,
		"DoNotLoadProfile": false, "DisablePushedAuthorization": false,
		"EnableFolderRoles": false, "EnableLiveTvRoles": false, "EnableLiveTv": false, "EnableLiveTvManagement": false,
		"AvatarUrlFormat": "",
	}
	unchanged := true
	for name, value := range governed {
		encoded, _ := json.Marshal(value)
		if !jellyfinSSOGovernedEqual(name, jellyfinSSOField(current, name), encoded) {
			unchanged = false
		}
		for key := range current {
			if strings.EqualFold(key, name) {
				delete(current, key)
			}
		}
		current[name] = encoded
	}
	if !unchanged {
		if err := jellyfinJSONRequest(ctx, client, baseURL, http.MethodPost, "/sso/OID/Add/pocketid", r.accessToken, current, nil); err != nil {
			return errors.New("configure Jellyfin SSO failed")
		}
	}
	observed, err := read()
	if err != nil {
		return err
	}
	for name, value := range governed {
		expected, _ := json.Marshal(value)
		if !jellyfinSSOGovernedEqual(name, jellyfinSSOField(observed, name), expected) {
			return errors.New("Jellyfin did not confirm the governed SSO account and role policy")
		}
	}
	return nil
}

func jellyfinSSOGovernedEqual(name string, observed, expected json.RawMessage) bool {
	// Jellyfin's System.Text.Json boundary may serialize Guid values in a
	// different textual form from the hyphenated form submitted by StackKits.
	// Subjects remain exact keys; only valid, equal UUID values are normalized.
	if strings.EqualFold(name, "CanonicalLinks") {
		return jellyfinSSOCanonicalLinksEqual(observed, expected)
	}
	// The pinned Jellyfin plugin omits its nullable port override when it is
	// unset. Absence and JSON null express the same default-port policy. A
	// concrete port still differs and is cleared by the governed write.
	if strings.EqualFold(name, "PortOverride") && len(observed) == 0 && string(expected) == "null" {
		return true
	}
	return jellyfinSSOEqual(observed, expected)
}

func jellyfinSSOCanonicalLinksEqual(observed, expected json.RawMessage) bool {
	var observedLinks, expectedLinks map[string]string
	if json.Unmarshal(observed, &observedLinks) != nil || json.Unmarshal(expected, &expectedLinks) != nil || len(observedLinks) != len(expectedLinks) {
		return false
	}
	for subject, expectedLink := range expectedLinks {
		observedLink, ok := observedLinks[subject]
		observedID, observedErr := uuid.Parse(observedLink)
		expectedID, expectedErr := uuid.Parse(expectedLink)
		if !ok || observedErr != nil || expectedErr != nil || observedID == uuid.Nil || observedID != expectedID {
			return false
		}
	}
	return true
}

func jellyfinSSOField(config map[string]json.RawMessage, name string) json.RawMessage {
	for key, value := range config {
		if strings.EqualFold(key, name) {
			return value
		}
	}
	return nil
}

func jellyfinSSOEqual(a, b json.RawMessage) bool {
	var left, right any
	return json.Unmarshal(a, &left) == nil && json.Unmarshal(b, &right) == nil && reflect.DeepEqual(left, right)
}
