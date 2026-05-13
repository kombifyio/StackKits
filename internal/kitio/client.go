package kitio

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/auth"
)

// AdminClient talks to the Admin API kit-import / kit-export endpoints.
//
// Two auth modes — picked at NewAdminClient time:
//
//  1. ServiceAuthSecret set (preferred): mints an HS256 JWT per request,
//     attaches it via X-Kombify-Service-Auth. Compatible with the admin's
//     requireServiceKeyOrAdmin path. Token format mirrors
//     kombify-go-common/servicecall.IssueToken — kept inline here to avoid
//     a cross-repo go-mod dependency for one helper.
//
//  2. LegacyToken set: attaches Authorization: Bearer <token>. Only works
//     when the admin has ALLOW_LEGACY_SERVICE_KEYS=true (dev/migration).
//
// Both empty: unauthenticated request. Useful for self-test instances.
type AdminClient struct {
	BaseURL           string
	ServiceAuthSecret string // for HS256 service-auth (preferred)
	LegacyToken       string // for Authorization: Bearer (legacy)
	ServiceName       string // svc claim, default "stackkits"
	TargetServiceName string // aud will be "kombify-" + this; default "administration"
	HTTP              *http.Client
}

// NewAdminClient returns a client pointing at endpoint. Decides auth mode:
//   - if serviceAuthSecret != "" → HS256 service-auth mode
//   - else if legacyToken != "" → legacy Bearer mode
//   - else → unauthenticated
//
// Defaults: ServiceName="stackkits", TargetServiceName="administration".
func NewAdminClient(endpoint, legacyToken string) *AdminClient {
	return &AdminClient{
		BaseURL:           strings.TrimRight(endpoint, "/"),
		LegacyToken:       legacyToken,
		ServiceName:       "stackkits",
		TargetServiceName: "administration",
		HTTP:              &http.Client{Timeout: 60 * time.Second},
	}
}

// WithServiceAuth enables HS256 service-auth using the supplied secret.
// Takes precedence over LegacyToken.
func (c *AdminClient) WithServiceAuth(secret string) *AdminClient {
	c.ServiceAuthSecret = secret
	return c
}

// FetchKitDefinition GETs /api/v1/sk/registry/stackkits/{slug}/kit-export.
func (c *AdminClient) FetchKitDefinition(slug string) (KitDefinition, error) {
	url := fmt.Sprintf("%s/api/v1/sk/registry/stackkits/%s/kit-export", c.BaseURL, slug)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return KitDefinition{}, err
	}
	req.Header.Set("Accept", "application/json")
	if err := c.attachAuth(req); err != nil {
		return KitDefinition{}, fmt.Errorf("attach auth: %w", err)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return KitDefinition{}, fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return KitDefinition{}, fmt.Errorf("GET %s: status=%d body=%s", url, resp.StatusCode, snippet(body))
	}

	var def KitDefinition
	if err := json.Unmarshal(body, &def); err != nil {
		return KitDefinition{}, fmt.Errorf("decode response: %w (body=%s)", err, snippet(body))
	}
	return def, nil
}

// PostKitImport POSTs a KitDefinition to the kit-import endpoint.
func (c *AdminClient) PostKitImport(slug string, def KitDefinition, dryRun bool) (KitImportResult, error) {
	def.DryRun = dryRun
	body, err := json.Marshal(def)
	if err != nil {
		return KitImportResult{}, fmt.Errorf("marshal kit def: %w", err)
	}

	url := fmt.Sprintf("%s/api/v1/sk/registry/stackkits/%s/kit-import", c.BaseURL, slug)
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(string(body)))
	if err != nil {
		return KitImportResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if err := c.attachAuth(req); err != nil {
		return KitImportResult{}, fmt.Errorf("attach auth: %w", err)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return KitImportResult{}, fmt.Errorf("POST %s: %w", url, err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return KitImportResult{}, fmt.Errorf("POST %s: status=%d body=%s", url, resp.StatusCode, snippet(respBody))
	}
	var result KitImportResult
	_ = json.Unmarshal(respBody, &result)
	return result, nil
}

// KitImportResult is the structured response from POST kit-import.
type KitImportResult struct {
	Status         string `json:"status"`
	Slug           string `json:"slug"`
	ContractHash   string `json:"contractHash"`
	ID             string `json:"id,omitempty"`
	IsLocked       bool   `json:"isLocked,omitempty"`
	SourceOfTruth  string `json:"sourceOfTruth,omitempty"`
	LastImportedAt string `json:"lastImportedAt,omitempty"`
	LastImportedBy string `json:"lastImportedBy,omitempty"`
	Message        string `json:"message,omitempty"`
}

// attachAuth picks the right header based on the client's auth posture.
// Delegates the HS256 signing to internal/auth so the algorithm lives in
// one place and is testable in isolation.
func (c *AdminClient) attachAuth(req *http.Request) error {
	if c.ServiceAuthSecret != "" {
		token, err := auth.SignServiceToken(c.ServiceName, c.TargetServiceName, c.ServiceAuthSecret, auth.DefaultTokenTTL)
		if err != nil {
			return err
		}
		req.Header.Set(auth.HeaderServiceAuth, token)
		return nil
	}
	if c.LegacyToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.LegacyToken)
	}
	return nil
}

func snippet(b []byte) string {
	if len(b) <= 200 {
		return string(b)
	}
	return string(b[:200]) + "..."
}
