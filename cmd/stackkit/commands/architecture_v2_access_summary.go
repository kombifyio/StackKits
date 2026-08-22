package commands

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/servicecatalog"
	"github.com/kombifyio/stackkits/pkg/models"
)

const architectureV2AccessSchemaVersion = "stackkit.access-manifest/v2"

var architectureV2AccessDigestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

type architectureV2AccessBinding struct {
	PlanHash, ApplyResultHash string
	AppliedAt                 time.Time
}

type architectureV2AccessPlan struct {
	StackID string `json:"stackId"`
	Kit     struct {
		Slug    string `json:"slug"`
		Version string `json:"version"`
	} `json:"kit"`
	Network struct {
		Configuration struct {
			Domain struct {
				Base string `json:"base"`
			} `json:"domain"`
			TLS struct {
				DefaultMode string `json:"defaultMode"`
			} `json:"tls"`
		} `json:"configuration"`
		Routes []architectureV2AccessRoute `json:"routes"`
	} `json:"network"`
	Modules []architectureV2AccessModule `json:"modules"`
}

type architectureV2AccessModule struct {
	ID              string `json:"id"`
	ServiceControls []struct {
		Key            string   `json:"key"`
		ServiceRef     string   `json:"serviceRef"`
		Adapter        string   `json:"adapter"`
		RuntimeRef     string   `json:"runtimeRef"`
		ComponentRefs  []string `json:"componentRefs"`
		AllowedActions []string `json:"allowedActions"`
		Critical       bool     `json:"critical"`
	} `json:"serviceControls"`
	RenderUnits []struct {
		ServiceEndpoints []struct {
			ServiceRef string `json:"serviceRef"`
		} `json:"serviceEndpoints"`
	} `json:"renderUnits"`
}

type architectureV2AccessRoute struct {
	ID, ModuleRef, ServiceRef, Exposure, Protocol, Host, Path string
	Access                                                    struct {
		PolicyRef     string `json:"policyRef"`
		DefaultClosed bool   `json:"defaultClosed"`
	} `json:"access"`
	TLS struct {
		Required bool   `json:"required"`
		Mode     string `json:"mode"`
	} `json:"tls"`
	CapabilityRealizations []json.RawMessage `json:"capabilityRealizations"`
}

func buildArchitectureV2AccessSummary(plan generationartifact.VerifiedPlan, apply architecturev2.ApplyResultSummary) (*accessSummary, error) {
	return buildArchitectureV2AccessSummaryFromCanonical(plan.Canonical(), architectureV2AccessBinding{
		PlanHash: plan.Binding().PlanHash, ApplyResultHash: apply.ResultHash, AppliedAt: apply.AppliedAt,
	})
}

