package registry

// channel_resolver.go is the CLI-side client for the Admin compatibility
// resolver endpoint introduced in kit-update-phase-1 (ADR-0018 §2).
//
// Wire shape:
//   GET /api/v1/sk/compat/resolve
//     ?kit_slug=<slug>&kit_version=<id>&kit_channel=<edge|beta|stable>
//     [&module_channel=<edge|beta|stable>]
//
//   Response body:
//     {
//       "kitVersionId": "<uuid>",
//       "kitChannel":   "stable",
//       "modules": [
//         {"moduleSlug":"traefik","moduleVersionId":"...","moduleSemver":"3.2.0",
//          "channel":"stable","reason":"matched"},
//         ...
//       ]
//     }
//
// `reason` is one of `matched` | `fallback` | `override` so the CLI can
// surface "this module fell back to beta because no stable version is
// compatible" verbatim in the upgrade dry-run.

import (
	"context"
	"fmt"
	"net/url"
)

// ResolveRequest is the input to a resolver call.
type ResolveRequest struct {
	KitSlug       string
	KitVersionID  string // sk_stackkit.id (UUID); identifies the kit-version row in Phase 1
	KitChannel    string // edge | beta | stable
	ModuleChannel string // optional override; "" = inherit from KitChannel
}

// ResolveResult is the resolver-decision snapshot for one upgrade.
type ResolveResult struct {
	KitVersionID string           `json:"kitVersionId"`
	KitChannel   string           `json:"kitChannel"`
	Modules      []ResolvedModule `json:"modules"`
}

// ResolvedModule is one row in ResolveResult.Modules.
type ResolvedModule struct {
	ModuleSlug      string `json:"moduleSlug"`
	ModuleVersionID string `json:"moduleVersionId"`
	ModuleSemver    string `json:"moduleSemver"`
	Channel         string `json:"channel"`
	Reason          string `json:"reason"` // matched | fallback | override
}

// ChannelResolver wraps a RemoteClient with the resolver-specific path.
// It deliberately does NOT live behind the embedded-client fallback:
// a resolver decision needs the live DB view (sk_kit_module_compat),
// so an offline CLI cannot answer it. EmbeddedClient callers should
// use the static module-version pinned in the kit composition instead.
type ChannelResolver struct {
	client *RemoteClient
}

// NewChannelResolver builds a resolver client. baseURL/token follow the
// same env-var pattern as the rest of the registry package
// (STACKKIT_ADMIN_ENDPOINT / STACKKIT_ADMIN_TOKEN).
func NewChannelResolver(baseURL, token string) *ChannelResolver {
	return &ChannelResolver{client: NewRemoteClient(baseURL, token)}
}

// Resolve issues the GET against /api/v1/sk/compat/resolve.
func (r *ChannelResolver) Resolve(ctx context.Context, req ResolveRequest) (*ResolveResult, error) {
	if req.KitSlug == "" {
		return nil, fmt.Errorf("resolver: kit_slug is required")
	}
	if req.KitVersionID == "" {
		return nil, fmt.Errorf("resolver: kit_version is required")
	}
	if req.KitChannel == "" {
		req.KitChannel = "stable"
	}
	if !isValidChannel(req.KitChannel) {
		return nil, fmt.Errorf("resolver: invalid kit_channel %q (want edge|beta|stable)", req.KitChannel)
	}
	if req.ModuleChannel != "" && !isValidChannel(req.ModuleChannel) {
		return nil, fmt.Errorf("resolver: invalid module_channel %q (want edge|beta|stable)", req.ModuleChannel)
	}

	q := url.Values{}
	q.Set("kit_slug", req.KitSlug)
	q.Set("kit_version", req.KitVersionID)
	q.Set("kit_channel", req.KitChannel)
	if req.ModuleChannel != "" {
		q.Set("module_channel", req.ModuleChannel)
	}

	path := "/api/v1/sk/compat/resolve?" + q.Encode()
	var out ResolveResult
	if err := r.client.getJSON(ctx, path, &out); err != nil {
		return nil, fmt.Errorf("resolver: %w", err)
	}
	return &out, nil
}

// SummarizeReasons folds the result into a human-readable map of
// reason -> count. CLI uses this for the dry-run summary line:
//
//	"15 matched, 2 fallback, 1 override"
func (r *ResolveResult) SummarizeReasons() map[string]int {
	out := map[string]int{"matched": 0, "fallback": 0, "override": 0}
	for _, m := range r.Modules {
		out[m.Reason]++
	}
	return out
}

func isValidChannel(c string) bool {
	switch c {
	case "edge", "beta", "stable":
		return true
	default:
		return false
	}
}
