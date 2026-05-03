package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// EnvEndpoint is the env var that routes the CLI to the private Admin
// API. When it is set, registry calls hit live DB data; otherwise the
// CLI falls back to the baked-in snapshot (OSS mode).
const EnvEndpoint = "STACKKIT_ADMIN_ENDPOINT"

// EnvToken is the Bearer token for the Admin API. Only relevant when
// EnvEndpoint is set.
const EnvToken = "STACKKIT_ADMIN_TOKEN"

// Client abstracts the registry read path. Two implementations ship:
// RemoteClient (Admin API, kombify-internal) and EmbeddedClient
// (baked-in snapshot, OSS-safe). AutoClient picks between them.
type Client interface {
	Source() string
	Snapshot(ctx context.Context) (Snapshot, error)

	// Tool / Module / StackKit return the matching entry or
	// ErrNotFound when the slug is unknown to the registry.
	Tool(ctx context.Context, slug string) (Tool, error)
	Module(ctx context.Context, slug string) (Module, error)
	StackKit(ctx context.Context, slug string) (StackKit, error)
}

// ErrNotFound signals an unknown slug. Callers use errors.Is.
var ErrNotFound = fmt.Errorf("registry: not found")

// ---------------------------------------------------------------------------
// AutoClient
// ---------------------------------------------------------------------------

// AutoClient chooses the appropriate backend based on process env:
//   - STACKKIT_ADMIN_ENDPOINT set -> RemoteClient
//   - otherwise                  -> EmbeddedClient
//
// The selection is intentionally static: callers that want to force one
// backend can construct it directly.
func AutoClient() Client {
	if ep := os.Getenv(EnvEndpoint); ep != "" {
		return NewRemoteClient(ep, os.Getenv(EnvToken))
	}
	return NewEmbeddedClient()
}

// ---------------------------------------------------------------------------
// EmbeddedClient
// ---------------------------------------------------------------------------

// EmbeddedClient serves the baked-in snapshot. It is the OSS default and
// the only backend available when the CLI is installed from the public
// stackKits repo.
type EmbeddedClient struct {
	snap Snapshot
	err  error
}

// NewEmbeddedClient loads the embedded snapshot eagerly so that
// downstream calls are trivial. Decode errors are captured and returned
// from every method so the caller sees them as soon as they use the
// client.
func NewEmbeddedClient() *EmbeddedClient {
	snap, err := EmbeddedSnapshot()
	return &EmbeddedClient{snap: snap, err: err}
}

// Source returns "embedded".
func (c *EmbeddedClient) Source() string { return "embedded" }

// Snapshot returns a copy of the embedded snapshot.
func (c *EmbeddedClient) Snapshot(_ context.Context) (Snapshot, error) {
	if c.err != nil {
		return Snapshot{}, c.err
	}
	return c.snap, nil
}

// Tool looks up a tool by slug.
func (c *EmbeddedClient) Tool(_ context.Context, slug string) (Tool, error) {
	if c.err != nil {
		return Tool{}, c.err
	}
	for _, t := range c.snap.Tools {
		if t.Slug == slug {
			return t, nil
		}
	}
	return Tool{}, fmt.Errorf("%w: tool %q", ErrNotFound, slug)
}

// Module looks up a module by slug.
func (c *EmbeddedClient) Module(_ context.Context, slug string) (Module, error) {
	if c.err != nil {
		return Module{}, c.err
	}
	for _, m := range c.snap.Modules {
		if m.Slug == slug {
			return m, nil
		}
	}
	return Module{}, fmt.Errorf("%w: module %q", ErrNotFound, slug)
}

// StackKit looks up a stackkit by slug.
func (c *EmbeddedClient) StackKit(_ context.Context, slug string) (StackKit, error) {
	if c.err != nil {
		return StackKit{}, c.err
	}
	for _, s := range c.snap.StackKits {
		if s.Slug == slug {
			return s, nil
		}
	}
	return StackKit{}, fmt.Errorf("%w: stackkit %q", ErrNotFound, slug)
}

// ---------------------------------------------------------------------------
// RemoteClient
// ---------------------------------------------------------------------------

// RemoteClient talks to the Admin API (/api/v1/sk/registry/*). It is
// used by kombify-internal builds and CI release jobs. Absent a
// configured endpoint, callers should use EmbeddedClient instead.
//
// The remote schema mirrors the snapshot format; the Admin API exposes
// a /snapshot endpoint that returns the same JSON shape we serialize to
// disk. When that endpoint is not yet available, RemoteClient falls
// back to stitching together separate list endpoints.
type RemoteClient struct {
	baseURL  string
	token    string
	http     *http.Client
	endpoint string
}

// NewRemoteClient builds a RemoteClient. baseURL is trimmed of trailing
// slashes; token may be empty for unauthenticated dev instances.
func NewRemoteClient(baseURL, token string) *RemoteClient {
	return &RemoteClient{
		baseURL:  trimTrailingSlash(baseURL),
		token:    token,
		http:     &http.Client{Timeout: 30 * time.Second},
		endpoint: trimTrailingSlash(baseURL),
	}
}

// Source returns SourceAdminAPI.
func (c *RemoteClient) Source() string { return SourceAdminAPI }

// Snapshot fetches the complete registry from the Admin API. The
// returned Snapshot has Source=SourceAdminAPI and GeneratedAt set by
// the server (or, if the server omits it, by the client).
func (c *RemoteClient) Snapshot(ctx context.Context) (Snapshot, error) {
	var snap Snapshot
	if err := c.getJSON(ctx, "/api/v1/sk/registry/snapshot", &snap); err != nil {
		return Snapshot{}, fmt.Errorf("admin registry snapshot: %w", err)
	}
	if snap.Source == "" {
		snap.Source = SourceAdminAPI
	}
	if snap.GeneratedAt.IsZero() {
		snap.GeneratedAt = time.Now().UTC()
	}
	snap.AdminEndpoint = c.endpoint
	return snap, nil
}

// Tool fetches one tool from the Admin API.
func (c *RemoteClient) Tool(ctx context.Context, slug string) (Tool, error) {
	var t Tool
	path := fmt.Sprintf("/api/v1/sk/registry/tools/%s", slug)
	if err := c.getJSON(ctx, path, &t); err != nil {
		return Tool{}, err
	}
	return t, nil
}

// Module fetches the latest version of one module from the Admin API.
func (c *RemoteClient) Module(ctx context.Context, slug string) (Module, error) {
	var m Module
	path := fmt.Sprintf("/api/v1/sk/registry/modules/%s?latest=true", slug)
	if err := c.getJSON(ctx, path, &m); err != nil {
		return Module{}, err
	}
	return m, nil
}

// StackKit fetches one curated stackkit from the Admin API.
func (c *RemoteClient) StackKit(ctx context.Context, slug string) (StackKit, error) {
	var s StackKit
	path := fmt.Sprintf("/api/v1/sk/registry/stackkits/%s", slug)
	if err := c.getJSON(ctx, path, &s); err != nil {
		return StackKit{}, err
	}
	return s, nil
}

// getJSON is a tiny GET-and-decode helper. 404 is mapped to ErrNotFound.
func (c *RemoteClient) getJSON(ctx context.Context, path string, out interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("%w: %s", ErrNotFound, path)
	}
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("admin API %s: status=%d body=%s", path, resp.StatusCode, string(body))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// trimTrailingSlash duplicates commands.trimTrailingSlash to keep the
// registry package import-graph independent of cmd/stackkit.
func trimTrailingSlash(s string) string {
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}
