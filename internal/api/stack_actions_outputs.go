package api

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/kombifyio/stackkits/internal/tofu"
)

func collectStackKitOutputsStackAction(ctx context.Context, exec *tofu.Executor, resp *stackActionResponse) *stackActionStackKitOutputs {
	result, err := exec.Output(ctx)
	if err != nil || result == nil || !result.Success {
		detail := "tofu output unavailable"
		if result != nil && strings.TrimSpace(result.Stderr) != "" {
			detail = strings.TrimSpace(result.Stderr)
		} else if err != nil {
			detail = err.Error()
		}
		resp.Checks = append(resp.Checks, stackActionCheck{Name: "stackkit_outputs", Status: "warning", Detail: detail})
		return stackKitOutputsFromOpenTofuStackAction(*resp, nil)
	}

	values := parseOpenTofuOutputValuesStackAction(result.Stdout)
	resp.Checks = append(resp.Checks, stackActionCheck{Name: "stackkit_outputs", Status: "ok"})
	return stackKitOutputsFromOpenTofuStackAction(*resp, values)
}

func parseOpenTofuOutputValuesStackAction(raw string) map[string]string {
	var document map[string]struct {
		Value interface{} `json:"value"`
	}
	if err := json.Unmarshal([]byte(raw), &document); err != nil {
		return nil
	}
	values := make(map[string]string, len(document))
	for key, output := range document {
		switch v := output.Value.(type) {
		case string:
			values[key] = strings.TrimSpace(v)
		case float64:
			values[key] = strings.TrimSpace(jsonNumberStackAction(v))
		}
	}
	return values
}

func stackKitOutputsFromOpenTofuStackAction(resp stackActionResponse, values map[string]string) *stackActionStackKitOutputs {
	loginURL := firstRuntimeOutputStackAction(values, "tinyauth_login_url", "paas_url", "coolify_url", "dashboard_url", "homepage_url")
	if loginURL == "" {
		loginURL = "https://" + stackSlugStackAction(resp.StackName, resp.StackID) + ".kombify.me"
	}
	ownerEmail := firstRuntimeOutputStackAction(values, "coolify_admin_email", "admin_email")
	ownerUsername := ownerEmail
	if ownerUsername == "" {
		ownerUsername = "owner"
	}
	links := make([]stackActionServiceLink, 0, 15)
	for _, candidate := range []struct {
		name string
		keys []string
	}{
		{name: "base", keys: []string{"dashboard_url"}},
		{name: "homepage", keys: []string{"homepage_url"}},
		{name: "auth", keys: []string{"tinyauth_login_url", "auth_url"}},
		{name: "pocketid", keys: []string{"pocketid_url"}},
		{name: "traefik", keys: []string{"traefik_url"}},
		{name: "coolify", keys: []string{"coolify_url"}},
		{name: "komodo", keys: []string{"komodo_url"}},
		{name: "dokploy", keys: []string{"dokploy_url"}},
		{name: "dockge", keys: []string{"dockge_url"}},
		{name: "monitoring", keys: []string{"kuma_url"}},
		{name: "whoami", keys: []string{"whoami_url"}},
		{name: "vaultwarden", keys: []string{"vaultwarden_url"}},
		{name: "jellyfin", keys: []string{"jellyfin_url"}},
		{name: "immich", keys: []string{"immich_url"}},
		{name: "files", keys: []string{"files_url"}},
	} {
		if url := firstRuntimeOutputStackAction(values, candidate.keys...); url != "" {
			links = append(links, stackActionServiceLink{Name: candidate.name, URL: url})
		}
	}
	return &stackActionStackKitOutputs{
		Identity: &stackActionIdentityOutputs{
			Owner: &stackActionOwnerOutput{
				Username:    ownerUsername,
				Email:       ownerEmail,
				DisplayName: "Owner",
			},
			Recovery: &stackActionRecoveryOutput{
				BundleRef: "homelab://recovery/stacks/" + resp.StackID,
			},
		},
		LoginGateway: &stackActionLoginGateway{
			URL:   loginURL,
			Label: "Open first login",
		},
		Recovery: &stackActionRecoveryOutput{
			BundleRef: "homelab://recovery/stacks/" + resp.StackID,
		},
		Services: links,
	}
}

func firstRuntimeOutputStackAction(values map[string]string, keys ...string) string {
	for _, key := range keys {
		if values != nil && strings.TrimSpace(values[key]) != "" {
			return strings.TrimSpace(values[key])
		}
	}
	return ""
}

func stackSlugStackAction(values ...string) string {
	for _, value := range values {
		slug := strings.ToLower(strings.TrimSpace(value))
		slug = strings.Map(func(r rune) rune {
			switch {
			case r >= 'a' && r <= 'z':
				return r
			case r >= '0' && r <= '9':
				return r
			case r == '-':
				return r
			default:
				return '-'
			}
		}, slug)
		slug = strings.Trim(slug, "-")
		for strings.Contains(slug, "--") {
			slug = strings.ReplaceAll(slug, "--", "-")
		}
		if slug != "" {
			return slug
		}
	}
	return "homelab"
}

func jsonNumberStackAction(value float64) string {
	if value == float64(int64(value)) {
		return strconv.FormatInt(int64(value), 10)
	}
	return strconv.FormatFloat(value, 'f', -1, 64)
}
