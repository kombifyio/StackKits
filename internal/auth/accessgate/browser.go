package accessgate

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Browser sessions and app passes (homelab identity target §6.3, §12). A
// browser carries no per-request proof: the gate sends a navigation without
// a session to the Hub with a fresh launch state, the Hub signs a 60 second
// launch proof with the device key, and the gate exchanges it at LaunchPath
// on the same host for a host-only session cookie recorded against the
// device. A third-party app sends its per-device app pass in HeaderAppPass on
// the routes the Owner opened to apps away.
const (
	// LaunchPath is reserved on every gated host for the launch exchange.
	LaunchPath = "/.well-known/stackkit/launch"
	// SessionCookie is the gate's per-host browser session.
	SessionCookie = "__Host-stackkit-session"
	// LaunchStateCookie binds a launch to the browser the gate sent to the Hub.
	LaunchStateCookie = "__Host-stackkit-launch"

	launchStateLifetime = 5 * time.Minute
)

// BrowserLauncher is the gate's seam to the Hub launch (internal/identity).
type BrowserLauncher interface {
	// LaunchURL is the Hub page a browser without a session is sent to.
	LaunchURL(target, state string) (string, error)
	// CompleteLaunch admits a launch proof and returns the session value and
	// its lifetime: four hours when the launch came through the LAN
	// listener (Home), one hour otherwise.
	CompleteLaunch(ctx context.Context, launch LaunchRequest) (string, time.Duration, error)
}

// LaunchRequest is one launch exchange as the gate observed it.
type LaunchRequest struct {
	Host       string
	Credential string
	Proof      string
	State      string
	Target     string
	Home       bool
}

// AppPassVerifier verifies an app pass for one host.
type AppPassVerifier interface {
	VerifyAppPass(ctx context.Context, host, pass string) (Principal, error)
}

// completeLaunch exchanges the launch at LaunchPath for a session: the
// query's state must equal this browser's launch cookie on this host.
func (g *Gate) completeLaunch(ctx context.Context, request Request, host string, remote bool) Decision {
	if g.launcher == nil {
		return deny(http.StatusForbidden, "browser_sessions_unavailable")
	}
	_, rawQuery, _ := strings.Cut(request.URI, "?")
	query, err := url.ParseQuery(rawQuery)
	if err != nil {
		return deny(http.StatusBadRequest, "launch_malformed")
	}
	values := map[string]string{}
	for _, name := range []string{"state", "credential", "proof", "target"} {
		if len(query[name]) != 1 || query[name][0] == "" {
			return deny(http.StatusBadRequest, "launch_malformed")
		}
		values[name] = query[name][0]
	}
	state, ok := singleCookie(request.Header, LaunchStateCookie)
	if !ok || subtle.ConstantTimeCompare([]byte(state), []byte(values["state"])) != 1 {
		return deny(http.StatusForbidden, "launch_state_mismatch")
	}
	session, lifetime, err := g.launcher.CompleteLaunch(ctx, LaunchRequest{
		Host: host, Credential: values["credential"], Proof: values["proof"], State: values["state"], Target: values["target"], Home: !remote,
	})
	if err != nil || session == "" || lifetime <= 0 {
		return deny(http.StatusUnauthorized, "launch_refused")
	}
	return Decision{Status: http.StatusFound, Reason: "browser_session_started", Redirect: values["target"], Cookies: []*http.Cookie{
		hostCookie(SessionCookie, session, lifetime),
		hostCookie(LaunchStateCookie, "", -1),
	}}
}

// hostTakesBrowserSessions reports whether host has a user route a browser
// session can open on this listener: human or human+device, and away not
// local-only.
func (g *Gate) hostTakesBrowserSessions(host string, remote bool) bool {
	for _, route := range g.table.Routes {
		if route.Host != host || route.Privilege != PrivilegeUser ||
			route.Authentication != AuthenticationHuman && route.Authentication != AuthenticationHumanDevice {
			continue
		}
		if !remote || route.Exposure != ExposureLocalOnly {
			return true
		}
	}
	return false
}

// sendToHub answers a navigation without a session: to the Hub's launch page
// for this exact URL, with a fresh launch state cookie on this host. Any other
// request gets the plain denial.
func (g *Gate) sendToHub(request Request, host, method, reason string) Decision {
	if g.launcher == nil || !isNavigation(request.Header, method) {
		return deny(http.StatusUnauthorized, reason)
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return deny(http.StatusUnauthorized, reason)
	}
	state := base64.RawURLEncoding.EncodeToString(raw)
	location, err := g.launcher.LaunchURL("https://"+host+request.URI, state)
	if err != nil {
		return deny(http.StatusUnauthorized, reason)
	}
	return Decision{Status: http.StatusFound, Reason: "browser_launch_required", Redirect: location,
		Cookies: []*http.Cookie{hostCookie(LaunchStateCookie, state, launchStateLifetime)}}
}

// browserSession returns the principal of this host's session, if any.
//
// The session cookie is SameSite=Lax, and until kombify.me is a public suffix
// another homelab's host counts as the same site: a state-changing request
// whose fetch metadata names any other origin is never admitted by session.
func (g *Gate) browserSession(ctx context.Context, host string, request Request) (Principal, bool) {
	if g.browsers == nil || request.HTTP == nil {
		return Principal{}, false
	}
	switch strings.ToUpper(request.Method) {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
	default:
		if site := request.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" {
			return Principal{}, false
		}
	}
	principal, err := g.browsers.VerifyBrowserSession(ctx, host, request.HTTP)
	return principal, err == nil && principal.Subject != "" && principal.Device != ""
}

// appPass returns the principal of the request's app pass on a route the
// Owner opened to apps away.
func (g *Gate) appPass(ctx context.Context, route Route, host string, header http.Header) (Principal, bool) {
	values := header.Values(HeaderAppPass)
	if !route.AppAccessAway || g.appPasses == nil || len(values) != 1 {
		return Principal{}, false
	}
	principal, err := g.appPasses.VerifyAppPass(ctx, host, values[0])
	return principal, err == nil && principal.Subject != "" && principal.Device != ""
}

// isNavigation reports a top-level browser navigation: a GET whose fetch
// metadata says navigate, or, from a browser without fetch metadata, a GET
// that accepts HTML.
func isNavigation(header http.Header, method string) bool {
	if method != http.MethodGet {
		return false
	}
	if mode := header.Get("Sec-Fetch-Mode"); mode != "" {
		return mode == "navigate"
	}
	return strings.Contains(header.Get("Accept"), "text/html")
}

func singleCookie(header http.Header, name string) (string, bool) {
	found := ""
	count := 0
	for _, cookie := range (&http.Request{Header: header}).Cookies() {
		if cookie.Name == name {
			found, count = cookie.Value, count+1
		}
	}
	return found, count == 1
}

// hostCookie is a "__Host-" cookie: Secure, path /, no Domain, HttpOnly, Lax.
// A negative lifetime clears it.
func hostCookie(name, value string, lifetime time.Duration) *http.Cookie {
	cookie := &http.Cookie{Name: name, Value: value, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode}
	if lifetime < 0 {
		cookie.MaxAge = -1
	} else {
		cookie.MaxAge = int(lifetime / time.Second)
	}
	return cookie
}
