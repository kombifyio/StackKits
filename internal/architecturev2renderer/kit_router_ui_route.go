package architecturev2renderer

import "slices"

// kitRouterUIRoute is the exact route of a node-installed platform UI
// (Marketplace, Dokploy): HTTPS on 443 at the document's own host through its
// Kit router with forward authentication, private except on the Cloud Kit.
type kitRouterUIRoute struct {
	moduleRef, serviceRef, kitRef, host string
	targetPort                          int
	tlsMinVersions                      []string
}

func (want kitRouterUIRoute) matches(route ApplicationDeliveryRouteDescriptor) bool {
	coreModuleRef, supported := MarketplaceCoreModuleRef(want.kitRef)
	return supported && route.ModuleRef == want.moduleRef && route.ServiceRef == want.serviceRef &&
		route.CoreModuleRef == coreModuleRef && route.Host == want.host && route.Path == "/" &&
		route.Protocol == "https" && route.Port == 443 && route.UpstreamProtocol == "http" && route.TargetPort == want.targetPort &&
		route.TLSRequired && slices.Contains(want.tlsMinVersions, route.TLSMinVersion) && route.IngressAuth == "forward-auth" &&
		(route.Exposure == "local" || route.Exposure == "remote-private" || (want.kitRef == "cloud-kit" && route.Exposure == "public"))
}
