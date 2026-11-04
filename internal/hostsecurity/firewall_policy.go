package hostsecurity

import (
	"fmt"
	"net/netip"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// Names of the firewall tables. The home table is owned by this package; the
// cloud table is owned by the Cloud host-security executor, which this package
// only observes.
const (
	HomeFirewallTable  = "stackkits_home_host_security"
	HomeFirewallChain  = "stackkits_home_host_base"
	CloudFirewallTable = "stackkits_cloud_host_security"
)

// Packet is the minimal description of an inbound packet the firewall policy
// can be asked about. It is the test seam that lets a caller ask "does this
// policy still admit the management path" without parsing rule text.
type Packet struct {
	Source      netip.Addr
	Interface   string
	Protocol    string // tcp, udp or icmp
	Port        int    // destination port
	SourcePort  int
	Established bool
	Invalid     bool
}

// privateSourcesV4 and privateSourcesV6 are the address ranges a home LAN uses.
// 100.64.0.0/10 is deliberately absent: ISP carrier-grade NAT shares it with
// strangers. Overlay networks are trusted by interface instead.
var (
	privateSourcesV4 = []netip.Prefix{
		netip.MustParsePrefix("10.0.0.0/8"),
		netip.MustParsePrefix("169.254.0.0/16"),
		netip.MustParsePrefix("172.16.0.0/12"),
		netip.MustParsePrefix("192.168.0.0/16"),
	}
	privateSourcesV6 = []netip.Prefix{
		netip.MustParsePrefix("fc00::/7"),
		netip.MustParsePrefix("fe80::/10"),
	}
	// overlayInterfaces are the interface name patterns of common overlay
	// networks (Tailscale, WireGuard, NetBird, ZeroTier, Nebula). Traffic that
	// arrives on one of them has already been authenticated by the overlay.
	overlayInterfaces = []string{"tailscale0", "wg*", "wt0", "zt*", "nebula*"}
	// bridgeInterfaces carry container-to-host traffic (Docker, Podman).
	bridgeInterfaces = []string{"docker0", "br-*", "podman*"}
)

// FirewallPolicy is the desired inbound policy of a home host. One model
// renders the nftables ruleset and answers Admits, so the text the host loads
// and the decision a test asks about cannot drift apart.
type FirewallPolicy struct {
	// ContainerOnly leaves the host input policy untouched when only the
	// plan-owned cohort's destination policy has been installed.
	ContainerOnly bool                         `json:"container_only,omitempty"`
	Containers    []ContainerDestinationPolicy `json:"container_destinations,omitempty"`
	// SSHPorts are the ports sshd listens on. They are never closed to the
	// management path.
	SSHPorts []int
	// Peers are the client addresses of SSH sessions in use when the policy was
	// built; they keep reaching sshd after the policy loads.
	Peers []netip.Addr
	// ManagementSources are owner-configured networks that may always reach
	// sshd (a jump host, an office network).
	ManagementSources []netip.Prefix
	// DeclaredOnly restricts LAN, overlay and container traffic to declared
	// service ports. Without declared-service authority the policy trusts
	// those sources on every port and drops everything else.
	DeclaredOnly bool
	// DeclaredTCP and DeclaredUDP are the declared service ports.
	DeclaredTCP []int
	DeclaredUDP []int
	// RemoteConnectorIsolation confines the relay and tunnel connector bridge
	// to the remote listener: it may reach the internet and its own bridge,
	// never the host or a private address (homelab identity target §7).
	RemoteConnectorIsolation bool `json:"remote_connector_isolation,omitempty"`
	// AddOnPublicTCP and AddOnPublicUDP are the ports an add-on the Owner
	// switched on serves to every source, such as the VPN add-on's WireGuard
	// endpoint or peer service, which a router forwards from the internet.
	// They are set and cleared only by that add-on (SetAddOnPublicListeners)
	// and kept across baseline repairs.
	AddOnPublicTCP []int `json:"add_on_public_tcp,omitempty"`
	AddOnPublicUDP []int `json:"add_on_public_udp,omitempty"`
}

// The Basement core's connector network and its stable Linux bridge. The
// bridge holds the remote router and the connectors, nothing else.
const (
	RemoteConnectorNetwork = "stackkit-remote-connector"
	RemoteConnectorBridge  = "skremote0"
	remoteConnectorChain   = "stackkits_home_remote_connector"
)

// Destinations the connector bridge may never forward to: the host's and
// every private, shared, link-local, loopback, multicast or reserved range.
var (
	remoteConnectorDeniedV4 = []netip.Prefix{
		netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("10.0.0.0/8"),
		netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("127.0.0.0/8"),
		netip.MustParsePrefix("169.254.0.0/16"), netip.MustParsePrefix("172.16.0.0/12"),
		netip.MustParsePrefix("192.168.0.0/16"), netip.MustParsePrefix("224.0.0.0/3"),
	}
	remoteConnectorDeniedV6 = []netip.Prefix{
		netip.MustParsePrefix("::1/128"), netip.MustParsePrefix("fc00::/7"),
		netip.MustParsePrefix("fe80::/10"), netip.MustParsePrefix("ff00::/8"),
	}
)

// renderRemoteConnectorChain is the forward hook of the connector bridge. It
// runs before the container engine's own chains: a drop here is final, an
// accept lets the engine's rules decide as before. A destination-translated
// packet is a published host port (the LAN router, the enrollment listener),
// whatever address range the engine's pool uses, so it is dropped first.
func renderRemoteConnectorChain() string {
	selector := "\t\tiifname \"" + RemoteConnectorBridge + "\""
	return strings.Join([]string{
		"\tchain " + remoteConnectorChain + " {",
		"\t\ttype filter hook forward priority filter - 10; policy accept;",
		selector + " ct status dnat counter drop",
		selector + " oifname \"" + RemoteConnectorBridge + "\" accept",
		selector + " ip daddr " + prefixSet(remoteConnectorDeniedV4) + " counter drop",
		selector + " ip6 daddr " + prefixSet(remoteConnectorDeniedV6) + " counter drop",
		"\t}",
	}, "\n")
}

type firewallRule struct {
	text    string
	accepts bool
	matches func(Packet) bool
}

func (p FirewallPolicy) normalized() FirewallPolicy {
	p.SSHPorts = sortedUniquePorts(p.SSHPorts)
	p.DeclaredTCP = sortedUniquePorts(p.DeclaredTCP)
	p.DeclaredUDP = sortedUniquePorts(p.DeclaredUDP)
	p.AddOnPublicTCP = sortedUniquePorts(p.AddOnPublicTCP)
	p.AddOnPublicUDP = sortedUniquePorts(p.AddOnPublicUDP)
	peers := make([]netip.Addr, 0, len(p.Peers))
	for _, peer := range p.Peers {
		peers = append(peers, peer.Unmap())
	}
	slices.SortFunc(peers, netip.Addr.Compare)
	p.Peers = slices.Compact(peers)
	sources := append([]netip.Prefix(nil), p.ManagementSources...)
	slices.SortFunc(sources, comparePrefix)
	p.ManagementSources = slices.Compact(sources)
	return p
}

func comparePrefix(a, b netip.Prefix) int {
	if c := a.Addr().Compare(b.Addr()); c != 0 {
		return c
	}
	return a.Bits() - b.Bits()
}

func sortedUniquePorts(ports []int) []int {
	out := make([]int, 0, len(ports))
	for _, port := range ports {
		if port >= 1 && port <= 65535 {
			out = append(out, port)
		}
	}
	sort.Ints(out)
	return slices.Compact(out)
}

// Validate refuses a policy that could lock the host away from its owner.
func (p FirewallPolicy) Validate() error {
	stacks := map[string]bool{}
	for _, scope := range p.Containers {
		if err := scope.Validate(); err != nil {
			return err
		}
		if stacks[scope.StackID] {
			return fmt.Errorf("duplicate container policy for %s", scope.StackID)
		}
		stacks[scope.StackID] = true
	}
	for _, source := range p.ManagementSources {
		if !source.IsValid() || source.Bits() == 0 {
			return fmt.Errorf("management source %q would admit every address", source)
		}
	}
	ports := slices.Concat(p.SSHPorts, p.DeclaredTCP, p.DeclaredUDP, p.AddOnPublicTCP, p.AddOnPublicUDP)
	for _, port := range ports {
		if port < 1 || port > 65535 {
			return fmt.Errorf("port %d is outside 1-65535", port)
		}
	}
	return nil
}

// Admits reports whether the policy lets the packet reach the host.
func (p FirewallPolicy) Admits(packet Packet) bool {
	packet.Source = packet.Source.Unmap()
	for _, rule := range p.normalized().rules() {
		if rule.matches(packet) {
			return rule.accepts
		}
	}
	return false
}

func matchAny(prefixes []netip.Prefix, source netip.Addr) bool {
	for _, prefix := range prefixes {
		if prefix.Contains(source) {
			return true
		}
	}
	return false
}

func interfaceMatches(patterns []string, name string) bool {
	for _, pattern := range patterns {
		if prefix, wildcard := strings.CutSuffix(pattern, "*"); wildcard {
			if strings.HasPrefix(name, prefix) {
				return true
			}
		} else if name == pattern {
			return true
		}
	}
	return false
}

func portSet(ports []int) string {
	parts := make([]string, len(ports))
	for i, port := range ports {
		parts[i] = strconv.Itoa(port)
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return "{ " + strings.Join(parts, ", ") + " }"
}

// prefixSet spells prefixes the way nft lists them back (a single address has
// no mask, a single element has no braces), so the rendered text and a live
// listing normalize to the same bytes.
func prefixSet(prefixes []netip.Prefix) string {
	parts := make([]string, len(prefixes))
	for i, prefix := range prefixes {
		if prefix.IsSingleIP() {
			parts[i] = prefix.Addr().String()
		} else {
			parts[i] = prefix.String()
		}
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return "{ " + strings.Join(parts, ", ") + " }"
}

func splitFamilies(prefixes []netip.Prefix) (v4, v6 []netip.Prefix) {
	for _, prefix := range prefixes {
		if prefix.Addr().Is4() {
			v4 = append(v4, prefix)
		} else {
			v6 = append(v6, prefix)
		}
	}
	return v4, v6
}

func containsPort(ports []int, port int) bool { return slices.Contains(ports, port) }

// rules returns the ordered ruleset. Order is the nftables evaluation order.
//
//nolint:gocyclo // One ordered list; each optional rule family is a guard, not a branch of logic.
func (p FirewallPolicy) rules() []firewallRule {
	var rules []firewallRule
	add := func(text string, accepts bool, matches func(Packet) bool) {
		rules = append(rules, firewallRule{text: text, accepts: accepts, matches: matches})
	}
	add("ct state established,related accept", true, func(k Packet) bool { return k.Established })
	add("ct state invalid drop", false, func(k Packet) bool { return k.Invalid })
	add(`iif "lo" accept`, true, func(k Packet) bool { return k.Interface == "lo" })
	if p.RemoteConnectorIsolation {
		// A relay or tunnel connector reaches the remote listener on its own
		// bridge and nothing on the host, so the LAN listener stays home-only.
		add(`iifname "`+RemoteConnectorBridge+`" drop`, false, func(k Packet) bool { return k.Interface == RemoteConnectorBridge })
	}
	// ICMP carries path MTU discovery and IPv6 neighbor discovery; dropping it
	// breaks connectivity without hiding the host.
	add("meta l4proto { icmp, ipv6-icmp } accept", true, func(k Packet) bool { return k.Protocol == "icmp" })
	add("udp sport 67 udp dport 68 accept", true, func(k Packet) bool {
		return k.Protocol == "udp" && k.SourcePort == 67 && k.Port == 68
	})

	sshAllowed := func(k Packet) bool { return k.Protocol == "tcp" && containsPort(p.SSHPorts, k.Port) }
	mgmtV4, mgmtV6 := splitFamilies(p.ManagementSources)
	hasSSH := len(p.SSHPorts) > 0
	if hasSSH && len(mgmtV4) > 0 {
		add("ip saddr "+prefixSet(mgmtV4)+" tcp dport "+portSet(p.SSHPorts)+" accept", true,
			func(k Packet) bool { return sshAllowed(k) && matchAny(mgmtV4, k.Source) })
	}
	if hasSSH && len(mgmtV6) > 0 {
		add("ip6 saddr "+prefixSet(mgmtV6)+" tcp dport "+portSet(p.SSHPorts)+" accept", true,
			func(k Packet) bool { return sshAllowed(k) && matchAny(mgmtV6, k.Source) })
	}
	var peersV4, peersV6 []netip.Prefix
	for _, peer := range p.Peers {
		prefix := netip.PrefixFrom(peer, peer.BitLen())
		if peer.Is4() {
			peersV4 = append(peersV4, prefix)
		} else {
			peersV6 = append(peersV6, prefix)
		}
	}
	if hasSSH && len(peersV4) > 0 {
		add("ip saddr "+prefixSet(peersV4)+" tcp dport "+portSet(p.SSHPorts)+" accept", true,
			func(k Packet) bool { return sshAllowed(k) && matchAny(peersV4, k.Source) })
	}
	if hasSSH && len(peersV6) > 0 {
		add("ip6 saddr "+prefixSet(peersV6)+" tcp dport "+portSet(p.SSHPorts)+" accept", true,
			func(k Packet) bool { return sshAllowed(k) && matchAny(peersV6, k.Source) })
	}
	// An Owner-enabled add-on's public ports (the VPN add-on's endpoint and
	// peer service) are reachable from every source; each authenticates its
	// own clients.
	if len(p.AddOnPublicTCP) > 0 {
		ports := p.AddOnPublicTCP
		add("tcp dport "+portSet(ports)+" accept", true, func(k Packet) bool { return k.Protocol == "tcp" && containsPort(ports, k.Port) })
	}
	if len(p.AddOnPublicUDP) > 0 {
		ports := p.AddOnPublicUDP
		add("udp dport "+portSet(ports)+" accept", true, func(k Packet) bool { return k.Protocol == "udp" && containsPort(ports, k.Port) })
	}

	tcpPorts := sortedUniquePorts(append(slices.Clone(p.DeclaredTCP), p.SSHPorts...))
	udpPorts := p.DeclaredUDP
	// trustedInterface adds the rules for an interface the host trusts (overlay
	// or container bridge): everything, or only declared services.
	trustedInterface := func(name string) {
		selector := `iifname "` + name + `"`
		matchesInterface := func(k Packet) bool { return interfaceMatches([]string{name}, k.Interface) }
		if !p.DeclaredOnly {
			add(selector+" accept", true, matchesInterface)
			return
		}
		add(selector+" tcp dport "+portSet(tcpPorts)+" accept", true,
			func(k Packet) bool {
				return matchesInterface(k) && k.Protocol == "tcp" && containsPort(tcpPorts, k.Port)
			})
		if len(udpPorts) > 0 {
			add(selector+" udp dport "+portSet(udpPorts)+" accept", true,
				func(k Packet) bool {
					return matchesInterface(k) && k.Protocol == "udp" && containsPort(udpPorts, k.Port)
				})
		}
	}
	for _, name := range bridgeInterfaces {
		// Container-to-host traffic (socket proxy, DNS) is local workload
		// traffic, never outside exposure.
		add(`iifname "`+name+`" accept`, true, func(k Packet) bool { return interfaceMatches([]string{name}, k.Interface) })
	}
	for _, name := range overlayInterfaces {
		trustedInterface(name)
	}

	for _, family := range []struct {
		selector string
		ranges   []netip.Prefix
	}{{"ip saddr", privateSourcesV4}, {"ip6 saddr", privateSourcesV6}} {
		ranges := family.ranges
		if !p.DeclaredOnly {
			add(family.selector+" "+prefixSet(ranges)+" accept", true, func(k Packet) bool { return matchAny(ranges, k.Source) })
			continue
		}
		add(family.selector+" "+prefixSet(ranges)+" tcp dport "+portSet(tcpPorts)+" accept", true,
			func(k Packet) bool {
				return matchAny(ranges, k.Source) && k.Protocol == "tcp" && containsPort(tcpPorts, k.Port)
			})
		if len(udpPorts) > 0 {
			add(family.selector+" "+prefixSet(ranges)+" udp dport "+portSet(udpPorts)+" accept", true,
				func(k Packet) bool {
					return matchAny(ranges, k.Source) && k.Protocol == "udp" && containsPort(udpPorts, k.Port)
				})
		}
	}
	return rules
}

// Render returns the nftables table text. It is one table with one input base
// chain whose policy is drop: anything no rule names is refused.
func (p FirewallPolicy) Render() string {
	p = p.normalized()
	lines := []string{"table inet " + HomeFirewallTable + " {"}
	if !p.ContainerOnly {
		lines = append(lines,
			"\tchain "+HomeFirewallChain+" {",
			"\t\ttype filter hook input priority filter; policy drop;",
		)
		for _, rule := range p.rules() {
			lines = append(lines, "\t\t"+rule.text)
		}
		lines = append(lines, "\t}")
	}
	if p.RemoteConnectorIsolation {
		lines = append(lines, renderRemoteConnectorChain())
	}
	for _, scope := range p.Containers {
		lines = append(lines, scope.renderChain())
	}
	lines = append(lines, "}")
	return strings.Join(lines, "\n") + "\n"
}

// RenderRuleset is the file nft loads: the create/delete preamble makes the
// load idempotent, and nft applies the whole file as one transaction.
func (p FirewallPolicy) RenderRuleset() string {
	return "table inet " + HomeFirewallTable + "\n" +
		"delete table inet " + HomeFirewallTable + "\n" +
		p.Render()
}
