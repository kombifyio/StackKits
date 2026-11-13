package architecturev2renderer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/kombifyio/stackkits/internal/accessgatetable"
	"gopkg.in/yaml.v3"
)

// accessGateRoutesEnvironment carries the compiler-owned route table into the
// stackkit-server that runs the Access Gate. It is part of the digest-bound
// Compose artifact, so the table cannot drift from the plan.
const accessGateRoutesEnvironment = "STACKKIT_ACCESS_GATE_ROUTES"

var (
	accessGateRoutesAnchor = regexp.MustCompile(`(?m)^([ ]+)STACKKIT_MCP_PIN_BASE_DIR: "true"\n`)
	accessGateRoutesLine   = regexp.MustCompile(`(?m)^[ ]+` + accessGateRoutesEnvironment + `: ".*"\n`)
)

func isHomeCoreModule(moduleID string) bool {
	return moduleID == basementCoreModuleID || moduleID == basementCoreLiteModuleID
}

// coreServiceHost is the host the core template routes a core endpoint on.
func coreServiceHost(unit RenderUnit, serviceRef string) (string, error) {
	domain, _ := unit.NetworkDomainBase()
	prefix, _ := unit.NetworkSubdomainPrefix()
	service := serviceRef
	if service == "basement-hub" {
		service = "base"
	}
	if !isHomeCoreModule(unit.ModuleID()) {
		topology, err := cloudSessionTopology(unit)
		if err != nil {
			return "", err
		}
		switch service {
		case "id":
			return topology.PocketIDHost, nil
		case "auth":
			return topology.TinyAuthHost, nil
		case "base":
			return topology.HubHost, nil
		}
	}
	if prefix != "" {
		return prefix + "-" + service + "." + domain, nil
	}
	return service + "." + domain, nil
}

// accessGateRouteFromAccess projects a resolved route access decision.
func accessGateRouteFromAccess(id, host, path, serviceRef, ingressAuth string, access rawPublicRouteAccessV2) accessgatetable.Route {
	route := accessgatetable.Route{
		ID: id, Host: host, Path: path,
		Exposure: access.PolicyExposure, Privilege: access.Privilege, Authentication: access.Authentication,
		IngressAuth:    ingressAuth,
		AllowedMethods: append([]string(nil), access.AllowedMethods...),
		AppAccessAway:  access.AppAccessAway,
	}
	if access.AnonymousRateLimit != nil {
		limit := *access.AnonymousRateLimit
		route.AnonymousRateLimit = &limit
	}
	if access.Privilege == accessgatetable.PrivilegeSignIn {
		if serviceRef == "id" {
			route.IdentityAdministration = accessgatetable.IdentityAdministrationPocketID
			route.AdministrationRemoteOptIn = access.OwnerRemoteOptIn
		}
	} else {
		route.OwnerRemoteOptIn = access.OwnerRemoteOptIn
		if groups, ok := applicationAudienceGroups(access, serviceRef); ok {
			route.Groups = strings.Split(groups, ",")
		}
	}
	return route
}

// homeCoreDefaultAccess is the fixed access of a Home core endpoint the plan
// routes no explicit policy to: server administration stays on the LAN
// listener, Pocket ID's sign-in surface is on every enabled path of a homelab
// with a public identity origin, and TinyAuth's login stays on the LAN
// listener because its cookie is no remote credential. A reserved name
// (lab.home included) has no remote path. A Cloud core has no LAN listener and
// no default: without its plan route an endpoint stays closed.
func homeCoreDefaultAccess(unit RenderUnit, endpoint rawModuleServiceEndpoint) rawPublicRouteAccessV2 {
	access := rawPublicRouteAccessV2{PolicyExposure: accessgatetable.ExposureLocalOnly, Privilege: endpoint.RequiredPrivilege, Authentication: accessgatetable.AuthenticationHumanDevice}
	if endpoint.RequiredPrivilege == accessgatetable.PrivilegeSignIn {
		access.Authentication = accessgatetable.AuthenticationHuman
		domain, _ := unit.NetworkDomainBase()
		if endpoint.ServiceRef == "id" && isHomeCoreModule(unit.ModuleID()) && !homelabReservedName(domain) {
			access.PolicyExposure = accessgatetable.ExposureRemotePrivate
		}
	}
	return access
}

