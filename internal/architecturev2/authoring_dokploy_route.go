package architecturev2

import (
	"errors"
	"maps"
	"sort"
	"strings"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
)

// The Dokploy UI is a module route of the Dokploy bootstrap. TinyAuth admits
// only declared hosts, so the route exists while any workload deploys through
// Dokploy and is retired with the last one. It takes the Kit's user access
// policy and route template, like the Marketplace UI: household members pass
// TinyAuth but hold no Dokploy account.
const (
	dokployUIRouteRef  = "dokploy-initial-https"
	dokployUIAccessRef = "dokploy-initial-access"
)

func dokployWorkloadIDs(spec map[string]any) []string {
	workloads, _ := spec["workloads"].(map[string]any)
	ids := make([]string, 0, len(workloads))
	for id, raw := range workloads {
		if entry, _ := raw.(map[string]any); entry["runtimeAdapterRef"] == "dokploy" {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

// authorDokployPlatformRoute adds the UI route with the first Dokploy workload;
// an existing Dokploy UI route is kept as authored.
func authorDokployPlatformRoute(spec, contract map[string]any) error {
	ids := dokployWorkloadIDs(spec)
	if len(ids) == 0 {
		return nil
	}
	routes := initialAuthoringObject(spec, "routes")
	for _, raw := range routes {
		if route, _ := raw.(map[string]any); route["moduleRef"] == architecturev2renderer.DokployBootstrapModuleRef || route["serviceRef"] == "dokploy" {
			return nil
		}
	}
	policies, _ := contract["accessPolicies"].(map[string]any)
	policy, declared := policies["user"].(map[string]any)
	template, _ := contract["route"].(map[string]any)
	if !declared || template == nil {
		return errors.New("the Dokploy UI route requires the Kit's user access policy and route template")
	}
	site, err := initialWorkloadSiteRef(spec, ids[0])
	if err != nil {
		return err
	}
	domain, err := nestedString(spec, "network", "domain", "base")
	if err != nil || strings.TrimSpace(domain) == "" {
		return errors.New("the Dokploy UI route requires a declared network domain")
	}
	if !retainInitialAccessPolicy(spec, dokployUIAccessRef, policy) {
		return errors.New("the Dokploy UI access policy conflicts with existing intent")
	}
	bindings := initialAuthoringObject(initialAuthoringObject(spec, "data"), "bindings")
	if _, exists := bindings["dokploy"]; !exists {
		bindings["dokploy"] = map[string]any{"classes": []any{"secret"}, "primarySiteRef": site}
	}
	route := maps.Clone(template)
	route["serviceRef"], route["moduleRef"] = "dokploy", architecturev2renderer.DokployBootstrapModuleRef
	route["host"], route["accessPolicyRef"] = "dokploy."+domain, dokployUIAccessRef
	routes[dokployUIRouteRef] = route
	return nil
}

// retireDokployPlatformRoute removes the UI route with the last Dokploy
// workload. The access policy and data binding stay as retained user intent.
func retireDokployPlatformRoute(spec map[string]any) {
	if len(dokployWorkloadIDs(spec)) > 0 {
		return
	}
	routes, _ := spec["routes"].(map[string]any)
	for id, raw := range routes {
		if route, _ := raw.(map[string]any); route["moduleRef"] == architecturev2renderer.DokployBootstrapModuleRef {
			delete(routes, id)
		}
	}
}
