// Package cue provides CUE schema validation and Terraform bridge for StackKits.
//
// CatalogEntry drives domain computation and dashboard generation.
// Data is extracted from module CUE contracts (#ServiceDefinition.subdomain + .dashboard).
// Services without module contracts (coolify, dockge) are included as fallbacks.
package cue

import (
	"fmt"
	"sort"
	"strings"
)

// CatalogEntry describes a service for domain computation and dashboard generation.
// Fields correspond to #ServiceDefinition.subdomain and #ServiceDefinition.dashboard in CUE.
type CatalogEntry struct {
	// Key in Terraform local.domains map (e.g., "dashboard", "auth")
	Key string
	// Subdomain for own-domain mode: {Nested}.{domain}
	Nested string
	// Subdomain for flat/kombify.me mode: {prefix}-{Flat}.{domain}
	Flat string
	// Human-readable service name
	DisplayName string
	// Short description for dashboard card
	Description string
	// HTML entity for dashboard icon (e.g., "&#128100;")
	Icon string
	// Layer badge label (e.g., "L1 · IdP")
	Badge string
	// Dashboard section: "Platform" or "Applications"
	Section string
	// Display order within section (lower = first)
	Order int
	// Terraform enable variable name. Empty = always shown.
	EnableVar string
}

// ServiceCatalogFromModules reads all module contracts and builds the service catalog.
// This replaces the old hardcoded ServiceCatalog() function.
func ServiceCatalogFromModules(modulesDir string) ([]CatalogEntry, error) {
	reader := NewModuleReader()
	contracts, err := reader.ReadAllModules(modulesDir)
	if err != nil {
		return nil, fmt.Errorf("read module contracts: %w", err)
	}

	var catalog []CatalogEntry
	for _, mc := range contracts {
		for _, svc := range mc.Services {
			// Only include services with dashboard metadata
			if svc.DashboardIcon == "" {
				continue
			}
			catalog = append(catalog, catalogEntryForService(mc, svc))
		}
	}

	// Add fallback entries for services without module contracts
	catalog = append(catalog, fallbackCatalogEntries()...)
	catalog = dedupeCatalogEntries(catalog)

	// Sort by section (Platform first) then by order
	sort.Slice(catalog, func(i, j int) bool {
		if catalog[i].Section != catalog[j].Section {
			return catalog[i].Section == "Platform"
		}
		return catalog[i].Order < catalog[j].Order
	})

	return catalog, nil
}

// DomainEntriesFromModules returns ALL services that need a local.domains entry,
// including services that don't appear in the dashboard (e.g., dashboard, dozzle).
func DomainEntriesFromModules(modulesDir string) ([]CatalogEntry, error) {
	reader := NewModuleReader()
	contracts, err := reader.ReadAllModules(modulesDir)
	if err != nil {
		return nil, fmt.Errorf("read module contracts: %w", err)
	}

	var entries []CatalogEntry
	for _, mc := range contracts {
		for _, svc := range mc.Services {
			// Include any service with subdomain routing
			if svc.SubdomainKey == "" {
				continue
			}
			entries = append(entries, catalogEntryForService(mc, svc))
		}
	}

	// Add fallback entries for services without module contracts
	entries = append(entries, fallbackCatalogEntries()...)
	entries = dedupeCatalogEntries(entries)

	return entries, nil
}

func dedupeCatalogEntries(entries []CatalogEntry) []CatalogEntry {
	seen := map[string]bool{}
	out := make([]CatalogEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.Key == "" || seen[entry.Key] {
			continue
		}
		seen[entry.Key] = true
		out = append(out, entry)
	}
	return out
}

func catalogEntryForService(mc ModuleContract, svc ServiceDef) CatalogEntry {
	return CatalogEntry{
		Key:         svc.SubdomainKey,
		Nested:      svc.SubdomainNested,
		Flat:        svc.SubdomainFlat,
		DisplayName: firstNonEmpty(svc.DisplayName, mc.Metadata.DisplayName, humanizeServiceName(svc.Name), humanizeServiceName(svc.SubdomainKey)),
		Description: firstNonEmpty(svc.Description, mc.Metadata.Description, svc.OutputDesc),
		Icon:        svc.DashboardIcon,
		Badge:       svc.DashboardBadge,
		Section:     svc.DashboardSection,
		Order:       svc.DashboardOrder,
		EnableVar:   svc.DashboardEnableVar,
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func humanizeServiceName(value string) string {
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == '-' || r == '_' || r == '.'
	})
	if len(parts) == 0 {
		return ""
	}
	for i, part := range parts {
		if len(part) == 0 {
			continue
		}
		parts[i] = strings.ToUpper(part[:1]) + part[1:]
	}
	return strings.Join(parts, " ")
}

// fallbackCatalogEntries provides catalog entries for services that don't have
// module CUE contracts yet. Remove entries here as modules are created.
func fallbackCatalogEntries() []CatalogEntry {
	return []CatalogEntry{
		{
			Key: "coolify", Nested: "coolify", Flat: "coolify",
			DisplayName: "Coolify",
			Description: "Self-hosted Heroku/Vercel alternative with Git deployment and auto-HTTPS.",
			Icon:        "&#128171;", Badge: "L2 \u00b7 PaaS", Section: "Platform", Order: 41,
			EnableVar: "enable_coolify",
		},
		{
			Key: "dockge", Nested: "dockge", Flat: "dockge",
			DisplayName: "Dockge",
			Description: "Lightweight Docker Compose manager. Create and manage compose stacks with a simple UI.",
			Icon:        "&#128230;", Badge: "L2 \u00b7 Compose Manager", Section: "Platform", Order: 42,
			EnableVar: "enable_dockge",
		},
		{
			Key: "point", Nested: "point", Flat: "point",
			DisplayName: "Kombify Point DNS",
			Description: "Local LAN DNS resolver for readable home service names.",
			Icon:        "&#127760;", Badge: "L1 \u00b7 DNS", Section: "Platform", Order: 35,
			EnableVar: "enable_kombify_point",
		},
	}
}

