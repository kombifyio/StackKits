// Package servicecatalog normalizes StackKit service identity across CUE,
// the Admin registry snapshot, generated URLs, and kombify.me registration.
package servicecatalog

import (
	"sort"

	cueval "github.com/kombifyio/stackkits/internal/cue"
	"github.com/kombifyio/stackkits/internal/registry"
)

const (
	IdentityPolicyNone        = "none"
	IdentityPolicyForwardAuth = "forwardauth"
	IdentityPolicyOIDC        = "oidc"
	IdentityPolicyProvider    = "provider"
	IdentityPolicySelfAuth    = "self-auth"

	OwnerProvisioningNone     = "none"
	OwnerProvisioningRequired = "required"
)

// Service is the canonical service-facing identity for one exposed StackKit
// endpoint. Tool and module names describe implementation; Key and slugs
// describe the product URL contract.
type Service struct {
	Key                     string   `json:"key"`
	Name                    string   `json:"name"`
	DisplayName             string   `json:"display_name"`
	Description             string   `json:"description,omitempty"`
	ToolName                string   `json:"tool_name"`
	ModuleSlug              string   `json:"module_slug"`
	LocalSlug               string   `json:"local_slug"`
	PublicSlug              string   `json:"public_slug"`
	LegacyAliases           []string `json:"legacy_aliases,omitempty"`
	IdentityPolicy          string   `json:"identity_policy"`
	OwnerProvisioningPolicy string   `json:"owner_provisioning_policy"`
	Icon                    string   `json:"icon,omitempty"`
	Badge                   string   `json:"badge,omitempty"`
	Section                 string   `json:"section,omitempty"`
	Order                   int      `json:"order,omitempty"`
	EnableVar               string   `json:"enable_var,omitempty"`
	GuideURL                string   `json:"guide_url,omitempty"`
	Default                 bool     `json:"default"`

	// Template-compat fields used by the existing monolithic Base Kit template.
	// They intentionally mirror LocalSlug/PublicSlug until the template is fully
	// generated from the registry service shape.
	Nested string `json:"-"`
	Flat   string `json:"-"`
}

