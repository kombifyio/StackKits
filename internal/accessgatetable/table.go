// Package accessgatetable is the route table contract between the core
// renderer, which projects the plan's routes into it, and the StackKits Access
// Gate (internal/auth/accessgate), which decides from it (homelab identity
// target §7). It also names the headers the routers' gate middlewares carry.
// It holds no decision code and no credential handling, so the public CLI's
// renderer can depend on it.
package accessgatetable

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// RoutesSchema identifies the compiler-owned route table the core renderer
// hands to the gate.
const RoutesSchema = "stackkit.access-gate-routes/v1"

// Request headers the gate reads and response headers it owns. Every name in
// ResponseHeaders is listed in the router's authResponseHeaders, so the router
// deletes a client-supplied copy before the backend sees the request and
// copies only the gate's value: the listener class and identity cannot be
// spoofed, and the per-request proof never reaches a backend. The device
// credential travels as "Authorization: DPoP <credential>" with its proof in
// "DPoP" (the carrier of internal/identity, RFC 9449); the gate hands an
// Authorization header of any other scheme back unchanged, so only a DPoP
// credential is removed.
const (
	HeaderListener      = "Stackkit-Access-Listener"
	HeaderSubject       = "Stackkit-Access-Subject"
	HeaderDevice        = "Stackkit-Access-Device"
	HeaderAuthorization = "Authorization"
	HeaderDPoP          = "DPoP"
	HeaderAgentEnvelope = "Stackkit-Agent-Envelope"
	// HeaderAppPass carries a device's app pass (homelab identity target
	// §6.3); the gate never returns it, so the router strips it.
	HeaderAppPass = "Stackkit-App-Pass"
)

// ResponseHeaders is the exact authResponseHeaders list of both gate
// middlewares. The TinyAuth identity headers are included so that on the
// remote listener, where TinyAuth is not consulted, a client cannot inject
// them.
var ResponseHeaders = []string{
	HeaderListener, HeaderSubject, HeaderDevice,
	HeaderAuthorization, HeaderDPoP, HeaderAgentEnvelope, HeaderAppPass,
	"Remote-User", "Remote-Sub", "Remote-Name", "Remote-Email", "Remote-Groups",
}

// Listener classes. The class comes from the gate path the router calls.
const (
	ListenerLAN    = "lan"
	ListenerRemote = "remote"
)

// Policy exposures of #AccessPolicyV2.
const (
	ExposureLocalOnly     = "local-only"
	ExposureRemotePrivate = "remote-private"
	ExposurePublic        = "public"
)

// Authentication classes. workload is a machine credential the endpoint
// verifies natively (the node /mcp token); it never admits a person.
const (
	AuthenticationNone        = "none"
	AuthenticationHuman       = "human"
	AuthenticationHumanDevice = "human+device"
	AuthenticationAgent       = "agent"
	AuthenticationWorkload    = "workload"
)

// Privileges. sign-in labels the identity provider and forward-auth login
// surfaces, which sign-in and away enrollment need on every enabled path;
// identity is identity administration and stays on the LAN listener unless
// the Owner opts in.
const (
	PrivilegeUser     = "user"
	PrivilegeSignIn   = "sign-in"
	PrivilegeAdmin    = "admin"
	PrivilegeIdentity = "identity"
	PrivilegeSecrets  = "secrets"
	PrivilegeVault    = "vault"
	PrivilegeRecovery = "recovery"
)

// Ingress authentication of the endpoint behind the route.
const (
	IngressForwardAuth = "forward-auth"
	IngressNative      = "native"
	IngressNone        = "none"
)

// DeviceProofEndpointPath is the one route prefix whose endpoint verifies the
// device credential and proof itself.
const DeviceProofEndpointPath = "/api/v1/identity/"

// IdentityAdministrationPocketID marks the sign-in route whose host also
// serves the Pocket ID administration surface.
const IdentityAdministrationPocketID = "pocketid"

// LANBoundPrivileges stay on the LAN listener (or the VPN add-on) unless the
// Owner opts in (#AccessLANBoundPrivilegesV2).
var LANBoundPrivileges = []string{PrivilegeAdmin, PrivilegeIdentity, PrivilegeSecrets, PrivilegeVault, PrivilegeRecovery}

// RateLimit is the anonymous request budget of a public route without login.
type RateLimit struct {
	Requests      int `json:"requests"`
	WindowSeconds int `json:"windowSeconds"`
}

