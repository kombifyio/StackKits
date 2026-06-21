package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// kitVersionMeta is the minimal shape we need from
// `/api/v1/sk/registry/stackkits/<slug>/versions`. The admin endpoint
// returns more fields; we ignore them.
type kitVersionMeta struct {
	ID         string    `json:"id"`
	Semver     string    `json:"semver"`
	Channel    string    `json:"releaseChannel"`
	ReleasedAt time.Time `json:"releasedAt,omitempty"`
}

// fetchVersions hits GET /api/v1/sk/registry/stackkits/<slug>/versions?channel=<c>
// and returns the parsed list. Empty list is a valid response; caller decides
// what to do.
func fetchVersions(ctx context.Context, endpoint, token, kitSlug, channel string) ([]kitVersionMeta, error) {
	if endpoint == "" {
		return nil, errors.New("admin endpoint not configured")
	}
	url := fmt.Sprintf("%s/api/v1/sk/registry/stackkits/%s/versions?channel=%s",
		strings.TrimRight(endpoint, "/"), kitSlug, channel)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil) // #nosec G107 G704 -- endpoint is an operator-supplied admin URL.
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req) // #nosec G107 G704 -- request URL is operator-supplied CLI configuration.
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("admin %s: status=%d body=%s", url, resp.StatusCode, string(body))
	}

	var versions []kitVersionMeta
	if err := json.NewDecoder(resp.Body).Decode(&versions); err != nil {
		return nil, fmt.Errorf("decode versions: %w", err)
	}
	return versions, nil
}
