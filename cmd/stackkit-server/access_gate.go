package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/auth/accessgate"
	"github.com/kombifyio/stackkits/internal/identity"
)

// accessGateRoutesEnvironment is the compiler-owned route table the core
// Compose artifact gives this process.
const accessGateRoutesEnvironment = "STACKKIT_ACCESS_GATE_ROUTES"

func init() {
	if accessgate.DBSCRegisterPath != identity.HomeDBSCRegisterPath || accessgate.DBSCRefreshPath != identity.HomeDBSCRefreshPath {
		panic("stackkit-server: the Access Gate and the identity verifier must agree on the DBSC paths")
	}
	if accessgate.LaunchPath != identity.HomeLaunchPath || accessgate.HeaderAppPass != identity.HomeAppPassHeader {
		panic("stackkit-server: the Access Gate and the identity verifier must agree on the launch path and the app pass header")
	}
}

// loadAccessGateTable reads and validates the route table. A missing or
// invalid table fails closed: the process does not start, so every
// forward-auth call fails and every gated route stays closed.
func loadAccessGateTable() (accessgate.Table, error) {
	raw := os.Getenv(accessGateRoutesEnvironment)
	if strings.TrimSpace(raw) == "" {
		return accessgate.Table{}, fmt.Errorf("%s is required with --access-gate-listen", accessGateRoutesEnvironment)
	}
	return accessgate.ParseTable([]byte(raw))
}

// accessGateAppHosts are the hosts with a route the Owner opened to app
// passes away; the Hub issues passes for these only.
func accessGateAppHosts(table accessgate.Table) []string {
	hosts := []string{}
	for _, route := range table.Routes {
		if route.AppAccessAway && !slices.Contains(hosts, route.Host) {
			hosts = append(hosts, route.Host)
		}
	}
	slices.Sort(hosts)
	return hosts
}

// accessGateLaunchHosts are the hosts with a user route a browser session
// opens; the Hub signs launch proofs for these only.
func accessGateLaunchHosts(table accessgate.Table) []string {
	hosts := []string{}
	for _, route := range table.Routes {
		if route.Privilege == accessgate.PrivilegeUser && !slices.Contains(hosts, route.Host) &&
			(route.Authentication == accessgate.AuthenticationHuman || route.Authentication == accessgate.AuthenticationHumanDevice) {
			hosts = append(hosts, route.Host)
		}
	}
	slices.Sort(hosts)
	return hosts
}

// newAccessGateServer is the Access Gate listener. It answers the routers'
// forward-auth calls only and carries no API key, CORS or other route: the
// router reaches it inside the core network, and no host port publishes it.
func newAccessGateServer(baseDir, listen string, table accessgate.Table) (*http.Server, error) {
	gate, err := accessgate.New(table, accessgate.Options{
		Devices:   identityDeviceVerifier{root: baseDir},
		Browsers:  identityBrowserSessions{root: baseDir},
		Launcher:  identityLauncher{root: baseDir},
		AppPasses: identityAppPasses{root: baseDir},
		// No Gateway key is pinned at linking yet (igti.12).
		Agents: accessgate.PinnedGatewayKeyMissing{},
	})
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.Handle(accessgate.PathLAN, gate)
	mux.Handle(accessgate.PathRemote, gate)
	// The DBSC endpoints answer 2xx bodies, which a forward-auth call cannot
	// carry; a router per gated host sends them here (identity.HomeDBSCHandler).
	mux.Handle(identity.HomeDBSCRegisterPath, identity.HomeDBSCHandler(baseDir))
	mux.Handle(identity.HomeDBSCRefreshPath, identity.HomeDBSCHandler(baseDir))
	return &http.Server{
		Addr: listen, Handler: mux,
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second,
		WriteTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second,
		MaxHeaderBytes: 256 << 10,
	}, nil
}

func serveAccessGate(server *http.Server) {
	servePlainListener("access gate", server)
}

// servePlainListener serves a core-network listener the routers call; a
// failure stops the process, so the service restarts instead of half-running.
func servePlainListener(name string, server *http.Server) {
	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error(name+" listener failed", "error", err)
			os.Exit(1)
		}
	}()
	slog.Info(name+" listening", "addr", server.Addr)
}

// withHouseholdGroups adds the account's household groups from the
// Authority's projection; unknown groups stay unknown, and the gate refuses
// a grouped route away for them.
func withHouseholdGroups(root string, principal accessgate.Principal) accessgate.Principal {
	principal.Groups, principal.GroupsKnown = identity.HomeAccountGroups(root, principal.Subject, time.Now())
	return principal
}

