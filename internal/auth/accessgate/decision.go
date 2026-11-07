package accessgate

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/kombifyio/stackkits/internal/accessgatetable"
)

// Principal is a verified Homelab account, with its enrolled device where
// one was proven. Groups are the account's household groups when the verifier
// knows them (GroupsKnown); a device credential carries none.
type Principal struct {
	Subject     string
	Device      string
	Groups      []string
	GroupsKnown bool
	ExpiresAt   time.Time
}

// DeviceProof is the per-request proof a kombify client carries, as the
// router observed the request: RouteOrigin is the https origin of the matched
// route from the gate's own route table, never a request header; Path is the
// request target in origin form (the verifier drops the query); Header holds
// "Authorization: DPoP <credential>" and the "DPoP" proof.
type DeviceProof struct {
	Method      string
	RouteOrigin string
	Path        string
	Header      http.Header
}

// DeviceProofVerifier is the seam to the device verifier (internal/identity,
// reworked by igti.2). It returns the account, the device and the account's
// groups, or an error; the gate never inspects the credential itself.
type DeviceProofVerifier interface {
	VerifyDeviceProof(ctx context.Context, proof DeviceProof) (Principal, error)
}

// BrowserSessionVerifier verifies the per-host __Host- session the gate sets
// after a Hub launch proof (homelab identity target §6.3, igti.10). Until it
// exists a browser carries no accepted remote session.
type BrowserSessionVerifier interface {
	VerifyBrowserSession(ctx context.Context, host string, request *http.Request) (Principal, error)
}

// AgentRequest is what the gate can bind an agent envelope to. Forward-auth
// carries no body, so the body hash is verified by the endpoint.
type AgentRequest struct {
	Method   string
	Target   string
	Envelope string
}

// AgentEnvelopeVerifier verifies a Gateway-signed envelope (subject, agent
// instance, lease, body hash) against the Gateway key pinned at linking and
// maps it to the Homelab account it acts for (igti.12).
type AgentEnvelopeVerifier interface {
	VerifyAgentEnvelope(ctx context.Context, request AgentRequest) (Principal, error)
}

// ErrNoPinnedGatewayKey is the agent class's fail-closed state: no linking
// step pins a Gateway key on this server yet.
var ErrNoPinnedGatewayKey = errors.New("accessgate: no Gateway key is pinned at linking")

// PinnedGatewayKeyMissing denies every agent request.
type PinnedGatewayKeyMissing struct{}

// VerifyAgentEnvelope always fails closed.
func (PinnedGatewayKeyMissing) VerifyAgentEnvelope(context.Context, AgentRequest) (Principal, error) {
	return Principal{}, ErrNoPinnedGatewayKey
}

// Request is one forward-auth call: the listener class the router bound, and
// the method, host and request URI the router derived from the request.
type Request struct {
	Listener string
	Method   string
	Host     string
	URI      string
	Header   http.Header
	HTTP     *http.Request
}

// Decision is the gate's answer. Headers are set only on allow; Redirect and
// Cookies only on the browser launch answers (a 302 the router relays).
type Decision struct {
	Allow    bool
	Status   int
	Reason   string
	Headers  http.Header
	Redirect string
	Cookies  []*http.Cookie
	// Response are extra headers of the launch answer (the DBSC registration
	// offer).
	Response http.Header
}

// Gate decides per route from the compiler-owned table.
type Gate struct {
	table     Table
	devices   DeviceProofVerifier
	browsers  BrowserSessionVerifier
	launcher  BrowserLauncher
	appPasses AppPassVerifier
	agents    AgentEnvelopeVerifier
	limiter   *anonymousLimiter
	now       func() time.Time
}

// Options are the gate's verifier seams. A nil seam denies its class.
type Options struct {
	Devices   DeviceProofVerifier
	Browsers  BrowserSessionVerifier
	Launcher  BrowserLauncher
	AppPasses AppPassVerifier
	Agents    AgentEnvelopeVerifier
	Now       func() time.Time
}

