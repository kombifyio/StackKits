package dataprofile

// Egress guard of the Full Local profile (DATA-ARCHITECTURE.md section 5.4.2,
// D-2026-10-21-12): under loc=local nothing leaves the homelab, so a guarded
// connection is checked against the homelab address ranges before it is dialed
// and the connected peer is checked again after.
//
// Scope. EnforceEgress guards two process-wide defaults: http.DefaultTransport
// (so every client that uses it, including http.DefaultClient) and
// net.DefaultResolver. Anything with a transport, dialer or resolver of its own
// (a websocket or gRPC dialer, a database driver, an exporter, a raw net.Dial)
// is not covered until its owner passes it through GuardTransport or GuardDial.
//
// The guard decides on addresses, never on names: a name is resolved first, by
// a resolver that only talks to homelab nameservers, every answer must be a
// homelab address, and the connection goes to the checked address, so a second
// lookup cannot redirect it. A homelab nameserver may itself forward a name
// upstream; that is the DNS of the operator, outside this guard. Proxies are
// dropped, since a proxy would carry the request out of the homelab under a
// homelab dial address. Redirects need no special case: each redirected request
// dials through the same guard. Unix sockets stay allowed because they never
// leave the host.
//
// Peer check. After a TCP or UDP dial the guard verifies the address the
// connection actually reached. The connection returned by the wrapped dial
// function must therefore report it through RemoteAddr: a *net.TCPAddr, a
// *net.UDPAddr, or an address whose String is "ip:port". A connection whose
// peer cannot be verified is closed and refused (fail closed); only a dial on a
// unix network is exempt.
//
// Audit log. Every dial attempt is written through the process's slog.Default
// as one content-free record (message "egress"): decision allow or deny, kind
// connect, dns or unix, network, host, peer address, port, outcome (connected,
// failed, refused or unverified_peer) and reason. For an allowed attempt peer is
// the address that attempt dialed, confirmed from the connection when it
// connected. The record never carries a URL, query, header, credential, payload
// or socket path. The guard does not choose where slog.Default writes: a process
// under Full Local must install a local handler for it (and no telemetry
// handler), as the consumer owns that sink.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync/atomic"
)

// ReasonFullLocal names a Full Local refusal in logs, gates and denial envelopes.
const ReasonFullLocal = "data_profile_full_local"

// metadataEndpoints is an explicit, non-exhaustive list of documented
// instance-metadata and credential services that the address ranges below would
// otherwise accept (link-local or unique-local), although they belong to a cloud
// provider and never to the homelab owner's application. It holds the AWS
// addresses only: EC2 instance metadata (169.254.169.254, fd00:ec2::254), ECS
// task metadata and credentials (169.254.170.2) and the EKS Pod Identity Agent
// (169.254.170.23, fd00:ec2::23). It is not a catalog of every provider's
// metadata service; an address is added only with an official source.
var metadataEndpoints = []netip.Addr{
	netip.MustParseAddr("169.254.169.254"),
	netip.MustParseAddr("169.254.170.2"),
	netip.MustParseAddr("169.254.170.23"),
	netip.MustParseAddr("fd00:ec2::254"),
	netip.MustParseAddr("fd00:ec2::23"),
}

// localPrefixes are the address ranges of the homelab: loopback (IPv4 127.0.0.0/8,
// IPv6 ::1), private (RFC 1918, RFC 4193) and link-local unicast (IPv4
// 169.254.0.0/16, IPv6 fe80::/10). They are the single authority of LocalAddr and
// LocalDestinationPolicy and match, range for range, what the netip classifiers
// IsLoopback, IsPrivate and IsLinkLocalUnicast accept. The shared address space
// 100.64.0.0/10 (RFC 6598) is not included.
var localPrefixes = []netip.Prefix{
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("::1/128"),
	netip.MustParsePrefix("fc00::/7"),
	netip.MustParsePrefix("fe80::/10"),
}

// LocalDestinationPolicy returns the homelab destination policy that LocalAddr
// enforces: prefixes are the accepted address ranges and excluded are the
// individual addresses inside them that are refused (the listed provider
// metadata endpoints). An address is local when a prefix contains it and
// excluded does not list it; a consumer must compare in the same normal form as
// LocalAddr, that is IPv4-mapped IPv6 addresses unmapped to IPv4 and the IPv6
// zone dropped. The slices are copies, so a caller may modify them freely.
func LocalDestinationPolicy() (prefixes []netip.Prefix, excluded []netip.Addr) {
	return append([]netip.Prefix(nil), localPrefixes...), append([]netip.Addr(nil), metadataEndpoints...)
}

