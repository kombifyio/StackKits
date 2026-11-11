package publiccert

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/lancert"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/originca"
	"github.com/kombifyio/stackkits/internal/routerdiagnostics"
)

// The step-ca fallback (kombify-StackKits-igti.15). While the public-names
// certificate serves the node, the routes it covers name no step-ca resolver
// (the apply interpolates STACKKIT_ROUTER_CERT_RESOLVER empty), so no
// exact-name leaf can shadow it. When it stops serving (renewals kept failing
// until it expired, it was removed, or it no longer covers the identity
// domain), those routes would have no certificate until the next apply. The
// router policy then carries one file-provider router that names the step-ca
// resolver for exactly those hosts: Traefik orders a leaf for each of its TLS
// domains, and every router of that host is served from the shared store. No
// apply and no restart is needed; the router watches router-tls/.
const (
	routerAPITimeout     = 10 * time.Second
	fallbackRouterName   = "stackkit-step-ca-fallback"
	fallbackRouterPrefix = "/.well-known/stackkit-step-ca-fallback/"
	routerCertResolver   = "stackkits"
)

// ErrRouterNotRefreshed marks a policy refresh that could not read the
// router's routes; the existing policy stays in place and the signer retries.
var ErrRouterNotRefreshed = errors.New("publiccert: the router policy was not refreshed")

type routerAPIRouter struct {
	Name        string   `json:"name"`
	Provider    string   `json:"provider"`
	Status      string   `json:"status"`
	Rule        string   `json:"rule"`
	EntryPoints []string `json:"entryPoints"`
	TLS         *struct {
		CertResolver string `json:"certResolver"`
	} `json:"tls"`
}

var (
	routeHostPattern = regexp.MustCompile("Host\\(`([^`]+)`\\)")
	plainHostPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$`)
)

// fallbackHosts are the hosts of the identity domain that the public
// certificate covers and whose running TLS routes name no resolver, without
// the hosts an installed origin certificate serves. A node whose certificate
// serves, or that has no public identity origin, needs none.
func fallbackHosts(ctx context.Context, workspace string) ([]string, error) {
	if localevidence.PublicRouterCertificateServes(workspace, time.Now()) {
		return nil, nil
	}
	domain, err := Domain(workspace)
	if err != nil {
		return nil, nil
	}
	routers, err := readRouters(ctx)
	if err != nil {
		return nil, errors.Join(ErrRouterNotRefreshed, err)
	}
	hosts := []string{}
	for _, router := range routers {
		if router.Provider != "docker" || router.Status != "enabled" || router.TLS == nil ||
			router.TLS.CertResolver != "" || !slices.Contains(router.EntryPoints, "websecure") {
			continue
		}
		for _, match := range routeHostPattern.FindAllStringSubmatch(router.Rule, -1) {
			host := strings.ToLower(match[1])
			if plainHostPattern.MatchString(host) && architecturev2renderer.PublicNameCovers(domain, host) &&
				!originca.Covers(workspace, host) && !slices.Contains(hosts, host) {
				hosts = append(hosts, host)
			}
		}
	}
	slices.Sort(hosts)
	return hosts, nil
}

func readRouters(ctx context.Context) ([]routerAPIRouter, error) {
	ctx, cancel := context.WithTimeout(ctx, routerAPITimeout)
	defer cancel()
	raw, err := routerdiagnostics.ReadHome(ctx, routerdiagnostics.HomeRouters)
	if err != nil {
		return nil, err
	}
	var routers []routerAPIRouter
	if err := json.Unmarshal(raw, &routers); err != nil {
		return nil, fmt.Errorf("decode the router's routes: %w", err)
	}
	return routers, nil
}

// fallbackPolicy is the file-provider router that orders step-ca leaves for
// hosts. Its rule names a path nothing serves, at the lowest priority, so it
// never takes a request from the routes it backs.
func fallbackPolicy(hosts []string) string {
	if len(hosts) == 0 {
		return ""
	}
	var out strings.Builder
	out.WriteString("# The public-names certificate does not serve: step-ca for the routes it covered.\n")
	out.WriteString("http:\n  routers:\n    " + fallbackRouterName + ":\n")
	out.WriteString("      rule: \"Host(`" + hosts[0] + "`) && PathPrefix(`" + fallbackRouterPrefix + "`)\"\n")
	out.WriteString("      entryPoints: [websecure]\n      priority: 1\n      service: noop@internal\n")
	out.WriteString("      tls:\n        certResolver: " + routerCertResolver + "\n        domains:\n")
	for _, host := range hosts {
		out.WriteString("          - main: \"" + host + "\"\n")
	}
	return out.String()
}

// retireRouterStore names a fresh public store generation once per lapse: the
// fallback orders exact-name step-ca leaves into the store the router runs
// with, so the next apply that finds the certificate serving must not give the
// router that store again. A lapse is identified by the certificate that
// stopped serving ("none" once it was removed); a later lapse is a later
// certificate and retires the store the apply in between selected.
func retireRouterStore(workspace string, uid, gid int) error {
	lapse := "none"
	if state, _, err := lancert.LoadPublic(workspace); err == nil && state.CertificateSHA256 != "" {
		lapse = "sha256:" + strings.TrimPrefix(strings.ToLower(state.CertificateSHA256), "sha256:")
	}
	if record, ok := localevidence.LoadPublicRouterStoreRecord(workspace); ok && record.Lapse == lapse {
		return nil
	}
	var generation [6]byte
	if _, err := rand.Read(generation[:]); err != nil {
		return err
	}
	raw, err := json.Marshal(localevidence.PublicRouterStoreRecord{
		Schema: localevidence.PublicRouterStoreSchema, Generation: hex.EncodeToString(generation[:]),
		Retired: localevidence.PublicRouterACMEStore(workspace), Lapse: lapse,
	})
	if err != nil {
		return err
	}
	return lancert.WriteConfined(localevidence.PublicRouterStoreCustodyDir(workspace), localevidence.PublicRouterStoreRecordName, raw, 0o600, uid, gid)
}