// accessGateRoutes builds the gate's table from the core endpoints and every
// compiler-owned route this core serves.
//
//nolint:gocyclo // One projection per route family, each with its own guard.
func accessGateRoutes(unit RenderUnit) (accessgatetable.Table, error) {
	path := "resolvedPlan.modules." + unit.ModuleID() + ".values." + tinyAuthRouteACLInputRef
	var values coreRuntimeValues
	if err := decodeStrict(unit.ValuesJSON(), &values); err != nil {
		return accessgatetable.Table{}, wrap(ErrInvalidPlan, path, "decode compiler-owned routes", err)
	}
	var endpoints []rawModuleServiceEndpoint
	if err := decodeStrict(unit.ServiceEndpointsJSON(), &endpoints); err != nil {
		return accessgatetable.Table{}, wrap(ErrInvalidPlan, "resolvedPlan.modules."+unit.ModuleID()+".serviceEndpoints", "decode core endpoints", err)
	}
	table := accessgatetable.Table{Schema: accessgatetable.RoutesSchema, LANListener: isHomeCoreModule(unit.ModuleID())}
	var hubRoute, signInRoute *accessgatetable.Route
	for _, endpoint := range endpoints {
		host, err := coreServiceHost(unit, endpoint.ServiceRef)
		if err != nil {
			return accessgatetable.Table{}, wrap(ErrInvalidPlan, path, "resolve core host", err)
		}
		ingressAuth := endpoint.IngressAuth
		if ingressAuth == "" {
			ingressAuth = accessgatetable.IngressNative
		}
		var access *rawPublicRouteAccessV2
		for index := range values.TinyAuthRouteACLs {
			route := values.TinyAuthRouteACLs[index]
			if route.ModuleRef != unit.ModuleID() || route.ServiceRef != endpoint.ServiceRef || route.Host != host {
				continue
			}
			if access != nil && !sameGateAccess(*access, route.Access) {
				return accessgatetable.Table{}, fail(ErrInvalidPlan, path, "core service %q has conflicting route access", endpoint.ServiceRef)
			}
			access = &values.TinyAuthRouteACLs[index].Access
		}
		if access == nil {
			defaults := homeCoreDefaultAccess(unit, endpoint)
			if !isHomeCoreModule(unit.ModuleID()) {
				defaults.PolicyExposure = accessgatetable.ExposureLocalOnly
			}
			access = &defaults
		}
		if access.Privilege != endpoint.RequiredPrivilege {
			return accessgatetable.Table{}, fail(ErrInvalidPlan, path, "core service %q route privilege differs from its endpoint", endpoint.ServiceRef)
		}
		route := accessGateRouteFromAccess("core-"+endpoint.ServiceRef, host, "/", endpoint.ServiceRef, ingressAuth, *access)
		table.Routes = append(table.Routes, route)
		if endpoint.ServiceRef == "basement-hub" || endpoint.ServiceRef == "base" {
			hub := route
			hubRoute = &hub
		}
		if endpoint.ServiceRef == "id" {
			signIn := route
			signInRoute = &signIn
		}
	}
	if hubRoute == nil {
		return accessgatetable.Table{}, fail(ErrInvalidPlan, path, "core has no hub endpoint for the StackKits MCP route")
	}
	// The node /mcp of a Home core is behind the gate's agent class (homelab
	// identity target §7, §10): a Gateway-signed envelope mapped to an Owner
	// or admin Homelab account, and on the LAN listener the Owner's local
	// agents with the dedicated MCP token, which the endpoint verifies. A
	// Cloud core has no LAN listener; its /mcp keeps the native token only.
	mcp := accessgatetable.Route{
		ID: "core-mcp", Host: hubRoute.Host, Path: "/mcp", ExactPath: true,
		Exposure: hubRoute.Exposure, Privilege: hubRoute.Privilege, Authentication: accessgatetable.AuthenticationWorkload,
		IngressAuth: accessgatetable.IngressNative, OwnerRemoteOptIn: hubRoute.OwnerRemoteOptIn,
	}
	if isHomeCoreModule(unit.ModuleID()) {
		mcp.Authentication, mcp.Groups = accessgatetable.AuthenticationAgent, []string{"owners", "admins"}
	}
	table.Routes = append(table.Routes, mcp)
	// The Homelab Hub on a Home core: the device client PWA and the
	// device-facing identity routes on the Hub host. They are the Authority's
	// sign-in surface for devices, reachable wherever its Pocket ID sign-in
	// is: every identity route verifies a device proof or a passkey sign-in
	// itself, and only the LAN enrollment listener attests home.
	if isHomeCoreModule(unit.ModuleID()) && signInRoute != nil {
		for _, hubPath := range []struct{ id, path string }{{"core-hub-device", "/device/"}, {"core-hub-identity", accessgatetable.DeviceProofEndpointPath}} {
			// A phone away installs the Hub where it signs in (homelab
			// identity target §6.2, P1 exit), so the shell has the sign-in
			// exposure. The edge of a remote path reads but does not modify
			// traffic (target §8, Connect D5); one that modified it could
			// answer for this host without asking the homelab, so a
			// local-only shell would not stop it either.
			exposure := signInRoute.Exposure
			table.Routes = append(table.Routes, accessgatetable.Route{
				ID: hubPath.id, Host: hubRoute.Host, Path: hubPath.path,
				Exposure: exposure, Privilege: accessgatetable.PrivilegeSignIn, Authentication: accessgatetable.AuthenticationHuman,
				IngressAuth: accessgatetable.IngressNative, DeviceProofEndpoint: hubPath.path == accessgatetable.DeviceProofEndpointPath,
			})
		}
	}
	for _, route := range values.TinyAuthRouteACLs {
		if route.ModuleRef == unit.ModuleID() || route.CoreModuleRef != unit.ModuleID() || route.Host == "" {
			continue
		}
		host := strings.ToLower(strings.TrimSpace(route.Host))
		routePath := route.Path
		if routePath == "" {
			routePath = "/"
		}
		ingressAuth := route.IngressAuth
		if ingressAuth == "" {
			ingressAuth = accessgatetable.IngressNative
		}
		if route.Access.Audience != nil {
			if _, ok := applicationAudienceGroups(route.Access, route.ServiceRef); !ok || route.IngressAuth != "forward-auth" {
				return accessgatetable.Table{}, fail(ErrInvalidPlan, path, "invalid application audience")
			}
		}
		next := accessGateRouteFromAccess(route.ID, host, routePath, route.ServiceRef, ingressAuth, route.Access)
		duplicate := false
		for _, existing := range table.Routes {
			if existing.Host != next.Host || existing.Path != next.Path || existing.ExactPath {
				continue
			}
			// Two routes on one router rule (a LAN route and the remote route of
			// the same policy) share one gate decision; differing ones are
			// ambiguous and refused.
			existing.ID = next.ID
			if !reflect.DeepEqual(existing, next) {
				return accessgatetable.Table{}, fail(ErrInvalidPlan, path, "route %q shares its host and path with a route of different access", route.ID)
			}
			duplicate = true
		}
		if !duplicate {
			table.Routes = append(table.Routes, next)
		}
	}
	sort.Slice(table.Routes, func(i, j int) bool { return table.Routes[i].ID < table.Routes[j].ID })
	if err := table.Validate(); err != nil {
		return accessgatetable.Table{}, wrap(ErrInvalidPlan, path, "the Access Gate cannot enforce the plan's routes exactly", err)
	}
	return table, nil
}