// Route is one compiler-owned route the gate decides for. Host and Path
// mirror the router rule: an exact host and a Traefik PathPrefix (plain
// string prefix) or, with ExactPath, a Path rule.
type Route struct {
	ID                        string     `json:"id"`
	Host                      string     `json:"host"`
	Path                      string     `json:"path"`
	ExactPath                 bool       `json:"exactPath,omitempty"`
	Exposure                  string     `json:"exposure"`
	Privilege                 string     `json:"privilege"`
	Authentication            string     `json:"authentication"`
	IngressAuth               string     `json:"ingressAuth"`
	OwnerRemoteOptIn          bool       `json:"ownerRemoteOptIn,omitempty"`
	Groups                    []string   `json:"groups,omitempty"`
	AllowedMethods            []string   `json:"allowedMethods,omitempty"`
	IdentityAdministration    string     `json:"identityAdministration,omitempty"`
	AdministrationRemoteOptIn bool       `json:"administrationRemoteOptIn,omitempty"`
	AnonymousRateLimit        *RateLimit `json:"anonymousRateLimit,omitempty"`
	// AppAccessAway is the Owner's decision that the service's third-party
	// apps reach this route on the remote listener with a per-device app pass
	// issued by the Hub (homelab identity target §6.3); only a user route a
	// person signs in to, reachable away from home, carries it.
	AppAccessAway bool `json:"appAccessAway,omitempty"`
	// DeviceProofEndpoint marks the Authority's own device identity routes
	// (the Hub host's /api/v1/identity/): their endpoint verifies the device
	// credential and DPoP proof itself, so the gate hands both through. Every
	// other backend never sees them.
	DeviceProofEndpoint bool `json:"deviceProofEndpoint,omitempty"`
}

// Table is the complete route table of one server.
type Table struct {
	Schema string `json:"schema"`
	// LANListener is true only on a server with a LAN listener (a Home
	// core). A Cloud core's only listener is public, so its gate refuses
	// the LAN path whoever calls it.
	LANListener bool    `json:"lanListener"`
	Routes      []Route `json:"routes"`
}

var (
	// HostPattern is a lowercase DNS name with at least two labels.
	HostPattern   = regexp.MustCompile(`^(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
	groupPattern  = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)
	routeIDFormat = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,127}$`)
	// HTTPMethods are the request methods a route can serve, WebDAV, CalDAV
	// and CardDAV included.
	HTTPMethods = []string{
		"CONNECT", "COPY", "DELETE", "GET", "HEAD", "LOCK", "MKCALENDAR", "MKCOL", "MOVE", "OPTIONS",
		"PATCH", "POST", "PROPFIND", "PROPPATCH", "PUT", "REPORT", "SEARCH", "TRACE", "UNLOCK",
	}
)

// ErrInvalidTable rejects a route table the gate cannot enforce exactly.
var ErrInvalidTable = errors.New("accessgate: invalid route table")

// ParseTable decodes and validates a route table strictly. A table the gate
// cannot enforce exactly is refused as a whole, so the router's forward-auth
// call fails and every route stays closed.
func ParseTable(raw []byte) (Table, error) {
	var table Table
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&table); err != nil {
		return Table{}, fmt.Errorf("%w: %v", ErrInvalidTable, err)
	}
	if decoder.More() {
		return Table{}, fmt.Errorf("%w: trailing data", ErrInvalidTable)
	}
	if err := table.Validate(); err != nil {
		return Table{}, err
	}
	return table, nil
}

// Validate enforces the #AccessPolicyV2 invariants on every route, so a
// stale or hand-edited table cannot widen access.
func (t Table) Validate() error {
	if t.Schema != RoutesSchema {
		return fmt.Errorf("%w: schema %q", ErrInvalidTable, t.Schema)
	}
	seen := map[string]bool{}
	for index, route := range t.Routes {
		if err := route.validate(); err != nil {
			return fmt.Errorf("%w: routes[%d] %q: %v", ErrInvalidTable, index, route.ID, err)
		}
		key := route.Host + "\x00" + route.Path + "\x00" + fmt.Sprint(route.ExactPath)
		if seen[key] {
			return fmt.Errorf("%w: routes[%d] duplicates host %q path %q", ErrInvalidTable, index, route.Host, route.Path)
		}
		seen[key] = true
	}
	return nil
}

