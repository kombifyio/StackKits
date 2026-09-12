package siteaddress

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
)

// lanDNSDiscoveryTarget is a TEST-NET-1 address (RFC 5737). Dialling it on UDP
// creates no traffic — the kernel only selects the route — which is what makes
// this a local fact rather than a network call. Its single purpose is to learn
// which of the host's addresses the site is reachable on.
const lanDNSDiscoveryTarget = "192.0.2.1:9"

// ErrLANAddressUndiscoverable reports that the host has no usable site address.
// The resolver zone is not written from a guess: a wrong record would send every
// LAN client to the wrong host, which is worse than an install that stops and
// says so.
var ErrLANAddressUndiscoverable = errors.New("localevidence: no site-reachable host address for the LAN resolver")

// DiscoverSiteAddress observes the local target address for inventory
// attestation and runtime DNS custody. Off-target compilation consumes the
// attested inventory value and must never call this observation itself.
func DiscoverSiteAddress() (netip.Addr, error) {
	conn, err := net.Dial("udp", lanDNSDiscoveryTarget)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("%w: %v", ErrLANAddressUndiscoverable, err)
	}
	defer func() { _ = conn.Close() }()

	local, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok || local == nil {
		return netip.Addr{}, ErrLANAddressUndiscoverable
	}
	address, ok := netip.AddrFromSlice(local.IP)
	if !ok {
		return netip.Addr{}, ErrLANAddressUndiscoverable
	}
	address = address.Unmap()
	// Loopback would make the zone answer correctly on the node and wrongly on
	// every other device, which is exactly the failure this resolver exists to
	// remove. Unspecified and multicast are never a host's own address.
	if !address.IsValid() || address.IsLoopback() || address.IsUnspecified() || address.IsMulticast() {
		return netip.Addr{}, ErrLANAddressUndiscoverable
	}
	return address, nil
}