func sameGateAccess(left, right rawPublicRouteAccessV2) bool {
	return left.PolicyExposure == right.PolicyExposure && left.Privilege == right.Privilege &&
		left.Authentication == right.Authentication && left.OwnerRemoteOptIn == right.OwnerRemoteOptIn &&
		left.AppAccessAway == right.AppAccessAway && reflect.DeepEqual(left.AnonymousRateLimit, right.AnonymousRateLimit) &&
		strings.Join(left.AllowedMethods, ",") == strings.Join(right.AllowedMethods, ",")
}

// renderAccessGateRoutes inserts the table into the stackkit-server
// environment directly after its unique governed anchor.
func renderAccessGateRoutes(unit RenderUnit, compose []byte) ([]byte, error) {
	table, err := accessGateRoutes(unit)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(table)
	if err != nil {
		return nil, wrap(ErrRendererFailure, "renderer.access-gate-routes", "encode route table", err)
	}
	anchors := accessGateRoutesAnchor.FindAllSubmatchIndex(compose, -1)
	if len(anchors) != 1 || accessGateRoutesLine.Match(compose) {
		return nil, fail(ErrOutputChanged, "renderer.access-gate-routes", "Compose template lacks the exact Access Gate route insertion point")
	}
	match := anchors[0]
	indent := string(compose[match[2]:match[3]])
	line := fmt.Sprintf("%s%s: %s\n", indent, accessGateRoutesEnvironment, strconv.Quote(string(encoded)))
	var out bytes.Buffer
	out.Write(compose[:match[1]])
	out.WriteString(line)
	out.Write(compose[match[1]:])
	return out.Bytes(), nil
}

// stripAccessGateRoutes removes the route table line for the closed artifact
// comparison. The table's content is bound by the plan digest; here it must
// only be one the gate accepts. An artifact without it stays closed: the gate
// refuses to start without a table, so every gated route fails its
// forward-auth call.
func stripAccessGateRoutes(content []byte) ([]byte, bool) {
	lines := accessGateRoutesLine.FindAllIndex(content, -1)
	if len(lines) == 0 {
		return content, true
	}
	if len(lines) != 1 {
		return nil, false
	}
	line := content[lines[0][0]:lines[0][1]]
	var document map[string]string
	if yaml.Unmarshal(bytes.TrimSpace(line), &document) != nil {
		return nil, false
	}
	if _, err := accessgatetable.ParseTable([]byte(document[accessGateRoutesEnvironment])); err != nil {
		return nil, false
	}
	stripped := append(append([]byte(nil), content[:lines[0][0]]...), content[lines[0][1]:]...)
	return stripped, true
}