func (r Route) validate() error {
	if !routeIDFormat.MatchString(r.ID) {
		return errors.New("route id is not a contract id")
	}
	if !HostPattern.MatchString(r.Host) {
		return errors.New("host is not a lowercase DNS name")
	}
	// "$" would be interpolated by Compose inside the environment value.
	if !strings.HasPrefix(r.Path, "/") || strings.ContainsAny(r.Path, "?#\\$") || HasDotSegment(r.Path) {
		return errors.New("path is not a canonical absolute path")
	}
	if !slices.Contains([]string{ExposureLocalOnly, ExposureRemotePrivate, ExposurePublic}, r.Exposure) {
		return errors.New("unknown exposure")
	}
	if !slices.Contains([]string{PrivilegeUser, PrivilegeSignIn, PrivilegeAdmin, PrivilegeIdentity, PrivilegeSecrets, PrivilegeVault, PrivilegeRecovery}, r.Privilege) {
		return errors.New("unknown privilege")
	}
	if !slices.Contains([]string{AuthenticationNone, AuthenticationHuman, AuthenticationHumanDevice, AuthenticationAgent, AuthenticationWorkload}, r.Authentication) {
		return errors.New("unknown authentication")
	}
	if !slices.Contains([]string{IngressForwardAuth, IngressNative, IngressNone}, r.IngressAuth) {
		return errors.New("unknown ingress authentication")
	}
	for _, group := range r.Groups {
		if !groupPattern.MatchString(group) {
			return errors.New("group is not a group name")
		}
	}
	for _, method := range r.AllowedMethods {
		if !slices.Contains(HTTPMethods, method) {
			return errors.New("unknown HTTP method")
		}
	}
	lanBound := slices.Contains(LANBoundPrivileges, r.Privilege)
	switch {
	case lanBound && !r.OwnerRemoteOptIn && r.Exposure != ExposureLocalOnly:
		return errors.New("a LAN-bound privilege is local-only without the Owner's opt-in")
	case !lanBound && r.OwnerRemoteOptIn:
		return errors.New("only a LAN-bound privilege carries the Owner's remote opt-in")
	case r.Authentication == AuthenticationNone && r.Privilege != PrivilegeUser:
		return errors.New("a route without login serves anonymous user content only")
	case r.Exposure == ExposureRemotePrivate && r.Authentication == AuthenticationNone:
		return errors.New("private remote access is for Homelab accounts only")
	case r.Authentication == AuthenticationNone && r.Exposure == ExposurePublic && !r.AnonymousRateLimit.Valid():
		return errors.New("a public route without login needs its anonymous rate limit")
	case r.AnonymousRateLimit != nil && (r.Authentication != AuthenticationNone || r.Exposure != ExposurePublic):
		return errors.New("only a public route without login carries an anonymous rate limit")
	case r.AppAccessAway && (r.Privilege != PrivilegeUser || r.Authentication != AuthenticationHuman ||
		(r.Exposure != ExposureRemotePrivate && r.Exposure != ExposurePublic) || r.DeviceProofEndpoint || r.IdentityAdministration != ""):
		return errors.New("app access away exists only on a user route a person signs in to away from home")
	case r.Privilege == PrivilegeSignIn && (r.Authentication != AuthenticationHuman || r.IngressAuth == IngressForwardAuth):
		return errors.New("a sign-in surface authenticates natively")
	case r.Privilege != PrivilegeUser && r.Privilege != PrivilegeSignIn &&
		r.Authentication != AuthenticationHumanDevice && r.Authentication != AuthenticationAgent && r.Authentication != AuthenticationWorkload:
		return errors.New("a privileged route needs human+device or agent")
	case r.Authentication == AuthenticationWorkload && r.IngressAuth != IngressNative:
		return errors.New("a workload credential is verified natively by its endpoint")
	case r.Privilege != PrivilegeUser && r.Privilege != PrivilegeSignIn && r.Authentication == AuthenticationHumanDevice && len(r.Groups) == 0:
		return errors.New("a privileged route a person reaches needs its group ceiling")
	case r.IdentityAdministration != "" && (r.IdentityAdministration != IdentityAdministrationPocketID || r.Privilege != PrivilegeSignIn):
		return errors.New("identity administration is classified only on the Pocket ID sign-in surface")
	case r.AdministrationRemoteOptIn && r.IdentityAdministration == "":
		return errors.New("identity administration opt-in needs the identity administration surface")
	case r.DeviceProofEndpoint && (r.Privilege != PrivilegeSignIn || r.IngressAuth != IngressNative || r.IdentityAdministration != "" ||
		r.ExactPath || r.Path != DeviceProofEndpointPath):
		return errors.New("only the Authority's device identity routes verify device proofs at their endpoint")
	}
	return nil
}

func (l *RateLimit) Valid() bool {
	return l != nil && l.Requests >= 1 && l.WindowSeconds >= 1
}

// lookup returns the route the router selected for host and path: an exact
// host, then the longest matching Path or PathPrefix rule.
func (t Table) Lookup(host, path string) (Route, bool) {
	var best Route
	found := false
	for _, route := range t.Routes {
		if route.Host != host {
			continue
		}
		if route.ExactPath {
			if path != route.Path {
				continue
			}
		} else if !strings.HasPrefix(path, route.Path) {
			continue
		}
		if !found || ruleLength(route) > ruleLength(best) {
			best, found = route, true
		}
	}
	return best, found
}

// ruleLength mirrors the router priority: a longer rule wins and an exact
// Path rule wins over a prefix of the same length.
func ruleLength(route Route) int {
	length := len(route.Path) * 2
	if route.ExactPath {
		length++
	}
	return length
}

func HasDotSegment(path string) bool {
	for _, segment := range strings.Split(path, "/") {
		if segment == "." || segment == ".." {
			return true
		}
	}
	return false
}