// LocalAddr reports whether addr stays inside the homelab: loopback, private
// (RFC 1918, RFC 4193) or link-local, except the listed provider metadata
// endpoints. The shared address space 100.64.0.0/10 (RFC 6598) is not included:
// it is assigned to provider carrier networks, not to a homelab, and no standard
// admits it. An IPv4-mapped IPv6 address is judged as its IPv4 address and an
// IPv6 zone is ignored, so fd00:ec2::254%eth0 is the metadata endpoint
// fd00:ec2::254 and not a distinct, accepted address.
func LocalAddr(addr netip.Addr) bool {
	addr = addr.Unmap().WithZone("")
	for _, endpoint := range metadataEndpoints {
		if addr == endpoint {
			return false
		}
	}
	for _, prefix := range localPrefixes {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

// DeniedError is the error of a connection Full Local refused.
type DeniedError struct {
	Host string
}

func (e *DeniedError) Error() string {
	return fmt.Sprintf("%s: %s is outside the homelab (%s=%s)", ReasonFullLocal, e.Host, BootEnvVar, FullLocal)
}

// IsDenied reports whether err is a Full Local refusal. Besides a *DeniedError
// it recognizes the refusal of a public nameserver by LocalResolver, which the
// Go resolver reports as a *net.DNSError with the cause flattened to text: the
// error counts only if its text is exactly the text of this guard's own denial
// for the nameserver the error names. That is the machine reason the guard
// itself produced, not a reading of arbitrary error text, so an ordinary lookup
// failure (not found, timeout, a public server in a Standard process) is never
// classified as a refusal, and there is no shared state between lookups.
func IsDenied(err error) bool {
	var denied *DeniedError
	if errors.As(err, &denied) {
		return true
	}
	_, ok := nameserverDenial(err)
	return ok
}

// nameserverDenial returns the denial that a net.DNSError carries when
// LocalResolver refused the nameserver it names.
func nameserverDenial(err error) (*DeniedError, bool) {
	var dnsErr *net.DNSError
	if !errors.As(err, &dnsErr) || dnsErr.Server == "" {
		return nil, false
	}
	host, _, splitErr := net.SplitHostPort(dnsErr.Server)
	if splitErr != nil {
		host = dnsErr.Server
	}
	denied := &DeniedError{Host: host}
	return denied, dnsErr.Err == denied.Error()
}

// Egress decisions and outcomes, as written to the audit log.
const (
	decisionAllow = "allow"
	decisionDeny  = "deny"
	kindConnect   = "connect"
	kindDNS       = "dns"
	kindUnix      = "unix"
	reasonHomelab = "homelab"

	outcomeConnected  = "connected"
	outcomeFailed     = "failed"
	outcomeRefused    = "refused"
	outcomeUnverified = "unverified_peer"
)

// logEgress writes one content-free record. Fields are fixed so no caller can
// add a URL, header or path to it.
func logEgress(decision, kind, network, host, peer, port, outcome, reason string) {
	attrs := []any{"decision", decision, "kind", kind, "network", network, "host", host, "peer", peer, "port", port, "outcome", outcome, "reason", reason}
	if decision == decisionDeny {
		slog.Warn("egress", attrs...)
		return
	}
	slog.Info("egress", attrs...)
}

func outcomeOf(err error) string {
	if err != nil {
		return outcomeFailed
	}
	return outcomeConnected
}

func deny(kind, network, host, peer, port, outcome string) error {
	logEgress(decisionDeny, kind, network, host, peer, port, outcome, ReasonFullLocal)
	return &DeniedError{Host: host}
}

// DialFunc is the signature of net.Dialer.DialContext. It is an alias so named
// dial types of other packages (for example pgconn.DialFunc) convert implicitly.
type DialFunc = func(ctx context.Context, network, address string) (net.Conn, error)

func isUnix(network string) bool { return strings.HasPrefix(network, "unix") }

// GuardDial wraps next (a plain net.Dialer when nil) so that it only connects
// to homelab addresses. See the file comment for the rules, including what the
// connection returned by next must report as its peer.
func GuardDial(next DialFunc) DialFunc {
	if next == nil {
		next = (&net.Dialer{}).DialContext
	}
	resolver := LocalResolver()
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		if isUnix(network) {
			conn, err := next(ctx, network, address)
			logEgress(decisionAllow, kindUnix, network, "", "", "", outcomeOf(err), reasonHomelab) // the socket path is not logged
			return conn, err
		}
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		var targets []netip.Addr
		if addr, parseErr := netip.ParseAddr(host); parseErr == nil {
			targets = []netip.Addr{addr}
		} else if targets, err = resolver.LookupNetIP(ctx, "ip", host); err != nil {
			if denied, ok := nameserverDenial(err); ok {
				return nil, denied // already recorded by the resolver
			}
			return nil, err
		}
		if len(targets) == 0 {
			return nil, deny(kindConnect, network, host, "", port, outcomeRefused)
		}
		for _, addr := range targets {
			if !LocalAddr(addr) {
				return nil, deny(kindConnect, network, host, addr.Unmap().String(), port, outcomeRefused)
			}
		}
		var lastErr error
		for _, addr := range targets {
			dialed := addr.Unmap().String()
			conn, dialErr := next(ctx, network, net.JoinHostPort(dialed, port))
			if dialErr != nil {
				logEgress(decisionAllow, kindConnect, network, host, dialed, port, outcomeFailed, reasonHomelab)
				lastErr = dialErr
				continue
			}
			peer, ok := peerAddr(conn.RemoteAddr())
			if !ok {
				_ = conn.Close()
				return nil, deny(kindConnect, network, host, dialed, port, outcomeUnverified)
			}
			if !LocalAddr(peer) {
				_ = conn.Close()
				return nil, deny(kindConnect, network, host, peer.String(), port, outcomeRefused)
			}
			logEgress(decisionAllow, kindConnect, network, host, peer.String(), port, outcomeConnected, reasonHomelab)
			return conn, nil
		}
		return nil, lastErr
	}
}

// peerAddr extracts the IP address of a connected TCP or UDP peer. It accepts
// the standard address types and any net.Addr whose String is "ip:port"; a nil,
// unix or otherwise unparsable address cannot be verified.
func peerAddr(a net.Addr) (netip.Addr, bool) {
	switch v := a.(type) {
	case nil:
		return netip.Addr{}, false
	case *net.TCPAddr:
		return validAddr(v.AddrPort().Addr())
	case *net.UDPAddr:
		return validAddr(v.AddrPort().Addr())
	}
	if strings.HasPrefix(a.Network(), "unix") {
		return netip.Addr{}, false
	}
	ap, err := netip.ParseAddrPort(a.String())
	if err != nil {
		return netip.Addr{}, false
	}
	return validAddr(ap.Addr())
}

func validAddr(addr netip.Addr) (netip.Addr, bool) { return addr.Unmap(), addr.IsValid() }

// LocalResolver returns a resolver that only asks homelab nameservers, so a
// lookup cannot reach a public DNS server. It forces the Go resolver, whose
// nameserver dial is the only hook for this check. A refused nameserver makes
// the lookup fail with a *net.DNSError that IsDenied recognizes.
func LocalResolver() *net.Resolver {
	var dialer net.Dialer
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			addr, err := netip.ParseAddr(host)
			if err != nil || !LocalAddr(addr) {
				return nil, deny(kindDNS, network, host, host, port, outcomeRefused)
			}
			conn, dialErr := dialer.DialContext(ctx, network, address)
			logEgress(decisionAllow, kindDNS, network, host, addr.Unmap().String(), port, outcomeOf(dialErr), reasonHomelab)
			return conn, dialErr
		},
	}
}

