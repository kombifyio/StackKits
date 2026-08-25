package addressplan

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

const APIVersion = "stackkit.address-plan/v1"

var prefixPattern = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

type Plan struct {
	APIVersion      string    `json:"api_version"`
	Provider        string    `json:"provider"`
	StackName       string    `json:"stack_name"`
	Domain          string    `json:"domain"`
	SubdomainPrefix string    `json:"subdomain_prefix,omitempty"`
	Services        []Service `json:"services"`
}

type Service struct {
	ServiceKey string `json:"service_key"`
	Host       string `json:"host"`
}

// FromCanonicalStackSpec projects the secret-free address-registration input
// from CUE-validated StackSpec intent. It performs no network or account work.
func FromCanonicalStackSpec(canonical []byte) (Plan, error) {
	var spec map[string]any
	if err := json.Unmarshal(canonical, &spec); err != nil {
		return Plan{}, fmt.Errorf("decode canonical StackSpec: %w", err)
	}
	metadata, _ := spec["metadata"].(map[string]any)
	network, _ := spec["network"].(map[string]any)
	domainIntent, _ := network["domain"].(map[string]any)
	stackName, _ := metadata["name"].(string)
	domain, _ := domainIntent["base"].(string)
	prefix, _ := domainIntent["subdomainPrefix"].(string)
	stackName, domain, prefix = strings.TrimSpace(stackName), strings.ToLower(strings.TrimSpace(domain)), strings.TrimSpace(prefix)
	if stackName == "" || domain == "" {
		return Plan{}, fmt.Errorf("canonical StackSpec has no stack name or network domain")
	}

	routes, _ := spec["routes"].(map[string]any)
	services := make(map[string]Service)
	for routeID, raw := range routes {
		route, _ := raw.(map[string]any)
		if route["exposure"] != "public" {
			continue
		}
		serviceKey, _ := route["serviceRef"].(string)
		host, _ := route["host"].(string)
		serviceKey, host = strings.TrimSpace(serviceKey), strings.ToLower(strings.TrimSpace(host))
		if serviceKey == "" || host == "" {
			return Plan{}, fmt.Errorf("public route %q has no service or host", routeID)
		}
		if previous, exists := services[serviceKey]; exists && previous.Host != host {
			return Plan{}, fmt.Errorf("service %q has multiple public hosts", serviceKey)
		}
		services[serviceKey] = Service{ServiceKey: serviceKey, Host: host}
	}
	if len(services) == 0 {
		return Plan{}, fmt.Errorf("canonical StackSpec has no public service routes")
	}
	ordered := make([]Service, 0, len(services))
	for _, service := range services {
		ordered = append(ordered, service)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ServiceKey < ordered[j].ServiceKey })
	provider := "external"
	if domain == "kombify.me" {
		provider = "kombify-me"
	}
	return Plan{APIVersion: APIVersion, Provider: provider, StackName: stackName, Domain: domain, SubdomainPrefix: prefix, Services: ordered}, nil
}

// BindPrefix returns a StackSpec candidate whose public route hosts exactly
// match the allocated prefix. The caller must pass the result through CUE
// validation before persisting or generating artifacts.
func BindPrefix(canonical []byte, prefix string) ([]byte, error) {
	prefix = strings.ToLower(strings.TrimSpace(prefix))
	if !prefixPattern.MatchString(prefix) {
		return nil, fmt.Errorf("subdomain prefix must match %s", prefixPattern.String())
	}
	var spec map[string]any
	if err := json.Unmarshal(canonical, &spec); err != nil {
		return nil, fmt.Errorf("decode canonical StackSpec: %w", err)
	}
	network, _ := spec["network"].(map[string]any)
	domainIntent, _ := network["domain"].(map[string]any)
	domain, _ := domainIntent["base"].(string)
	domain = strings.ToLower(strings.TrimSpace(domain))
	if domain == "" {
		return nil, fmt.Errorf("canonical StackSpec has no network domain")
	}
	domainIntent["subdomainPrefix"] = prefix
	routes, _ := spec["routes"].(map[string]any)
	bound := 0
	for routeID, raw := range routes {
		route, _ := raw.(map[string]any)
		if route["exposure"] != "public" {
			continue
		}
		serviceKey, _ := route["serviceRef"].(string)
		serviceKey = strings.TrimSpace(serviceKey)
		if serviceKey == "" {
			return nil, fmt.Errorf("public route %q has no service", routeID)
		}
		route["host"] = prefix + "-" + serviceKey + "." + domain
		bound++
	}
	if bound == 0 {
		return nil, fmt.Errorf("canonical StackSpec has no public service routes")
	}
	return json.Marshal(spec)
}
