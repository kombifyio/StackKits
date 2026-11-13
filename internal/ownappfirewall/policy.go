// Package ownappfirewall owns the closed network authorization boundary for
// own-application v2. It never changes the host's general firewall or Docker tables.
package ownappfirewall

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/netip"
	"strings"
)

// Registry snapshots retrieved 2026-11-12. All registry entries are denied,
// including globally reachable exceptions: this policy grants ordinary public
// unicast destinations, not special-purpose services or translation mechanisms.
// Sources: https://www.iana.org/assignments/iana-ipv4-special-registry/
// https://www.iana.org/assignments/iana-ipv6-special-registry/
// Multicast and deprecated compatible/site-local space supplement those registries.
//
//go:embed special-use.json
var registryJSON []byte

type SpecialUse struct {
	Prefix   string `json:"prefix"`
	Name     string `json:"name"`
	Registry string `json:"registry"`
}

func SpecialRanges() []SpecialUse {
	var entries []SpecialUse
	if err := json.Unmarshal(registryJSON, &entries); err != nil {
		panic(err)
	}
	return entries
}

func identity(network string) string {
	s := sha256.Sum256([]byte(network))
	return hex.EncodeToString(s[:])
}
func Table(network string) string  { return "stackkit_own_" + identity(network)[:16] }
func Bridge(network string) string { return "sko" + identity(network)[:12] }
func MAC(network string) string {
	h := identity(network)
	return "02:" + strings.Join([]string{h[:2], h[2:4], h[4:6], h[6:8], h[8:10]}, ":")
}

const Owner = "stackkit.own-egress/v1"

// Policy is the static ruleset of one firewalled own application. A firewalled
// application has outbound Internet, published host ports, or both. IngressOnly
// is the variant without outbound Internet: the container may only answer
// connections made to it (its published ports and its router), so it needs no
// uplink, gateway, egress echo or host facts at all.
type Policy struct {
	Network      string   `json:"network"`
	IngressOnly  bool     `json:"ingressOnly,omitempty"`
	HomePrefixes []string `json:"homePrefixes"`
	Gateway      string   `json:"gateway"`
	Uplink       string   `json:"uplink"`
	UplinkSource string   `json:"uplinkSource"`
	NextHops     []string `json:"nextHops"`
}

// DefinitionDigest binds the rule compiler, registry data, dynamic host
// exclusion contract and restart policy to review and signed receipts. Changing
// enforcement requires changing this definition, and invalidates older reviews.
func DefinitionDigest() string {
	return digest(struct {
		Version  string
		Registry json.RawMessage
		Template string
		Ingress  string
	}{
		Owner + ";ipv4-only;public-dns;boot-restore-reviewed-snapshot;host-connected-prefixes;default-gateway-only;egress-echo-required;watcher-3s;egress-refresh-30s-max-age-90s;deny-all-nexthops;deny-all-nondefault-routes;ingress-only-replies-for-published-ports", registryJSON,
		Policy{Network: "definition", HomePrefixes: []string{"198.51.100.0/24"}, Gateway: "192.0.2.1", Uplink: "uplink"}.Script(),
		IngressPolicy("definition").Script(),
	})
}
func digest(v any) string {
	raw, _ := json.Marshal(v)
	s := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(s[:])
}
func (p Policy) Digest() string {
	return digest(struct{ Script, UplinkSource string }{p.Script(), p.UplinkSource})
}

// AllowsDestination is the address portion of the policy. IPv6 is deliberately
// denied entirely; IPv4-mapped IPv6 is not unwrapped into an IPv4 permission.
// The ingress-only variant allows no destination at all.
func (p Policy) AllowsDestination(addr netip.Addr) bool {
	if p.IngressOnly || !addr.Is4() || !addr.IsGlobalUnicast() {
		return false
	}
	if addr.String() == p.Gateway || addr.String() == "168.63.129.16" {
		return false
	}
	for _, hop := range p.NextHops {
		if addr.String() == hop {
			return false
		}
	}
	for _, row := range SpecialRanges() {
		if netip.MustParsePrefix(row.Prefix).Contains(addr) {
			return false
		}
	}
	for _, raw := range p.HomePrefixes {
		if netip.MustParsePrefix(raw).Contains(addr) {
			return false
		}
	}
	return true
}

// Rules exist before the bridge or container. Inet prerouting runs after
// conntrack but before DNAT; forward repeats destination checks after DNAT.
// Bridge filtering covers same-bridge peers without depending on br_netfilter.
// Only conntrack replies to admitted inbound requests may return to a private
// peer. The application cannot initiate even a connection to its ingress router.
const EgressSet = "household_egress_v4"

