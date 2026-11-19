package architecturev2renderer

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

func validatePublicRouteTLSAuthorityV4(route rawPublicServiceRouteV4, roles map[string]string, path string) error {
	tls := route.TLS
	if !tls.Required {
		if tls.Mode != "off" || tls.MinVersion != "" || tls.OwnerCapabilityRef != "" || tls.ProfileRef != "" || tls.IssuerRef != "" || route.Exposure == "public" || roles["edge"] != "" || roles["egress"] != "" {
			return fail(ErrInvalidPlan, path+".tls", "TLS-off route cannot carry public edge, egress, profile, issuer, or custody authority")
		}
		return nil
	}
	if tls.MinVersion == "" {
		return fail(ErrInvalidPlan, path+".tls", "authority-bound route requires an explicit TLS minimum")
	}
	switch tls.Mode {
	case "external":
		if route.Exposure != "public" || tls.OwnerCapabilityRef == "" || roles["egress"] != tls.OwnerCapabilityRef || roles["edge"] != "" || tls.ProfileRef != "" || tls.IssuerRef != "" {
			return fail(ErrInvalidPlan, path+".tls", "external TLS requires the exact egress owner and forbids edge/profile/issuer authority")
		}
	case "terminate-at-edge":
		if route.Exposure != "public" || roles["edge"] == "" || roles["egress"] != "" || tls.OwnerCapabilityRef != "" || tls.ProfileRef == "" || tls.IssuerRef == "" {
			return fail(ErrInvalidPlan, path+".tls", "edge TLS requires exact edge/profile/issuer authority and forbids egress custody")
		}
	case "internal":
		if tls.OwnerCapabilityRef != "" || tls.ProfileRef == "" || tls.IssuerRef == "" || roles["edge"] != "" || roles["egress"] != "" {
			return fail(ErrInvalidPlan, path+".tls", "internal TLS forbids public edge or egress authority")
		}
	default:
		return fail(ErrInvalidPlan, path+".tls.mode", "unsupported required TLS mode %q", tls.Mode)
	}
	return nil
}

func validatePublicRouteCapabilityAuthoritiesV4(route rawPublicServiceRouteV4, path string) error {
	if route.CapabilityAuthorities == nil {
		return fail(ErrInvalidPlan, path+".capabilityAuthorities", "authority list is mandatory")
	}
	capabilities := map[string]struct{}{}
	roles := map[string]string{}
	for authorityIndex, authority := range route.CapabilityAuthorities {
		authorityPath := fmt.Sprintf("%s.capabilityAuthorities[%d]", path, authorityIndex)
		if !oneOf(authority.Role, "access", "transport", "edge", "egress") {
			return fail(ErrInvalidPlan, authorityPath+".role", "unsupported route capability role %q", authority.Role)
		}
		if _, duplicate := capabilities[authority.CapabilityRef]; duplicate {
			return fail(ErrDuplicate, authorityPath+".capabilityRef", "duplicate route capability authority")
		}
		capabilities[authority.CapabilityRef] = struct{}{}
		if existing, duplicate := roles[authority.Role]; duplicate {
			return fail(ErrDuplicate, authorityPath+".role", "route role %q is already owned by %q", authority.Role, existing)
		}
		roles[authority.Role] = authority.CapabilityRef
	}
	if route.Exposure == "local" && len(route.CapabilityAuthorities) != 0 {
		return fail(ErrInvalidPlan, path+".capabilityAuthorities", "local route cannot carry remote reachability authority")
	}
	if route.Exposure != "local" && len(route.CapabilityAuthorities) == 0 {
		return fail(ErrInvalidPlan, path+".capabilityAuthorities", "non-local route requires exact reachability authority")
	}
	return validatePublicRouteTLSAuthorityV4(route, roles, path)
}

