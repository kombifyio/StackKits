package ownappfirewall

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"
)

type Runner func(context.Context, string, []string, []byte) ([]byte, error)

func Run(ctx context.Context, name string, args []string, input []byte) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = bytes.NewReader(input)
	if name == "docker" {
		cmd.Env = []string{"LANG=C", "LC_ALL=C"}
	}
	raw, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("own application firewall: %s failed (requires Linux nftables, bridge conntrack and host CAP_NET_ADMIN)", name)
	}
	return raw, nil
}

type Receipt struct {
	PolicyDigest  string   `json:"policyDigest"`
	RulesetDigest string   `json:"rulesetDigest"`
	Network       string   `json:"network"`
	Tables        []string `json:"tables"`
	IPv6          string   `json:"ipv6"`
	DynamicSets   []string `json:"dynamicSets,omitempty"`
}
type Guard struct {
	Policy    Policy
	run       Runner
	expected  []any
	egress    []string
	egressAt  time.Time
	workspace string
	container string
}

// New observes the host for the static policy of a firewalled application.
// Without outbound Internet the policy is ingress-only and needs no host facts:
// no uplink, gateway or route inventory, so a disconnected host can still run
// an application that only publishes ports.
func New(ctx context.Context, network string, outbound bool) (*Guard, error) {
	if !outbound {
		return &Guard{Policy: IngressPolicy(network), run: Run}, nil
	}
	p := Policy{Network: network}
	seen := map[string]bool{}
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, errors.New("own firewall: cannot observe host interfaces")
	}
	for _, iface := range interfaces {
		if iface.Name == Bridge(network) {
			continue
		}
		prefixes, err := iface.Addrs()
		if err != nil {
			return nil, errors.New("own firewall: cannot observe host networks")
		}
		for _, a := range prefixes {
			pr, e := netip.ParsePrefix(a.String())
			if e != nil {
				return nil, e
			}
			if pr.Addr().Is4() {
				seen[pr.Masked().String()] = true
			}
		}
	}
	for prefix := range seen {
		p.HomePrefixes = append(p.HomePrefixes, prefix)
	}
	sort.Strings(p.HomePrefixes)
	raw, err := Run(ctx, "ip", []string{"-j", "-4", "route", "get", "1.1.1.1"}, nil)
	if err != nil {
		return nil, err
	}
	var routes []struct {
		Gateway string `json:"gateway"`
		Dev     string `json:"dev"`
		Source  string `json:"prefsrc"`
	}
	if json.Unmarshal(raw, &routes) != nil || len(routes) != 1 {
		return nil, errors.New("own application firewall: default uplink observation unavailable")
	}
	gateway, e := netip.ParseAddr(routes[0].Gateway)
	if e != nil || !gateway.Is4() || routes[0].Dev == "" || len(routes[0].Dev) > 15 || strings.ContainsAny(routes[0].Dev, "\x00\r\n\"\\") {
		return nil, errors.New("own application firewall: routed IPv4 uplink with gateway required")
	}
	iface, err := net.InterfaceByName(routes[0].Dev)
	if err != nil {
		return nil, errors.New("own application firewall: cannot inspect uplink interface")
	}
	addresses, err := iface.Addrs()
	if err != nil {
		return nil, errors.New("own application firewall: cannot inspect uplink addresses")
	}
	if err = admitUplinkSource(routes[0].Source, addresses); err != nil {
		return nil, err
	}
	routeJSON, err := Run(ctx, "ip", []string{"-j", "-4", "route", "show", "table", "all"}, nil)
	if err != nil {
		return nil, err
	}
	var hostRoutes []struct {
		Dev         string `json:"dev"`
		Destination string `json:"dst"`
		Gateway     string `json:"gateway"`
		Nexthops    []struct {
			Gateway string `json:"gateway"`
		} `json:"nexthops"`
	}
	if json.Unmarshal(routeJSON, &hostRoutes) != nil {
		return nil, errors.New("own application firewall: host route inventory malformed")
	}
	hops := map[string]bool{gateway.String(): true}
	for _, route := range hostRoutes {
		if route.Dev == Bridge(network) {
			continue
		}
		for _, hop := range append([]string{route.Gateway}, routeHops(route.Nexthops)...) {
			if hop != "" {
				a, e := netip.ParseAddr(hop)
				if e != nil || !a.Is4() {
					return nil, errors.New("own firewall: unclassifiable next hop")
				}
				hops[a.String()] = true
			}
		}
		if route.Destination == "default" {
			continue
		}
		dst := route.Destination
		if !strings.Contains(dst, "/") {
			dst += "/32"
		}
		prefix, err := netip.ParsePrefix(dst)
		if err != nil || !prefix.Addr().Is4() {
			return nil, errors.New("own application firewall: cannot classify non-default host route")
		}
		if prefix.Bits() == 0 {
			continue
		}
		seen[prefix.Masked().String()] = true
	}
	p.HomePrefixes = nil
	for prefix := range seen {
		p.HomePrefixes = append(p.HomePrefixes, prefix)
	}
	sort.Strings(p.HomePrefixes)
	for hop := range hops {
		p.NextHops = append(p.NextHops, hop)
	}
	sort.Strings(p.NextHops)
	p.UplinkSource = routes[0].Source
	p.Gateway, p.Uplink = gateway.String(), routes[0].Dev
	return &Guard{Policy: p, run: Run}, nil
}
func (g *Guard) command(ctx context.Context, args []string, input []byte) ([]byte, error) {
	return g.run(ctx, "nft", args, input)
}