// New returns a gate for a validated table.
func New(table Table, options Options) (*Gate, error) {
	if err := table.Validate(); err != nil {
		return nil, err
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	agents := options.Agents
	if agents == nil {
		agents = PinnedGatewayKeyMissing{}
	}
	return &Gate{
		table: table, devices: options.Devices, browsers: options.Browsers, launcher: options.Launcher, appPasses: options.AppPasses,
		agents: agents, limiter: newAnonymousLimiter(), now: now,
	}, nil
}

func deny(status int, reason string) Decision {
	return Decision{Status: status, Reason: reason}
}

// Decide is the decision table of homelab identity target §7 and §8.
//
//nolint:gocyclo // One ordered decision table; each step is a guard.
func (g *Gate) Decide(ctx context.Context, request Request) Decision {
	if request.Listener != ListenerLAN && request.Listener != ListenerRemote {
		return deny(http.StatusForbidden, "listener_unknown")
	}
	if request.Listener == ListenerLAN && !g.table.LANListener {
		return deny(http.StatusForbidden, "lan_listener_absent")
	}
	// The listener class is the gate's own answer. A request that already
	// carries one was either spoofed or passed another gate first.
	if len(request.Header.Values(HeaderListener)) != 0 {
		return deny(http.StatusForbidden, "listener_header_present")
	}
	// The router strips the canonical names only; an underscore spelling
	// reaches backends that read headers CGI-style as the same variable.
	for name := range request.Header {
		alias := http.CanonicalHeaderKey(strings.ReplaceAll(name, "_", "-"))
		if strings.Contains(name, "_") && slices.ContainsFunc(ResponseHeaders, func(owned string) bool { return http.CanonicalHeaderKey(owned) == alias }) {
			return deny(http.StatusForbidden, "header_alias_refused")
		}
	}
	// On the remote listener the router overwrites the forwarding headers it
	// manages from an untrusted peer (X-Forwarded-For and -Proto among them,
	// which a Cloudflare edge always adds) and passes every other one; those
	// are refused. Cloudflare's own CF-* headers are not forwarding claims the
	// gate reads: the remote router strips them before any backend.
	if request.Listener == ListenerRemote && refusedForwardingHeader(request.Header) {
		return deny(http.StatusForbidden, "forwarded_header_refused")
	}
	host, ok := canonicalHost(request.Host)
	if !ok {
		return deny(http.StatusForbidden, "host_invalid")
	}
	method := strings.ToUpper(strings.TrimSpace(request.Method))
	if !slices.Contains(httpMethods, method) {
		return deny(http.StatusForbidden, "method_invalid")
	}
	path, ok := canonicalPath(request.URI)
	if !ok {
		return deny(http.StatusForbidden, "path_not_canonical")
	}
	remote := request.Listener == ListenerRemote
	// The launch exchange is the gate's own route, decided by the host's
	// routes as a whole: only a host with a user route a browser session can
	// open on this listener takes one, whatever route covers the path.
	if (path == DBSCRegisterPath || path == DBSCRefreshPath) && method == http.MethodPost {
		if !g.hostTakesBrowserSessions(host, remote) {
			return deny(http.StatusForbidden, "launch_host_refused")
		}
		return Decision{Allow: true, Reason: "dbsc_endpoint", Headers: http.Header{HeaderListener: {request.Listener}}}
	}
	if path == LaunchPath && method == http.MethodGet {
		if !g.hostTakesBrowserSessions(host, remote) {
			return deny(http.StatusForbidden, "launch_host_refused")
		}
		return g.completeLaunch(ctx, request, host, remote)
	}
	route, found := g.table.Lookup(host, path)
	if !found {
		return deny(http.StatusForbidden, "route_unknown")
	}

	privilege, optIn := route.Privilege, route.OwnerRemoteOptIn
	authentication := route.Authentication
	if route.IdentityAdministration == IdentityAdministrationPocketID && isPocketIDAdministration(method, path) {
		// Identity administration on the sign-in host is its own privileged
		// route: LAN-bound unless the Owner opted in, device-bound always, and
		// for owners and admins only.
		privilege, optIn, authentication = PrivilegeIdentity, route.AdministrationRemoteOptIn, AuthenticationHumanDevice
		route.Groups = []string{"owners", "admins"}
	}
	if remote {
		if route.Exposure == ExposureLocalOnly {
			return deny(http.StatusForbidden, "route_local_only")
		}
		if slices.Contains(LANBoundPrivileges, privilege) && !optIn {
			return deny(http.StatusForbidden, "privileged_route_lan_only")
		}
	}
	if len(route.AllowedMethods) != 0 && !slices.Contains(route.AllowedMethods, method) {
		return deny(http.StatusForbidden, "method_not_allowed")
	}
	headers := http.Header{}
	headers.Set(HeaderListener, request.Listener)
	// The router strips Authorization before the backend sees the request. A
	// credential of another scheme belongs to the endpoint, so the gate hands
	// it back; a DPoP device credential is the gate's own and never reaches a
	// backend.
	if authorization := request.Header.Values(HeaderAuthorization); len(authorization) != 0 && !hasDPoPAuthorization(request.Header) {
		headers[HeaderAuthorization] = append([]string(nil), authorization...)
	}
	// The proof binds the route origin from the gate's own table, never a
	// request header; the target URI excludes query and fragment (RFC 9449).
	routeOrigin := "https://" + route.Host
	target := (&url.URL{Scheme: "https", Host: route.Host, Path: path}).String()

	if privilege == PrivilegeSignIn {
		// The sign-in surface performs the authentication itself. The
		// Authority's device identity routes verify the device credential and
		// proof at the endpoint, so the gate hands both back to them only.
		if route.DeviceProofEndpoint {
			if values := request.Header.Values(HeaderAuthorization); len(values) != 0 {
				headers[HeaderAuthorization] = append([]string(nil), values...)
			}
			for _, value := range request.Header.Values(HeaderDPoP) {
				headers.Add(HeaderDPoP, value)
			}
		}
		return Decision{Allow: true, Status: http.StatusOK, Headers: headers}
	}
	switch authentication {
	case AuthenticationNone:
		if privilege != PrivilegeUser {
			return deny(http.StatusForbidden, "anonymous_privileged")
		}
		if remote {
			if route.Exposure != ExposurePublic || !route.AnonymousRateLimit.Valid() {
				return deny(http.StatusForbidden, "anonymous_route_closed")
			}
			if !g.limiter.allow(route.ID, *route.AnonymousRateLimit, g.now()) {
				return deny(http.StatusTooManyRequests, "anonymous_rate_limited")
			}
		}
		return Decision{Allow: true, Status: http.StatusOK, Headers: headers}
	case AuthenticationWorkload:
		// The endpoint verifies its machine credential natively.
		if route.IngressAuth != IngressNative {
			return deny(http.StatusForbidden, "workload_route_not_native")
		}
		return Decision{Allow: true, Status: http.StatusOK, Headers: headers}
	case AuthenticationAgent:
		principal, err := g.agents.VerifyAgentEnvelope(ctx, AgentRequest{Method: method, Target: target, Envelope: request.Header.Get(HeaderAgentEnvelope)})
		if err != nil {
			return deny(http.StatusUnauthorized, "agent_envelope_unverified")
		}
		return g.admit(route, principal, headers, false)
	case AuthenticationHuman:
		if !remote {
			// On the LAN listener TinyAuth, the next forward-auth in the
			// chain, checks the session and the route's groups. A native
			// endpoint authenticates itself.
			if route.IngressAuth == IngressForwardAuth || route.IngressAuth == IngressNative {
				return Decision{Allow: true, Status: http.StatusOK, Headers: headers}
			}
			return deny(http.StatusForbidden, "human_route_without_login")
		}
		principal, reason := g.remoteHuman(ctx, route, host, method, routeOrigin, request)
		if reason == "remote_proof_required" {
			return g.sendToHub(request, host, method, reason)
		}
		if reason != "" {
			return deny(http.StatusUnauthorized, reason)
		}
		return g.admit(route, principal, headers, false)
	case AuthenticationHumanDevice:
		principal, reason := g.deviceProof(ctx, method, routeOrigin, request)
		// A browser's session is device-bound (Hub launch proof); it stands
		// for the device proof on user routes, never on privileged ones.
		if reason == "device_proof_required" && !hasDPoPAuthorization(request.Header) && privilege == PrivilegeUser {
			session, ok := g.browserSession(ctx, host, request)
			if !ok {
				return g.sendToHub(request, host, method, reason)
			}
			principal, reason = session, ""
		}
		if reason != "" {
			return deny(http.StatusUnauthorized, reason)
		}
		if principal.Device == "" {
			return deny(http.StatusUnauthorized, "device_proof_required")
		}
		// On the LAN listener TinyAuth checks the route's groups next.
		return g.admit(route, principal, headers, !remote && route.IngressAuth == IngressForwardAuth)
	}
	return deny(http.StatusForbidden, "authentication_unknown")
}

// remoteHuman accepts only a per-request device proof, an app pass on a
// route the Owner opened to apps away, or the gate's own per-host browser
// session; TinyAuth's parent-domain cookie is not a remote credential.
func (g *Gate) remoteHuman(ctx context.Context, route Route, host, method, routeOrigin string, request Request) (Principal, string) {
	if hasDPoPAuthorization(request.Header) {
		return g.deviceProof(ctx, method, routeOrigin, request)
	}
	if len(request.Header.Values(HeaderAppPass)) != 0 {
		if principal, ok := g.appPass(ctx, route, host, request.Header); ok {
			return principal, ""
		}
		return Principal{}, "app_pass_refused"
	}
	if principal, ok := g.browserSession(ctx, host, request); ok {
		return principal, ""
	}
	return Principal{}, "remote_proof_required"
}

func (g *Gate) deviceProof(ctx context.Context, method, routeOrigin string, request Request) (Principal, string) {
	header := request.Header
	if !hasDPoPAuthorization(header) || len(header.Values(HeaderAuthorization)) != 1 || len(header.Values(HeaderDPoP)) != 1 {
		return Principal{}, "device_proof_required"
	}
	if g.devices == nil {
		return Principal{}, "device_verifier_unavailable"
	}
	principal, err := g.devices.VerifyDeviceProof(ctx, DeviceProof{Method: method, RouteOrigin: routeOrigin, Path: request.URI, Header: header.Clone()})
	if err != nil || principal.Subject == "" {
		return Principal{}, "device_proof_unverified"
	}
	return principal, ""
}

// hasDPoPAuthorization reports an Authorization header of the DPoP scheme.
func hasDPoPAuthorization(header http.Header) bool {
	return slices.ContainsFunc(header.Values(HeaderAuthorization), func(value string) bool {
		scheme, _, _ := strings.Cut(strings.TrimSpace(value), " ")
		return strings.EqualFold(scheme, "DPoP")
	})
}

// admit applies the route's group ceiling and projects the verified identity.
// A device credential carries no groups: on the LAN listener TinyAuth enforces
// a forward-auth route's groups next in the chain (groupsCheckedNext); anywhere
// else unknown membership is refused.
func (g *Gate) admit(route Route, principal Principal, headers http.Header, groupsCheckedNext bool) Decision {
	if principal.Subject == "" || strings.ContainsAny(principal.Subject, "\r\n") || strings.ContainsAny(principal.Device, "\r\n") {
		return deny(http.StatusUnauthorized, "principal_invalid")
	}
	if !principal.ExpiresAt.IsZero() && !g.now().Before(principal.ExpiresAt) {
		return deny(http.StatusUnauthorized, "principal_expired")
	}
	if len(route.Groups) != 0 {
		switch {
		case principal.GroupsKnown:
			if !slices.ContainsFunc(principal.Groups, func(group string) bool { return slices.Contains(route.Groups, group) }) {
				return deny(http.StatusForbidden, "group_denied")
			}
		case groupsCheckedNext:
		default:
			return deny(http.StatusForbidden, "group_membership_unverified")
		}
	}
	headers.Set(HeaderSubject, principal.Subject)
	if principal.Device != "" {
		headers.Set(HeaderDevice, principal.Device)
	}
	return Decision{Allow: true, Status: http.StatusOK, Headers: headers}
}

func canonicalHost(raw string) (string, bool) {
	host := strings.ToLower(strings.TrimSpace(raw))
	if strings.Contains(host, "[") {
		return "", false
	}
	if name, port, found := strings.Cut(host, ":"); found {
		if port == "" || strings.Trim(port, "0123456789") != "" {
			return "", false
		}
		host = name
	}
	host = strings.TrimSuffix(host, ".")
	return host, accessgatetable.HostPattern.MatchString(host)
}

// canonicalPath decodes the request URI the way the backend will and refuses
// any form a router and a backend could read differently.
func canonicalPath(uri string) (string, bool) {
	if uri == "" || uri[0] != '/' || strings.ContainsAny(uri, "#\\") {
		return "", false
	}
	rawPath, _, _ := strings.Cut(uri, "?")
	lower := strings.ToLower(rawPath)
	if strings.Contains(lower, "%2f") || strings.Contains(lower, "%5c") || strings.Contains(lower, "%00") || strings.Contains(lower, "%25") {
		return "", false
	}
	path, err := url.PathUnescape(rawPath)
	if err != nil || hasDotSegment(path) || strings.ContainsAny(path, "\x00\\") {
		return "", false
	}
	return path, true
}

type anonymousWindow struct {
	start time.Time
	count int
}

// managedForwardingHeaders are the forwarding headers the router itself
// writes on the remote entrypoint: it drops a peer's copies (no trusted
// proxy) and sets its own values from the connection, so the gate sees only
// the router's.
var managedForwardingHeaders = map[string]bool{
	"X-Forwarded-For": true, "X-Forwarded-Proto": true, "X-Forwarded-Host": true, "X-Forwarded-Port": true,
	"X-Forwarded-Server": true, "X-Forwarded-Uri": true, "X-Forwarded-Method": true, "X-Forwarded-Prefix": true,
	"X-Forwarded-Tls-Client-Cert": true, "X-Forwarded-Tls-Client-Cert-Info": true, "X-Real-Ip": true,
}

// refusedForwardingNames claim a client address, host or URL on behalf of an
// intermediary. None of them reaches the remote listener legitimately: kombify
// Me removes the visitor's copies before the tunnel and a Cloudflare edge adds
// none of them.
var refusedForwardingNames = map[string]bool{
	"Forwarded": true, "X-Forwarded": true, "X-Original-Url": true, "X-Rewrite-Url": true, "X-Original-Host": true,
	"X-Host": true, "X-Client-Ip": true, "Client-Ip": true, "X-Cluster-Client-Ip": true, "X-Proxyuser-Ip": true,
}

// refusedForwardingHeader reports a forwarding header on the remote listener
// that the router does not overwrite: an RFC 7239 Forwarded header, any other
// X-Forwarded-* name, another client-address or URL-override claim, and an
// underscore spelling of any forwarding name (the router overwrites only the
// canonical one, and a backend that reads headers CGI-style treats both alike).
func refusedForwardingHeader(header http.Header) bool {
	for name := range header {
		canonical := http.CanonicalHeaderKey(strings.ReplaceAll(name, "_", "-"))
		forwarding := refusedForwardingNames[canonical] || managedForwardingHeaders[canonical] || strings.HasPrefix(canonical, "X-Forwarded-")
		if strings.Contains(name, "_") && forwarding {
			return true
		}
		if refusedForwardingNames[canonical] || strings.HasPrefix(canonical, "X-Forwarded-") && !managedForwardingHeaders[canonical] {
			return true
		}
	}
	return false
}

// anonymousLimiter is one fixed window per route. Behind a relay every
// request shares the connector's address, so the budget is per route, never
// per client address.
type anonymousLimiter struct {
	mu      sync.Mutex
	windows map[string]anonymousWindow
}

func newAnonymousLimiter() *anonymousLimiter {
	return &anonymousLimiter{windows: map[string]anonymousWindow{}}
}

func (l *anonymousLimiter) allow(routeID string, limit RateLimit, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	window := l.windows[routeID]
	if now.Sub(window.start) >= time.Duration(limit.WindowSeconds)*time.Second || now.Before(window.start) {
		window = anonymousWindow{start: now}
	}
	if window.count >= limit.Requests {
		l.windows[routeID] = window
		return false
	}
	window.count++
	l.windows[routeID] = window
	return true
}
