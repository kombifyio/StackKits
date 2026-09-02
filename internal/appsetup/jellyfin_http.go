package appsetup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const jellyfinResponseBodyLimit = 1 << 20

// NewJellyfinHTTPClient returns the bounded client used by the Jellyfin setup
// adapter. Credentials and session tokens must not be sent through an
// environment-controlled proxy or across a redirect.
func NewJellyfinHTTPClient() *http.Client {
	return &http.Client{
		Timeout:   20 * time.Second,
		Transport: &http.Transport{Proxy: nil},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("Jellyfin setup refuses HTTP redirects")
		},
	}
}

// cloneJellyfinHTTPClient preserves an admitted custom transport while
// enforcing the setup adapter's timeout and redirect boundary. Native setup
// supplies a transport which performs its own exact container re-check.
func cloneJellyfinHTTPClient(client *http.Client) *http.Client {
	if client == nil {
		return NewJellyfinHTTPClient()
	}
	clone := *client
	if clone.Timeout <= 0 || clone.Timeout > 20*time.Second {
		clone.Timeout = 20 * time.Second
	}
	oldCheckRedirect := clone.CheckRedirect
	clone.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if oldCheckRedirect != nil {
			if err := oldCheckRedirect(req, via); err != nil {
				return err
			}
		}
		return errors.New("Jellyfin setup refuses HTTP redirects")
	}
	if clone.Transport == nil {
		clone.Transport = &http.Transport{Proxy: nil}
	}
	return &clone
}

type jellyfinHTTPError struct {
	status int
	cause  error
}

func (e *jellyfinHTTPError) Error() string {
	if e == nil || e.status == 0 {
		return "Jellyfin request failed"
	}
	return fmt.Sprintf("Jellyfin request failed (HTTP %d)", e.status)
}

func (e *jellyfinHTTPError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

type jellyfinResponseError struct{ cause error }

func (e *jellyfinResponseError) Error() string {
	return "Jellyfin response was invalid"
}

func (e *jellyfinResponseError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func jellyfinHTTPStatus(err error) int {
	var responseErr *jellyfinHTTPError
	if errors.As(err, &responseErr) {
		return responseErr.status
	}
	return 0
}

func jellyfinJSONRequest(ctx context.Context, client *http.Client, baseURL, method, path, token string, payload any, out any) error {
	_, err := jellyfinJSONRequestStatus(ctx, client, baseURL, method, path, token, payload, out)
	return err
}

func jellyfinJSONRequestStatus(ctx context.Context, client *http.Client, baseURL, method, path, token string, payload any, out any) (int, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	endpoint, err := jellyfinEndpointURL(baseURL, path)
	if err != nil {
		return 0, err
	}
	if client == nil {
		client = NewJellyfinHTTPClient()
		defer client.CloseIdleConnections()
	}
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return 0, &jellyfinResponseError{cause: err}
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return 0, &jellyfinResponseError{cause: err}
	}
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	// Password login also requires a client/device identity. Jellyfin rejects
	// AuthenticateByName without these fields before issuing a session token.
	authorization, err := jellyfinAuthorizationHeader(token)
	if err != nil {
		return 0, &jellyfinResponseError{cause: err}
	}
	req.Header.Set("Authorization", authorization)
	resp, err := client.Do(req)
	if err != nil {
		return 0, &jellyfinHTTPError{cause: err}
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, jellyfinResponseBodyLimit+1))
	if err != nil {
		return resp.StatusCode, &jellyfinResponseError{cause: err}
	}
	if len(data) > jellyfinResponseBodyLimit {
		return resp.StatusCode, &jellyfinResponseError{cause: errors.New("response exceeded the bounded setup limit")}
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return resp.StatusCode, &jellyfinHTTPError{status: resp.StatusCode}
	}
	if out == nil || len(bytes.TrimSpace(data)) == 0 {
		return resp.StatusCode, nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return resp.StatusCode, &jellyfinResponseError{cause: err}
	}
	return resp.StatusCode, nil
}

func jellyfinAuthorizationHeader(token string) (string, error) {
	if strings.ContainsAny(token, "\r\n\\\"") {
		return "", errors.New("Jellyfin session token was invalid")
	}
	header := `MediaBrowser Client="StackKit", Device="StackKit", DeviceId="stackkit-native-setup", Version="` + JellyfinPinnedVersion + `"`
	if token != "" {
		header += `, Token="` + token + `"`
	}
	return header, nil
}

func jellyfinEndpointURL(baseURL, path string) (string, error) {
	base, err := url.Parse(strings.TrimRight(strings.TrimSpace(baseURL), "/"))
	if err != nil || base.Scheme == "" || base.Host == "" {
		return "", errors.New("Jellyfin setup requires an absolute HTTP(S) URL")
	}
	if base.Scheme != "http" && base.Scheme != "https" {
		return "", errors.New("Jellyfin setup accepts only HTTP(S) URLs")
	}
	if base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return "", errors.New("Jellyfin setup URL cannot contain credentials, query, or fragment data")
	}
	if path == "" || !strings.HasPrefix(path, "/") {
		return "", errors.New("Jellyfin setup endpoint must be an absolute path")
	}
	endpoint, err := url.Parse(path)
	if err != nil || endpoint.IsAbs() || endpoint.Host != "" || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return "", errors.New("Jellyfin setup endpoint is invalid")
	}
	base.Path = strings.TrimRight(base.Path, "/") + endpoint.Path
	base.RawPath = ""
	base.RawQuery = ""
	base.Fragment = ""
	return base.String(), nil
}