// IngressPolicy is the complete static policy of a firewalled application
// without outbound Internet. It depends on the network name only.
func IngressPolicy(network string) Policy { return Policy{Network: network, IngressOnly: true} }

func (p Policy) Script() string {
	t, b, m := Table(p.Network), Bridge(p.Network), MAC(p.Network)
	var s strings.Builder
	fmt.Fprintf(&s, "table inet %s {\n comment %q\n", t, Owner+":"+p.Network)
	if p.IngressOnly {
		// Without outbound Internet every packet the bridge sources is dropped
		// before NAT, in forwarding and towards the host, except conntrack
		// replies of the exact container to a connection made to it: Docker's
		// published-port DNAT or proxy and the ingress router. No destination
		// is ever granted, so no host facts take part.
		for _, chain := range []struct{ name, hook string }{{"before_nat", "prerouting"}, {"after_nat", "forward"}, {"host", "input"}} {
			prefix := fmt.Sprintf("iifname %q ", b)
			fmt.Fprintf(&s, " chain %s { type filter hook %s priority -150; policy accept;\n", chain.name, chain.hook)
			fmt.Fprintf(&s, " %smeta nfproto ipv6 drop\n %sether saddr %s ct direction reply accept\n %sdrop\n }\n", prefix, prefix, m, prefix)
		}
		s.WriteString("}\n")
		p.bridgeTable(&s)
		return s.String()
	}
	fmt.Fprintf(&s, " set %s { type ipv4_addr; }\n", EgressSet)
	for _, chain := range []struct {
		name, hook string
		priority   int
	}{{"before_nat", "prerouting", -150}, {"after_nat", "forward", -150}, {"host", "input", -150}} {
		fmt.Fprintf(&s, " chain %s { type filter hook %s priority %d; policy accept;\n", chain.name, chain.hook, chain.priority)
		prefix := fmt.Sprintf("iifname %q ", b)
		fmt.Fprintf(&s, " %smeta nfproto ipv6 drop\n %sether saddr %s ct direction reply accept\n", prefix, prefix, m)
		if chain.name == "host" {
			fmt.Fprintf(&s, " %sdrop\n", prefix)
		} else {
			fmt.Fprintf(&s, " %sfib daddr type local drop\n %sip daddr @%s drop\n %sip daddr %s drop comment \"default gateway\"\n", prefix, prefix, EgressSet, prefix, p.Gateway)
			fmt.Fprintf(&s, " %sip daddr 168.63.129.16 drop comment \"provider internal service\"\n", prefix)
			for _, hop := range p.NextHops {
				fmt.Fprintf(&s, " %sip daddr %s drop comment \"host route next hop\"\n", prefix, hop)
			}
			for _, row := range SpecialRanges() {
				if netip.MustParsePrefix(row.Prefix).Addr().Is4() {
					fmt.Fprintf(&s, " %sip daddr %s drop comment %q\n", prefix, row.Prefix, row.Name)
				}
			}
			for _, subnet := range p.HomePrefixes {
				fmt.Fprintf(&s, " %sip daddr %s drop comment %q\n", prefix, subnet, "host-connected network")
			}
			if chain.name == "after_nat" {
				fmt.Fprintf(&s, " %soifname != %q drop\n %srt ip nexthop != %s drop\n", prefix, p.Uplink, prefix, p.Gateway)
			}
		}
		s.WriteString(" }\n")
	}
	s.WriteString("}\n")
	p.bridgeTable(&s)
	return s.String()
}

// bridgeTable is shared by both variants: no IPv6 on the bridge, and a
// same-bridge peer only reaches the application or receives its replies.
func (p Policy) bridgeTable(s *strings.Builder) {
	t, b, m := Table(p.Network), Bridge(p.Network), MAC(p.Network)
	fmt.Fprintf(s, "table bridge %s {\n comment %q\n", t, Owner+":"+p.Network)
	fmt.Fprintf(s, " chain ipv6 { type filter hook prerouting priority -300; policy accept;\n meta ibrname %q ether type ip6 drop\n }\n", b)
	fmt.Fprintf(s, " chain peers { type filter hook forward priority 0; policy accept;\n meta ibrname %q ether type ip ether daddr %s accept\n meta ibrname %q ether type ip ether saddr %s ct direction reply accept\n meta ibrname %q ether type ip drop\n }\n}\n", b, m, b, m, b)
}