// ServiceCatalog returns the canonical service catalog.
// Deprecated: Use ServiceCatalogFromModules for CUE-driven catalog.
// This remains as a fallback if module loading fails.
func ServiceCatalog() []CatalogEntry {
	return []CatalogEntry{
		// === Platform section ===
		{
			Key: "pocketid", Nested: "id", Flat: "id",
			DisplayName: "Pocket ID",
			Description: "OIDC identity provider with passkey authentication. Manage users and SSO clients.",
			Icon:        "&#128100;", Badge: "L1 &middot; IdP", Section: "Platform", Order: 10,
		},
		{
			Key: "auth", Nested: "auth", Flat: "tinyauth",
			DisplayName: "TinyAuth",
			Description: "ForwardAuth gateway. Protects all services via TinyAuth middleware backed by Pocket ID.",
			Icon:        "&#128274;", Badge: "L1 &middot; ForwardAuth", Section: "Platform", Order: 20,
		},
		{
			Key: "traefik", Nested: "traefik", Flat: "traefik",
			DisplayName: "Traefik",
			Description: "Routes all traffic across services. View active routes, middlewares, and upstreams.",
			Icon:        "&#9889;", Badge: "L2 &middot; Reverse Proxy", Section: "Platform", Order: 30,
		},
		{
			Key: "dokploy", Nested: "dokploy", Flat: "dokploy",
			DisplayName: "Dokploy",
			Description: "Deploy and manage applications. Your self-hosted Heroku for services and compose stacks.",
			Icon:        "&#128640;", Badge: "L2 &middot; PaaS", Section: "Platform", Order: 40,
			EnableVar: "enable_dokploy",
		},
		{
			Key: "point", Nested: "point", Flat: "point",
			DisplayName: "Kombify Point DNS",
			Description: "Local LAN DNS resolver for readable home service names.",
			Icon:        "&#127760;", Badge: "L1 &middot; DNS", Section: "Platform", Order: 35,
			EnableVar: "enable_kombify_point",
		},
		{
			Key: "coolify", Nested: "coolify", Flat: "coolify",
			DisplayName: "Coolify",
			Description: "Self-hosted Heroku/Vercel alternative with Git deployment and auto-HTTPS.",
			Icon:        "&#128171;", Badge: "L2 &middot; PaaS", Section: "Platform", Order: 41,
			EnableVar: "enable_coolify",
		},
		{
			Key: "dockge", Nested: "dockge", Flat: "dockge",
			DisplayName: "Dockge",
			Description: "Lightweight Docker Compose manager. Create and manage compose stacks with a simple UI.",
			Icon:        "&#128230;", Badge: "L2 &middot; Compose Manager", Section: "Platform", Order: 42,
			EnableVar: "enable_dockge",
		},

		// === Applications section ===
		{
			Key: "kuma", Nested: "kuma", Flat: "kuma",
			DisplayName: "Uptime Kuma",
			Description: "Service uptime monitoring and status pages for all homelab services.",
			Icon:        "&#128202;", Badge: "L3 &middot; Monitoring", Section: "Applications", Order: 10,
		},
		{
			Key: "whoami", Nested: "whoami", Flat: "whoami",
			DisplayName: "Whoami",
			Description: "HTTP echo service for verifying Traefik routing, TinyAuth middleware, and headers.",
			Icon:        "&#129302;", Badge: "L3 &middot; Test", Section: "Applications", Order: 20,
		},
		{
			Key: "vault", Nested: "vault", Flat: "vault",
			DisplayName: "Vaultwarden",
			Description: "Self-hosted password manager. Bitwarden-compatible vault for passwords, TOTP, and secure notes.",
			Icon:        "&#128272;", Badge: "L3 &middot; Vault", Section: "Applications", Order: 30,
			EnableVar: "enable_vaultwarden",
		},
		{
			Key: "media", Nested: "media", Flat: "media",
			DisplayName: "Jellyfin",
			Description: "Free media server for movies, TV, music, and photos. Stream to any device on your network.",
			Icon:        "&#127916;", Badge: "L3 &middot; Media", Section: "Applications", Order: 40,
			EnableVar: "enable_jellyfin",
		},
		{
			Key: "photos", Nested: "photos", Flat: "photos",
			DisplayName: "Immich",
			Description: "Self-hosted photo and video management with AI-powered search, facial recognition, and mobile backup.",
			Icon:        "&#128247;", Badge: "L3 &middot; Photos", Section: "Applications", Order: 50,
			EnableVar: "enable_immich",
		},
	}
}

// DomainEntries returns ALL services that need a local.domains entry,
// including services that don't appear in the dashboard (e.g., "dashboard" itself).
// Deprecated: Use DomainEntriesFromModules for CUE-driven domains.
func DomainEntries() []CatalogEntry {
	// Dashboard container: not a dashboard card, but needs a domain entry
	dashboard := CatalogEntry{
		Key: "dashboard", Nested: "base", Flat: "dash",
	}

	catalog := ServiceCatalog()
	entries := make([]CatalogEntry, 0, len(catalog)+1)
	entries = append(entries, dashboard)
	entries = append(entries, catalog...)
	return entries
}
