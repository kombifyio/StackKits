package architecturev2renderer

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/kombifyio/stackkits/internal/accessgatetable"
)

// The Gate's own paths on every gated host (homelab identity target §6.3).
// The launch exchange and the browser-native session endpoints
// (/.well-known/stackkit/) must reach the Access Gate on every host a browser
// session opens, whatever routes the host's workloads own: a host whose routes
// do not cover "/" would otherwise answer 404 at the router and a browser
// could neither launch nor refresh. One router per gated host and listener
// routes that prefix at the Gate listener (the stackkit-gate service); the
// LAN router carries the LAN gate, the remote router the remote entrypoint's.
const (
	gateLaunchRoutersMarker = "      # stackkit-gate-launch-routers\n"
	gateLaunchPathPrefix    = "/.well-known/stackkit/"
	gateLaunchPriority      = 2000
)

var gateLaunchRouterLinePattern = regexp.MustCompile("^      - traefik\\.http\\.routers\\.stackkit-gate-[0-9a-f]{16}(-remote)?\\.(" +
	"rule=Host\\(`[a-z0-9.-]+`\\) && PathPrefix\\(`/\\.well-known/stackkit/`\\)|" +
	"priority=2000|entrypoints=(websecure|remote)|tls=true|tls\\.certresolver=(stackkits|\\$\\{STACKKIT_ROUTER_CERT_RESOLVER\\?\\})|" +
	"middlewares=stackkit-access-gate-lan@docker|service=stackkit-gate)$")

// PublicNameCovers reports whether a public-names certificate for *.<domain>
// and <domain> covers host: the domain itself or exactly one label under it.
func PublicNameCovers(domain, host string) bool {
	host, domain = strings.ToLower(host), strings.ToLower(domain)
	if domain == "" {
		return false
	}
	if host == domain {
		return true
	}
	label, ok := strings.CutSuffix(host, "."+domain)
	return ok && label != "" && !strings.Contains(label, ".")
}

// gateLaunchHosts are the hosts with a user route a browser session opens.
func gateLaunchHosts(table accessgatetable.Table) []string {
	hosts := []string{}
	seen := map[string]bool{}
	for _, route := range table.Routes {
		if route.Privilege == accessgatetable.PrivilegeUser && !seen[route.Host] &&
			(route.Authentication == accessgatetable.AuthenticationHuman || route.Authentication == accessgatetable.AuthenticationHumanDevice) {
			seen[route.Host] = true
			hosts = append(hosts, route.Host)
		}
	}
	sort.Strings(hosts)
	return hosts
}

// renderGateLaunchRouters inserts the per-host Gate routers directly after
// their governed marker. A template without the marker (the Cloud core, which
// has no LAN listener and no Hub launch) is left as it is.
func renderGateLaunchRouters(unit RenderUnit, compose []byte) ([]byte, error) {
	marker := []byte(gateLaunchRoutersMarker)
	switch bytes.Count(compose, marker) {
	case 0:
		return compose, nil
	case 1:
	default:
		return nil, fail(ErrOutputChanged, "renderer.gate-launch-routers", "Compose template has more than one Gate launch router insertion point")
	}
	table, err := accessGateRoutes(unit)
	if err != nil {
		return nil, err
	}
	domain, _ := unit.NetworkDomainBase()
	var lines []string
	for _, host := range gateLaunchHosts(table) {
		if !basementDomainPattern.MatchString(host) || host != strings.ToLower(host) {
			return nil, fail(ErrInvalidPlan, "renderer.gate-launch-routers", "gated host %q is not a plain lowercase host name", host)
		}
		sum := sha256.Sum256([]byte(host))
		id := "stackkit-gate-" + hex.EncodeToString(sum[:])[:16]
		rule := fmt.Sprintf("Host(`%s`) && PathPrefix(`%s`)", host, gateLaunchPathPrefix)
		// A host the public-names certificate covers names the core resolver
		// (none while that certificate serves); any other host keeps step-ca.
		resolver := "stackkits"
		if PublicNameCovers(domain, host) {
			resolver = basementCoreRouteCertResolver
		}
		if table.LANListener {
			lines = append(lines,
				fmt.Sprintf("      - traefik.http.routers.%s.rule=%s", id, rule),
				fmt.Sprintf("      - traefik.http.routers.%s.priority=%d", id, gateLaunchPriority),
				fmt.Sprintf("      - traefik.http.routers.%s.entrypoints=websecure", id),
				fmt.Sprintf("      - traefik.http.routers.%s.tls=true", id),
				fmt.Sprintf("      - traefik.http.routers.%s.tls.certresolver=%s", id, resolver),
				fmt.Sprintf("      - traefik.http.routers.%s.middlewares=stackkit-access-gate-lan@docker", id),
				fmt.Sprintf("      - traefik.http.routers.%s.service=stackkit-gate", id),
			)
		}
		lines = append(lines,
			fmt.Sprintf("      - traefik.http.routers.%s-remote.rule=%s", id, rule),
			fmt.Sprintf("      - traefik.http.routers.%s-remote.priority=%d", id, gateLaunchPriority),
			fmt.Sprintf("      - traefik.http.routers.%s-remote.entrypoints=remote", id),
			fmt.Sprintf("      - traefik.http.routers.%s-remote.service=stackkit-gate", id),
		)
	}
	if len(lines) == 0 {
		return compose, nil
	}
	return bytes.Replace(compose, marker, append(append([]byte(nil), marker...), []byte(strings.Join(lines, "\n")+"\n")...), 1), nil
}

// stripGateLaunchRouters removes the per-host routers for the closed artifact
// comparison; any other line under the stackkit-gate- prefix fails it.
func stripGateLaunchRouters(content []byte) ([]byte, bool) {
	lines := bytes.Split(content, []byte("\n"))
	out := make([][]byte, 0, len(lines))
	for _, line := range lines {
		if bytes.HasPrefix(line, []byte("      - traefik.http.routers.stackkit-gate-")) {
			if !gateLaunchRouterLinePattern.Match(line) {
				return nil, false
			}
			continue
		}
		out = append(out, line)
	}
	return bytes.Join(out, []byte("\n")), true
}
