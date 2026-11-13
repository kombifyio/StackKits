package ownappfirewall

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/mereach"
)

const WatchInterval = 3 * time.Second
const EgressRefresh = 30 * time.Second
const EgressMaxAge = 90 * time.Second

// ObserveEgress uses only the installation's already-configured Me origin.
// No registration, no echo route, or an unusable answer means no grant. The
// source-bound direct IPv4 transport excludes proxies and another uplink.
// The ingress-only policy grants no destination, so it needs no echo: only the
// workspace is recorded for the watcher and the boot-time restore.
func (g *Guard) ObserveEgress(ctx context.Context, workspace string) error {
	if g.Policy.IngressOnly {
		if workspace == "" {
			return errors.New("own firewall: workspace required")
		}
		g.workspace = workspace
		return nil
	}
	values, err := g.fetchEgress(ctx, workspace)
	if err != nil {
		return err
	}
	return g.acceptEgress(ctx, values, time.Now(), workspace)
}
func (g *Guard) fetchEgress(ctx context.Context, workspace string) ([]string, error) {
	registration, err := mereach.LoadRegistration(workspace)
	if err != nil {
		return nil, errors.New("own firewall: public egress address unknown; existing Me connection and egress echo answer required; grant refused")
	}
	dialer := &net.Dialer{Timeout: 2 * time.Second, LocalAddr: &net.TCPAddr{IP: net.ParseIP(g.Policy.UplinkSource)}}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
		return dialer.DialContext(ctx, "tcp4", addr)
	}}
	defer transport.CloseIdleConnections()
	client := mereach.Client{Registration: registration, HTTP: &http.Client{Transport: transport, Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	addresses, err := client.EgressAddresses(ctx)
	if err != nil {
		return nil, errors.New("own firewall: public egress echo unavailable on the existing Me origin; grant refused or last exclusion retained until stale")
	}
	return egressAddresses(addresses)
}
func (g *Guard) acceptEgress(ctx context.Context, values []string, at time.Time, workspace string) error {
	values, err := egressAddresses(values)
	if err != nil {
		return err
	}
	// Retain earlier household addresses for this lifetime too; a DHCP change
	// must never reopen a connection to an earlier hairpin destination.
	seen := map[string]bool{}
	for _, a := range append(append([]string{}, g.egress...), values...) {
		seen[a] = true
	}
	values = nil
	for a := range seen {
		values = append(values, a)
	}
	sort.Strings(values)
	if len(values) > 64 {
		return errors.New("own firewall: too many household egress addresses; reviewed restart required")
	}
	if len(g.expected) > 0 {
		if err := g.Verify(ctx); err != nil {
			return err
		}
		script := fmt.Sprintf("flush set inet %s %s\nadd element inet %s %s { %s }\n", Table(g.Policy.Network), EgressSet, Table(g.Policy.Network), EgressSet, strings.Join(values, ", "))
		if _, err = g.command(ctx, []string{"-f", "-"}, []byte(script)); err != nil {
			return err
		}
	}
	g.egress, g.egressAt, g.workspace = values, at, workspace
	if len(g.expected) > 0 {
		if err := g.Verify(ctx); err != nil {
			return err
		}
		if g.container != "" {
			return g.saveWatcher()
		}
	}
	return nil
}
func egressAddresses(values []string) ([]string, error) {
	if len(values) == 0 || len(values) > 64 {
		return nil, errors.New("own firewall: empty or excessive egress echo answer; grant refused")
	}
	seen := map[string]bool{}
	for _, v := range values {
		a, e := netip.ParseAddr(v)
		if e != nil || !(Policy{}).AllowsDestination(a) {
			return nil, errors.New("own firewall: egress echo must contain ordinary public IPv4 addresses")
		}
		seen[a.String()] = true
	}
	out := []string{}
	for v := range seen {
		out = append(out, v)
	}
	sort.Strings(out)
	return out, nil
}
func (g *Guard) verifyEgress(ctx context.Context) error {
	raw, err := g.command(ctx, []string{"-j", "list", "set", "inet", Table(g.Policy.Network), EgressSet}, nil)
	if err != nil {
		return err
	}
	var doc struct {
		NFTables []struct {
			Set *struct {
				Name string
				Elem []string
			}
		}
	}
	if json.Unmarshal(raw, &doc) != nil {
		return errors.New("own firewall: malformed egress set")
	}
	for _, o := range doc.NFTables {
		if o.Set != nil && o.Set.Name == EgressSet {
			v, err := egressAddresses(o.Set.Elem)
			if err != nil {
				return err
			}
			if reflect.DeepEqual(v, g.egress) {
				return nil
			}
		}
	}
	return errors.New("own firewall: household egress exclusion missing or changed")
}
