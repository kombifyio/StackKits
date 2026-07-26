package releaseindex

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

const defaultGitHubAPI = "https://api.github.com"

type GitHubSource struct {
	Client     *http.Client
	APIBaseURL string
	Repository string
}

func NewGitHubSource(client *http.Client) *GitHubSource {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &GitHubSource{Client: client, APIBaseURL: defaultGitHubAPI, Repository: "kombifyio/stackKits"}
}

// NewGitHubFixtureSource creates the hermetic HTTP adapter used by the
// standalone OSS E2E. It deliberately accepts only loopback and the reserved
// .localhost namespace; public release URLs retain the GitHub-only policy.
func NewGitHubFixtureSource(client *http.Client, apiBaseURL string) (*GitHubSource, error) {
	parsed, err := url.Parse(strings.TrimSpace(apiBaseURL))
	if err != nil {
		return nil, fmt.Errorf("parse GitHub release fixture URL: %w", err)
	}
	if parsed.Scheme != "http" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		!isLoopbackFixtureHost(parsed.Hostname()) || parsed.Port() == "" {
		return nil, fmt.Errorf("GitHub release fixture URL must be an explicit HTTP loopback or .localhost endpoint")
	}
	source := NewGitHubSource(client)
	source.APIBaseURL = strings.TrimRight(parsed.String(), "/")
	return source, nil
}

func (source *GitHubSource) ListReleases(ctx context.Context) ([]Release, error) {
	if source == nil || source.Client == nil {
		return nil, fmt.Errorf("GitHub HTTP client is required")
	}
	if source.Repository != "kombifyio/stackKits" {
		return nil, fmt.Errorf("GitHub release repository %q is not trusted", source.Repository)
	}
	endpoint := strings.TrimRight(source.APIBaseURL, "/") + "/repos/" + source.Repository + "/releases?per_page=100"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	response, err := source.Client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub releases returned HTTP %d", response.StatusCode)
	}
	var payload []struct {
		TagName     string    `json:"tag_name"`
		Prerelease  bool      `json:"prerelease"`
		Draft       bool      `json:"draft"`
		PublishedAt time.Time `json:"published_at"`
		Assets      []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
		} `json:"assets"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, maxIndexBytes))
	if err := decoder.Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode GitHub releases: %w", err)
	}
	releases := make([]Release, 0, len(payload))
	for _, item := range payload {
		if item.Draft {
			continue
		}
		assetURLs := make(map[string]string, len(item.Assets))
		for _, asset := range item.Assets {
			assetURLs[asset.Name] = asset.BrowserDownloadURL
		}
		indexURL := assetURLs[ReleaseIndexAssetName]
		indexAttestationURL := assetURLs[ReleaseIndexAttestationAssetName]
		trustedRootURL := assetURLs[TrustedRootAssetName]
		if indexURL == "" || indexAttestationURL == "" || trustedRootURL == "" {
			continue
		}
		releases = append(releases, Release{
			TagName: item.TagName, Prerelease: item.Prerelease, PublishedAt: item.PublishedAt,
			IndexURL: indexURL, IndexAttestationURL: indexAttestationURL, TrustedRootURL: trustedRootURL,
		})
	}
	return releases, nil
}

func (source *GitHubSource) Fetch(ctx context.Context, location string, limit int64) ([]byte, error) {
	if source == nil || source.Client == nil {
		return nil, fmt.Errorf("GitHub HTTP client is required")
	}
	if limit <= 0 {
		return nil, fmt.Errorf("positive download limit is required")
	}
	parsed, err := url.Parse(location)
	if err != nil {
		return nil, fmt.Errorf("parse GitHub asset URL: %w", err)
	}
	if err := source.admitURL(parsed); err != nil {
		return nil, err
	}
	if fixtureBase, fixtureMode := localFixtureBase(source.APIBaseURL); fixtureMode && parsed.Host != fixtureBase.Host {
		parsed = &url.URL{
			Scheme: fixtureBase.Scheme,
			Host:   fixtureBase.Host,
			Path:   "/assets/" + url.PathEscape(path.Base(parsed.Path)),
		}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/octet-stream")
	response, err := source.Client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub asset returned HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("GitHub asset exceeds %d bytes", limit)
	}
	return data, nil
}

func (source *GitHubSource) admitURL(parsed *url.URL) error {
	fixtureBase, fixtureMode := localFixtureBase(source.APIBaseURL)
	if parsed.Scheme != "https" && !fixtureMode {
		return fmt.Errorf("release asset URL must use HTTPS")
	}
	if fixtureMode {
		if parsed.Scheme == fixtureBase.Scheme && parsed.Host == fixtureBase.Host {
			return nil
		}
	}
	switch strings.ToLower(parsed.Hostname()) {
	case "github.com", "api.github.com", "objects.githubusercontent.com", "release-assets.githubusercontent.com", "github-releases.githubusercontent.com":
		return nil
	default:
		return fmt.Errorf("release asset host %q is not GitHub-controlled", parsed.Hostname())
	}
}

func localFixtureBase(raw string) (*url.URL, bool) {
	parsed, err := url.Parse(raw)
	return parsed, err == nil && parsed.Scheme == "http" && parsed.Port() != "" &&
		isLoopbackFixtureHost(parsed.Hostname())
}

func isLoopbackFixtureHost(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	parsed := net.ParseIP(host)
	return parsed != nil && parsed.IsLoopback()
}
