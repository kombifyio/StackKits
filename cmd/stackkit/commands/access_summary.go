package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/config"
	cueval "github.com/kombifyio/stackkits/internal/cue"
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
	Key         string `json:"key"`
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	Section     string `json:"section,omitempty"`
	URL         string `json:"url"`
	Host        string `json:"host"`
	Status      string `json:"status,omitempty"`
}

func buildAccessSummary(wd string, spec *models.StackSpec) (*accessSummary, error) {
	if spec == nil {
		return nil, fmt.Errorf("stack spec is nil")
	}

	tfvars, err := loadGeneratedTFVars(wd)
	if err != nil {
		return nil, err
	}

	catalog := loadAccessCatalog(wd, spec)
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

func loadAccessCatalog(wd string, spec *models.StackSpec) []cueval.CatalogEntry {
	loader := config.NewLoader(wd)
	stackkitDir, err := loader.FindStackKitDir(spec.StackKit)
	if err != nil {
		parentLoader := config.NewLoader(filepath.Dir(wd))
		stackkitDir, err = parentLoader.FindStackKitDir(spec.StackKit)
	}
	if err != nil {
		return cueval.ServiceCatalog()
	}

	modulesDir := resolveModulesDir(stackkitDir, wd)
	catalog, err := cueval.ServiceCatalogFromModules(modulesDir)
	if err != nil {
		return cueval.ServiceCatalog()
	}
	return catalog
}

func buildAccessSummaryFromInputs(spec *models.StackSpec, tfvars map[string]any, catalog []cueval.CatalogEntry) *accessSummary {
	domain := stringInput(tfvars, "domain", spec.Domain)
	if domain == "" {
		domain = models.DomainHomeLab
	}
	prefix := stringInput(tfvars, "subdomain_prefix", spec.SubdomainPrefix)

	proto := "http"
	if boolInput(tfvars, "enable_https", false) || models.IsKombifyMeDomain(domain) || models.IsLocalDomain(domain) {
		proto = "https"
	}

	entries := make([]cueval.CatalogEntry, 0, len(catalog)+1)
	entries = append(entries, cueval.CatalogEntry{
		Key:         "dashboard",
		Nested:      "base",
		Flat:        "dash",
		DisplayName: "Dashboard",
		Description: "StackKits service hub",
		Section:     "Platform",
		Order:       -1,
		EnableVar:   "enable_dashboard",
	})
	entries = append(entries, catalog...)

	summary := &accessSummary{
		StackKit:        spec.StackKit,
		Mode:            spec.Mode,
		Domain:          domain,
		SubdomainPrefix: prefix,
		GeneratedAt:     time.Now().UTC(),
	}

	seen := map[string]bool{}
	for _, entry := range entries {
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
		name := entry.Key
		if models.IsKombifyMeDomain(domain) && entry.Flat != "" {
			name = entry.Flat
		}

		svc := accessService{
			Key:         entry.Key,
			Name:        name,
			DisplayName: display,
			Section:     entry.Section,
			Host:        host,
			URL:         proto + "://" + host,
			Status:      string(models.ServiceStatusRunning),
		}
		summary.Services = append(summary.Services, svc)
		if entry.Key == "dashboard" {
			summary.HubURL = svc.URL
		}
	}

	return summary
}

func entryEnabled(entry cueval.CatalogEntry, tfvars map[string]any) bool {
	enableVar := entry.EnableVar
	if enableVar == "" {
		enableVar = defaultEnableVar(entry.Key)
	}
	if enableVar == "" {
		return true
	}
	return boolInput(tfvars, enableVar, true)
}

func defaultEnableVar(key string) string {
	switch key {
	case "dashboard":
		return "enable_dashboard"
	case "traefik":
		return "enable_traefik"
	case "auth":
		return "enable_tinyauth"
	case "pocketid":
		return "enable_pocketid"
	case "dokploy":
		return "enable_dokploy"
	case "dockge":
		return "enable_dockge"
	case "coolify":
		return "enable_coolify"
	case "kuma":
		return "enable_uptime_kuma"
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

func hostForEntry(entry cueval.CatalogEntry, domain, prefix string) string {
	if prefix != "" {
		flat := entry.Flat
		if flat == "" {
			flat = entry.Key
		}
		return prefix + "-" + flat + "." + domain
	}

	nested := entry.Nested
	if nested == "" {
		nested = entry.Key
	}
	return nested + "." + domain
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
		for _, alias := range []string{svc.Key, svc.Name, firstHostLabel(svc.Host)} {
			alias = strings.ToLower(strings.TrimSpace(alias))
			if alias != "" {
				aliases[alias] = svc.URL
			}
		}
	}
	aliases["tinyauth"] = aliases["auth"]
	aliases["uptime-kuma"] = aliases["kuma"]
	aliases["dashboard"] = summary.HubURL
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
