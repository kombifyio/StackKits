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
	// Tool/service implementation name from CUE
	ToolName string
	// Module slug that owns this service
	ModuleSlug string
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
	// Public Mintlify guide URL for this service.
	GuideURL string
}

// ServiceCatalogFromModules reads all module contracts and builds the canonical service catalog.
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
		ToolName:    svc.Name,
		ModuleSlug:  mc.Metadata.Name,
		DisplayName: firstNonEmpty(svc.DisplayName, mc.Metadata.DisplayName, humanizeServiceName(svc.Name), humanizeServiceName(svc.SubdomainKey)),
		Description: firstNonEmpty(svc.Description, mc.Metadata.Description, svc.OutputDesc),
		Icon:        svc.DashboardIcon,
		Badge:       svc.DashboardBadge,
		Section:     svc.DashboardSection,
		Order:       svc.DashboardOrder,
		EnableVar:   svc.DashboardEnableVar,
		GuideURL:    svc.DashboardGuideURL,
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
			Icon:        "&#128171;", Badge: "L2 \u00b7 PaaS", Section: "Platform", Order: 42,
			EnableVar: "enable_coolify", GuideURL: "https://docs.kombify.io/guides/stackkits/services/coolify",
		},
		{
			Key: "komodo", Nested: "komodo", Flat: "komodo",
			DisplayName: "Komodo",
			Description: "Programmable self-hosted PaaS for Compose stack deployment through API keys.",
			Icon:        "&#9881;", Badge: "L2 \u00b7 PaaS", Section: "Platform", Order: 41,
			EnableVar: "enable_komodo", GuideURL: "https://docs.kombify.io/guides/stackkits/services/komodo",
		},
		{
			Key: "dockge", Nested: "dockge", Flat: "dockge",
			DisplayName: "Dockge",
			Description: "Lightweight Docker Compose manager. Create and manage compose stacks with a simple UI.",
			Icon:        "&#128230;", Badge: "L2 \u00b7 Compose Manager", Section: "Platform", Order: 43,
			EnableVar: "enable_dockge", GuideURL: "https://docs.kombify.io/guides/stackkits/services/dockge",
		},
		{
			Key: "point", Nested: "point", Flat: "point",
			DisplayName: "kombify Point DNS",
			Description: "Local LAN DNS resolver for readable home service names.",
			Icon:        "&#127760;", Badge: "L1 \u00b7 DNS", Section: "Platform", Order: 35,
			EnableVar: "enable_kombify_point", GuideURL: "https://docs.kombify.io/guides/stackkits/services/kombify-point",
		},
	}
}