func validatePublicServiceRouteListV3(raw json.RawMessage, valuePath string) error {
	var values []json.RawMessage
	if err := decodeJSON(raw, &values, false); err != nil || values == nil {
		return fail(ErrInvalidPlan, valuePath, "expected public service route v3 list")
	}
	seenRoutes := make(map[string]struct{}, len(values))
	for index, value := range values {
		path := fmt.Sprintf("%s[%d]", valuePath, index)
		var route rawPublicServiceRouteV3
		if err := decodeStrict(value, &route); err != nil {
			return wrap(ErrInvalidPlan, path, "decode public service route v3", err)
		}
		if _, duplicate := seenRoutes[route.ID]; duplicate {
			return fail(ErrDuplicate, path+".id", "duplicate public route %q", route.ID)
		}
		seenRoutes[route.ID] = struct{}{}
		base, err := json.Marshal([]rawPublicServiceRouteV2{route.rawPublicServiceRouteV2})
		if err != nil {
			return wrap(ErrInvalidPlan, path, "encode public service route v3 base", err)
		}
		if err := validatePublicServiceRouteListV2(base, valuePath); err != nil {
			return err
		}
		probe := route.HealthProbe
		if probe.Port != route.TargetPort || probe.Protocol != route.UpstreamProtocol || probe.TimeoutSeconds < 1 || probe.TimeoutSeconds > 300 {
			return fail(ErrInvalidPlan, path+".healthProbe", "probe protocol, port, and timeout must match the route contract")
		}
		switch probe.Kind {
		case "http":
			if !oneOf(probe.Protocol, "http", "https") || probe.Method != "GET" || probe.FollowRedirects == nil || *probe.FollowRedirects || !strings.HasPrefix(probe.Path, "/") || len(probe.ExpectedStatuses) == 0 {
				return fail(ErrInvalidPlan, path+".healthProbe", "invalid closed HTTP probe")
			}
			seenStatuses := map[int]struct{}{}
			for _, status := range probe.ExpectedStatuses {
				if status < 100 || status > 599 {
					return fail(ErrInvalidPlan, path+".healthProbe.expectedStatuses", "HTTP status is outside 100..599")
				}
				if _, duplicate := seenStatuses[status]; duplicate {
					return fail(ErrDuplicate, path+".healthProbe.expectedStatuses", "duplicate HTTP status")
				}
				seenStatuses[status] = struct{}{}
			}
		case "tcp":
			if probe.Protocol != "tcp" || probe.Method != "" || probe.FollowRedirects != nil || probe.Path != "" || probe.ExpectedStatuses != nil {
				return fail(ErrInvalidPlan, path+".healthProbe", "TCP probes forbid HTTP-only fields")
			}
		default:
			return fail(ErrInvalidPlan, path+".healthProbe.kind", "unsupported probe kind %q", probe.Kind)
		}
	}
	return nil
}

func validatePublicServiceRouteListV4(raw json.RawMessage, valuePath string) error {
	var values []json.RawMessage
	if err := decodeJSON(raw, &values, false); err != nil || values == nil {
		return fail(ErrInvalidPlan, valuePath, "expected public service route v4 list")
	}
	seenRoutes := make(map[string]struct{}, len(values))
	for index, value := range values {
		path := fmt.Sprintf("%s[%d]", valuePath, index)
		var route rawPublicServiceRouteV4
		if err := decodeStrict(value, &route); err != nil {
			return wrap(ErrInvalidPlan, path, "decode public service route v4", err)
		}
		if route.CoreModuleRef != "" && !oneOf(route.CoreModuleRef, "stackkits-basement-core-runtime", "stackkits-basement-core-lite-runtime", "stackkits-cloud-core-runtime", "stackkits-cloud-core-standalone-runtime") {
			return fail(ErrInvalidPlan, path+".coreModuleRef", "unsupported local routing core")
		}
		if route.IngressAuth != "" && !oneOf(route.IngressAuth, "none", "native", "forward-auth") {
			return fail(ErrInvalidPlan, path+".ingressAuth", "unsupported ingress auth mode %q", route.IngressAuth)
		}
		if _, duplicate := seenRoutes[route.ID]; duplicate {
			return fail(ErrDuplicate, path+".id", "duplicate public route %q", route.ID)
		}
		seenRoutes[route.ID] = struct{}{}
		sites, err := uniqueContractIDSet(route.OriginSiteRefs, path+".originSiteRefs")
		if err != nil || len(sites) == 0 {
			return fail(ErrInvalidPlan, path+".originSiteRefs", "at least one unique origin Site is required")
		}
		nodes, err := uniqueContractIDSet(route.OriginNodeRefs, path+".originNodeRefs")
		if err != nil || len(nodes) == 0 {
			return fail(ErrInvalidPlan, path+".originNodeRefs", "at least one unique origin node is required")
		}
		switch route.OriginSelector {
		case "single-site", "control-authority-site":
			if len(route.OriginSiteRefs) != 1 || route.OriginSiteRef != route.OriginSiteRefs[0] || route.OriginSelection != nil {
				return fail(ErrInvalidPlan, path, "single-Site selector requires one matching originSiteRef and forbids originSelection")
			}
		case "multi-zone", "edge-pool":
			if route.OriginSiteRef != "" || route.OriginSelection == nil {
				return fail(ErrInvalidPlan, path, "multi-Site selector forbids originSiteRef and requires originSelection")
			}
			if err := validateEndpointOriginSelection(*route.OriginSelection, route.OriginSelector, path+".originSelection", true); err != nil {
				return err
			}
			if len(route.OriginSiteRefs) < route.OriginSelection.MinSites {
				return fail(ErrInvalidPlan, path+".originSiteRefs", "origin Site count is below minSites")
			}
			for domainIndex, domain := range route.OriginSelection.NodeFailureDomains {
				if _, exists := sites[domain.SiteRef]; !exists {
					return fail(ErrInvalidPlan, fmt.Sprintf("%s.originSelection.nodeFailureDomains[%d].siteRef", path, domainIndex), "node failure domain is outside the origin Site set")
				}
			}
		default:
			return fail(ErrInvalidPlan, path+".originSelector", "unsupported selector %q", route.OriginSelector)
		}
		if route.BackendPool.UpstreamProtocol != route.UpstreamProtocol || route.BackendPool.TargetPort != route.TargetPort || len(route.BackendPool.Members) == 0 {
			return fail(ErrInvalidPlan, path+".backendPool", "backend pool must exactly match the route upstream and contain members")
		}
		seenInstances, memberSites, memberNodes := map[string]struct{}{}, map[string]struct{}{}, map[string]struct{}{}
		for memberIndex, member := range route.BackendPool.Members {
			memberPath := fmt.Sprintf("%s.backendPool.members[%d]", path, memberIndex)
			if _, exists := sites[member.SiteRef]; !exists {
				return fail(ErrInvalidPlan, memberPath+".siteRef", "backend member is outside the origin Site set")
			}
			if _, exists := nodes[member.NodeRef]; !exists {
				return fail(ErrInvalidPlan, memberPath+".nodeRef", "backend member is outside the origin node set")
			}
			if _, duplicate := seenInstances[member.InstanceRef]; duplicate {
				return fail(ErrDuplicate, memberPath+".instanceRef", "duplicate backend instance")
			}
			seenInstances[member.InstanceRef], memberSites[member.SiteRef], memberNodes[member.NodeRef] = struct{}{}, struct{}{}, struct{}{}
		}
		if !reflect.DeepEqual(sites, memberSites) || !reflect.DeepEqual(nodes, memberNodes) {
			return fail(ErrInvalidPlan, path+".backendPool.members", "backend member Site and node sets must exactly equal the route origin sets")
		}
		probe := route.HealthProbe
		if probe.Port != route.TargetPort || probe.Protocol != route.UpstreamProtocol || probe.TimeoutSeconds < 1 || probe.TimeoutSeconds > 300 {
			return fail(ErrInvalidPlan, path+".healthProbe", "probe protocol, port, and timeout must match the route contract")
		}
		switch probe.Kind {
		case "http":
			if !oneOf(probe.Protocol, "http", "https") || probe.Method != "GET" || probe.FollowRedirects == nil || *probe.FollowRedirects || !strings.HasPrefix(probe.Path, "/") || len(probe.ExpectedStatuses) == 0 {
				return fail(ErrInvalidPlan, path+".healthProbe", "invalid closed HTTP probe")
			}
		case "tcp":
			if probe.Protocol != "tcp" || probe.Method != "" || probe.FollowRedirects != nil || probe.Path != "" || probe.ExpectedStatuses != nil {
				return fail(ErrInvalidPlan, path+".healthProbe", "TCP probes forbid HTTP-only fields")
			}
		default:
			return fail(ErrInvalidPlan, path+".healthProbe.kind", "unsupported probe kind %q", probe.Kind)
		}
		if err := validatePublicRouteCapabilityAuthoritiesV4(route, path); err != nil {
			return err
		}
	}
	return nil
}

