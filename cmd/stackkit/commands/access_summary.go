package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/config"
	"github.com/kombifyio/stackkits/internal/servicecatalog"
	"github.com/kombifyio/stackkits/pkg/models"
)

type accessSummary struct {
	StackKit        string          `json:"stackkit"`
	Mode            string          `json:"mode"`
	Domain          string          `json:"domain"`
	SubdomainPrefix string          `json:"subdomainPrefix,omitempty"`
	HubURL          string          `json:"hubUrl"`
	Services        []accessService `json:"services"`
	GeneratedAt     time.Time       `json:"generatedAt"`
}

type accessService struct {
	Key           string   `json:"key"`
	Name          string   `json:"name"`
	DisplayName   string   `json:"displayName"`
	ToolName      string   `json:"toolName"`
	ModuleSlug    string   `json:"moduleSlug"`
	RouteSlug     string   `json:"routeSlug"`
	Section       string   `json:"section,omitempty"`
	URL           string   `json:"url"`
	Host          string   `json:"host"`
	Status        string   `json:"status,omitempty"`
	LegacyAliases []string `json:"legacyAliases,omitempty"`
}

func buildAccessSummary(wd string, spec *models.StackSpec) (*accessSummary, error) {
	if spec == nil {
		return nil, fmt.Errorf("stack spec is nil")
	}

	tfvars, err := loadGeneratedTFVars(wd)
	if err != nil {
		return nil, err
	}

	catalog := loadCanonicalServiceCatalog(wd, spec)
	return buildAccessSummaryFromInputs(spec, tfvars, catalog), nil
}

func loadGeneratedTFVars(wd string) (map[string]any, error) {
	path := filepath.Join(wd, config.GetDeployDir(), "terraform.tfvars.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, nil
		}
		return nil, fmt.Errorf("read terraform.tfvars.json: %w", err)
	}

	var tfvars map[string]any
	if err := json.Unmarshal(data, &tfvars); err != nil {
		return nil, fmt.Errorf("parse terraform.tfvars.json: %w", err)
	}
	return tfvars, nil
}

func buildAccessSummaryFromInputs(spec *models.StackSpec, tfvars map[string]any, catalog []servicecatalog.Service) *accessSummary {
	domain := stringInput(tfvars, "domain", spec.Domain)
	if domain == "" {
		domain = models.DomainHomeLab
	}
	prefix := stringInput(tfvars, "subdomain_prefix", spec.SubdomainPrefix)

	proto := "http"
	if boolInput(tfvars, "enable_https", false) || models.IsKombifyMeDomain(domain) {
		proto = "https"
	}

	summary := &accessSummary{
		StackKit:        spec.StackKit,
		Mode:            spec.Mode,
		Domain:          domain,
		SubdomainPrefix: prefix,
		GeneratedAt:     time.Now().UTC(),
	}

	seen := map[string]bool{}
	for _, entry := range catalog {
		if entry.Key == "" || seen[entry.Key] || !entryEnabled(entry, tfvars) {
			continue
		}
		seen[entry.Key] = true

		host := hostForEntry(entry, domain, prefix)
		if host == "" {
			continue
		}
		display := entry.DisplayName
		if display == "" {
			display = entry.Key
		}
		name := entry.Name
		if name == "" {
			name = entry.Key
		}
		routeSlug := routeSlugForEntry(entry, prefix)

		svc := accessService{
			Key:           entry.Key,
			Name:          name,
			DisplayName:   display,
			ToolName:      entry.ToolName,
			ModuleSlug:    entry.ModuleSlug,
			RouteSlug:     routeSlug,
			Section:       entry.Section,
			Host:          host,
			URL:           proto + "://" + host,
			Status:        string(models.ServiceStatusRunning),
			LegacyAliases: append([]string(nil), entry.LegacyAliases...),
		}
		summary.Services = append(summary.Services, svc)
		if entry.Key == "base" {
			summary.HubURL = svc.URL
		}
	}

	return summary
}

