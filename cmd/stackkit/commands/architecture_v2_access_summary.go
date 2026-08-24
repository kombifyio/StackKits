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

const architectureV2AccessSchemaVersion = "stackkit.access-manifest/v3"

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
	ID      string `json:"id"`
	Runtime struct {
		Engine     string                                 `json:"engine"`
		Components []architectureV2AccessRuntimeComponent `json:"components"`
	} `json:"runtime"`
	ServiceControls []architectureV2AccessServiceControl `json:"serviceControls"`
	RenderUnits     []struct {
		ServiceEndpoints []struct {
			ServiceRef string `json:"serviceRef"`
		} `json:"serviceEndpoints"`
	} `json:"renderUnits"`
}

type architectureV2AccessRuntimeComponent struct {
	ID        string `json:"id"`
	Role      string `json:"role"`
	Lifecycle string `json:"lifecycle"`
	Health    struct {
		Kind string `json:"kind"`
		Port int    `json:"port"`
	} `json:"health"`
}

type architectureV2AccessServiceControl struct {
	Key            string   `json:"key"`
	ServiceRef     string   `json:"serviceRef"`
	Adapter        string   `json:"adapter"`
	RuntimeRef     string   `json:"runtimeRef"`
	ComponentRefs  []string `json:"componentRefs"`
	AllowedActions []string `json:"allowedActions"`
	Critical       bool     `json:"critical"`
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
	runtimeServices, err := architectureV2AccessRuntimeServices(projection.Modules, summary.Services)
	if err != nil {
		return nil, err
	}
	summary.RuntimeServices = runtimeServices
	return summary, nil
}

func architectureV2AccessRuntimeServices(modules []architectureV2AccessModule, services []accessService) ([]accessRuntimeService, error) {
	displayNames := map[string]string{}
	toolNames := map[string]string{}
	for _, service := range services {
		displayNames[service.Key] = service.DisplayName
		toolNames[service.Key] = service.ToolName
	}

	result := []accessRuntimeService{}
	seen := map[string]struct{}{}
	for _, module := range modules {
		components := map[string]architectureV2AccessRuntimeComponent{}
		for _, component := range module.Runtime.Components {
			components[component.ID] = component
		}
		unmanagedAdapter, unmanagedRuntimeRef := "observed", module.ID
		if len(module.ServiceControls) > 0 {
			unmanagedAdapter, unmanagedRuntimeRef = module.ServiceControls[0].Adapter, module.ServiceControls[0].RuntimeRef
		}
		controlled := map[string]struct{}{}
		for _, control := range module.ServiceControls {
			applicationKey := architectureV2AccessServiceKey(control.ServiceRef)
			for _, componentRef := range control.ComponentRefs {
				component, ok := components[componentRef]
				if !ok {
					return nil, fmt.Errorf("verified Architecture v2 plan controls unknown runtime component %q", componentRef)
				}
				identityKey := module.ID + "\x00" + component.ID
				if _, duplicate := seen[identityKey]; duplicate {
					return nil, fmt.Errorf("verified Architecture v2 plan controls runtime component %q more than once", component.ID)
				}
				seen[identityKey] = struct{}{}
				controlled[component.ID] = struct{}{}

				lifecycle := strings.ReplaceAll(strings.TrimSpace(component.Lifecycle), "-", "_")
				impact := "supporting"
				if lifecycle == "one_shot" {
					impact = "none"
				} else if control.Critical {
					impact = "critical"
				}
				runtimeService := accessRuntimeService{
					ServiceKey: component.ID, ApplicationKey: applicationKey,
					DisplayName: architectureV2RuntimeDisplayName(displayNames[applicationKey], toolNames[applicationKey], applicationKey, component.ID),
					Role:        component.Role, Lifecycle: lifecycle, OperationalImpact: impact,
				}
				runtimeService.RuntimeIdentity = architectureV2RuntimeIdentity(module.Runtime.Engine, control.Adapter, control.RuntimeRef, component.ID)
				if component.Health.Kind == "http" && component.Health.Port > 0 {
					runtimeService.InternalAddress = fmt.Sprintf("http://%s:%d", component.ID, component.Health.Port)
				}
				result = append(result, runtimeService)
			}
		}
		for _, component := range module.Runtime.Components {
			if _, ok := controlled[component.ID]; ok {
				continue
			}
			identityKey := module.ID + "\x00" + component.ID
			if _, duplicate := seen[identityKey]; duplicate {
				continue
			}
			seen[identityKey] = struct{}{}
			lifecycle := strings.ReplaceAll(strings.TrimSpace(component.Lifecycle), "-", "_")
			runtimeService := accessRuntimeService{
				ServiceKey: component.ID, ApplicationKey: "system",
				DisplayName: architectureV2HumanizeRuntimeName(component.ID), Role: component.Role,
				Lifecycle: lifecycle, OperationalImpact: "unknown",
				RuntimeIdentity: architectureV2RuntimeIdentity(module.Runtime.Engine, unmanagedAdapter, unmanagedRuntimeRef, component.ID),
			}
			if lifecycle == "one_shot" {
				runtimeService.OperationalImpact = "none"
			}
			if component.Health.Kind == "http" && component.Health.Port > 0 {
				runtimeService.InternalAddress = fmt.Sprintf("http://%s:%d", component.ID, component.Health.Port)
			}
			result = append(result, runtimeService)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].ApplicationKey == result[j].ApplicationKey {
			return result[i].ServiceKey < result[j].ServiceKey
		}
		return result[i].ApplicationKey < result[j].ApplicationKey
	})
	return result, nil
}

