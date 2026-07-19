package resolvedplan

import (
	"fmt"
	"sort"
	"strings"
)

// buildLocalReachability creates the sole network/access projection available
// to Home-local renderers. It contains no network configuration, credentials,
// provider identity, management addresses, bridge state, or non-local routes.
func buildLocalReachability(network map[string]any, sites []any) (map[string]any, error) {
	homeSites := make(map[string]struct{})
	homeSiteRefs := make([]string, 0, len(sites))
	for index, rawSite := range sites {
		site, err := asObject(rawSite, fmt.Sprintf("sites[%d]", index))
		if err != nil {
			return nil, err
		}
		id, err := stringField(site, fmt.Sprintf("sites[%d]", index), "id")
		if err != nil {
			return nil, err
		}
		kind, err := stringField(site, fmt.Sprintf("sites[%d]", index), "kind")
		if err != nil {
			return nil, err
		}
		if kind == "home" {
			homeSites[id] = struct{}{}
			homeSiteRefs = append(homeSiteRefs, id)
		}
	}
	sort.Strings(homeSiteRefs)

	rawRoutes, err := objectListField(network, "resolvedPlan.network", "routes")
	if err != nil {
		return nil, err
	}
	routes := make([]any, 0, len(rawRoutes))
	for index, route := range rawRoutes {
		path := fmt.Sprintf("resolvedPlan.network.routes[%d]", index)
		exposure, err := stringField(route, path, "exposure")
		if err != nil {
			return nil, err
		}
		originSiteRef, err := stringField(route, path, "originSiteRef")
		if err != nil {
			return nil, err
		}
		if exposure != "local" {
			continue
		}
		if _, isHome := homeSites[originSiteRef]; !isHome {
			continue
		}
		projected, err := projectLocalReachabilityRoute(route, path, originSiteRef, homeSites)
		if err != nil {
			return nil, err
		}
		routes = append(routes, projected)
	}
	sort.Slice(routes, func(i, j int) bool {
		return routes[i].(map[string]any)["id"].(string) < routes[j].(map[string]any)["id"].(string)
	})
	return map[string]any{"homeSiteRefs": stringSliceAny(homeSiteRefs), "routes": routes}, nil
}

//nolint:gocyclo // Projection and leak prevention are intentionally enforced at one compiler boundary.
func projectLocalReachabilityRoute(route map[string]any, path, originSiteRef string, homeSites map[string]struct{}) (map[string]any, error) {
	projected := make(map[string]any)
	for _, field := range []string{"id", "serviceRef", "moduleRef", "originSiteRef", "originNodeRefs", "exposure", "protocol", "upstreamProtocol", "port", "targetPort", "healthGateRef", "host", "path"} {
		if value, present := route[field]; present {
			projected[field] = value
		}
	}
	access, err := objectField(route, path, "access")
	if err != nil {
		return nil, err
	}
	projectedAccess := make(map[string]any)
	for _, field := range []string{"policyRef", "policyExposure", "authentication", "privilege", "enrolledDeviceRequired", "ownerStepUpRequired", "lanStepDown", "allowedMethods", "defaultClosed"} {
		if value, present := access[field]; present {
			projectedAccess[field] = value
		}
	}
	if allowed, present := access["allowedSiteRefs"]; present {
		allowedRefs, err := stringListField(map[string]any{"allowedSiteRefs": allowed}, path+".access", "allowedSiteRefs", true)
		if err != nil {
			return nil, err
		}
		allowedOrigin := false
		for _, siteRef := range allowedRefs {
			if _, isHome := homeSites[siteRef]; !isHome {
				return nil, fail(ErrProfileMismatch, path+".access.allowedSiteRefs", "Home-local access cannot authorize non-Home Site %q", siteRef)
			}
			allowedOrigin = allowedOrigin || siteRef == originSiteRef
		}
		if !allowedOrigin {
			return nil, fail(ErrProfileMismatch, path+".access.allowedSiteRefs", "Home-local access must authorize its route origin %q", originSiteRef)
		}
		projectedAccess["allowedSiteRefs"] = stringSliceAny(sortStringsUnique(allowedRefs))
	} else {
		// Absence in the reusable source policy is narrowed to this concrete
		// route origin for the effective local realization.
		projectedAccess["allowedSiteRefs"] = []any{originSiteRef}
	}
	if projectedAccess["policyExposure"] == "lan" {
		if rawHost, present := projected["host"]; present {
			host, ok := rawHost.(string)
			if !ok {
				return nil, fail(ErrInvalidInput, path+".host", "route host must be a string")
			}
			host = strings.ToLower(strings.TrimSuffix(host, "."))
			if host == "localhost" || strings.HasSuffix(host, ".localhost") {
				return nil, fail(ErrProfileMismatch, path+".host", ".localhost is process-local and cannot be a Home LAN route")
			}
		}
	}
	projected["access"] = projectedAccess

	tls, err := objectField(route, path, "tls")
	if err != nil {
		return nil, err
	}
	projectedTLS := make(map[string]any)
	for _, field := range []string{"required", "mode", "minVersion"} {
		if value, present := tls[field]; present {
			projectedTLS[field] = value
		}
	}
	projected["tls"] = projectedTLS

	normalized, err := normalizeJSON(projected, false, path)
	if err != nil {
		return nil, err
	}
	return normalized.(map[string]any), nil
}