func entryEnabled(entry servicecatalog.Service, tfvars map[string]any) bool {
	enableVar := entry.EnableVar
	if enableVar == "" {
		enableVar = defaultEnableVar(entry.Key)
	}
	if enableVar == "" {
		return entry.Default
	}
	return boolInput(tfvars, enableVar, entry.Default)
}

func defaultEnableVar(key string) string {
	switch key {
	case "base", "dashboard":
		return "enable_dashboard"
	case "home", "homepage":
		return "enable_homepage"
	case "traefik":
		return "enable_traefik"
	case "auth":
		return "enable_tinyauth"
	case "id", "pocketid":
		return "enable_pocketid"
	case "dokploy":
		return "enable_dokploy"
	case "dockge":
		return "enable_dockge"
	case "coolify":
		return "enable_coolify"
	case "komodo":
		return "enable_komodo"
	case "kuma":
		return "enable_uptime_kuma"
	case "whoami":
		return "enable_whoami"
	case "vault":
		return "enable_vaultwarden"
	case "media":
		return "enable_jellyfin"
	case "photos":
		return "enable_immich"
	default:
		return ""
	}
}

func hostForEntry(entry servicecatalog.Service, domain, prefix string) string {
	if prefix != "" {
		flat := entry.PublicSlug
		if flat == "" {
			flat = entry.Key
		}
		return prefix + "-" + flat + "." + domain
	}

	nested := entry.LocalSlug
	if nested == "" {
		nested = entry.Key
	}
	return nested + "." + domain
}

func routeSlugForEntry(entry servicecatalog.Service, prefix string) string {
	if prefix != "" {
		if entry.PublicSlug != "" {
			return entry.PublicSlug
		}
		return entry.Key
	}
	if entry.LocalSlug != "" {
		return entry.LocalSlug
	}
	return entry.Key
}

func writeAccessSummary(wd string, summary *accessSummary) error {
	if summary == nil {
		return nil
	}
	dir := filepath.Join(wd, ".stackkit")
	if err := os.MkdirAll(dir, 0750); err != nil {
		return fmt.Errorf("create .stackkit directory: %w", err)
	}

	data, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal access summary: %w", err)
	}
	data = append(data, '\n')

	path := filepath.Join(dir, "access.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("write access summary: %w", err)
	}
	return nil
}

func serviceStatesFromAccessSummary(summary *accessSummary) []models.ServiceState {
	if summary == nil {
		return nil
	}
	services := make([]models.ServiceState, 0, len(summary.Services))
	for _, svc := range summary.Services {
		services = append(services, models.ServiceState{
			Name:   svc.Key,
			URL:    svc.URL,
			Status: models.ServiceStatusRunning,
			Health: models.HealthStatusUnknown,
		})
	}
	return services
}

func urlAliases(summary *accessSummary) map[string]string {
	aliases := map[string]string{}
	if summary == nil {
		return aliases
	}
	for _, svc := range summary.Services {
		candidates := []string{svc.Key, svc.Name, svc.RouteSlug, svc.ToolName, svc.ModuleSlug, firstHostLabel(svc.Host)}
		candidates = append(candidates, svc.LegacyAliases...)
		for _, alias := range candidates {
			alias = strings.ToLower(strings.TrimSpace(alias))
			if alias != "" {
				aliases[alias] = svc.URL
			}
		}
	}
	return aliases
}

func firstHostLabel(host string) string {
	if idx := strings.IndexByte(host, '.'); idx > 0 {
		return host[:idx]
	}
	return host
}

func stringInput(values map[string]any, key, fallback string) string {
	if values == nil {
		return fallback
	}
	if value, ok := values[key].(string); ok && strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return fallback
}

func boolInput(values map[string]any, key string, fallback bool) bool {
	if values == nil {
		return fallback
	}
	if value, ok := values[key].(bool); ok {
		return value
	}
	return fallback
}
