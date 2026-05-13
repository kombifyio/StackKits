package platformdeploy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type apiClient struct {
	cfg      HTTPConfig
	authMode string
}

const (
	authBearer = "bearer"
	authAPIKey = "x-api-key"
)

func (c apiClient) postJSON(ctx context.Context, path string, payload any, out any) (int, []byte, error) {
	return c.doJSON(ctx, http.MethodPost, path, payload, out)
}

func (c apiClient) getJSON(ctx context.Context, path string, out any) (int, []byte, error) {
	return c.doJSON(ctx, http.MethodGet, path, nil, out)
}

func (c apiClient) doJSON(ctx context.Context, method, path string, payload any, out any) (int, []byte, error) {
	url, err := c.cfg.endpoint(path)
	if err != nil {
		return 0, nil, err
	}

	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return 0, nil, fmt.Errorf("marshal platform API payload: %w", err)
		}
		body = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return 0, nil, fmt.Errorf("build platform API request: %w", err)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.cfg.Token != "" {
		switch c.authMode {
		case authAPIKey:
			req.Header.Set("X-Api-Key", c.cfg.Token)
		default:
			req.Header.Set("Authorization", "Bearer "+c.cfg.Token)
		}
	}

	resp, err := c.cfg.httpClient().Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("platform API request %s %s failed: %w", method, path, err)
	}
	defer resp.Body.Close()

	data, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return resp.StatusCode, nil, fmt.Errorf("read platform API response: %w", readErr)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, data, fmt.Errorf("platform API %s %s returned status %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(data)))
	}
	if out != nil && len(strings.TrimSpace(string(data))) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return resp.StatusCode, data, fmt.Errorf("decode platform API response: %w", err)
		}
	}
	return resp.StatusCode, data, nil
}

func firstString(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := values[key].(string); ok && value != "" {
			return value
		}
	}
	return ""
}