// identityDeviceVerifier is the gate's device-proof seam bound to the
// verifier export of internal/identity: one process-wide HomeVerifier, the
// route origin from the gate's own route table, the credential and proof from
// the request's "Authorization: DPoP" and "DPoP" headers.
type identityDeviceVerifier struct{ root string }

func (v identityDeviceVerifier) VerifyDeviceProof(_ context.Context, proof accessgate.DeviceProof) (accessgate.Principal, error) {
	verifier, err := identity.HomeVerifierFor(v.root)
	if err != nil {
		return accessgate.Principal{}, err
	}
	credential, dpop, err := identity.ParseHomeDeviceAuthorization(proof.Header)
	if err != nil {
		return accessgate.Principal{}, err
	}
	result, err := verifier.Authenticate(identity.HomeDeviceRequest{
		Method: proof.Method, RouteOrigin: proof.RouteOrigin, Path: proof.Path,
		Credential: credential, Proof: dpop,
	})
	if err != nil {
		return accessgate.Principal{}, err
	}
	return withHouseholdGroups(v.root, accessgate.Principal{Subject: result.Subject, Device: result.Device, ExpiresAt: result.ExpiresAt}), nil
}

// identityBrowserSessions verifies the gate's per-host session cookie. The
// listener class is the gate path the router called.
type identityBrowserSessions struct{ root string }

func (s identityBrowserSessions) VerifyBrowserSession(_ context.Context, host string, request *http.Request) (accessgate.Principal, error) {
	var value string
	count := 0
	for _, cookie := range request.Cookies() {
		if cookie.Name == accessgate.SessionCookie {
			value, count = cookie.Value, count+1
		}
	}
	if count != 1 {
		return accessgate.Principal{}, identity.ErrHomeAccessDenied
	}
	verifier, err := identity.HomeVerifierFor(s.root)
	if err != nil {
		return accessgate.Principal{}, err
	}
	session, err := verifier.VerifyHomeBrowserSession(value, host, request.URL.Path != accessgate.PathLAN)
	if err != nil {
		return accessgate.Principal{}, err
	}
	return withHouseholdGroups(s.root, accessgate.Principal{Subject: session.Account, Device: session.Device, ExpiresAt: session.ExpiresAt}), nil
}

// identityLauncher sends browsers to the Hub and exchanges its launch proofs.
type identityLauncher struct{ root string }

func (l identityLauncher) LaunchURL(target, state string) (string, error) {
	return identity.HomeLaunchURL(l.root, target, state)
}

func (l identityLauncher) CompleteLaunch(_ context.Context, launch accessgate.LaunchRequest) (string, time.Duration, error) {
	verifier, err := identity.HomeVerifierFor(l.root)
	if err != nil {
		return "", 0, err
	}
	caller, err := verifier.AuthenticateLaunch(identity.HomeLaunch{
		Host: launch.Host, Credential: launch.Credential, Proof: launch.Proof, State: launch.State, Target: launch.Target,
	})
	if err != nil {
		return "", 0, err
	}
	now := time.Now()
	value, session, err := identity.IssueHomeBrowserSession(l.root, caller, launch.Host, launch.Home, now)
	if err != nil {
		return "", 0, err
	}
	slog.Info("browser session started", "host", launch.Host, "device", caller.Device, "home", launch.Home, "expiresAt", session.ExpiresAt)
	return value, session.ExpiresAt.Sub(now), nil
}

// SessionRegistration offers a browser-native device-bound session for the
// launch cookie just issued (DBSC); the browser may ignore it.
func (l identityLauncher) SessionRegistration(session string) string {
	return identity.HomeDBSCRegistration(l.root, session, time.Now())
}

// identityAppPasses verifies app passes against the Authority's records.
type identityAppPasses struct{ root string }

func (a identityAppPasses) VerifyAppPass(_ context.Context, host, pass string) (accessgate.Principal, error) {
	verifier, err := identity.HomeVerifierFor(a.root)
	if err != nil {
		return accessgate.Principal{}, err
	}
	record, err := verifier.VerifyHomeAppPass(pass, host)
	if err != nil {
		return accessgate.Principal{}, err
	}
	return withHouseholdGroups(a.root, accessgate.Principal{Subject: record.Account, Device: record.Device, ExpiresAt: record.ExpiresAt}), nil
}

// accessGateHosts are every host of the route table.
func accessGateHosts(table accessgate.Table) []string {
	hosts := []string{}
	for _, route := range table.Routes {
		if !slices.Contains(hosts, route.Host) {
			hosts = append(hosts, route.Host)
		}
	}
	return hosts
}