func validatePublicServiceRouteListV2(raw json.RawMessage, valuePath string) error {
	var values []json.RawMessage
	if err := decodeJSON(raw, &values, false); err != nil || values == nil {
		return fail(ErrInvalidPlan, valuePath, "expected public service route list")
	}
	seenRoutes := make(map[string]struct{}, len(values))
	for index, value := range values {
		path := fmt.Sprintf("%s[%d]", valuePath, index)
		var route rawPublicServiceRouteV2
		if err := decodeStrict(value, &route); err != nil {
			return wrap(ErrInvalidPlan, path, "decode public service route v2", err)
		}
		if _, duplicate := seenRoutes[route.ID]; duplicate {
			return fail(ErrDuplicate, path+".id", "duplicate public route %q", route.ID)
		}
		seenRoutes[route.ID] = struct{}{}
		if len(route.BackendPool.Members) == 0 {
			return fail(ErrInvalidPlan, path+".backendPool.members", "at least one backend member is required")
		}
		nodes, err := uniqueContractIDSet(route.OriginNodeRefs, path+".originNodeRefs")
		if err != nil || len(nodes) == 0 {
			return fail(ErrInvalidPlan, path+".originNodeRefs", "at least one unique origin node is required")
		}
		seenInstances := make(map[string]struct{}, len(route.BackendPool.Members))
		for memberIndex, member := range route.BackendPool.Members {
			memberPath := fmt.Sprintf("%s.backendPool.members[%d]", path, memberIndex)
			if member.SiteRef != route.OriginSiteRef {
				return fail(ErrInvalidPlan, memberPath+".siteRef", "backend member is outside the route origin site")
			}
			if _, exists := nodes[member.NodeRef]; !exists {
				return fail(ErrInvalidPlan, memberPath+".nodeRef", "backend member is outside the route origin nodes")
			}
			if _, duplicate := seenInstances[member.InstanceRef]; duplicate {
				return fail(ErrDuplicate, memberPath+".instanceRef", "duplicate backend instance")
			}
			seenInstances[member.InstanceRef] = struct{}{}
		}
	}
	return nil
}