// Default returns the OSS-safe canonical service catalog used when the Admin
// registry is unavailable or its snapshot predates the services projection.
func Default() []Service {
	services := []Service{
		{
			Key: "base", Name: "base", ToolName: "dashboard", ModuleSlug: "dashboard",
			DisplayName: "Node Hub", Description: "StackKits node hub with onboarding, recovery, and local service links.",
			LocalSlug: "base", PublicSlug: "base", LegacyAliases: []string{"dashboard", "dash"},
			IdentityPolicy: IdentityPolicyForwardAuth, OwnerProvisioningPolicy: OwnerProvisioningRequired,
			Icon: "&#128421;", Badge: "L3 \u00b7 Hub", Section: "Platform", Order: -1, EnableVar: "enable_dashboard", GuideURL: "https://docs.kombify.io/guides/stackkits/node-hub", Default: true,
		},
		{
			Key: "home", Name: "home", ToolName: "homepage", ModuleSlug: "homepage",
			DisplayName: "Homepage", Description: "IaC-managed homelab start dashboard generated from the StackKits service catalog.",
			LocalSlug: "home", PublicSlug: "home", LegacyAliases: []string{"homepage", "homelab-dashboard"},
			IdentityPolicy: IdentityPolicyForwardAuth, OwnerProvisioningPolicy: OwnerProvisioningRequired,
			Icon: "&#8962;", Badge: "L3 \u00b7 Start", Section: "Platform", Order: 0, EnableVar: "enable_homepage", GuideURL: "https://docs.kombify.io/guides/stackkits/services/homepage", Default: true,
		},
		{
			Key: "auth", Name: "auth", ToolName: "tinyauth", ModuleSlug: "tinyauth",
			DisplayName: "TinyAuth", Description: "ForwardAuth gateway backed by PocketID.",
			LocalSlug: "auth", PublicSlug: "auth", LegacyAliases: []string{"tinyauth"},
			IdentityPolicy: IdentityPolicyOIDC, OwnerProvisioningPolicy: OwnerProvisioningRequired,
			Icon: "&#128274;", Badge: "L1 \u00b7 ForwardAuth", Section: "Platform", Order: 20, EnableVar: "enable_tinyauth", GuideURL: "https://docs.kombify.io/guides/stackkits/services/tinyauth", Default: true,
		},
		{
			Key: "id", Name: "id", ToolName: "pocketid", ModuleSlug: "pocketid",
			DisplayName: "PocketID", Description: "OIDC identity provider with passkey authentication.",
			LocalSlug: "id", PublicSlug: "id", LegacyAliases: []string{"pocketid"},
			IdentityPolicy: IdentityPolicyProvider, OwnerProvisioningPolicy: OwnerProvisioningRequired,
			Icon: "&#128100;", Badge: "L1 \u00b7 IdP", Section: "Platform", Order: 10, EnableVar: "enable_pocketid", GuideURL: "https://docs.kombify.io/guides/stackkits/services/pocketid", Default: true,
		},
		{
			Key: "traefik", Name: "traefik", ToolName: "traefik", ModuleSlug: "traefik",
			DisplayName: "Traefik", Description: "Routes all service traffic.",
			LocalSlug: "traefik", PublicSlug: "traefik",
			IdentityPolicy: IdentityPolicyForwardAuth, OwnerProvisioningPolicy: OwnerProvisioningRequired,
			Icon: "&#9889;", Badge: "L2 \u00b7 Reverse Proxy", Section: "Platform", Order: 30, EnableVar: "enable_traefik", GuideURL: "https://docs.kombify.io/guides/stackkits/services/traefik", Default: true,
		},
		{
			Key: "dokploy", Name: "dokploy", ToolName: "dokploy", ModuleSlug: "dokploy",
			DisplayName: "Dokploy", Description: "Self-hosted PaaS for deploying applications.",
			LocalSlug: "dokploy", PublicSlug: "dokploy",
			IdentityPolicy: IdentityPolicyForwardAuth, OwnerProvisioningPolicy: OwnerProvisioningRequired,
			Icon: "&#128640;", Badge: "L2 \u00b7 PaaS", Section: "Platform", Order: 40, EnableVar: "enable_dokploy", GuideURL: "https://docs.kombify.io/guides/stackkits/services/dokploy", Default: true,
		},
		{
			Key: "kuma", Name: "kuma", ToolName: "uptime-kuma", ModuleSlug: "uptime-kuma",
			DisplayName: "Uptime Kuma", Description: "Service uptime monitoring and status pages.",
			LocalSlug: "kuma", PublicSlug: "kuma", LegacyAliases: []string{"uptime-kuma"},
			IdentityPolicy: IdentityPolicyForwardAuth, OwnerProvisioningPolicy: OwnerProvisioningRequired,
			Icon: "&#128202;", Badge: "L3 \u00b7 Monitoring", Section: "Applications", Order: 10, EnableVar: "enable_uptime_kuma", GuideURL: "https://docs.kombify.io/guides/stackkits/services/uptime-kuma", Default: true,
		},
		{
			Key: "whoami", Name: "whoami", ToolName: "whoami", ModuleSlug: "whoami",
			DisplayName: "Whoami", Description: "TinyAuth-protected HTTP echo service for routing diagnostics.",
			LocalSlug: "whoami", PublicSlug: "whoami",
			IdentityPolicy: IdentityPolicyForwardAuth, OwnerProvisioningPolicy: OwnerProvisioningRequired,
			Icon: "&#129302;", Badge: "L3 \u00b7 SSO test", Section: "Applications", Order: 20, EnableVar: "enable_whoami", GuideURL: "https://docs.kombify.io/guides/stackkits/services/whoami", Default: true,
		},
		{
			Key: "vault", Name: "vault", ToolName: "vaultwarden", ModuleSlug: "vaultwarden",
			DisplayName: "Vaultwarden", Description: "Bitwarden-compatible password vault with its own app login.",
			LocalSlug: "vault", PublicSlug: "vault", LegacyAliases: []string{"vaultwarden"},
			IdentityPolicy: IdentityPolicySelfAuth, OwnerProvisioningPolicy: OwnerProvisioningNone,
			Icon: "&#128272;", Badge: "L3 \u00b7 App login", Section: "Applications", Order: 30, EnableVar: "enable_vaultwarden", GuideURL: "https://docs.kombify.io/guides/stackkits/services/vaultwarden", Default: true,
		},
		{
			Key: "media", Name: "media", ToolName: "jellyfin", ModuleSlug: "jellyfin",
			DisplayName: "Jellyfin", Description: "Media server with its own app login.",
			LocalSlug: "media", PublicSlug: "media", LegacyAliases: []string{"jellyfin"},
			IdentityPolicy: IdentityPolicySelfAuth, OwnerProvisioningPolicy: OwnerProvisioningNone,
			Icon: "&#127916;", Badge: "L3 \u00b7 App login", Section: "Applications", Order: 40, EnableVar: "enable_jellyfin", GuideURL: "https://docs.kombify.io/guides/stackkits/services/jellyfin", Default: true,
		},
		{
			Key: "photos", Name: "photos", ToolName: "immich", ModuleSlug: "immich",
			DisplayName: "Immich", Description: "Photo and video management with its own app login.",
			LocalSlug: "photos", PublicSlug: "photos", LegacyAliases: []string{"immich"},
			IdentityPolicy: IdentityPolicySelfAuth, OwnerProvisioningPolicy: OwnerProvisioningRequired,
			Icon: "&#128247;", Badge: "L3 \u00b7 App login", Section: "Applications", Order: 50, EnableVar: "enable_immich", GuideURL: "https://docs.kombify.io/guides/stackkits/services/immich", Default: true,
		},
		{
			Key: "dockge", Name: "dockge", ToolName: "dockge", ModuleSlug: "dockge",
			DisplayName: "Dockge", Description: "Docker Compose stack manager.",
			LocalSlug: "dockge", PublicSlug: "dockge",
			IdentityPolicy: IdentityPolicyForwardAuth, OwnerProvisioningPolicy: OwnerProvisioningRequired,
			Icon: "&#128230;", Badge: "L2 \u00b7 Compose Manager", Section: "Platform", Order: 42, EnableVar: "enable_dockge", GuideURL: "https://docs.kombify.io/guides/stackkits/services/dockge",
		},
		{
			Key: "coolify", Name: "coolify", ToolName: "coolify", ModuleSlug: "coolify",
			DisplayName: "Coolify", Description: "Self-hosted deployment platform.",
			LocalSlug: "coolify", PublicSlug: "coolify",
			IdentityPolicy: IdentityPolicyForwardAuth, OwnerProvisioningPolicy: OwnerProvisioningRequired,
			Icon: "&#128171;", Badge: "L2 \u00b7 PaaS", Section: "Platform", Order: 41, EnableVar: "enable_coolify", GuideURL: "https://docs.kombify.io/guides/stackkits/services/coolify",
		},
		{
			Key: "point", Name: "point", ToolName: "kombify-point", ModuleSlug: "kombify-point",
			DisplayName: "kombify Point DNS", Description: "Local LAN DNS resolver for home service names.",
			LocalSlug: "point", PublicSlug: "point",
			IdentityPolicy: IdentityPolicyNone, OwnerProvisioningPolicy: OwnerProvisioningNone,
			Icon: "&#127760;", Badge: "L1 \u00b7 DNS", Section: "Platform", Order: 35, EnableVar: "enable_kombify_point", GuideURL: "https://docs.kombify.io/guides/stackkits/services/kombify-point",
		},
	}
	for i := range services {
		normalize(&services[i])
	}
	return services
}

