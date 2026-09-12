package architecturev2

import (
	"fmt"
	"maps"
	"strings"
)

func selectedWorkloadServiceEndpoint(module, alternative map[string]any) map[string]any {
	route, _ := alternative["route"].(map[string]any)
	units, _ := module["renderUnits"].([]any)
	for _, raw := range units {
		unit, _ := raw.(map[string]any)
		endpoints, _ := unit["serviceEndpoints"].([]any)
		for _, rawEndpoint := range endpoints {
			endpoint, _ := rawEndpoint.(map[string]any)
			if endpoint["serviceRef"] == route["serviceRef"] && endpoint["healthRef"] == route["healthRef"] {
				return endpoint
			}
		}
	}
	return nil
}

// projectInitialWorkloadAccess runs only while materializing new native intent.
// The kit owns selection/access defaults, the selected module owns its endpoint
// and data classes, and the domain is read from the already overridden intent.
func projectInitialWorkloadAccess(spec, authoring map[string]any, selections map[string]useCaseWorkloadSelection) error {
	contract, declared := authoring["selectedWorkloadAccess"].(map[string]any)
	if !declared {
		return nil
	}
	refs, _ := contract["workloadRefs"].([]any)
	for _, rawRef := range refs {
		id, _ := rawRef.(string)
		selection, selected := selections[id]
		if !selected {
			continue
		}
		endpoint := selection.ServiceEndpoint
		service, _ := endpoint["serviceRef"].(string)
		privilege, _ := endpoint["requiredPrivilege"].(string)
		policies, _ := contract["accessPolicies"].(map[string]any)
		policy, policyDeclared := policies[privilege].(map[string]any)
		template, _ := contract["route"].(map[string]any)
		if service == "" || !policyDeclared || template == nil {
			return fmt.Errorf("workload %q has no declared endpoint or initial access policy", id)
		}
		domain, err := nestedString(spec, "network", "domain", "base")
		if err != nil || strings.TrimSpace(domain) == "" {
			return fmt.Errorf("workload %q requires a declared network domain", id)
		}
		site, err := initialWorkloadSiteRef(spec, id)
		if err != nil {
			return err
		}
		dataContract, _ := endpoint["data"].(map[string]any)
		bindingRef, _ := dataContract["bindingRef"].(string)
		classes, _ := dataContract["requiredClasses"].([]any)
		if bindingRef == "" || len(classes) == 0 {
			return fmt.Errorf("workload %q has no declared endpoint data binding", id)
		}
		data := initialAuthoringObject(spec, "data")
		bindings := initialAuthoringObject(data, "bindings")
		if _, exists := bindings[bindingRef]; !exists {
			bindings[bindingRef] = map[string]any{"classes": classes, "primarySiteRef": site}
		}
		routes := initialAuthoringObject(spec, "routes")
		for _, rawRoute := range routes {
			route, _ := rawRoute.(map[string]any)
			if route["serviceRef"] == service {
				return fmt.Errorf("workload %q already has an initial route; resolve the duplicate authoring contract", id)
			}
		}
		policyRef := service + "-initial-access"
		access := initialAuthoringObject(spec, "access")
		if _, exists := access[policyRef]; exists {
			return fmt.Errorf("workload %q initial access policy conflicts with existing intent", id)
		}
		access[policyRef] = maps.Clone(policy)
		route := maps.Clone(template)
		route["serviceRef"], route["moduleRef"] = service, selection.ModuleRef
		route["host"], route["accessPolicyRef"] = service+"."+domain, policyRef
		routeRef := service + "-initial-https"
		if _, exists := routes[routeRef]; exists {
			return fmt.Errorf("workload %q initial route conflicts with existing intent", id)
		}
		routes[routeRef] = route
		capabilities, _ := contract["enableCapabilities"].([]any)
		var enable []string
		for _, raw := range capabilities {
			capability, _ := raw.(string)
			enable = append(enable, capability)
		}
		if err := appendNestedStringList(spec, enable, "capabilities", "enable"); err != nil {
			return err
		}
	}
	return nil
}

func initialAuthoringObject(parent map[string]any, field string) map[string]any {
	if object, ok := parent[field].(map[string]any); ok {
		return object
	}
	object := map[string]any{}
	parent[field] = object
	return object
}
