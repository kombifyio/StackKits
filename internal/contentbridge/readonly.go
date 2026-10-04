package contentbridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"time"
)

// Outcomes of reading an app. They map one to one onto the statuses of the
// contract, so a failed read can never become an empty answer.
var (
	ErrAppUnreachable    = errors.New("content bridge: app unreachable")
	ErrCredentialInvalid = errors.New("content bridge: app credential invalid")
	ErrNotConfigured     = errors.New("content bridge: app not configured")
	// ErrRequestRefused is returned for anything outside the read-only
	// allowlist. It signals a defect, never a state of the app.
	ErrRequestRefused = errors.New("content bridge: request refused by the read-only allowlist")
)

// maxAppResponseBytes bounds one app answer. The bridge reads counts only.
const maxAppResponseBytes = 64 << 10

// Endpoint is one allowlisted GET endpoint of an app and the query
// parameters it may carry.
type Endpoint struct {
	Path  string
	Query []string
}

// KeyFunc returns the app credential. It reads custody on every call, so a
// rotated key takes effect without a restart. It returns ErrNotConfigured
// when no credential has been issued yet.
type KeyFunc func() (string, error)

// NewReadOnlyTransport wraps next so that only GET requests to the listed
// endpoints of one origin can leave the bridge. The guard sits in the
// transport, below every caller: no code path that holds the client, now or
// later, can issue a mutating call by choosing another method, path or host.
func NewReadOnlyTransport(next http.RoundTripper, origin *url.URL, endpoints []Endpoint) http.RoundTripper {
	allowed := make(map[string][]string, len(endpoints))
	for _, endpoint := range endpoints {
		allowed[endpoint.Path] = slices.Clone(endpoint.Query)
	}
	return &readOnlyTransport{next: next, scheme: origin.Scheme, host: origin.Host, allowed: allowed}
}

type readOnlyTransport struct {
	next         http.RoundTripper
	scheme, host string
	allowed      map[string][]string
}

func (t *readOnlyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := t.admit(req); err != nil {
		if req.Body != nil {
			_ = req.Body.Close()
		}
		return nil, err
	}
	return t.next.RoundTrip(req)
}

// admit checks a request without echoing any part of it into the error:
// paths and queries of an app can carry identifiers.
func (t *readOnlyTransport) admit(req *http.Request) error {
	target := req.URL
	queryKeys, pathAllowed := t.allowed[target.Path]
	switch {
	case req.Method != http.MethodGet,
		req.Body != nil && req.Body != http.NoBody,
		target.Scheme != t.scheme, target.Host != t.host,
		target.User != nil, target.Fragment != "", target.Opaque != "",
		!pathAllowed:
		return ErrRequestRefused
	}
	for key := range target.Query() {
		if !slices.Contains(queryKeys, key) {
			return ErrRequestRefused
		}
	}
	return nil
}

// ReadOnlyClient reads JSON from one app. It has no method for anything but
// GET, and its transport refuses everything outside the allowlist.
type ReadOnlyClient struct {
	origin *url.URL
	client *http.Client
	key    KeyFunc
}

// NewReadOnlyClient builds the client for one app origin. base is the
// network transport below the guard; nil selects a direct transport that
// ignores proxy environment variables, because the app is on the node's own
// internal network and the bridge has no egress.
func NewReadOnlyClient(origin string, endpoints []Endpoint, key KeyFunc, base http.RoundTripper) (*ReadOnlyClient, error) {
	parsed, err := url.Parse(origin)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" ||
		parsed.User != nil || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("content bridge: the app origin must be a bare http or https origin")
	}
	parsed.Path = ""
	if key == nil {
		return nil, errors.New("content bridge: an app credential source is required")
	}
	if base == nil {
		base = &http.Transport{
			Proxy:                 nil,
			DialContext:           (&net.Dialer{Timeout: 3 * time.Second}).DialContext,
			TLSHandshakeTimeout:   3 * time.Second,
			ResponseHeaderTimeout: 5 * time.Second,
			MaxIdleConns:          2,
			IdleConnTimeout:       30 * time.Second,
		}
	}
	return &ReadOnlyClient{
		origin: parsed,
		key:    key,
		client: &http.Client{
			Transport: NewReadOnlyTransport(base, parsed, endpoints),
			Timeout:   8 * time.Second,
			// A redirect could leave the allowlisted origin and carry the key.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}, nil
}

// GetJSON reads one allowlisted endpoint and decodes the answer into out.
// The error is one of the outcome errors above; it never contains a URL, a
// credential or a response body.
func (c *ReadOnlyClient) GetJSON(ctx context.Context, path string, query url.Values, out any) error {
	key, err := c.key()
	if err != nil {
		return err
	}
	target := *c.origin
	target.Path = path
	target.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return ErrRequestRefused
	}
	req.Header.Set("x-api-key", key)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "kombify-content-bridge")
	resp, err := c.client.Do(req)
	if err != nil {
		if errors.Is(err, ErrRequestRefused) {
			return ErrRequestRefused
		}
		return ErrAppUnreachable
	}
	defer func() { _ = resp.Body.Close() }()
	switch {
	case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden:
		return ErrCredentialInvalid
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return ErrAppUnreachable
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxAppResponseBytes+1))
	if err != nil || len(body) > maxAppResponseBytes {
		return ErrAppUnreachable
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("%w: unexpected answer shape", ErrAppUnreachable)
	}
	return nil
}