// GuardTransport binds t to the homelab: connections go through GuardDial and
// proxies are dropped. Use it for a transport a client creates for itself.
func GuardTransport(t *http.Transport) {
	t.Proxy = nil
	t.DialTLSContext = nil
	t.DialTLS = nil //nolint:staticcheck // deprecated hook, cleared so it cannot bypass the guard
	t.Dial = nil    //nolint:staticcheck // deprecated hook, cleared so it cannot bypass the guard
	t.DialContext = GuardDial(t.DialContext)
	t.CloseIdleConnections()
}

var egressEnforced atomic.Bool

// EnforceEgress binds http.DefaultTransport and net.DefaultResolver to the
// homelab when p is Full Local, and changes nothing for any other valid profile;
// an invalid profile is an error, never a no-op. Call it once at boot, before
// any client or telemetry is created. The shared transport is changed in place,
// so it stays an *http.Transport that can be cloned and clients that captured it
// earlier are guarded as well. Clients with a transport, dialer or resolver of
// their own need GuardTransport or GuardDial.
func (p Profile) EnforceEgress() error {
	if err := p.Validate(); err != nil {
		return err
	}
	if !p.Local() || !egressEnforced.CompareAndSwap(false, true) {
		return nil
	}
	shared, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		egressEnforced.Store(false)
		return fmt.Errorf("%s: the shared HTTP transport is %T, not *http.Transport, and cannot be guarded", ReasonFullLocal, http.DefaultTransport)
	}
	GuardTransport(shared)
	net.DefaultResolver = LocalResolver()
	return nil
}
