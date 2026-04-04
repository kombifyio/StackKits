//go:build production

package production

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

type simCloudAuthConfig struct {
	BaseURL     string
	Issuer      string
	ClientID    string
	RedirectURL string
}

func TestSimCloudSSORedirectConfigured(t *testing.T) {
	cfg := loadSimCloudAuthConfig(t)

	httpClient := &http.Client{Timeout: 30 * time.Second}

	loginResp, err := httpClient.Get(strings.TrimRight(cfg.BaseURL, "/") + "/login") //nolint:noctx
	if err != nil {
		t.Fatalf("load simulate login page: %v", err)
	}
	defer loginResp.Body.Close()

	if loginResp.StatusCode != http.StatusOK {
		t.Fatalf("simulate login page returned %d", loginResp.StatusCode)
	}

	authorizeURL := buildAuthorizeURL(cfg)
	noRedirect := &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	resp, err := noRedirect.Get(authorizeURL) //nolint:noctx
	if err != nil {
		t.Fatalf("request authorize URL: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read authorize response: %v", err)
	}
	bodyText := string(body)

	if strings.Contains(bodyText, "redirect_uri is missing in the client configuration") {
		t.Fatalf("simulate cloud SSO is misconfigured: redirect_uri rejected by Zitadel")
	}
	if strings.Contains(bodyText, `"error":"invalid_request"`) {
		t.Fatalf("simulate cloud SSO returned invalid_request: %s", bodyText)
	}
	if resp.StatusCode >= 400 {
		t.Fatalf("simulate cloud SSO authorize endpoint returned %d: %s", resp.StatusCode, bodyText)
	}
}

func loadSimCloudAuthConfig(t *testing.T) simCloudAuthConfig {
	t.Helper()

	baseURL := firstEnv("KOMBIFY_SIM_BASE_URL")
	if baseURL == "" {
		baseURL = "https://simulate.kombify.space"
	}

	issuer := firstEnv("KOMBIFY_ZITADEL_ISSUER", "ZITADEL_ISSUER")
	clientID := firstEnv("KOMBIFY_SIM_CLIENT_ID", "KOMBISIM_AUTH_CLOUD_CLIENT_ID")
	redirectURL := firstEnv("KOMBIFY_SIM_REDIRECT_URL", "KOMBISIM_AUTH_CLOUD_REDIRECT_URL")
	if issuer == "" || clientID == "" || redirectURL == "" {
		t.Skip("simulate cloud auth config not set")
	}

	return simCloudAuthConfig{
		BaseURL:     baseURL,
		Issuer:      issuer,
		ClientID:    clientID,
		RedirectURL: redirectURL,
	}
}

func buildAuthorizeURL(cfg simCloudAuthConfig) string {
	values := url.Values{}
	values.Set("client_id", cfg.ClientID)
	values.Set("redirect_uri", cfg.RedirectURL)
	values.Set("response_type", "code")
	values.Set("scope", "openid profile email")
	values.Set("state", "stackkits-ci")
	return fmt.Sprintf("%s/oauth/v2/authorize?%s", strings.TrimRight(cfg.Issuer, "/"), values.Encode())
}