// FromCUE overlays CUE module dashboard metadata onto the canonical service
// identity. CUE remains authoritative for behavior; this package normalizes
// product-facing names and route slugs.
func FromCUE(entries []cueval.CatalogEntry) []Service {
	services := Default()
	byKey := map[string]int{}
	for i := range services {
		byKey[services[i].Key] = i
	}

	for _, entry := range entries {
		key := canonicalKey(entry.Key)
		idx, ok := byKey[key]
		if !ok {
			toolName := first(entry.ToolName, key)
			moduleSlug := first(entry.ModuleSlug, toolName)
			svc := Service{
				Key:                     key,
				Name:                    key,
				ToolName:                toolName,
				ModuleSlug:              moduleSlug,
				DisplayName:             first(entry.DisplayName, key),
				Description:             entry.Description,
				LocalSlug:               first(entry.Nested, key),
				PublicSlug:              first(entry.Flat, first(entry.Nested, key)),
				IdentityPolicy:          IdentityPolicyForwardAuth,
				OwnerProvisioningPolicy: OwnerProvisioningRequired,
				Icon:                    entry.Icon,
				Badge:                   entry.Badge,
				Section:                 entry.Section,
				Order:                   entry.Order,
				EnableVar:               entry.EnableVar,
				GuideURL:                entry.GuideURL,
			}
			normalize(&svc)
			byKey[key] = len(services)
			services = append(services, svc)
			continue
		}
		overlayFromCUE(&services[idx], entry)
	}

	sort.SliceStable(services, func(i, j int) bool {
		if services[i].Section != services[j].Section {
			return services[i].Section == "Platform"
		}
		if services[i].Order != services[j].Order {
			return services[i].Order < services[j].Order
		}
		return services[i].Key < services[j].Key
	})
	return services
}