// owned observes absence with a successful listing; an inspect failure never
// becomes permission to replace an existing (possibly foreign) table.
func (g *Guard) owned(ctx context.Context, family string) (bool, error) {
	raw, err := g.command(ctx, []string{"-j", "list", "tables"}, nil)
	if err != nil {
		return false, err
	}
	var doc struct {
		NFTables []map[string]json.RawMessage `json:"nftables"`
	}
	if json.Unmarshal(raw, &doc) != nil {
		return false, errors.New("own firewall table inventory malformed")
	}
	found := false
	for _, item := range doc.NFTables {
		if v, ok := item["table"]; ok {
			var t struct{ Family, Name string }
			if json.Unmarshal(v, &t) != nil {
				return false, errors.New("own firewall table inventory malformed")
			}
			if t.Family == family && t.Name == Table(g.Policy.Network) {
				found = true
			}
		}
	}
	if !found {
		return false, nil
	}
	raw, err = g.command(ctx, []string{"-j", "list", "table", family, Table(g.Policy.Network)}, nil)
	if err != nil {
		return false, err
	}
	if json.Unmarshal(raw, &doc) != nil {
		return false, errors.New("own firewall table observation malformed")
	}
	for _, item := range doc.NFTables {
		if v, ok := item["table"]; ok {
			var t struct{ Family, Name, Comment string }
			if json.Unmarshal(v, &t) != nil {
				return false, errors.New("own firewall table malformed")
			}
			if t.Family == family && t.Name == Table(g.Policy.Network) && t.Comment == Owner+":"+g.Policy.Network {
				return true, nil
			}
		}
	}
	return false, errors.New("own application firewall: refusing foreign table ownership")
}
func (g *Guard) Apply(ctx context.Context) (Receipt, error) {
	script := ""
	for _, family := range []string{"inet", "bridge"} {
		present, err := g.owned(ctx, family)
		if err != nil {
			return Receipt{}, err
		}
		if present {
			script += "delete table " + family + " " + Table(g.Policy.Network) + "\n"
		}
	}
	script += g.Policy.Script()
	if !g.Policy.IngressOnly {
		if len(g.egress) == 0 {
			return Receipt{}, errors.New("own application firewall: no public egress echo answer; outbound grant refused")
		}
		script += fmt.Sprintf("add element inet %s %s { %s }\n", Table(g.Policy.Network), EgressSet, strings.Join(g.egress, ", "))
	}
	// Kernel echo contains the compiled expressions, including implicit protocol
	// dependencies. Compare every rule and chain to independent kernel readback,
	// ignoring only generated handles and reference counts. Comments alone are
	// never treated as proof that a rule exists or enforces its intended match.
	raw, err := g.command(ctx, []string{"-j", "-e", "-f", "-"}, []byte(script))
	if err != nil {
		return Receipt{}, err
	}
	g.expected, err = objects(raw, true)
	if err != nil {
		return Receipt{}, err
	}
	rules, chains, tables, sets := 0, 0, 0, 0
	for _, obj := range g.expected {
		m := obj.(map[string]any)
		if m["set"] != nil {
			sets++
		}
		if m["rule"] != nil {
			rules++
		}
		if m["chain"] != nil {
			chains++
		}
		if m["table"] != nil {
			tables++
		}
	}
	complete := rules >= 50 && sets == 1
	if g.Policy.IngressOnly {
		// 3 inet chains of 3 rules, the bridge IPv6 rule and 3 peer rules.
		complete = rules == ingressRules && sets == 0
	}
	if !complete || chains != 5 || tables != 2 {
		return Receipt{}, errors.New("own application firewall: incomplete kernel apply acknowledgement")
	}
	if err = g.Verify(ctx); err != nil {
		return Receipt{}, err
	}
	receipt := Receipt{PolicyDigest: DefinitionDigest(), RulesetDigest: g.Policy.Digest(), Network: g.Policy.Network, Tables: []string{"inet " + Table(g.Policy.Network), "bridge " + Table(g.Policy.Network)}, IPv6: "disabled; bridge IPv6 drop; container disable_ipv6=1"}
	if !g.Policy.IngressOnly {
		receipt.DynamicSets = []string{"inet " + Table(g.Policy.Network) + " " + EgressSet}
	}
	return receipt, nil
}

const ingressRules = 13