func architectureV2RuntimeIdentity(engine, adapter, runtimeRef, componentID string) accessRuntimeIdentity {
	identity := accessRuntimeIdentity{Adapter: adapter, RuntimeRef: runtimeRef, Service: componentID}
	switch adapter {
	case "compose":
		identity.Kind = "docker_compose_service"
		identity.Project = "stackkit-" + runtimeRef
		identity.File = ".stackkit/runtime/" + runtimeRef + "/compose.yaml"
	case "komodo":
		identity.Kind = "komodo_service"
		identity.Deployment = runtimeRef
	case "observed":
		identity.Kind = engine + "_component"
	}
	if engine == "systemd" {
		identity.Kind, identity.Service, identity.Unit = "systemd_unit", "", componentID
	}
	return identity
}

func architectureV2RuntimeDisplayName(applicationName, toolName, applicationKey, componentID string) string {
	if applicationName == "" {
		applicationName = architectureV2HumanizeRuntimeName(applicationKey)
	}
	normalizedTool := strings.ToLower(strings.ReplaceAll(toolName, "-", ""))
	normalizedComponent := strings.ToLower(strings.ReplaceAll(componentID, "-", ""))
	if componentID == applicationKey || normalizedComponent == normalizedTool || (applicationKey == "base" && componentID == "hub") {
		return applicationName
	}
	suffix := strings.TrimPrefix(componentID, applicationKey+"-")
	if suffix == componentID && toolName != "" {
		suffix = strings.TrimPrefix(componentID, toolName+"-")
	}
	return strings.TrimSpace(applicationName + " " + architectureV2HumanizeRuntimeName(suffix))
}

func architectureV2HumanizeRuntimeName(value string) string {
	parts := strings.Fields(strings.NewReplacer("-", " ", "_", " ").Replace(value))
	for index, part := range parts {
		switch strings.ToLower(part) {
		case "id":
			parts[index] = "ID"
		case "postgres", "postgresql":
			parts[index] = "PostgreSQL"
		case "redis":
			parts[index] = "Redis"
		case "ca":
			parts[index] = "CA"
		default:
			parts[index] = strings.ToUpper(part[:1]) + part[1:]
		}
	}
	return strings.Join(parts, " ")
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