// FromRegistry converts the Admin registry service projection into the
// generator/catalog shape. Empty registry data returns nil so callers can
// cleanly fall back to CUE/defaults.
func FromRegistry(entries []registry.Service) []Service {
	if len(entries) == 0 {
		return nil
	}
	services := make([]Service, 0, len(entries))
	for _, entry := range entries {
		svc := Service{
			Key:                     entry.Key,
			Name:                    entry.Key,
			DisplayName:             entry.DisplayName,
			Description:             entry.Description,
			ToolName:                entry.ToolName,
			ModuleSlug:              entry.ModuleSlug,
			LocalSlug:               entry.LocalSlug,
			PublicSlug:              entry.PublicSlug,
			LegacyAliases:           append([]string(nil), entry.LegacyAliases...),
			IdentityPolicy:          entry.IdentityPolicy,
			OwnerProvisioningPolicy: entry.OwnerProvisioningPolicy,
			Icon:                    entry.Icon,
			Badge:                   entry.Badge,
			Section:                 entry.Section,
			Order:                   entry.Order,
			EnableVar:               entry.EnableVar,
			GuideURL:                entry.GuideURL,
			Default:                 entry.Default,
		}
		normalize(&svc)
		services = append(services, svc)
	}
	sort.SliceStable(services, func(i, j int) bool {
		if services[i].Section != services[j].Section {
			return services[i].Section == "Platform"
		}
		if services[i].Order != services[j].Order {
			return services[i].Order < services[j].Order
		}
		return services[i].Key < services[j].Key
	})
	return services
}

func overlayFromCUE(svc *Service, entry cueval.CatalogEntry) {
	if entry.DisplayName != "" {
		svc.DisplayName = entry.DisplayName
	}
	if entry.Description != "" {
		svc.Description = entry.Description
	}
	if entry.Icon != "" {
		svc.Icon = entry.Icon
	}
	if entry.Badge != "" {
		svc.Badge = entry.Badge
	}
	if entry.Section != "" {
		svc.Section = entry.Section
	}
	if entry.Order != 0 {
		svc.Order = entry.Order
	}
	if entry.EnableVar != "" {
		svc.EnableVar = entry.EnableVar
	}
	if entry.GuideURL != "" {
		svc.GuideURL = entry.GuideURL
	}
	normalize(svc)
}

// WithDefaultFallbacks merges default catalog metadata into a partial catalog
// and appends default services that are missing entirely.
func WithDefaultFallbacks(services []Service) []Service {
	if len(services) == 0 {
		return services
	}
	defaults := Default()
	byKey := make(map[string]Service, len(defaults))
	for _, svc := range defaults {
		byKey[svc.Key] = svc
	}
	seen := make(map[string]bool, len(services))
	out := append([]Service(nil), services...)
	for i := range out {
		key := canonicalKey(out[i].Key)
		seen[key] = true
		if fallback, ok := byKey[key]; ok {
			mergeMissingDefaultFields(&out[i], fallback)
		}
		normalize(&out[i])
	}
	for _, fallback := range defaults {
		if !seen[fallback.Key] {
			out = append(out, fallback)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Section != out[j].Section {
			return out[i].Section == "Platform"
		}
		if out[i].Order != out[j].Order {
			return out[i].Order < out[j].Order
		}
		return out[i].Key < out[j].Key
	})
	return out
}

func mergeMissingDefaultFields(svc *Service, fallback Service) {
	if svc.GuideURL == "" {
		svc.GuideURL = fallback.GuideURL
	}
}

func canonicalKey(key string) string {
	switch key {
	case "dashboard":
		return "base"
	case "pocketid":
		return "id"
	default:
		return key
	}
}

func normalize(svc *Service) {
	if canonicalKey(svc.Key) == "point" && svc.DisplayName == "Kombify Point DNS" {
		svc.DisplayName = "kombify Point DNS"
	}
	if svc.Name == "" {
		svc.Name = svc.Key
	}
	if svc.ToolName == "" {
		svc.ToolName = svc.Key
	}
	if svc.ModuleSlug == "" {
		svc.ModuleSlug = svc.ToolName
	}
	if svc.LocalSlug == "" {
		svc.LocalSlug = svc.Key
	}
	if svc.PublicSlug == "" {
		svc.PublicSlug = svc.LocalSlug
	}
	svc.Nested = svc.LocalSlug
	svc.Flat = svc.PublicSlug
}

func first(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