func (g *Guard) Verify(ctx context.Context) error {
	if len(g.expected) == 0 {
		return errors.New("own application firewall: no verified apply")
	}
	var observed []any
	for _, family := range []string{"inet", "bridge"} {
		raw, err := g.command(ctx, []string{"-j", "list", "table", family, Table(g.Policy.Network)}, nil)
		if err != nil {
			return err
		}
		items, err := objects(raw, false)
		if err != nil {
			return err
		}
		observed = append(observed, items...)
	}
	sort.SliceStable(observed, func(i, j int) bool { return objectKey(observed[i]) < objectKey(observed[j]) })
	if !reflect.DeepEqual(g.expected, observed) {
		return errors.New("own application firewall: kernel rules missing or changed; application must stay stopped")
	}
	if g.Policy.IngressOnly {
		return nil
	}
	return g.verifyEgress(ctx)
}
func (g *Guard) Remove(ctx context.Context) error {
	script := ""
	for _, family := range []string{"inet", "bridge"} {
		present, err := g.owned(ctx, family)
		if err != nil {
			return err
		}
		if present {
			script += "delete table " + family + " " + Table(g.Policy.Network) + "\n"
		}
	}
	if script != "" {
		if _, err := g.command(ctx, []string{"-f", "-"}, []byte(script)); err != nil {
			return err
		}
	}
	for _, family := range []string{"inet", "bridge"} {
		present, err := g.owned(ctx, family)
		if err != nil {
			return err
		}
		if present {
			return errors.New("own application firewall: exact removal not verified")
		}
	}
	return nil
}

// Remove needs no current uplink: a disconnected host must still be removable.
func Remove(ctx context.Context, network string) error {
	if err := StopWatcher(ctx, network, true); err != nil {
		return err
	}
	return (&Guard{Policy: Policy{Network: network}, run: Run}).Remove(ctx)
}

func objects(raw []byte, echo bool) ([]any, error) {
	var doc struct {
		NFTables []map[string]any `json:"nftables"`
	}
	if json.Unmarshal(raw, &doc) != nil {
		return nil, errors.New("own firewall kernel JSON malformed")
	}
	result := []any{}
	for _, item := range doc.NFTables {
		if item["metainfo"] != nil {
			continue
		}
		if echo {
			if item["delete"] != nil {
				continue
			}
			v, ok := item["add"].(map[string]any)
			if !ok {
				return nil, errors.New("own firewall unexpected apply echo")
			}
			item = v
		}
		if len(item) != 1 {
			return nil, errors.New("own firewall unexpected kernel object")
		}
		if el, ok := item["element"].(map[string]any); ok && el["name"] == EgressSet {
			continue
		}
		for kind, value := range item {
			if kind != "table" && kind != "chain" && kind != "rule" && kind != "set" && kind != "element" {
				return nil, errors.New("own firewall unexpected kernel object kind")
			}
			v, ok := value.(map[string]any)
			if !ok {
				return nil, errors.New("own firewall malformed kernel object")
			}
			if kind == "set" && v["name"] == EgressSet {
				delete(v, "elem")
			}
			delete(v, "handle")
			delete(v, "use")
			delete(v, "index")
			delete(v, "position")
		}
		result = append(result, item)
	}
	// Kernel lists chains before rules, while echo follows source order. Group
	// by object type, retaining order within each chain (rule ordering matters).
	sort.SliceStable(result, func(i, j int) bool { return objectKey(result[i]) < objectKey(result[j]) })
	return result, nil
}
func objectKey(v any) string {
	m := v.(map[string]any)
	for _, kind := range []string{"table", "chain", "rule", "set"} {
		if o, ok := m[kind].(map[string]any); ok {
			return fmt.Sprint(o["family"]) + ":" + kind + ":" + fmt.Sprint(o["chain"]) + ":" + fmt.Sprint(o["name"])
		}
	}
	return ""
}

// VerifyBridge refuses rootless/remote daemon namespaces: rules in this host
// namespace cannot authorize a bridge that exists only inside another one.
// It is called after stopped container/network creation, before any start.
func (g *Guard) VerifyBridge() error {
	name := Bridge(g.Policy.Network)
	iface, err := net.InterfaceByName(name)
	if err != nil || iface.Flags&net.FlagUp == 0 {
		return errors.New("own application firewall: managed bridge is not up in the firewall host namespace; rootless/remote Docker is unsupported")
	}
	if info, err := os.Stat(filepath.Join("/sys/class/net", name, "bridge")); err != nil || !info.IsDir() {
		return errors.New("own application firewall: managed host interface is not a Linux bridge")
	}
	return nil
}

// NAT is admitted only with a current external echo exclusion. This check
// proves the route source belongs to the uplink, not that it is public.
func admitUplinkSource(source string, addresses []net.Addr) error {
	addr, err := netip.ParseAddr(source)
	if err != nil || !addr.Is4() || !addr.IsGlobalUnicast() || addr.IsLoopback() || addr.IsLinkLocalUnicast() {
		return errors.New("own firewall: usable IPv4 uplink source required")
	}
	for _, candidate := range addresses {
		prefix, err := netip.ParsePrefix(candidate.String())
		if err == nil && prefix.Addr() == addr {
			return nil
		}
	}
	return errors.New("own firewall: route source not assigned to uplink")
}
func routeHops(hops []struct {
	Gateway string `json:"gateway"`
}) []string {
	var out []string
	for _, h := range hops {
		out = append(out, h.Gateway)
	}
	return out
}
