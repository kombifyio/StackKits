package commands

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/servicecatalog"
	"github.com/kombifyio/stackkits/pkg/models"
)

type architectureV2AccessPlan struct {
	StackID string `json:"stackId"`
	Network struct {
		Configuration struct {
			Domain struct {
				Base string `json:"base"`
			} `json:"domain"`
			TLS struct {
				DefaultMode string `json:"defaultMode"`
			} `json:"tls"`
		} `json:"configuration"`
	} `json:"network"`
	Modules []struct {
		RenderUnits []struct {
			ServiceEndpoints []struct {
				ServiceRef string `json:"serviceRef"`
			} `json:"serviceEndpoints"`
		} `json:"renderUnits"`
	} `json:"modules"`
}

func buildArchitectureV2AccessSummary(plan generationartifact.VerifiedPlan, appliedAt time.Time) (*accessSummary, error) {
	return buildArchitectureV2AccessSummaryFromCanonical(plan.Canonical(), appliedAt)
}

func buildArchitectureV2AccessSummaryFromCanonical(canonical []byte, appliedAt time.Time) (*accessSummary, error) {
	var projection architectureV2AccessPlan
	if err := json.Unmarshal(canonical, &projection); err != nil {
		return nil, fmt.Errorf("decode verified Architecture v2 access projection: %w", err)
	}
	projection.StackID = strings.TrimSpace(projection.StackID)
	domain := strings.TrimSpace(projection.Network.Configuration.Domain.Base)
	if projection.StackID == "" || domain == "" {
		return nil, fmt.Errorf("verified Architecture v2 plan is missing stackId or network.configuration.domain.base")
	}
	if appliedAt.IsZero() {
		return nil, fmt.Errorf("verified Architecture v2 Apply result is missing appliedAt")
	}

	exposed := map[string]struct{}{}
	for _, module := range projection.Modules {
		for _, unit := range module.RenderUnits {
			for _, endpoint := range unit.ServiceEndpoints {
				key := architectureV2AccessServiceKey(endpoint.ServiceRef)
				if key != "" {
					exposed[key] = struct{}{}
				}
			}
		}
	}
	if len(exposed) == 0 {
		return nil, fmt.Errorf("verified Architecture v2 plan exposes no service endpoints")
	}

	protocol := "http"
	if projection.Network.Configuration.TLS.DefaultMode == "public" || models.IsKombifyMeDomain(domain) {
		protocol = "https"
	}
	summary := &accessSummary{
		StackKit: projection.StackID, Mode: "native-v2", Domain: domain,
		GeneratedAt: appliedAt.UTC(),
	}

	catalog := servicecatalog.Default()
	known := map[string]struct{}{}
	for _, entry := range catalog {
		if _, ok := exposed[entry.Key]; !ok {
			continue
		}
		known[entry.Key] = struct{}{}
		summary.Services = append(summary.Services, architectureV2AccessService(entry, protocol, domain))
	}
	unknown := make([]string, 0)
	for key := range exposed {
		if _, ok := known[key]; !ok {
			unknown = append(unknown, key)
		}
	}
	sort.Strings(unknown)
	for _, key := range unknown {
		entry := servicecatalog.Service{Key: key, Name: key, DisplayName: key, ToolName: key, ModuleSlug: key, LocalSlug: key, PublicSlug: key}
		summary.Services = append(summary.Services, architectureV2AccessService(entry, protocol, domain))
	}
	for _, service := range summary.Services {
		if service.Key == "base" {
			summary.HubURL = service.URL
			break
		}
	}
	return summary, nil
}

func architectureV2AccessServiceKey(serviceRef string) string {
	switch strings.TrimSpace(serviceRef) {
	case "basement-hub", "dashboard":
		return "base"
	default:
		return strings.TrimSpace(serviceRef)
	}
}

func architectureV2AccessService(entry servicecatalog.Service, protocol, domain string) accessService {
	host := entry.LocalSlug + "." + domain
	return accessService{
		Key: entry.Key, Name: entry.Name, DisplayName: entry.DisplayName,
		ToolName: entry.ToolName, ModuleSlug: entry.ModuleSlug, RouteSlug: entry.LocalSlug,
		Section: entry.Section, URL: protocol + "://" + host, Host: host,
		Status: string(models.ServiceStatusRunning), LegacyAliases: append([]string(nil), entry.LegacyAliases...),
	}
}
