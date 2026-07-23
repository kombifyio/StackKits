package resolvedplan

import (
	"fmt"
	"sort"
	"strings"
)

func buildHomeNetworkProjections(spec *specView, network map[string]any, sites []any) (map[string]any, map[string]any, error) {
	localReachability, err := buildLocalReachability(network, sites)
	if err != nil {
		return nil, nil, err
	}
	homeLANDiscovery, err := buildHomeLANDiscovery(spec.lanDiscovery, network, sites)
	if err != nil {
		return nil, nil, err
	}
	return localReachability, homeLANDiscovery, nil
}

// buildHomeLANDiscovery derives the only LAN-advertisement projection exposed
// to renderers. A route is never advertised merely because it is reachable:
// every advertisement must be named by lanDiscovery.advertiseRouteRefs.
func buildHomeLANDiscovery(intent, network map[string]any, sites []any) (map[string]any, error) {
	homeSites := make(map[string]struct{})
	homeSiteRefs := make([]string, 0, len(sites))
	for index, rawSite := range sites {
		path := fmt.Sprintf("sites[%d]", index)
		site, err := asObject(rawSite, path)
		if err != nil {
			return nil, err
		}
		id, err := stringField(site, path, "id")
		if err != nil {
			return nil, err
		}
		kind, err := stringField(site, path, "kind")
		if err != nil {
			return nil, err
		}
		if kind == "home" {
			homeSites[id] = struct{}{}
			homeSiteRefs = append(homeSiteRefs, id)
		}
	}
	sort.Strings(homeSiteRefs)

	requestedRefs, err := homeLANDiscoveryRouteRefs(intent)
	if err != nil {
		return nil, err
	}
	rawRoutes, err := objectListField(network, "resolvedPlan.network", "routes")
	if err != nil {
		return nil, err
	}
	routesByID := make(map[string]map[string]any, len(rawRoutes))
	for index, route := range rawRoutes {
		path := fmt.Sprintf("resolvedPlan.network.routes[%d]", index)
		id, err := stringField(route, path, "id")
		if err != nil {
			return nil, err
		}
		routesByID[id] = route
	}

	advertisements := make([]any, 0, len(requestedRefs))
	for _, routeRef := range requestedRefs {
		route, exists := routesByID[routeRef]
		if !exists {
			return nil, fail(ErrProfileMismatch, "spec.lanDiscovery.advertiseRouteRefs", "LAN discovery route %q does not exist", routeRef)
		}
		advertisement, err := projectHomeLANAdvertisement(routeRef, route, homeSites)
		if err != nil {
			return nil, err
		}
		advertisements = append(advertisements, advertisement)
	}

	return map[string]any{
		"homeSiteRefs":   stringSliceAny(homeSiteRefs),
		"advertisements": advertisements,
	}, nil
}

func homeLANDiscoveryRouteRefs(intent map[string]any) ([]string, error) {
	if intent == nil {
		return []string{}, nil
	}
	if _, present := intent["advertiseRouteRefs"]; !present {
		return []string{}, nil
	}
	refs, err := stringListField(intent, "spec.lanDiscovery", "advertiseRouteRefs", false)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(refs))
	for index, ref := range refs {
		if _, duplicate := seen[ref]; duplicate {
			return nil, fail(ErrInvalidInput, fmt.Sprintf("spec.lanDiscovery.advertiseRouteRefs[%d]", index), "route reference %q is duplicated", ref)
		}
		seen[ref] = struct{}{}
	}
	sort.Strings(refs)
	return refs, nil
}

func projectHomeLANAdvertisement(routeRef string, route map[string]any, homeSites map[string]struct{}) (map[string]any, error) {
	path := "resolvedPlan.network.routes." + routeRef
	exposure, err := stringField(route, path, "exposure")
	if err != nil {
		return nil, err
	}
	if exposure != "local" {
		return nil, fail(ErrProfileMismatch, path+".exposure", "LAN discovery requires local exposure, got %q", exposure)
	}
	originSiteRef, err := singleRouteOriginSite(route, path)
	if err != nil {
		return nil, err
	}
	if _, home := homeSites[originSiteRef]; !home {
		return nil, fail(ErrProfileMismatch, path+".originSiteRef", "LAN discovery requires a Home origin Site, got %q", originSiteRef)
	}
	host, err := stringField(route, path, "host")
	if err != nil {
		return nil, err
	}
	normalizedHost := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	if normalizedHost == "" {
		return nil, fail(ErrProfileMismatch, path+".host", "LAN discovery requires a host")
	}
	if normalizedHost == "localhost" || strings.HasSuffix(normalizedHost, ".localhost") {
		return nil, fail(ErrProfileMismatch, path+".host", ".localhost is process-local and cannot be advertised on a Home LAN")
	}

	access, err := objectField(route, path, "access")
	if err != nil {
		return nil, err
	}
	policyExposure, err := stringField(access, path+".access", "policyExposure")
	if err != nil {
		return nil, err
	}
	if policyExposure != "lan" {
		return nil, fail(ErrProfileMismatch, path+".access.policyExposure", "LAN discovery requires effective LAN access policy, got %q", policyExposure)
	}
	defaultClosed, err := requiredBoolField(access, path+".access", "defaultClosed")
	if err != nil {
		return nil, err
	}
	if !defaultClosed {
		return nil, fail(ErrProfileMismatch, path+".access.defaultClosed", "LAN discovery requires defaultClosed=true")
	}
	policyRef, err := stringField(access, path+".access", "policyRef")
	if err != nil {
		return nil, err
	}

	serviceRef, err := stringField(route, path, "serviceRef")
	if err != nil {
		return nil, err
	}
	originNodeRefs, err := stringListField(route, path, "originNodeRefs", true)
	if err != nil {
		return nil, err
	}
	protocol, err := stringField(route, path, "protocol")
	if err != nil {
		return nil, err
	}
	port, err := intField(route, path, "port")
	if err != nil {
		return nil, err
	}

	projected := map[string]any{
		"routeRef":       routeRef,
		"serviceRef":     serviceRef,
		"originSiteRef":  originSiteRef,
		"originNodeRefs": stringSliceAny(sortStringsUnique(originNodeRefs)),
		"protocol":       protocol,
		"port":           port,
		"host":           host,
		"access": map[string]any{
			"policyRef":      policyRef,
			"policyExposure": policyExposure,
			"defaultClosed":  defaultClosed,
		},
	}
	normalized, err := normalizeJSON(projected, false, path)
	if err != nil {
		return nil, err
	}
	return normalized.(map[string]any), nil
}
