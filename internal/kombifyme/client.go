// Package kombifyme provides an HTTP client for the kombify.me subdomain registration API.
package kombifyme

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	defaultBaseURL = "https://kombify.me/_kombify/api/v1"
	apiKeyHeader   = "X-Kombify-API-Key"
)

// Client is an HTTP client for the kombify.me subdomain API.
type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

// NewClient creates a new kombify.me API client.
func NewClient(apiKey string) *Client {
	return &Client{
		baseURL: defaultBaseURL,
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

// RegisterResponse holds the response from the registration endpoint.
type RegisterResponse struct {
	UserID string `json:"user_id"`
	APIKey string `json:"api_key"`
	Status string `json:"status"`
}

// Register creates a new self-hosted user account via email verification.
// This is an unauthenticated endpoint — no API key required.
func Register(email, fingerprint string) (*RegisterResponse, error) {
	body := map[string]string{
		"email":              email,
		"device_fingerprint": fingerprint,
	}
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequest("POST", defaultBaseURL+"/auth/register", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("registration request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("registration failed (%d): %s", resp.StatusCode, string(respBody))
	}

	var result RegisterResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse registration response: %w", err)
	}
	return &result, nil
}

// Subdomain represents a subdomain returned by the API.
type Subdomain struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	FQDN          string `json:"fqdn"`
	SubdomainKind string `json:"subdomain_kind"`
	ParentID      string `json:"parent_id"`
	Exposed       bool   `json:"exposed"`
	Status        string `json:"status"`
}

// AutoRegister registers a base subdomain using the naming convention.
func (c *Client) AutoRegister(homelabName, deviceFingerprint, description string) (*Subdomain, error) {
	body := map[string]string{
		"homelab_name":       homelabName,
		"kind":               "self-hosted",
		"device_fingerprint": deviceFingerprint,
		"description":        description,
	}
	var sub Subdomain
	if err := c.post("/subdomains/auto-register", body, &sub); err != nil {
		return nil, fmt.Errorf("auto-register base subdomain: %w", err)
	}
	return &sub, nil
}

// RegisterService registers a service subdomain under a base subdomain.
func (c *Client) RegisterService(baseSubdomainName, serviceName, localAddr, description string) (*Subdomain, error) {
	body := map[string]string{
		"base_subdomain_name": baseSubdomainName,
		"service_name":        serviceName,
		"local_addr":          localAddr,
		"description":         description,
	}
	var sub Subdomain
	if err := c.post("/subdomains/auto-register/service", body, &sub); err != nil {
		return nil, fmt.Errorf("register service %s: %w", serviceName, err)
	}
	return &sub, nil
}

// ExposeService toggles a service subdomain's public exposure.
func (c *Client) ExposeService(baseID, serviceID string, exposed bool) error {
	body := map[string]bool{"exposed": exposed}
	path := fmt.Sprintf("/subdomains/%s/services/%s/expose", baseID, serviceID)
	return c.put(path, body)
}

// ListServices lists all service subdomains under a base subdomain.
func (c *Client) ListServices(baseID string) ([]Subdomain, error) {
	path := fmt.Sprintf("/subdomains/%s/services", baseID)
	var subs []Subdomain
	if err := c.get(path, &subs); err != nil {
		return nil, fmt.Errorf("list services: %w", err)
	}
	return subs, nil
}

// ListServicesByPrefix lists all subdomains (base + services) for a given prefix.
// It first looks up the base subdomain by name, then lists its services.
func (c *Client) ListServicesByPrefix(prefix string) ([]Subdomain, error) {
	// Fetch the user's subdomains and find the base by name
	var allSubs []Subdomain
	if err := c.get("/subdomains", &allSubs); err != nil {
		return nil, fmt.Errorf("list subdomains: %w", err)
	}

	var result []Subdomain
	var baseID string
	for _, s := range allSubs {
		if s.Name == prefix && s.SubdomainKind == "base" {
			baseID = s.ID
			result = append(result, s)
			break
		}
	}

	if baseID == "" {
		return nil, nil
	}

	services, err := c.ListServices(baseID)
	if err != nil {
		return result, err
	}
	result = append(result, services...)
	return result, nil
}

// maxResponseSize limits API response bodies to 1 MB to prevent OOM from malicious servers.
const maxResponseSize = 1 << 20

func (c *Client) post(path string, body interface{}, result interface{}) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequest("POST", c.baseURL+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(apiKeyHeader, c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))

	if resp.StatusCode >= 400 {
		return fmt.Errorf("API error %d: %s", resp.StatusCode, string(respBody))
	}

	if result != nil {
		return json.Unmarshal(respBody, result)
	}
	return nil
}

func (c *Client) put(path string, body interface{}) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequest("PUT", c.baseURL+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(apiKeyHeader, c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
		return fmt.Errorf("API error %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

func (c *Client) delete(path string) error {
	req, err := http.NewRequest("DELETE", c.baseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set(apiKeyHeader, c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
		return fmt.Errorf("API error %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

func (c *Client) get(path string, result interface{}) error {
	req, err := http.NewRequest("GET", c.baseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set(apiKeyHeader, c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))

	if resp.StatusCode >= 400 {
		return fmt.Errorf("API error %d: %s", resp.StatusCode, string(respBody))
	}

	if result != nil {
		return json.Unmarshal(respBody, result)
	}
	return nil
}