func buildArchitectureV2AccessSummaryFromCanonical(canonical []byte, binding architectureV2AccessBinding) (*accessSummary, error) {
	var projection architectureV2AccessPlan
	if err := json.Unmarshal(canonical, &projection); err != nil {
		return nil, fmt.Errorf("decode verified Architecture v2 access projection: %w", err)
	}
	projection.StackID = strings.TrimSpace(projection.StackID)
	projection.Kit.Slug = strings.TrimSpace(projection.Kit.Slug)
	projection.Kit.Version = strings.TrimSpace(projection.Kit.Version)
	domain := strings.TrimSpace(projection.Network.Configuration.Domain.Base)
	if projection.StackID == "" || projection.Kit.Slug == "" || projection.Kit.Version == "" || domain == "" {
		return nil, fmt.Errorf("verified Architecture v2 plan is missing stackId, kit identity, or network.configuration.domain.base")
	}
	if !architectureV2AccessDigestPattern.MatchString(binding.PlanHash) || !architectureV2AccessDigestPattern.MatchString(binding.ApplyResultHash) || binding.AppliedAt.IsZero() {
		return nil, fmt.Errorf("verified Architecture v2 access projection requires exact plan and Apply result identity")
	}

	exposed := map[string]struct{}{}
	endpointRefs := map[string]struct{}{}
	for _, module := range projection.Modules {
		for _, unit := range module.RenderUnits {
			for _, endpoint := range unit.ServiceEndpoints {
				key := architectureV2AccessServiceKey(endpoint.ServiceRef)
				if key != "" {
					exposed[key] = struct{}{}
					endpointRefs[module.ID+"\x00"+endpoint.ServiceRef] = struct{}{}
				}
			}
		}
	}
	routes := map[string]architectureV2AccessRoute{}
	if projection.Kit.Slug == "cloud-kit" {
		exposed = map[string]struct{}{}
		for _, route := range projection.Network.Routes {
			key := architectureV2AccessServiceKey(route.ServiceRef)
			if key == "" || route.ID == "" || route.Exposure != "public" || route.Protocol != "https" || route.Host == "" ||
				route.Access.PolicyRef == "" || !route.Access.DefaultClosed || !route.TLS.Required || route.TLS.Mode != "terminate-at-edge" ||
				len(route.CapabilityRealizations) == 0 {
				return nil, fmt.Errorf("verified Cloud access route %q is not an exact default-closed public HTTPS route", route.ID)
			}
			if _, ok := endpointRefs[route.ModuleRef+"\x00"+route.ServiceRef]; !ok {
				return nil, fmt.Errorf("verified Cloud access route %q has no exact service endpoint", route.ID)
			}
			parsed, err := url.Parse(route.Protocol + "://" + route.Host + route.Path)
			if err != nil || parsed.User != nil || parsed.Hostname() == "" || !strings.HasSuffix(parsed.Hostname(), "."+domain) {
				return nil, fmt.Errorf("verified Cloud access route %q has no exact plan-domain URL", route.ID)
			}
			if _, duplicate := routes[key]; duplicate {
				return nil, fmt.Errorf("verified Cloud access service %q has multiple public routes", key)
			}
			exposed[key], routes[key] = struct{}{}, route
		}
	}
	if len(exposed) == 0 {
		return nil, fmt.Errorf("verified Architecture v2 plan exposes no service endpoints")
	}
	controls, err := architectureV2AccessServiceControls(projection.Modules)
	if err != nil {
		return nil, err
	}

	protocol := "http"
	if projection.Network.Configuration.TLS.DefaultMode == "public" || models.IsKombifyMeDomain(domain) {
		protocol = "https"
	}
	summary := &accessSummary{
		SchemaVersion: architectureV2AccessSchemaVersion, StackID: projection.StackID,
		PlanHash: binding.PlanHash, ApplyResultHash: binding.ApplyResultHash,
		StackKit: projection.Kit.Slug, StackKitVersion: projection.Kit.Version, Mode: "native-v2", Domain: domain,
		GeneratedAt: binding.AppliedAt.UTC(),
	}

	catalog := servicecatalog.Default()
	known := map[string]struct{}{}
	for _, entry := range catalog {
		if _, ok := exposed[entry.Key]; !ok {
			continue
		}
		known[entry.Key] = struct{}{}
		service := architectureV2AccessService(entry, protocol, domain)
		service.AllowedActions = append([]string(nil), controls[entry.Key]...)
		if route, ok := routes[entry.Key]; ok {
			service = architectureV2RoutedAccessService(service, route)
		}
		summary.Services = append(summary.Services, service)
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
		service := architectureV2AccessService(entry, protocol, domain)
		service.AllowedActions = append([]string(nil), controls[key]...)
		if route, ok := routes[key]; ok {
			service = architectureV2RoutedAccessService(service, route)
		}
		summary.Services = append(summary.Services, service)
	}
	for _, service := range summary.Services {
		if service.Key == "base" {
			summary.HubURL = service.URL
			break
		}
	}
	return summary, nil
}

func architectureV2AccessServiceControls(modules []architectureV2AccessModule) (map[string][]string, error) {
	result := map[string][]string{}
	knownActions := map[string]struct{}{"start": {}, "stop": {}, "restart": {}, "logs": {}}
	for _, module := range modules {
		for _, control := range module.ServiceControls {
			key := architectureV2AccessServiceKey(control.Key)
			if key == "" || architectureV2AccessServiceKey(control.ServiceRef) != key ||
				(control.Adapter != "compose" && control.Adapter != "komodo") ||
				strings.TrimSpace(control.RuntimeRef) == "" || len(control.ComponentRefs) == 0 || len(control.AllowedActions) == 0 {
				return nil, fmt.Errorf("verified Architecture v2 plan has an invalid service-control projection")
			}
			if _, duplicate := result[key]; duplicate {
				return nil, fmt.Errorf("verified Architecture v2 plan controls service %q more than once", key)
			}
			seen := map[string]struct{}{}
			for _, action := range control.AllowedActions {
				action = strings.TrimSpace(action)
				if _, known := knownActions[action]; !known {
					return nil, fmt.Errorf("verified Architecture v2 plan has an unsupported service action")
				}
				if _, duplicate := seen[action]; duplicate {
					return nil, fmt.Errorf("verified Architecture v2 plan duplicates a service action")
				}
				if control.Critical && action == "stop" {
					return nil, fmt.Errorf("verified Architecture v2 plan allows stop for critical service %q", key)
				}
				seen[action] = struct{}{}
			}
			result[key] = append([]string(nil), control.AllowedActions...)
		}
	}
	return result, nil
}

func architectureV2RoutedAccessService(service accessService, route architectureV2AccessRoute) accessService {
	service.RouteRef, service.Host = route.ID, route.Host
	service.URL = route.Protocol + "://" + route.Host
	if route.Path != "" && route.Path != "/" {
		service.URL += route.Path
	}
	return service
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
